package scanner

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// FindBlobsForPath finds all blobs in the repository history that match the given path pattern.
// The pattern can be:
//   - A literal path: "vendor/module/file.go"
//   - A glob pattern: "vendor/**", "*.env", "config/*.yaml"
//
// Returns findings with Type=FindingTypeAdd and Purge=true.
func FindBlobsForPath(repoPath, pattern string) ([]*domain.Finding, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	repo, err := git.PlainOpen(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Determine if this is a glob pattern or literal path
	isGlob := strings.ContainsAny(pattern, "*?[")

	// Track unique blobs by hash to avoid duplicates
	// Store path and commits for each blob
	type blobData struct {
		path    string
		commits []string
	}
	blobs := make(map[string]*blobData) // hash -> data

	// Walk all commits
	commitIter, err := repo.Log(&git.LogOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("failed to get commit log: %w", err)
	}

	err = commitIter.ForEach(func(commit *object.Commit) error {
		tree, err := commit.Tree()
		if err != nil {
			return nil // Skip commits with tree errors
		}

		return tree.Files().ForEach(func(f *object.File) error {
			var match bool
			if isGlob {
				match, _ = doublestar.Match(pattern, f.Name)
			} else {
				match = f.Name == pattern
			}

			if match {
				hash := f.Hash.String()
				if existing, ok := blobs[hash]; ok {
					// Add commit if not already present
					existing.commits = appendUnique(existing.commits, commit.Hash.String())
				} else {
					blobs[hash] = &blobData{
						path:    f.Name,
						commits: []string{commit.Hash.String()},
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk commits: %w", err)
	}

	// Convert to findings
	var findings []*domain.Finding
	for hash, data := range blobs {
		findings = append(findings, &domain.Finding{
			BlobHash: hash,
			Type:     domain.FindingTypeAdd,
			Path:     data.path,
			Commits:  data.commits,
			Purge:    true,
		})
	}

	return findings, nil
}

// appendUnique appends a value to a slice if not already present.
func appendUnique(slice []string, val string) []string {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}

// HistoricalFile represents a file that exists in git history.
type HistoricalFile struct {
	Path     string   // File path
	BlobHash string   // Most recent blob hash for this path
	Commits  []string // All commits that contain this path (any version)
	Extant   bool     // True if file exists in HEAD, false if deleted (pure history)
}

// FindAllPathsForBlobs scans the repository history to find all unique paths
// where each of the given blob hashes appears. This is used to detect shared blobs
// (same content appearing at multiple paths due to git's content deduplication).
//
// Returns a map of blob hash -> list of paths where that blob appears.
func FindAllPathsForBlobs(repoPath string, blobHashes []string) (map[string][]string, error) {
	if len(blobHashes) == 0 {
		return make(map[string][]string), nil
	}

	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	repo, err := git.PlainOpen(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Build a set of hashes we're looking for
	targetHashes := make(map[string]bool)
	for _, h := range blobHashes {
		targetHashes[h] = true
	}

	// Map of blob hash -> set of paths (using map for deduplication)
	blobPaths := make(map[string]map[string]bool)
	for _, h := range blobHashes {
		blobPaths[h] = make(map[string]bool)
	}

	// Walk all commits
	commitIter, err := repo.Log(&git.LogOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("failed to get commit log: %w", err)
	}

	err = commitIter.ForEach(func(commit *object.Commit) error {
		tree, err := commit.Tree()
		if err != nil {
			return nil // Skip commits with tree errors
		}

		return tree.Files().ForEach(func(f *object.File) error {
			hash := f.Hash.String()
			if targetHashes[hash] {
				blobPaths[hash][f.Name] = true
			}
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk commits: %w", err)
	}

	// Convert to result format (map of hash -> slice of paths)
	result := make(map[string][]string)
	for hash, pathSet := range blobPaths {
		paths := make([]string, 0, len(pathSet))
		for p := range pathSet {
			paths = append(paths, p)
		}
		result[hash] = paths
	}

	return result, nil
}

// ListHistoricalFiles returns all unique file paths that have ever existed in the repository.
// Files are returned with their most recent blob hash, list of commits, and whether they
// still exist in HEAD (extant) or have been deleted (pure history).
func ListHistoricalFiles(repoPath string) ([]*HistoricalFile, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	repo, err := git.PlainOpen(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Get HEAD tree to check which files are extant
	headFiles := make(map[string]bool)
	headRef, err := repo.Head()
	if err == nil {
		headCommit, err := repo.CommitObject(headRef.Hash())
		if err == nil {
			headTree, err := headCommit.Tree()
			if err == nil {
				headTree.Files().ForEach(func(f *object.File) error {
					headFiles[f.Name] = true
					return nil
				})
			}
		}
	}

	// Track files by path
	type fileData struct {
		blobHash string
		commits  []string
	}
	files := make(map[string]*fileData)

	// Walk all commits
	commitIter, err := repo.Log(&git.LogOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("failed to get commit log: %w", err)
	}

	err = commitIter.ForEach(func(commit *object.Commit) error {
		tree, err := commit.Tree()
		if err != nil {
			return nil // Skip commits with tree errors
		}

		return tree.Files().ForEach(func(f *object.File) error {
			path := f.Name
			commitHash := commit.Hash.String()

			if existing, ok := files[path]; ok {
				existing.commits = appendUnique(existing.commits, commitHash)
			} else {
				files[path] = &fileData{
					blobHash: f.Hash.String(),
					commits:  []string{commitHash},
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk commits: %w", err)
	}

	// Convert to slice
	result := make([]*HistoricalFile, 0, len(files))
	for path, data := range files {
		result = append(result, &HistoricalFile{
			Path:     path,
			BlobHash: data.blobHash,
			Commits:  data.commits,
			Extant:   headFiles[path],
		})
	}

	return result, nil
}
