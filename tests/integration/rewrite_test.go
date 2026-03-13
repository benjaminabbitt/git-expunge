package integration

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/rewriter"
	"github.com/benjaminabbitt/git-expunge/internal/scanner"
	"github.com/benjaminabbitt/git-expunge/tests/integration/fixtures"
)

func TestRewriter_DryRun(t *testing.T) {
	repo := fixtures.RepoWithBinary(t)

	// First scan to find the binary
	config := scanner.DefaultConfig()
	config.SizeThreshold = 10 * 1024 // 10KB for test
	config.ScanSecrets = false

	s := scanner.New(config)
	manifest, err := s.Scan(repo.Path)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(manifest) == 0 {
		t.Fatal("expected to find at least one binary")
	}

	// Get blob hashes to exclude
	var blobHashes []string
	for hash := range manifest {
		blobHashes = append(blobHashes, hash)
	}

	// Run rewriter in dry-run mode
	rw := rewriter.NewRewriter(repo.Path)
	rw.SetDryRun(true)

	stats, err := rw.Rewrite(blobHashes)
	if err != nil {
		t.Fatalf("Rewrite failed: %v", err)
	}

	// Should have processed some blobs
	if stats.TotalBlobs == 0 {
		t.Error("expected to process some blobs")
	}

	// Should have excluded the binary
	if stats.ExcludedBlobs == 0 {
		t.Error("expected to exclude at least one blob")
	}

	// Should have modified at least one commit
	if stats.ModifiedCommits == 0 {
		t.Error("expected to modify at least one commit")
	}

	t.Logf("Stats: TotalBlobs=%d, ExcludedBlobs=%d, TotalCommits=%d, ModifiedCommits=%d",
		stats.TotalBlobs, stats.ExcludedBlobs, stats.TotalCommits, stats.ModifiedCommits)
}

func TestRewriter_EmptyExcludeList(t *testing.T) {
	repo := fixtures.RepoWithBinary(t)

	rw := rewriter.NewRewriter(repo.Path)
	rw.SetDryRun(true)

	stats, err := rw.Rewrite([]string{})
	if err != nil {
		t.Fatalf("Rewrite failed: %v", err)
	}

	// Should return empty stats for empty input
	if stats.TotalBlobs != 0 && stats.ExcludedBlobs != 0 {
		t.Error("expected empty stats for empty exclude list")
	}
}

// TestRewriter_PreservesHead tests that rewriting preserves the original HEAD reference.
// This is a regression test for a bug where rewriting would change HEAD from the original
// branch (e.g., "main") to "master" because git init --bare defaults HEAD to master.
func TestRewriter_PreservesHead(t *testing.T) {
	// Create repo with HEAD pointing to "main", not "master"
	repo := fixtures.RepoWithNonMasterHead(t)

	// Verify initial state: HEAD should be on main
	initialBranch := strings.TrimSpace(repo.CurrentBranch())
	if initialBranch != "main" {
		t.Fatalf("expected initial branch to be 'main', got '%s'", initialBranch)
	}

	// Count commits on main before rewrite
	mainCommitsBefore := strings.TrimSpace(repo.Git("rev-list", "--count", "main"))

	// Scan for binaries to purge
	config := scanner.DefaultConfig()
	config.SizeThreshold = 10 * 1024 // 10KB for test
	config.ScanSecrets = false

	s := scanner.New(config)
	manifest, err := s.Scan(repo.Path)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(manifest) == 0 {
		t.Fatal("expected to find at least one binary")
	}

	// Get blob hashes to exclude
	var blobHashes []string
	for hash := range manifest {
		blobHashes = append(blobHashes, hash)
	}

	// Run rewriter with execute mode (not dry-run)
	rw := rewriter.NewRewriter(repo.Path)
	rw.SetDryRun(false)

	stats, err := rw.Rewrite(blobHashes)
	if err != nil {
		t.Fatalf("Rewrite failed: %v", err)
	}

	t.Logf("Rewrite stats: TotalBlobs=%d, ExcludedBlobs=%d, ModifiedCommits=%d",
		stats.TotalBlobs, stats.ExcludedBlobs, stats.ModifiedCommits)

	// Verify HEAD still points to main (not master)
	afterBranch := strings.TrimSpace(repo.CurrentBranch())
	if afterBranch != "main" {
		t.Errorf("HEAD changed from 'main' to '%s' after rewrite - HEAD was not preserved", afterBranch)
	}

	// Verify commit count on main is preserved (hashes will change, count should not)
	mainCommitsAfter := strings.TrimSpace(repo.Git("rev-list", "--count", "main"))
	if mainCommitsBefore != mainCommitsAfter {
		t.Errorf("commit count on main changed: before=%s, after=%s",
			mainCommitsBefore, mainCommitsAfter)
	}
}
