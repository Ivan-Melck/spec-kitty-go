package charter

import (
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func activationFixture(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestActivationLedgerRequiredFloorAndIdentity(t *testing.T) {
	root := t.TempDir()
	activationFixture(t, root, ".kittify/config.yaml", "charter_packs:\n  org:\n    packs:\n    - name: base\n      local_path: base\n    - name: overlay\n      local_path: overlay\n")
	activationFixture(t, root, "base/org-charter.yaml", "required_tactics: [declared-tactic]\n")
	activationFixture(t, root, "overlay/org-charter.yaml", "extends: base\n")
	activationFixture(t, root, "base/tactics/file-stem.tactic.yaml", "id: declared-tactic\ntitle: Fixture\n")
	activationFixture(t, root, dct.ActivationLedgerRelative, "activated_tactics: [declared-tactic]\ndeactivated_tactics: [file-stem, unknown-stem]\n")
	act, err := dct.LoadActivation(root)
	if err != nil {
		t.Fatal(err)
	}
	if !act.Admits(dct.Tactic, "declared-tactic") || !act.Denied[dct.Tactic]["unknown-stem"] {
		t.Fatalf("activation=%#v", act)
	}
	before, _ := os.ReadFile(dct.ActivationLedgerPath(root))
	if _, err := Deactivate(root, "tactic", "declared-tactic"); err == nil || !strings.Contains(err.Error(), "'base' (via 'overlay')") {
		t.Fatalf("required refusal: %v", err)
	}
	after, _ := os.ReadFile(dct.ActivationLedgerPath(root))
	if string(before) != string(after) {
		t.Fatal("refused write modified ledger")
	}
	activationFixture(t, root, "base/org-charter.yaml", "extends: overlay\n")
	if _, err := dct.LoadActivation(root); err == nil || !strings.Contains(err.Error(), "Cycle detected") {
		t.Fatalf("cycle not refused: %v", err)
	}
}

func TestActivationLedgerGuardAndPrecondition(t *testing.T) {
	root := t.TempDir()
	activationFixture(t, root, ".kittify/config.yaml", "agents: {}\n")
	activationFixture(t, root, dct.ActivationLedgerRelative, "# retained\ndeactivated_tactics: [b, a, b]\ndeactivated_future: [untouched]\n")
	target, err := LoadWriteTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	target.Data.Set("deactivated_tactics", []any{"c"})
	if err := target.Save(); err == nil || !strings.Contains(err.Error(), "added: ['c'], removed: ['a', 'b']") {
		t.Fatalf("guard=%v", err)
	}
	if err := target.SaveWithChoice("activate"); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(target.Path)
	if !strings.Contains(string(actual), "# retained") || !strings.Contains(string(actual), "deactivated_future: [untouched]") {
		t.Fatal(string(actual))
	}
	target, err = LoadWriteTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	target.Data.Set("activated_tactics", []any{})
	activationFixture(t, root, dct.ActivationLedgerRelative, "deactivated_tactics: [changed]\n")
	if err := target.Save(); err == nil {
		t.Fatal("concurrent edit overwritten")
	}
}

func TestActivationLedgerFlowRoot(t *testing.T) {
	root := t.TempDir()
	activationFixture(t, root, "activation.yaml", "{}\n")
	doc := jsonx.NewObject("activated_tactics", []any{"one"})
	if err := writeYAMLDocument(filepath.Join(root, "activation.yaml"), doc, 80); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "activation.yaml"))
	if string(b) != "{activated_tactics: [one]}\n" {
		t.Fatalf("flow round trip=%q", b)
	}
}
