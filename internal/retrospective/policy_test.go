package retrospective

import (
	"os"
	"path/filepath"
	"testing"
)

func writePolicyFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestResolvePolicy(t *testing.T) {
	t.Setenv("SPEC_KITTY_RETROSPECTIVE", "")
	t.Setenv("SPEC_KITTY_MODE", "")
	cases := []struct {
		name              string
		files             map[string]string
		enabled, blocking bool
		source            map[string]string
		err               string
	}{
		{name: "defaults", enabled: true, source: map[string]string{"enabled": "<default>", "timing": "<default>"}},
		{name: "config disables", files: map[string]string{configRel: "retrospective:\n  enabled: false\n"},
			source: map[string]string{"enabled": ".kittify/config.yaml#retrospective.enabled"}},
		{name: "config strict gate", files: map[string]string{configRel: "retrospective:\n  timing: before_completion\n  failure_policy: block\n"},
			enabled: true, blocking: true, source: map[string]string{"failure_policy": ".kittify/config.yaml#retrospective.failure_policy"}},
		{name: "charter wins over config", files: map[string]string{
			charterMDRel: "---\nretrospective:\n  enabled: true\n---\n",
			configRel:    "retrospective:\n  enabled: false\n  failure_policy: block\n  timing: before_completion\n"},
			enabled: true, blocking: true,
			source: map[string]string{"enabled": ".kittify/charter/charter.md:retrospective.enabled", "timing": ".kittify/config.yaml#retrospective.timing"}},
		{name: "charter.yaml authority over charter.md", files: map[string]string{
			charterMDRel:   "---\nretrospective:\n  enabled: false\n---\n",
			charterYAMLRel: "governance:\n  retrospective:\n    enabled: 'yes'\n"},
			enabled: true, source: map[string]string{"enabled": ".kittify/charter/charter.yaml:governance.retrospective.enabled"}},
		{name: "unknown keys strict", files: map[string]string{configRel: "retrospective:\n  strict_keys: true\n  zz: 1\n  aa: 2\n"},
			err: "[unknown_key] in .kittify/config.yaml#retrospective: Unknown keys in retrospective block: ['aa', 'zz']"},
		{name: "bad enum", files: map[string]string{configRel: "retrospective:\n  timing: 3\n"},
			err: "[invalid_enum] in .kittify/config.yaml#retrospective: timing: got 3, expected one of ['before_completion', 'post_completion']"},
		{name: "bad permission", files: map[string]string{configRel: "retrospective:\n  permissions:\n    write_record: 'yes'\n"},
			err: "[invalid_enum] in .kittify/config.yaml#retrospective: permissions.write_record: expected boolean, got str."},
		{name: "charter.yaml validation", files: map[string]string{charterYAMLRel: "governance:\n  retrospective:\n    timing: later\n    enabled: [1]\n"},
			err: "[invalid_type] in .kittify/charter/charter.yaml: enabled: Input should be a valid boolean; timing: Input should be 'post_completion' or 'before_completion'"},
		{name: "charter.yaml types", files: map[string]string{charterYAMLRel: "governance:\n  retrospective:\n    permissions: 3\n    strict_keys: maybe\n    generator: 2\n"},
			err: "[invalid_type] in .kittify/charter/charter.yaml: permissions: Input should be a valid dictionary or instance of RetrospectiveGovernancePermissions; generator: Input should be 'python'; strict_keys: Input should be a valid boolean, unable to interpret input"},
		{name: "charter.md not a mapping", files: map[string]string{charterMDRel: "---\nretrospective: 3\n---\n"},
			err: "[invalid_type_for_retrospective_block] in .kittify/charter/charter.md: Charter frontmatter 'retrospective:' value must be a mapping; got int."},
		{name: "unrelated malformed charter.yaml", files: map[string]string{charterYAMLRel: "a: [\n"}, enabled: true,
			source: map[string]string{"enabled": "<default>"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writePolicyFiles(t, tc.files)
			p, src, err := ResolvePolicy(root)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("error = %v, want %s", err, tc.err)
				}
				if src.GetString("enabled") != resolutionErr && tc.name != "unknown keys strict" && tc.name != "bad enum" && tc.name != "bad permission" {
					t.Errorf("source map not marked: %v", src.GetString("enabled"))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Enabled() != tc.enabled || p.BlocksCompletion() != tc.blocking {
				t.Errorf("enabled=%v blocking=%v", p.Enabled(), p.BlocksCompletion())
			}
			for k, v := range tc.source {
				if got := src.GetString(k); got != v {
					t.Errorf("source[%s] = %q, want %q", k, got, v)
				}
			}
		})
	}
}

func TestResolvePolicyObservesEnv(t *testing.T) {
	t.Setenv("SPEC_KITTY_NO_DEPRECATION_WARNINGS", "1")
	t.Setenv("SPEC_KITTY_RETROSPECTIVE", "0")
	t.Setenv("SPEC_KITTY_MODE", "strict")
	root := writePolicyFiles(t, map[string]string{configRel: "retrospective:\n  timing: post_completion\n"})
	p, src, err := ResolvePolicy(root)
	if err != nil || !p.Enabled() {
		t.Fatalf("env must not override: %v %v", err, p.Enabled())
	}
	want := map[string]string{"enabled": "<env:SPEC_KITTY_RETROSPECTIVE>", "timing": ".kittify/config.yaml#retrospective.timing",
		"failure_policy": "<env:SPEC_KITTY_MODE>"}
	for k, v := range want {
		if got := src.GetString(k); got != v {
			t.Errorf("source[%s] = %q, want %q", k, got, v)
		}
	}
}
