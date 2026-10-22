package closeout_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// Legacy-schema retrospective proposals (agent retrospect synthesize,
// DOC-19). Proposal ids are fixed ULIDs so outputs compare across sides.
const (
	propGloss    = "01KQ6YEG00000000000000001A"
	propEdge     = "01KQ6YEG00000000000000001B"
	propDir      = "01KQ6YEG00000000000000001C"
	propFlag     = "01KQ6YEG00000000000000001D"
	propPending  = "01KQ6YEG00000000000000001E"
	propUpd      = "01KQ6YEG00000000000000001F"
	propRewire   = "01KQ6YEG00000000000000001G"
	propStale    = "01KQ6YEG00000000000000001H"
	propConflict = "01KQ6YEG00000000000000001J"
	staleEvent   = "01KQ6YEG0000000000000000ZZ"
)

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// missionIDs returns the side's mission ULID and its first status event id.
func missionIDs(t testing.TB, s *testkit.Sandbox) (string, string) {
	t.Helper()
	dir := filepath.Join(s.Dir, "kitty-specs", slugOf(t, s))
	var meta struct {
		MissionID string `json:"mission_id"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil || json.Unmarshal(b, &meta) != nil {
		t.Fatalf("meta.json: %v", err)
	}
	ev, err := os.ReadFile(filepath.Join(coord.Resolve(s.Dir, slugOf(t, s)).StatusDir(), "status.events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		EventID string `json:"event_id"`
	}
	json.Unmarshal([]byte(strings.SplitN(string(ev), "\n", 2)[0]), &row)
	return meta.MissionID, row.EventID
}

type legacyProp struct {
	id, status string
	evidence   string // "" = the mission's first event
	payload    string // YAML lines indented by 4
	kind       string
}

func writeLegacyRecord(t testing.TB, s *testkit.Sandbox, props []legacyProp) {
	t.Helper()
	slug := slugOf(t, s)
	mid, ev := missionIDs(t, s)
	var b strings.Builder
	fmt.Fprintf(&b, `schema_version: "1"
mission:
  mission_id: %s
  mid8: %s
  mission_slug: %s
  mission_type: software-dev
  mission_started_at: "2026-01-01T00:00:00+00:00"
  mission_completed_at: "2026-01-02T00:00:00+00:00"
mode:
  value: autonomous
  source_signal:
    kind: explicit_flag
    evidence: --autonomous
status: completed
started_at: "2026-01-02T00:00:00+00:00"
completed_at: "2026-01-02T00:10:00+00:00"
actor:
  kind: agent
  id: claude
helped: []
not_helpful: []
gaps: []
provenance:
  authored_by:
    kind: agent
    id: claude
  runtime_version: 3.2.0
  written_at: "2026-01-02T00:10:00+00:00"
  schema_version: 1
proposals:
`, mid, mid[:8], slug)
	for _, p := range props {
		e := ev
		if p.evidence != "" {
			e = p.evidence
		}
		fmt.Fprintf(&b, `- id: %s
  kind: %s
  payload:
    kind: %s
%s  rationale: because
  state:
    status: %s
  provenance:
    source_mission_id: %s
    source_evidence_event_ids: [%s]
    authored_by:
      kind: agent
      id: claude
`, p.id, p.kind, p.kind, p.payload, p.status, mid, e)
	}
	s.WriteFile(filepath.Join("kitty-specs", slug, "retrospective.yaml"), b.String())
}

func edgeYAML(key, from, to, kind string) string {
	return fmt.Sprintf("    %s:\n      from_node: %s\n      to_node: %s\n      kind: %s\n", key, from, to, kind)
}

func baseProps() []legacyProp {
	body := "# Use TDD\n\nWrite the test first.\n"
	return []legacyProp{
		{id: propGloss, status: "accepted", kind: "add_glossary_term", payload: "    term_key: widget\n    definition: A small thing.\n    definition_hash: " + sha("A small thing.") + "\n    related_terms: [gadget]\n"},
		{id: propEdge, status: "accepted", kind: "add_edge", payload: edgeYAML("edge", "directive:DIRECTIVE_001", "tactic:tdd", "requires")},
		{id: propDir, status: "accepted", kind: "synthesize_directive", payload: "    artifact_id: use-tdd\n    body: " + jsonQuote(body) + "\n    body_hash: " + sha(body) + "\n    scope:\n      actions: [implement]\n"},
		{id: propFlag, status: "pending", kind: "flag_not_helpful", payload: "    target:\n      kind: doctrine_tactic\n      urn: tactic:noisy/thing\n"},
		{id: propPending, status: "pending", kind: "add_glossary_term", payload: "    term_key: other\n    definition: x\n    definition_hash: h1\n"},
		{id: propUpd, status: "accepted", kind: "update_glossary_term", payload: "    term_key: lane\n    definition: A lane.\n    definition_hash: h2\n"},
		{id: propRewire, status: "accepted", kind: "rewire_edge", payload: edgeYAML("edge_old", "directive:DIRECTIVE_001", "tactic:tdd", "requires") + edgeYAML("edge_new", "directive:DIRECTIVE_002", "tactic:tdd", "suggests")},
	}
}

func jsonQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// TestDiffRetrospectSynthesizeLegacy covers legacy-schema proposal
// application: dry-run, --proposal-id selection and refusals, --apply
// writes (glossary, DRG edges, charter-pack artifacts, flags, provenance
// sidecars, status events), re-application, stale evidence, conflicts and
// --fabricate-empty.
func TestDiffRetrospectSynthesizeLegacy(t *testing.T) {
	p := approvedPair(t, "approved")
	syn := []string{"agent", "retrospect", "synthesize", "--mission", "$M"}
	cmp := func(extra ...string) { comparePair(t, p, append(append([]string{}, syn...), extra...)...) }
	// --fabricate-empty: insufficient artifacts first, then a real record.
	p.Both(func(s *testkit.Sandbox) {
		d := filepath.Join(s.Dir, "kitty-specs", slugOf(t, s))
		os.Rename(filepath.Join(d, "tasks.md"), filepath.Join(d, "tasks.md.bak"))
	})
	cmp("--fabricate-empty")
	cmp("--fabricate-empty", "--json")
	p.Both(func(s *testkit.Sandbox) {
		d := filepath.Join(s.Dir, "kitty-specs", slugOf(t, s))
		os.Rename(filepath.Join(d, "tasks.md.bak"), filepath.Join(d, "tasks.md"))
	})
	cmp("--fabricate-empty", "--json", "--proposal-id", propGloss)
	compareMissionTree(t, p, false)
	cmp("--fabricate-empty")
	cmp("--json")

	p.Both(func(s *testkit.Sandbox) { writeLegacyRecord(t, s, baseProps()) })
	cmp()
	cmp("--json")
	// Staleness reads the primary feature dir's event log, which the
	// coordination topology leaves empty: every applied proposal is stale.
	cmp("--apply", "--json")
	p.Both(func(s *testkit.Sandbox) {
		slug := slugOf(t, s)
		b, err := os.ReadFile(filepath.Join(coord.Resolve(s.Dir, slug).StatusDir(), "status.events.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		s.WriteFile(filepath.Join("kitty-specs", slug, "status.events.jsonl"), string(b))
	})
	cmp()
	cmp("--proposal-id", propPending)
	cmp("--proposal-id", propPending, "--proposal-id", "nope", "--json")
	cmp("--proposal-id", propGloss, "--apply", "--json")
	p.CompareTree(".kittify/glossary", true)
	cmp("--apply")
	for _, d := range []string{".kittify/glossary", ".kittify/drg", ".kittify/charter-packs"} {
		p.CompareTree(d, true)
	}
	compareMissionTree(t, p, false)
	cmp("--apply", "--json")

	// Stale evidence: exit 5.
	props := append(baseProps(), legacyProp{id: propStale, status: "accepted", kind: "add_glossary_term", evidence: staleEvent,
		payload: "    term_key: stale\n    definition: s\n    definition_hash: h3\n"})
	p.Both(func(s *testkit.Sandbox) { writeLegacyRecord(t, s, props) })
	cmp("--proposal-id", propStale)
	cmp("--proposal-id", propStale, "--apply", "--json")

	// Conflicts: exit 4 and rejection events.
	props = append(baseProps(), legacyProp{id: propConflict, status: "accepted", kind: "add_glossary_term",
		payload: "    term_key: widget\n    definition: Other.\n    definition_hash: " + sha("Other.") + "\n"})
	p.Both(func(s *testkit.Sandbox) { writeLegacyRecord(t, s, props) })
	cmp()
	cmp("--apply")
	cmp("--apply", "--json", "--proposal-id", propGloss, "--proposal-id", propConflict)
	compareMissionTree(t, p, false)
	compareGit(t, p)
}
