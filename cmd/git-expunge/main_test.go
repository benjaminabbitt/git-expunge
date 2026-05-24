package main

import (
	"bytes"
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
		Purge:    true,
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
		Purge:    true,
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

func TestRewriteFlags_NoPurgeItems(t *testing.T) {
	// Create a test repo
	repo := testutil.NewTestRepo(t)

	repo.WriteFile("readme.txt", "Hello world")
	repo.AddAndCommit("add readme")

	// Create a manifest with no items marked for purge
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeBinary,
		Path:     "file.bin",
		Purge:    false, // Not marked for purge
	})
	manifestPath := filepath.Join(repo.Path, "git-expunge-findings.json")
	if err := manifest.WriteJSON(m, manifestPath); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	// Run rewrite
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
	if !strings.Contains(output, "No items marked for purging") {
		t.Errorf("expected 'No items marked for purging' message, got: %s", output)
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
		Purge:    true,
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
		Path: "secrets.env", Purge: true,
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
	m.Add(&domain.Finding{BlobHash: blobHash, Type: domain.FindingTypeAdd, Path: "secrets.env", Purge: true})
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
	m.Add(&domain.Finding{BlobHash: bogus, Type: domain.FindingTypeAdd, Path: "ghost.bin", Purge: true})
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

// newRetroignoreTestCmd builds a fresh cobra tree with just the retroignore
// command, mirroring newTestRootCmd's pattern so tests stay isolated.
func newRetroignoreTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "git-expunge"}
	ri := &cobra.Command{
		Use:  "retroignore [repo-path]",
		Args: cobra.MaximumNArgs(1),
		RunE: runRetroignore,
	}
	ri.Flags().StringP("output", "o", "", "Output manifest path")
	ri.Flags().BoolP("yes", "y", false, "Auto-confirm review prompt")
	ri.Flags().Bool("no-prompt", false, "Skip review prompt")
	cmd.AddCommand(ri)
	return cmd
}

func TestRetroignoreCmd_NoPrompt_WritesManifest(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.AddAndCommit("oops")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env files")

	var stdout bytes.Buffer
	cmd := newRetroignoreTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"retroignore", repo.Path, "--no-prompt"})

	// Track whether the verify launcher fires; --no-prompt should NOT launch it.
	called := false
	prevLauncher := retroignoreVerifyLauncher
	retroignoreVerifyLauncher = func(*cobra.Command, []string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { retroignoreVerifyLauncher = prevLauncher })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("retroignore failed: %v\n%s", err, stdout.String())
	}
	if called {
		t.Errorf("--no-prompt should not launch the verify UI")
	}

	manifestPath := filepath.Join(repo.Path, "git-expunge-findings.json")
	m, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(m))
	}
	var f *domain.Finding
	for _, v := range m {
		f = v
	}
	if f.Path != "secrets.env" {
		t.Errorf("expected secrets.env, got %q", f.Path)
	}
	if !f.Purge {
		t.Errorf("expected Purge=true")
	}

	out := stdout.String()
	if !strings.Contains(out, "git-expunge ui") {
		t.Errorf("expected next-step hint mentioning 'git-expunge ui', got: %s", out)
	}
}

func TestRetroignoreCmd_NonTTY_PrintsHintWithoutHanging(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.AddAndCommit("oops")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env files")

	var stdout bytes.Buffer
	cmd := newRetroignoreTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"retroignore", repo.Path})

	called := false
	prevLauncher := retroignoreVerifyLauncher
	retroignoreVerifyLauncher = func(*cobra.Command, []string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { retroignoreVerifyLauncher = prevLauncher })

	// stdin is non-TTY under `go test`; the command must not block on input.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("retroignore failed: %v\n%s", err, stdout.String())
	}
	if called {
		t.Errorf("non-TTY run should not auto-launch the verify UI")
	}
	if !strings.Contains(stdout.String(), "git-expunge ui") {
		t.Errorf("expected next-step hint mentioning 'git-expunge ui', got: %s", stdout.String())
	}
}

func TestRetroignoreCmd_Yes_LaunchesVerify(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.AddAndCommit("oops")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env files")

	var stdout bytes.Buffer
	cmd := newRetroignoreTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"retroignore", repo.Path, "--yes"})

	called := false
	prevLauncher := retroignoreVerifyLauncher
	retroignoreVerifyLauncher = func(c *cobra.Command, args []string) error {
		called = true
		if len(args) == 0 || args[0] != repo.Path {
			t.Errorf("launcher should receive repo path %q, got %v", repo.Path, args)
		}
		return nil
	}
	t.Cleanup(func() { retroignoreVerifyLauncher = prevLauncher })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("retroignore failed: %v\n%s", err, stdout.String())
	}
	if !called {
		t.Errorf("--yes should launch the verify UI")
	}
}

func TestRetroignoreCmd_NoMatches_PrintsZeroAndExits(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("README.md", "hello")
	repo.AddAndCommit("init")

	var stdout bytes.Buffer
	cmd := newRetroignoreTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"retroignore", repo.Path, "--no-prompt"})

	prevLauncher := retroignoreVerifyLauncher
	retroignoreVerifyLauncher = func(*cobra.Command, []string) error {
		t.Errorf("launcher should not be called when there are no findings")
		return nil
	}
	t.Cleanup(func() { retroignoreVerifyLauncher = prevLauncher })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("retroignore failed: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "0 ") && !strings.Contains(stdout.String(), "No ") {
		t.Errorf("expected zero/no-findings message, got: %s", stdout.String())
	}
}
