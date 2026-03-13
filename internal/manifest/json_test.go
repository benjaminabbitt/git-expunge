package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/spf13/afero"
)

func TestWriteAndReadJSON(t *testing.T) {
	// Create test manifest
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Size:     1024,
		MimeType: "application/octet-stream",
		Commits:  []string{"c1", "c2"},
		Purge:    true,
	})
	m.Add(&domain.Finding{
		BlobHash: "def456",
		Type:     domain.FindingTypeSecret,
		Path:     ".env",
		Rule:     "aws-access-key",
		Commits:  []string{"c3"},
		Purge:    false,
	})

	// Write to temp file
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "git-expunge-findings.json")

	if err := WriteJSON(m, path); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("manifest file was not created")
	}

	// Read back
	loaded, err := ReadJSON(path)
	if err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}

	// Verify contents
	if len(loaded) != len(m) {
		t.Errorf("expected %d findings, got %d", len(m), len(loaded))
	}

	// Check specific finding
	f := loaded["abc123"]
	if f == nil {
		t.Fatal("finding abc123 not found")
	}
	if f.Type != domain.FindingTypeBinary {
		t.Errorf("expected type binary, got %s", f.Type)
	}
	if f.Path != "bin/app" {
		t.Errorf("expected path bin/app, got %s", f.Path)
	}
	if !f.Purge {
		t.Error("expected purge=true")
	}
}

func TestReadJSON_FileNotFound(t *testing.T) {
	_, err := ReadJSON("/nonexistent/path/git-expunge-findings.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestReadJSON_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "invalid.json")

	// Write invalid JSON
	if err := os.WriteFile(path, []byte("not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadJSON(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestWriteAndReadJSON_WithMemFs(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Create test manifest
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Size:     1024,
		MimeType: "application/octet-stream",
		Commits:  []string{"c1", "c2"},
		Purge:    true,
	})
	m.Add(&domain.Finding{
		BlobHash: "def456",
		Type:     domain.FindingTypeSecret,
		Path:     ".env",
		Rule:     "aws-access-key",
		Commits:  []string{"c3"},
		Purge:    false,
	})

	// Write to memory filesystem
	path := "/manifests/git-expunge-findings.json"
	if err := WriteJSONWithFs(fs, m, path); err != nil {
		t.Fatalf("WriteJSONWithFs failed: %v", err)
	}

	// Verify file exists
	exists, err := afero.Exists(fs, path)
	if err != nil {
		t.Fatalf("failed to check existence: %v", err)
	}
	if !exists {
		t.Fatal("manifest file was not created")
	}

	// Read back
	loaded, err := ReadJSONWithFs(fs, path)
	if err != nil {
		t.Fatalf("ReadJSONWithFs failed: %v", err)
	}

	// Verify contents
	if len(loaded) != len(m) {
		t.Errorf("expected %d findings, got %d", len(m), len(loaded))
	}

	// Check specific finding
	f := loaded["abc123"]
	if f == nil {
		t.Fatal("finding abc123 not found")
	}
	if f.Type != domain.FindingTypeBinary {
		t.Errorf("expected type binary, got %s", f.Type)
	}
	if f.Path != "bin/app" {
		t.Errorf("expected path bin/app, got %s", f.Path)
	}
	if !f.Purge {
		t.Error("expected purge=true")
	}
}

func TestReadJSONWithFs_FileNotFound(t *testing.T) {
	fs := afero.NewMemMapFs()

	_, err := ReadJSONWithFs(fs, "/nonexistent/git-expunge-findings.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestReadJSONWithFs_InvalidJSON(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Write invalid JSON
	if err := afero.WriteFile(fs, "/invalid.json", []byte("not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadJSONWithFs(fs, "/invalid.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestWriteJSONWithFs_EmptyManifest(t *testing.T) {
	fs := afero.NewMemMapFs()
	m := domain.NewManifest()

	path := "/empty.json"
	if err := WriteJSONWithFs(fs, m, path); err != nil {
		t.Fatalf("WriteJSONWithFs failed: %v", err)
	}

	loaded, err := ReadJSONWithFs(fs, path)
	if err != nil {
		t.Fatalf("ReadJSONWithFs failed: %v", err)
	}

	if len(loaded) != 0 {
		t.Errorf("expected empty manifest, got %d findings", len(loaded))
	}
}
