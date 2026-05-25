// Package retroignore builds an expunge manifest by retroactively applying
// the repository's current gitignore rules to every (path, blob) tuple in
// git history. Any historical blob whose path would today be ignored by
// .gitignore, .git/info/exclude, or core.excludesFile becomes a Finding.
package retroignore

import (
	"fmt"
	"maps"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/gitquery"
)

// blobAtPath holds the per-(path, blob) data we accumulate during the walk.
type blobAtPath struct {
	size    int64
	commits map[string]struct{}
}

// BuildManifest walks every commit in the repository, finds every (path,
// blob) tuple, asks git which of the unique paths would be ignored under
// the current rules, and emits a Finding per matched blob.
//
// If existing is non-nil, the returned manifest contains all of its entries
// (unchanged) plus the new findings; on blob-hash collision the existing
// entry wins so user-curated state (Type, etc.) is preserved.
//
// Complexity: O(B + U) where B is the number of (path, blob) tuples across
// history and U is the number of unique paths. The matcher runs as a single
// `git check-ignore --stdin` subprocess.
func BuildManifest(repoPath string, existing domain.Manifest) (domain.Manifest, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}
	repo, err := git.PlainOpen(absPath)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}

	// pathToBlobs[path][blobHash] = aggregated commit set + size
	pathToBlobs := make(map[string]map[string]*blobAtPath)

	commitIter, err := repo.Log(&git.LogOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("commit log: %w", err)
	}
	err = commitIter.ForEach(func(commit *object.Commit) error {
		tree, err := commit.Tree()
		if err != nil {
			return nil // skip unreadable trees
		}
		commitHash := commit.Hash.String()
		return tree.Files().ForEach(func(f *object.File) error {
			path := f.Name
			hash := f.Hash.String()
			perBlob, ok := pathToBlobs[path]
			if !ok {
				perBlob = make(map[string]*blobAtPath)
				pathToBlobs[path] = perBlob
			}
			entry, ok := perBlob[hash]
			if !ok {
				entry = &blobAtPath{size: f.Size, commits: make(map[string]struct{})}
				perBlob[hash] = entry
			}
			entry.commits[commitHash] = struct{}{}
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("walk commits: %w", err)
	}

	paths := make([]string, 0, len(pathToBlobs))
	for p := range pathToBlobs {
		paths = append(paths, p)
	}

	matches, err := gitquery.CheckIgnored(absPath, paths)
	if err != nil {
		return nil, fmt.Errorf("check ignored: %w", err)
	}

	result := domain.NewManifest()
	maps.Copy(result, existing)

	for path, match := range matches {
		blobs := pathToBlobs[path]
		for hash, info := range blobs {
			if _, exists := result[hash]; exists {
				// Preserve user-curated state from the existing manifest.
				continue
			}
			result[hash] = &domain.Finding{
				BlobHash: hash,
				Type:     domain.FindingTypeAdd,
				Path:     path,
				Size:     info.size,
				Rule:     formatRule(match),
				Commits:  commitSliceFromSet(info.commits),
			}
		}
	}

	return result, nil
}

func formatRule(m gitquery.IgnoreMatch) string {
	return fmt.Sprintf("gitignore:%s:%s", m.Source, m.Pattern)
}

func commitSliceFromSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	return out
}
