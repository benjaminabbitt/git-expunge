package scanner

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// BlobInfo contains information about a blob encountered during walking.
type BlobInfo struct {
	// Hash is the blob's SHA hash.
	Hash string

	// Path is the file path in the tree.
	Path string

	// Size is the blob size in bytes.
	Size int64

	// CommitHash is the commit where this blob was found.
	CommitHash string

	// Content returns the blob content (lazy loaded).
	Content func() ([]byte, error)
}

// Walker walks through all commits in a repository.
type Walker struct {
	repo *git.Repository
}

// NewWalker creates a new Walker for the given repository path.
func NewWalker(repoPath string) (*Walker, error) {
	// Convert to absolute path to handle relative paths correctly
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	repo, err := git.PlainOpen(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	return &Walker{repo: repo}, nil
}

// BlobHandler is called for each blob encountered during walking.
type BlobHandler func(blob *BlobInfo) error

// Walk walks through all commits and calls the handler for each blob.
func (w *Walker) Walk(handler BlobHandler) error {
	// Track seen blobs to avoid duplicates
	seenBlobs := make(map[plumbing.Hash][]string) // blob hash -> commit hashes

	// Get all references
	refs, err := w.repo.References()
	if err != nil {
		return fmt.Errorf("failed to get references: %w", err)
	}

	// Collect all commit hashes to process
	commitHashes := make(map[plumbing.Hash]bool)

	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() == plumbing.HashReference {
			commitHashes[ref.Hash()] = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to iterate references: %w", err)
	}

	// Also get HEAD
	head, err := w.repo.Head()
	if err == nil {
		commitHashes[head.Hash()] = true
	}

	// Walk each commit and its ancestors
	for hash := range commitHashes {
		if err := w.walkCommit(hash, seenBlobs, handler); err != nil {
			return err
		}
	}

	return nil
}

func (w *Walker) walkCommit(hash plumbing.Hash, seenBlobs map[plumbing.Hash][]string, handler BlobHandler) error {
	// Use commit iterator to walk history
	commitIter, err := w.repo.Log(&git.LogOptions{From: hash, All: false})
	if err != nil {
		// Might not be a valid commit (could be a tag pointing to a tree)
		return nil
	}

	return commitIter.ForEach(func(commit *object.Commit) error {
		return w.processCommit(commit, seenBlobs, handler)
	})
}

func (w *Walker) processCommit(commit *object.Commit, seenBlobs map[plumbing.Hash][]string, handler BlobHandler) error {
	tree, err := commit.Tree()
	if err != nil {
		return fmt.Errorf("failed to get tree for commit %s: %w", commit.Hash.String(), err)
	}

	return tree.Files().ForEach(func(f *object.File) error {
		blobHash := f.Hash
		commitHash := commit.Hash.String()

		// Track which commits contain this blob
		seenBlobs[blobHash] = append(seenBlobs[blobHash], commitHash)

		// Deduplicate: only process each blob once (first time we see it)
		// This ensures the handler is called exactly once per unique blob hash
		if len(seenBlobs[blobHash]) > 1 {
			return nil
		}

		blob, err := w.repo.BlobObject(blobHash)
		if err != nil {
			return fmt.Errorf("failed to get blob %s: %w", blobHash.String(), err)
		}

		info := &BlobInfo{
			Hash:       blobHash.String(),
			Path:       f.Name,
			Size:       blob.Size,
			CommitHash: commitHash,
			Content: func() ([]byte, error) {
				reader, err := blob.Reader()
				if err != nil {
					return nil, err
				}
				defer reader.Close()
				return io.ReadAll(reader)
			},
		}

		return handler(info)
	})
}

// GetCommitsForBlob returns all commits that contain a specific blob.
// This is useful after the initial walk to get full commit lists.
func (w *Walker) GetCommitsForBlob(blobHash string) ([]string, error) {
	var commits []string
	targetHash := plumbing.NewHash(blobHash)

	commitIter, err := w.repo.Log(&git.LogOptions{All: true})
	if err != nil {
		return nil, err
	}

	err = commitIter.ForEach(func(commit *object.Commit) error {
		tree, err := commit.Tree()
		if err != nil {
			return nil
		}

		found := false
		tree.Files().ForEach(func(f *object.File) error {
			if f.Hash == targetHash {
				found = true
			}
			return nil
		})

		if found {
			commits = append(commits, commit.Hash.String())
		}
		return nil
	})

	return commits, err
}
