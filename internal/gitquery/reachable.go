package gitquery

import (
	"errors"
	"os/exec"
)

// IsReachable reports whether the given blob hash is still reachable in
// the repository — i.e. whether `git cat-file -e <hash>` succeeds. After
// a successful rewrite (and gc) a purged blob should NOT be reachable.
//
// Returns false on any non-zero exit from git (including the "object
// missing" case, which is what we want to detect). Returns an error only
// for context failures like git not being installed.
func IsReachable(repoPath, blobHash string) (bool, error) {
	cmd := exec.Command("git", "-C", repoPath, "cat-file", "-e", blobHash)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Any non-zero exit means git couldn't find the object — i.e.
		// it's unreachable. Not an error from our perspective.
		return false, nil
	}
	return false, err
}
