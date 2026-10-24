package maintenance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffRC7LegacyAssetMoves(t *testing.T) {
	for _, scenario := range []string{"overwrite", "symlink", "blocked-parent"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			p := missionfx.NewOraclePair(t)
			p.Both(func(s *testkit.Sandbox) {
				s.WriteFile(".kittify/scripts/custom.sh", "incoming content\n")
				switch scenario {
				case "overwrite":
					s.WriteFile(".kittify/overrides/scripts/custom.sh", "previous override\n")
				case "symlink":
					s.WriteFile("payload.txt", "symlink payload\n")
					path := filepath.Join(s.Dir, ".kittify/scripts/custom.sh")
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(s.Dir, "payload.txt"), path); err != nil {
						t.Fatal(err)
					}
				case "blocked-parent":
					s.WriteFile(".kittify/overrides/scripts", "not a directory\n")
				}
			})
			p.Compare(true, "migrate", "--dry-run", "--verbose")
			if scenario != "blocked-parent" {
				p.Compare(true, "migrate", "--force", "--verbose")
			} else {
				g, o := p.Run("migrate", "--force", "--verbose")
				if g.Code != 1 || o.Code != 1 {
					t.Errorf("exit codes: Go %d, RC7 %d", g.Code, o.Code)
				}
				// Traceback source locations differ between languages; the
				// Python exception class and filesystem error must agree.
				message := "FileExistsError: [Errno 17] File exists: "
				if !strings.Contains(g.Stderr, message) || !strings.Contains(o.Stderr, message) {
					t.Errorf("missing RC7 filesystem error:\nGo: %s\nRC7: %s", g.Stderr, o.Stderr)
				}
			}
			p.CompareTree(".kittify", true)
		})
	}
}
