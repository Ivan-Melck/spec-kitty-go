package pyyaml

import (
	"fmt"
	"strings"
)

// Dialect selects which library's behaviour the parser reproduces.
type Dialect int

const (
	// PyYAML is PyYAML 6 (yaml.safe_load and friends).
	PyYAML Dialect = iota
	// Ruamel is ruamel.yaml 0.19 YAML() / YAML(typ="rt"): RoundTripScanner
	// (comment tokens) and RoundTripParser. Compared to PyYAML: YAML 1.2
	// scanning rules unless %YAML 1.1, ruamel's wording changes, a
	// "^ (line: N)" caret, duplicate anchors allowed (warning only) and
	// duplicate mapping keys refused.
	Ruamel
	// RuamelSafe is ruamel.yaml 0.19 YAML(typ="safe") (pure Scanner/Parser).
	RuamelSafe
)

func (d Dialect) ruamel() bool { return d != PyYAML }

// Mark is yaml.error.Mark / ruamel's StringMark (FileMark when Buffer is
// nil): a position in the input, rendered with a source snippet.
type Mark struct {
	Name    string
	Index   int
	Line    int
	Column  int
	Buffer  []rune // nil for stream input (no snippet)
	Pointer int
	dialect Dialect
}

func isBreakZ(r rune) bool {
	return r == 0 || r == '\r' || r == '\n' || r == 0x85 || r == 0x2028 || r == 0x2029
}

// snippet is Mark.get_snippet(indent=4, max_length=75).
func (m *Mark) snippet() (string, bool) {
	if m.Buffer == nil {
		return "", false
	}
	const indent, maxLength = 4, 75
	head := ""
	start := m.Pointer
	for start > 0 && !isBreakZ(m.Buffer[start-1]) {
		start--
		if float64(m.Pointer-start) > maxLength/2.0-1 {
			head = " ... "
			start += 5
			break
		}
	}
	tail := ""
	end := m.Pointer
	for end < len(m.Buffer) && !isBreakZ(m.Buffer[end]) {
		end++
		if float64(end-m.Pointer) > maxLength/2.0-1 {
			tail = " ... "
			end -= 5
			break
		}
	}
	caret := "^"
	if m.dialect.ruamel() {
		caret = fmt.Sprintf("^ (line: %d)", m.Line+1)
	}
	snip := ""
	if start < end {
		snip = string(m.Buffer[start:end])
	}
	return strings.Repeat(" ", indent) + head + snip + tail + "\n" +
		strings.Repeat(" ", indent+m.Pointer-start+len(head)) + caret, true
}

// String is Mark.__str__.
func (m *Mark) String() string {
	where := fmt.Sprintf("  in \"%s\", line %d, column %d", m.Name, m.Line+1, m.Column+1)
	if s, ok := m.snippet(); ok {
		where += ":\n" + s
	}
	return where
}

// MarkedYAMLError is yaml.MarkedYAMLError (ScannerError, ParserError,
// ComposerError, ConstructorError; ruamel's DuplicateKeyError).
type MarkedYAMLError struct {
	Class       string
	Context     string // "" = None
	ContextMark *Mark
	Problem     string
	ProblemMark *Mark
	Note        string
	dialect     Dialect
}

// Error is MarkedYAMLError.__str__.
func (e *MarkedYAMLError) Error() string {
	var lines []string
	if e.Context != "" {
		lines = append(lines, e.Context)
	}
	if e.ContextMark != nil && (e.Problem == "" || e.ProblemMark == nil ||
		e.ContextMark.Name != e.ProblemMark.Name || e.ContextMark.Line != e.ProblemMark.Line ||
		e.ContextMark.Column != e.ProblemMark.Column) {
		lines = append(lines, e.ContextMark.String())
	}
	if e.Problem != "" {
		lines = append(lines, e.Problem)
	}
	if e.ProblemMark != nil {
		lines = append(lines, e.ProblemMark.String())
	}
	if e.Note != "" {
		if e.dialect.ruamel() {
			lines = append(lines, dedent(e.Note))
		} else {
			lines = append(lines, e.Note)
		}
	}
	return strings.Join(lines, "\n")
}

// ReaderError is yaml.reader.ReaderError (a non-printable character or an
// undecodable byte).
type ReaderError struct {
	Name      string
	Position  int
	Character rune
	Byte      int // >= 0: undecodable byte (Character unused)
	Encoding  string
	Reason    string
}

func (e *ReaderError) Error() string {
	if e.Byte >= 0 {
		return fmt.Sprintf("'%s' codec can't decode byte #x%02x: %s\n  in \"%s\", position %d", e.Encoding, e.Byte, e.Reason, e.Name, e.Position)
	}
	return fmt.Sprintf("unacceptable character #x%04x: %s\n  in \"%s\", position %d", e.Character, e.Reason, e.Name, e.Position)
}

// ClassName returns the Python exception class name of a parse error
// (ScannerError, ParserError, ComposerError, ConstructorError, ReaderError,
// DuplicateKeyError), or "" for other errors.
func ClassName(err error) string {
	switch e := err.(type) {
	case *MarkedYAMLError:
		return e.Class
	case *ReaderError:
		return "ReaderError"
	case *PyError:
		return e.Type
	}
	return ""
}

// dedent is textwrap.dedent.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	margin := ""
	first := true
	for _, l := range lines {
		if strings.TrimLeft(l, " \t") == "" {
			continue
		}
		ws := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if first {
			margin, first = ws, false
			continue
		}
		for !strings.HasPrefix(ws, margin) {
			margin = margin[:len(margin)-1]
		}
	}
	for i, l := range lines {
		if strings.TrimLeft(l, " \t") == "" {
			lines[i] = ""
			continue
		}
		lines[i] = strings.TrimPrefix(l, margin)
	}
	return strings.Join(lines, "\n")
}
