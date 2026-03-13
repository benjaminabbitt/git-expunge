package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/manifest"
	"github.com/benjaminabbitt/git-expunge/internal/preview"
	"github.com/benjaminabbitt/git-expunge/internal/rewriter"
	"github.com/benjaminabbitt/git-expunge/internal/safety"
	"github.com/benjaminabbitt/git-expunge/internal/scanner"
	"github.com/benjaminabbitt/git-expunge/internal/ui/cli"
	"github.com/benjaminabbitt/git-expunge/internal/ui/tui"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var rootCmd = &cobra.Command{
	Use:   "git-expunge [repo-path]",
	Short: "Safely remove sensitive data and large files from Git history",
	Long: `git-expunge is a user-friendly tool for removing accidentally committed
secrets, API keys, binary files, and other sensitive data from your Git
repository history.

Unlike other tools, it prioritizes safety with full backup/restore capabilities
and provides multiple interfaces (TUI, CLI, and report-based) for reviewing
findings before making any destructive changes.

Run without arguments to launch the interactive TUI.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runUI,
}

var scanCmd = &cobra.Command{
	Use:   "scan [repo-path]",
	Short: "Scan repository for binaries and secrets",
	Long: `Scan a Git repository's entire history for accidentally committed
binaries and sensitive data (secrets, API keys, etc.).

Outputs a git-expunge-findings.json file that can be reviewed and edited before rewriting.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runScan,
}

func runScan(cmd *cobra.Command, args []string) error {
	// Get repo path
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}

	// Get flags
	scanSecrets, _ := cmd.Flags().GetBool("secrets")
	scanBinaries, _ := cmd.Flags().GetBool("binaries")
	sizeThresholdStr, _ := cmd.Flags().GetString("size-threshold")
	outputPath, _ := cmd.Flags().GetString("output")
	workers, _ := cmd.Flags().GetInt("workers")

	// If output not explicitly set, put manifest in repo root
	if outputPath == "./git-expunge-findings.json" {
		outputPath = filepath.Join(repoPath, "git-expunge-findings.json")
	}

	// Parse size threshold
	sizeThreshold, err := parseSize(sizeThresholdStr)
	if err != nil {
		return fmt.Errorf("invalid size-threshold: %w", err)
	}

	// Configure scanner with progress reporting
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	config := scanner.Config{
		ScanSecrets:   scanSecrets,
		ScanBinaries:  scanBinaries,
		SizeThreshold: sizeThreshold,
		Workers:       workers,
		ProgressFunc: func(blobsScanned, findingsCount int) {
			fmt.Printf("\rScanning... %d blobs processed, %d findings", blobsScanned, findingsCount)
		},
	}

	cmd.Printf("Scanning %s with %d workers...\n", repoPath, workers)

	// Run scan
	s := scanner.New(config)
	result, err := s.Scan(repoPath)
	fmt.Println() // Clear progress line
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Write manifest
	if err := manifest.WriteJSON(result, outputPath); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	// Summary
	binaryCount := 0
	secretCount := 0
	for _, f := range result {
		switch f.Type {
		case "binary":
			binaryCount++
		case "secret":
			secretCount++
		}
	}

	cmd.Printf("Found %d findings:\n", len(result))
	cmd.Printf("  - Binaries: %d\n", binaryCount)
	cmd.Printf("  - Secrets: %d\n", secretCount)
	cmd.Printf("Manifest written to: %s\n", outputPath)

	return nil
}

// parseSize parses a size string like "100KB" to bytes.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))

	multiplier := int64(1)
	if strings.HasSuffix(s, "KB") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "KB")
	} else if strings.HasSuffix(s, "MB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	} else if strings.HasSuffix(s, "GB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	} else if strings.HasSuffix(s, "B") {
		s = strings.TrimSuffix(s, "B")
	}

	val, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, err
	}

	return val * multiplier, nil
}

// getRepoSize calculates the total size of the .git directory
func getRepoSize(repoPath string) int64 {
	gitDir := filepath.Join(repoPath, ".git")

	// Check if it's a bare repo
	if _, err := os.Stat(filepath.Join(repoPath, "objects")); err == nil {
		gitDir = repoPath
	}

	var size int64
	filepath.Walk(gitDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue on error
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// formatBytes formats bytes as human-readable string
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate or read human-readable reports",
}

var reportGenerateCmd = &cobra.Command{
	Use:   "generate [repo-path]",
	Short: "Generate human-readable markdown report from manifest",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runReportGenerate,
}

func runReportGenerate(cmd *cobra.Command, args []string) error {
	// Get repo path
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}

	outputPath, _ := cmd.Flags().GetString("output")

	// Input manifest is in repo root
	inputPath := filepath.Join(repoPath, "git-expunge-findings.json")

	// Default output to repo root
	if outputPath == "./manifest.md" {
		outputPath = filepath.Join(repoPath, "manifest.md")
	}

	// Read manifest
	m, err := manifest.ReadJSON(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Create output file
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer f.Close()

	// Generate report with previews
	gen := manifest.NewReportGenerator(repoPath)

	// Find shared blobs to enhance the report with warnings
	var blobHashes []string
	for hash := range m {
		blobHashes = append(blobHashes, hash)
	}
	if len(blobHashes) > 0 {
		cmd.Println("Analyzing blob sharing across paths...")
		if sharedBlobs, err := scanner.FindAllPathsForBlobs(repoPath, blobHashes); err == nil {
			gen.SetSharedBlobs(sharedBlobs)
			// Count how many blobs are shared
			sharedCount := 0
			for _, paths := range sharedBlobs {
				if len(paths) > 1 {
					sharedCount++
				}
			}
			if sharedCount > 0 {
				cmd.Printf("Found %d blob(s) that appear at multiple paths.\n", sharedCount)
			}
		} else {
			cmd.Printf("Warning: could not analyze shared blobs: %v\n", err)
		}
	}

	if err := gen.Generate(m, f); err != nil {
		return fmt.Errorf("failed to generate report: %w", err)
	}

	cmd.Printf("Report generated: %s\n", outputPath)
	cmd.Printf("Edit the file and check [x] boxes for items to purge.\n")
	cmd.Printf("Then run: git-expunge report read %s\n", outputPath)

	return nil
}

var reportReadCmd = &cobra.Command{
	Use:   "read [manifest.md]",
	Short: "Parse markdown report back to git-expunge-findings.json",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runReportRead,
}

func runReportRead(cmd *cobra.Command, args []string) error {
	// Get input path
	inputPath := "./manifest.md"
	if len(args) > 0 {
		inputPath = args[0]
	}

	outputPath, _ := cmd.Flags().GetString("output")

	// Open input file
	f, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open report: %w", err)
	}
	defer f.Close()

	// Parse report
	m, err := manifest.ParseReport(f)
	if err != nil {
		return fmt.Errorf("failed to parse report: %w", err)
	}

	// Write manifest
	if err := manifest.WriteJSON(m, outputPath); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	// Show summary
	purgeCount := m.PurgeCount()
	cmd.Printf("Manifest written: %s\n", outputPath)
	cmd.Printf("Total findings: %d, marked for purge: %d\n", len(m), purgeCount)

	if purgeCount > 0 {
		cmd.Printf("Next step: git-expunge rewrite --manifest %s\n", outputPath)
	}

	return nil
}

var uiCmd = &cobra.Command{
	Use:   "ui [repo-path]",
	Short: "Launch interactive TUI for scanning, reviewing, and rewriting",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runUI,
}

func runUI(cmd *cobra.Command, args []string) error {
	// Get repo path
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}

	// Manifest is in repo root
	manifestPath := filepath.Join(repoPath, "git-expunge-findings.json")

	// Get UI mode
	uiMode, _ := cmd.Flags().GetString("mode")

	// Read manifest if it exists, otherwise start with empty
	var m domain.Manifest
	if _, err := os.Stat(manifestPath); err == nil {
		m, err = manifest.ReadJSON(manifestPath)
		if err != nil {
			return fmt.Errorf("failed to read manifest: %w", err)
		}
	} else {
		m = domain.NewManifest()
	}

	var updatedManifest domain.Manifest

	// Determine UI mode
	useTUI := uiMode == "tui" || (uiMode == "" && term.IsTerminal(int(os.Stdin.Fd())))

	if useTUI && uiMode != "cli" {
		// Start in Browse mode if no manifest, Review mode if there are findings
		startMode := tui.ModeBrowse
		if len(m) > 0 {
			startMode = tui.ModeReview
		}

		// Run TUI
		result, saved, err := tui.Run(m, repoPath, startMode)
		if err != nil {
			return fmt.Errorf("TUI failed: %w", err)
		}
		updatedManifest = result

		if !saved {
			cmd.Println("Exited without saving.")
			return nil
		}
	} else {
		// Run CLI review
		if len(m) == 0 {
			cmd.Println("No findings in manifest. Run 'git-expunge scan' first or use TUI to browse files.")
			return nil
		}
		reviewer := cli.NewReviewer(m, repoPath)
		if err := reviewer.Run(); err != nil {
			return fmt.Errorf("review failed: %w", err)
		}
		updatedManifest = reviewer.GetManifest()
	}

	// Save updated manifest
	if err := manifest.WriteJSON(updatedManifest, manifestPath); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	cmd.Printf("\nManifest saved to: %s\n", manifestPath)

	purgeCount := updatedManifest.PurgeCount()
	if purgeCount > 0 {
		cmd.Printf("Next step: git-expunge rewrite %s --execute\n", repoPath)
	}

	return nil
}

var rewriteCmd = &cobra.Command{
	Use:   "rewrite [repo-path]",
	Short: "Rewrite repository history to remove selected findings",
	Long: `Rewrite the Git repository history to permanently remove blobs
marked for purging in the manifest.

By default, runs in dry-run mode. Use --execute to actually perform the rewrite.
A full backup archive is created before any destructive operation.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRewrite,
}

func runRewrite(cmd *cobra.Command, args []string) error {
	// Get repo path
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}

	// Get flags
	manifestPath, _ := cmd.Flags().GetString("manifest")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	execute, _ := cmd.Flags().GetBool("execute")
	backupDir, _ := cmd.Flags().GetString("backup-dir")
	skipBackup, _ := cmd.Flags().GetBool("skip-backup")

	// --execute overrides --dry-run
	if execute {
		dryRun = false
	}

	if manifestPath == "" {
		manifestPath = filepath.Join(repoPath, "git-expunge-findings.json")
	}

	// Read manifest
	m, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Get blobs to purge
	blobsToPurge := m.BlobsToPurge()
	if len(blobsToPurge) == 0 {
		cmd.Println("No items marked for purging. Use 'git-expunge review' or 'git-expunge report' to select items.")
		return nil
	}

	cmd.Printf("Found %d blobs marked for purging\n", len(blobsToPurge))

	// Check for shared blobs and filter to safe-only
	cmd.Println("Checking for shared blob content...")
	allPaths, err := scanner.FindAllPathsForBlobs(repoPath, blobsToPurge)
	if err != nil {
		cmd.Printf("Warning: could not check for shared blobs: %v\n", err)
	} else {
		// Filter to only blobs where ALL paths are marked for purge
		safeToPurge, skipped := m.SafeBlobsToPurge(allPaths)

		if len(skipped) > 0 {
			yellow := color.New(color.FgYellow, color.Bold)
			yellow.Fprintf(cmd.OutOrStderr(), "\n⚠️  PROTECTED: %d blob(s) skipped to protect unmarked paths\n", len(skipped))
			cmd.Println("These blobs appear at paths NOT marked for purge:")
			cmd.Println()

			for _, s := range skipped {
				cmd.Printf("  Blob: %s (marked via: %s)\n", s.BlobHash[:12], s.MarkedPath)
				cmd.Printf("  Protected paths (not marked for purge):\n")
				maxShow := 5
				for i, p := range s.UnmarkedPaths {
					if i >= maxShow {
						cmd.Printf("      ... and %d more\n", len(s.UnmarkedPaths)-maxShow)
						break
					}
					cmd.Printf("      - %s\n", p)
				}
				cmd.Println()
			}

			cmd.Printf("Proceeding with %d safe blob(s) instead of %d requested.\n", len(safeToPurge), len(blobsToPurge))
			cmd.Println("To purge shared blobs, mark ALL paths containing that content for purge.")
			cmd.Println()
		}

		// Use the filtered list
		blobsToPurge = safeToPurge

		if len(blobsToPurge) == 0 {
			cmd.Println("No blobs safe to purge after filtering shared content.")
			cmd.Println("All requested blobs appear at paths not marked for purge.")
			return nil
		}
	}

	// Create rewriter
	rw := rewriter.NewRewriter(repoPath)
	rw.SetDryRun(dryRun)

	if dryRun {
		cmd.Println("\n[DRY RUN] Analyzing what would be changed...")
	} else {
		cmd.Println("\n[EXECUTE] Rewriting repository history...")

		// Create backup unless skipped
		if !skipBackup {
			cmd.Println("Creating backup archive...")
			archive, err := safety.CreateBackup(repoPath, backupDir)
			if err != nil {
				return fmt.Errorf("failed to create backup: %w", err)
			}
			cmd.Printf("Backup created: %s\n", archive.ArchivePath)

			// Verify backup
			if err := safety.VerifyBackup(archive.ArchivePath); err != nil {
				return fmt.Errorf("backup verification failed: %w", err)
			}
			cmd.Println("Backup verified successfully.")
		} else {
			cmd.Println("WARNING: Skipping backup! This is dangerous.")
		}
	}

	// Measure size before rewrite
	var sizeBefore int64
	if !dryRun {
		sizeBefore = getRepoSize(repoPath)
	}

	// Run rewrite
	stats, err := rw.Rewrite(blobsToPurge)
	if err != nil {
		return fmt.Errorf("rewrite failed: %w", err)
	}

	// Measure size after rewrite
	var sizeAfter int64
	if !dryRun {
		sizeAfter = getRepoSize(repoPath)
	}

	// Show results
	cmd.Println("\nResults:")
	cmd.Printf("  Total blobs processed: %d\n", stats.TotalBlobs)
	cmd.Printf("  Blobs removed: %d\n", stats.ExcludedBlobs)
	cmd.Printf("  Commits modified: %d\n", stats.ModifiedCommits)

	if !dryRun && sizeBefore > 0 {
		cmd.Println("\nRepository size:")
		cmd.Printf("  Before: %s\n", formatBytes(sizeBefore))
		cmd.Printf("  After:  %s\n", formatBytes(sizeAfter))
		saved := sizeBefore - sizeAfter
		if saved > 0 {
			percent := float64(saved) / float64(sizeBefore) * 100
			cmd.Printf("  Saved:  %s (%.1f%%)\n", formatBytes(saved), percent)
		}
	}

	if dryRun {
		cmd.Println("\nThis was a dry run. To actually rewrite history, run:")
		cmd.Printf("  git-expunge rewrite --manifest %s --execute\n", manifestPath)
	} else {
		cmd.Println("\nRepository history has been rewritten.")
		cmd.Println("Run 'git-expunge verify' to confirm purged items are unreachable.")
	}

	return nil
}

var verifyCmd = &cobra.Command{
	Use:   "verify [repo-path]",
	Short: "Verify purged items are unreachable in repository",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runVerify,
}

func runVerify(cmd *cobra.Command, args []string) error {
	// Get repo path
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}

	manifestPath, _ := cmd.Flags().GetString("manifest")
	if manifestPath == "" {
		manifestPath = filepath.Join(repoPath, "git-expunge-findings.json")
	}

	// Read manifest
	m, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Get blobs that were supposed to be purged
	blobsToPurge := m.BlobsToPurge()
	if len(blobsToPurge) == 0 {
		cmd.Println("No items were marked for purging in manifest.")
		return nil
	}

	cmd.Printf("Verifying %d blobs are unreachable...\n", len(blobsToPurge))

	// Check each blob
	var stillReachable []string
	for _, hash := range blobsToPurge {
		// Use git cat-file to check if object exists and is reachable
		checkCmd := fmt.Sprintf("git -C %s cat-file -e %s 2>/dev/null", repoPath, hash)
		if err := runGitCommand(checkCmd); err == nil {
			stillReachable = append(stillReachable, hash)
		}
	}

	if len(stillReachable) == 0 {
		cmd.Println("\n✓ All purged blobs are unreachable. Rewrite was successful!")
		cmd.Println("\nNext steps:")
		cmd.Println("  1. Run 'git gc --aggressive --prune=now' to permanently delete objects")
		cmd.Println("  2. Force push to remote: 'git push --force --all'")
		cmd.Println("  3. Notify collaborators to re-clone the repository")
		return nil
	}

	cmd.Printf("\n✗ %d blobs are still reachable:\n", len(stillReachable))
	for _, hash := range stillReachable {
		// Find the path for this blob
		for _, f := range m {
			if f.BlobHash == hash {
				cmd.Printf("  - %s (%s)\n", f.Path, hash[:7])
				break
			}
		}
	}
	cmd.Println("\nThe rewrite may not have completed successfully.")

	return fmt.Errorf("%d blobs still reachable", len(stillReachable))
}

func runGitCommand(command string) error {
	// Simple shell execution for git commands
	cmd := exec.Command("sh", "-c", command)
	return cmd.Run()
}

var restoreCmd = &cobra.Command{
	Use:   "restore [repo-path]",
	Short: "Restore repository from backup archive",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runRestore,
}

var addCmd = &cobra.Command{
	Use:   "add <path>... [flags]",
	Short: "Add files or directories to the manifest for removal",
	Long: `Add files or directories to be removed from Git history.

Paths can be literal file paths or glob patterns:
  git-expunge add vendor/secrets.json .        # Exact path
  git-expunge add "*.env" .                    # Glob pattern (quote to prevent shell expansion)
  git-expunge add "vendor/**" .                # Double-star glob for recursive match

When using glob patterns, quote them to prevent shell expansion and allow
git-expunge to search through Git history for matching paths.

The last argument is the repository path (defaults to current directory).`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAdd,
}


func runRestore(cmd *cobra.Command, args []string) error {
	// Get destination path
	destPath := "."
	if len(args) > 0 {
		destPath = args[0]
	}

	archivePath, _ := cmd.Flags().GetString("archive")
	listBackups, _ := cmd.Flags().GetBool("list")

	// List mode
	if listBackups {
		backupDir := destPath
		if archivePath != "" {
			backupDir = archivePath
		}

		backups, err := safety.ListBackups(backupDir)
		if err != nil {
			return fmt.Errorf("failed to list backups: %w", err)
		}

		if len(backups) == 0 {
			cmd.Println("No backup archives found.")
			return nil
		}

		cmd.Println("Available backups:")
		for _, b := range backups {
			cmd.Printf("  %s\n", b)
		}
		return nil
	}

	// Restore mode
	if archivePath == "" {
		return fmt.Errorf("--archive flag is required")
	}

	// Verify archive first
	cmd.Printf("Verifying archive: %s\n", archivePath)
	if err := safety.VerifyBackup(archivePath); err != nil {
		return fmt.Errorf("archive verification failed: %w", err)
	}
	cmd.Println("Archive verified.")

	// Restore
	cmd.Printf("Restoring to: %s\n", destPath)
	if err := safety.RestoreBackup(archivePath, destPath); err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	cmd.Println("Repository restored successfully.")
	return nil
}

func runAdd(cmd *cobra.Command, args []string) error {
	// Parse arguments: paths are all args except the last one which is the repo path
	// Unless there's only one arg, in which case it's the path and repo is "."
	var patterns []string
	repoPath := "."

	if len(args) == 1 {
		patterns = args
	} else {
		// Check if last arg looks like a repo path (contains .git or is a directory)
		lastArg := args[len(args)-1]
		if isRepoPath(lastArg) {
			repoPath = lastArg
			patterns = args[:len(args)-1]
		} else {
			patterns = args
		}
	}

	manifestPath, _ := cmd.Flags().GetString("manifest")
	if manifestPath == "" {
		manifestPath = filepath.Join(repoPath, "git-expunge-findings.json")
	}

	// Load existing manifest or create new one
	var m domain.Manifest
	if _, err := os.Stat(manifestPath); err == nil {
		m, err = manifest.ReadJSON(manifestPath)
		if err != nil {
			return fmt.Errorf("failed to read existing manifest: %w", err)
		}
	} else {
		m = domain.NewManifest()
	}

	// Track if manifest existed before
	manifestExisted := false
	if _, err := os.Stat(manifestPath); err == nil {
		manifestExisted = true
	}

	// Process each pattern
	totalAdded := 0
	for _, pattern := range patterns {
		findings, err := scanner.FindBlobsForPath(repoPath, pattern)
		if err != nil {
			return fmt.Errorf("failed to find blobs for '%s': %w", pattern, err)
		}

		added := 0
		for _, f := range findings {
			// Check if already in manifest
			if _, exists := m[f.BlobHash]; !exists {
				m.Add(f)
				added++
			}
		}

		if len(findings) == 0 {
			cmd.Printf("No blobs found matching '%s'\n", pattern)
		} else {
			cmd.Printf("Added %d blob(s) for '%s' (%d already in manifest)\n",
				added, pattern, len(findings)-added)
		}
		totalAdded += added
	}

	// Only save manifest if we added something OR it already existed
	if totalAdded == 0 && !manifestExisted {
		cmd.Printf("\nNo blobs found. Manifest not created.\n")
		return nil
	}

	// Save manifest
	if err := manifest.WriteJSON(m, manifestPath); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	cmd.Printf("\nManifest saved: %s\n", manifestPath)
	cmd.Printf("Total: %d findings (%d marked for purge)\n", len(m), m.PurgeCount())

	if totalAdded > 0 {
		cmd.Printf("\nNext step: git-expunge rewrite %s --execute\n", repoPath)
	}

	return nil
}

var previewCmd = &cobra.Command{
	Use:   "preview <blob-hash> [repo-path]",
	Short: "Preview the content of a git blob",
	Long: `Preview the content of a git blob by its hash.

Shows text content for text files, or a hex dump for binary files.
Useful for inspecting blobs before deciding whether to purge them.

The blob hash can be found in the manifest or report files.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runPreview,
}

func runPreview(cmd *cobra.Command, args []string) error {
	// Get blob hash and optional repo path
	blobHash := args[0]
	repoPath := "."
	if len(args) > 1 {
		repoPath = args[1]
	}

	// Get flags
	lines, _ := cmd.Flags().GetInt("lines")
	raw, _ := cmd.Flags().GetBool("raw")

	// Create preview generator
	gen, err := preview.NewGenerator(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Generate preview
	p, err := gen.Generate(blobHash)
	if err != nil {
		return fmt.Errorf("failed to preview blob: %w", err)
	}

	// Output
	if raw {
		// Raw mode - just print content
		cmd.Println(p.Content)
	} else {
		// Formatted mode with headers
		if p.IsBinary {
			cmd.Println("Binary content (hex dump):")
			cmd.Println(strings.Repeat("─", 60))
		} else {
			cmd.Printf("Text content (blob %s):\n", blobHash[:12])
			cmd.Println(strings.Repeat("─", 60))
		}

		// Truncate to requested lines if specified
		contentLines := strings.Split(p.Content, "\n")
		if lines > 0 && len(contentLines) > lines {
			contentLines = contentLines[:lines]
			contentLines = append(contentLines, fmt.Sprintf("... (%d more lines)", len(strings.Split(p.Content, "\n"))-lines))
		}

		for _, line := range contentLines {
			cmd.Println(line)
		}
	}

	return nil
}

// isRepoPath checks if the given path looks like a repository path
func isRepoPath(path string) bool {
	// Check if it's a directory
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return false
	}

	// Check if it has a .git directory or is a bare repo
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(path, "objects")); err == nil {
		return true
	}

	// If it's "." or "..", treat it as a repo path
	if path == "." || path == ".." {
		return true
	}

	return false
}

func init() {
	// scan flags
	scanCmd.Flags().Bool("secrets", true, "Scan for secrets")
	scanCmd.Flags().Bool("binaries", true, "Scan for binaries")
	scanCmd.Flags().String("size-threshold", "100KB", "Minimum file size for binary detection")
	scanCmd.Flags().StringP("output", "o", "./git-expunge-findings.json", "Output manifest path")
	scanCmd.Flags().String("config", "", "Config file for custom rules")
	scanCmd.Flags().IntP("workers", "j", 0, "Number of parallel workers (default: number of CPUs)")

	// report subcommands
	reportGenerateCmd.Flags().StringP("output", "o", "./manifest.md", "Output path")
	reportReadCmd.Flags().StringP("output", "o", "./git-expunge-findings.json", "Output path")
	reportCmd.AddCommand(reportGenerateCmd)
	reportCmd.AddCommand(reportReadCmd)

	// ui flags (on both root and ui commands)
	rootCmd.Flags().String("mode", "", "UI mode: tui, cli (default: tui if terminal)")
	uiCmd.Flags().String("mode", "", "UI mode: tui, cli (default: tui if terminal)")

	// rewrite flags
	rewriteCmd.Flags().String("manifest", "", "Manifest file with purge selections")
	rewriteCmd.Flags().Bool("dry-run", true, "Show what would be done without making changes")
	rewriteCmd.Flags().Bool("execute", false, "Actually perform the rewrite")
	rewriteCmd.Flags().String("backup-dir", "", "Directory for archive backup (default: parent of repo)")
	rewriteCmd.Flags().Bool("skip-backup", false, "Skip backup (dangerous, requires confirmation)")

	// verify flags
	verifyCmd.Flags().String("manifest", "", "Manifest to check against")

	// restore flags
	restoreCmd.Flags().String("archive", "", "Path to backup archive")
	restoreCmd.Flags().Bool("list", false, "List available backups")

	// add flags
	addCmd.Flags().String("manifest", "", "Manifest file path (default: <repo>/git-expunge-findings.json)")

	// preview flags
	previewCmd.Flags().IntP("lines", "n", 0, "Limit output to N lines (0 = no limit)")
	previewCmd.Flags().Bool("raw", false, "Output raw content without headers")

	// add commands to root
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(uiCmd)
	rootCmd.AddCommand(rewriteCmd)
	rootCmd.AddCommand(verifyCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(previewCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
