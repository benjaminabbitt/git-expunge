package gitquery

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitCommonDir resolves the canonical git directory for a repository,
// handling normal repos, linked worktrees, and bare repos uniformly.
//
//   - Normal repo at /foo:     /foo/.git
//   - Linked worktree at /bar: /main/.git   (the common dir, NOT /bar/.git
//     which is the per-worktree file pointer)
//   - Bare repo at /foo.git:   /foo.git
//
// git-expunge persists its own state (manifest, post-rewrite audit) under
// <common-dir>/git-expunge/, so this resolution is the load-bearing
// step that lets the same workflow work across all three layouts.
//
// Implemented via `git rev-parse --git-common-dir`. Output is normalised
// to an absolute path so callers can join filenames without worrying
// about the working directory.
func GitCommonDir(repoPath string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir in %s: %w", repoPath, err)
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		// `--git-common-dir` is relative to the working directory git
		// chose, which is the -C target. Join against repoPath, not the
		// caller's cwd.
		path = filepath.Join(repoPath, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}
	return filepath.Clean(abs), nil
}
