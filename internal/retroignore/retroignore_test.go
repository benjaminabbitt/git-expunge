package retroignore

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/tests/testutil"
)

func TestBuildManifest_ScrubsIgnoredHistoricalBlob(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB_PASSWORD=hunter2")
	repo.AddAndCommit("oops")

	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("add gitignore")

	m, err := BuildManifest(repo.Path, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	if len(m) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(m), m)
	}

	var f *domain.Finding
	for _, v := range m {
		f = v
	}
	if f.Path != "secrets.env" {
		t.Errorf("expected path 'secrets.env', got %q", f.Path)
	}
	if f.Type != domain.FindingTypeAdd {
		t.Errorf("expected type 'add', got %q", f.Type)
	}
	if !strings.Contains(f.Rule, "gitignore") || !strings.Contains(f.Rule, "*.env") {
		t.Errorf("Rule should mention gitignore and *.env, got %q", f.Rule)
	}
	if len(f.Commits) == 0 {
		t.Errorf("expected at least one commit, got none")
	}
}

func TestBuildManifest_HonorsSubdirGitignore(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("sub/foo.log", "log entry")
	repo.WriteFile("top.log", "also a log")
	repo.AddAndCommit("commit logs")

	repo.WriteFile("sub/.gitignore", "*.log\n")
	repo.AddAndCommit("ignore logs in sub")

	m, err := BuildManifest(repo.Path, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	gotPaths := make(map[string]bool)
	for _, f := range m {
		gotPaths[f.Path] = true
	}

	if !gotPaths["sub/foo.log"] {
		t.Errorf("expected sub/foo.log in manifest, got %v", gotPaths)
	}
	if gotPaths["top.log"] {
		t.Errorf("top.log should not be in manifest (subdir gitignore only applies to sub/)")
	}
}

func TestBuildManifest_MergesIntoExistingManifest_PreservesExisting(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB_PASSWORD=hunter2")
	repo.AddAndCommit("oops")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("add gitignore")

	existing := domain.NewManifest()
	existing["preexisting-binary-hash"] = &domain.Finding{
		BlobHash: "preexisting-binary-hash",
		Type:     domain.FindingTypeBinary,
		Path:     "large.bin",
	}

	m, err := BuildManifest(repo.Path, existing)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	pre, ok := m["preexisting-binary-hash"]
	if !ok {
		t.Fatalf("pre-existing finding was dropped from merged manifest")
	}
	if pre.Type != domain.FindingTypeBinary || pre.Path != "large.bin" {
		t.Errorf("pre-existing finding was modified: %+v", pre)
	}

	var found bool
	for hash, f := range m {
		if hash == "preexisting-binary-hash" {
			continue
		}
		if f.Path == "secrets.env" && f.Type == domain.FindingTypeAdd {
			found = true
		}
	}
	if !found {
		t.Errorf("expected secrets.env finding to be added to merged manifest, got %+v", m)
	}
}

func TestBuildManifest_MergesIntoExistingManifest_DoesNotOverwriteSameHash(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB_PASSWORD=hunter2")
	repo.AddAndCommit("oops")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("add gitignore")

	// First pass: build manifest, then capture the hash of the gitignore-matched blob.
	first, err := BuildManifest(repo.Path, nil)
	if err != nil {
		t.Fatalf("first BuildManifest: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("expected exactly 1 finding in first pass, got %d", len(first))
	}
	var existingHash string
	for h := range first {
		existingHash = h
	}

	// Pre-seed an existing manifest where that same blob was already classified
	// as a binary by a prior scan.
	existing := domain.NewManifest()
	existing[existingHash] = &domain.Finding{
		BlobHash: existingHash,
		Type:     domain.FindingTypeBinary,
		Path:     "secrets.env",
	}

	merged, err := BuildManifest(repo.Path, existing)
	if err != nil {
		t.Fatalf("second BuildManifest: %v", err)
	}

	f := merged[existingHash]
	if f == nil {
		t.Fatalf("merged manifest missing the existing entry")
	}
	if f.Type != domain.FindingTypeBinary {
		t.Errorf("retroignore should not overwrite an existing finding's type; got %q want %q", f.Type, domain.FindingTypeBinary)
	}
}

func TestBuildManifest_DeduplicatesByBlobHash(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	// Same content at two different ignored paths -> same blob hash in git.
	const content = "shared secret content"
	repo.WriteFile("a.env", content)
	repo.WriteFile("b.env", content)
	repo.AddAndCommit("two paths same content")

	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env files")

	m, err := BuildManifest(repo.Path, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	if len(m) != 1 {
		t.Fatalf("expected 1 deduplicated finding (shared blob), got %d: %+v", len(m), m)
	}
}

func TestBuildManifest_NoMatches(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("README.md", "hello")
	repo.AddAndCommit("init")

	m, err := BuildManifest(repo.Path, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty manifest, got %+v", m)
	}
}
