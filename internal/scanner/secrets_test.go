package scanner

import (
	"testing"
)

func TestSecretDetector_DetectContent(t *testing.T) {
	detector, err := NewSecretDetector()
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	tests := []struct {
		name      string
		content   string
		hasSecret bool
	}{
		{
			name:      "AWS access key pattern",
			content:   "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			hasSecret: true,
		},
		{
			name:      "Generic API key pattern",
			content:   "api_key=sk-proj-abcdefghijklmnop123456789",
			hasSecret: true,
		},
		{
			name:      "Plain text",
			content:   "Hello, this is just plain text with no secrets.",
			hasSecret: false,
		},
		{
			name:      "Source code",
			content:   "func main() {\n    fmt.Println(\"Hello\")\n}",
			hasSecret: false,
		},
		{
			name:      "Config without secrets",
			content:   "DATABASE_HOST=localhost\nPORT=5432",
			hasSecret: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := detector.DetectContent([]byte(tt.content))
			if found != tt.hasSecret {
				t.Errorf("DetectContent() = %v, want %v", found, tt.hasSecret)
			}
		})
	}
}

func TestSecretDetector_Detect(t *testing.T) {
	detector, err := NewSecretDetector()
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	secretContent := []byte("AWS_ACCESS_KEY_ID=AKIAZT5K7YFAPXR3VBCD\nAWS_SECRET_ACCESS_KEY=secret123456789012345678901234567890")
	blob := &BlobInfo{
		Hash:       "abc123",
		Path:       ".env",
		Size:       int64(len(secretContent)),
		CommitHash: "commit1",
		Content: func() ([]byte, error) {
			return secretContent, nil
		},
	}

	findings := detector.Detect(blob)
	if len(findings) == 0 {
		t.Error("expected to find secrets, got none")
	}

	// Check finding properties
	for _, f := range findings {
		if f.BlobHash != blob.Hash {
			t.Errorf("expected BlobHash=%s, got %s", blob.Hash, f.BlobHash)
		}
		if f.Path != blob.Path {
			t.Errorf("expected Path=%s, got %s", blob.Path, f.Path)
		}
		if f.Type != "secret" {
			t.Errorf("expected Type=secret, got %s", f.Type)
		}
	}
}

func TestSecretDetector_SkipsLargeFiles(t *testing.T) {
	detector, err := NewSecretDetector()
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Create a blob that would have secrets but is too large
	largeContent := make([]byte, 11*1024*1024) // 11MB
	copy(largeContent, []byte("AWS_ACCESS_KEY_ID=AKIAZT5K7YFAPXR3VBCD"))

	blob := &BlobInfo{
		Hash:       "def456",
		Path:       "large.env",
		Size:       int64(len(largeContent)),
		CommitHash: "commit2",
		Content: func() ([]byte, error) {
			return largeContent, nil
		},
	}

	findings := detector.Detect(blob)
	if len(findings) != 0 {
		t.Error("expected no findings for large file, but found some")
	}
}
