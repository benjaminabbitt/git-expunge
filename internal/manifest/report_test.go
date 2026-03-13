package manifest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
)

func TestGenerateReport(t *testing.T) {
	manifest := domain.NewManifest()
	manifest.Add(&domain.Finding{
		BlobHash: "abc123def456",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Size:     1024 * 1024, // 1MB
		MimeType: "application/x-executable",
		Commits:  []string{"commit1", "commit2"},
		Purge:    false,
	})
	manifest.Add(&domain.Finding{
		BlobHash: "def456abc123",
		Type:     domain.FindingTypeSecret,
		Path:     ".env",
		Rule:     "aws-access-key",
		Commits:  []string{"commit3"},
		Purge:    true,
	})

	var buf bytes.Buffer
	if err := GenerateReport(manifest, &buf); err != nil {
		t.Fatalf("GenerateReport failed: %v", err)
	}

	output := buf.String()

	// Check header
	if !strings.Contains(output, "# git-expunge Manifest") {
		t.Error("missing header")
	}

	// Check sections
	if !strings.Contains(output, "## Binaries") {
		t.Error("missing Binaries section")
	}
	if !strings.Contains(output, "## Secrets") {
		t.Error("missing Secrets section")
	}

	// Check binary entry
	if !strings.Contains(output, "### [ ] bin/app") {
		t.Error("missing binary entry with unchecked box")
	}
	if !strings.Contains(output, "`abc123def456`") {
		t.Error("missing blob hash")
	}
	if !strings.Contains(output, "1.0 MB") {
		t.Error("missing size")
	}

	// Check secret entry
	if !strings.Contains(output, "### [x] .env") {
		t.Error("missing secret entry with checked box")
	}
	if !strings.Contains(output, "aws-access-key") {
		t.Error("missing rule")
	}
}

func TestParseReport(t *testing.T) {
	input := `# git-expunge Manifest

Review the findings below.

## Binaries

### [ ] bin/app

- **Blob:** ` + "`abc123def456`" + `
- **Size:** 1.0 MB
- **Type:** application/x-executable
- **Commits:** ` + "`commit1`" + `, ` + "`commit2`" + `

## Secrets

### [x] .env

- **Blob:** ` + "`def456abc123`" + `
- **Rule:** aws-access-key
- **Commits:** ` + "`commit3`" + `
`

	manifest, err := ParseReport(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseReport failed: %v", err)
	}

	if len(manifest) != 2 {
		t.Errorf("expected 2 findings, got %d", len(manifest))
	}

	// Check binary
	binary := manifest["abc123def456"]
	if binary == nil {
		t.Fatal("binary finding not found")
	}
	if binary.Type != domain.FindingTypeBinary {
		t.Errorf("expected type binary, got %s", binary.Type)
	}
	if binary.Path != "bin/app" {
		t.Errorf("expected path bin/app, got %s", binary.Path)
	}
	if binary.Purge {
		t.Error("binary should not be marked for purge")
	}

	// Check secret
	secret := manifest["def456abc123"]
	if secret == nil {
		t.Fatal("secret finding not found")
	}
	if secret.Type != domain.FindingTypeSecret {
		t.Errorf("expected type secret, got %s", secret.Type)
	}
	if secret.Path != ".env" {
		t.Errorf("expected path .env, got %s", secret.Path)
	}
	if !secret.Purge {
		t.Error("secret should be marked for purge")
	}
	if secret.Rule != "aws-access-key" {
		t.Errorf("expected rule aws-access-key, got %s", secret.Rule)
	}
}

func TestRoundTrip(t *testing.T) {
	// Create original manifest
	original := domain.NewManifest()
	original.Add(&domain.Finding{
		BlobHash: "a1b2c3d4e5f6789012345678901234567890abcd",
		Type:     domain.FindingTypeBinary,
		Path:     "path/to/binary",
		Size:     2048,
		MimeType: "application/octet-stream",
		Commits:  []string{"c1"},
		Purge:    true,
	})
	original.Add(&domain.Finding{
		BlobHash: "fedcba9876543210fedcba9876543210fedcba98",
		Type:     domain.FindingTypeSecret,
		Path:     "config/secrets.yaml",
		Rule:     "generic-api-key",
		Commits:  []string{"c2", "c3"},
		Purge:    false,
	})

	// Generate report
	var buf bytes.Buffer
	if err := GenerateReport(original, &buf); err != nil {
		t.Fatalf("GenerateReport failed: %v", err)
	}

	// Parse report
	parsed, err := ParseReport(&buf)
	if err != nil {
		t.Fatalf("ParseReport failed: %v", err)
	}

	// Verify round-trip
	if len(parsed) != len(original) {
		t.Errorf("expected %d findings, got %d", len(original), len(parsed))
	}

	for hash, orig := range original {
		p := parsed[hash]
		if p == nil {
			t.Errorf("missing finding for hash %s", hash)
			continue
		}

		if p.Type != orig.Type {
			t.Errorf("hash %s: type mismatch: %s vs %s", hash, p.Type, orig.Type)
		}
		if p.Path != orig.Path {
			t.Errorf("hash %s: path mismatch: %s vs %s", hash, p.Path, orig.Path)
		}
		if p.Purge != orig.Purge {
			t.Errorf("hash %s: purge mismatch: %v vs %v", hash, p.Purge, orig.Purge)
		}
		if p.Rule != orig.Rule {
			t.Errorf("hash %s: rule mismatch: %s vs %s", hash, p.Rule, orig.Rule)
		}
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 bytes"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024*1024*1024 + 512*1024*1024, "1.5 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatSize(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatSize(%d) = %s, want %s", tt.bytes, result, tt.expected)
			}
		})
	}
}
