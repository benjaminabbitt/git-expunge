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
