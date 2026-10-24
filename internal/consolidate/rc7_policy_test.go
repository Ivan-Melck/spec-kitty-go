package consolidate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRC7GatePolicyModes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".kittify"), 0o755); err != nil {
		t.Fatal(err)
	}
	values := []any{"warn", "block", "off", "BLOCK", " block ", "Warn", "unknown", "", nil, 0, 1, true, false, []any{}, map[string]any{}}
	var got []string
	for _, raw := range values {
		config, _ := json.Marshal(map[string]any{"policy": map[string]any{"merge_gates": map[string]any{"mode": raw}}})
		if err := os.WriteFile(filepath.Join(root, ".kittify/config.yaml"), config, 0o644); err != nil {
			t.Fatal(err)
		}
		mode := LoadGatePolicy(root).Mode
		want := "warn"
		if raw == "block" || raw == "off" {
			want = raw.(string)
		}
		if mode != want {
			t.Errorf("mode %v: got %s, want %s", raw, mode, want)
		}
		got = append(got, mode)
	}
	if os.Getenv("SPEC_KITTY_ORACLE") == "" {
		return
	}
	raw, _ := json.Marshal(values)
	cmd := exec.Command(oraclePython(t), "-I", "-c", `
import json, sys
from pathlib import Path
from specify_cli.policy.config import load_policy_config
root = Path(sys.argv[1])
modes = []
for raw in json.loads(sys.argv[2]):
    (root / '.kittify/config.yaml').write_text(json.dumps({'policy': {'merge_gates': {'mode': raw}}}))
    modes.append(load_policy_config(root).merge_gates.mode)
print(json.dumps(modes))
`, root, string(raw))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("oracle: %v\n%s", err, output)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("oracle JSON: %v\n%s", err, output)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Go modes %v differ from RC7 %v", got, want)
	}
}
