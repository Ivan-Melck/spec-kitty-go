package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
		var affinity map[string]any
		if json.Unmarshal(r.row.Measurements["trace_affinity"], &affinity) != nil || affinity["mode"] != "diagnostic-only-first-allowed-cpu/1" {
			t.Fatal("current separate trace lacks affinity mode")
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
		if strings.Contains(string(raw), "--- SIG") {
			t.Fatal("separate query signal notices not filtered")
		}
		var uniqueChildren int
		if json.Unmarshal(r.row.Measurements["observed_exec_child_pids"], &uniqueChildren) != nil || uniqueChildren <= 0 {
			t.Fatal("missing distinct exec-child PID census")
		}
		var u, v int
		_ = json.Unmarshal(r.row.Measurements["untraced_command"], &u)
		_ = json.Unmarshal(r.row.Measurements["traced_command"], &v)
		if !strings.Contains(strings.Join(r.row.Commands[v].Argv, " "), "-e signal=none") {
			t.Fatal("separate query signal filter absent")
		}
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

// Exercise the actual inventory tracer, including its failure evidence boundary.
func TestInventoryTraceRetainsFailureAndFiltersSignalNotices(t *testing.T) {
	trace, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("strace required")
	}
	for _, outcome := range []string{"success", "failure", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			dir := t.TempDir()
			child := filepath.Join(dir, "signal-child")
			script := "#!/usr/bin/python3\nimport os,signal,subprocess,sys,time,json\nseen=[]\nsignal.signal(signal.SIGUSR1,lambda *_:seen.append(1))\nfor _ in range(20):os.kill(os.getpid(),signal.SIGUSR1)\nsubprocess.run(['/usr/bin/true'],check=True)\nprint(json.dumps({\"signals\":len(seen),\"cpus\":sorted(os.sched_getaffinity(0))}),flush=True)\n" + map[string]string{"success": "sys.exit(0)", "failure": "sys.exit(7)", "canceled": "time.sleep(30)"}[outcome] + "\n"
			if err := os.WriteFile(child, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			r := newScenarioRunner(qualificationOptions{Product: child, EvidenceDir: dir})
			r.row = &testcampaign.HubnotScenarioResult{Measurements: map[string]json.RawMessage{}}
			f := &publicFixture{directory: dir, binding: "unused"}
			path := filepath.Join(dir, "inventory.execve.trace")
			timeout := 10 * time.Second
			if outcome == "canceled" {
				timeout = 300 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			out, err := r.traceInventory(ctx, f, trace, path)
			if outcome != "success" && err == nil || outcome == "success" && err != nil {
				t.Fatalf("outcome=%s error=%v", outcome, err)
			}
			if outcome == "canceled" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("original deadline lost: %v", err)
			}
			observed, decodeErr := decodeObject(out)
			var affinity map[string]any
			if json.Unmarshal(r.row.Measurements["trace_affinity"], &affinity) != nil || affinity["mode"] != "diagnostic-only-first-allowed-cpu/1" {
				t.Fatal("current affinity metadata missing")
			}
			if decodeErr != nil || fmt.Sprint(observed["signals"]) != "20" || len(list(observed["cpus"])) != 1 || fmt.Sprint(list(observed["cpus"])[0]) != fmt.Sprint(affinity["requested_cpu"]) {
				t.Fatalf("signal delivery or child affinity differs: %q", out)
			}
			var index int
			var seconds float64
			if json.Unmarshal(r.row.Measurements["traced_command"], &index) != nil || index != 0 || len(r.row.Commands) != 1 {
				t.Fatal("actual trace command index missing")
			}
			if json.Unmarshal(r.row.Measurements["traced_seconds"], &seconds) != nil || seconds <= 0 {
				t.Fatal("actual trace duration missing")
			}
			if _, present := r.row.Measurements["details_processes"]; present {
				t.Fatal("helper fabricated successful census")
			}
			var artifact testcampaign.HubnotArtifact
			if json.Unmarshal(r.row.Measurements["process_trace"], &artifact) != nil || artifact.Path != path || artifact.Bytes == 0 || len(r.row.Artifacts) != 1 {
				t.Fatal("trace artifact missing after command terminal")
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if artifact.SHA256 != shaBytes(raw) {
				t.Fatal("trace digest differs")
			}
			if strings.Contains(string(raw), "--- SIG") {
				t.Fatal("signal notices were not filtered")
			}
			records, err := parseExecTrace(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]int{}
			for _, record := range records {
				if record.Success {
					found[record.Path]++
				}
			}
			if found[child] != 1 || found["/usr/bin/true"] != 1 {
				t.Fatalf("actual execs lost: %v", found)
			}
		})
	}
}

func TestTraceAffinityMaskSelection(t *testing.T) {
	for _, test := range []struct {
		mask  string
		first int
	}{{"0", 0}, {"3-7,11,20-22", 3}, {"9", 9}, {"1024-2147483647", 1024}} {
		got, err := firstTraceCPU(test.mask)
		if err != nil || got != test.first {
			t.Fatalf("%s got%d: %v", test.mask, got, err)
		}
	}
	for _, mask := range []string{"", "0,", "-1", "+1", "01", "2-1", "0-2,2", "4,3", "1--2", "0 1", "2147483648", strings.Repeat("1", 4097)} {
		if _, err := firstTraceCPU(mask); err == nil {
			t.Fatalf("invalid parent mask %q admitted", mask)
		}
	}
}
func TestTraceAffinityMissingTracerRefusesWithoutExecution(t *testing.T) {
	dir := t.TempDir()
	r := newScenarioRunner(qualificationOptions{Product: "/usr/bin/true", EvidenceDir: dir})
	r.row = &testcampaign.HubnotScenarioResult{Measurements: map[string]json.RawMessage{}}
	_, err := r.traceInventory(context.Background(), &publicFixture{directory: dir}, filepath.Join(dir, "missing-strace"), filepath.Join(dir, "trace"))
	if err == nil || len(r.row.Commands) != 0 {
		t.Fatal("missing system tracer did not refuse before child execution")
	}
}
