package integration

import (
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/retroignore"
	"github.com/benjaminabbitt/git-expunge/internal/rewriter"
	"github.com/benjaminabbitt/git-expunge/tests/testutil"
)

// TestRetroignore_EndToEnd_ManifestDrivesRewrite verifies that the manifest
// produced by retroignore is shaped correctly enough for the rewriter to
// accept and act on, across all three gitignore sources (root, subdir, info/
// exclude).
func TestRetroignore_EndToEnd_ManifestDrivesRewrite(t *testing.T) {
	repo := testutil.NewTestRepo(t)

	// Files committed before any ignore rules exist.
	repo.WriteFile("creds.env", "DB_PASSWORD=hunter2")
	repo.WriteFile("sub/app.log", "log entry")
	repo.WriteFile("data/cache.bin", "cached bytes")
	repo.WriteFile("keep.md", "keep me")
	repo.AddAndCommit("initial commit with later-ignored files")

	// Now author the ignore rules from three different sources.
	repo.WriteFile(".gitignore", "*.env\n")
	repo.WriteFile("sub/.gitignore", "*.log\n")
	repo.AddAndCommit("add gitignore files")

	// .git/info/exclude — a third source, not committed but local to the repo.
	repo.WriteFile(".git/info/exclude", "data/*.bin\n")

	// Build the manifest from gitignore rules.
	m, err := retroignore.BuildManifest(repo.Path, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	paths := make(map[string]bool)
	for _, f := range m {
		paths[f.Path] = true
	}

	for _, expect := range []string{"creds.env", "sub/app.log", "data/cache.bin"} {
		if !paths[expect] {
			t.Errorf("expected %q in manifest, got %v", expect, paths)
		}
	}
	if paths["keep.md"] {
		t.Errorf("keep.md must not be in manifest, got %v", paths)
	}

	// The rewriter accepts the manifest's blob hashes in dry-run mode and
	// reports excluded blobs — proving the manifest is interoperable.
	hashes := make([]string, 0, len(m))
	for h := range m {
		hashes = append(hashes, h)
	}

	rw := rewriter.NewRewriter(repo.Path)
	rw.SetDryRun(true)
	stats, err := rw.Rewrite(hashes)
	if err != nil {
		t.Fatalf("Rewrite dry-run: %v", err)
	}
	if stats.ExcludedBlobs == 0 {
		t.Errorf("expected dry-run to report at least one excluded blob, got stats=%+v", stats)
	}
}
