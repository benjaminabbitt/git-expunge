package rewriter

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Parser parses git fast-export output.
type Parser struct {
	reader *bufio.Reader
	line   []byte
	lineNo int
}

// NewParser creates a new fast-export parser.
func NewParser(r io.Reader) *Parser {
	return &Parser{
		reader: bufio.NewReaderSize(r, 1024*1024), // 1MB buffer for large blobs
	}
}

// Parse reads and returns the next command, or io.EOF when done.
func (p *Parser) Parse() (Command, error) {
	// If we have a buffered line from previous parsing, use it
	if len(p.line) == 0 {
		if err := p.readLine(); err != nil {
			return nil, err
		}
	}

	// Skip empty lines
	for len(p.line) == 0 {
		if err := p.readLine(); err != nil {
			return nil, err
		}
	}

	line := string(p.line)
	// Clear the line so we read fresh next time
	p.line = nil

	switch {
	case strings.HasPrefix(line, "blob"):
		return p.parseBlob()
	case strings.HasPrefix(line, "commit "):
		return p.parseCommit(line)
	case strings.HasPrefix(line, "reset "):
		return p.parseReset(line)
	case strings.HasPrefix(line, "tag "):
		return p.parseTag(line)
	case strings.HasPrefix(line, "progress "):
		return p.parseProgress(line)
	case strings.HasPrefix(line, "feature "):
		return p.parseFeature(line)
	case line == "done":
		return &DoneCommand{}, nil
	default:
		return nil, fmt.Errorf("line %d: unknown command: %s", p.lineNo, line)
	}
}

func (p *Parser) readLine() error {
	line, err := p.reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if err == io.EOF && len(line) == 0 {
		return io.EOF
	}
	p.line = bytes.TrimSuffix(line, []byte("\n"))
	p.lineNo++
	return nil
}

func (p *Parser) parseBlob() (*BlobCommand, error) {
	cmd := &BlobCommand{}

	// Read mark line (optional)
	if err := p.readLine(); err != nil {
		return nil, err
	}

	if strings.HasPrefix(string(p.line), "mark ") {
		cmd.Mark = strings.TrimPrefix(string(p.line), "mark ")
		if err := p.readLine(); err != nil {
			return nil, err
		}
	}

	// Read original-oid (optional)
	if strings.HasPrefix(string(p.line), "original-oid ") {
		cmd.OrigRef = strings.TrimPrefix(string(p.line), "original-oid ")
		if err := p.readLine(); err != nil {
			return nil, err
		}
	}

	// Read data
	data, err := p.parseData()
	if err != nil {
		return nil, err
	}
	cmd.Data = data

	return cmd, nil
}

func (p *Parser) parseData() ([]byte, error) {
	line := string(p.line)
	p.line = nil // Clear so we read fresh next time

	if !strings.HasPrefix(line, "data ") {
		return nil, fmt.Errorf("line %d: expected 'data', got %q", p.lineNo, line)
	}

	sizeStr := strings.TrimPrefix(line, "data ")

	// Handle delimited data format (data <<EOF)
	if strings.HasPrefix(sizeStr, "<<") {
		delimiter := strings.TrimPrefix(sizeStr, "<<")
		return p.readDelimitedData(delimiter)
	}

	// Handle exact size format (data <size>)
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("line %d: invalid data size: %w", p.lineNo, err)
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(p.reader, data); err != nil {
		return nil, fmt.Errorf("line %d: failed to read data: %w", p.lineNo, err)
	}

	// Read trailing newline
	if b, err := p.reader.ReadByte(); err != nil {
		return nil, err
	} else if b != '\n' {
		p.reader.UnreadByte()
	}

	return data, nil
}

func (p *Parser) readDelimitedData(delimiter string) ([]byte, error) {
	var buf bytes.Buffer
	for {
		if err := p.readLine(); err != nil {
			return nil, err
		}
		if string(p.line) == delimiter {
			break
		}
		buf.Write(p.line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func (p *Parser) parseCommit(firstLine string) (*CommitCommand, error) {
	cmd := &CommitCommand{
		Branch: strings.TrimPrefix(firstLine, "commit "),
	}

	for {
		if err := p.readLine(); err != nil {
			if err == io.EOF {
				return cmd, nil
			}
			return nil, err
		}

		line := string(p.line)
		switch {
		case strings.HasPrefix(line, "mark "):
			cmd.Mark = strings.TrimPrefix(line, "mark ")

		case strings.HasPrefix(line, "original-oid "):
			// Skip original-oid

		case strings.HasPrefix(line, "author "):
			sig, err := p.parseSignature(strings.TrimPrefix(line, "author "))
			if err != nil {
				return nil, err
			}
			cmd.Author = sig

		case strings.HasPrefix(line, "committer "):
			sig, err := p.parseSignature(strings.TrimPrefix(line, "committer "))
			if err != nil {
				return nil, err
			}
			cmd.Committer = sig

		case strings.HasPrefix(line, "data "):
			data, err := p.parseData()
			if err != nil {
				return nil, err
			}
			cmd.Message = data

		case strings.HasPrefix(line, "from "):
			cmd.From = strings.TrimPrefix(line, "from ")

		case strings.HasPrefix(line, "merge "):
			cmd.Merge = append(cmd.Merge, strings.TrimPrefix(line, "merge "))

		case strings.HasPrefix(line, "M "):
			op, err := p.parseFileModify(line)
			if err != nil {
				return nil, err
			}
			cmd.Operations = append(cmd.Operations, op)

		case strings.HasPrefix(line, "D "):
			path := strings.TrimPrefix(line, "D ")
			cmd.Operations = append(cmd.Operations, &FileDelete{Path: unquotePath(path)})

		case strings.HasPrefix(line, "C "):
			parts := strings.SplitN(strings.TrimPrefix(line, "C "), " ", 2)
			if len(parts) == 2 {
				cmd.Operations = append(cmd.Operations, &FileCopy{
					Source: unquotePath(parts[0]),
					Dest:   unquotePath(parts[1]),
				})
			}

		case strings.HasPrefix(line, "R "):
			parts := strings.SplitN(strings.TrimPrefix(line, "R "), " ", 2)
			if len(parts) == 2 {
				cmd.Operations = append(cmd.Operations, &FileRename{
					Source: unquotePath(parts[0]),
					Dest:   unquotePath(parts[1]),
				})
			}

		case line == "deleteall":
			cmd.Operations = append(cmd.Operations, &FileDeleteAll{})

		case len(line) == 0:
			// Empty line signals end of commit
			p.line = nil // Clear so next Parse reads fresh
			return cmd, nil

		default:
			// Unknown line - might be start of next command
			// Keep p.line set so next Parse() call uses it
			return cmd, nil
		}
	}
}

func (p *Parser) parseFileModify(line string) (*FileModify, error) {
	// Format: M <mode> <dataref> <path>
	// Or: M <mode> inline <path> followed by data
	parts := strings.SplitN(strings.TrimPrefix(line, "M "), " ", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("line %d: invalid M command: %s", p.lineNo, line)
	}

	op := &FileModify{
		Mode:    parts[0],
		DataRef: parts[1],
		Path:    unquotePath(parts[2]),
	}

	if op.DataRef == "inline" {
		if err := p.readLine(); err != nil {
			return nil, err
		}
		data, err := p.parseData()
		if err != nil {
			return nil, err
		}
		op.Data = data
	}

	return op, nil
}

func (p *Parser) parseSignature(line string) (*Signature, error) {
	// Format: Name <email> timestamp timezone
	// Example: Author Name <email@example.com> 1234567890 +0000
	ltIdx := strings.LastIndex(line, " <")
	gtIdx := strings.LastIndex(line, "> ")

	if ltIdx < 0 || gtIdx < 0 || gtIdx < ltIdx {
		return nil, fmt.Errorf("invalid signature format: %s", line)
	}

	return &Signature{
		Name:  line[:ltIdx],
		Email: line[ltIdx+2 : gtIdx],
		When:  line[gtIdx+2:],
	}, nil
}

func (p *Parser) parseReset(firstLine string) (*ResetCommand, error) {
	cmd := &ResetCommand{
		Branch: strings.TrimPrefix(firstLine, "reset "),
	}

	if err := p.readLine(); err != nil {
		if err == io.EOF {
			return cmd, nil
		}
		return nil, err
	}

	if strings.HasPrefix(string(p.line), "from ") {
		cmd.From = strings.TrimPrefix(string(p.line), "from ")
		p.line = nil // Clear so we read fresh next time
	}
	// If it's not "from", keep p.line for next Parse() call

	return cmd, nil
}

func (p *Parser) parseTag(firstLine string) (*TagCommand, error) {
	cmd := &TagCommand{
		Name: strings.TrimPrefix(firstLine, "tag "),
	}

	for {
		if err := p.readLine(); err != nil {
			if err == io.EOF {
				return cmd, nil
			}
			return nil, err
		}

		line := string(p.line)
		switch {
		case strings.HasPrefix(line, "from "):
			cmd.From = strings.TrimPrefix(line, "from ")

		case strings.HasPrefix(line, "tagger "):
			sig, err := p.parseSignature(strings.TrimPrefix(line, "tagger "))
			if err != nil {
				return nil, err
			}
			cmd.Tagger = sig

		case strings.HasPrefix(line, "data "):
			data, err := p.parseData()
			if err != nil {
				return nil, err
			}
			cmd.Message = data
			return cmd, nil

		case len(line) == 0:
			return cmd, nil
		}
	}
}

func (p *Parser) parseProgress(line string) (*ProgressCommand, error) {
	return &ProgressCommand{
		Message: strings.TrimPrefix(line, "progress "),
	}, nil
}

func (p *Parser) parseFeature(line string) (*FeatureCommand, error) {
	return &FeatureCommand{
		Feature: strings.TrimPrefix(line, "feature "),
	}, nil
}

// unquotePath handles quoted paths in fast-export format.
func unquotePath(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		// Handle C-style escaping
		unquoted := s[1 : len(s)-1]
		unquoted = strings.ReplaceAll(unquoted, `\\`, "\x00")
		unquoted = strings.ReplaceAll(unquoted, `\"`, `"`)
		unquoted = strings.ReplaceAll(unquoted, `\n`, "\n")
		unquoted = strings.ReplaceAll(unquoted, `\t`, "\t")
		unquoted = strings.ReplaceAll(unquoted, "\x00", `\`)
		return unquoted
	}
	return s
}
