package rewriter

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// GitExecutor abstracts git command execution for testing.
type GitExecutor interface {
	// FastExport runs git fast-export and returns a reader for the output.
	FastExport(repoPath string) (io.ReadCloser, error)
	// FastImport runs git fast-import and returns a writer for the input.
	FastImport(repoPath string) (io.WriteCloser, func() error, error)
	// InitBare initializes a bare repository.
	InitBare(path string) error
	// GC runs garbage collection.
	GC(repoPath string) error
}

// RealGitExecutor implements GitExecutor using actual git commands.
type RealGitExecutor struct{}

func (e *RealGitExecutor) FastExport(repoPath string) (io.ReadCloser, error) {
	cmd := exec.Command("git", "-C", repoPath, "fast-export", "--all", "--show-original-ids")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &cmdReadCloser{Reader: stdout, cmd: cmd}, nil
}

func (e *RealGitExecutor) FastImport(repoPath string) (io.WriteCloser, func() error, error) {
	cmd := exec.Command("git", "-C", repoPath, "fast-import", "--force")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return stdin, cmd.Wait, nil
}

func (e *RealGitExecutor) InitBare(path string) error {
	return exec.Command("git", "init", "--bare", path).Run()
}

func (e *RealGitExecutor) GC(repoPath string) error {
	// 1. Expire all reflog entries immediately
	// This prevents reflogs from keeping old objects reachable
	exec.Command("git", "-C", repoPath, "reflog", "expire", "--expire=now", "--all").Run()

	// 2. Remove refs/original/* and other backup refs using go-git
	if err := deleteBackupRefs(repoPath); err != nil {
		// Non-fatal, continue with cleanup
		fmt.Fprintf(os.Stderr, "warning: failed to delete backup refs: %v\n", err)
	}

	// 3. Clear stash (might reference old commits)
	exec.Command("git", "-C", repoPath, "stash", "clear").Run()

	// 4. Remove remote tracking refs that might reference old commits
	exec.Command("git", "-C", repoPath, "remote", "prune", "origin").Run()

	// 5. Prune unreachable objects first (before repack)
	// Use --expire=now to prune immediately without the 2-week grace period
	pruneCmd := exec.Command("git", "-C", repoPath, "prune", "--expire=now")
	if output, err := pruneCmd.CombinedOutput(); err != nil {
		// Non-fatal, continue - gc will also prune
		fmt.Fprintf(os.Stderr, "warning: prune failed: %v\nOutput: %s\n", err, string(output))
	}

	// 6. Run garbage collection
	// Use --prune=now to immediately remove unreachable objects
	// Don't use --aggressive on first pass - it's slower and may fail on partially cleaned repos
	gcCmd := exec.Command("git", "-C", repoPath, "gc", "--prune=now")
	output, err := gcCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gc failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// deleteBackupRefs uses go-git to remove refs/original/* and other backup refs
func deleteBackupRefs(repoPath string) error {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return err
	}

	// Collect refs to delete
	var refsToDelete []plumbing.ReferenceName
	refs, err := repo.References()
	if err != nil {
		return err
	}

	err = refs.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().String()
		// Delete refs/original/*, refs/backup/*, etc.
		if strings.HasPrefix(name, "refs/original/") ||
			strings.HasPrefix(name, "refs/backup/") {
			refsToDelete = append(refsToDelete, ref.Name())
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Delete collected refs
	for _, refName := range refsToDelete {
		if err := repo.Storer.RemoveReference(refName); err != nil {
			// Continue on error, try to delete as many as possible
			fmt.Fprintf(os.Stderr, "warning: failed to delete ref %s: %v\n", refName, err)
		}
	}

	return nil
}


type cmdReadCloser struct {
	io.Reader
	cmd *exec.Cmd
}

func (c *cmdReadCloser) Close() error {
	return c.cmd.Wait()
}

// Rewriter handles git history rewriting.
type Rewriter struct {
	repoPath string
	dryRun   bool
	git      GitExecutor
}

// NewRewriter creates a new Rewriter for the given repository.
func NewRewriter(repoPath string) *Rewriter {
	return &Rewriter{
		repoPath: repoPath,
		dryRun:   true, // Default to dry-run
		git:      &RealGitExecutor{},
	}
}

// WithGitExecutor sets a custom git executor (for testing).
func (r *Rewriter) WithGitExecutor(git GitExecutor) *Rewriter {
	r.git = git
	return r
}

// SetDryRun sets whether to run in dry-run mode.
func (r *Rewriter) SetDryRun(dryRun bool) {
	r.dryRun = dryRun
}

// Rewrite removes the specified blobs from repository history.
func (r *Rewriter) Rewrite(blobHashes []string) (*Stats, error) {
	if len(blobHashes) == 0 {
		return &Stats{}, nil
	}

	// Create pipeline
	pipeline := NewPipelineWithStats(blobHashes)

	if r.dryRun {
		return r.dryRunRewrite(pipeline)
	}

	return r.executeRewrite(pipeline)
}

func (r *Rewriter) dryRunRewrite(pipeline *PipelineWithStats) (*Stats, error) {
	// Run fast-export and process through pipeline, but discard output
	reader, err := r.git.FastExport(r.repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to start fast-export: %w", err)
	}
	defer reader.Close()

	// Process through pipeline, discard output
	if err := pipeline.Process(reader, io.Discard); err != nil {
		return nil, fmt.Errorf("pipeline failed: %w", err)
	}

	return &pipeline.Stats, nil
}

func (r *Rewriter) executeRewrite(pipeline *PipelineWithStats) (*Stats, error) {
	// Create a temporary directory for the new repo
	tempDir, err := os.MkdirTemp("", "git-expunge-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Initialize a bare repo for import
	newRepoPath := filepath.Join(tempDir, "repo.git")
	if err := r.git.InitBare(newRepoPath); err != nil {
		return nil, fmt.Errorf("failed to init temp repo: %w", err)
	}

	// Run fast-export on original repo
	exportReader, err := r.git.FastExport(r.repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to start fast-export: %w", err)
	}

	// Run fast-import on new repo
	importWriter, waitImport, err := r.git.FastImport(newRepoPath)
	if err != nil {
		exportReader.Close()
		return nil, fmt.Errorf("failed to start fast-import: %w", err)
	}

	// Process through pipeline
	if err := pipeline.Process(exportReader, importWriter); err != nil {
		importWriter.Close()
		exportReader.Close()
		waitImport()
		return nil, fmt.Errorf("pipeline failed: %w", err)
	}

	// Close streams and wait for commands
	importWriter.Close()
	exportReader.Close()

	if err := waitImport(); err != nil {
		return nil, fmt.Errorf("fast-import failed: %w", err)
	}

	// Replace original repo's objects with the new ones
	if err := r.replaceObjects(newRepoPath); err != nil {
		return nil, fmt.Errorf("failed to replace objects: %w", err)
	}

	// Reset working tree to match the new HEAD
	if err := r.resetWorkingTree(); err != nil {
		// Non-fatal - might be a bare repo
		fmt.Fprintf(os.Stderr, "warning: failed to reset working tree: %v\n", err)
	}

	// Run garbage collection to clean up
	if err := r.git.GC(r.repoPath); err != nil {
		return nil, fmt.Errorf("failed to run gc: %w", err)
	}

	// Verify repository integrity after rewrite
	fsckCmd := exec.Command("git", "-C", r.repoPath, "fsck", "--no-dangling")
	if output, err := fsckCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("repository integrity check failed: %w\nOutput: %s", err, string(output))
	}

	return &pipeline.Stats, nil
}

func (r *Rewriter) replaceObjects(newRepoPath string) error {
	// Get the .git directory path
	gitDir := r.repoPath
	if filepath.Base(gitDir) != ".git" {
		gitDir = filepath.Join(r.repoPath, ".git")
	}

	// Check if it's a bare repo or working directory
	if _, err := os.Stat(filepath.Join(r.repoPath, "objects")); err == nil {
		gitDir = r.repoPath // It's a bare repo
	}

	// Backup old refs
	oldRefsDir := filepath.Join(gitDir, "refs.old")
	refsDir := filepath.Join(gitDir, "refs")
	if err := os.Rename(refsDir, oldRefsDir); err != nil {
		return fmt.Errorf("failed to backup refs: %w", err)
	}

	// Copy new refs
	newRefsDir := filepath.Join(newRepoPath, "refs")
	if err := copyDir(newRefsDir, refsDir); err != nil {
		os.Rename(oldRefsDir, refsDir) // Restore on error
		return fmt.Errorf("failed to copy new refs: %w", err)
	}

	// Handle packed-refs: copy from new repo OR delete if new repo doesn't have it
	packedRefs := filepath.Join(gitDir, "packed-refs")
	newPackedRefs := filepath.Join(newRepoPath, "packed-refs")
	if _, err := os.Stat(newPackedRefs); err == nil {
		// New repo has packed-refs, copy it
		if err := copyFile(newPackedRefs, packedRefs); err != nil {
			return fmt.Errorf("failed to copy packed-refs: %w", err)
		}
	} else {
		// New repo doesn't have packed-refs, delete the original
		// This is critical: old packed-refs contains refs to commits that no longer exist
		os.Remove(packedRefs)
	}

	// Delete special ref files that may reference old commits
	// These files are not part of refs/ but are in the git directory
	specialRefs := []string{
		"FETCH_HEAD",
		"ORIG_HEAD",
		"MERGE_HEAD",
		"CHERRY_PICK_HEAD",
		"REVERT_HEAD",
		"BISECT_LOG",
		"BISECT_EXPECTED_REV",
		"BISECT_ANCESTORS_OK",
		"BISECT_NAMES",
		"AUTO_MERGE",
	}
	for _, ref := range specialRefs {
		os.Remove(filepath.Join(gitDir, ref))
	}

	// Delete the index file - it references old tree/blob hashes
	// git reset --hard will recreate it with correct references
	os.Remove(filepath.Join(gitDir, "index"))

	// Delete logs directory (reflogs) - they reference old commits
	// GC will also expire reflogs, but deleting them here is cleaner
	os.RemoveAll(filepath.Join(gitDir, "logs"))

	// Handle worktrees - they have their own index and logs that reference old commits
	worktreesDir := filepath.Join(gitDir, "worktrees")
	if entries, err := os.ReadDir(worktreesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				wtDir := filepath.Join(worktreesDir, entry.Name())
				// Delete worktree index
				os.Remove(filepath.Join(wtDir, "index"))
				// Delete worktree logs
				os.RemoveAll(filepath.Join(wtDir, "logs"))
				// Note: We keep HEAD - it's a symref to a branch, which should still be valid
				// The worktree will need to be reset after the rewrite
			}
		}
	}

	// NOTE: We intentionally do NOT copy HEAD from the new repo.
	// The new repo was created with `git init --bare` which defaults HEAD to refs/heads/master.
	// The original repo's HEAD is a symbolic ref (e.g., "ref: refs/heads/main") pointing to
	// the user's current branch. Since we copy the new refs (which contain the rewritten
	// branch tips), the original HEAD remains valid and points to the correct branch.
	// Copying HEAD from the new repo would incorrectly change the user's current branch.

	// Replace objects directory entirely
	objectsDir := filepath.Join(gitDir, "objects")
	objectsOldDir := filepath.Join(gitDir, "objects.old")
	newObjectsDir := filepath.Join(newRepoPath, "objects")

	// Backup old objects
	if err := os.Rename(objectsDir, objectsOldDir); err != nil {
		return fmt.Errorf("failed to backup objects: %w", err)
	}

	// Copy new objects
	if err := copyDir(newObjectsDir, objectsDir); err != nil {
		os.Rename(objectsOldDir, objectsDir) // Restore on error
		return fmt.Errorf("failed to copy objects: %w", err)
	}

	// Remove old objects backup
	os.RemoveAll(objectsOldDir)

	// Remove old refs backup
	os.RemoveAll(oldRefsDir)

	return nil
}

// resetWorkingTree resets the working tree to match the new HEAD.
// This is necessary after replacing objects because the old index
// references tree/blob hashes that may no longer exist.
func (r *Rewriter) resetWorkingTree() error {
	// Get the .git directory path
	gitDir := r.repoPath
	isBareRepo := false

	if filepath.Base(gitDir) == ".git" {
		// Already pointing to .git directory, bare repo
		isBareRepo = true
	} else if _, err := os.Stat(filepath.Join(r.repoPath, ".git")); err != nil {
		// Check if it's a bare repo
		if _, err := os.Stat(filepath.Join(r.repoPath, "objects")); err == nil {
			isBareRepo = true
		}
	} else {
		gitDir = filepath.Join(r.repoPath, ".git")
	}

	// Reset the main working tree if not bare
	if !isBareRepo {
		cmd := exec.Command("git", "-C", r.repoPath, "reset", "--hard", "HEAD")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git reset failed: %w\nOutput: %s", err, string(output))
		}
	}

	// Reset all worktrees
	worktreesDir := filepath.Join(gitDir, "worktrees")
	if entries, err := os.ReadDir(worktreesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				wtDir := filepath.Join(worktreesDir, entry.Name())
				// Read the gitdir file to find the worktree path
				gitdirPath := filepath.Join(wtDir, "gitdir")
				if gitdirContent, err := os.ReadFile(gitdirPath); err == nil {
					// gitdir contains path to .git file in worktree
					wtGitPath := strings.TrimSpace(string(gitdirContent))
					wtPath := filepath.Dir(wtGitPath)

					// Check if worktree directory exists
					if _, err := os.Stat(wtPath); err == nil {
						cmd := exec.Command("git", "-C", wtPath, "reset", "--hard", "HEAD")
						if output, err := cmd.CombinedOutput(); err != nil {
							fmt.Fprintf(os.Stderr, "warning: failed to reset worktree %s: %v\nOutput: %s\n",
								wtPath, err, string(output))
						}
					}
				}
			}
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	dest, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dest.Close()

	_, err = io.Copy(dest, source)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return copyFile(path, dstPath)
	})
}
