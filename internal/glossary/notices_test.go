package glossary

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Go-only: a fresh dispatch's invocation id cannot match pre-existing
// oracle events, so the notice surface is pinned against observation.py's
// documented rendering instead of a differential run.
func TestCollectNoticesAndRender(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kittify", "events", "glossary")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "m.events.jsonl"), []byte(
		`{"event_type": "SemanticCheckEvaluated", "step_id": "INV1", "findings": [{"term": {"surface_text": "lane"}, "severity": "HIGH", "conflict_type": "ambiguous"}, {"term": {"surface_text": "wp"}, "severity": "low", "conflict_type": "x"}]}
{"event_type": "semantic_check_evaluated", "invocation_id": "INV1", "term": "lane", "severity": "critical", "conflict_type": "scope_mismatch"}
{"event_type": "SemanticCheckEvaluated", "step_id": "OTHER", "findings": [{"term": "zzz", "severity": "high"}]}
not json
`), 0o644)
	got := NoticeLines(CollectNotices(root, "INV1"))
	want := []string{"", "⚠ Glossary drift [critical]: \"lane\" — scope_mismatch detected", "  Suggest: run `spec-kitty glossary conflicts --unresolved`"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if NoticeLines(CollectNotices(root, "NONE")) != nil {
		t.Fatal("expected no notices")
	}
}
