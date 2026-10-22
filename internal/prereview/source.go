package prereview

import (
	"os"
	"path/filepath"
	"strings"
)

// Raw is RawRunResult (the unparsed product of one command run).
type Raw struct {
	ReturnCode     int
	Stdout, Stderr string
	ArtifactPath   string // "" = none
}

// DeclaredSource is DeclaredCommandScopeSource: the repo's own
// review.test_command, run as a whole (no per-file narrowing), parsed as
// JUnit (when an artifact exists) or by the `FAIL <test>[: msg]` text
// convention.
type DeclaredSource struct {
	Root       string
	outputFile string
}

// NewDeclaredSource is resolve_scope_source(root).
func NewDeclaredSource(root string) *DeclaredSource { return &DeclaredSource{Root: root} }

// OutputFile is the instance's lazily created {output_file} target
// (tempfile.mkdtemp(prefix="spec-kitty-declared-cmd-")/output.xml).
func (s *DeclaredSource) OutputFile() string {
	if s.outputFile == "" {
		dir, err := os.MkdirTemp("", "spec-kitty-declared-cmd-")
		if err != nil {
			dir = filepath.Join(os.TempDir(), "spec-kitty-declared-cmd-x")
		}
		s.outputFile = filepath.Join(dir, "output.xml")
	}
	return s.outputFile
}

// TestCommand is the runnable argv (["sh", "-c", ...]); ok=false when
// review.test_command is unset/empty or its quoting is malformed.
func (s *DeclaredSource) TestCommand() ([]string, bool) {
	tmpl, _ := TestCommandTemplate(s.Root)
	if tmpl == "" {
		return nil, false
	}
	if !ShlexValid(tmpl) {
		return nil, false
	}
	subs := map[string]string{}
	if strings.Contains(tmpl, "{output_file}") {
		subs["output_file"] = s.OutputFile()
	}
	return BuildShellCommand(tmpl, subs), true
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (s *DeclaredSource) resolvedArtifact(raw *Raw) string {
	if raw.ArtifactPath != "" && exists(raw.ArtifactPath) {
		return raw.ArtifactPath
	}
	if s.outputFile != "" && exists(s.outputFile) {
		return s.outputFile
	}
	return ""
}

// ParseMode is "junit_xml" or "text".
func (s *DeclaredSource) ParseMode(raw *Raw) string {
	if s.resolvedArtifact(raw) != "" {
		return "junit_xml"
	}
	return "text"
}

// Identity is scope_source_identity.
func (s *DeclaredSource) Identity(raw *Raw) string {
	return "DeclaredCommandScopeSource/" + s.ParseMode(raw)
}

const failurePrefix = "FAIL "

func parseFailLines(text string) []Failure {
	var out []Failure
	for _, line := range splitLines(text) {
		if !strings.HasPrefix(line, failurePrefix) {
			continue
		}
		rest := line[len(failurePrefix):]
		name, msg, _ := strings.Cut(rest, ":")
		e := strings.TrimSpace(msg)
		if e == "" {
			e = "failed"
		}
		out = append(out, Failure{Test: strings.TrimSpace(name), Error: truncRunes(e, 200), File: "unknown"})
	}
	return out
}

// splitLines is str.splitlines() for the common separators.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func truncRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// ParseResults is parse_results (JUnit, else FAIL lines, else a whole-run
// failure for a non-zero exit).
func (s *DeclaredSource) ParseResults(raw *Raw) ([]Failure, error) {
	if a := s.resolvedArtifact(raw); a != "" {
		_, _, _, _, f, err := ParseJUnit(a)
		return f, err
	}
	f := append(parseFailLines(raw.Stdout), parseFailLines(raw.Stderr)...)
	if len(f) > 0 {
		return f, nil
	}
	if raw.ReturnCode != 0 {
		src := raw.Stderr
		if src == "" {
			src = raw.Stdout
		}
		lines := splitLines(strings.TrimSpace(src))
		summary := "exit code " + itoa(raw.ReturnCode)
		if len(lines) > 0 {
			summary = truncRunes(lines[len(lines)-1], 200)
		}
		return []Failure{{Test: "<declared-command>", Error: summary, File: "unknown"}}, nil
	}
	return nil, nil
}
