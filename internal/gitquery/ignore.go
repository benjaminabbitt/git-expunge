package gitquery

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// IgnoreMatch describes which gitignore rule caused a path to be ignored.
type IgnoreMatch struct {
	// Path is the input path that was checked.
	Path string
	// Source is the ignore file that contained the matching pattern
	// (e.g. ".gitignore", "sub/.gitignore", ".git/info/exclude",
	// or an absolute path for the global excludefile).
	Source string
	// LineNum is the 1-based line number of the matching pattern in Source.
	LineNum int
	// Pattern is the matching gitignore pattern.
	Pattern string
}

// CheckIgnored asks git which of the given paths would be ignored under the
// repository's current gitignore rules (per-directory .gitignore, .git/info/
// exclude, and core.excludesFile). The check is purely rule-based: paths do
// not need to exist on disk.
//
// Implemented by running `git check-ignore -v -z --no-index --stdin` once,
// piping all paths through stdin so the cost is one subprocess regardless of
// input size.
//
// Returns a map keyed by input path containing only the matched entries.
// Negated patterns produce no entry (i.e. the path is not ignored).
// Empty input returns an empty map without spawning a subprocess.
func CheckIgnored(repoPath string, paths []string) (map[string]IgnoreMatch, error) {
	result := make(map[string]IgnoreMatch)
	if len(paths) == 0 {
		return result, nil
	}

	var stdin bytes.Buffer
	for _, p := range paths {
		stdin.WriteString(p)
		stdin.WriteByte(0)
	}

	cmd := exec.Command("git", "check-ignore", "-v", "-z", "--no-index", "--stdin")
	cmd.Dir = repoPath
	cmd.Stdin = &stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// git check-ignore exits 1 when nothing matched — that's success for us.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return result, nil
		}
		return nil, fmt.Errorf("git check-ignore: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	if err := parseCheckIgnoreOutput(&stdout, result); err != nil {
		return nil, fmt.Errorf("parse check-ignore output: %w", err)
	}
	return result, nil
}

// parseCheckIgnoreOutput consumes the NUL-separated records emitted by
// `git check-ignore -v -z`. Each record is four NUL-terminated fields:
// source, linenum, pattern, path. A negated pattern is reported with a
// leading '!' on the pattern; we treat those as non-matches.
func parseCheckIgnoreOutput(r io.Reader, out map[string]IgnoreMatch) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	fields := bytes.Split(data, []byte{0})
	// Trailing empty field after the final NUL — drop it.
	if len(fields) > 0 && len(fields[len(fields)-1]) == 0 {
		fields = fields[:len(fields)-1]
	}
	if len(fields)%4 != 0 {
		return fmt.Errorf("unexpected field count %d (want multiple of 4)", len(fields))
	}

	for i := 0; i < len(fields); i += 4 {
		source := string(fields[i])
		linenumStr := string(fields[i+1])
		pattern := string(fields[i+2])
		path := string(fields[i+3])

		// Skip negated patterns — git reports them in -v output but they mean
		// "this path is NOT ignored" which is the same as a non-match for us.
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		// Skip placeholder records that some git versions emit for non-matches
		// (source/pattern empty). With check-ignore those should not appear
		// without --non-matching, but guard defensively.
		if source == "" && pattern == "" {
			continue
		}

		linenum, _ := strconv.Atoi(linenumStr)
		out[path] = IgnoreMatch{
			Path:    path,
			Source:  source,
			LineNum: linenum,
			Pattern: pattern,
		}
	}
	return nil
}
