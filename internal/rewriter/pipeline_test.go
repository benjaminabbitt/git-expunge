package rewriter

import (
	"bytes"
	"strings"
	"testing"
)

func TestPipeline_FilterBlob(t *testing.T) {
	// Create input with two blobs
	input := `blob
mark :1
data 5
hello
blob
mark :2
data 5
world
commit refs/heads/main
mark :3
author Test <test@example.com> 1234567890 +0000
committer Test <test@example.com> 1234567890 +0000
data 4
test
M 100644 :1 file1.txt
M 100644 :2 file2.txt

`

	// Calculate hash of "hello" blob - we want to exclude this
	helloHash := calculateBlobHash([]byte("hello"))

	pipeline := NewPipelineWithStats([]string{helloHash})

	var output bytes.Buffer
	if err := pipeline.Process(strings.NewReader(input), &output); err != nil {
		t.Fatalf("Pipeline failed: %v", err)
	}

	// Check stats
	if pipeline.Stats.TotalBlobs != 2 {
		t.Errorf("expected TotalBlobs=2, got %d", pipeline.Stats.TotalBlobs)
	}
	if pipeline.Stats.ExcludedBlobs != 1 {
		t.Errorf("expected ExcludedBlobs=1, got %d", pipeline.Stats.ExcludedBlobs)
	}
	if pipeline.Stats.ModifiedCommits != 1 {
		t.Errorf("expected ModifiedCommits=1, got %d", pipeline.Stats.ModifiedCommits)
	}

	// Check output doesn't contain the excluded blob
	result := output.String()
	if strings.Contains(result, "hello") {
		t.Error("output should not contain excluded blob content 'hello'")
	}
	if !strings.Contains(result, "world") {
		t.Error("output should contain non-excluded blob content 'world'")
	}

	// Check that file1.txt operation was removed
	if strings.Contains(result, "file1.txt") {
		t.Error("output should not reference file1.txt (excluded blob)")
	}
	if !strings.Contains(result, "file2.txt") {
		t.Error("output should reference file2.txt (non-excluded blob)")
	}
}

func TestPipeline_NoExclusions(t *testing.T) {
	input := `blob
mark :1
data 5
hello
commit refs/heads/main
mark :2
author Test <test@example.com> 1234567890 +0000
committer Test <test@example.com> 1234567890 +0000
data 4
test
M 100644 :1 file.txt

`

	pipeline := NewPipelineWithStats([]string{})

	var output bytes.Buffer
	if err := pipeline.Process(strings.NewReader(input), &output); err != nil {
		t.Fatalf("Pipeline failed: %v", err)
	}

	if pipeline.Stats.ExcludedBlobs != 0 {
		t.Errorf("expected ExcludedBlobs=0, got %d", pipeline.Stats.ExcludedBlobs)
	}

	// Output should contain everything
	result := output.String()
	if !strings.Contains(result, "hello") {
		t.Error("output should contain blob content")
	}
	if !strings.Contains(result, "file.txt") {
		t.Error("output should contain file reference")
	}
}

func TestPipeline_PreservesNonBlobCommands(t *testing.T) {
	input := `reset refs/heads/main
from :1

tag v1.0.0
from :1
tagger Test <test@example.com> 1234567890 +0000
data 7
Release
progress Processing
done
`

	pipeline := NewPipelineWithStats([]string{"somehash"})

	var output bytes.Buffer
	if err := pipeline.Process(strings.NewReader(input), &output); err != nil {
		t.Fatalf("Pipeline failed: %v", err)
	}

	result := output.String()

	// All these should be preserved
	if !strings.Contains(result, "reset refs/heads/main") {
		t.Error("reset command should be preserved")
	}
	if !strings.Contains(result, "tag v1.0.0") {
		t.Error("tag command should be preserved")
	}
	if !strings.Contains(result, "progress Processing") {
		t.Error("progress command should be preserved")
	}
	if !strings.Contains(result, "done") {
		t.Error("done command should be preserved")
	}
}

func TestCalculateBlobHash(t *testing.T) {
	// Test with known values
	// git hash-object -t blob --stdin <<< "hello" (without trailing newline)
	// would give a known hash

	// Test consistency
	hash1 := calculateBlobHash([]byte("test content"))
	hash2 := calculateBlobHash([]byte("test content"))

	if hash1 != hash2 {
		t.Error("same content should produce same hash")
	}

	// Different content should produce different hash
	hash3 := calculateBlobHash([]byte("different content"))
	if hash1 == hash3 {
		t.Error("different content should produce different hash")
	}

	// Hash should be 40 hex characters (SHA-1)
	if len(hash1) != 40 {
		t.Errorf("expected 40 char hash, got %d chars", len(hash1))
	}
}

func TestPipeline_InlineData(t *testing.T) {
	// Test with inline data in commit
	input := `commit refs/heads/main
mark :1
author Test <test@example.com> 1234567890 +0000
committer Test <test@example.com> 1234567890 +0000
data 4
test
M 100644 inline file.txt
data 6
secret

`

	// Calculate hash of the inline content
	secretHash := calculateBlobHash([]byte("secret"))

	pipeline := NewPipelineWithStats([]string{secretHash})

	var output bytes.Buffer
	if err := pipeline.Process(strings.NewReader(input), &output); err != nil {
		t.Fatalf("Pipeline failed: %v", err)
	}

	result := output.String()

	// The file operation should be filtered out
	if strings.Contains(result, "file.txt") {
		t.Error("inline data with excluded hash should be filtered")
	}
}
