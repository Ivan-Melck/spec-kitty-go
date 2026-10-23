package doctrine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeActivationFixture(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestActivationLedgerPrecedence(t *testing.T) {
	root := t.TempDir()
	writeActivationFixture(t, root, ".kittify/config.yaml", "charter: .kittify/charter/charter.yaml\nactivated_directives: [legacy-config]\n")
	writeActivationFixture(t, root, ".kittify/charter/charter.yaml", "activated_directives: [legacy-charter]\n")
	writeActivationFixture(t, root, ActivationLedgerRelative, "activated_directives: []\nmission_type_activations: [plan]\n")
	var warnings []string
	prior := ActivationWarnLog
	ActivationWarnLog = func(message string) { warnings = append(warnings, message) }
	t.Cleanup(func() { ActivationWarnLog = prior })
	for range 2 {
		act, err := LoadActivation(root)
		if err != nil {
			t.Fatal(err)
		}
		if act.Activated(Directive) == nil || len(act.Activated(Directive)) != 0 || len(act.MissionTypes) != 1 || act.MissionTypes[0] != "plan" {
			t.Fatalf("legacy keys overrode the ledger: %+v", act)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "activation ledger (activation.yaml) wins") || !strings.Contains(warnings[0], "legacy") && !strings.Contains(warnings[0], "activated_directives") {
		t.Fatalf("stray legacy warning: %q", warnings)
	}
}

func TestActivationSourceFreshLegacyAndUpgradeScope(t *testing.T) {
	root := t.TempDir()
	writeActivationFixture(t, root, ".kittify/config.yaml", "agents:\n  available: []\n")
	store, err := ResolveActivationStore(root)
	if err != nil || !store.Ledger || store.Exists {
		t.Fatalf("fresh store=%+v err=%v", store, err)
	}
	err = LegacyActivationScope([]string{root}, func() error {
		store, err := ResolveActivationStore(root)
		if err != nil || store.Ledger || store.Path != ConfigPath(root) {
			t.Fatalf("upgrade store=%+v err=%v", store, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err = ResolveActivationStore(root)
	if err != nil || !store.Ledger {
		t.Fatalf("upgrade scope leaked: store=%+v err=%v", store, err)
	}
	writeActivationFixture(t, root, ".kittify/config.yaml", "activated_directives: [DIRECTIVE_024]\ndeactivated_directives: [DIRECTIVE_024]\n")
	act, err := LoadActivation(root)
	if err != nil || !act.Activated(Directive).Has("DIRECTIVE_024") {
		t.Fatalf("legacy deny must not apply: act=%+v err=%v", act, err)
	}
	store, err = ResolveActivationStore(root)
	if err != nil || store.Ledger || len(store.Unmigrated) != 1 {
		t.Fatalf("unmigrated store=%+v err=%v", store, err)
	}
}

func TestActivationLedgerRejectsInvalidState(t *testing.T) {
	for _, tc := range []struct{ text, contains string }{
		{"- item\n", "root must be a mapping"},
		{"activated_directives: false\n", "must be a list, got bool"},
		{"activated_directives: [12]\n", "must list string ids, got [12]"},
		{"activated_directivess: []\n", "Unknown activation ledger key 'activated_directivess'"},
		{"deactivated_future_kind: [false]\n", "must list string ids"},
		{"activated_directives: [\n", "Invalid YAML"},
	} {
		t.Run(tc.contains, func(t *testing.T) {
			root := t.TempDir()
			writeActivationFixture(t, root, ActivationLedgerRelative, tc.text)
			_, err := LoadActivation(root)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error=%v, want %s", err, tc.contains)
			}
		})
	}
	root := t.TempDir()
	writeActivationFixture(t, root, ActivationLedgerRelative, "deactivated_future_kind: [unknown]\nactivated_directives: null\n")
	if _, err := LoadActivation(root); err != nil {
		t.Fatalf("unknown deny keys and null lists must be preserved: %v", err)
	}
}

func TestMissionTypeSubstantiveSpecCapability(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    *bool
		invalid bool
	}{
		{"false", boolPointer(false), false}, {"true", boolPointer(true), false},
		{"null", nil, false}, {"'off'", boolPointer(false), false},
		{"[true]", nil, true}, {"'maybe'", nil, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			root := t.TempDir()
			writeActivationFixture(t, root, ".kittify/config.yaml", "mission_type_activations: [custom]\n")
			writeActivationFixture(t, root, ".kittify/missions/mission_types/custom.yaml", "id: custom\ndisplay_name: Custom\naction_sequence: [specify]\nrequires_substantive_spec: "+tc.value+"\n")
			mts, err := LayeredMissionTypes(root)
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "requires_substantive_spec") {
					t.Fatalf("expected capability validation error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := mts["custom"].RequiresSubstantiveSpec
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("capability=%v, want %v", got, tc.want)
			}
			want := tc.want != nil && *tc.want
			if got := RequiresSubstantiveSpecFor(root, "custom"); got != want {
				t.Fatalf("requires substantive spec=%t, want %t", got, want)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
