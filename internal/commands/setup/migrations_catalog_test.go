package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/migrationledger"
)

// The catalog is generated from the upstream registry; it must follow the
// shipped execution-order manifest exactly (ids and order).
func TestMigrationCatalogFollowsOrderManifest(t *testing.T) {
	raw := assets.MustRead("specify_cli/upgrade/migrations/order_manifest.txt")
	var manifest []string
	for _, line := range strings.Split(string(raw), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line != "" {
			manifest = append(manifest, line)
		}
	}
	var catalog []string
	for _, m := range migrationCatalog {
		catalog = append(catalog, m.ID)
	}
	if !reflect.DeepEqual(catalog, manifest) {
		t.Fatalf("catalog order differs from order_manifest.txt\ncatalog:  %v\nmanifest: %v", catalog, manifest)
	}
	if len(catalog) != 136 {
		t.Fatalf("catalog has %d entries, want 136", len(catalog))
	}
	for _, id := range []string{"0.9.0_residue_safe_frontmatter_only_lanes", "0.9.1_residue_safe_complete_migration",
		"2.0.6_residue_safe_consistency_sweep", "3.1.1_residue_safe_normalize_status_json", "4.0.0rc8_promote_interview_selections"} {
		if _, ok := migrationImpls[id]; !ok {
			t.Errorf("%s has no Go implementation", id)
		}
	}
}

// Superseded migrations are recorded no-ops whose runner record carries the
// superseded_by note; their successors share the predecessor's slot (#6589,
// #6629, #6669).
func TestSupersededMigrationsAreRecordedNoOps(t *testing.T) {
	want := map[string]string{
		"0.9.0_frontmatter_only_lanes":             "Superseded by 0.9.0_residue_safe_frontmatter_only_lanes",
		"0.9.1_complete_migration":                 "Superseded by 0.9.1_residue_safe_complete_migration",
		"2.0.6_consistency_sweep":                  "Superseded by 2.0.6_residue_safe_consistency_sweep",
		"3.1.1_normalize_status_json":              "Superseded by 3.1.1_residue_safe_normalize_status_json",
		"3.2.5_agents_skills_gitignore_backfill":   "Superseded by 4.0.0rc8_narrow_agent_gitignore",
		"3.2.6rc3_narrow_cursor_gitignore":         "Superseded by 4.0.0rc8_narrow_agent_gitignore",
		"unify_charter_activation_promote_answers": "Superseded by 4.0.0rc8_promote_interview_selections",
	}
	root := t.TempDir()
	for id, note := range want {
		m, ok := migrationByID(id)
		if !ok || m.SupersededBy != note {
			t.Fatalf("%s: superseded_by=%q, want %q", id, m.SupersededBy, note)
		}
		if m.detect(root) {
			t.Errorf("%s: a superseded migration must never be detected", id)
		}
		if okApply, reason := m.canApply(root); okApply || reason != note {
			t.Errorf("%s: can_apply=(%v, %q), want (false, %q)", id, okApply, reason, note)
		}
		successor := strings.TrimPrefix(note, "Superseded by ")
		if s, ok := migrationByID(successor); !ok || s.SupersededBy != "" {
			t.Errorf("%s: successor %s missing or itself superseded", id, successor)
		}
	}
	// The runner record carries the note; a seeded record does not.
	m, _ := migrationByID("2.0.6_consistency_sweep")
	ledger := migrationledger.Ledger{Root: root}
	if err := ledger.Append(runnerMigrationRecord(m, "skipped")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".kittify", "migration-ledger.jsonl"))
	if !strings.Contains(string(b), `"superseded_by": "Superseded by 2.0.6_residue_safe_consistency_sweep"`) {
		t.Fatalf("runner record lacks superseded_by: %s", b)
	}
	records, err := ledger.Read(false)
	if err != nil || len(records) != 1 || records[0].SupersededBy != m.SupersededBy {
		t.Fatalf("superseded_by did not round-trip: %+v %v", records, err)
	}
	if migrationRecord(m, "success", "").JSON().Has("superseded_by") {
		t.Fatal("a seeded record must not carry superseded_by")
	}
}

// A successor is inert once its predecessor is settled in the selection view.
func TestResidueSuccessorInertOncePredecessorSettled(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "kitty-specs", "m", "tasks", "planned"), 0o755)
	os.WriteFile(filepath.Join(root, "kitty-specs", "m", "spec.md"), []byte("# m\n"), 0o644)
	os.WriteFile(filepath.Join(root, "kitty-specs", "m", "tasks", "planned", "WP01.md"), []byte("---\n---\n"), 0o644)
	successor, _ := migrationByID("0.9.0_residue_safe_frontmatter_only_lanes")
	if !successor.detect(root) {
		t.Fatal("the successor must detect a Mission's legacy lanes")
	}
	pred, _ := migrationByID("0.9.0_frontmatter_only_lanes")
	if err := (migrationledger.Ledger{Root: root}).Append(migrationRecord(pred, "success", "")); err != nil {
		t.Fatal(err)
	}
	if successor.detect(root) {
		t.Fatal("the successor must be inert once its predecessor is settled")
	}
}
