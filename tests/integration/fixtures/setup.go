// Package fixtures provides test fixture setup for integration tests.
package fixtures

import (
	"testing"

	"github.com/benjaminabbitt/git-expunge/tests/testutil"
)

// RepoWithSecret creates a test repo containing a secret (AWS key).
func RepoWithSecret(t *testing.T) *testutil.TestRepo {
	t.Helper()
	repo := testutil.NewTestRepo(t)

	// Initial commit
	repo.WriteFile("README.md", "# Test Project\n")
	repo.AddAndCommit("Initial commit")

	// Add a secret - using realistic-looking but fake AWS credentials
	// Note: AKIA prefix is valid for AWS access keys, followed by 16 alphanumeric chars
	repo.WriteFile(".env", "AWS_ACCESS_KEY_ID=AKIAZT5K7YFAPXR3VBCD\nAWS_SECRET_ACCESS_KEY=Hq9GkT8m4pLnRvXwYz1234567890abcdefghijk\n")
	repo.AddAndCommit("Add config")

	// Add more commits
	repo.WriteFile("main.go", "package main\n\nfunc main() {}\n")
	repo.AddAndCommit("Add main")

	return repo
}

// RepoWithBinary creates a test repo containing a binary file.
func RepoWithBinary(t *testing.T) *testutil.TestRepo {
	t.Helper()
	repo := testutil.NewTestRepo(t)

	// Initial commit
	repo.WriteFile("README.md", "# Test Project\n")
	repo.AddAndCommit("Initial commit")

	// Add a binary file (ELF header)
	elfHeader := []byte{
		0x7f, 0x45, 0x4c, 0x46, // ELF magic
		0x02, 0x01, 0x01, 0x00, // 64-bit, little endian, ELF version
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	// Pad to make it larger than typical threshold
	binary := make([]byte, 100*1024) // 100KB
	copy(binary, elfHeader)
	repo.WriteBinary("bin/app", binary)
	repo.AddAndCommit("Add binary")

	return repo
}

// RepoWithSecretAndBinary creates a test repo with both secrets and binaries.
func RepoWithSecretAndBinary(t *testing.T) *testutil.TestRepo {
	t.Helper()
	repo := testutil.NewTestRepo(t)

	// Initial commit
	repo.WriteFile("README.md", "# Test Project\n")
	repo.AddAndCommit("Initial commit")

	// Add secret
	repo.WriteFile("config/secrets.env", "DATABASE_URL=postgres://user:password@localhost/db\nAPI_KEY=sk-proj-1234567890abcdef\n")
	repo.AddAndCommit("Add config")

	// Add binary
	binary := make([]byte, 50*1024)
	binary[0] = 0x7f
	binary[1] = 0x45
	binary[2] = 0x4c
	binary[3] = 0x46
	repo.WriteBinary("build/output", binary)
	repo.AddAndCommit("Add build artifact")

	// More changes
	repo.WriteFile("src/main.go", "package main\n")
	repo.AddAndCommit("Add source")

	return repo
}

// EmptyRepo creates an empty test repo with just an initial commit.
func EmptyRepo(t *testing.T) *testutil.TestRepo {
	t.Helper()
	repo := testutil.NewTestRepo(t)

	repo.WriteFile(".gitkeep", "")
	repo.AddAndCommit("Initial commit")

	return repo
}

// RepoWithNonMasterHead creates a test repo with HEAD pointing to "main" (not "master"),
// containing a binary file that will be detected and purged. This tests that rewrite
// preserves the original HEAD reference.
func RepoWithNonMasterHead(t *testing.T) *testutil.TestRepo {
	t.Helper()
	repo := testutil.NewTestRepo(t)

	// Create initial commit on default branch
	repo.WriteFile("README.md", "# Test Project\n")
	repo.AddAndCommit("Initial commit")

	// Rename branch to main (simulating modern git default)
	repo.Git("branch", "-m", "main")

	// Create a binary file (ELF header) - will be detected for purge
	elfHeader := []byte{
		0x7f, 0x45, 0x4c, 0x46, // ELF magic
		0x02, 0x01, 0x01, 0x00, // 64-bit, little endian, ELF version
	}
	binary := make([]byte, 100*1024) // 100KB
	copy(binary, elfHeader)
	repo.WriteBinary("bin/app", binary)
	repo.AddAndCommit("Add binary")

	// Add more commits on main
	repo.WriteFile("src/main.go", "package main\n\nfunc main() {}\n")
	repo.AddAndCommit("Add main.go")

	// Create an older master branch (diverged earlier)
	repo.Git("checkout", "-b", "master", "HEAD~2")
	repo.WriteFile("old.txt", "old content\n")
	repo.AddAndCommit("Old commit on master")

	// Switch back to main - this is where HEAD should point
	repo.Git("checkout", "main")

	return repo
}
