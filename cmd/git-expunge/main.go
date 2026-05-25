package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/gitquery"
	"github.com/benjaminabbitt/git-expunge/internal/manifest"
	"github.com/benjaminabbitt/git-expunge/internal/preview"
	"github.com/benjaminabbitt/git-expunge/internal/report"
	"github.com/benjaminabbitt/git-expunge/internal/rewriter"
	"github.com/benjaminabbitt/git-expunge/internal/safety"
	"github.com/benjaminabbitt/git-expunge/internal/scanner"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// exitCodeError carries a process exit code out of a cobra RunE so main
// can map it to os.Exit. Used by `scan` to distinguish:
//
//	0 — success, nothing new added
//	1 — success, new findings added (CI signal for "something to look at")
//	2 — tool error (bad flag, git failure, missing repo, etc.)
type exitCodeError struct {
	code int
	msg  string
}

func (e *exitCodeError) Error() string { return e.msg }

// version is injected at build time via:
//
//	-ldflags="-X main.version=$(cat VERSION)"
//
// (see justfile `build` and `build-all` targets). Falls back to "dev"
// for `go build` / `go install` invocations that don't set it.
var version = "dev"

// Canonical filenames git-expunge writes to disk. Use these constants
// everywhere instead of typing the literal — keeps the manifest /
// sidecar / markdown report names consistent across help text, code
// paths, and tests.
const (
	// manifestFilename is the on-disk findings manifest, lives under
	// <git-common-dir>/git-expunge/.
	manifestFilename = "findings.json"
	// sidecarFilename is the post-rewrite audit record consulted by
	// `verify`, also under <git-common-dir>/git-expunge/.
	sidecarFilename = "last-purged.json"
	// reportFilename is the human-readable markdown report produced by
	// `report generate`, written to the repo root.
	reportFilename = "git-expunge.md"
	// stateDirname is the subdirectory under .git/ where all
	// git-expunge state lives.
	stateDirname = "git-expunge"
)

var rootCmd = &cobra.Command{
	Use:     "git-expunge",
	Short:   "Safely remove sensitive data and large files from Git history",
	Version: version,
	Long: `git-expunge is a user-friendly tool for removing accidentally committed
secrets, API keys, binary files, and other sensitive data from your Git
repository history.

Unlike other tools, it prioritizes safety with full backup/restore capabilities
and surfaces findings through plain-text commands you can pipe and grep.

Repo selection: every subcommand operates on the current working directory by
default. Use -C / --repo <path> to operate on a repository elsewhere (matching
git's -C convention):

  cd /path/to/repo
  git-expunge scan              # operates on .
  git-expunge list
  git-expunge expunge --execute
  git-expunge verify

  git-expunge -C /elsewhere/repo scan
  git-expunge --repo /elsewhere/repo list`,
}

// repoPathFlag returns the repo path resolved from --repo/-C (defaulting
// to "."). All subcommands route their repo-path through this helper —
// no subcommand takes a positional repo arg, so command-line parsing
// doesn't depend on filesystem state.
func repoPathFlag(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString("repo"); v != "" {
		return v
	}
	return "."
}

// detectorNames is the canonical set of detector identifiers accepted as
// positional args to `scan`.
var detectorNames = map[string]bool{
	"binaries":   true,
	"secrets":    true,
	"large":      true,
	"gitignored": true,
	"all":        true,
}

var scanCmd = &cobra.Command{
	Use:   "scan [detector...]",
	Short: "Detect content for the manifest",
	Long: `Scan history for content that should be removed. Each positional
argument names a detector.

Detectors:
  secrets    — gitleaks rules over blob content
  gitignored — paths matching the repository's current gitignore rules
  binaries   — MIME / magic-byte binary detection (any size)
  large      — size-only detection (any MIME); see --size
  all        — every detector above

With no detector named, runs the safe defaults (secrets, gitignored).
Multiple detectors share a single history walk where possible.

Use -C / --repo to operate on a repository other than the current
directory.

Exit codes:
  0  no new findings added (manifest unchanged)
  1  one or more new findings added to the manifest
  2  tool error (bad flag, git failure, etc.)`,
	RunE:          runScan,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// validateDetectors checks that every positional arg is a known detector
// name. Returns an error naming the offending arg otherwise.
func validateDetectors(args []string) error {
	for _, d := range args {
		if !detectorNames[d] {
			return fmt.Errorf("unknown detector %q (valid: binaries, secrets, large, gitignored, all)", d)
		}
	}
	return nil
}

// configFromDetectors builds a scanner.Config enabling exactly the named
// detectors. With no detectors, returns scanner.DefaultConfig (the safe
// default — secrets + gitignored only).
func configFromDetectors(detectors []string, sizeThreshold int64, workers int) scanner.Config {
	if len(detectors) == 0 {
		c := scanner.DefaultConfig()
		c.SizeThreshold = sizeThreshold
		c.Workers = workers
		return c
	}
	c := scanner.Config{SizeThreshold: sizeThreshold, Workers: workers}
	for _, d := range detectors {
		switch d {
		case "all":
			c.ScanBinaries = true
			c.ScanSecrets = true
			c.ScanLargeFiles = true
			c.ScanGitignored = true
		case "binaries":
			c.ScanBinaries = true
		case "secrets":
			c.ScanSecrets = true
		case "large":
			c.ScanLargeFiles = true
		case "gitignored":
			c.ScanGitignored = true
		}
	}
	return c
}

func runScan(cmd *cobra.Command, args []string) error {
	if err := validateDetectors(args); err != nil {
		return &exitCodeError{code: 2, msg: err.Error()}
	}
	detectors := args
	repoPath := repoPathFlag(cmd)

	sizeStr, _ := cmd.Flags().GetString("size")
	outputPath, _ := cmd.Flags().GetString("output")
	workers, _ := cmd.Flags().GetInt("workers")
	emitJSON, _ := cmd.Flags().GetBool("json")

	if outputPath == "" {
		var err error
		outputPath, err = manifestPathFor(repoPath)
		if err != nil {
			return &exitCodeError{code: 2, msg: fmt.Sprintf("resolve manifest path: %v", err)}
		}
	}

	sizeThreshold, err := parseSize(sizeStr)
	if err != nil {
		return &exitCodeError{code: 2, msg: fmt.Sprintf("invalid --size: %v", err)}
	}

	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	config := configFromDetectors(detectors, sizeThreshold, workers)
	// Progress reporter only when emitting human-readable output.
	if !emitJSON {
		config.ProgressFunc = func(blobsScanned, findingsCount int) {
			fmt.Fprintf(cmd.ErrOrStderr(), "\rScanning... %d blobs processed, %d findings", blobsScanned, findingsCount)
		}
	}

	if !emitJSON {
		cmd.Printf("Scanning %s (%s)...\n", repoPath, summariseDetectors(config))
	}

	s := scanner.New(config)
	result, err := s.Scan(repoPath)
	if !emitJSON {
		fmt.Fprintln(cmd.ErrOrStderr()) // clear progress line
	}
	if err != nil {
		return &exitCodeError{code: 2, msg: fmt.Sprintf("scan failed: %v", err)}
	}

	// Read existing manifest (if any) so we can compute the delta.
	var existing domain.Manifest
	if _, statErr := os.Stat(outputPath); statErr == nil {
		existing, err = manifest.ReadJSON(outputPath)
		if err != nil {
			return &exitCodeError{code: 2, msg: fmt.Sprintf("read existing manifest: %v", err)}
		}
	} else {
		existing = domain.NewManifest()
	}

	// Compute new findings (hashes not previously in the manifest).
	var newFindings []*domain.Finding
	for h, f := range result {
		if _, had := existing[h]; !had {
			newFindings = append(newFindings, f)
		}
	}
	added := existing.Merge(result)

	if err := manifest.WriteJSON(existing, outputPath); err != nil {
		return &exitCodeError{code: 2, msg: fmt.Sprintf("write manifest: %v", err)}
	}

	if emitJSON {
		if newFindings == nil {
			newFindings = []*domain.Finding{}
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(newFindings); err != nil {
			return &exitCodeError{code: 2, msg: fmt.Sprintf("encode json: %v", err)}
		}
	} else {
		printScanSummary(cmd, newFindings, len(existing), outputPath)
	}

	if added > 0 {
		return &exitCodeError{code: 1, msg: ""}
	}
	return nil
}

func summariseDetectors(c scanner.Config) string {
	var parts []string
	if c.ScanSecrets {
		parts = append(parts, "secrets")
	}
	if c.ScanGitignored {
		parts = append(parts, "gitignored")
	}
	if c.ScanBinaries {
		parts = append(parts, "binaries")
	}
	if c.ScanLargeFiles {
		parts = append(parts, "large")
	}
	if len(parts) == 0 {
		return "no detectors"
	}
	return strings.Join(parts, "+")
}

func printScanSummary(cmd *cobra.Command, added []*domain.Finding, manifestSize int, manifestPath string) {
	byType := map[string]int{}
	for _, f := range added {
		byType[string(f.Type)]++
	}
	cmd.Printf("Added %d new finding(s) (manifest now has %d total).\n", len(added), manifestSize)
	if len(added) > 0 {
		var types []string
		for t := range byType {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, t := range types {
			cmd.Printf("  %-10s %d\n", t, byType[t])
		}
	}
	cmd.Printf("Manifest: %s\n", manifestPath)
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
	Use:   "generate",
	Short: "Generate human-readable markdown report from manifest",
	Args:  cobra.NoArgs,
	RunE:  runReportGenerate,
}

func runReportGenerate(cmd *cobra.Command, args []string) error {
	repoPath := repoPathFlag(cmd)

	outputPath, _ := cmd.Flags().GetString("output")

	inputPath, err := manifestPathFor(repoPath)
	if err != nil {
		return fmt.Errorf("resolve manifest path: %w", err)
	}

	// Default output to repo root
	if outputPath == "" {
		outputPath = filepath.Join(repoPath, reportFilename)
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
	gen := report.NewGenerator(repoPath)

	// Find shared blobs to enhance the report with warnings
	var blobHashes []string
	for hash := range m {
		blobHashes = append(blobHashes, hash)
	}
	if len(blobHashes) > 0 {
		cmd.Println("Analyzing blob sharing across paths...")
		if sharedBlobs, err := gitquery.FindAllPathsForBlobs(repoPath, blobHashes); err == nil {
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
	Use:   "read [report.md]",
	Short: "Parse markdown report back into the findings manifest",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runReportRead,
}

func runReportRead(cmd *cobra.Command, args []string) error {
	repoPath := repoPathFlag(cmd)

	// Input report path: explicit positional arg, otherwise the canonical
	// default in the repo root.
	inputPath := filepath.Join(repoPath, reportFilename)
	if len(args) > 0 {
		inputPath = args[0]
	}

	outputPath, _ := cmd.Flags().GetString("output")
	if outputPath == "" {
		// Default to the active manifest in the resolved repo.
		mp, err := manifestPathFor(repoPath)
		if err != nil {
			return fmt.Errorf("resolve manifest path: %w", err)
		}
		outputPath = mp
	}

	// Open input file
	f, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open report: %w", err)
	}
	defer f.Close()

	// Parse report
	m, err := report.Parse(f)
	if err != nil {
		return fmt.Errorf("failed to parse report: %w", err)
	}

	// Write manifest
	if err := manifest.WriteJSON(m, outputPath); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	// Show summary
	purgeCount := len(m)
	cmd.Printf("Manifest written: %s\n", outputPath)
	cmd.Printf("Total findings: %d, marked for purge: %d\n", len(m), purgeCount)

	if purgeCount > 0 {
		cmd.Printf("Next step: git-expunge expunge --manifest %s\n", outputPath)
	}

	return nil
}

var expungeCmd = &cobra.Command{
	Use:   "expunge",
	Short: "Rewrite history to permanently remove every blob in the manifest",
	Long: `Apply the findings manifest by rewriting the repository's history
to permanently remove the blobs it contains.

Runs in dry-run mode by default. Pass --execute to actually perform the
rewrite — a full backup archive is created next to the repo before any
destructive operation.`,
	Args: cobra.NoArgs,
	RunE: runExpunge,
}

func runExpunge(cmd *cobra.Command, args []string) error {
	repoPath := repoPathFlag(cmd)

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
		mp, err := manifestPathFor(repoPath)
		if err != nil {
			return fmt.Errorf("resolve manifest path: %w", err)
		}
		manifestPath = mp
	}

	// Read manifest
	m, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Get blobs to purge
	blobsToPurge := m.Blobs()
	if len(blobsToPurge) == 0 {
		cmd.Println("No items marked for purging. Use 'git-expunge review' or 'git-expunge report' to select items.")
		return nil
	}

	cmd.Printf("Found %d blobs marked for purging\n", len(blobsToPurge))

	// Check for shared blobs and filter to safe-only
	cmd.Println("Checking for shared blob content...")
	allPaths, err := gitquery.FindAllPathsForBlobs(repoPath, blobsToPurge)
	if err != nil {
		cmd.Printf("Warning: could not check for shared blobs: %v\n", err)
	} else {
		// Filter to only blobs where ALL paths are marked for purge
		safeToPurge, skipped := m.SafeBlobs(allPaths)

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
		cmd.Println("  git-expunge expunge --execute")
	} else {
		// Move the purged entries out of the active manifest and into
		// the audit sidecar under .git/git-expunge/last-purged.json.
		// The sidecar is what `verify` consults to confirm the rewrite
		// actually removed those blobs; the main manifest is cleaned
		// so subsequent reads don't keep showing them queued.
		sidecar := domain.NewManifest()
		for _, hash := range blobsToPurge {
			if f, ok := m[hash]; ok {
				sidecar[hash] = f
				delete(m, hash)
			}
		}
		sidecarPath, err := purgedSidecarPath(repoPath)
		if err != nil {
			return fmt.Errorf("resolve sidecar path: %w", err)
		}
		if err := manifest.WriteJSON(sidecar, sidecarPath); err != nil {
			return fmt.Errorf("failed to write purged sidecar: %w", err)
		}
		if err := manifest.WriteJSON(m, manifestPath); err != nil {
			return fmt.Errorf("failed to update manifest after rewrite: %w", err)
		}

		cmd.Println("\nRepository history has been rewritten.")
		cmd.Printf("Recorded %d purged entr(ies) for verification.\n", len(sidecar))
		cmd.Println("Run 'git-expunge verify' to confirm purged items are unreachable.")
	}

	return nil
}

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify purged items are unreachable in repository",
	Args:  cobra.NoArgs,
	RunE:  runVerify,
}

func runVerify(cmd *cobra.Command, args []string) error {
	repoPath := repoPathFlag(cmd)

	manifestPath, _ := cmd.Flags().GetString("manifest")

	// verify checks the post-rewrite sidecar — the authoritative record
	// of what was actually expunged. An explicit --manifest flag points
	// at a specific file (sidecar or otherwise); without it we read the
	// default sidecar. We deliberately do NOT fall back to the main
	// findings manifest: a Purge=true flag there means "the user intends
	// to remove this," not "this was removed," and verifying intent is
	// meaningless.
	source := manifestPath
	if source == "" {
		sp, err := purgedSidecarPath(repoPath)
		if err != nil {
			return fmt.Errorf("resolve sidecar path: %w", err)
		}
		source = sp
	}

	m, err := manifest.ReadJSON(source)
	if err != nil {
		if os.IsNotExist(err) {
			cmd.Printf("No purged-blobs record found at %s.\n", source)
			cmd.Println("Run 'git-expunge expunge --execute' first; verify reads the sidecar it writes.")
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", source, err)
	}
	blobsToPurge := make([]string, 0, len(m))
	for hash := range m {
		blobsToPurge = append(blobsToPurge, hash)
	}

	if len(blobsToPurge) == 0 {
		cmd.Printf("No purged blobs recorded in %s.\n", source)
		return nil
	}
	cmd.Printf("Source: %s\n", source)

	cmd.Printf("Verifying %d blobs are unreachable...\n", len(blobsToPurge))

	var stillReachable []string
	for _, hash := range blobsToPurge {
		reachable, err := gitquery.IsReachable(repoPath, hash)
		if err != nil {
			return fmt.Errorf("checking blob %s: %w", hash, err)
		}
		if reachable {
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

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore repository from backup archive",
	Args:  cobra.NoArgs,
	RunE:  runRestore,
}

var addCmd = &cobra.Command{
	Use:   "add <glob>...",
	Short: "Add files or directories to the manifest for removal",
	Long: `Add files or directories to be removed from Git history. Arguments
are literal paths or glob patterns:

  git-expunge add vendor/secrets.json    # exact path
  git-expunge add "*.env"                # glob (quote to prevent shell expansion)
  git-expunge add "vendor/**"            # double-star recursive glob

Use -C / --repo to operate on a repository other than the current
directory.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAdd,
}


func runRestore(cmd *cobra.Command, args []string) error {
	destPath := repoPathFlag(cmd)

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
	patterns := args
	repoPath := repoPathFlag(cmd)

	manifestPath, _ := cmd.Flags().GetString("manifest")
	if manifestPath == "" {
		mp, err := manifestPathFor(repoPath)
		if err != nil {
			return fmt.Errorf("resolve manifest path: %w", err)
		}
		manifestPath = mp
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
		findings, err := gitquery.FindBlobsForPath(repoPath, pattern)
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
	cmd.Printf("Total: %d findings (%d marked for purge)\n", len(m), len(m))

	if totalAdded > 0 {
		cmd.Println("\nNext step: git-expunge expunge --execute")
	}

	return nil
}

var previewCmd = &cobra.Command{
	Use:   "preview <blob-hash>",
	Short: "Preview the content of a git blob",
	Long: `Preview the content of a git blob by its hash.

Shows text content for text files, or a hex dump for binary files.
Useful for inspecting blobs before deciding whether to purge them.

The blob hash can be found in the manifest or report files. Use -C /
--repo to operate on a repository other than the current directory.`,
	Args: cobra.ExactArgs(1),
	RunE: runPreview,
}

func runPreview(cmd *cobra.Command, args []string) error {
	blobHash := args[0]
	repoPath := repoPathFlag(cmd)

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

// listCmd, searchCmd, removeCmd, summaryCmd are the read/write surface
// for the on-disk manifest. They all print tab-separated lines so callers
// can filter with grep/awk; there's no query DSL.

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Print every manifest entry as a tab-separated line",
	Long: `Tab-separated columns: type, 7-char hash, size, path.
Filter with grep/awk:

  git-expunge list | grep secret
  git-expunge list | awk -F'\t' '$3 > 1048576 { print }'`,
	Args: cobra.NoArgs,
	RunE: runList,
}

var searchCmd = &cobra.Command{
	Use:   "search <glob>...",
	Short: "Preview history-glob matches without writing the manifest",
	Long: `Walks history for blobs whose paths match the given glob(s)
and prints them in list-format. Does not touch the manifest — use
'git-expunge add' for that.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSearch,
}

var removeCmd = &cobra.Command{
	Use:   "remove <glob>...",
	Short: "Drop manifest entries whose Path matches one or more globs",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRemove,
}

var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Print counts of manifest entries grouped by type",
	Args:  cobra.NoArgs,
	RunE:  runSummary,
}

// gitExpungeDir returns <git-common-dir>/<stateDirname> — the directory
// where every persistent file git-expunge writes lives. Uses
// `git rev-parse --git-common-dir` under the hood so normal repos,
// linked worktrees, and bare repos all resolve correctly.
func gitExpungeDir(repoPath string) (string, error) {
	commonDir, err := gitquery.GitCommonDir(repoPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(commonDir, stateDirname), nil
}

// manifestPathFor returns the path to the on-disk findings manifest.
// Lives under <git-common-dir>/<stateDirname>/<manifestFilename> so it
// travels with the repo, doesn't pollute the working tree, and never
// needs to be gitignored.
func manifestPathFor(repoPath string) (string, error) {
	dir, err := gitExpungeDir(repoPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, manifestFilename), nil
}

// purgedSidecarPath returns the path of the post-expunge audit sidecar
// — the record of what `expunge --execute` actually removed. `verify`
// consults this file to confirm the blobs are unreachable.
func purgedSidecarPath(repoPath string) (string, error) {
	dir, err := gitExpungeDir(repoPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sidecarFilename), nil
}

func readManifestOrEmpty(path string) (domain.Manifest, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return domain.NewManifest(), nil
	}
	return manifest.ReadJSON(path)
}

func writeFindingLine(cmd *cobra.Command, f *domain.Finding) {
	short := f.BlobHash
	if len(short) > 7 {
		short = short[:7]
	}
	// list / search produce structured data that callers pipe through
	// grep/awk — write to stdout, not cobra's stderr-defaulted cmd.Printf.
	fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d\t%s\n", f.Type, short, f.Size, f.Path)
}

func sortedFindings(m domain.Manifest) []*domain.Finding {
	out := make([]*domain.Finding, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func runList(cmd *cobra.Command, args []string) error {
	repoPath := repoPathFlag(cmd)
	mPath, err := manifestPathFor(repoPath)
	if err != nil {
		return err
	}
	m, err := readManifestOrEmpty(mPath)
	if err != nil {
		return err
	}
	for _, f := range sortedFindings(m) {
		writeFindingLine(cmd, f)
	}
	return nil
}

func runSearch(cmd *cobra.Command, args []string) error {
	patterns := args
	repoPath := repoPathFlag(cmd)
	var findings []*domain.Finding
	for _, p := range patterns {
		got, err := gitquery.FindBlobsForPath(repoPath, p)
		if err != nil {
			return fmt.Errorf("search %q: %w", p, err)
		}
		findings = append(findings, got...)
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	for _, f := range findings {
		writeFindingLine(cmd, f)
	}
	return nil
}

func runRemove(cmd *cobra.Command, args []string) error {
	patterns := args
	repoPath := repoPathFlag(cmd)
	mPath, err := manifestPathFor(repoPath)
	if err != nil {
		return err
	}
	m, err := readManifestOrEmpty(mPath)
	if err != nil {
		return err
	}

	removed := 0
	for hash, f := range m {
		for _, p := range patterns {
			match, matchErr := doublestar.Match(p, f.Path)
			if matchErr != nil {
				return fmt.Errorf("invalid glob %q: %w", p, matchErr)
			}
			if !match && f.Path != p {
				continue
			}
			delete(m, hash)
			removed++
			break
		}
	}

	if err := manifest.WriteJSON(m, mPath); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	cmd.Printf("Removed %d entr(ies). Manifest now has %d.\n", removed, len(m))
	return nil
}

func runSummary(cmd *cobra.Command, args []string) error {
	repoPath := repoPathFlag(cmd)
	mPath, err := manifestPathFor(repoPath)
	if err != nil {
		return err
	}
	m, err := readManifestOrEmpty(mPath)
	if err != nil {
		return err
	}
	byType := map[string]int{}
	for _, f := range m {
		byType[string(f.Type)]++
	}
	// summary is structured data callers may pipe; write to stdout, not
	// cobra's stderr-defaulted cmd.Printf.
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Total: %d\n", len(m))
	var types []string
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		fmt.Fprintf(out, "  %-10s %d\n", t, byType[t])
	}
	return nil
}

func init() {
	// Persistent repo-selection flag (matches git's -C convention). Every
	// subcommand resolves the repo via repoPathFlag — no subcommand takes
	// a positional repo, so argument parsing doesn't depend on the
	// filesystem.
	rootCmd.PersistentFlags().StringP("repo", "C", "", "Operate on the repository at this path (default: current directory)")

	// scan flags
	scanCmd.Flags().String("size", "100KB", "Size threshold for the `large` detector (e.g. 100KB, 1MB)")
	scanCmd.Flags().StringP("output", "o", "", fmt.Sprintf("Manifest path (default: <repo>/.git/%s/%s)", stateDirname, manifestFilename))
	scanCmd.Flags().Bool("json", false, "Emit new findings as a JSON array on stdout; suppress the human-readable summary")
	scanCmd.Flags().IntP("workers", "j", 0, "Number of parallel workers (default: number of CPUs)")

	// report subcommands
	reportGenerateCmd.Flags().StringP("output", "o", "", fmt.Sprintf("Output path (default: <repo>/%s)", reportFilename))
	reportReadCmd.Flags().StringP("output", "o", "", fmt.Sprintf("Manifest path (default: <repo>/.git/%s/%s)", stateDirname, manifestFilename))
	reportCmd.AddCommand(reportGenerateCmd)
	reportCmd.AddCommand(reportReadCmd)

	// rewrite flags
	expungeCmd.Flags().String("manifest", "", "Manifest file with purge selections")
	expungeCmd.Flags().Bool("dry-run", true, "Show what would be done without making changes")
	expungeCmd.Flags().Bool("execute", false, "Actually perform the rewrite")
	expungeCmd.Flags().String("backup-dir", "", "Directory for archive backup (default: parent of repo)")
	expungeCmd.Flags().Bool("skip-backup", false, "Skip backup (dangerous, requires confirmation)")

	// verify flags
	verifyCmd.Flags().String("manifest", "", "Manifest to check against")

	// restore flags
	restoreCmd.Flags().String("archive", "", "Path to backup archive")
	restoreCmd.Flags().Bool("list", false, "List available backups")

	// add flags
	addCmd.Flags().String("manifest", "", fmt.Sprintf("Manifest file path (default: <repo>/.git/%s/%s)", stateDirname, manifestFilename))

	// preview flags
	previewCmd.Flags().IntP("lines", "n", 0, "Limit output to N lines (0 = no limit)")
	previewCmd.Flags().Bool("raw", false, "Output raw content without headers")

	// add commands to root
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(summaryCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(expungeCmd)
	rootCmd.AddCommand(verifyCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(previewCmd)
}

func main() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	var ec *exitCodeError
	if errors.As(err, &ec) {
		if ec.msg != "" {
			fmt.Fprintln(os.Stderr, ec.msg)
		}
		os.Exit(ec.code)
	}
	// Default: cobra has already printed the error.
	os.Exit(1)
}
