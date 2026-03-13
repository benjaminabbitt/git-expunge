package scanner

import (
	"sync"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/zricethezav/gitleaks/v8/detect"
)

// SecretDetector detects secrets in file content using gitleaks rules.
type SecretDetector struct {
	detector *detect.Detector
	mu       sync.Mutex
}

// NewSecretDetector creates a new SecretDetector with default gitleaks rules.
func NewSecretDetector() (*SecretDetector, error) {
	detector, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, err
	}

	// Configure detector settings
	detector.Redact = 0 // Don't redact - we need full info for manifest
	detector.Verbose = false

	return &SecretDetector{
		detector: detector,
	}, nil
}

// Detect checks if a blob contains secrets.
// Returns a slice of findings (one per secret found).
func (d *SecretDetector) Detect(blob *BlobInfo) []*domain.Finding {
	content, err := blob.Content()
	if err != nil {
		return nil
	}

	// Skip very large files to avoid performance issues
	if len(content) > 10*1024*1024 { // 10MB
		return nil
	}

	// Use gitleaks to detect secrets
	d.mu.Lock()
	findings := d.detector.DetectBytes(content)
	d.mu.Unlock()

	if len(findings) == 0 {
		return nil
	}

	// Convert gitleaks findings to our domain findings
	// Group by rule (same secret type in same file = one finding)
	ruleFindings := make(map[string]*domain.Finding)

	for _, f := range findings {
		key := f.RuleID

		loc := domain.SecretLocation{
			StartLine:   f.StartLine,
			EndLine:     f.EndLine,
			StartColumn: f.StartColumn,
			EndColumn:   f.EndColumn,
			Match:       f.Match,
		}

		if existing, ok := ruleFindings[key]; ok {
			// Already have a finding for this rule, add location
			existing.Commits = mergeStringSlices(existing.Commits, []string{blob.CommitHash})
			existing.SecretLocations = append(existing.SecretLocations, loc)
		} else {
			ruleFindings[key] = &domain.Finding{
				BlobHash:        blob.Hash,
				Type:            domain.FindingTypeSecret,
				Path:            blob.Path,
				Size:            blob.Size,
				Rule:            f.RuleID,
				SecretLocations: []domain.SecretLocation{loc},
				Commits:         []string{blob.CommitHash},
				Purge:           false,
			}
		}
	}

	// Convert map to slice
	result := make([]*domain.Finding, 0, len(ruleFindings))
	for _, f := range ruleFindings {
		result = append(result, f)
	}

	return result
}

// DetectContent is a helper that checks raw content for secrets.
// Useful for testing without a full blob.
func (d *SecretDetector) DetectContent(content []byte) bool {
	d.mu.Lock()
	findings := d.detector.DetectBytes(content)
	d.mu.Unlock()
	return len(findings) > 0
}

// mergeStringSlices merges two string slices, removing duplicates.
func mergeStringSlices(a, b []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
