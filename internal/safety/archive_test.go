package safety

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
)

func TestCreateAndRestoreBackup_WithMemFs(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Create source directory with test files
	srcDir := "/repo"
	if err := fs.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, filepath.Join(srcDir, "file1.txt"), []byte("content1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, filepath.Join(srcDir, "subdir", "file2.txt"), []byte("content2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create backup
	backupDir := "/backups"
	archive, err := CreateBackupWithFs(fs, srcDir, backupDir)
	if err != nil {
		t.Fatalf("CreateBackupWithFs failed: %v", err)
	}

	// Verify backup exists
	exists, err := afero.Exists(fs, archive.ArchivePath)
	if err != nil {
		t.Fatalf("failed to check archive existence: %v", err)
	}
	if !exists {
		t.Error("archive file was not created")
	}

	// Verify backup
	if err := VerifyBackupWithFs(fs, archive.ArchivePath); err != nil {
		t.Errorf("VerifyBackupWithFs failed: %v", err)
	}

	// Restore to new location
	restoreDir := "/restored"
	if err := RestoreBackupWithFs(fs, archive.ArchivePath, restoreDir); err != nil {
		t.Fatalf("RestoreBackupWithFs failed: %v", err)
	}

	// Verify restored files
	restoredBase := filepath.Join(restoreDir, filepath.Base(srcDir))

	content1, err := afero.ReadFile(fs, filepath.Join(restoredBase, "file1.txt"))
	if err != nil {
		t.Errorf("failed to read restored file1.txt: %v", err)
	}
	if string(content1) != "content1" {
		t.Errorf("file1.txt content mismatch: got %q", string(content1))
	}

	content2, err := afero.ReadFile(fs, filepath.Join(restoredBase, "subdir", "file2.txt"))
	if err != nil {
		t.Errorf("failed to read restored file2.txt: %v", err)
	}
	if string(content2) != "content2" {
		t.Errorf("file2.txt content mismatch: got %q", string(content2))
	}
}

func TestCreateAndRestoreBackup(t *testing.T) {
	// Integration test using real filesystem
	srcDir := t.TempDir()

	// Create test files
	if err := os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("content1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "subdir", "file2.txt"), []byte("content2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create backup
	backupDir := t.TempDir()
	archive, err := CreateBackup(srcDir, backupDir)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	// Verify backup exists
	if _, err := os.Stat(archive.ArchivePath); os.IsNotExist(err) {
		t.Error("archive file was not created")
	}

	// Verify backup
	if err := VerifyBackup(archive.ArchivePath); err != nil {
		t.Errorf("VerifyBackup failed: %v", err)
	}

	// Restore to new location
	restoreDir := t.TempDir()
	if err := RestoreBackup(archive.ArchivePath, restoreDir); err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	// Verify restored files
	restoredBase := filepath.Join(restoreDir, filepath.Base(srcDir))

	content1, err := os.ReadFile(filepath.Join(restoredBase, "file1.txt"))
	if err != nil {
		t.Errorf("failed to read restored file1.txt: %v", err)
	}
	if string(content1) != "content1" {
		t.Errorf("file1.txt content mismatch: got %q", string(content1))
	}

	content2, err := os.ReadFile(filepath.Join(restoredBase, "subdir", "file2.txt"))
	if err != nil {
		t.Errorf("failed to read restored file2.txt: %v", err)
	}
	if string(content2) != "content2" {
		t.Errorf("file2.txt content mismatch: got %q", string(content2))
	}
}

func TestVerifyBackup_InvalidFile(t *testing.T) {
	// Create invalid archive
	tmpFile := filepath.Join(t.TempDir(), "invalid.tar.gz")
	if err := os.WriteFile(tmpFile, []byte("not a valid archive"), 0644); err != nil {
		t.Fatal(err)
	}

	err := VerifyBackup(tmpFile)
	if err == nil {
		t.Error("expected error for invalid archive")
	}
}

func TestVerifyBackup_InvalidFile_WithMemFs(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Create invalid archive
	archivePath := "/backups/invalid.tar.gz"
	if err := fs.MkdirAll("/backups", 0755); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, archivePath, []byte("not a valid archive"), 0644); err != nil {
		t.Fatal(err)
	}

	err := VerifyBackupWithFs(fs, archivePath)
	if err == nil {
		t.Error("expected error for invalid archive")
	}
}

func TestListBackups(t *testing.T) {
	backupDir := t.TempDir()

	// Create some fake backup files
	os.WriteFile(filepath.Join(backupDir, "repo-backup-20240101-120000.tar.gz"), []byte{}, 0644)
	os.WriteFile(filepath.Join(backupDir, "repo-backup-20240102-120000.tar.gz"), []byte{}, 0644)
	os.WriteFile(filepath.Join(backupDir, "other-file.txt"), []byte{}, 0644)

	backups, err := ListBackups(backupDir)
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}

	if len(backups) != 2 {
		t.Errorf("expected 2 backups, got %d", len(backups))
	}
}

func TestListBackups_WithMemFs(t *testing.T) {
	fs := afero.NewMemMapFs()
	backupDir := "/backups"

	// Create backup directory
	if err := fs.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create some fake backup files
	afero.WriteFile(fs, filepath.Join(backupDir, "repo-backup-20240101-120000.tar.gz"), []byte{}, 0644)
	afero.WriteFile(fs, filepath.Join(backupDir, "repo-backup-20240102-120000.tar.gz"), []byte{}, 0644)
	afero.WriteFile(fs, filepath.Join(backupDir, "other-file.txt"), []byte{}, 0644)

	backups, err := ListBackupsWithFs(fs, backupDir)
	if err != nil {
		t.Fatalf("ListBackupsWithFs failed: %v", err)
	}

	if len(backups) != 2 {
		t.Errorf("expected 2 backups, got %d", len(backups))
	}
}

func TestCreateBackup_ErrorCases(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Test with non-existent source directory
	_, err := CreateBackupWithFs(fs, "/nonexistent", "/backups")
	if err == nil {
		t.Error("expected error for non-existent source")
	}
}

func TestRestoreBackup_ErrorCases(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Test with non-existent archive
	err := RestoreBackupWithFs(fs, "/nonexistent.tar.gz", "/restored")
	if err == nil {
		t.Error("expected error for non-existent archive")
	}
}

func TestVerifyBackup_EmptyArchive(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Create source directory (empty)
	srcDir := "/emptyrepo"
	if err := fs.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create backup of empty directory
	backupDir := "/backups"
	archive, err := CreateBackupWithFs(fs, srcDir, backupDir)
	if err != nil {
		t.Fatalf("CreateBackupWithFs failed: %v", err)
	}

	// Verify should fail for empty archive
	err = VerifyBackupWithFs(fs, archive.ArchivePath)
	if err == nil {
		t.Error("expected error for empty archive")
	}
}
