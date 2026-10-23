package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalAssetRecheck(t *testing.T) {
	for _, scenario := range []string{"unchanged", "edited-output", "source-drift", "source-role-sticky", "extra-child", "directory-replaced", "marker-only", "canonical-peer", "environment"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			dir := filepath.Join(home, "skills")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("SPEC_KITTY_HOME", filepath.Join(home, ".kittify"))
			p, err := newPrep("test_owner", root, filepath.Join(home, ".kittify/cache"), ".test.lock")
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "SKILL.md")
			if _, err := p.observeAsset(dir, true, false); err != nil {
				t.Fatal(err)
			}
			if err := p.asset(target, []byte("canonical\n"), 0o644, false, false); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "source")
			if err := os.WriteFile(source, []byte("input\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := p.observeAsset(source, false, true); err != nil {
				t.Fatal(err)
			}
			if scenario == "source-role-sticky" {
				p.observeAsset(source, false, false)
			}
			if err := p.finish(filepath.Join(home, ".kittify/cache/version.lock"), "test"); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "edited-output":
				os.WriteFile(target, []byte("operator edit\n"), 0o644)
			case "source-drift", "source-role-sticky":
				os.WriteFile(source, []byte("different input\n"), 0o644)
			case "extra-child":
				os.WriteFile(filepath.Join(dir, "custom"), []byte("keep\n"), 0o644)
			case "directory-replaced":
				os.Rename(dir, dir+".old")
				os.Mkdir(dir, 0o755)
			case "marker-only":
				os.MkdirAll(filepath.Dir(p.inventory), 0o755)
				os.WriteFile(p.inventory, p.writes[p.inventory].content, 0o644)
			case "canonical-peer":
				// Apply the same complete preparation as an independently
				// serialized peer, then recheck the original stale preparation.
				for _, w := range p.sortedWrites() {
					if err := applyWrite(w); err != nil {
						t.Fatal(err)
					}
				}
			case "environment":
				t.Setenv("SPEC_KITTY_PACKS_ROOT", filepath.Join(root, "other"))
			}
			err = p.apply()
			wantSuccess := scenario == "unchanged" || scenario == "canonical-peer"
			if (err == nil) != wantSuccess {
				t.Fatalf("apply error = %v; want success %v", err, wantSuccess)
			}
			if !wantSuccess && !strings.Contains(err.Error(), "Global asset") {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if scenario == "edited-output" {
				if data, _ := os.ReadFile(target); string(data) != "operator edit\n" {
					t.Fatal("concurrent edit was overwritten")
				}
			}
			if wantSuccess {
				if data, _ := os.ReadFile(target); string(data) != "canonical\n" {
					t.Fatal("canonical payload was not installed")
				}
			} else if scenario != "marker-only" {
				if _, err := os.Stat(p.inventory); !os.IsNotExist(err) {
					t.Fatal("refused batch certified its inventory")
				}
			}
		})
	}
}
