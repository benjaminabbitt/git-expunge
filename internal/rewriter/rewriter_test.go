package rewriter

import (
	"bytes"
	"io"
	"testing"
)

// MockGitExecutor is a mock implementation of GitExecutor for testing.
type MockGitExecutor struct {
	FastExportData  []byte
	FastExportErr   error
	FastImportBuf   *bytes.Buffer
	FastImportErr   error
	InitBareErr     error
	GCErr           error
	FastExportCalls int
	FastImportCalls int
	InitBareCalls   int
	GCCalls         int
}

func (m *MockGitExecutor) FastExport(repoPath string) (io.ReadCloser, error) {
	m.FastExportCalls++
	if m.FastExportErr != nil {
		return nil, m.FastExportErr
	}
	return io.NopCloser(bytes.NewReader(m.FastExportData)), nil
}

func (m *MockGitExecutor) FastImport(repoPath string) (io.WriteCloser, func() error, error) {
	m.FastImportCalls++
	if m.FastImportErr != nil {
		return nil, nil, m.FastImportErr
	}
	if m.FastImportBuf == nil {
		m.FastImportBuf = &bytes.Buffer{}
	}
	return &nopWriteCloser{m.FastImportBuf}, func() error { return nil }, nil
}

func (m *MockGitExecutor) InitBare(path string) error {
	m.InitBareCalls++
	return m.InitBareErr
}

func (m *MockGitExecutor) GC(repoPath string) error {
	m.GCCalls++
	return m.GCErr
}

type nopWriteCloser struct {
	io.Writer
}

func (n *nopWriteCloser) Close() error { return nil }

func TestRewriter_DryRun_WithMock(t *testing.T) {
	// Create mock with fast-export data
	mockData := `blob
mark :1
data 5
hello
commit refs/heads/main
mark :2
author Test <test@test.com> 1234567890 +0000
committer Test <test@test.com> 1234567890 +0000
data 4
test
M 100644 :1 file.txt

`

	mock := &MockGitExecutor{
		FastExportData: []byte(mockData),
	}

	r := NewRewriter("/fake/path").WithGitExecutor(mock)
	r.SetDryRun(true)

	// Calculate hash of "hello" blob to exclude
	helloHash := calculateBlobHash([]byte("hello"))

	stats, err := r.Rewrite([]string{helloHash})
	if err != nil {
		t.Fatalf("Rewrite failed: %v", err)
	}

	// Verify mock was called
	if mock.FastExportCalls != 1 {
		t.Errorf("expected 1 FastExport call, got %d", mock.FastExportCalls)
	}

	// Verify stats
	if stats.TotalBlobs != 1 {
		t.Errorf("expected TotalBlobs=1, got %d", stats.TotalBlobs)
	}
	if stats.ExcludedBlobs != 1 {
		t.Errorf("expected ExcludedBlobs=1, got %d", stats.ExcludedBlobs)
	}
}

func TestRewriter_EmptyBlobList(t *testing.T) {
	mock := &MockGitExecutor{}
	r := NewRewriter("/fake/path").WithGitExecutor(mock)

	stats, err := r.Rewrite([]string{})
	if err != nil {
		t.Fatalf("Rewrite failed: %v", err)
	}

	// Should return early without calling git
	if mock.FastExportCalls != 0 {
		t.Errorf("expected 0 FastExport calls for empty list, got %d", mock.FastExportCalls)
	}

	if stats.TotalBlobs != 0 {
		t.Errorf("expected TotalBlobs=0, got %d", stats.TotalBlobs)
	}
}

func TestRewriter_SetDryRun(t *testing.T) {
	r := NewRewriter("/test")

	// Default should be dry-run
	if !r.dryRun {
		t.Error("expected default dryRun=true")
	}

	r.SetDryRun(false)
	if r.dryRun {
		t.Error("expected dryRun=false after SetDryRun(false)")
	}

	r.SetDryRun(true)
	if !r.dryRun {
		t.Error("expected dryRun=true after SetDryRun(true)")
	}
}

func TestRewriter_WithGitExecutor(t *testing.T) {
	mock := &MockGitExecutor{}
	r := NewRewriter("/test").WithGitExecutor(mock)

	if r.git != mock {
		t.Error("WithGitExecutor did not set the executor")
	}
}

func TestNewRewriter(t *testing.T) {
	r := NewRewriter("/test/path")

	if r.repoPath != "/test/path" {
		t.Errorf("expected repoPath=/test/path, got %s", r.repoPath)
	}
	if !r.dryRun {
		t.Error("expected dryRun=true by default")
	}
	if r.git == nil {
		t.Error("expected git executor to be initialized")
	}
}
