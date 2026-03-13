package rewriter

import (
	"io"
	"strings"
	"testing"
)

func TestParser_Blob(t *testing.T) {
	input := `blob
mark :1
data 13
Hello, World!
`

	parser := NewParser(strings.NewReader(input))
	cmd, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	blob, ok := cmd.(*BlobCommand)
	if !ok {
		t.Fatalf("expected BlobCommand, got %T", cmd)
	}

	if blob.Mark != ":1" {
		t.Errorf("expected Mark=:1, got %s", blob.Mark)
	}

	if string(blob.Data) != "Hello, World!" {
		t.Errorf("expected Data='Hello, World!', got %q", string(blob.Data))
	}
}

func TestParser_Commit(t *testing.T) {
	input := `commit refs/heads/main
mark :2
author Test Author <test@example.com> 1234567890 +0000
committer Test Committer <committer@example.com> 1234567890 +0000
data 14
Initial commit
M 100644 :1 README.md

`

	parser := NewParser(strings.NewReader(input))
	cmd, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	commit, ok := cmd.(*CommitCommand)
	if !ok {
		t.Fatalf("expected CommitCommand, got %T", cmd)
	}

	if commit.Branch != "refs/heads/main" {
		t.Errorf("expected Branch=refs/heads/main, got %s", commit.Branch)
	}

	if commit.Mark != ":2" {
		t.Errorf("expected Mark=:2, got %s", commit.Mark)
	}

	if commit.Author == nil || commit.Author.Name != "Test Author" {
		t.Errorf("expected Author.Name='Test Author', got %v", commit.Author)
	}

	if string(commit.Message) != "Initial commit" {
		t.Errorf("expected Message='Initial commit', got %q", string(commit.Message))
	}

	if len(commit.Operations) != 1 {
		t.Errorf("expected 1 operation, got %d", len(commit.Operations))
	}

	op, ok := commit.Operations[0].(*FileModify)
	if !ok {
		t.Fatalf("expected FileModify, got %T", commit.Operations[0])
	}

	if op.Mode != "100644" || op.DataRef != ":1" || op.Path != "README.md" {
		t.Errorf("unexpected FileModify: %+v", op)
	}
}

func TestParser_MergeCommit(t *testing.T) {
	input := `commit refs/heads/main
mark :5
author Test <test@example.com> 1234567890 +0000
committer Test <test@example.com> 1234567890 +0000
data 12
Merge branch
from :3
merge :4

`

	parser := NewParser(strings.NewReader(input))
	cmd, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	commit, ok := cmd.(*CommitCommand)
	if !ok {
		t.Fatalf("expected CommitCommand, got %T", cmd)
	}

	if commit.From != ":3" {
		t.Errorf("expected From=:3, got %s", commit.From)
	}

	if len(commit.Merge) != 1 || commit.Merge[0] != ":4" {
		t.Errorf("expected Merge=[':4'], got %v", commit.Merge)
	}
}

func TestParser_Reset(t *testing.T) {
	input := `reset refs/heads/feature
from :2

`

	parser := NewParser(strings.NewReader(input))
	cmd, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	reset, ok := cmd.(*ResetCommand)
	if !ok {
		t.Fatalf("expected ResetCommand, got %T", cmd)
	}

	if reset.Branch != "refs/heads/feature" {
		t.Errorf("expected Branch=refs/heads/feature, got %s", reset.Branch)
	}

	if reset.From != ":2" {
		t.Errorf("expected From=:2, got %s", reset.From)
	}
}

func TestParser_Tag(t *testing.T) {
	input := `tag v1.0.0
from :10
tagger Tagger <tagger@example.com> 1234567890 +0000
data 11
Version 1.0
`

	parser := NewParser(strings.NewReader(input))
	cmd, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	tag, ok := cmd.(*TagCommand)
	if !ok {
		t.Fatalf("expected TagCommand, got %T", cmd)
	}

	if tag.Name != "v1.0.0" {
		t.Errorf("expected Name=v1.0.0, got %s", tag.Name)
	}

	if tag.From != ":10" {
		t.Errorf("expected From=:10, got %s", tag.From)
	}

	if string(tag.Message) != "Version 1.0" {
		t.Errorf("expected Message='Version 1.0', got %q", string(tag.Message))
	}
}

func TestParser_MultipleCommands(t *testing.T) {
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

done
`

	parser := NewParser(strings.NewReader(input))

	// First blob
	cmd, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse blob 1 failed: %v", err)
	}
	if _, ok := cmd.(*BlobCommand); !ok {
		t.Errorf("expected BlobCommand, got %T", cmd)
	}

	// Second blob
	cmd, err = parser.Parse()
	if err != nil {
		t.Fatalf("Parse blob 2 failed: %v", err)
	}
	if _, ok := cmd.(*BlobCommand); !ok {
		t.Errorf("expected BlobCommand, got %T", cmd)
	}

	// Commit
	cmd, err = parser.Parse()
	if err != nil {
		t.Fatalf("Parse commit failed: %v", err)
	}
	commit, ok := cmd.(*CommitCommand)
	if !ok {
		t.Errorf("expected CommitCommand, got %T", cmd)
	}
	if len(commit.Operations) != 2 {
		t.Errorf("expected 2 operations, got %d", len(commit.Operations))
	}

	// Done
	cmd, err = parser.Parse()
	if err != nil {
		t.Fatalf("Parse done failed: %v", err)
	}
	if _, ok := cmd.(*DoneCommand); !ok {
		t.Errorf("expected DoneCommand, got %T", cmd)
	}

	// EOF
	_, err = parser.Parse()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}
