package gitquery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/benjaminabbitt/git-expunge/tests/testutil"
)

func TestCheckIgnored_RootGitignore(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("add gitignore")

	got, err := CheckIgnored(repo.Path, []string{"creds.env", "notes.txt"})
	if err != nil {
		t.Fatalf("CheckIgnored failed: %v", err)
	}

	match, ok := got["creds.env"]
	if !ok {
		t.Fatalf("expected creds.env to be ignored, got map=%v", got)
	}
	if match.Pattern != "*.env" {
		t.Errorf("expected pattern '*.env', got %q", match.Pattern)
	}
	if filepath.Base(match.Source) != ".gitignore" {
		t.Errorf("expected source to be .gitignore, got %q", match.Source)
	}
	if _, ok := got["notes.txt"]; ok {
		t.Errorf("notes.txt should not be ignored")
	}
}

func TestCheckIgnored_SubdirectoryGitignore(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("sub/.gitignore", "*.log\n")
	repo.AddAndCommit("add subdir gitignore")

	got, err := CheckIgnored(repo.Path, []string{"sub/app.log", "top.log"})
	if err != nil {
		t.Fatalf("CheckIgnored failed: %v", err)
	}

	if _, ok := got["sub/app.log"]; !ok {
		t.Errorf("sub/app.log should be ignored by sub/.gitignore, got map=%v", got)
	}
	if _, ok := got["top.log"]; ok {
		t.Errorf("top.log should not be ignored (subdir gitignore only applies under sub/)")
	}
}

func TestCheckIgnored_GitInfoExclude(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	// Seed at least one commit so the repo is fully usable.
	repo.WriteFile("README.md", "hello")
	repo.AddAndCommit("init")

	excludePath := filepath.Join(repo.Path, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		t.Fatalf("mkdir info: %v", err)
	}
	if err := os.WriteFile(excludePath, []byte("*.cache\n"), 0o644); err != nil {
		t.Fatalf("write exclude: %v", err)
	}

	got, err := CheckIgnored(repo.Path, []string{"data.cache", "data.txt"})
	if err != nil {
		t.Fatalf("CheckIgnored failed: %v", err)
	}

	match, ok := got["data.cache"]
	if !ok {
		t.Fatalf("data.cache should be ignored via .git/info/exclude, got map=%v", got)
	}
	if match.Pattern != "*.cache" {
		t.Errorf("expected pattern '*.cache', got %q", match.Pattern)
	}
	if _, ok := got["data.txt"]; ok {
		t.Errorf("data.txt should not be ignored")
	}
}

func TestCheckIgnored_RespectsNegation(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile(".gitignore", "*.env\n!keep.env\n")
	repo.AddAndCommit("add gitignore with negation")

	got, err := CheckIgnored(repo.Path, []string{"creds.env", "keep.env"})
	if err != nil {
		t.Fatalf("CheckIgnored failed: %v", err)
	}

	if _, ok := got["creds.env"]; !ok {
		t.Errorf("creds.env should be ignored, got map=%v", got)
	}
	if _, ok := got["keep.env"]; ok {
		t.Errorf("keep.env should NOT be ignored (negation), got match=%v", got["keep.env"])
	}
}

func TestCheckIgnored_EmptyInput(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("add gitignore")

	got, err := CheckIgnored(repo.Path, nil)
	if err != nil {
		t.Fatalf("CheckIgnored with nil paths failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result for nil paths, got %v", got)
	}

	got, err = CheckIgnored(repo.Path, []string{})
	if err != nil {
		t.Fatalf("CheckIgnored with empty slice failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result for empty slice, got %v", got)
	}
}

func TestCheckIgnored_NoMatches(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("add gitignore")

	got, err := CheckIgnored(repo.Path, []string{"a.txt", "b.md"})
	if err != nil {
		t.Fatalf("CheckIgnored failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
}
