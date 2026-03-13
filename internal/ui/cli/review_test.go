package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
)

func createTestManifest() domain.Manifest {
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123def456",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Size:     1024,
		MimeType: "application/octet-stream",
		Commits:  []string{"c1"},
		Purge:    false,
	})
	m.Add(&domain.Finding{
		BlobHash: "def456abc123",
		Type:     domain.FindingTypeSecret,
		Path:     ".env",
		Rule:     "aws-access-key",
		Commits:  []string{"c2"},
		Purge:    false,
	})
	return m
}

func TestNewReviewer(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	if r == nil {
		t.Fatal("NewReviewer returned nil")
	}
	if len(r.findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(r.findings))
	}
	if r.index != 0 {
		t.Error("expected index to start at 0")
	}
}

func TestReviewer_EmptyManifest(t *testing.T) {
	m := domain.NewManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !strings.Contains(out.String(), "No findings to review") {
		t.Error("expected empty message")
	}
}

func TestReviewer_Toggle(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("t\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// First finding should be toggled
	if !r.findings[0].Purge {
		t.Error("expected first finding to be marked for purge")
	}
}

func TestReviewer_Navigation(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("n\np\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Should end at first item after next/prev
	if r.index != 0 {
		t.Errorf("expected index 0, got %d", r.index)
	}
}

func TestReviewer_PurgeAll(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("a\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	for _, f := range r.findings {
		if !f.Purge {
			t.Errorf("expected all findings to be marked for purge")
		}
	}
}

func TestReviewer_ClearAll(t *testing.T) {
	m := createTestManifest()
	// Pre-mark one for purge
	for _, f := range m {
		f.Purge = true
		break
	}

	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("c\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	for _, f := range r.findings {
		if f.Purge {
			t.Error("expected all purge marks to be cleared")
		}
	}
}

func TestReviewer_Summary(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("s\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Summary") {
		t.Error("expected summary output")
	}
	if !strings.Contains(output, "Total findings: 2") {
		t.Error("expected finding count in summary")
	}
}

func TestReviewer_Help(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("h\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Commands:") {
		t.Error("expected help output")
	}
}

func TestReviewer_JumpToNumber(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("2\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if r.index != 1 {
		t.Errorf("expected index 1 after jumping to 2, got %d", r.index)
	}
}

func TestReviewer_InvalidNumber(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("99\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Invalid index") {
		t.Error("expected invalid index message")
	}
}

func TestReviewer_UnknownCommand(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	r.in = strings.NewReader("x\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Unknown command") {
		t.Error("expected unknown command message")
	}
}

func TestReviewer_GetManifest(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	got := r.GetManifest()
	if len(got) != len(m) {
		t.Errorf("expected %d findings, got %d", len(m), len(got))
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
	}

	for _, tt := range tests {
		result := formatSize(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatSize(%d) = %s, expected %s", tt.bytes, result, tt.expected)
		}
	}
}

func TestReviewer_NavigationBounds(t *testing.T) {
	m := createTestManifest()
	r := NewReviewer(m, "")

	var out bytes.Buffer
	// Try to go previous from first item (should stay at 0)
	// Then go next twice (should stop at last item)
	r.in = strings.NewReader("p\nn\nn\nn\nq\n")
	r.out = &out

	err := r.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Should be at last item (index 1 for 2 items)
	if r.index != 1 {
		t.Errorf("expected index 1 at end, got %d", r.index)
	}
}
