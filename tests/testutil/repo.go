// Package testutil provides test utilities for git-expunge tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRepo represents a temporary git repository for testing.
type TestRepo struct {
	Path string
	t    *testing.T
}

// NewTestRepo creates a new temporary git repository for testing.
func NewTestRepo(t *testing.T) *TestRepo {
	t.Helper()

	dir, err := os.MkdirTemp("", "git-expunge-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	repo := &TestRepo{Path: dir, t: t}

	// Initialize git repo
	repo.Git("init")
	repo.Git("config", "user.email", "test@example.com")
	repo.Git("config", "user.name", "Test User")

	t.Cleanup(func() {
		os.RemoveAll(dir)
	})

	return repo
}

// Git runs a git command in the test repository.
func (r *TestRepo) Git(args ...string) string {
	r.t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = r.Path
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// WriteFile writes a file to the repository.
func (r *TestRepo) WriteFile(path, content string) {
	r.t.Helper()

	fullPath := filepath.Join(r.Path, path)
	dir := filepath.Dir(fullPath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		r.t.Fatalf("failed to create directory %s: %v", dir, err)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		r.t.Fatalf("failed to write file %s: %v", path, err)
	}
}

// WriteBinary writes binary content to a file in the repository.
func (r *TestRepo) WriteBinary(path string, content []byte) {
	r.t.Helper()

	fullPath := filepath.Join(r.Path, path)
	dir := filepath.Dir(fullPath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		r.t.Fatalf("failed to create directory %s: %v", dir, err)
	}

	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		r.t.Fatalf("failed to write file %s: %v", path, err)
	}
}

// AddAndCommit stages all changes and creates a commit.
func (r *TestRepo) AddAndCommit(message string) {
	r.t.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-m", message)
}

// Commit creates a commit with the given message (files must be staged).
func (r *TestRepo) Commit(message string) {
	r.t.Helper()
	r.Git("commit", "-m", message)
}

// AddFile stages a specific file.
func (r *TestRepo) AddFile(path string) {
	r.t.Helper()
	r.Git("add", path)
}

// Head returns the current HEAD commit hash.
func (r *TestRepo) Head() string {
	r.t.Helper()
	return r.Git("rev-parse", "HEAD")
}

// CurrentBranch returns the name of the current branch.
func (r *TestRepo) CurrentBranch() string {
	r.t.Helper()
	return r.Git("rev-parse", "--abbrev-ref", "HEAD")
}

// CreateBranch creates and checks out a new branch.
func (r *TestRepo) CreateBranch(name string) {
	r.t.Helper()
	r.Git("checkout", "-b", name)
}

// Checkout checks out an existing branch.
func (r *TestRepo) Checkout(name string) {
	r.t.Helper()
	r.Git("checkout", name)
}
