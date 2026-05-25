package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/manifest"
	"github.com/benjaminabbitt/git-expunge/tests/testutil"
	"github.com/spf13/cobra"
)

// newTestRootCmd creates a fresh root command for testing
// to avoid state pollution between tests
func newTestRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git-expunge",
		Short: "Test",
	}

	rewrite := &cobra.Command{
		Use:  "rewrite [repo-path]",
		Args: cobra.MaximumNArgs(1),
		RunE: runRewrite,
	}
	rewrite.Flags().String("manifest", "", "Manifest file")
	rewrite.Flags().Bool("dry-run", true, "Dry run mode")
	rewrite.Flags().Bool("execute", false, "Execute mode")
	rewrite.Flags().String("backup-dir", "", "Backup directory")
	rewrite.Flags().Bool("skip-backup", false, "Skip backup")

	cmd.AddCommand(rewrite)
	return cmd
}

func TestRewriteFlags_DryRunByDefault(t *testing.T) {
	// Create a test repo with a finding
	repo := testutil.NewTestRepo(t)

	repo.WriteFile("secret.txt", "AWS_SECRET_KEY=AKIAIOSFODNN7EXAMPLE")
	repo.AddAndCommit("add secret")

	// Create a manifest with one item marked for purge
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeSecret,
		Path:     "secret.txt",
	})
	manifestPath := filepath.Join(repo.Path, "git-expunge-findings.json")
	if err := manifest.WriteJSON(m, manifestPath); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	// Run rewrite without --execute flag
	var stdout bytes.Buffer
	cmd := newTestRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rewrite", repo.Path})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("rewrite command failed: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "DRY RUN") {
		t.Errorf("expected dry run by default, got: %s", output)
	}
}

func TestRewriteFlags_ExecuteOverridesDryRun(t *testing.T) {
	// Create a test repo
	repo := testutil.NewTestRepo(t)

	repo.WriteFile("secret.txt", "AWS_SECRET_KEY=AKIAIOSFODNN7EXAMPLE")
	repo.AddAndCommit("add secret")

	// Create a manifest with one item marked for purge
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeSecret,
		Path:     "secret.txt",
	})
	manifestPath := filepath.Join(repo.Path, "git-expunge-findings.json")
	if err := manifest.WriteJSON(m, manifestPath); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	// Run rewrite with --execute flag
	var stdout bytes.Buffer
	cmd := newTestRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rewrite", repo.Path, "--execute", "--skip-backup"})

	// We expect this might fail (invalid blob hash), but we want to check
	// that it at least tries to execute (not dry run)
	_ = cmd.Execute()

	output := stdout.String()
	// The key check: should NOT say DRY RUN when --execute is passed
	if strings.Contains(output, "[DRY RUN]") {
		t.Errorf("expected execute mode (not dry run) when --execute is passed, got: %s", output)
	}
	// Should attempt to execute
	if !strings.Contains(output, "[EXECUTE]") {
		t.Errorf("expected [EXECUTE] in output when --execute flag is passed, got: %s", output)
	}
}

// TestRewriteFlags_EmptyManifest pins down the "nothing to do" path: when
// the manifest is empty, rewrite prints a friendly message and exits
// cleanly. Replaces the old "no purge items" test — every entry in the
// manifest is now implicitly to-be-purged, so the only way to have
// nothing to purge is an empty manifest.
func TestRewriteFlags_EmptyManifest(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("readme.txt", "Hello world")
	repo.AddAndCommit("add readme")

	manifestPath := filepath.Join(repo.Path, "git-expunge-findings.json")
	if err := manifest.WriteJSON(domain.NewManifest(), manifestPath); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	var stdout bytes.Buffer
	cmd := newTestRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rewrite", repo.Path})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if !strings.Contains(stdout.String(), "No items marked for purging") {
		t.Errorf("expected friendly empty-manifest message, got: %s", stdout.String())
	}
}

// TestRewriteExecute_RemovesPurgedEntriesFromManifest pins down the contract
// that after a successful rewrite --execute, the on-disk manifest no longer
// contains entries for blobs that were just expunged from history. Without
// this, the UI/CLI keeps showing the same items queued for purge even
// though they're already gone, which is confusing and prevents the workflow
// from terminating cleanly.
func TestRewriteExecute_RemovesPurgedEntriesFromManifest(t *testing.T) {
	repo := testutil.NewTestRepo(t)

	// Commit a real file so we have a real blob hash to purge.
	repo.WriteFile("secrets.env", "DB_PASSWORD=hunter2")
	repo.WriteFile("keep.md", "this stays")
	repo.AddAndCommit("seed commit")

	// Discover the real blob hash for secrets.env.
	blobHash := strings.TrimSpace(repo.Git("rev-parse", "HEAD:secrets.env"))
	if blobHash == "" {
		t.Fatal("could not resolve secrets.env blob hash")
	}

	// Build a manifest with that blob marked for purge.
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: blobHash,
		Type:     domain.FindingTypeAdd,
		Path:     "secrets.env",
	})
	manifestPath := filepath.Join(repo.Path, "git-expunge-findings.json")
	if err := manifest.WriteJSON(m, manifestPath); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Run rewrite --execute --skip-backup.
	var stdout bytes.Buffer
	cmd := newTestRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rewrite", repo.Path, "--execute", "--skip-backup"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("rewrite failed: %v\n%s", err, stdout.String())
	}

	// The on-disk manifest should no longer carry the purged blob entry.
	after, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		t.Fatalf("read manifest after rewrite: %v", err)
	}
	if _, present := after[blobHash]; present {
		t.Errorf("expected manifest to drop purged entry %s, but it's still present: %+v",
			blobHash, after[blobHash])
	}
}

// TestRewriteExecute_WritesPurgedSidecar pins down that the rewrite step
// records what it just purged into a sidecar file. The sidecar is what
// `verify` consults to confirm the rewrite worked, since the main manifest
// has its purged entries removed.
func TestRewriteExecute_WritesPurgedSidecar(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.WriteFile("keep.md", "stays")
	repo.AddAndCommit("seed")

	blobHash := strings.TrimSpace(repo.Git("rev-parse", "HEAD:secrets.env"))

	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: blobHash, Type: domain.FindingTypeAdd,
		Path: "secrets.env",
	})
	if err := manifest.WriteJSON(m, filepath.Join(repo.Path, "git-expunge-findings.json")); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stdout bytes.Buffer
	cmd := newTestRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rewrite", repo.Path, "--execute", "--skip-backup"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rewrite: %v\n%s", err, stdout.String())
	}

	sidecarPath := filepath.Join(repo.Path, ".git", "git-expunge-last-purged.json")
	side, err := manifest.ReadJSON(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar %s: %v", sidecarPath, err)
	}
	if _, ok := side[blobHash]; !ok {
		t.Errorf("expected sidecar to contain purged blob %s, got %+v", blobHash, side)
	}
}

// TestVerify_ReadsSidecar exercises the post-rewrite verify path: after a
// rewrite cleans the main manifest, verify should still confirm
// unreachability by consulting the sidecar.
func TestVerify_ReadsSidecar(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.AddAndCommit("seed")
	blobHash := strings.TrimSpace(repo.Git("rev-parse", "HEAD:secrets.env"))

	m := domain.NewManifest()
	m.Add(&domain.Finding{BlobHash: blobHash, Type: domain.FindingTypeAdd, Path: "secrets.env"})
	if err := manifest.WriteJSON(m, filepath.Join(repo.Path, "git-expunge-findings.json")); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Rewrite -> populates sidecar, empties manifest.
	rewriteCmd := newTestRootCmd()
	var rwOut bytes.Buffer
	rewriteCmd.SetOut(&rwOut)
	rewriteCmd.SetErr(&rwOut)
	rewriteCmd.SetArgs([]string{"rewrite", repo.Path, "--execute", "--skip-backup"})
	if err := rewriteCmd.Execute(); err != nil {
		t.Fatalf("rewrite: %v\n%s", err, rwOut.String())
	}
	// Drop loose objects so blobs become truly unreachable.
	repo.Git("reflog", "expire", "--expire=now", "--all")
	repo.Git("gc", "--prune=now")

	// Now verify — should use the sidecar and report success.
	verifyCmd := newTestRootCmdWithVerify()
	var vOut bytes.Buffer
	verifyCmd.SetOut(&vOut)
	verifyCmd.SetErr(&vOut)
	verifyCmd.SetArgs([]string{"verify", repo.Path})
	if err := verifyCmd.Execute(); err != nil {
		t.Fatalf("verify: %v\n%s", err, vOut.String())
	}
	got := vOut.String()
	if strings.Contains(got, "No items were marked for purging") {
		t.Errorf("verify should not say 'no items' when a sidecar exists; got:\n%s", got)
	}
	if !strings.Contains(got, "unreachable") {
		t.Errorf("verify should confirm unreachability via sidecar; got:\n%s", got)
	}
}

// TestVerify_NoSidecar_DoesNotFallBack pins down that verify intentionally
// refuses to consult the main findings manifest as a fallback. A Purge=true
// flag there means "the user intends to remove this," not "this was
// removed" — verifying intent would be misleading. Verify should instead
// tell the user no rewrite-record exists and point them at `rewrite`.
func TestVerify_NoSidecar_DoesNotFallBack(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("keep.md", "stays")
	repo.AddAndCommit("seed")

	// Populate the main manifest with a Purge=true entry but do NOT run
	// rewrite, so no sidecar exists.
	bogus := "0000000000000000000000000000000000000000"
	m := domain.NewManifest()
	m.Add(&domain.Finding{BlobHash: bogus, Type: domain.FindingTypeAdd, Path: "ghost.bin"})
	if err := manifest.WriteJSON(m, filepath.Join(repo.Path, "git-expunge-findings.json")); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	verifyCmd := newTestRootCmdWithVerify()
	var vOut bytes.Buffer
	verifyCmd.SetOut(&vOut)
	verifyCmd.SetErr(&vOut)
	verifyCmd.SetArgs([]string{"verify", repo.Path})
	if err := verifyCmd.Execute(); err != nil {
		t.Fatalf("verify: %v\n%s", err, vOut.String())
	}
	got := vOut.String()
	if strings.Contains(got, "Verifying") {
		t.Errorf("verify must not attempt to check the main manifest's Purge=true entries; got:\n%s", got)
	}
	if !strings.Contains(got, "rewrite") {
		t.Errorf("verify should direct user to run rewrite when no sidecar exists; got:\n%s", got)
	}
}

// newTestRootCmdWithVerify wires up the verify subcommand for isolated
// CLI-level tests, mirroring newTestRootCmd's pattern.
func newTestRootCmdWithVerify() *cobra.Command {
	cmd := &cobra.Command{Use: "git-expunge"}
	v := &cobra.Command{
		Use:  "verify [repo-path]",
		Args: cobra.MaximumNArgs(1),
		RunE: runVerify,
	}
	v.Flags().String("manifest", "", "Manifest file")
	cmd.AddCommand(v)
	return cmd
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"100", 100, false},
		{"100B", 100, false},
		{"100KB", 100 * 1024, false},
		{"100kb", 100 * 1024, false},
		{"1MB", 1024 * 1024, false},
		{"1GB", 1024 * 1024 * 1024, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseSize(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseSize(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("parseSize(%q) unexpected error: %v", tt.input, err)
				return
			}
			if result != tt.expected {
				t.Errorf("parseSize(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 bytes"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1024 * 1024, "1.00 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
		{1024*1024*1024 + 512*1024*1024, "1.50 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatBytes(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

func init() {
	// Ensure we're not affecting real repos during tests
	os.Setenv("GIT_AUTHOR_NAME", "Test")
	os.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	os.Setenv("GIT_COMMITTER_NAME", "Test")
	os.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")
}

// --- scan / list / search / remove / summary --------------------------------

// newScanTestCmd builds an isolated cobra tree for the new scan command.
func newScanTestCmd() *cobra.Command {
	root := &cobra.Command{Use: "git-expunge"}
	s := &cobra.Command{
		Use:           "scan [detector...] [repo]",
		RunE:          runScan,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	s.Flags().String("size", "100KB", "Size threshold for `large`")
	s.Flags().StringP("output", "o", "", "Manifest path")
	s.Flags().Bool("json", false, "Emit JSON delta")
	s.Flags().IntP("workers", "j", 0, "Workers")
	root.AddCommand(s)
	return root
}

// TestScanCmd_NoArgs_RunsSafeDefaults pins down that bare `scan` runs the
// safe defaults — secrets + gitignored — and writes a manifest at
// <repo>/git-expunge-findings.json.
func TestScanCmd_NoArgs_RunsSafeDefaults(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.AddAndCommit("seed")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env")

	var out bytes.Buffer
	cmd := newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"scan", repo.Path})

	err := cmd.Execute()
	// Findings were added, expect exit-code-error 1.
	var ec *exitCodeError
	if !errors.As(err, &ec) || ec.code != 1 {
		t.Fatalf("expected exitCodeError{code:1}, got %v\n%s", err, out.String())
	}

	m, readErr := manifest.ReadJSON(filepath.Join(repo.Path, "git-expunge-findings.json"))
	if readErr != nil {
		t.Fatalf("read manifest: %v", readErr)
	}
	if len(m) == 0 {
		t.Errorf("expected at least one finding in manifest, got 0")
	}
}

// TestScanCmd_PositionalDetectors_RunsOnlyThose pins down that named
// detectors override the safe default — `scan gitignored` runs only
// gitignored, ignoring secrets.
func TestScanCmd_PositionalDetectors_RunsOnlyThose(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE")
	repo.AddAndCommit("seed")
	// No .gitignore — gitignored has nothing to flag.

	var out bytes.Buffer
	cmd := newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"scan", "gitignored", repo.Path})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("scan gitignored: %v\n%s", err, out.String())
	}

	m, _ := manifest.ReadJSON(filepath.Join(repo.Path, "git-expunge-findings.json"))
	for _, f := range m {
		if f.Type == domain.FindingTypeSecret {
			t.Errorf("scan gitignored must not run secret detection; got: %+v", f)
		}
	}
}

// TestScanCmd_UnknownDetector_Exits2 pins the exit-code-2 path for a bad
// detector name.
func TestScanCmd_UnknownDetector_Exits2(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("a", "b")
	repo.AddAndCommit("seed")

	var out bytes.Buffer
	cmd := newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"scan", "not-a-detector", repo.Path})

	err := cmd.Execute()
	var ec *exitCodeError
	if !errors.As(err, &ec) || ec.code != 2 {
		t.Errorf("expected exitCodeError{code:2}, got %v", err)
	}
}

// TestScanCmd_ExitsZeroWhenNothingNew pins down idempotency — running scan
// twice against an already-clean (or already-scanned) repo returns 0.
func TestScanCmd_ExitsZeroWhenNothingNew(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("README.md", "hello")
	repo.AddAndCommit("seed")

	// First run: nothing matches (no secrets, no gitignored). Returns 0.
	var out bytes.Buffer
	cmd := newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"scan", repo.Path})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("first scan returned non-nil: %v", err)
	}

	// Second run: still nothing new.
	out.Reset()
	cmd = newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"scan", repo.Path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("second scan returned non-nil: %v", err)
	}
}

// TestScanCmd_JSONFlag_EmitsArrayOfNewFindings pins the --json contract.
func TestScanCmd_JSONFlag_EmitsArrayOfNewFindings(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.AddAndCommit("seed")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env")

	var out bytes.Buffer
	cmd := newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"scan", "gitignored", repo.Path, "--json"})

	err := cmd.Execute()
	var ec *exitCodeError
	if !errors.As(err, &ec) || ec.code != 1 {
		t.Fatalf("expected exit code 1 (findings added), got %v", err)
	}

	body := out.String()
	if !strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Errorf("--json should emit a JSON array, got: %q", body)
	}
	if strings.Contains(body, "Added ") || strings.Contains(body, "Manifest:") {
		t.Errorf("--json should suppress the human-readable summary, got: %q", body)
	}
}

// --- list / search / remove / summary ---------------------------------------

func newReadWriteTestCmd() *cobra.Command {
	root := &cobra.Command{Use: "git-expunge"}
	l := &cobra.Command{Use: "list [repo]", Args: cobra.MaximumNArgs(1), RunE: runList}
	se := &cobra.Command{Use: "search <glob>... [repo]", Args: cobra.MinimumNArgs(1), RunE: runSearch}
	rm := &cobra.Command{Use: "remove <glob>... [repo]", Args: cobra.MinimumNArgs(1), RunE: runRemove}
	su := &cobra.Command{Use: "summary [repo]", Args: cobra.MaximumNArgs(1), RunE: runSummary}
	root.AddCommand(l, se, rm, su)
	return root
}

func TestListCmd_PrintsTabSeparated(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	m := domain.NewManifest()
	m.Add(&domain.Finding{BlobHash: "abc1234567890", Type: domain.FindingTypeSecret, Path: "secrets.env", Size: 42})
	m.Add(&domain.Finding{BlobHash: "def4567890123", Type: domain.FindingTypeBinary, Path: "bin/app", Size: 1024})
	if err := manifest.WriteJSON(m, filepath.Join(repo.Path, "git-expunge-findings.json")); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var out bytes.Buffer
	cmd := newReadWriteTestCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list", repo.Path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.Count(line, "\t") != 3 {
			t.Errorf("expected 3 tabs per line, got %q", line)
		}
	}
	if !strings.Contains(out.String(), "secret\tabc1234\t42\tsecrets.env") {
		t.Errorf("expected secret line, got: %q", out.String())
	}
}

func TestRemoveCmd_GlobDrops(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	m := domain.NewManifest()
	m.Add(&domain.Finding{BlobHash: "h1", Type: domain.FindingTypeBinary, Path: "vendor/a.bin"})
	m.Add(&domain.Finding{BlobHash: "h2", Type: domain.FindingTypeBinary, Path: "vendor/b.bin"})
	m.Add(&domain.Finding{BlobHash: "h3", Type: domain.FindingTypeSecret, Path: "secrets.env"})
	mp := filepath.Join(repo.Path, "git-expunge-findings.json")
	if err := manifest.WriteJSON(m, mp); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	cmd := newReadWriteTestCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"remove", "vendor/**", repo.Path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("remove: %v\n%s", err, out.String())
	}

	after, _ := manifest.ReadJSON(mp)
	if len(after) != 1 {
		t.Fatalf("expected 1 entry after remove, got %d: %+v", len(after), after)
	}
	if _, ok := after["h3"]; !ok {
		t.Errorf("expected h3 to remain, got %+v", after)
	}
}

func TestRemoveCmd_RefusesEmptyArgs(t *testing.T) {
	cmd := newReadWriteTestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"remove"})
	if err := cmd.Execute(); err == nil {
		t.Errorf("remove with no args must error (cobra MinimumNArgs)")
	}
}

func TestSearchCmd_HistoryGlob_NoManifestWrite(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=x")
	repo.AddAndCommit("seed")

	mp := filepath.Join(repo.Path, "git-expunge-findings.json")
	if _, err := os.Stat(mp); err == nil {
		t.Fatalf("manifest should not exist yet")
	}

	var out bytes.Buffer
	cmd := newReadWriteTestCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"search", "*.env", repo.Path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("search: %v\n%s", err, out.String())
	}

	if !strings.Contains(out.String(), "secrets.env") {
		t.Errorf("expected secrets.env in search output, got: %q", out.String())
	}
	if _, err := os.Stat(mp); err == nil {
		t.Errorf("search must not write the manifest")
	}
}

func TestSummaryCmd_CountsByType(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	m := domain.NewManifest()
	m.Add(&domain.Finding{BlobHash: "h1", Type: domain.FindingTypeSecret, Path: "a"})
	m.Add(&domain.Finding{BlobHash: "h2", Type: domain.FindingTypeSecret, Path: "b"})
	m.Add(&domain.Finding{BlobHash: "h3", Type: domain.FindingTypeBinary, Path: "c"})
	if err := manifest.WriteJSON(m, filepath.Join(repo.Path, "git-expunge-findings.json")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	cmd := newReadWriteTestCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"summary", repo.Path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("summary: %v", err)
	}

	body := out.String()
	if !strings.Contains(body, "Total: 3") {
		t.Errorf("expected 'Total: 3', got: %q", body)
	}
	if !strings.Contains(body, "secret") || !strings.Contains(body, "binary") {
		t.Errorf("expected per-type counts, got: %q", body)
	}
}

func TestParseScanArgs(t *testing.T) {
	tests := []struct {
		args      []string
		detectors []string
		repo      string
		wantErr   bool
	}{
		{[]string{}, nil, ".", false},
		{[]string{"."}, nil, ".", false},
		{[]string{"secrets"}, []string{"secrets"}, ".", false},
		{[]string{"secrets", "."}, []string{"secrets"}, ".", false},
		{[]string{"secrets", "gitignored"}, []string{"secrets", "gitignored"}, ".", false},
		{[]string{"secrets", "gitignored", "/tmp/repo"}, []string{"secrets", "gitignored"}, "/tmp/repo", false},
		{[]string{"all"}, []string{"all"}, ".", false},
		// A single non-detector arg is treated as a repo path, not an error.
		{[]string{"/tmp/repo-path"}, nil, "/tmp/repo-path", false},
		// An unknown name in a non-trailing slot can't be re-interpreted
		// as a repo path → error.
		{[]string{"not-a-detector", "secrets"}, nil, "", true},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			got, repo, err := parseScanArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("err mismatch: got %v want err=%v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if repo != tt.repo {
				t.Errorf("repo: got %q want %q", repo, tt.repo)
			}
			if len(got) != len(tt.detectors) {
				t.Errorf("detectors: got %v want %v", got, tt.detectors)
			}
		})
	}
}
