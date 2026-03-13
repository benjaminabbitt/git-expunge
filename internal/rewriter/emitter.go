package rewriter

import (
	"fmt"
	"io"
	"strings"
)

// Emitter writes commands in git fast-import format.
type Emitter struct {
	writer io.Writer
}

// NewEmitter creates a new fast-import emitter.
func NewEmitter(w io.Writer) *Emitter {
	return &Emitter{writer: w}
}

// Emit writes a command in fast-import format.
func (e *Emitter) Emit(cmd Command) error {
	switch c := cmd.(type) {
	case *BlobCommand:
		return e.emitBlob(c)
	case *CommitCommand:
		return e.emitCommit(c)
	case *ResetCommand:
		return e.emitReset(c)
	case *TagCommand:
		return e.emitTag(c)
	case *ProgressCommand:
		return e.emitProgress(c)
	case *FeatureCommand:
		return e.emitFeature(c)
	case *DoneCommand:
		return e.emitDone()
	default:
		return fmt.Errorf("unknown command type: %T", cmd)
	}
}

func (e *Emitter) emitBlob(cmd *BlobCommand) error {
	if _, err := fmt.Fprintln(e.writer, "blob"); err != nil {
		return err
	}

	if cmd.Mark != "" {
		if _, err := fmt.Fprintf(e.writer, "mark %s\n", cmd.Mark); err != nil {
			return err
		}
	}

	if err := e.emitData(cmd.Data); err != nil {
		return err
	}

	return nil
}

func (e *Emitter) emitCommit(cmd *CommitCommand) error {
	if _, err := fmt.Fprintf(e.writer, "commit %s\n", cmd.Branch); err != nil {
		return err
	}

	if cmd.Mark != "" {
		if _, err := fmt.Fprintf(e.writer, "mark %s\n", cmd.Mark); err != nil {
			return err
		}
	}

	if cmd.Author != nil {
		if _, err := fmt.Fprintf(e.writer, "author %s <%s> %s\n",
			cmd.Author.Name, cmd.Author.Email, cmd.Author.When); err != nil {
			return err
		}
	}

	if cmd.Committer != nil {
		if _, err := fmt.Fprintf(e.writer, "committer %s <%s> %s\n",
			cmd.Committer.Name, cmd.Committer.Email, cmd.Committer.When); err != nil {
			return err
		}
	}

	if err := e.emitData(cmd.Message); err != nil {
		return err
	}

	if cmd.From != "" {
		if _, err := fmt.Fprintf(e.writer, "from %s\n", cmd.From); err != nil {
			return err
		}
	}

	for _, merge := range cmd.Merge {
		if _, err := fmt.Fprintf(e.writer, "merge %s\n", merge); err != nil {
			return err
		}
	}

	for _, op := range cmd.Operations {
		if err := e.emitFileOp(op); err != nil {
			return err
		}
	}

	// Empty line to end commit
	if _, err := fmt.Fprintln(e.writer); err != nil {
		return err
	}

	return nil
}

func (e *Emitter) emitFileOp(op FileOp) error {
	switch o := op.(type) {
	case *FileModify:
		path := quotePath(o.Path)
		if o.DataRef == "inline" {
			if _, err := fmt.Fprintf(e.writer, "M %s inline %s\n", o.Mode, path); err != nil {
				return err
			}
			return e.emitData(o.Data)
		}
		if _, err := fmt.Fprintf(e.writer, "M %s %s %s\n", o.Mode, o.DataRef, path); err != nil {
			return err
		}

	case *FileDelete:
		if _, err := fmt.Fprintf(e.writer, "D %s\n", quotePath(o.Path)); err != nil {
			return err
		}

	case *FileCopy:
		if _, err := fmt.Fprintf(e.writer, "C %s %s\n", quotePath(o.Source), quotePath(o.Dest)); err != nil {
			return err
		}

	case *FileRename:
		if _, err := fmt.Fprintf(e.writer, "R %s %s\n", quotePath(o.Source), quotePath(o.Dest)); err != nil {
			return err
		}

	case *FileDeleteAll:
		if _, err := fmt.Fprintln(e.writer, "deleteall"); err != nil {
			return err
		}
	}

	return nil
}

func (e *Emitter) emitReset(cmd *ResetCommand) error {
	if _, err := fmt.Fprintf(e.writer, "reset %s\n", cmd.Branch); err != nil {
		return err
	}

	if cmd.From != "" {
		if _, err := fmt.Fprintf(e.writer, "from %s\n", cmd.From); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(e.writer); err != nil {
		return err
	}

	return nil
}

func (e *Emitter) emitTag(cmd *TagCommand) error {
	if _, err := fmt.Fprintf(e.writer, "tag %s\n", cmd.Name); err != nil {
		return err
	}

	if cmd.From != "" {
		if _, err := fmt.Fprintf(e.writer, "from %s\n", cmd.From); err != nil {
			return err
		}
	}

	if cmd.Tagger != nil {
		if _, err := fmt.Fprintf(e.writer, "tagger %s <%s> %s\n",
			cmd.Tagger.Name, cmd.Tagger.Email, cmd.Tagger.When); err != nil {
			return err
		}
	}

	if err := e.emitData(cmd.Message); err != nil {
		return err
	}

	return nil
}

func (e *Emitter) emitProgress(cmd *ProgressCommand) error {
	_, err := fmt.Fprintf(e.writer, "progress %s\n", cmd.Message)
	return err
}

func (e *Emitter) emitFeature(cmd *FeatureCommand) error {
	_, err := fmt.Fprintf(e.writer, "feature %s\n", cmd.Feature)
	return err
}

func (e *Emitter) emitDone() error {
	_, err := fmt.Fprintln(e.writer, "done")
	return err
}

func (e *Emitter) emitData(data []byte) error {
	if _, err := fmt.Fprintf(e.writer, "data %d\n", len(data)); err != nil {
		return err
	}
	if _, err := e.writer.Write(data); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(e.writer); err != nil {
		return err
	}
	return nil
}

// quotePath quotes a path if it contains special characters.
func quotePath(s string) string {
	needsQuote := false
	for _, c := range s {
		if c == ' ' || c == '"' || c == '\\' || c == '\n' || c == '\t' {
			needsQuote = true
			break
		}
	}

	if !needsQuote {
		return s
	}

	// C-style quoting
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return `"` + s + `"`
}
