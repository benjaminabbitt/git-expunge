// Package domain contains core domain types for git-expunge.
//
// The manifest is the authoritative list of blobs to be removed from
// history. Membership in the manifest is the intent — there is no
// separate "marked for purge" flag. A blob in the manifest will be
// removed on the next `git-expunge rewrite --execute`; remove it from
// the manifest first if you don't want that.
package domain

// FindingType categorises why a blob is in the manifest.
type FindingType string

const (
	// FindingTypeBinary indicates the blob was classified as binary by
	// MIME/magic-byte detection.
	FindingTypeBinary FindingType = "binary"
	// FindingTypeSecret indicates a secret/sensitive content match
	// (gitleaks rule).
	FindingTypeSecret FindingType = "secret"
	// FindingTypeLargeFile indicates the blob exceeded the size
	// threshold of the LargeFileDetector — irrespective of MIME type.
	FindingTypeLargeFile FindingType = "large_file"
	// FindingTypeAdd indicates a path-matched addition (from the `add`
	// command or `scan gitignored`).
	FindingTypeAdd FindingType = "add"
)

// SecretLocation represents the location of a secret within a file.
type SecretLocation struct {
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	StartColumn int    `json:"start_column"`
	EndColumn   int    `json:"end_column"`
	Match       string `json:"match"`
}

// Finding represents an entry in the manifest — a blob targeted for
// removal from history, with metadata explaining why.
type Finding struct {
	BlobHash        string           `json:"blob_hash"`
	Type            FindingType      `json:"type"`
	Path            string           `json:"path"`
	Size            int64            `json:"size,omitempty"`
	MimeType        string           `json:"mime_type,omitempty"`
	Rule            string           `json:"rule,omitempty"`
	SecretLocations []SecretLocation `json:"secret_locations,omitempty"`
	Commits         []string         `json:"commits,omitempty"`
}

// Manifest is the collection of findings keyed by blob hash. Every
// entry is implicitly "to be purged" — there is no per-entry flag.
type Manifest map[string]*Finding

// NewManifest creates a new empty manifest.
func NewManifest() Manifest {
	return make(Manifest)
}

// Add adds a finding to the manifest. If a finding with the same blob
// hash exists, its commit list is unioned with the incoming finding's
// commit list; other fields are left untouched (first writer wins on
// Type, Path, etc.).
func (m Manifest) Add(f *Finding) {
	if existing, ok := m[f.BlobHash]; ok {
		existing.Commits = mergeCommits(existing.Commits, f.Commits)
		return
	}
	m[f.BlobHash] = f
}

// Remove deletes the finding with the given blob hash. Returns true if
// anything was removed.
func (m Manifest) Remove(hash string) bool {
	if _, ok := m[hash]; !ok {
		return false
	}
	delete(m, hash)
	return true
}

// Merge folds another manifest into m, applying Add's merge-on-collision
// semantics for existing hashes. Returns the count of newly inserted
// entries.
func (m Manifest) Merge(other Manifest) int {
	added := 0
	for _, f := range other {
		if _, existed := m[f.BlobHash]; !existed {
			added++
		}
		m.Add(f)
	}
	return added
}

// Blobs returns every blob hash in the manifest. The order is
// unspecified — callers that need stable ordering should sort.
func (m Manifest) Blobs() []string {
	blobs := make([]string, 0, len(m))
	for hash := range m {
		blobs = append(blobs, hash)
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

// SharedBlobWarning describes a blob in the manifest that also appears
// at paths NOT in the manifest. Purging it would remove content from
// those un-manifested paths too — surfacing this lets the user opt in.
type SharedBlobWarning struct {
	BlobHash       string   // The blob hash being purged
	PurgePath      string   // The path recorded on the manifest entry
	AffectedPaths  []string // Other paths the same blob appears at (not in the manifest)
	TotalLocations int      // Total paths this blob appears at, across history
}

// SkippedBlob describes a blob excluded by SafeBlobs because one or
// more of its paths is not in the manifest (so purging would also
// remove content the user didn't request).
type SkippedBlob struct {
	BlobHash      string
	MarkedPath    string   // The path recorded on the manifest entry
	UnmarkedPaths []string // Paths NOT in the manifest that would be affected
}

// SafeBlobs returns blobs from the manifest that can be purged without
// removing un-manifested paths. A blob is "safe" when every path the
// blob appears at across history is also a path in the manifest.
// Pass allPathsForBlobs = blobhash -> []historical-path (computed by
// the gitquery layer).
func (m Manifest) SafeBlobs(allPathsForBlobs map[string][]string) ([]string, []SkippedBlob) {
	manifestPaths := make(map[string]bool, len(m))
	for _, f := range m {
		manifestPaths[f.Path] = true
	}

	var safe []string
	var skipped []SkippedBlob

	for hash, f := range m {
		paths, ok := allPathsForBlobs[hash]
		if !ok || len(paths) == 0 {
			// Caller didn't supply path info for this blob — conservatively include it.
			safe = append(safe, hash)
			continue
		}

		var unmarked []string
		for _, p := range paths {
			if !manifestPaths[p] {
				unmarked = append(unmarked, p)
			}
		}
		if len(unmarked) == 0 {
			safe = append(safe, hash)
		} else {
			skipped = append(skipped, SkippedBlob{
				BlobHash:      hash,
				MarkedPath:    f.Path,
				UnmarkedPaths: unmarked,
			})
		}
	}

	return safe, skipped
}

// SharedBlobWarnings returns warnings for blobs in the manifest that
// appear at multiple historical paths. The user might be unaware they're
// taking out content beyond the path on the manifest entry.
func (m Manifest) SharedBlobWarnings(allPathsForBlobs map[string][]string) []SharedBlobWarning {
	var warnings []SharedBlobWarning
	for hash, f := range m {
		paths, ok := allPathsForBlobs[hash]
		if !ok || len(paths) <= 1 {
			continue
		}
		var affected []string
		for _, p := range paths {
			if p != f.Path {
				affected = append(affected, p)
			}
		}
		if len(affected) == 0 {
			continue
		}
		warnings = append(warnings, SharedBlobWarning{
			BlobHash:       hash,
			PurgePath:      f.Path,
			AffectedPaths:  affected,
			TotalLocations: len(paths),
		})
	}
	return warnings
}
