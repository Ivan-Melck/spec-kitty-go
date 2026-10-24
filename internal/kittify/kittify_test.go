package kittify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveBuildIDMatchesOracle(t *testing.T) {
	// Observed in an oracle init (config.yaml project block).
	got := DeriveBuildID("2015b5cd-a93d-4d0e-8dab-6987d729fbdd", "aec3eace9d25")
	if want := "3e8e52d0-1c37-549a-882d-578c8a93b9fa"; got != want {
		t.Fatalf("build_id = %s, want %s", got, want)
	}
}

func TestInitSequenceMetadataShape(t *testing.T) {
	root := t.TempDir()
	m := &Metadata{Version: "4.0.0rc8", InitializedAt: "2026-10-20T13:50:11.694544+00:00", PythonVersion: "3.13.15", Platform: "linux", PlatformVersion: "Linux-x"}
	if _, err := m.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, err := StampSchema(root); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".kittify", "metadata.yaml"))
	want := MetadataHeader + `spec_kitty:
  schema_version: 3
  version: 4.0.0rc8
  initialized_at: '2026-10-20T13:50:11.694544+00:00'
  last_upgraded_at:
  schema_capabilities:
    canonical_context: true
    event_log_authority: true
    ownership_manifest: true
    thin_shims: true
environment:
  python_version: 3.13.15
  platform: linux
  platform_version: Linux-x
migrations:
  applied: []
`
	if string(b) != want {
		t.Fatalf("metadata.yaml:\n%s\nwant:\n%s", b, want)
	}
	st, _ := os.Stat(filepath.Join(root, ".kittify", "metadata.yaml"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	// A reload + save with no change is skipped.
	if wrote, err := LoadMetadata(root).Save(root); err != nil || wrote {
		t.Fatalf("no-op save wrote=%v err=%v", wrote, err)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	id, err := EnsureIdentity(root)
	if err != nil || !id.Complete() {
		t.Fatalf("identity %+v %v", id, err)
	}
	if err := SaveVCSConfig(root); err != nil {
		t.Fatal(err)
	}
	if err := SaveAgentConfig(root, AgentConfig{Available: []string{"claude"}, AutoCommit: true}); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgentConfig(root)
	if err != nil || len(ac.Available) != 1 || !ac.AutoCommit || ac.LintOnEdit {
		t.Fatalf("%+v %v", ac, err)
	}
	if VCSType(root) != "git" || LoadIdentity(root) != id {
		t.Fatal("round trip mismatch")
	}
	st, _ := os.Stat(ConfigPath(root))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %o", st.Mode().Perm())
	}
}
