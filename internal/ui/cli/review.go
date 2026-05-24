// Package cli provides a simple CLI-based review interface.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/preview"
	"github.com/benjaminabbitt/git-expunge/internal/review"
)

// Reviewer provides an interactive CLI for reviewing findings.
type Reviewer struct {
	session      *review.Session
	findings     []*domain.Finding // sorted view alias from session; stable for CLI's lifetime
	index        int
	in           io.Reader
	out          io.Writer
	previewGen   *preview.Generator
	previewCache map[string]*preview.Preview
}

// NewReviewer creates a new CLI reviewer over the given manifest. The
// shared review.Session owns the queue state and operations; mutations
// applied here are visible to whoever next reads the same map. The CLI
// never adds or removes findings (only toggles), so the sorted view
// captured here stays accurate for the session's lifetime.
func NewReviewer(manifest domain.Manifest, repoPath string) *Reviewer {
	// Create preview generator
	var previewGen *preview.Generator
	if repoPath != "" {
		previewGen, _ = preview.NewGenerator(repoPath)
	}

	s := review.NewSession(manifest)
	return &Reviewer{
		session:      s,
		findings:     s.Findings(),
		index:        0,
		in:           os.Stdin,
		out:          os.Stdout,
		previewGen:   previewGen,
		previewCache: make(map[string]*preview.Preview),
	}
}

// Run runs the interactive review session.
func (r *Reviewer) Run() error {
	if len(r.findings) == 0 {
		fmt.Fprintln(r.out, "No findings to review.")
		return nil
	}

	fmt.Fprintln(r.out, "=== git-expunge Interactive Review ===")
	fmt.Fprintln(r.out)
	fmt.Fprintf(r.out, "Reviewing %d findings. Commands:\n", len(r.findings))
	fmt.Fprintln(r.out, "  [space/enter] Toggle purge    [n] Next    [p] Previous")
	fmt.Fprintln(r.out, "  [a] Purge all                 [c] Clear all")
	fmt.Fprintln(r.out, "  [s] Show summary              [q] Save and quit")
	fmt.Fprintln(r.out)

	scanner := bufio.NewScanner(r.in)
	r.showCurrent()

	for {
		fmt.Fprint(r.out, "> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if input == "" {
			input = " " // treat empty as toggle
		}

		switch input[0] {
		case ' ', '\n', 't': // toggle
			r.toggleCurrent()
			r.showCurrent()

		case 'n': // next
			if r.index < len(r.findings)-1 {
				r.index++
			}
			r.showCurrent()

		case 'p': // previous
			if r.index > 0 {
				r.index--
			}
			r.showCurrent()

		case 'a': // purge all
			r.session.MarkAllForPurge()
			fmt.Fprintln(r.out, "Marked all findings for purge.")
			r.showCurrent()

		case 'c': // clear all
			r.session.ClearAllPurge()
			fmt.Fprintln(r.out, "Cleared all purge marks.")
			r.showCurrent()

		case 's': // summary
			r.showSummary()

		case 'q': // quit
			r.showSummary()
			return nil

		case 'h', '?': // help
			fmt.Fprintln(r.out, "Commands:")
			fmt.Fprintln(r.out, "  [space/t] Toggle purge    [n] Next    [p] Previous")
			fmt.Fprintln(r.out, "  [a] Purge all             [c] Clear all")
			fmt.Fprintln(r.out, "  [s] Show summary          [q] Save and quit")

		default:
			// Try to parse as number for direct jump
			var num int
			if _, err := fmt.Sscanf(input, "%d", &num); err == nil {
				if num >= 1 && num <= len(r.findings) {
					r.index = num - 1
					r.showCurrent()
				} else {
					fmt.Fprintf(r.out, "Invalid index. Enter 1-%d\n", len(r.findings))
				}
			} else {
				fmt.Fprintln(r.out, "Unknown command. Press 'h' for help.")
			}
		}
	}

	return scanner.Err()
}

func (r *Reviewer) showCurrent() {
	f := r.findings[r.index]

	status := "[ ]"
	if f.Purge {
		status = "[x]"
	}

	fmt.Fprintf(r.out, "\n--- Finding %d/%d ---\n", r.index+1, len(r.findings))
	fmt.Fprintf(r.out, "%s %s\n", status, f.Path)
	fmt.Fprintf(r.out, "    Type: %s\n", f.Type)
	fmt.Fprintf(r.out, "    Blob: %s\n", f.BlobHash[:12])

	if f.Size > 0 {
		fmt.Fprintf(r.out, "    Size: %s\n", formatSize(f.Size))
	}
	if f.MimeType != "" {
		fmt.Fprintf(r.out, "    MIME: %s\n", f.MimeType)
	}
	if f.Rule != "" {
		fmt.Fprintf(r.out, "    Rule: %s\n", f.Rule)
	}
	if len(f.Commits) > 0 {
		fmt.Fprintf(r.out, "    Commits: %d\n", len(f.Commits))
	}

	// Show preview
	if p := r.getPreview(f.BlobHash); p != nil {
		fmt.Fprintln(r.out)
		if p.IsBinary {
			fmt.Fprintln(r.out, "    Preview (hex):")
		} else {
			fmt.Fprintln(r.out, "    Preview:")
		}
		// Indent preview lines
		lines := strings.Split(p.Content, "\n")
		maxLines := 10
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			lines = append(lines, "...")
		}
		for _, line := range lines {
			if len(line) > 70 {
				line = line[:67] + "..."
			}
			fmt.Fprintf(r.out, "    │ %s\n", line)
		}
	}
}

func (r *Reviewer) getPreview(blobHash string) *preview.Preview {
	if r.previewGen == nil {
		return nil
	}

	if p, ok := r.previewCache[blobHash]; ok {
		return p
	}

	p, err := r.previewGen.Generate(blobHash)
	if err != nil {
		return nil
	}

	r.previewCache[blobHash] = p
	return p
}

func (r *Reviewer) toggleCurrent() {
	if _, err := r.session.ToggleAt(r.index); err != nil {
		fmt.Fprintf(r.out, "toggle failed: %v\n", err)
	}
}

func (r *Reviewer) showSummary() {
	purgeCount := 0
	var byType = make(map[domain.FindingType]int)

	for _, f := range r.findings {
		byType[f.Type]++
		if f.Purge {
			purgeCount++
		}
	}

	fmt.Fprintln(r.out, "\n=== Summary ===")
	fmt.Fprintf(r.out, "Total findings: %d\n", len(r.findings))
	fmt.Fprintf(r.out, "  Binaries: %d\n", byType[domain.FindingTypeBinary])
	fmt.Fprintf(r.out, "  Secrets: %d\n", byType[domain.FindingTypeSecret])
	fmt.Fprintf(r.out, "Marked for purge: %d\n", purgeCount)
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

// GetManifest returns the modified manifest. It is the same underlying
// map the caller passed to NewReviewer, so mutations made during the
// review session are visible here.
func (r *Reviewer) GetManifest() domain.Manifest {
	return r.session.Manifest()
}
