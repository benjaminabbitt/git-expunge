package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/manifest"
	"github.com/benjaminabbitt/git-expunge/internal/safety"
	"github.com/benjaminabbitt/git-expunge/internal/scanner"
	"github.com/benjaminabbitt/git-expunge/tests/integration/fixtures"
)

func TestEndToEnd_ScanReportReview(t *testing.T) {
	// Create test repo with secrets and binaries
	repo := fixtures.RepoWithSecretAndBinary(t)

	// Step 1: Scan
	config := scanner.DefaultConfig()
	config.SizeThreshold = 10 * 1024 // 10KB for test

	s := scanner.New(config)
	m, err := s.Scan(repo.Path)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(m) == 0 {
		t.Fatal("expected to find some findings")
	}

	// Step 2: Write manifest
	manifestPath := filepath.Join(t.TempDir(), "git-expunge-findings.json")
	if err := manifest.WriteJSON(m, manifestPath); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	// Step 3: Generate report
	reportPath := filepath.Join(t.TempDir(), "manifest.md")
	reportFile, err := os.Create(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.GenerateReport(m, reportFile); err != nil {
		t.Fatalf("GenerateReport failed: %v", err)
	}
	reportFile.Close()

	// Step 4: Read report back
	reportFile, err = os.Open(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := manifest.ParseReport(reportFile)
	reportFile.Close()
	if err != nil {
		t.Fatalf("ParseReport failed: %v", err)
	}

	if len(roundTripped) != len(m) {
		t.Errorf("round-trip lost findings: %d vs %d", len(roundTripped), len(m))
	}

	// Step 5: Mark items for purge
	for _, f := range m {
		f.Purge = true
	}

	purgeCount := m.PurgeCount()
	if purgeCount == 0 {
		t.Error("expected some items marked for purge")
	}

	// Step 6: Verify BlobsToPurge works
	blobs := m.BlobsToPurge()
	if len(blobs) != purgeCount {
		t.Errorf("BlobsToPurge mismatch: %d vs %d", len(blobs), purgeCount)
	}

	t.Logf("E2E test passed: found %d findings, marked %d for purge", len(m), purgeCount)
}

func TestEndToEnd_BackupAndRestore(t *testing.T) {
	// Create test repo
	repo := fixtures.RepoWithBinary(t)

	// Create backup
	backupDir := t.TempDir()
	archive, err := safety.CreateBackup(repo.Path, backupDir)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	// Verify backup
	if err := safety.VerifyBackup(archive.ArchivePath); err != nil {
		t.Fatalf("VerifyBackup failed: %v", err)
	}

	// Restore to new location
	restoreDir := t.TempDir()
	if err := safety.RestoreBackup(archive.ArchivePath, restoreDir); err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	// Verify restored repo has .git directory
	restoredGitDir := filepath.Join(restoreDir, filepath.Base(repo.Path), ".git")
	if _, err := os.Stat(restoredGitDir); os.IsNotExist(err) {
		t.Error("restored repo missing .git directory")
	}

	t.Log("Backup and restore test passed")
}

func TestEndToEnd_FindingTypes(t *testing.T) {
	// Test repo with both types
	repo := fixtures.RepoWithSecretAndBinary(t)

	config := scanner.DefaultConfig()
	config.SizeThreshold = 10 * 1024

	s := scanner.New(config)
	m, err := s.Scan(repo.Path)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	var binaryCount, secretCount int
	for _, f := range m {
		switch f.Type {
		case domain.FindingTypeBinary:
			binaryCount++
		case domain.FindingTypeSecret:
			secretCount++
		}
	}

	if binaryCount == 0 {
		t.Error("expected to find binaries")
	}
	if secretCount == 0 {
		t.Error("expected to find secrets")
	}

	t.Logf("Found %d binaries and %d secrets", binaryCount, secretCount)
}
