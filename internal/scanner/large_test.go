package scanner

import (
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/gitquery"
)

func TestLargeFileDetector_FlagsAnyMimeByThreshold(t *testing.T) {
	const threshold int64 = 1024

	tests := []struct {
		name    string
		size    int64
		content []byte
		want    bool
	}{
		{
			name:    "large text crosses threshold",
			size:    2048,
			content: makeTextContent(2048),
			want:    true,
		},
		{
			name:    "large binary crosses threshold",
			size:    2048,
			content: makeELFBinary(2048),
			want:    true,
		},
		{
			name:    "small text below threshold",
			size:    256,
			content: makeTextContent(256),
			want:    false,
		},
		{
			name:    "small binary below threshold",
			size:    256,
			content: makeELFBinary(256),
			want:    false,
		},
		{
			name:    "exactly at threshold is not flagged",
			size:    threshold,
			content: makeTextContent(int(threshold)),
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewLargeFileDetector(threshold)
			blob := &gitquery.BlobInfo{
				Hash:       "h",
				Path:       "f",
				Size:       tt.size,
				CommitHash: "c",
				Content:    func() ([]byte, error) { return tt.content, nil },
			}
			got := d.Detect(blob)
			if tt.want && got == nil {
				t.Errorf("expected finding, got nil")
			}
			if !tt.want && got != nil {
				t.Errorf("expected nil, got %+v", got)
			}
			if got != nil && got.Type != domain.FindingTypeLargeFile {
				t.Errorf("expected FindingTypeLargeFile, got %q", got.Type)
			}
		})
	}
}
