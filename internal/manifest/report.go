package manifest

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/preview"
)

// ReportGenerator generates markdown reports with previews.
type ReportGenerator struct {
	previewGen    *preview.Generator
	sharedBlobs   map[string][]string // blob hash -> all paths where it appears
}

// NewReportGenerator creates a report generator with preview support.
func NewReportGenerator(repoPath string) *ReportGenerator {
	var previewGen *preview.Generator
	if repoPath != "" {
		previewGen, _ = preview.NewGenerator(repoPath)
	}
	return &ReportGenerator{
		previewGen:  previewGen,
		sharedBlobs: make(map[string][]string),
	}
}

// SetSharedBlobs sets the shared blob information for enhanced reports.
// This maps blob hashes to all paths where that blob appears.
func (g *ReportGenerator) SetSharedBlobs(sharedBlobs map[string][]string) {
	g.sharedBlobs = sharedBlobs
}

// GenerateReport generates a human-readable markdown report from a manifest.
// Deprecated: Use NewReportGenerator(repoPath).Generate() for preview support.
func GenerateReport(manifest domain.Manifest, w io.Writer) error {
	return NewReportGenerator("").Generate(manifest, w)
}

// Generate generates a human-readable markdown report from a manifest.
func (g *ReportGenerator) Generate(manifest domain.Manifest, w io.Writer) error {
	// Sort findings by type, then by path for consistent output
	var binaries, secrets []*domain.Finding
	for _, f := range manifest {
		switch f.Type {
		case domain.FindingTypeBinary:
			binaries = append(binaries, f)
		case domain.FindingTypeSecret:
			secrets = append(secrets, f)
		}
	}

	sortByPath := func(findings []*domain.Finding) {
		sort.Slice(findings, func(i, j int) bool {
			return findings[i].Path < findings[j].Path
		})
	}
	sortByPath(binaries)
	sortByPath(secrets)

	// Write header
	fmt.Fprintln(w, "# git-expunge Manifest")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Review the findings below and check the boxes for items you want to purge.")
	fmt.Fprintln(w, "After editing, run `git-expunge report read` to convert back to JSON.")
	fmt.Fprintln(w)

	// Write summary
	fmt.Fprintf(w, "**Total Findings:** %d (%d binaries, %d secrets)\n\n",
		len(manifest), len(binaries), len(secrets))

	// Write binaries section
	if len(binaries) > 0 {
		fmt.Fprintln(w, "## Binaries")
		fmt.Fprintln(w)
		for _, f := range binaries {
			if err := g.writeFinding(w, f); err != nil {
				return err
			}
		}
	}

	// Write secrets section
	if len(secrets) > 0 {
		fmt.Fprintln(w, "## Secrets")
		fmt.Fprintln(w)
		for _, f := range secrets {
			if err := g.writeFinding(w, f); err != nil {
				return err
			}
		}
	}

	return nil
}

func (g *ReportGenerator) writeFinding(w io.Writer, f *domain.Finding) error {
	// Checkbox
	checkbox := "[ ]"
	if f.Purge {
		checkbox = "[x]"
	}

	// Header with path and checkbox
	fmt.Fprintf(w, "### %s %s\n\n", checkbox, f.Path)

	// Blob hash (required for parsing back)
	fmt.Fprintf(w, "- **Blob:** `%s`\n", f.BlobHash)

	// Size (for binaries)
	if f.Size > 0 {
		fmt.Fprintf(w, "- **Size:** %s\n", formatSize(f.Size))
	}

	// MIME type (for binaries)
	if f.MimeType != "" {
		fmt.Fprintf(w, "- **Type:** %s\n", f.MimeType)
	}

	// Rule (for secrets)
	if f.Rule != "" {
		fmt.Fprintf(w, "- **Rule:** %s\n", f.Rule)
	}

	// Commits
	if len(f.Commits) > 0 {
		fmt.Fprintf(w, "- **Commits:** %s\n", formatCommits(f.Commits))
	}

	// Shared blob warning - show all paths where this blob appears
	if paths, ok := g.sharedBlobs[f.BlobHash]; ok && len(paths) > 1 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "⚠️ **SHARED CONTENT:** This blob appears at %d locations:\n", len(paths))
		for _, p := range paths {
			marker := ""
			if p == f.Path {
				marker = " ← this entry"
			}
			fmt.Fprintf(w, "  - `%s`%s\n", p, marker)
		}
		fmt.Fprintln(w, "\n**Purging this blob will remove content from ALL paths above.**")
	}

	// Preview
	if g.previewGen != nil {
		if p, err := g.previewGen.Generate(f.BlobHash); err == nil && p != nil {
			fmt.Fprintln(w)
			if p.IsBinary {
				fmt.Fprintln(w, "**Preview (hex):**")
			} else {
				fmt.Fprintln(w, "**Preview:**")
			}
			fmt.Fprintln(w, "```")
			// Truncate preview for report
			lines := strings.Split(p.Content, "\n")
			maxLines := 15
			if len(lines) > maxLines {
				lines = lines[:maxLines]
				lines = append(lines, "...")
			}
			for _, line := range lines {
				fmt.Fprintln(w, line)
			}
			fmt.Fprintln(w, "```")
		}
	}

	fmt.Fprintln(w)
	return nil
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

func formatCommits(commits []string) string {
	if len(commits) == 0 {
		return ""
	}
	if len(commits) == 1 {
		return fmt.Sprintf("`%s`", shortHash(commits[0]))
	}
	if len(commits) <= 3 {
		var parts []string
		for _, c := range commits {
			parts = append(parts, fmt.Sprintf("`%s`", shortHash(c)))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("`%s` and %d more", shortHash(commits[0]), len(commits)-1)
}

func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// ParseReport parses a markdown report back into a manifest.
func ParseReport(r io.Reader) (domain.Manifest, error) {
	manifest := domain.NewManifest()
	scanner := bufio.NewScanner(r)

	var currentFinding *domain.Finding
	var currentType domain.FindingType

	// Regex patterns
	headerPattern := regexp.MustCompile(`^###\s+\[([ xX])\]\s+(.+)$`)
	blobPattern := regexp.MustCompile(`^\s*-\s+\*\*Blob:\*\*\s+` + "`" + `([a-f0-9]+)` + "`")
	sizePattern := regexp.MustCompile(`^\s*-\s+\*\*Size:\*\*\s+(.+)$`)
	typePattern := regexp.MustCompile(`^\s*-\s+\*\*Type:\*\*\s+(.+)$`)
	rulePattern := regexp.MustCompile(`^\s*-\s+\*\*Rule:\*\*\s+(.+)$`)

	for scanner.Scan() {
		line := scanner.Text()

		// Check for section headers
		if strings.HasPrefix(line, "## Binaries") {
			currentType = domain.FindingTypeBinary
			continue
		}
		if strings.HasPrefix(line, "## Secrets") {
			currentType = domain.FindingTypeSecret
			continue
		}

		// Check for finding header
		if matches := headerPattern.FindStringSubmatch(line); matches != nil {
			// Save previous finding
			if currentFinding != nil && currentFinding.BlobHash != "" {
				manifest.Add(currentFinding)
			}

			purge := strings.ToLower(matches[1]) == "x"
			path := strings.TrimSpace(matches[2])

			currentFinding = &domain.Finding{
				Type:  currentType,
				Path:  path,
				Purge: purge,
			}
			continue
		}

		// Parse finding details
		if currentFinding == nil {
			continue
		}

		if matches := blobPattern.FindStringSubmatch(line); matches != nil {
			currentFinding.BlobHash = matches[1]
			continue
		}

		if matches := sizePattern.FindStringSubmatch(line); matches != nil {
			currentFinding.Size = parseSize(matches[1])
			continue
		}

		if matches := typePattern.FindStringSubmatch(line); matches != nil {
			currentFinding.MimeType = strings.TrimSpace(matches[1])
			continue
		}

		if matches := rulePattern.FindStringSubmatch(line); matches != nil {
			currentFinding.Rule = strings.TrimSpace(matches[1])
			continue
		}
	}

	// Save last finding
	if currentFinding != nil && currentFinding.BlobHash != "" {
		manifest.Add(currentFinding)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return manifest, nil
}

func parseSize(s string) int64 {
	s = strings.TrimSpace(s)

	var value float64
	var unit string
	fmt.Sscanf(s, "%f %s", &value, &unit)

	switch strings.ToUpper(unit) {
	case "GB":
		return int64(value * 1024 * 1024 * 1024)
	case "MB":
		return int64(value * 1024 * 1024)
	case "KB":
		return int64(value * 1024)
	default:
		return int64(value)
	}
}
