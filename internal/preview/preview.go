// Package preview provides content preview generation for git blobs.
package preview

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

const (
	// PreviewSize is the max bytes to include in text preview
	PreviewSize = 512
	// HexPreviewSize is max bytes for hex preview
	HexPreviewSize = 256
)

// Highlight represents a region to highlight in the preview.
type Highlight struct {
	Line      int // 1-based line number
	StartCol  int // 0-based start column
	EndCol    int // 0-based end column (exclusive)
}

// Preview contains the generated preview data.
type Preview struct {
	Content    string
	IsBinary   bool
	Highlights []Highlight // Regions to highlight (e.g., secrets)
}

// Generator generates previews from git blobs.
type Generator struct {
	repo *git.Repository
}

// NewGenerator creates a new preview generator for a repository.
func NewGenerator(repoPath string) (*Generator, error) {
	// Convert to absolute path to handle relative paths correctly
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	repo, err := git.PlainOpen(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repo at %s: %w", absPath, err)
	}
	return &Generator{repo: repo}, nil
}

// Generate creates a preview for a blob by its hash.
func (g *Generator) Generate(blobHash string) (*Preview, error) {
	hash := plumbing.NewHash(blobHash)
	blob, err := g.repo.BlobObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob: %w", err)
	}

	reader, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("failed to read blob: %w", err)
	}
	defer reader.Close()

	// Read up to PreviewSize bytes
	buf := make([]byte, PreviewSize)
	n, _ := reader.Read(buf)
	if n == 0 {
		return &Preview{Content: "(empty)", IsBinary: false}, nil
	}
	content := buf[:n]

	// Determine if binary or text
	isBinary := !isLikelyText(content)

	var preview string
	if isBinary {
		// Hex dump for binary
		previewLen := HexPreviewSize
		if len(content) < previewLen {
			previewLen = len(content)
		}
		preview = formatHexDump(content[:previewLen])
	} else {
		// Text preview
		preview = formatTextPreview(content, blob.Size > int64(n))
	}

	return &Preview{
		Content:  preview,
		IsBinary: isBinary,
	}, nil
}

// GenerateWithSecrets creates a preview centered on secret locations with highlights.
func (g *Generator) GenerateWithSecrets(blobHash string, secrets []domain.SecretLocation) (*Preview, error) {
	if len(secrets) == 0 {
		return g.Generate(blobHash)
	}

	hash := plumbing.NewHash(blobHash)
	blob, err := g.repo.BlobObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob: %w", err)
	}

	reader, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("failed to read blob: %w", err)
	}
	defer reader.Close()

	// Read entire content (up to a reasonable limit)
	maxRead := 64 * 1024 // 64KB max
	buf := make([]byte, maxRead)
	n, _ := reader.Read(buf)
	if n == 0 {
		return &Preview{Content: "(empty)", IsBinary: false}, nil
	}
	content := buf[:n]

	// Check if binary
	if !isLikelyText(content) {
		// For binary, just show hex dump
		previewLen := HexPreviewSize
		if len(content) < previewLen {
			previewLen = len(content)
		}
		return &Preview{
			Content:  formatHexDump(content[:previewLen]),
			IsBinary: true,
		}, nil
	}

	// Split into lines
	lines := strings.Split(string(content), "\n")

	// Find the range of lines to show (context around secrets)
	minLine := secrets[0].StartLine
	maxLine := secrets[0].EndLine
	for _, s := range secrets {
		if s.StartLine < minLine {
			minLine = s.StartLine
		}
		if s.EndLine > maxLine {
			maxLine = s.EndLine
		}
	}

	// Add context lines around secrets
	contextLines := 3
	startLine := minLine - contextLines
	if startLine < 1 {
		startLine = 1
	}
	endLine := maxLine + contextLines
	if endLine > len(lines) {
		endLine = len(lines)
	}

	// Extract the relevant lines
	var previewLines []string
	if startLine > 1 {
		previewLines = append(previewLines, "...")
	}
	for i := startLine; i <= endLine && i <= len(lines); i++ {
		previewLines = append(previewLines, lines[i-1]) // lines is 0-indexed
	}
	if endLine < len(lines) {
		previewLines = append(previewLines, "...")
	}

	// Build highlights adjusted for the preview offset
	var highlights []Highlight
	for _, s := range secrets {
		// Adjust line number relative to preview start
		adjustedLine := s.StartLine - startLine + 1
		if startLine > 1 {
			adjustedLine++ // Account for "..." at start
		}
		highlights = append(highlights, Highlight{
			Line:     adjustedLine,
			StartCol: s.StartColumn,
			EndCol:   s.EndColumn,
		})
	}

	return &Preview{
		Content:    strings.Join(previewLines, "\n"),
		IsBinary:   false,
		Highlights: highlights,
	}, nil
}

// GenerateFromContent creates a preview from raw content bytes.
func GenerateFromContent(content []byte) *Preview {
	if len(content) == 0 {
		return &Preview{Content: "(empty)", IsBinary: false}
	}

	isBinary := !isLikelyText(content)

	var preview string
	if isBinary {
		previewLen := HexPreviewSize
		if len(content) < previewLen {
			previewLen = len(content)
		}
		preview = formatHexDump(content[:previewLen])
	} else {
		previewLen := PreviewSize
		if len(content) < previewLen {
			previewLen = len(content)
		}
		preview = formatTextPreview(content[:previewLen], len(content) > previewLen)
	}

	return &Preview{
		Content:  preview,
		IsBinary: isBinary,
	}
}

// isLikelyText checks if content appears to be text.
func isLikelyText(content []byte) bool {
	checkLen := 512
	if len(content) < checkLen {
		checkLen = len(content)
	}

	nonText := 0
	for i := 0; i < checkLen; i++ {
		b := content[i]
		// Null bytes strongly indicate binary
		if b == 0 {
			return false
		}
		// Count control characters (except tab, newline, carriage return)
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			nonText++
		}
	}

	// If more than 10% non-text, probably binary
	return float64(nonText)/float64(checkLen) < 0.1
}

// formatTextPreview formats text content for display.
func formatTextPreview(content []byte, truncated bool) string {
	// Ensure valid UTF-8
	preview := content
	for len(preview) > 0 && !utf8.Valid(preview) {
		preview = preview[:len(preview)-1]
	}

	result := string(preview)
	// Replace tabs with spaces for consistent display
	result = strings.ReplaceAll(result, "\t", "    ")

	if truncated {
		result += "\n..."
	}
	return result
}

// formatHexDump formats bytes as a hex dump with ASCII sidebar.
func formatHexDump(data []byte) string {
	var b strings.Builder
	bytesPerLine := 16

	for i := 0; i < len(data); i += bytesPerLine {
		// Offset
		b.WriteString(fmt.Sprintf("%04x  ", i))

		// Hex bytes
		end := i + bytesPerLine
		if end > len(data) {
			end = len(data)
		}

		for j := i; j < end; j++ {
			b.WriteString(hex.EncodeToString([]byte{data[j]}))
			b.WriteString(" ")
		}

		// Padding if line is short
		for j := end; j < i+bytesPerLine; j++ {
			b.WriteString("   ")
		}

		b.WriteString(" |")

		// ASCII representation
		for j := i; j < end; j++ {
			if data[j] >= 32 && data[j] < 127 {
				b.WriteByte(data[j])
			} else {
				b.WriteByte('.')
			}
		}

		b.WriteString("|")
		if i+bytesPerLine < len(data) {
			b.WriteString("\n")
		}
	}

	return b.String()
}
