package charterenc_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

var builtInRe = regexp.MustCompile(`^.*/built-in/`)

// provenanceRecords reads and clears the centralized provenance log,
// returning one normalized line per record (volatile fields dropped,
// built-in pack paths reduced to their pack-relative part).
func provenanceRecords(t *testing.T, s *testkit.Sandbox) []string {
	t.Helper()
	path := filepath.Join(s.Dir, ".kittify", "encoding-provenance", "global.jsonl")
	b, err := os.ReadFile(path)
	os.Remove(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad provenance line %q: %v", line, err)
		}
		fp, _ := rec["file_path"].(string)
		fp = strings.ReplaceAll(fp, s.Dir, "<PROJ>")
		fp = builtInRe.ReplaceAllString(fp, "<BUILTIN>/")
		delete(rec, "event_id")
		delete(rec, "at")
		rec["file_path"] = fp
		j, _ := json.Marshal(rec)
		out = append(out, string(j))
	}
	return out
}

// TestDiffCharterReadProvenance: charter commands decode charter files
// through the encoding chokepoint and append one provenance record per
// load, in upstream's order (built-in pack paths are compared by their
// pack-relative part: the Go binary serves an embedded pack, not
// site-packages).
func TestDiffCharterReadProvenance(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		if r := s.RunOracle("charter", "interview", "--defaults", "--json"); r.Code != 0 {
			t.Fatalf("interview: %s", r.Stderr)
		}
		provenanceRecords(t, s)
	})
	for _, args := range [][]string{
		{"charter", "generate", "--json"},
		{"charter", "context", "--action", "implement", "--json"},
		{"charter", "context", "--action", "implement", "--json"},
		{"charter", "context", "--action", "specify"},
		{"charter", "context", "--action", "review", "--json"},
		{"charter", "status", "--json"},
		{"charter", "resynthesize", "--json"},
		{"charter", "synthesize", "--json"},
		{"charter", "lint", "--json"},
	} {
		g := p.Go.Run(args...)
		o := p.Oracle.RunOracle(args...)
		gr, or := provenanceRecords(t, p.Go), provenanceRecords(t, p.Oracle)
		if strings.Join(gr, "\n") != strings.Join(or, "\n") {
			t.Errorf("%v (exit go=%d oracle=%d): provenance records differ\n oracle: %s\n go:     %s\n%s", args, g.Code, o.Code,
				paths(or), paths(gr), testkit.Diff(strings.Join(or, "\n"), strings.Join(gr, "\n")))
		}
	}
}

// TestDiffMissionPromptProvenance: runtime prompts that render charter
// context (implement / review / next) load charter files the same way.
func TestDiffMissionPromptProvenance(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		s.RunOracle("charter", "interview", "--defaults", "--json")
		s.RunOracle("charter", "generate", "--json")
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "charter")
		provenanceRecords(t, s)
	})
	for _, args := range [][]string{
		{"next", "--mission", "$M", "--json"},
		{"next", "--mission", "$M"},
		{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"},
		{"agent", "context", "resolve", "--action", "implement", "--mission", "$M", "--json"},
		{"charter", "context", "--action", "plan", "--json"},
		{"charter", "context", "--action", "tasks"},
	} {
		g := p.Go.Run(subst(args, missionfx.MissionSlug(t, p.Go))...)
		o := p.Oracle.RunOracle(subst(args, missionfx.MissionSlug(t, p.Oracle))...)
		gr, or := provenanceRecords(t, p.Go), provenanceRecords(t, p.Oracle)
		if strings.Join(gr, "\n") != strings.Join(or, "\n") {
			t.Errorf("%v (exit go=%d oracle=%d): provenance records differ\n oracle: %s\n go:     %s", args, g.Code, o.Code, paths(or), paths(gr))
		}
	}
}

// TestDiffCharterNonUTF8Answers: a cp1252 interview transcript is decoded
// (and recorded) through the chokepoint; an ambiguous one is refused.
func TestDiffCharterNonUTF8Answers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, tail string }{
		{"cp1252", "# The team\x92s \x93principles\x94 are simple \x97 ship small, review everything.\n"},
		{"ambiguous", "# plain ascii text with one stray byte \xe9 here\n"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := missionfx.NewOraclePair(t)
			p.Both(func(s *testkit.Sandbox) {
				s.RunOracle("charter", "interview", "--defaults", "--json")
				path := filepath.Join(s.Dir, ".kittify", "charter", "interview", "answers.yaml")
				b, _ := os.ReadFile(path)
				os.WriteFile(path, append(b, []byte(tc.tail)...), 0o644)
				provenanceRecords(t, s)
			})
			for _, args := range [][]string{
				{"charter", "context", "--action", "implement", "--json"},
				{"charter", "generate", "--json"},
				{"charter", "context", "--action", "tasks"},
			} {
				g := p.Go.Run(args...)
				o := p.Oracle.RunOracle(args...)
				gr, or := provenanceRecords(t, p.Go), provenanceRecords(t, p.Oracle)
				if g.Code != o.Code {
					t.Errorf("%v: exit go=%d oracle=%d\n--- go\n%s%s\n--- oracle\n%s%s", args, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, lastLines(o.Stderr, 4))
				}
				if gs, os_ := p.Go.Normalize(g.Stdout), p.Oracle.Normalize(o.Stdout); gs != os_ {
					t.Errorf("%v: stdout differs\n%s", args, testkit.Diff(os_, gs))
				}
				if strings.Join(gr, "\n") != strings.Join(or, "\n") {
					t.Errorf("%v: provenance records differ\n%s", args, testkit.Diff(strings.Join(or, "\n"), strings.Join(gr, "\n")))
				}
			}
		})
	}
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

func subst(args []string, slug string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "$M", slug)
	}
	return out
}

var fileRe =regexp.MustCompile(`"file_path":"([^"]*)"`)

func paths(recs []string) string {
	var out []string
	for _, r := range recs {
		if m := fileRe.FindStringSubmatch(r); m != nil {
			out = append(out, filepath.Base(m[1]))
		}
	}
	return strings.Join(out, " ")
}
