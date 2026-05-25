package integration

import (
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/scanner"
	"github.com/benjaminabbitt/git-expunge/tests/integration/fixtures"
)

func TestScanner_BinaryDetection(t *testing.T) {
	repo := fixtures.RepoWithBinary(t)

	// Isolate the binary detector — other detectors are tested separately.
	config := scanner.Config{ScanBinaries: true}

	s := scanner.New(config)
	manifest, err := s.Scan(repo.Path)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Should find at least one binary
	binaryCount := 0
	for _, f := range manifest {
		if f.Type == domain.FindingTypeBinary {
			binaryCount++
		}
	}

	if binaryCount == 0 {
		t.Error("expected to find at least one binary, found none")
	}

	// Check that the binary we added was found
	found := false
	for _, f := range manifest {
		if f.Path == "bin/app" {
			found = true
			if f.Type != domain.FindingTypeBinary {
				t.Errorf("expected type binary, got %s", f.Type)
			}
			break
		}
	}

	if !found {
		t.Error("expected to find bin/app in manifest")
	}
}

func TestScanner_EmptyRepo(t *testing.T) {
	repo := fixtures.EmptyRepo(t)

	config := scanner.DefaultConfig()
	s := scanner.New(config)

	manifest, err := s.Scan(repo.Path)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(manifest) != 0 {
		t.Errorf("expected empty manifest for empty repo, got %d findings", len(manifest))
	}
}

// TestScanner_SizeThreshold pins down that SizeThreshold gates the
// LargeFileDetector and ONLY the LargeFileDetector. Binary detection is
// orthogonal — a tiny PNG is still flagged as binary regardless.
func TestScanner_SizeThreshold(t *testing.T) {
	repo := fixtures.RepoWithBinary(t)

	tests := []struct {
		name           string
		threshold      int64
		expectFindings bool
	}{
		{
			name:           "threshold below binary size",
			threshold:      10 * 1024, // 10KB
			expectFindings: true,
		},
		{
			name:           "threshold above binary size",
			threshold:      200 * 1024, // 200KB
			expectFindings: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Only run the large-file detector for this test — others
			// would muddy the size-gating signal.
			config := scanner.Config{
				ScanLargeFiles: true,
				SizeThreshold:  tt.threshold,
			}

			s := scanner.New(config)
			manifest, err := s.Scan(repo.Path)
			if err != nil {
				t.Fatalf("Scan failed: %v", err)
			}

			hasFindings := len(manifest) > 0
			if hasFindings != tt.expectFindings {
				t.Errorf("expected findings=%v, got %d findings", tt.expectFindings, len(manifest))
			}
		})
	}
}

func TestScanner_SecretDetection(t *testing.T) {
	repo := fixtures.RepoWithSecret(t)

	config := scanner.Config{ScanSecrets: true}

	s := scanner.New(config)
	manifest, err := s.Scan(repo.Path)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Should find at least one secret
	secretCount := 0
	for _, f := range manifest {
		if f.Type == domain.FindingTypeSecret {
			secretCount++
		}
	}

	if secretCount == 0 {
		t.Error("expected to find at least one secret, found none")
	}

	// Check that the .env file was flagged
	found := false
	for _, f := range manifest {
		if f.Path == ".env" && f.Type == domain.FindingTypeSecret {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected to find .env in manifest as secret")
	}
}

func TestScanner_CombinedDetection(t *testing.T) {
	repo := fixtures.RepoWithSecretAndBinary(t)

	config := scanner.Config{
		ScanBinaries: true,
		ScanSecrets:  true,
	}

	s := scanner.New(config)
	manifest, err := s.Scan(repo.Path)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	binaryCount := 0
	secretCount := 0
	for _, f := range manifest {
		switch f.Type {
		case domain.FindingTypeBinary:
			binaryCount++
		case domain.FindingTypeSecret:
			secretCount++
		}
	}

	if binaryCount == 0 {
		t.Error("expected to find at least one binary")
	}
	if secretCount == 0 {
		t.Error("expected to find at least one secret")
	}
}

func TestManifest_Operations(t *testing.T) {
	manifest := domain.NewManifest()

	// Add findings
	manifest.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Commits:  []string{"commit1"},
	})

	manifest.Add(&domain.Finding{
		BlobHash: "def456",
		Type:     domain.FindingTypeSecret,
		Path:     "secrets.env",
		Commits:  []string{"commit2"},
	})

	// Membership in the manifest is the intent — every entry is "to be purged".
	if len(manifest) != 2 {
		t.Errorf("expected 2 entries, got %d", len(manifest))
	}

	blobs := manifest.Blobs()
	if len(blobs) != 2 {
		t.Errorf("expected Blobs() len=2, got %v", blobs)
	}

	// Test merge commits
	manifest.Add(&domain.Finding{
		BlobHash: "abc123",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Commits:  []string{"commit3"},
	})

	if f := manifest["abc123"]; len(f.Commits) != 2 {
		t.Errorf("expected 2 commits after merge, got %d", len(f.Commits))
	}
}
