package maintenance_test

import (
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffMigrateRebaselineRglobOrder covers artifacts that share an
// artifact_key: the re-indexed snapshot keeps them in Path.rglob("*") order
// (scandir order, LIFO directory walk), not name order.
func TestDiffMigrateRebaselineRglobOrder(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		m := "kitty-specs/047-dupkeys/"
		s.WriteFile(m+"meta.json", `{"slug": "047-dupkeys", "mission_type": "software-dev"}`+"\n")
		for _, rel := range []string{"z/notes.md", "spec.md", "a/deep/er/notes.md", "notes.md", "m/notes.md", "a/notes.md", "b/x/notes.md", "research/notes.md"} {
			s.WriteFile(m+rel, rel+"\n")
		}
		s.WriteFile(m+".kittify/dossiers/047-dupkeys/snapshot-latest.json", `{"parity_hash_sha256": "`+strings.Repeat("ef", 32)+`"}`)
	})
	compareSteps(t, p, "", "",
		step{"migrate", "rebaseline-dossier-hashes", "--dry-run", "--json"},
		step{"migrate", "rebaseline-dossier-hashes"},
	)
	if os.Getenv("KITTY_TEST_TRACE") != "" {
		t.Logf("oracle:\n%s", p.Oracle.ReadFile("kitty-specs/047-dupkeys/.kittify/dossiers/047-dupkeys/snapshot-latest.json"))
	}
	p.CompareTree("kitty-specs", true)
}
