package scanner

import (
	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/gitquery"
)

// LargeFileDetector flags blobs that exceed a size threshold, irrespective
// of MIME type. Size is the only axis; text content that crosses the
// threshold is just as much a candidate as a binary.
type LargeFileDetector struct {
	threshold int64
}

// NewLargeFileDetector creates a LargeFileDetector that flags blobs
// strictly larger than threshold bytes.
func NewLargeFileDetector(threshold int64) *LargeFileDetector {
	return &LargeFileDetector{threshold: threshold}
}

// Detect returns a finding when the blob's size is greater than the
// configured threshold. Returns nil otherwise.
func (d *LargeFileDetector) Detect(blob *gitquery.BlobInfo) *domain.Finding {
	if blob.Size <= d.threshold {
		return nil
	}
	return &domain.Finding{
		BlobHash: blob.Hash,
		Type:     domain.FindingTypeLargeFile,
		Path:     blob.Path,
		Size:     blob.Size,
		Commits:  []string{blob.CommitHash},
	}
}
