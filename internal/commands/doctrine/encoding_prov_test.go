package doctrine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var reBuiltInPath = regexp.MustCompile(`.*/(site-packages/packs|\.kittify/cache/packs/[0-9a-f]+)/built-in`)

// provenanceRecords lists the encoding-provenance records (file path, actor,
// encoding, confidence) appended since the first skip lines.
func provenanceRecords(t *testing.T, s *testkit.Sandbox, skip int) ([]string, int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.Dir, ".kittify", "encoding-provenance", "global.jsonl"))
	if err != nil {
		return nil, 0
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	var out []string
	for _, l := range lines[min(skip, len(lines)):] {
		var r struct {
			FilePath       string  `json:"file_path"`
			Actor          string  `json:"actor"`
			SourceEncoding string  `json:"source_encoding"`
			Confidence     float64 `json:"confidence"`
			Bypass         bool    `json:"bypass_used"`
			Normalized     bool    `json:"normalization_applied"`
		}
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("record %q: %v", l, err)
		}
		p := reBuiltInPath.ReplaceAllString(r.FilePath, "<BI>")
		p = strings.ReplaceAll(p, s.Dir, "<P>")
		out = append(out, strings.Join([]string{p, r.Actor, r.SourceEncoding}, " | "))
	}
	return out, len(lines)
}

// TestParityEncodingProvenanceDoctrineFlows covers the encoding-provenance
// records charter reads append (via load_charter_file) across the DOC
// commands: the interview / charter.md / built-in YAML loads, the extra
// infer_repo_languages passes (bundle scope filter, executor and profile
// service builds, pack-skill health, consistency-check's cwd-relative
// directive service).
func TestParityEncodingProvenanceDoctrineFlows(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	cmds := [][]string{
		{"charter", "interview", "--defaults"},
		{"charter", "generate"},
		{"charter", "context", "--action", "implement", "--mission-type", "software-dev"},
		{"charter", "context", "--action", "implement", "--mission-type", "software-dev", "--json"},
		{"charter", "status"},
		{"charter", "activate", "directive", "024-locality-of-change", "--resynthesize"},
		{"charter", "deactivate", "directive", "024-locality-of-change"},
		{"doctor", "charter-packs"},
		{"charter", "lint"},
		{"charter", "consistency-check"},
		{"dispatch", "plan-it", "--dry-run"},
	}
	gn, on := 0, 0
	for _, c := range cmds {
		g, o := p.Run(c...)
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d", c, g.Code, o.Code)
		}
		var gr, or []string
		gr, gn = provenanceRecords(t, p.Go, gn)
		or, on = provenanceRecords(t, p.Oracle, on)
		if strings.Join(gr, "\n") != strings.Join(or, "\n") {
			t.Errorf("%v: provenance records differ\n%s", c, testkit.Diff(strings.Join(or, "\n"), strings.Join(gr, "\n")))
		}
	}
}
