package scanner

import (
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/tests/testutil"
)

func TestFindBlobsForPath_ExactPath(t *testing.T) {
	repo := testutil.NewTestRepo(t)

	// Create files and commit
	repo.WriteFile("secret.env", "DB_PASSWORD=secret123")
	repo.WriteFile("config.json", "{}")
	repo.AddAndCommit("initial")

	// Find exact path
	findings, err := FindBlobsForPath(repo.Path, "secret.env")
	if err != nil {
		t.Fatalf("FindBlobsForPath failed: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.Path != "secret.env" {
		t.Errorf("expected path 'secret.env', got '%s'", f.Path)
	}
	if f.Type != domain.FindingTypeAdd {
		t.Errorf("expected type 'add', got '%s'", f.Type)
	}
	if !f.Purge {
		t.Error("expected purge=true")
	}
	if f.BlobHash == "" {
		t.Error("expected non-empty blob hash")
	}
}

func TestFindBlobsForPath_GlobPattern(t *testing.T) {
	repo := testutil.NewTestRepo(t)

	// Create files
	repo.WriteFile("config.env", "KEY1=val1")
	repo.WriteFile("local.env", "KEY2=val2")
	repo.WriteFile("readme.md", "# README")
	repo.AddAndCommit("initial")

	// Find glob pattern
	findings, err := FindBlobsForPath(repo.Path, "*.env")
	if err != nil {
		t.Fatalf("FindBlobsForPath failed: %v", err)
	}

	if len(findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(findings))
	}

	// Verify both .env files were found
	paths := make(map[string]bool)
	for _, f := range findings {
		paths[f.Path] = true
	}
	if !paths["config.env"] {
		t.Error("expected to find config.env")
	}
	if !paths["local.env"] {
		t.Error("expected to find local.env")
	}
}

func TestFindBlobsForPath_DoubleStarGlob(t *testing.T) {
	repo := testutil.NewTestRepo(t)

	// Create nested structure
	repo.WriteFile("vendor/package1/main.go", "package main")
	repo.WriteFile("vendor/package2/lib.go", "package lib")
	repo.WriteFile("src/app.go", "package app")
	repo.AddAndCommit("initial")

	// Find double-star glob
	findings, err := FindBlobsForPath(repo.Path, "vendor/**")
	if err != nil {
		t.Fatalf("FindBlobsForPath failed: %v", err)
	}

	if len(findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(findings))
	}

	// Verify vendor files were found
	for _, f := range findings {
		if f.Path != "vendor/package1/main.go" && f.Path != "vendor/package2/lib.go" {
			t.Errorf("unexpected path: %s", f.Path)
		}
	}
}

func TestFindBlobsForPath_TracksCommits(t *testing.T) {
	repo := testutil.NewTestRepo(t)

	// Create file in multiple commits
	repo.WriteFile("secret.env", "V1")
	repo.AddAndCommit("v1")

	repo.WriteFile("secret.env", "V2")
	repo.AddAndCommit("v2")

	// Find the file
	findings, err := FindBlobsForPath(repo.Path, "secret.env")
	if err != nil {
		t.Fatalf("FindBlobsForPath failed: %v", err)
	}

	// Should have 2 findings (different content = different blobs)
	if len(findings) != 2 {
		t.Errorf("expected 2 findings (2 different blob versions), got %d", len(findings))
	}
}

func TestFindBlobsForPath_NoMatch(t *testing.T) {
	repo := testutil.NewTestRepo(t)

	repo.WriteFile("readme.md", "# README")
	repo.AddAndCommit("initial")

	findings, err := FindBlobsForPath(repo.Path, "nonexistent.txt")
	if err != nil {
		t.Fatalf("FindBlobsForPath failed: %v", err)
	}

	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(findings))
	}
}
