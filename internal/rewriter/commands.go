// Package rewriter provides git history rewriting via fast-export/fast-import.
package rewriter

// Command represents a git fast-export command.
type Command interface {
	Type() string
}

// BlobCommand represents a blob (file content).
type BlobCommand struct {
	Mark    string // e.g., ":1"
	OrigRef string // original blob hash if known
	Data    []byte
}

func (c *BlobCommand) Type() string { return "blob" }

// CommitCommand represents a commit.
type CommitCommand struct {
	Mark       string
	Branch     string
	Author     *Signature
	Committer  *Signature
	Message    []byte
	From       string   // parent commit mark or hash
	Merge      []string // additional parent marks for merge commits
	Operations []FileOp
}

func (c *CommitCommand) Type() string { return "commit" }

// Signature represents author/committer information.
type Signature struct {
	Name  string
	Email string
	When  string // timestamp in git format
}

// FileOp represents a file operation in a commit.
type FileOp interface {
	OpType() string
}

// FileModify represents a file modification (M command).
type FileModify struct {
	Mode    string // e.g., "100644", "100755", "120000"
	DataRef string // mark reference like ":1" or "inline"
	Path    string
	Data    []byte // only set if DataRef is "inline"
}

func (o *FileModify) OpType() string { return "M" }

// FileDelete represents a file deletion (D command).
type FileDelete struct {
	Path string
}

func (o *FileDelete) OpType() string { return "D" }

// FileCopy represents a file copy (C command).
type FileCopy struct {
	Source string
	Dest   string
}

func (o *FileCopy) OpType() string { return "C" }

// FileRename represents a file rename (R command).
type FileRename struct {
	Source string
	Dest   string
}

func (o *FileRename) OpType() string { return "R" }

// FileDeleteAll represents deleteall command.
type FileDeleteAll struct{}

func (o *FileDeleteAll) OpType() string { return "deleteall" }

// ResetCommand represents a branch reset.
type ResetCommand struct {
	Branch string
	From   string // commit reference
}

func (c *ResetCommand) Type() string { return "reset" }

// TagCommand represents an annotated tag.
type TagCommand struct {
	Name    string
	From    string // commit reference
	Tagger  *Signature
	Message []byte
}

func (c *TagCommand) Type() string { return "tag" }

// ProgressCommand represents a progress message.
type ProgressCommand struct {
	Message string
}

func (c *ProgressCommand) Type() string { return "progress" }

// FeatureCommand represents a feature declaration.
type FeatureCommand struct {
	Feature string
}

func (c *FeatureCommand) Type() string { return "feature" }

// DoneCommand signals the end of the stream.
type DoneCommand struct{}

func (c *DoneCommand) Type() string { return "done" }
