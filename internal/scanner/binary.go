package scanner

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/gabriel-vasile/mimetype"
)

const (
	// PreviewSize is the max bytes to include in preview
	PreviewSize = 512
	// HexPreviewSize is max bytes for hex preview (will be 2x in output)
	HexPreviewSize = 256
)

// BinaryDetector detects binary files based on MIME type and size.
type BinaryDetector struct {
	sizeThreshold int64
}

// NewBinaryDetector creates a new BinaryDetector.
func NewBinaryDetector(sizeThreshold int64) *BinaryDetector {
	return &BinaryDetector{
		sizeThreshold: sizeThreshold,
	}
}

// Detect checks if a blob is a binary file that should be flagged.
// Returns nil if the blob is not a binary or doesn't meet criteria.
func (d *BinaryDetector) Detect(blob *BlobInfo) *domain.Finding {
	// Skip small files
	if blob.Size < d.sizeThreshold {
		return nil
	}

	// Get content for MIME detection
	content, err := blob.Content()
	if err != nil {
		return nil
	}

	// Detect MIME type
	mtype := mimetype.Detect(content)

	// Check if it's a binary (not text-based)
	if !d.isBinary(mtype) {
		return nil
	}

	return &domain.Finding{
		BlobHash: blob.Hash,
		Type:     domain.FindingTypeBinary,
		Path:     blob.Path,
		Size:     blob.Size,
		MimeType: mtype.String(),
		Commits:  []string{blob.CommitHash},
		Purge:    false,
	}
}

// isBinary checks if a MIME type represents a binary file.
func (d *BinaryDetector) isBinary(mtype *mimetype.MIME) bool {
	mime := mtype.String()

	// Text-based types that should NOT be flagged as binary
	textPrefixes := []string{
		"text/",
		"application/json",
		"application/xml",
		"application/javascript",
		"application/typescript",
		"application/x-yaml",
		"application/toml",
	}

	for _, prefix := range textPrefixes {
		if len(mime) >= len(prefix) && mime[:len(prefix)] == prefix {
			return false
		}
	}

	// Check the MIME hierarchy - if it descends from text/plain, it's text
	for m := mtype; m != nil; m = m.Parent() {
		if m.String() == "text/plain" {
			return false
		}
	}

	// Common binary types to flag
	binaryTypes := map[string]bool{
		"application/octet-stream":           true,
		"application/x-executable":           true,
		"application/x-mach-binary":          true,
		"application/x-elf":                  true,
		"application/x-dosexec":              true,
		"application/x-sharedlib":            true,
		"application/x-object":               true,
		"application/x-archive":              true,
		"application/zip":                    true,
		"application/gzip":                   true,
		"application/x-tar":                  true,
		"application/x-7z-compressed":        true,
		"application/x-rar-compressed":       true,
		"application/java-archive":           true,
		"application/vnd.android.package-archive": true,
		"image/png":                          true,
		"image/jpeg":                         true,
		"image/gif":                          true,
		"image/webp":                         true,
		"image/bmp":                          true,
		"image/tiff":                         true,
		"audio/mpeg":                         true,
		"audio/wav":                          true,
		"audio/ogg":                          true,
		"video/mp4":                          true,
		"video/webm":                         true,
		"video/x-msvideo":                    true,
		"application/pdf":                    true,
		"application/x-sqlite3":              true,
	}

	return binaryTypes[mime]
}

// IsBinaryContent is a helper that checks raw content for binary detection.
func IsBinaryContent(content []byte) bool {
	mtype := mimetype.Detect(content)
	d := &BinaryDetector{}
	return d.isBinary(mtype)
}

// generatePreview creates a preview string from content.
// For binary content, returns hex dump. For text, returns UTF-8 string.
// Returns (preview, isBinary).
func generatePreview(content []byte, forceBinary bool) (string, bool) {
	if len(content) == 0 {
		return "", false
	}

	// Check if content looks like text
	isText := !forceBinary && isLikelyText(content)

	if isText {
		// Text preview
		previewLen := PreviewSize
		if len(content) < previewLen {
			previewLen = len(content)
		}

		// Ensure we don't cut in the middle of a UTF-8 sequence
		preview := content[:previewLen]
		for len(preview) > 0 && !utf8.Valid(preview) {
			preview = preview[:len(preview)-1]
		}

		result := string(preview)
		// Clean up for display - replace tabs with spaces, limit line length
		result = strings.ReplaceAll(result, "\t", "    ")

		if len(content) > previewLen {
			result += "\n..."
		}
		return result, false
	}

	// Binary hex preview
	previewLen := HexPreviewSize
	if len(content) < previewLen {
		previewLen = len(content)
	}

	// Format as hex dump with ASCII sidebar
	return formatHexDump(content[:previewLen]), true
}

// isLikelyText checks if content appears to be text.
func isLikelyText(content []byte) bool {
	// Check first 512 bytes for non-text characters
	checkLen := 512
	if len(content) < checkLen {
		checkLen = len(content)
	}

	// Count non-printable/non-text bytes
	nonText := 0
	for i := 0; i < checkLen; i++ {
		b := content[i]
		// Allow: printable ASCII, tab, newline, carriage return, and UTF-8 continuation bytes
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			nonText++
		} else if b == 0 {
			// Null bytes are a strong indicator of binary
			return false
		}
	}

	// If more than 10% non-text, probably binary
	return float64(nonText)/float64(checkLen) < 0.1
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

		b.WriteString("|\n")
	}

	return b.String()
}
