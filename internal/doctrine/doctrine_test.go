package doctrine

import (
	"os"
	"path/filepath"
	"testing"
)

func setHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestBuiltInRepositories(t *testing.T) {
	setHome(t)
	off := NewOffering("", nil)
	counts := map[Kind]int{Directive: 38, Paradigm: 14, AgentProfile: 27, Toolguide: 18}
	for k, want := range counts {
		if got := off.Repo(k).Len(); got < want-2 || got > want+2 {
			t.Errorf("%s: got %d artifacts, want ~%d", k, got, want)
		}
	}
	d := off.Repo(Directive).Get("024-locality-of-change")
	if d == nil || d.ID != "DIRECTIVE_024" || d.Title() != "Locality of Change" {
		t.Fatalf("directive lookup by stem failed: %+v", d)
	}
	if _, err := os.Stat(d.Path); err != nil {
		t.Fatalf("materialised path missing: %v", err)
	}
	if d.Source != LayerBuiltIn {
		t.Fatalf("source = %q", d.Source)
	}
}

func TestProjectOverlayMerge(t *testing.T) {
	setHome(t)
	root := t.TempDir()
	dir := filepath.Join(root, ".kittify", "charter-packs", "directives")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "024-x.directive.yaml"), []byte("id: DIRECTIVE_024\ntitle: Local Title\n"), 0o644)
	off := NewOffering(root, nil)
	d := off.Repo(Directive).Get("DIRECTIVE_024")
	if d.Source != LayerProject || d.Title() != "Local Title" || d.Str("enforcement") == "" {
		t.Fatalf("merge failed: %+v", d.Data)
	}
}

func TestBuiltInGraphResolve(t *testing.T) {
	setHome(t)
	g, err := BuiltInGraph()
	if err != nil {
		t.Fatal(err)
	}
	if !g.NodeURNs()["action:software-dev/specify"] {
		t.Fatal("missing action node")
	}
	rc := ResolveContext(g, "action:software-dev/specify", 2)
	if len(rc.ArtifactURNs) == 0 {
		t.Fatal("no artifacts resolved")
	}
}

func TestNormalizeDirectiveID(t *testing.T) {
	cases := map[string]string{"4": "DIRECTIVE_004", "DIRECTIVE_024": "DIRECTIVE_024", "025-boy-scout-rule": "DIRECTIVE_025", "use-c4-model-techniques": "USE_C4_MODEL_TECHNIQUES"}
	for in, want := range cases {
		if got := NormalizeDirectiveID(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}

func TestActivationThreeState(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".kittify"), 0o755)
	os.WriteFile(filepath.Join(root, ".kittify", "config.yaml"), []byte("mission_type_activations:\n- software-dev\nactivated_directives: []\n"), 0o644)
	a, err := LoadActivation(root)
	if err != nil {
		t.Fatal(err)
	}
	if a.Activated(Directive) == nil || len(a.Activated(Directive)) != 0 {
		t.Fatal("explicit empty directives should be empty non-nil")
	}
	if a.Activated(Tactic) != nil {
		t.Fatal("absent tactics should be nil")
	}
	if len(a.MissionTypes) != 1 {
		t.Fatal(a.MissionTypes)
	}
	os.WriteFile(filepath.Join(root, ".kittify", "config.yaml"), []byte("activated_directives: nope\n"), 0o644)
	if _, err := LoadActivation(root); err == nil {
		t.Fatal("expected config error")
	}
}
