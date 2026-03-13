// Package safety provides backup and restore functionality.
package safety

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/afero"
)

// Fs is the filesystem used by the safety package. Defaults to the OS filesystem.
var Fs afero.Fs = afero.NewOsFs()

// Archive creates a backup archive of a repository.
type Archive struct {
	RepoPath    string
	ArchivePath string
	CreatedAt   time.Time
}

// CreateBackup creates a tar.gz backup of the entire repository.
// Uses shell tar for speed when available, falls back to Go implementation.
func CreateBackup(repoPath, backupDir string) (*Archive, error) {
	// Try fast shell-based backup first
	archive, err := createBackupShell(repoPath, backupDir)
	if err == nil {
		return archive, nil
	}
	// Fall back to Go implementation
	return CreateBackupWithFs(Fs, repoPath, backupDir)
}

// createBackupShell uses shell tar command for faster backup
func createBackupShell(repoPath, backupDir string) (*Archive, error) {
	// Check if tar is available
	if _, err := exec.LookPath("tar"); err != nil {
		return nil, fmt.Errorf("tar not found")
	}

	// Resolve absolute paths
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repo path: %w", err)
	}

	if backupDir == "" {
		backupDir = filepath.Dir(absRepoPath)
	}

	// Create backup directory if needed
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Generate archive name with timestamp
	repoName := filepath.Base(absRepoPath)
	timestamp := time.Now().Format("20060102-150405")
	archiveName := fmt.Sprintf("%s-backup-%s.tar.gz", repoName, timestamp)
	archivePath := filepath.Join(backupDir, archiveName)

	// Run tar command
	cmd := exec.Command("tar", "-czf", archivePath, "-C", filepath.Dir(absRepoPath), repoName)
	if err := cmd.Run(); err != nil {
		os.Remove(archivePath) // Clean up on error
		return nil, fmt.Errorf("tar failed: %w", err)
	}

	return &Archive{
		RepoPath:    absRepoPath,
		ArchivePath: archivePath,
		CreatedAt:   time.Now(),
	}, nil
}

// CreateBackupWithFs creates a backup using the provided filesystem.
func CreateBackupWithFs(fs afero.Fs, repoPath, backupDir string) (*Archive, error) {
	// Resolve absolute paths
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repo path: %w", err)
	}

	if backupDir == "" {
		backupDir = filepath.Dir(absRepoPath)
	}

	// Create backup directory if needed
	if err := fs.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Generate archive name with timestamp
	repoName := filepath.Base(absRepoPath)
	timestamp := time.Now().Format("20060102-150405")
	archiveName := fmt.Sprintf("%s-backup-%s.tar.gz", repoName, timestamp)
	archivePath := filepath.Join(backupDir, archiveName)

	// Create archive file
	archiveFile, err := fs.Create(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create archive file: %w", err)
	}
	defer archiveFile.Close()

	// Create gzip writer
	gzWriter := gzip.NewWriter(archiveFile)
	defer gzWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Walk the repository and add files to archive
	err = afero.Walk(fs, absRepoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path for archive
		relPath, err := filepath.Rel(absRepoPath, path)
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if relPath == "." {
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.Join(repoName, relPath)

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			if reader, ok := fs.(afero.LinkReader); ok {
				link, err := reader.ReadlinkIfPossible(path)
				if err != nil {
					return err
				}
				header.Linkname = link
			}
		}

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		// Write file content (skip directories and symlinks)
		if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			file, err := fs.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		fs.Remove(archivePath) // Clean up on error
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}

	return &Archive{
		RepoPath:    absRepoPath,
		ArchivePath: archivePath,
		CreatedAt:   time.Now(),
	}, nil
}

// RestoreBackup restores a repository from a backup archive.
func RestoreBackup(archivePath, destPath string) error {
	return RestoreBackupWithFs(Fs, archivePath, destPath)
}

// RestoreBackupWithFs restores a backup using the provided filesystem.
func RestoreBackupWithFs(fs afero.Fs, archivePath, destPath string) error {
	// Open archive file
	archiveFile, err := fs.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer archiveFile.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	// Extract files
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Determine target path
		targetPath := filepath.Join(destPath, header.Name)

		// Ensure parent directory exists
		if err := fs.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := fs.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return err
			}

		case tar.TypeReg:
			file, err := fs.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, tarReader); err != nil {
				file.Close()
				return err
			}
			file.Close()

		case tar.TypeSymlink:
			if linker, ok := fs.(afero.Linker); ok {
				if err := linker.SymlinkIfPossible(header.Linkname, targetPath); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// ListBackups returns available backup archives in a directory.
func ListBackups(backupDir string) ([]string, error) {
	return ListBackupsWithFs(Fs, backupDir)
}

// ListBackupsWithFs lists backups using the provided filesystem.
func ListBackupsWithFs(fs afero.Fs, backupDir string) ([]string, error) {
	entries, err := afero.ReadDir(fs, backupDir)
	if err != nil {
		return nil, err
	}

	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".gz" {
			backups = append(backups, filepath.Join(backupDir, entry.Name()))
		}
	}

	return backups, nil
}

// VerifyBackup checks that an archive is valid and can be read.
func VerifyBackup(archivePath string) error {
	return VerifyBackupWithFs(Fs, archivePath)
}

// VerifyBackupWithFs verifies a backup using the provided filesystem.
func VerifyBackupWithFs(fs afero.Fs, archivePath string) error {
	archiveFile, err := fs.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer archiveFile.Close()

	gzReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("invalid gzip archive: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	// Read through all headers to verify integrity
	count := 0
	for {
		_, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("corrupted archive at entry %d: %w", count, err)
		}
		count++
	}

	if count == 0 {
		return fmt.Errorf("archive is empty")
	}

	return nil
}
