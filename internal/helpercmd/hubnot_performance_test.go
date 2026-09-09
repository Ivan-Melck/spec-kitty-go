package helpercmd_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

func TestHubnotPerformanceTraceCensus(t *testing.T) {
	raw := []byte("10 execve(\"/product\", [\"/product\", \"tracker\", \"definition\"], 0x0 /* 0 vars */) = 0\n11 execve(\"/hn\", [\"/hn\", \"issue\", \"heads\"], 0x0 <unfinished ...>\n12 execve(\"/usr/bin/git\", [\"git\", \"show\"], 0x0) = 0\n11 <... execve resumed>) = 0\n11 execve(\"/hn\", [\"/hn\", \"identity\"], 0x0) = 0\n13 execve(\"/absent/git\", [\"git\"], 0x0) = -1 ENOENT (No such file)\n")
	got, err := auditExecTrace(raw, "/product", "/hn")
	if err != nil {
		t.Fatal(err)
	}
	if got.Counts["consumer"] != 1 || got.Counts["hubnot"] != 2 || got.Counts["git"] != 1 || got.Counts["failed_exec"] != 1 || got.Children != 2 {
		t.Fatalf("execs are not unique child PIDs: %+v", got)
	}
	for _, s := range []string{"11 execve(\"/hn\", [\"/hn\"], 0x0 <unfinished ...>\n", "11 <... execve resumed>) = 0\n", "11 execve(\"/hn\", [\"/hn\", ...], 0x0) = 0\n"} {
		if _, err := auditExecTrace([]byte(s), "/product", "/hn"); err == nil {
			t.Fatalf("accepted incomplete trace %q", s)
		}
	}
}

type auditTrace struct {
	Counts            map[string]int
	Children, Details int
}

func auditExecTrace(raw []byte, product, hn string) (auditTrace, error) {
	result := auditTrace{Counts: map[string]int{"consumer": 0, "hubnot": 0, "git": 0, "other": 0, "failed_exec": 0}}
	pending := map[string]string{}
	children, consumers := map[string]bool{}, map[string]bool{}
	lineRE := regexp.MustCompile(`^(\d+)\s+(.*)$`)
	execRE := regexp.MustCompile(`^execve\(("(?:\\.|[^"\\])*"), \[(.*?)\], .*\)\s+=\s+(.+)$`)
	stringRE := regexp.MustCompile(`"(?:\\.|[^"\\])*"`)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		m := lineRE.FindStringSubmatch(scanner.Text())
		if m == nil {
			if strings.Contains(scanner.Text(), "execve(") {
				return result, fmt.Errorf("unidentified exec PID")
			}
			continue
		}
		pid, line := m[1], m[2]
		if strings.HasPrefix(line, "execve(") && strings.Contains(line, "<unfinished ...>") {
			if pending[pid] != "" {
				return result, fmt.Errorf("duplicate pending exec")
			}
			pending[pid] = strings.Split(line, "<unfinished ...>")[0]
			continue
		}
		if strings.HasPrefix(line, "<... execve resumed>") {
			start, ok := pending[pid]
			if !ok {
				return result, fmt.Errorf("orphan resumed exec")
			}
			delete(pending, pid)
			line = start + strings.TrimPrefix(line, "<... execve resumed>")
		}
		if !strings.HasPrefix(line, "execve(") {
			continue
		}
		m = execRE.FindStringSubmatch(line)
		if m == nil {
			return result, fmt.Errorf("incomplete exec record")
		}
		path, e := strconv.Unquote(m[1])
		if e != nil {
			return result, e
		}
		var args []string
		for _, tok := range stringRE.FindAllString(m[2], -1) {
			v, e := strconv.Unquote(tok)
			if e != nil {
				return result, e
			}
			args = append(args, v)
		}
		rest := strings.TrimSpace(strings.ReplaceAll(stringRE.ReplaceAllString(m[2], ""), ",", ""))
		if rest != "" {
			return result, fmt.Errorf("truncated exec argv")
		}
		status := strings.Fields(m[3])
		if len(status) == 0 {
			return result, fmt.Errorf("missing exec status")
		}
		if status[0] != "0" {
			if status[0] != "-1" {
				return result, fmt.Errorf("unknown exec status")
			}
			result.Counts["failed_exec"]++
			continue
		}
		children[pid] = true
		kind := "other"
		switch {
		case path == product:
			kind = "consumer"
			consumers[pid] = true
		case path == hn:
			kind = "hubnot"
			if len(args) >= 4 && args[1] == "issue" && args[2] == "list" && args[3] == "--details" {
				result.Details++
			}
		case filepath.Base(path) == "git":
			kind = "git"
		}
		result.Counts[kind]++
	}
	if e := scanner.Err(); e != nil {
		return result, e
	}
	if len(pending) > 0 {
		return result, fmt.Errorf("unfinished exec")
	}
	for pid := range consumers {
		delete(children, pid)
	}
	result.Children = len(children)
	return result, nil
}
func auditMeasure(t testing.TB, r testcampaign.HubnotScenarioResult, key string) any {
	t.Helper()
	raw, ok := r.Measurements[key]
	if !ok {
		t.Fatal("missing measurement", key)
	}
	v := auditJSON(t, append(append([]byte(`{"v":`), raw...), '}'))
	if v["v"] == nil {
		t.Fatal("null measurement", key)
	}
	return v["v"]
}
func auditFloat(t testing.TB, v any) float64 {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatal("non-number measurement")
	}
	f, e := n.Float64()
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func auditIndex(t testing.TB, v any) int {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatal("non-integer measurement")
	}
	i, e := strconv.Atoi(string(n))
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func auditTraceArtifact(t testing.TB, r testcampaign.HubnotScenarioResult) testcampaign.HubnotArtifact {
	t.Helper()
	var ar testcampaign.HubnotArtifact
	if e := json.Unmarshal(r.Measurements["process_trace"], &ar); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, v := range r.Artifacts {
		if v == ar {
			found = true
		}
	}
	if !found {
		t.Fatal("trace not captured row artifact")
	}
	return ar
}
func auditQueryTimes(t testing.TB, r testcampaign.HubnotScenarioResult, c testcampaign.HubnotCommand) {
	t.Helper()
	elapsed := auditFloat(t, auditMeasure(t, r, "consumer_seconds"))
	command := c.FinishedAt.Sub(c.StartedAt).Seconds()
	if elapsed <= 0 || elapsed > 120 || command <= 0 || command > elapsed || elapsed-command > 1 {
		t.Fatalf("untraced timing incomplete: measurement=%f command=%f", elapsed, command)
	}
}
func auditStableResult(t testing.TB, v auditObject, action string) auditObject {
	t.Helper()
	out := auditCopy(v)
	p := out
	field := "proof"
	if action == "inventory" {
		field = "common_proof"
	} else if action == "define" {
		p = auditMap(t, out["observation"])
	}
	proof := auditMap(t, p[field])
	kind, id := "known-work-observation/1", "inventory:project"
	if action != "inventory" {
		id = auditRefID(t, p["external_ref"])
	}
	if action == "inventory" {
		kind, id = "complete-current-inventory/1", "inventory:project"
	} else if action == "define" {
		kind = "original-operation/1"
	}
	auditProofFor(t, proof, kind, id)
	stamp := auditString(t, proof["observed_at"])
	if _, e := time.Parse(time.RFC3339Nano, stamp); e != nil {
		t.Fatal(e)
	}
	if action != "inventory" {
		auditEqual(t, p["observed_at"], stamp, "same-invocation observation timestamp")
		delete(p, "observed_at")
	}
	delete(proof, "observed_at")
	return out
}
func auditSeparateQuery(a *publicAudit, id, action string) (auditObject, auditTrace) {
	t := a.t
	r := a.rows[id]
	uIndex := auditIndex(t, auditMeasure(t, r, "untraced_command"))
	tIndex := auditIndex(t, auditMeasure(t, r, "traced_command"))
	if uIndex < 0 || tIndex <= uIndex || tIndex >= len(r.Commands) {
		t.Fatal("query indices")
	}
	u, c := r.Commands[uIndex], r.Commands[tIndex]
	if a.action(u) != action || len(c.Argv) != 20 {
		t.Fatal("trace query shape")
	}
	trace := auditTraceArtifact(t, r)
	auditEqual(t, c.Argv[1:8], []string{"--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-o"}, "trace prefix")
	auditEqual(t, c.Argv[8], trace.Path, "trace path")
	auditEqual(t, c.Argv[9:], u.Argv, "traced original argv")
	auditEqual(t, u.Directory, c.Directory, "traced original directory")
	if u.Stdin == nil || c.Stdin == nil {
		t.Fatal("query input missing")
	}
	auditEqual(t, auditArtifact(t, *u.Stdin), auditArtifact(t, *c.Stdin), "traced original input")
	original := a.result(u, action)
	replayed := a.result(c, action)
	auditEqual(t, auditStableResult(t, replayed, action), auditStableResult(t, original, action), "full traced semantics")
	auditQueryTimes(t, r, u)
	if c.StartedAt.Before(u.FinishedAt) || auditFloat(t, auditMeasure(t, r, "traced_seconds")) < c.FinishedAt.Sub(c.StartedAt).Seconds() {
		t.Fatal("trace timing not separate")
	}
	counts, e := auditExecTrace(auditArtifact(t, trace), a.evidence.Source.Consumer.Path, a.evidence.Source.ProviderExecutable.Path)
	if e != nil {
		t.Fatal(e)
	}
	m := auditMap(t, auditMeasure(t, r, "exec_counts"))
	if e := auditRecordedCounts(counts, m, auditMeasure(t, r, "observed_exec_child_pids")); e != nil {
		t.Fatal(e)
	}
	a.noWrite(r, uIndex)
	a.noWrite(r, tIndex)
	if action == "define" {
		auditEqual(t, original["replayed"], true, "recovery replay")
		auditEqual(t, original["prior_mutation"], true, "recovery prior")
	}
	t.Logf("%s untraced=%vs traced=%vs successful execs=%v distinct successful-exec children=%d", id, auditMeasure(t, r, "consumer_seconds"), auditMeasure(t, r, "traced_seconds"), counts.Counts, counts.Children)
	return original, counts
}

type auditExpectedWork struct{ request, reference, state, opening, head auditObject }
type auditLineage struct {
	work     map[string]*auditExpectedWork
	byID     map[string]*auditExpectedWork
	comments map[string]auditObject
}

func auditPerformanceLineage(a *publicAudit, r testcampaign.HubnotScenarioResult, expectedCount int) *auditLineage {
	t := a.t
	l := &auditLineage{map[string]*auditExpectedWork{}, map[string]*auditExpectedWork{}, map[string]auditObject{}}
	var pending *auditExpectedWork
	for _, c := range r.Commands {
		switch {
		case a.action(c) == "define":
			if pending != nil {
				t.Fatal("unconsumed public template")
			}
			q := a.request(c)
			o := auditMap(t, a.result(c, "define")["observation"])
			event := auditMap(t, auditProofFor(t, o["proof"], "original-operation/1", auditRefID(t, o["external_ref"]))["event"])
			key := auditString(t, q["work_id"])
			if l.work[key] != nil {
				t.Fatal("duplicate public template")
			}
			pending = &auditExpectedWork{request: q, reference: auditMap(t, o["external_ref"]), state: auditMap(t, event["state"])}
			l.work[key] = pending
		case a.native(c, "open"):
			if pending == nil || c.Stdin == nil {
				t.Fatal("native opening without original public template")
			}
			state := auditJSON(t, auditArtifact(t, *c.Stdin))
			auditEqual(t, state, pending.state, "exact public opening copy")
			o := auditMap(t, auditJSON(t, auditArtifact(t, c.Stdout))["data"])
			key := auditString(t, o["issue_id"])
			if l.byID[key] != nil {
				t.Fatal("duplicate aggregate issue")
			}
			pending.reference = auditCopy(pending.reference)
			pending.reference["external_id"] = key
			pending.opening = auditMap(t, state["metadata"])
			actor := ""
			for i, v := range c.Argv {
				if v == "--actor" && i+1 < len(c.Argv) {
					actor = c.Argv[i+1]
				}
			}
			if actor == "" {
				t.Fatal("native opening not actor-pinned")
			}
			pending.head = auditObject{"id": o["event_id"], "actor": actor, "state": state}
			l.byID[key] = pending
			pending = nil
		case a.native(c, "heads") && len(c.Argv) > 3 && l.byID[c.Argv[3]] != nil:
			items := auditList(t, auditMap(t, auditJSON(t, auditArtifact(t, c.Stdout))["data"])["items"])
			if len(items) != 1 {
				t.Fatal("setup head conflict")
			}
			w := l.byID[c.Argv[3]]
			w.head = auditMap(t, items[0])
			w.state = auditMap(t, w.head["state"])
		case a.action(c) == "comment":
			q := a.request(c)
			entry := auditMap(t, a.result(c, "comment")["entry"])
			if e := auditCommentEntry(t, entry, q); e != nil {
				t.Fatal(e)
			}
			key := auditString(t, auditMap(t, q["external_ref"])["external_id"])
			if l.comments[key] != nil {
				t.Fatal("duplicate fixture comment")
			}
			l.comments[key] = entry
		}
	}
	if pending != nil || len(l.work) != expectedCount || len(l.byID) != expectedCount {
		t.Fatalf("public setup count %d/%d wanted %d", len(l.work), len(l.byID), expectedCount)
	}
	return l
}
func auditInventoryFields(a *publicAudit, l *auditLineage, result auditObject) {
	t := a.t
	proof := auditMap(t, result["common_proof"])
	canonical := auditProofFor(t, proof, "complete-current-inventory/1", "inventory:project")
	native := map[string]auditObject{}
	for _, v := range auditList(t, canonical["rows"]) {
		n := auditMap(t, v)
		key := auditString(t, n["id"])
		if native[key] != nil {
			t.Fatal("duplicate native inventory row")
		}
		native[key] = n
	}
	items := auditList(t, result["items"])
	if len(items) != len(l.work) || len(native) != len(l.byID) {
		t.Fatal("full inventory census differs")
	}
	seen := map[string]bool{}
	blockers := map[string][]any{}
	expectedEdges := map[string]bool{}
	// Derive current dependencies from independently captured native relations.
	for source, w := range l.byID {
		for _, v := range auditList(t, w.state["relations"]) {
			relation := auditMap(t, v)
			if relation["kind"] != "blocks" {
				continue
			}
			target := auditString(t, relation["target"])
			blocked := l.byID[target]
			if blocked == nil {
				t.Fatal("fixture edge target not mapped")
			}
			blockers[target] = append(blockers[target], w.request["work_id"])
			expectedEdges[source+":"+target] = true
		}
	}
	for _, v := range items {
		item := auditMap(t, v)
		d := auditMap(t, item["definition"])
		key := auditString(t, d["work_id"])
		w := l.work[key]
		if w == nil || seen[key] {
			t.Fatal("unknown/duplicate work")
		}
		seen[key] = true
		if e := auditDefinition(d, w.request); e != nil {
			t.Fatal(key, e)
		}
		ref := auditMap(t, item["external_ref"])
		for _, f := range []string{"project_id", "work_id", "provider", "external_id", "metadata_schema", "created_by_operation"} {
			auditEqual(t, ref[f], w.reference[f], "immutable mapping "+f)
		}
		auditEqual(t, ref["last_observation"], proof["revision"], "current reference revision")
		id := auditString(t, ref["external_id"])
		deps := blockers[id]
		if deps == nil {
			deps = []any{}
		}
		sort.Slice(deps, func(i, j int) bool { return deps[i].(string) < deps[j].(string) })
		auditEqual(t, d["dependencies"], deps, "current graph-derived dependencies")
		n := native[id]
		auditEqual(t, n["state"], w.state, "complete current native state")
		auditEqual(t, n["opening_metadata"], w.opening, "signed opening metadata")
		auditEqual(t, n["creator"], w.head["actor"], "fixture opening actor")
		auditEqual(t, n["head_count"], json.Number("1"), "head census")
		auditEqual(t, n["conflict"], false, "current conflict")
	}
	edges := auditList(t, canonical["edges"])
	if len(edges) != len(expectedEdges) {
		t.Fatal("global edge census")
	}
	for _, v := range edges {
		e := auditMap(t, v)
		key := auditString(t, e["source"]) + ":" + auditString(t, e["target"])
		if !expectedEdges[key] {
			t.Fatal("unexpected/duplicate global edge")
		}
		delete(expectedEdges, key)
	}
}
func TestHubnotIntegratedPerformanceEvidence(t *testing.T) {
	a := loadPublicAudit(t)
	r := a.rows["performance.inventory-1000"]
	l := auditPerformanceLineage(a, r, 1000)
	if len(l.comments) != 20 {
		t.Fatal("full discussion setup census")
	}
	criteria, blocks, related := 0, 0, 0
	for _, w := range l.work {
		criteria += len(auditList(t, w.request["criteria"]))
		if len(auditList(t, w.request["write_scopes"])) != 1 || len(auditList(t, w.request["provenance"])) != 1 {
			t.Fatal("full fixture rich scopes/provenance missing")
		}
		for _, v := range auditList(t, w.state["relations"]) {
			kind := auditMap(t, v)["kind"]
			if kind == "blocks" {
				blocks++
			}
			if kind == "related" {
				related++
			}
		}
	}
	if criteria != 2000 || blocks != 20 || related != 20 {
		t.Fatal("representative full fixture census", criteria, blocks, related)
	}
	direct := a.commands(r.Scenario.ID, "inventory")
	if len(direct) != 1 {
		t.Fatal("timed inventory command census")
	}
	inv := a.result(direct[0], "inventory")
	auditInventoryFields(a, l, inv)
	auditQueryTimes(t, r, direct[0])
	var traced *testcampaign.HubnotCommand
	for i, c := range r.Commands {
		if filepath.Base(c.Argv[0]) == "strace" {
			if traced != nil {
				t.Fatal("duplicate inventory trace")
			}
			traced = &r.Commands[i]
		}
	}
	if traced == nil {
		t.Fatal("missing inventory trace")
	}
	auditEqual(t, auditStableResult(t, a.result(*traced, "inventory"), "inventory"), auditStableResult(t, inv, "inventory"), "traced full inventory")
	trace := auditTraceArtifact(t, r)
	if len(traced.Argv) < 10 || traced.Argv[8] != trace.Path {
		t.Fatal("inventory trace artifact binding")
	}
	counts, e := auditExecTrace(auditArtifact(t, trace), a.evidence.Source.Consumer.Path, a.evidence.Source.ProviderExecutable.Path)
	if e != nil {
		t.Fatal(e)
	}
	if counts.Details != 125 {
		t.Fatal("inventory used incomplete detailed pages", counts.Details)
	}
	auditEqual(t, auditMeasure(t, r, "details_processes"), json.Number(strconv.Itoa(counts.Details)), "detailed exec count")
	auditEqual(t, auditMeasure(t, r, "git_processes"), json.Number(strconv.Itoa(counts.Counts["git"])), "Git exec count")
	histories := 0
	for _, c := range r.Commands {
		if a.native(c, "history") {
			out := auditJSON(t, auditArtifact(t, c.Stdout))
			items := auditList(t, auditMap(t, out["data"])["items"])
			want := l.comments[c.Argv[3]]
			if want == nil || len(items) != 1 || out["next_cursor"] != nil && out["next_cursor"] != "" {
				t.Fatal("native comment history census")
			}
			got := auditMap(t, items[0])
			for _, pair := range [][2]string{{"id", "external_comment_id"}, {"body", "body"}, {"timestamp", "created_at"}, {"actor", "signing_actor"}} {
				auditEqual(t, got[pair[0]], want[pair[1]], "native exact comment")
			}
			histories++
		}
	}
	if histories != 20 {
		t.Fatal("missing native comment preservation oracle")
	}
	known, _ := auditSeparateQuery(a, "performance.known-reference", "definition")
	kp := auditProofFor(t, known["proof"], "known-work-observation/1", auditRefID(t, l.work["performance-0000"].reference))
	w := l.work["performance-0000"]
	if e := auditDefinition(auditMap(t, known["definition"]), w.request); e != nil {
		t.Fatal(e)
	}
	auditEqual(t, kp["state"], w.state, "known current state")
	auditEqual(t, kp["source_heads"], auditObject{auditString(t, w.reference["external_id"]): []any{w.head}}, "known full signed heads")
	auditKnownFacts(t, kp, w, l.comments[auditString(t, w.reference["external_id"])])
	recovered, _ := auditSeparateQuery(a, "performance.original-recovery", "define")
	o := auditMap(t, recovered["observation"])
	rp := auditProofFor(t, o["proof"], "original-operation/1", auditRefID(t, o["external_ref"]))
	w = l.work["performance-0999"]
	auditEqual(t, auditMap(t, rp["event"])["issue_id"], w.reference["external_id"], "original recovered identity")
	auditEqual(t, auditMap(t, rp["event"])["state"], w.state, "original recovered full state")
	if e := auditDefinition(auditMap(t, o["definition"]), w.request); e != nil {
		t.Fatal(e)
	}
	t.Logf("full inventory1000 fields verified; detail execs=%d Git execs=%d untraced=%vs setup=%vs comparison=%vs output_bytes=%d", counts.Details, counts.Counts["git"], auditMeasure(t, r, "consumer_seconds"), auditMeasure(t, r, "setup_seconds"), auditMeasure(t, r, "comparison_seconds"), direct[0].Stdout.Bytes)
}

func auditRecordedCounts(actual auditTrace, recorded auditObject, child any) error {
	if len(recorded) != 5 || actual.Counts["consumer"] != 1 || actual.Counts["hubnot"] < 1 || actual.Counts["git"] < 1 || actual.Children < 1 {
		return fmt.Errorf("incomplete exec census")
	}
	for k, n := range actual.Counts {
		v, ok := recorded[k].(json.Number)
		if !ok {
			return fmt.Errorf("non-integer count %s", k)
		}
		i, e := strconv.Atoi(string(v))
		if e != nil || i != n || i < 0 {
			return fmt.Errorf("exec count %s differs", k)
		}
	}
	v, ok := child.(json.Number)
	if !ok {
		return fmt.Errorf("non-integer child PID count")
	}
	i, e := strconv.Atoi(string(v))
	if e != nil || i != actual.Children {
		return fmt.Errorf("child PID count differs")
	}
	return nil
}
func TestHubnotPerformanceRecordedCountMutants(t *testing.T) {
	counts := auditTrace{Counts: map[string]int{"consumer": 1, "hubnot": 2, "git": 7, "other": 0, "failed_exec": 0}, Children: 9}
	good := auditObject{}
	for k, n := range counts.Counts {
		good[k] = json.Number(strconv.Itoa(n))
	}
	if e := auditRecordedCounts(counts, good, json.Number("9")); e != nil {
		t.Fatal(e)
	}
	for _, k := range []string{"consumer", "hubnot", "git", "other", "failed_exec"} {
		for _, v := range []any{nil, json.Number("0.0"), json.Number("-1"), "0"} {
			bad := auditCopy(good)
			bad[k] = v
			if auditRecordedCounts(counts, bad, json.Number("9")) == nil {
				t.Fatalf("accepted %s=%v", k, v)
			}
		}
	}
	for _, v := range []any{nil, "9", json.Number("9.0"), json.Number("10")} {
		if auditRecordedCounts(counts, good, v) == nil {
			t.Fatal("accepted false child PID count", v)
		}
	}
}

func auditKnownFacts(t testing.TB, canonical auditObject, w *auditExpectedWork, entry auditObject) {
	t.Helper()
	relations := auditList(t, w.state["relations"])
	expected := map[string]bool{}
	for _, v := range relations {
		e := auditMap(t, v)
		expected[auditString(t, e["kind"])+":"+auditString(t, e["target"])] = true
	}
	edges := auditList(t, canonical["graph"])
	if len(edges) != len(expected) {
		t.Fatal("known complete graph census")
	}
	for _, v := range edges {
		node := auditMap(t, v)
		auditEqual(t, node["type"], "edge", "known graph row type")
		e := auditMap(t, node["edge"])
		key := auditString(t, e["kind"]) + ":" + auditString(t, e["target"])
		if !expected[key] {
			t.Fatal("known unexpected/duplicate graph relation")
		}
		delete(expected, key)
		auditEqual(t, e["source"], w.reference["external_id"], "known edge source")
		auditEqual(t, e["actor"], w.head["actor"], "known current edge signer")
		auditEqual(t, e["revision"], w.head["id"], "known current edge head")
		auditEqual(t, e["ambiguous"], false, "known edge conflict")
	}
	comments := auditList(t, canonical["comments"])
	if len(comments) != 1 || entry == nil {
		t.Fatal("known complete discussion census")
	}
	event := auditMap(t, auditProofFor(t, entry["proof"], "original-operation/1", auditRefID(t, w.reference))["event"])
	want := auditObject{"id": event["event_id"]}
	for _, f := range []string{"actor", "body", "intent", "kind", "operation", "parents", "timestamp"} {
		want[f] = event[f]
	}
	auditEqual(t, comments[0], want, "known exact original comment facts")
}
