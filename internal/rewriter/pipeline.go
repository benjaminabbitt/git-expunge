package rewriter

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
)

// Filter defines a function that decides whether to include a blob.
// Returns true if the blob should be included, false if it should be excluded.
type Filter func(blobHash string, data []byte) bool

// Pipeline processes a fast-export stream, filtering out specified blobs.
type Pipeline struct {
	excludeHashes map[string]bool
	markToHash    map[string]string // maps marks to blob hashes
	excludedMarks map[string]bool   // marks that were excluded
}

// NewPipeline creates a new filtering pipeline.
func NewPipeline(excludeHashes []string) *Pipeline {
	hashMap := make(map[string]bool)
	for _, h := range excludeHashes {
		hashMap[h] = true
	}

	return &Pipeline{
		excludeHashes: hashMap,
		markToHash:    make(map[string]string),
		excludedMarks: make(map[string]bool),
	}
}

// Process reads from input, filters blobs, and writes to output.
func (p *Pipeline) Process(input io.Reader, output io.Writer) error {
	parser := NewParser(input)
	emitter := NewEmitter(output)

	for {
		cmd, err := parser.Parse()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("parse error: %w", err)
		}

		filtered, err := p.filterCommand(cmd)
		if err != nil {
			return fmt.Errorf("filter error: %w", err)
		}

		if filtered != nil {
			if err := emitter.Emit(filtered); err != nil {
				return fmt.Errorf("emit error: %w", err)
			}
		}
	}

	return nil
}

func (p *Pipeline) filterCommand(cmd Command) (Command, error) {
	switch c := cmd.(type) {
	case *BlobCommand:
		return p.filterBlob(c)
	case *CommitCommand:
		return p.filterCommit(c), nil
	default:
		// Pass through other commands unchanged
		return cmd, nil
	}
}

func (p *Pipeline) filterBlob(cmd *BlobCommand) (Command, error) {
	// Calculate blob hash
	hash := calculateBlobHash(cmd.Data)

	// Store mapping from mark to hash
	if cmd.Mark != "" {
		p.markToHash[cmd.Mark] = hash
	}

	// Check if this blob should be excluded
	if p.excludeHashes[hash] || p.excludeHashes[cmd.OrigRef] {
		if cmd.Mark != "" {
			p.excludedMarks[cmd.Mark] = true
		}
		return nil, nil // Exclude this blob
	}

	return cmd, nil
}

func (p *Pipeline) filterCommit(cmd *CommitCommand) *CommitCommand {
	// Filter out file operations that reference excluded blobs
	var filteredOps []FileOp

	for _, op := range cmd.Operations {
		switch o := op.(type) {
		case *FileModify:
			// Check if this references an excluded blob
			if p.excludedMarks[o.DataRef] {
				// Skip this file operation
				continue
			}
			// Check if inline data should be excluded
			if o.DataRef == "inline" {
				hash := calculateBlobHash(o.Data)
				if p.excludeHashes[hash] {
					continue
				}
			}
			filteredOps = append(filteredOps, op)
		default:
			// Keep other operations (delete, copy, rename, deleteall)
			filteredOps = append(filteredOps, op)
		}
	}

	// Return modified commit with filtered operations
	return &CommitCommand{
		Mark:       cmd.Mark,
		Branch:     cmd.Branch,
		Author:     cmd.Author,
		Committer:  cmd.Committer,
		Message:    cmd.Message,
		From:       cmd.From,
		Merge:      cmd.Merge,
		Operations: filteredOps,
	}
}

// calculateBlobHash computes the git blob hash for content.
func calculateBlobHash(data []byte) string {
	// Git blob hash: sha1("blob <size>\0<content>")
	header := fmt.Sprintf("blob %d\x00", len(data))
	h := sha1.New()
	h.Write([]byte(header))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// Stats holds statistics about the filtering operation.
type Stats struct {
	TotalBlobs     int
	ExcludedBlobs  int
	TotalCommits   int
	ModifiedCommits int
}

// PipelineWithStats wraps Pipeline to collect statistics.
type PipelineWithStats struct {
	*Pipeline
	Stats Stats
}

// NewPipelineWithStats creates a pipeline that tracks statistics.
func NewPipelineWithStats(excludeHashes []string) *PipelineWithStats {
	return &PipelineWithStats{
		Pipeline: NewPipeline(excludeHashes),
	}
}

// Process reads from input, filters blobs, and writes to output with stats.
func (p *PipelineWithStats) Process(input io.Reader, output io.Writer) error {
	parser := NewParser(input)
	emitter := NewEmitter(output)

	for {
		cmd, err := parser.Parse()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("parse error: %w", err)
		}

		filtered, err := p.filterCommandWithStats(cmd)
		if err != nil {
			return fmt.Errorf("filter error: %w", err)
		}

		if filtered != nil {
			if err := emitter.Emit(filtered); err != nil {
				return fmt.Errorf("emit error: %w", err)
			}
		}
	}

	return nil
}

func (p *PipelineWithStats) filterCommandWithStats(cmd Command) (Command, error) {
	switch c := cmd.(type) {
	case *BlobCommand:
		p.Stats.TotalBlobs++
		result, err := p.filterBlob(c)
		if result == nil && err == nil {
			p.Stats.ExcludedBlobs++
		}
		return result, err
	case *CommitCommand:
		p.Stats.TotalCommits++
		originalOps := len(c.Operations)
		filtered := p.filterCommit(c)
		if len(filtered.Operations) != originalOps {
			p.Stats.ModifiedCommits++
		}
		return filtered, nil
	default:
		return cmd, nil
	}
}
