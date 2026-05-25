package scanner

import (
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/gitquery"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if !config.ScanSecrets {
		t.Error("expected ScanSecrets=true by default (high-confidence)")
	}
	if !config.ScanGitignored {
		t.Error("expected ScanGitignored=true by default (high-confidence)")
	}
	if config.ScanBinaries {
		t.Error("expected ScanBinaries=false by default — too noisy for the safe default")
	}
	if config.ScanLargeFiles {
		t.Error("expected ScanLargeFiles=false by default — too noisy for the safe default")
	}
	if config.SizeThreshold != 100*1024 {
		t.Errorf("expected SizeThreshold=102400, got %d", config.SizeThreshold)
	}
}

func TestNew(t *testing.T) {
	config := Config{
		ScanSecrets:   true,
		ScanBinaries:  true,
		SizeThreshold: 50 * 1024,
	}

	s := New(config)

	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.binaryDetector == nil {
		t.Error("binaryDetector not initialized")
	}
	if s.config.SizeThreshold != 50*1024 {
		t.Errorf("config not set correctly")
	}
}

func TestNew_WithoutSecrets(t *testing.T) {
	config := Config{
		ScanSecrets:   false,
		ScanBinaries:  true,
		SizeThreshold: 100 * 1024,
	}

	s := New(config)

	if s == nil {
		t.Fatal("New returned nil")
	}
	// Secret detector should be nil when scanning disabled
	if s.secretDetector != nil {
		t.Error("secretDetector should be nil when ScanSecrets=false")
	}
}

func TestMergeStringSlices(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected int
	}{
		{
			name:     "no overlap",
			a:        []string{"a", "b"},
			b:        []string{"c", "d"},
			expected: 4,
		},
		{
			name:     "with overlap",
			a:        []string{"a", "b", "c"},
			b:        []string{"b", "c", "d"},
			expected: 4,
		},
		{
			name:     "empty first",
			a:        []string{},
			b:        []string{"a", "b"},
			expected: 2,
		},
		{
			name:     "empty second",
			a:        []string{"a", "b"},
			b:        []string{},
			expected: 2,
		},
		{
			name:     "both empty",
			a:        []string{},
			b:        []string{},
			expected: 0,
		},
		{
			name:     "all duplicates",
			a:        []string{"a", "b"},
			b:        []string{"a", "b"},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeStringSlices(tt.a, tt.b)
			if len(result) != tt.expected {
				t.Errorf("expected %d elements, got %d: %v", tt.expected, len(result), result)
			}
		})
	}
}

// MockWalker is a mock implementation of RepoWalker for testing.
type MockWalker struct {
	Blobs []*gitquery.BlobInfo
	Err   error
}

func (m *MockWalker) Walk(handler gitquery.BlobHandler) error {
	if m.Err != nil {
		return m.Err
	}
	for _, blob := range m.Blobs {
		if err := handler(blob); err != nil {
			return err
		}
	}
	return nil
}

func TestScanner_Scan_WithMockWalker(t *testing.T) {
	// Create mock blobs
	binaryContent := make([]byte, 200*1024) // 200KB
	binaryContent[0] = 0x7f
	binaryContent[1] = 0x45
	binaryContent[2] = 0x4c
	binaryContent[3] = 0x46

	secretContent := []byte("AWS_ACCESS_KEY_ID=AKIAZT5K7YFAPXR3VBCD")

	mockWalker := &MockWalker{
		Blobs: []*gitquery.BlobInfo{
			{
				Hash:       "binaryhash123",
				Path:       "bin/app",
				Size:       int64(len(binaryContent)),
				CommitHash: "commit1",
				Content:    func() ([]byte, error) { return binaryContent, nil },
			},
			{
				Hash:       "secrethash456",
				Path:       ".env",
				Size:       int64(len(secretContent)),
				CommitHash: "commit2",
				Content:    func() ([]byte, error) { return secretContent, nil },
			},
		},
	}

	// MockWalker bypasses the real git layer; we deliberately leave
	// ScanGitignored off so the post-walk pass doesn't try to open the
	// fake repo path.
	config := Config{
		ScanBinaries: true,
		ScanSecrets:  true,
	}

	s := New(config).WithWalkerFactory(func(path string) (RepoWalker, error) {
		return mockWalker, nil
	})

	manifest, err := s.Scan("/fake/repo")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Should find both binary and secret
	if len(manifest) < 1 {
		t.Errorf("expected at least 1 finding, got %d", len(manifest))
	}

	// Check for binary
	var foundBinary, foundSecret bool
	for _, f := range manifest {
		if f.Type == domain.FindingTypeBinary {
			foundBinary = true
		}
		if f.Type == domain.FindingTypeSecret {
			foundSecret = true
		}
	}

	if !foundBinary {
		t.Error("expected to find binary")
	}
	if !foundSecret {
		t.Error("expected to find secret")
	}
}

func TestScanner_Scan_BinaryOnly(t *testing.T) {
	binaryContent := make([]byte, 200*1024)
	binaryContent[0] = 0x7f
	binaryContent[1] = 0x45
	binaryContent[2] = 0x4c
	binaryContent[3] = 0x46

	mockWalker := &MockWalker{
		Blobs: []*gitquery.BlobInfo{
			{
				Hash:       "hash1",
				Path:       "bin/app",
				Size:       int64(len(binaryContent)),
				CommitHash: "c1",
				Content:    func() ([]byte, error) { return binaryContent, nil },
			},
		},
	}

	config := Config{
		ScanBinaries:  true,
		ScanSecrets:   false,
		SizeThreshold: 100 * 1024,
	}

	s := New(config).WithWalkerFactory(func(path string) (RepoWalker, error) {
		return mockWalker, nil
	})

	manifest, err := s.Scan("/fake/repo")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(manifest) != 1 {
		t.Errorf("expected 1 finding, got %d", len(manifest))
	}

	for _, f := range manifest {
		if f.Type != domain.FindingTypeBinary {
			t.Errorf("expected binary type, got %s", f.Type)
		}
	}
}

func TestScanner_Scan_EmptyRepo(t *testing.T) {
	mockWalker := &MockWalker{
		Blobs: []*gitquery.BlobInfo{},
	}

	config := Config{ScanBinaries: true, ScanSecrets: true}
	s := New(config).WithWalkerFactory(func(path string) (RepoWalker, error) {
		return mockWalker, nil
	})

	manifest, err := s.Scan("/fake/repo")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(manifest) != 0 {
		t.Errorf("expected empty manifest, got %d findings", len(manifest))
	}
}

// TestScanner_Scan_GitignoredPass pins down that ScanGitignored=true
// triggers the post-walk pass and merges its findings into the manifest.
func TestScanner_Scan_GitignoredPass(t *testing.T) {
	mockWalker := &MockWalker{Blobs: []*gitquery.BlobInfo{}}

	gitignoreCalls := 0
	mockGitignore := func(repoPath string, existing domain.Manifest) (domain.Manifest, error) {
		gitignoreCalls++
		m := domain.NewManifest()
		m.Add(&domain.Finding{
			BlobHash: "gi-hash",
			Type:     domain.FindingTypeAdd,
			Path:     "ignored.env",
			Rule:     "gitignore:.gitignore:*.env",
		})
		return m, nil
	}

	config := Config{ScanGitignored: true}
	s := New(config).
		WithWalkerFactory(func(string) (RepoWalker, error) { return mockWalker, nil }).
		WithGitignoreScanner(mockGitignore)

	manifest, err := s.Scan("/any/path")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if gitignoreCalls != 1 {
		t.Errorf("expected gitignore scanner called once, got %d", gitignoreCalls)
	}
	if _, ok := manifest["gi-hash"]; !ok {
		t.Errorf("expected gitignored finding in manifest, got %+v", manifest)
	}
}

// TestScanner_Scan_GitignoredDisabled_DoesNotInvokePass guards against
// regressions where the post-walk pass runs unconditionally.
func TestScanner_Scan_GitignoredDisabled_DoesNotInvokePass(t *testing.T) {
	mockWalker := &MockWalker{Blobs: []*gitquery.BlobInfo{}}

	called := false
	mockGitignore := func(repoPath string, existing domain.Manifest) (domain.Manifest, error) {
		called = true
		return domain.NewManifest(), nil
	}

	config := Config{ScanBinaries: true, ScanGitignored: false}
	s := New(config).
		WithWalkerFactory(func(string) (RepoWalker, error) { return mockWalker, nil }).
		WithGitignoreScanner(mockGitignore)

	if _, err := s.Scan("/any/path"); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if called {
		t.Error("gitignore scanner should not be invoked when ScanGitignored=false")
	}
}

func TestScanner_WithWalkerFactory(t *testing.T) {
	customFactory := func(path string) (RepoWalker, error) {
		return &MockWalker{}, nil
	}

	config := Config{ScanBinaries: true}
	s := New(config).WithWalkerFactory(customFactory)

	// Verify factory was set by running a scan
	_, err := s.Scan("/any/path")
	if err != nil {
		t.Errorf("scan with custom factory failed: %v", err)
	}
}
