package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

// These invoke real public commands inside disposable fixtures. They are bounded
// handler regression witnesses, not a full qualifier/admission receipt.
func TestPublicHubnotScenarioHandlers(t *testing.T) {
	product, hn := os.Getenv("GO_KITTY_TEST_PRODUCT_BINARY"), os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY")
	if product == "" || hn == "" {
		t.Skip("explicit compiled consumer and admitted hn required")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"binding.admitted", "binding.refusals", "work.rich-roundtrip", "work.simple-update-guards", "graph.lifecycle", "graph.invalid-suppliers", "assignment.provenance", "discussion.attribution", "operation.historical-replay", "operation.key-attempt-refusal", "graph.partial-definition", "discussion.partial-pair", "replicas.selected-withheld", "replicas.reconstruction"} {
		t.Run(id, func(t *testing.T) {
			o := qualificationOptions{Repository: root, Product: product, HN: hn, GoBinary: os.Getenv("GO_KITTY_TEST_GO_BINARY"), EvidenceDir: t.TempDir()}
			if base := os.Getenv("GO_KITTY_TEST_ARTIFACT_ROOT"); base != "" {
				dir, err := os.MkdirTemp(base, "public-")
				if err != nil {
					t.Fatal(err)
				}
				o.EvidenceDir = dir
				t.Log("retained artifacts", dir)
			}
			r := newScenarioRunner(o)
			defer r.close()
			defer saveHandlerProbe(t, r)
			row := testcampaign.HubnotScenarioResult{Measurements: map[string]json.RawMessage{}}
			r.row = &row
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			for _, s := range testcampaign.HubnotManifest().Scenarios {
				if s.ID == id {
					r.row.Scenario = s
					if err := r.run(ctx, s); err != nil {
						t.Fatalf("%s: %v (commands=%d)", id, err, len(row.Commands))
					}
					return
				}
			}
			t.Fatal("scenario absent")
		})
	}
}

func TestExecTraceRejoinsActualProcessCompletions(t *testing.T) {
	raw := `10 execve("/qualified/hn", ["hn", "issue", "list", "--details"], 0x123 /* 5 vars */ <unfinished ...>
11 execve("/usr/bin/git", ["git", "status"], 0x456 /* 5 vars */) = 0
10 <... execve resumed>) = 0
12 execve("/missing/git", ["git"], 0x1) = -1 ENOENT (No such file)
10 +++ exited with 0 +++
`
	records, err := parseExecTrace(raw)
	if err != nil || len(records) != 3 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	if records[1].Path != "/qualified/hn" || !records[1].Success || records[2].Success {
		t.Fatal("split completion lost or failed exec counted")
	}
	for _, bad := range []string{`10 execve("/x", ["x"], 0x1 <unfinished ...>`, `10 <... execve resumed>) = 0`, `10 execve("/x", ["x", ...], 0x1) = 0`} {
		if _, err := parseExecTrace(bad); err == nil {
			t.Fatal("incomplete trace admitted")
		}
	}
}
func TestUnitCensusRejectsSkippedSubtestsAndMissingStarts(t *testing.T) {
	pkg := "./internal/example"
	prefix := `"Package":"github.com/spec-kitty/go-kitty3/internal/example"`
	line := func(action, test string) string {
		return `{` + prefix + `,"Action":"` + action + `","Test":"` + test + `"}` + "\n"
	}
	raw := line("start", "") + line("run", "TestLeaf") + line("run", "TestLeaf/case") + line("pass", "TestLeaf/case") + line("pass", "TestLeaf") + line("pass", "")
	if _, err := validateUnitCensus([]byte(raw), pkg, []string{"TestLeaf"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(raw, line("pass", "TestLeaf/case"), line("skip", "TestLeaf/case"), 1), strings.Replace(raw, line("run", "TestLeaf"), "", 1), raw + line("pass", "TestLeaf")} {
		if _, err := validateUnitCensus([]byte(bad), pkg, []string{"TestLeaf"}); err == nil {
			t.Fatal("incomplete unit census admitted")
		}
	}
}

func TestPublicPerformanceFixtureSmallOracle(t *testing.T) {
	product, hn := os.Getenv("GO_KITTY_TEST_PRODUCT_BINARY"), os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY")
	if product == "" || hn == "" {
		t.Skip("explicit real binaries required")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	if base := os.Getenv("GO_KITTY_TEST_ARTIFACT_ROOT"); base != "" {
		evidence, err = os.MkdirTemp(base, "small-query-traces-")
		if err != nil {
			t.Fatal(err)
		}
		t.Log("retained artifacts", evidence)
	}
	r := newScenarioRunner(qualificationOptions{Repository: root, Product: product, HN: hn, EvidenceDir: evidence})
	defer r.close()
	defer saveHandlerProbe(t, r)
	r.row = &testcampaign.HubnotScenarioResult{Measurements: map[string]json.RawMessage{}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err = r.preparePerformanceCount(ctx, 4); err != nil {
		t.Fatal(err)
	}
	observed, err := r.performance.call(ctx, "inventory", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.verifyPerformanceInventory(observed); err != nil {
		t.Fatal(err)
	}
	if err = r.verifyPerformanceComments(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"performance.known-reference", "performance.original-recovery"} {
		if err = r.performanceScenario(ctx, id); err != nil {
			t.Fatal(err)
		}
		var counts map[string]int
		if json.Unmarshal(r.row.Measurements["exec_counts"], &counts) != nil || counts["consumer"] != 1 || counts["hubnot"] < 1 || counts["git"] < 1 {
			t.Fatal("missing separate actual child census")
		}
		var trace testcampaign.HubnotArtifact
		if json.Unmarshal(r.row.Measurements["process_trace"], &trace) != nil || trace.Bytes == 0 {
			t.Fatal("missing separate trace bytes")
		}
		raw, err := os.ReadFile(trace.Path)
		if err != nil || shaBytes(raw) != trace.SHA256 {
			t.Fatal("trace identity mismatch")
		}
		if _, err = parseExecTrace(string(raw)); err != nil {
			t.Fatal(err)
		}
		var uniqueChildren int
		if json.Unmarshal(r.row.Measurements["observed_exec_child_pids"], &uniqueChildren) != nil || uniqueChildren <= 0 {
			t.Fatal("missing distinct exec-child PID census")
		}
		var u, v int
		_ = json.Unmarshal(r.row.Measurements["untraced_command"], &u)
		_ = json.Unmarshal(r.row.Measurements["traced_command"], &v)
		leftRaw, _ := os.ReadFile(r.row.Commands[u].Stdout.Path)
		rightRaw, _ := os.ReadFile(r.row.Commands[v].Stdout.Path)
		left, _ := decodeObject(leftRaw)
		right, _ := decodeObject(rightRaw)
		action := str(left["action"])
		if !sameSeparateQueryResult(action, obj(left["result"]), obj(right["result"])) {
			t.Fatal("stable actual result refused")
		}
		if action == "definition" {
			obj(obj(right["result"])["definition"])["title"] = "different portable content"
		} else {
			obj(right["result"])["prior_mutation"] = false
		}
		if sameSeparateQueryResult(action, obj(left["result"]), obj(right["result"])) {
			t.Fatal("different full traced semantics admitted")
		}
		t.Logf("%s successful exec classes=%v distinct successful-exec child PIDs=%d trace=%s", id, counts, uniqueChildren, trace.Path)
	}
	replay, err := r.performance.call(ctx, "define", r.performanceRequests[3], 0)
	if err != nil || replay["replayed"] != true || replay["prior_mutation"] != true {
		t.Fatalf("copied original recovery: %v %v", replay, err)
	}
	canonical, err := proof(obj(replay["observation"])["proof"], "original-operation/1")
	if err != nil || obj(canonical["event"])["issue_id"] != r.performanceRefs[3]["external_id"] {
		t.Fatal("recovery substituted template identity")
	}
}

func TestPublicLargeHeadsStagedResolution(t *testing.T) {
	if os.Getenv("GO_KITTY_TEST_LARGE_HEADS") != "1" {
		t.Skip("explicit large public fixture activation required")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	if base := os.Getenv("GO_KITTY_TEST_ARTIFACT_ROOT"); base != "" {
		evidence, err = os.MkdirTemp(base, "heads-")
		if err != nil {
			t.Fatal(err)
		}
		t.Log("retained artifacts", evidence)
	}
	r := newScenarioRunner(qualificationOptions{Repository: root, Product: os.Getenv("GO_KITTY_TEST_PRODUCT_BINARY"), HN: os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY"), EvidenceDir: evidence})
	defer r.close()
	defer saveHandlerProbe(t, r)
	r.row = &testcampaign.HubnotScenarioResult{Measurements: map[string]json.RawMessage{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err = r.headsScenario(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("201 actual actor heads, staged200→2→1; commands=%d", len(r.row.Commands))
}

func saveHandlerProbe(t *testing.T, r *scenarioRunner) {
	t.Helper()
	if r.row == nil {
		return
	}
	if len(r.row.Commands) > 0 {
		r.row.StartedAt = r.row.Commands[0].StartedAt
	}
	r.row.FinishedAt = time.Now().UTC()
	r.row.Verdict = "pass"
	if t.Failed() {
		r.row.Verdict = "fail"
	}
	raw, err := json.MarshalIndent(map[string]any{"schema": "go-kitty.hubnot-handler-probe/1", "qualification_evidence": false, "test": t.Name(), "row": r.row}, "", "  ")
	if err != nil {
		t.Error(err)
		return
	}
	if err = writeNew(filepath.Join(r.options.EvidenceDir, "handler-probe.json"), append(raw, '\n')); err != nil {
		t.Error(err)
	}
}

func TestPublicScenarioSupplementCensus(t *testing.T) {
	product, hn, goBinary := os.Getenv("GO_KITTY_TEST_PRODUCT_BINARY"), os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY"), os.Getenv("GO_KITTY_TEST_GO_BINARY")
	if product == "" || hn == "" || goBinary == "" {
		t.Skip("explicit real tools required")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range testcampaign.HubnotManifest().Scenarios {
		if scenario.Mode != testcampaign.HubnotInjected && scenario.ID != "bounds.public-input-output" && scenario.ID != "integration.compiled-dispatch" && scenario.ID != "integration.authority-boundaries" {
			continue
		}
		t.Run(scenario.ID, func(t *testing.T) {
			evidence := t.TempDir()
			if base := os.Getenv("GO_KITTY_TEST_ARTIFACT_ROOT"); base != "" {
				evidence, err = os.MkdirTemp(base, "supplement-")
				if err != nil {
					t.Fatal(err)
				}
				t.Log("retained artifacts", evidence)
			}
			r := newScenarioRunner(qualificationOptions{Repository: root, Product: product, HN: hn, GoBinary: goBinary, EvidenceDir: evidence})
			defer r.close()
			defer saveHandlerProbe(t, r)
			r.row = &testcampaign.HubnotScenarioResult{Scenario: scenario, Measurements: map[string]json.RawMessage{}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err = r.run(ctx, scenario); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPublicRecordedPreservationLeaves(t *testing.T) {
	original := os.Getenv("GO_KITTY_TEST_ORIGINAL_REPOSITORY")
	if original == "" {
		t.Skip("explicit actual original repository required")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cutover.frozen-source", "cutover.local-recovery"} {
		t.Run(id, func(t *testing.T) {
			evidence := t.TempDir()
			if base := os.Getenv("GO_KITTY_TEST_ARTIFACT_ROOT"); base != "" {
				evidence, err = os.MkdirTemp(base, "preservation-")
				if err != nil {
					t.Fatal(err)
				}
				t.Log("retained artifacts", evidence)
			}
			r := newScenarioRunner(qualificationOptions{Repository: root, OriginalRepository: original, EvidenceDir: evidence})
			defer saveHandlerProbe(t, r)
			r.row = &testcampaign.HubnotScenarioResult{Measurements: map[string]json.RawMessage{}}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err = r.preservationScenario(ctx, id); err != nil {
				t.Fatal(err)
			}
		})
	}
}
