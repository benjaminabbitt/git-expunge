package scanner

import (
	"testing"
)

func TestBinaryDetector_Detect(t *testing.T) {
	tests := []struct {
		name          string
		content       []byte
		size          int64
		sizeThreshold int64
		wantBinary    bool
	}{
		{
			name:          "ELF binary above threshold",
			content:       makeELFBinary(100 * 1024),
			size:          100 * 1024,
			sizeThreshold: 50 * 1024,
			wantBinary:    true,
		},
		{
			name:          "ELF binary below threshold",
			content:       makeELFBinary(10 * 1024),
			size:          10 * 1024,
			sizeThreshold: 50 * 1024,
			wantBinary:    false, // Below threshold
		},
		{
			name:          "Text file above threshold",
			content:       makeTextContent(100 * 1024),
			size:          100 * 1024,
			sizeThreshold: 50 * 1024,
			wantBinary:    false, // Text is not binary
		},
		{
			name:          "PNG image above threshold",
			content:       makePNGHeader(100 * 1024),
			size:          100 * 1024,
			sizeThreshold: 50 * 1024,
			wantBinary:    true,
		},
		{
			name:          "ZIP archive above threshold",
			content:       makeZIPHeader(100 * 1024),
			size:          100 * 1024,
			sizeThreshold: 50 * 1024,
			wantBinary:    true,
		},
		{
			name:          "JSON file above threshold",
			content:       []byte(`{"key": "value", "nested": {"array": [1, 2, 3]}}`),
			size:          100 * 1024,
			sizeThreshold: 50 * 1024,
			wantBinary:    false, // JSON is text
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewBinaryDetector(tt.sizeThreshold)

			blob := &BlobInfo{
				Hash:       "abc123",
				Path:       "test/file",
				Size:       tt.size,
				CommitHash: "commit123",
				Content: func() ([]byte, error) {
					return tt.content, nil
				},
			}

			finding := detector.Detect(blob)

			if tt.wantBinary && finding == nil {
				t.Errorf("expected binary detection, got nil")
			}
			if !tt.wantBinary && finding != nil {
				t.Errorf("expected no detection, got finding: %+v", finding)
			}
		})
	}
}

func TestIsBinaryContent(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    bool
	}{
		{
			name:    "ELF binary",
			content: makeELFBinary(1024),
			want:    true,
		},
		{
			name:    "Plain text",
			content: []byte("Hello, World!\nThis is plain text."),
			want:    false,
		},
		{
			name:    "Go source code",
			content: []byte("package main\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}"),
			want:    false,
		},
		{
			name:    "PNG image",
			content: makePNGHeader(512),
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBinaryContent(tt.content)
			if got != tt.want {
				t.Errorf("IsBinaryContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Helper functions to create test content

func makeELFBinary(size int) []byte {
	// ELF magic header
	header := []byte{
		0x7f, 0x45, 0x4c, 0x46, // ELF magic
		0x02, 0x01, 0x01, 0x00, // 64-bit, little endian
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	content := make([]byte, size)
	copy(content, header)
	return content
}

func makePNGHeader(size int) []byte {
	// PNG magic header
	header := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	}
	content := make([]byte, size)
	copy(content, header)
	return content
}

func makeZIPHeader(size int) []byte {
	// ZIP magic header
	header := []byte{
		0x50, 0x4b, 0x03, 0x04,
	}
	content := make([]byte, size)
	copy(content, header)
	return content
}

func makeTextContent(size int) []byte {
	text := "This is a text file with some content.\n"
	content := make([]byte, 0, size)
	for len(content) < size {
		content = append(content, text...)
	}
	return content[:size]
}
