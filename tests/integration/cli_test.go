package integration

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/benjaminabbitt/git-expunge/tests/testutil"
)

// CLI integration tests: build the real `git-expunge` binary once, then
// exec it for each scenario. These complement the in-process cobra-level
// tests in cmd/git-expunge/ by exercising the actual process boundary —
// argv parsing, exit codes observed by the kernel, ldflags-injected
// version string, and any future PreRun/PostRun behavior on main().

var (
	binPathOnce sync.Once
	binPath     string
	binPathErr  error
)

// buildBinary builds the git-expunge binary into a temp file once per
// test run. Returns the path to the binary or an error if the build
// failed. Subsequent calls return the cached result.
func buildBinary(t *testing.T) string {
	t.Helper()
	binPathOnce.Do(func() {
		dir, err := os.MkdirTemp("", "git-expunge-cli-bin-")
		if err != nil {
			binPathErr = err
			return
		}
		// Keep the dir alive for the whole test process; the temp dir
		// from t.TempDir() would be cleaned up per-test, which would
		// invalidate the cached binary path.
		out := filepath.Join(dir, "git-expunge")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/git-expunge")
		cmd.Dir = repoRoot(t)
		if output, err := cmd.CombinedOutput(); err != nil {
			binPathErr = errors.New("go build failed: " + err.Error() + "\n" + string(output))
			return
		}
		binPath = out
	})
	if binPathErr != nil {
		t.Fatalf("build git-expunge binary: %v", binPathErr)
	}
	return binPath
}

// repoRoot resolves the project root by walking up from this file's
// directory until it finds go.mod. Lets the test work whether `go test`
// is run from the repo root or from tests/integration/.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod up the tree)")
		}
		dir = parent
	}
}

// runCLI runs the built git-expunge binary with the given args. Returns
// stdout, stderr, exit code, and any exec error. Does NOT t.Fatal on
// non-zero exit codes — tests assert on exit codes directly.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := buildBinary(t)
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	stdout, stderr = outBuf.String(), errBuf.String()
	if err == nil {
		return stdout, stderr, 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout, stderr, exitErr.ExitCode()
	}
	t.Fatalf("exec git-expunge: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	return
}

// TestCLI_Version pins that the binary prints a version string that at
// least starts with the SemVer core. The ldflags-injection should yield
// either "0.1.0", "0.1.0+abc1234", "0.1.0+abc1234.<ts>", or "dev"
// (plain `go build` fallback when ldflags aren't passed).
func TestCLI_Version(t *testing.T) {
	stdout, _, code := runCLI(t, "--version")
	if code != 0 {
		t.Errorf("--version exit code: got %d, want 0", code)
	}
	if !strings.Contains(stdout, "git-expunge version ") {
		t.Errorf("expected 'git-expunge version ...', got: %q", stdout)
	}
}

// TestCLI_ScanWritesUnderGitDir pins the load-bearing path-relocation
// guarantee: after `git-expunge scan`, no manifest exists in the working
// tree, but it does exist under .git/git-expunge/.
func TestCLI_ScanWritesUnderGitDir(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=x")
	repo.AddAndCommit("seed")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env")

	_, _, code := runCLI(t, "-C", repo.Path, "scan")
	if code != 1 {
		// 1 = new findings added; 0 = nothing new.
		t.Errorf("scan exit code: got %d, want 1 (new findings)", code)
	}

	// Working tree should stay clean — no legacy file in repo root.
	if _, err := os.Stat(filepath.Join(repo.Path, "git-expunge-findings.json")); err == nil {
		t.Error("scan must not write the legacy <repo>/git-expunge-findings.json path")
	}
	// Manifest lives under .git/git-expunge/.
	stateFile := filepath.Join(repo.Path, ".git", "git-expunge", "findings.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Errorf("expected manifest at %s, got: %v", stateFile, err)
	}
}

// TestCLI_ExitCodesByScenario pins the three exit-code branches of scan.
func TestCLI_ExitCodesByScenario(t *testing.T) {
	t.Run("0_when_nothing_new", func(t *testing.T) {
		repo := testutil.NewTestRepo(t)
		repo.WriteFile("README.md", "hello")
		repo.AddAndCommit("seed")
		_, _, code := runCLI(t, "-C", repo.Path, "scan")
		if code != 0 {
			t.Errorf("clean repo: exit code = %d, want 0", code)
		}
	})

	t.Run("1_when_findings_added", func(t *testing.T) {
		repo := testutil.NewTestRepo(t)
		repo.WriteFile("secrets.env", "DB=x")
		repo.AddAndCommit("seed")
		repo.WriteFile(".gitignore", "*.env\n")
		repo.AddAndCommit("ignore env")
		_, _, code := runCLI(t, "-C", repo.Path, "scan")
		if code != 1 {
			t.Errorf("repo with findings: exit code = %d, want 1", code)
		}
	})

	t.Run("2_on_unknown_detector", func(t *testing.T) {
		repo := testutil.NewTestRepo(t)
		repo.WriteFile("a", "b")
		repo.AddAndCommit("seed")
		_, _, code := runCLI(t, "-C", repo.Path, "scan", "not-a-detector")
		if code != 2 {
			t.Errorf("bad detector: exit code = %d, want 2", code)
		}
	})
}

// TestCLI_RepoFlagOperatesOnRepoElsewhere pins that -C resolves to the
// target repo's .git/, not the caller's cwd. Catches regressions if a
// command stops routing through repoPathFlag.
func TestCLI_RepoFlagOperatesOnRepoElsewhere(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=x")
	repo.AddAndCommit("seed")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env")

	// Run from a directory that is not the target repo.
	cwd := t.TempDir()
	bin := buildBinary(t)
	cmd := exec.Command(bin, "-C", repo.Path, "scan")
	cmd.Dir = cwd
	if err := cmd.Run(); err != nil {
		// Exit code 1 (findings added) is expected; treat any other
		// error as a real failure.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("scan: %v", err)
		}
	}

	// Manifest must land in the -C target.
	target := filepath.Join(repo.Path, ".git", "git-expunge", "findings.json")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected manifest at -C target %s, got: %v", target, err)
	}
	// Nothing should exist in the (unrelated) caller cwd.
	if _, err := os.Stat(filepath.Join(cwd, ".git", "git-expunge", "findings.json")); err == nil {
		t.Error("manifest leaked into caller's cwd")
	}
}

// TestCLI_JSONFlag pins that --json emits a parseable array of findings
// and suppresses the human-readable summary.
func TestCLI_JSONFlag(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=x")
	repo.AddAndCommit("seed")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env")

	stdout, _, code := runCLI(t, "-C", repo.Path, "scan", "gitignored", "--json")
	if code != 1 {
		t.Errorf("expected exit 1 (findings added), got %d", code)
	}
	if strings.Contains(stdout, "Added ") || strings.Contains(stdout, "Manifest:") {
		t.Errorf("--json should suppress the summary, got: %q", stdout)
	}

	var findings []map[string]any
	if err := json.Unmarshal([]byte(stdout), &findings); err != nil {
		t.Fatalf("--json should emit a JSON array, decode failed: %v\nstdout: %q", err, stdout)
	}
	if len(findings) == 0 {
		t.Error("expected at least one finding in the JSON array")
	}
}

// TestCLI_ListAndSummary pins the read commands against a manifest
// scan just produced. Exercises the full round-trip:
//
//	scan → manifest on disk → list/summary reads it back.
func TestCLI_ListAndSummary(t *testing.T) {
	repo := testutil.NewTestRepo(t)
	repo.WriteFile("secrets.env", "DB=x")
	repo.AddAndCommit("seed")
	repo.WriteFile(".gitignore", "*.env\n")
	repo.AddAndCommit("ignore env")

	scanOut, scanErr, scanCode := runCLI(t, "-C", repo.Path, "scan")
	if scanCode != 1 {
		t.Fatalf("scan setup: exit=%d (want 1)\nstdout: %s\nstderr: %s", scanCode, scanOut, scanErr)
	}

	stdout, stderr, code := runCLI(t, "-C", repo.Path, "list")
	if code != 0 {
		t.Errorf("list: exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "secrets.env") {
		t.Errorf("list should mention secrets.env, got stdout: %q\nstderr: %q", stdout, stderr)
	}

	stdout, stderr, code = runCLI(t, "-C", repo.Path, "summary")
	if code != 0 {
		t.Errorf("summary: exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Total: ") {
		t.Errorf("summary should print a Total, got stdout: %q\nstderr: %q", stdout, stderr)
	}
}
