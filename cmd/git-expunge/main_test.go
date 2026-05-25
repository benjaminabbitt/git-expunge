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

// bindRepoFlag attaches the persistent -C/--repo flag to a test root cmd
// so tests can route the repo via the same mechanism as the real binary.
func bindRepoFlag(root *cobra.Command) {
	root.PersistentFlags().StringP("repo", "C", "", "Operate on the repository at this path")
}

// newExpungeTestCmd builds an isolated cobra tree for the expunge command.
func newExpungeTestCmd() *cobra.Command {
	root := &cobra.Command{Use: "git-expunge"}
	bindRepoFlag(root)

	ex := &cobra.Command{
		Use:  "expunge",
		Args: cobra.NoArgs,
		RunE: runExpunge,
	}
	ex.Flags().String("manifest", "", "Manifest file")
	ex.Flags().Bool("dry-run", true, "Dry run mode")
	ex.Flags().Bool("execute", false, "Execute mode")
	ex.Flags().String("backup-dir", "", "Backup directory")
	ex.Flags().Bool("skip-backup", false, "Skip backup")

	root.AddCommand(ex)
	return root
}

// repoManifestPath returns the canonical on-disk manifest path under
// <repo>/.git/git-expunge/findings.json. Tests use this directly rather
// than going through manifestPathFor so they don't have to handle the
// (string, error) signature in every call site.
func repoManifestPath(repoPath string) string {
	return filepath.Join(repoPath, ".git", "git-expunge", "findings.json")
}

// repoSidecarPath returns <repo>/.git/git-expunge/last-purged.json.
func repoSidecarPath(repoPath string) string {
	return filepath.Join(repoPath, ".git", "git-expunge", "last-purged.json")
}

func TestExpungeFlags_DryRunByDefault(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secret.txt", "AWS_SECRET_KEY=AKIAIOSFODNN7EXAMPLE")
	repo.AddAndCommit("add secret")

	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeSecret,
		Path:     "secret.txt",
	})
	if err := manifest.WriteJSON(m, repoManifestPath(repo.Path)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stdout bytes.Buffer
	cmd := newExpungeTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"-C", repo.Path, "expunge"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expunge command failed: %v", err)
	}

	if !strings.Contains(stdout.String(), "DRY RUN") {
		t.Errorf("expected dry run by default, got: %s", stdout.String())
	}
}

func TestExpungeFlags_ExecuteOverridesDryRun(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secret.txt", "AWS_SECRET_KEY=AKIAIOSFODNN7EXAMPLE")
	repo.AddAndCommit("add secret")

	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeSecret,
		Path:     "secret.txt",
	})
	if err := manifest.WriteJSON(m, repoManifestPath(repo.Path)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stdout bytes.Buffer
	cmd := newExpungeTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"-C", repo.Path, "expunge", "--execute", "--skip-backup"})

	// May fail (the manifest references a bogus blob), but we only care
	// that the dry-run gate was bypassed.
	_ = cmd.Execute()

	output := stdout.String()
	if strings.Contains(output, "[DRY RUN]") {
		t.Errorf("expected execute mode when --execute is passed, got: %s", output)
	}
	if !strings.Contains(output, "[EXECUTE]") {
		t.Errorf("expected [EXECUTE] in output, got: %s", output)
	}
}

// TestExpungeFlags_EmptyManifest pins down the "nothing to do" path. With
// the queue model gone, the only way to have nothing to purge is an
// empty manifest — every entry IS implicitly to-be-purged.
func TestExpungeFlags_EmptyManifest(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("readme.txt", "Hello world")
	repo.AddAndCommit("add readme")

	if err := manifest.WriteJSON(domain.NewManifest(), repoManifestPath(repo.Path)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stdout bytes.Buffer
	cmd := newExpungeTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"-C", repo.Path, "expunge"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expunge: %v", err)
	}

	if !strings.Contains(stdout.String(), "No items marked for purging") {
		t.Errorf("expected empty-manifest message, got: %s", stdout.String())
	}
}

// TestExpungeExecute_RemovesPurgedEntriesFromManifest pins the contract that
// after a successful expunge --execute, the on-disk manifest no longer
// contains entries for blobs that were just expunged from history. Without
// this, subsequent reads keep showing items already gone.
func TestExpungeExecute_RemovesPurgedEntriesFromManifest(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB_PASSWORD=hunter2")
	repo.WriteFile("keep.md", "this stays")
	repo.AddAndCommit("seed commit")

	blobHash := strings.TrimSpace(repo.Git("rev-parse", "HEAD:secrets.env"))
	if blobHash == "" {
		t.Fatal("could not resolve secrets.env blob hash")
	}

	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: blobHash,
		Type:     domain.FindingTypeAdd,
		Path:     "secrets.env",
	})
	manifestPath := repoManifestPath(repo.Path)
	if err := manifest.WriteJSON(m, manifestPath); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stdout bytes.Buffer
	cmd := newExpungeTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"-C", repo.Path, "expunge", "--execute", "--skip-backup"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expunge: %v\n%s", err, stdout.String())
	}

	after, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		t.Fatalf("read manifest after expunge: %v", err)
	}
	if _, present := after[blobHash]; present {
		t.Errorf("expected manifest to drop purged entry %s, still present: %+v",
			blobHash, after[blobHash])
	}
}

// TestExpungeExecute_WritesPurgedSidecar pins down that expunge --execute
// records what it just purged into <repo>/.git/git-expunge/last-purged.json.
// `verify` reads that sidecar.
func TestExpungeExecute_WritesPurgedSidecar(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.WriteFile("keep.md", "stays")
	repo.AddAndCommit("seed")

	blobHash := strings.TrimSpace(repo.Git("rev-parse", "HEAD:secrets.env"))

	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: blobHash, Type: domain.FindingTypeAdd, Path: "secrets.env",
	})
	if err := manifest.WriteJSON(m, repoManifestPath(repo.Path)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stdout bytes.Buffer
	cmd := newExpungeTestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"-C", repo.Path, "expunge", "--execute", "--skip-backup"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expunge: %v\n%s", err, stdout.String())
	}

	side, err := manifest.ReadJSON(repoSidecarPath(repo.Path))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if _, ok := side[blobHash]; !ok {
		t.Errorf("expected sidecar to contain purged blob %s, got %+v", blobHash, side)
	}
}

// TestVerify_ReadsSidecar exercises the post-expunge verify path: after
// expunge cleans the main manifest, verify still confirms unreachability
// by consulting the sidecar.
func TestVerify_ReadsSidecar(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=hunter2")
	repo.AddAndCommit("seed")
	blobHash := strings.TrimSpace(repo.Git("rev-parse", "HEAD:secrets.env"))

	m := domain.NewManifest()
	m.Add(&domain.Finding{BlobHash: blobHash, Type: domain.FindingTypeAdd, Path: "secrets.env"})
	if err := manifest.WriteJSON(m, repoManifestPath(repo.Path)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Expunge → populates sidecar, empties manifest.
	expCmd := newExpungeTestCmd()
	var rwOut bytes.Buffer
	expCmd.SetOut(&rwOut)
	expCmd.SetErr(&rwOut)
	expCmd.SetArgs([]string{"-C", repo.Path, "expunge", "--execute", "--skip-backup"})
	if err := expCmd.Execute(); err != nil {
		t.Fatalf("expunge: %v\n%s", err, rwOut.String())
	}

	// Drop loose objects so blobs become truly unreachable.
	repo.Git("reflog", "expire", "--expire=now", "--all")
	repo.Git("gc", "--prune=now")

	verifyCmd := newVerifyTestCmd()
	var vOut bytes.Buffer
	verifyCmd.SetOut(&vOut)
	verifyCmd.SetErr(&vOut)
	verifyCmd.SetArgs([]string{"-C", repo.Path, "verify"})
	if err := verifyCmd.Execute(); err != nil {
		t.Fatalf("verify: %v\n%s", err, vOut.String())
	}
	got := vOut.String()
	if !strings.Contains(got, "unreachable") {
		t.Errorf("verify should confirm unreachability via sidecar; got:\n%s", got)
	}
}

// TestVerify_NoSidecar_DoesNotFallBack pins down that verify refuses to
// consult the active findings manifest as a fallback. Entries there are
// intent ("user wants to remove this"), not outcome ("this was removed");
// verifying intent would be misleading. Verify should instead point the
// user at `expunge`.
func TestVerify_NoSidecar_DoesNotFallBack(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("keep.md", "stays")
	repo.AddAndCommit("seed")

	bogusHash := "0000000000000000000000000000000000000000"
	m := domain.NewManifest()
	m.Add(&domain.Finding{BlobHash: bogusHash, Type: domain.FindingTypeAdd, Path: "ghost.bin"})
	if err := manifest.WriteJSON(m, repoManifestPath(repo.Path)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	verifyCmd := newVerifyTestCmd()
	var vOut bytes.Buffer
	verifyCmd.SetOut(&vOut)
	verifyCmd.SetErr(&vOut)
	verifyCmd.SetArgs([]string{"-C", repo.Path, "verify"})
	if err := verifyCmd.Execute(); err != nil {
		t.Fatalf("verify: %v\n%s", err, vOut.String())
	}
	got := vOut.String()
	if strings.Contains(got, "Verifying") {
		t.Errorf("verify must not attempt to check the active manifest's entries; got:\n%s", got)
	}
	if !strings.Contains(got, "expunge") {
		t.Errorf("verify should direct user to run expunge when no sidecar exists; got:\n%s", got)
	}
}

// newVerifyTestCmd wires up the verify subcommand for isolated tests.
func newVerifyTestCmd() *cobra.Command {
	root := &cobra.Command{Use: "git-expunge"}
	bindRepoFlag(root)
	v := &cobra.Command{
		Use:  "verify",
		Args: cobra.NoArgs,
		RunE: runVerify,
	}
	v.Flags().String("manifest", "", "Manifest file")
	root.AddCommand(v)
	return root
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
	os.Setenv("GIT_AUTHOR_NAME", "Test")
	os.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	os.Setenv("GIT_COMMITTER_NAME", "Test")
	os.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")
}

// --- scan -------------------------------------------------------------------

func newScanTestCmd() *cobra.Command {
	root := &cobra.Command{Use: "git-expunge"}
	bindRepoFlag(root)
	s := &cobra.Command{
		Use:           "scan [detector...]",
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

// TestScanCmd_NoArgs_RunsSafeDefaults pins that bare `scan` runs the safe
// defaults (secrets + gitignored) and writes the manifest under
// <repo>/.git/git-expunge/findings.json.
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
	cmd.SetArgs([]string{"-C", repo.Path, "scan"})

	err := cmd.Execute()
	var ec *exitCodeError
	if !errors.As(err, &ec) || ec.code != 1 {
		t.Fatalf("expected exitCodeError{code:1}, got %v\n%s", err, out.String())
	}

	m, readErr := manifest.ReadJSON(repoManifestPath(repo.Path))
	if readErr != nil {
		t.Fatalf("read manifest: %v", readErr)
	}
	if len(m) == 0 {
		t.Errorf("expected at least one finding in manifest, got 0")
	}
}

// TestScanCmd_PositionalDetectors_RunsOnlyThose pins that named detectors
// override the safe default — `scan gitignored` runs gitignored only.
func TestScanCmd_PositionalDetectors_RunsOnlyThose(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "AWS_ACCESS_KEY_ID=AKIAZT5K7YFAPXR3VBCD")
	repo.AddAndCommit("seed")
	// No .gitignore — gitignored has nothing to flag.

	var out bytes.Buffer
	cmd := newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-C", repo.Path, "scan", "gitignored"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan gitignored: %v\n%s", err, out.String())
	}

	m, _ := manifest.ReadJSON(repoManifestPath(repo.Path))
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
	cmd.SetArgs([]string{"-C", repo.Path, "scan", "not-a-detector"})

	err := cmd.Execute()
	var ec *exitCodeError
	if !errors.As(err, &ec) || ec.code != 2 {
		t.Errorf("expected exitCodeError{code:2}, got %v", err)
	}
}

// TestScanCmd_ExitsZeroWhenNothingNew pins idempotency — running scan
// twice against a clean repo returns 0 each time.
func TestScanCmd_ExitsZeroWhenNothingNew(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("README.md", "hello")
	repo.AddAndCommit("seed")

	var out bytes.Buffer
	cmd := newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-C", repo.Path, "scan"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("first scan returned non-nil: %v", err)
	}

	out.Reset()
	cmd = newScanTestCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-C", repo.Path, "scan"})
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
	cmd.SetArgs([]string{"-C", repo.Path, "scan", "gitignored", "--json"})

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
	bindRepoFlag(root)
	l := &cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: runList}
	se := &cobra.Command{Use: "search <glob>...", Args: cobra.MinimumNArgs(1), RunE: runSearch}
	rm := &cobra.Command{Use: "remove <glob>...", Args: cobra.MinimumNArgs(1), RunE: runRemove}
	su := &cobra.Command{Use: "summary", Args: cobra.NoArgs, RunE: runSummary}
	root.AddCommand(l, se, rm, su)
	return root
}

func TestListCmd_PrintsTabSeparated(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	m := domain.NewManifest()
	m.Add(&domain.Finding{BlobHash: "abc1234567890", Type: domain.FindingTypeSecret, Path: "secrets.env", Size: 42})
	m.Add(&domain.Finding{BlobHash: "def4567890123", Type: domain.FindingTypeBinary, Path: "bin/app", Size: 1024})
	if err := manifest.WriteJSON(m, repoManifestPath(repo.Path)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var out bytes.Buffer
	cmd := newReadWriteTestCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"-C", repo.Path, "list"})
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
	mp := repoManifestPath(repo.Path)
	if err := manifest.WriteJSON(m, mp); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	cmd := newReadWriteTestCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"-C", repo.Path, "remove", "vendor/**"})
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

	mp := repoManifestPath(repo.Path)
	if _, err := os.Stat(mp); err == nil {
		t.Fatalf("manifest should not exist yet")
	}

	var out bytes.Buffer
	cmd := newReadWriteTestCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"-C", repo.Path, "search", "*.env"})
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
	if err := manifest.WriteJSON(m, repoManifestPath(repo.Path)); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	cmd := newReadWriteTestCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"-C", repo.Path, "summary"})
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

func TestValidateDetectors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"empty", []string{}, false},
		{"one valid", []string{"secrets"}, false},
		{"two valid", []string{"secrets", "gitignored"}, false},
		{"all", []string{"all"}, false},
		{"one invalid", []string{"not-a-detector"}, true},
		{"valid then invalid", []string{"secrets", "not-a-detector"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDetectors(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("err mismatch: got %v want err=%v", err, tt.wantErr)
			}
		})
	}
}
