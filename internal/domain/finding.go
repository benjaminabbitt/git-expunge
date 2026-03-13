// Package domain contains core domain types for git-expunge.
package domain

// FindingType represents the type of finding (binary or secret).
type FindingType string

const (
	// FindingTypeBinary indicates a binary file.
	FindingTypeBinary FindingType = "binary"
	// FindingTypeSecret indicates a secret/sensitive data.
	FindingTypeSecret FindingType = "secret"
	// FindingTypeAdd indicates a manually added path.
	FindingTypeAdd FindingType = "add"
)

// SecretLocation represents the location of a secret within a file.
type SecretLocation struct {
	// StartLine is the 1-based line number where the secret starts.
	StartLine int `json:"start_line"`
	// EndLine is the 1-based line number where the secret ends.
	EndLine int `json:"end_line"`
	// StartColumn is the 0-based column where the secret starts.
	StartColumn int `json:"start_column"`
	// EndColumn is the 0-based column where the secret ends.
	EndColumn int `json:"end_column"`
	// Match is the actual matched secret string.
	Match string `json:"match"`
}

// Finding represents a detected item that may need to be expunged.
type Finding struct {
	// BlobHash is the git blob SHA that contains this finding.
	BlobHash string `json:"blob_hash"`

	// Type indicates whether this is a binary or secret.
	Type FindingType `json:"type"`

	// Path is the file path where this finding was detected.
	Path string `json:"path"`

	// Size is the size of the blob in bytes.
	Size int64 `json:"size,omitempty"`

	// MimeType is the detected MIME type (for binaries).
	MimeType string `json:"mime_type,omitempty"`

	// Rule is the detection rule that matched (for secrets).
	Rule string `json:"rule,omitempty"`

	// SecretLocations contains the locations of secrets within the file.
	SecretLocations []SecretLocation `json:"secret_locations,omitempty"`

	// Commits lists the commit hashes where this blob appears.
	Commits []string `json:"commits,omitempty"`

	// Purge indicates whether this finding should be removed.
	Purge bool `json:"purge"`
}

// Manifest represents the collection of findings from a scan.
// The key is the blob hash.
type Manifest map[string]*Finding

// NewManifest creates a new empty manifest.
func NewManifest() Manifest {
	return make(Manifest)
}

// Add adds a finding to the manifest.
// If a finding with the same blob hash exists, commits are merged.
func (m Manifest) Add(f *Finding) {
	if existing, ok := m[f.BlobHash]; ok {
		// Merge commits
		existing.Commits = mergeCommits(existing.Commits, f.Commits)
		return
	}
	m[f.BlobHash] = f
}

// PurgeCount returns the number of findings marked for purging.
func (m Manifest) PurgeCount() int {
	count := 0
	for _, f := range m {
		if f.Purge {
			count++
		}
	}
	return count
}

// BlobsToPurge returns a list of blob hashes marked for purging.
func (m Manifest) BlobsToPurge() []string {
	var blobs []string
	for hash, f := range m {
		if f.Purge {
			blobs = append(blobs, hash)
		}
	}
	return blobs
}

// mergeCommits merges two commit lists, removing duplicates.
func mergeCommits(a, b []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, c := range a {
		if !seen[c] {
			seen[c] = true
			result = append(result, c)
		}
	}
	for _, c := range b {
		if !seen[c] {
			seen[c] = true
			result = append(result, c)
		}
	}
	return result
}

// SharedBlobInfo contains information about a blob that appears at multiple paths.
type SharedBlobInfo struct {
	BlobHash string   // The blob hash
	Paths    []string // All paths where this blob content appears
}

// SharedBlobWarning contains warning information about purging a shared blob.
type SharedBlobWarning struct {
	BlobHash       string   // The blob hash being purged
	PurgePath      string   // The path marked for purging in the manifest
	AffectedPaths  []string // Additional paths that will be affected
	TotalLocations int      // Total number of locations this blob appears
}

// SkippedBlob contains information about a blob that was skipped due to shared paths.
type SkippedBlob struct {
	BlobHash      string   // The blob hash that was skipped
	MarkedPath    string   // The path that was marked for purge
	UnmarkedPaths []string // Paths NOT marked for purge that would be affected
}

// SafeBlobsToPurge returns only blobs where ALL paths containing that blob are marked for purge.
// If a blob appears at any path NOT in the manifest or NOT marked for purge, it is excluded.
// Returns the safe blobs and a list of skipped blobs with reasons.
func (m Manifest) SafeBlobsToPurge(allPathsForBlobs map[string][]string) ([]string, []SkippedBlob) {
	// Build a map of paths marked for purge
	purgedPaths := make(map[string]bool)
	for _, f := range m {
		if f.Purge {
			purgedPaths[f.Path] = true
		}
	}

	var safeBlobs []string
	var skipped []SkippedBlob

	for _, f := range m {
		if !f.Purge {
			continue
		}

		paths, exists := allPathsForBlobs[f.BlobHash]
		if !exists || len(paths) == 0 {
			// No path info available, include the blob (conservative for backwards compat)
			safeBlobs = append(safeBlobs, f.BlobHash)
			continue
		}

		// Check if ALL paths for this blob are marked for purge
		var unmarkedPaths []string
		for _, p := range paths {
			if !purgedPaths[p] {
				unmarkedPaths = append(unmarkedPaths, p)
			}
		}

		if len(unmarkedPaths) == 0 {
			// All paths are marked for purge - safe to include
			safeBlobs = append(safeBlobs, f.BlobHash)
		} else {
			// Some paths are NOT marked - skip this blob
			skipped = append(skipped, SkippedBlob{
				BlobHash:      f.BlobHash,
				MarkedPath:    f.Path,
				UnmarkedPaths: unmarkedPaths,
			})
		}
	}

	return safeBlobs, skipped
}

// GetSharedBlobWarnings returns warnings for blobs marked for purge that appear at multiple paths.
// This helps users understand that purging one path will affect all paths with the same content.
func (m Manifest) GetSharedBlobWarnings(allPathsForBlobs map[string][]string) []SharedBlobWarning {
	var warnings []SharedBlobWarning

	for _, f := range m {
		if !f.Purge {
			continue
		}

		paths, exists := allPathsForBlobs[f.BlobHash]
		if !exists || len(paths) <= 1 {
			continue
		}

		// This blob appears at multiple paths - create a warning
		var affectedPaths []string
		for _, p := range paths {
			if p != f.Path {
				affectedPaths = append(affectedPaths, p)
			}
		}

		if len(affectedPaths) > 0 {
			warnings = append(warnings, SharedBlobWarning{
				BlobHash:       f.BlobHash,
				PurgePath:      f.Path,
				AffectedPaths:  affectedPaths,
				TotalLocations: len(paths),
			})
		}
	}

	return warnings
}
