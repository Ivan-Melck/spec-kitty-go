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
	if a.action(u) != action {
		t.Fatal("trace query shape")
	}
	trace := auditTraceArtifact(t, r)
	suffix, err := auditObserverArgs(r, c, trace)
	if err != nil {
		t.Fatal(err)
	}
	auditEqual(t, suffix, u.Argv, "traced original argv")
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
	trace := auditTraceArtifact(t, r)
	tracedIndex, err := auditInventoryObserverIndex(r, trace)
	if err != nil {
		t.Fatal(err)
	}
	traced := r.Commands[tracedIndex]
	suffix, err := auditObserverArgs(r, traced, trace)
	if err != nil {
		t.Fatal(err)
	}
	if len(direct[0].Argv) != 9 {
		t.Fatal("normal inventory argv differs")
	}
	auditEqual(t, suffix, []string{a.evidence.Source.Consumer.Path, "--output", "json", "tracker", "inventory", "--binding", direct[0].Argv[8]}, "inventory trace original binding/argv")
	auditEqual(t, traced.Directory, direct[0].Directory, "inventory trace original directory")
	if traced.StartedAt.Before(direct[0].FinishedAt) {
		t.Fatal("inventory trace precedes untraced completion")
	}
	auditEqual(t, auditStableResult(t, a.result(traced, "inventory"), "inventory"), auditStableResult(t, inv, "inventory"), "traced full inventory")
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

// Independently interpret the three closed observer command forms. This oracle
// does not delegate command or affinity validation to the receipt validator.
func auditObserverArgs(r testcampaign.HubnotScenarioResult, c testcampaign.HubnotCommand, trace testcampaign.HubnotArtifact) ([]string, error) {
	argv := c.Argv
	if len(argv) == 0 {
		return nil, fmt.Errorf("observer argv missing")
	}
	metadata, present := r.Measurements["trace_affinity"]
	offset := 1
	if argv[0] == "/usr/bin/taskset" || argv[0] == "/bin/taskset" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(metadata, &fields) != nil || len(fields) != 6 {
			return nil, fmt.Errorf("observer affinity object required")
		}
		var mode, parent, path, digest string
		var selected *int
		var size *int64
		if json.Unmarshal(fields["mode"], &mode) != nil || mode != "diagnostic-only-first-allowed-cpu/1" || json.Unmarshal(fields["parent_allowed_cpus"], &parent) != nil || json.Unmarshal(fields["requested_cpu"], &selected) != nil || selected == nil || json.Unmarshal(fields["taskset_path"], &path) != nil || path != argv[0] || json.Unmarshal(fields["taskset_sha256"], &digest) != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) || json.Unmarshal(fields["taskset_bytes"], &size) != nil || size == nil || *size <= 0 {
			return nil, fmt.Errorf("observer affinity identity invalid")
		}
		if len(parent) > 4096 || !regexp.MustCompile(`^(0|[1-9][0-9]*)(-(0|[1-9][0-9]*))?(,(0|[1-9][0-9]*)(-(0|[1-9][0-9]*))?)*$`).MatchString(parent) {
			return nil, fmt.Errorf("observer parent mask invalid")
		}
		first, last := -1, -1
		for _, span := range strings.Split(parent, ",") {
			pair := strings.Split(span, "-")
			low, e := strconv.ParseInt(pair[0], 10, 32)
			if e != nil || low < 0 {
				return nil, fmt.Errorf("observer CPU invalid")
			}
			high := low
			if len(pair) == 2 {
				high, e = strconv.ParseInt(pair[1], 10, 32)
				if e != nil {
					return nil, fmt.Errorf("observer CPU invalid")
				}
			}
			if low <= int64(last) || high < low {
				return nil, fmt.Errorf("observer CPU order invalid")
			}
			if first < 0 {
				first = int(low)
			}
			last = int(high)
		}
		if first != *selected || len(argv) < 15 || argv[1] != "--cpu-list" || argv[2] != strconv.Itoa(first) || (argv[3] != "/usr/bin/strace" && argv[3] != "/bin/strace") {
			return nil, fmt.Errorf("observer selected CPU or launcher differs")
		}
		prefix := []string{"--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-e", "signal=none", "-o", trace.Path}
		for i, want := range prefix {
			if argv[4+i] != want {
				return nil, fmt.Errorf("affinity observer prefix differs")
			}
		}
		return argv[14:], nil
	}
	if present {
		return nil, fmt.Errorf("historical observer has affinity field")
	}
	if argv[0] != "/usr/bin/strace" && argv[0] != "/bin/strace" {
		return nil, fmt.Errorf("observer launcher unsupported")
	}
	for _, prefix := range [][]string{{"--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-o", trace.Path}, {"--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-e", "signal=none", "-o", trace.Path}} {
		if len(argv) <= offset+len(prefix) {
			continue
		}
		same := true
		for i, want := range prefix {
			if argv[offset+i] != want {
				same = false
				break
			}
		}
		if same {
			return argv[offset+len(prefix):], nil
		}
	}
	return nil, fmt.Errorf("historical observer prefix unsupported")
}
func auditInventoryObserverIndex(r testcampaign.HubnotScenarioResult, trace testcampaign.HubnotArtifact) (int, error) {
	if raw, present := r.Measurements["traced_command"]; present {
		var index *int
		if json.Unmarshal(raw, &index) != nil || index == nil || *index < 0 || *index >= len(r.Commands) {
			return 0, fmt.Errorf("observer index invalid")
		}
		if _, e := auditObserverArgs(r, r.Commands[*index], trace); e != nil {
			return 0, e
		}
		return *index, nil
	}
	if _, present := r.Measurements["trace_affinity"]; present {
		return 0, fmt.Errorf("current observer index required")
	}
	found := -1
	for i, c := range r.Commands {
		if len(c.Argv) == 0 {
			continue
		}
		if filepath.Base(c.Argv[0]) == "taskset" {
			return 0, fmt.Errorf("current observer index required")
		}
		if filepath.Base(c.Argv[0]) != "strace" {
			continue
		}
		if found != -1 {
			return 0, fmt.Errorf("historical observer ambiguous")
		}
		if _, e := auditObserverArgs(r, c, trace); e != nil {
			return 0, e
		}
		found = i
	}
	if found < 0 {
		return 0, fmt.Errorf("historical observer absent")
	}
	return found, nil
}

func TestHubnotObserverArgumentBoundaries(t *testing.T) {
	trace := testcampaign.HubnotArtifact{Path: "/evidence/trace", SHA256: strings.Repeat("a", 64), Bytes: 10}
	suffix := []string{"/product", "--output", "json", "tracker", "inventory", "--binding", "/binding"}
	for _, form := range []string{"old", "filtered", "affinity"} {
		t.Run(form, func(t *testing.T) {
			prefix := []string{"/usr/bin/strace", "--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve"}
			if form != "old" {
				prefix = append(prefix, "-e", "signal=none")
			}
			prefix = append(prefix, "-o", trace.Path)
			row := testcampaign.HubnotScenarioResult{Measurements: map[string]json.RawMessage{}}
			metadata := map[string]any{"mode": "diagnostic-only-first-allowed-cpu/1", "parent_allowed_cpus": "3-5,8", "requested_cpu": 3, "taskset_path": "/usr/bin/taskset", "taskset_sha256": strings.Repeat("a", 64), "taskset_bytes": 42}
			if form == "affinity" {
				prefix = append([]string{"/usr/bin/taskset", "--cpu-list", "3"}, prefix...)
				row.Measurements["trace_affinity"], _ = json.Marshal(metadata)
				row.Measurements["traced_command"] = json.RawMessage(`1`)
			}
			command := testcampaign.HubnotCommand{Argv: append(prefix, suffix...)}
			row.Commands = []testcampaign.HubnotCommand{{Argv: []string{"/unrelated"}}, command}
			got, err := auditObserverArgs(row, command, trace)
			if err != nil || strings.Join(got, "\x00") != strings.Join(suffix, "\x00") {
				t.Fatalf("valid %s refused: %v", form, err)
			}
			if index, err := auditInventoryObserverIndex(row, trace); err != nil || index != 1 {
				t.Fatalf("valid observer index refused: %d %v", index, err)
			}
			defects := []string{"launcher", "prefix", "affinity-null", "duplicate"}
			if form == "affinity" {
				defects = append(defects, "missing", "cpu-null", "cpu-wrong", "mask-overlap", "mask-malformed", "tool", "hash", "size-null", "extra-field", "index-null", "index-wrong", "index-absent")
			}
			for _, defect := range defects {
				t.Run(defect, func(t *testing.T) {
					changed := row
					changed.Measurements = map[string]json.RawMessage{}
					for k, v := range row.Measurements {
						changed.Measurements[k] = v
					}
					changed.Commands = append([]testcampaign.HubnotCommand(nil), row.Commands...)
					changed.Commands[1].Argv = append([]string(nil), command.Argv...)
					m := map[string]any{}
					for k, v := range metadata {
						m[k] = v
					}
					switch defect {
					case "launcher":
						changed.Commands[1].Argv[0] = "/usr/bin/env"
					case "prefix":
						changed.Commands[1].Argv[1] = "--unexpected"
					case "affinity-null":
						changed.Measurements["trace_affinity"] = json.RawMessage(`null`)
					case "duplicate":
						changed.Commands = append(changed.Commands, command)
						delete(changed.Measurements, "traced_command")
					case "missing":
						delete(changed.Measurements, "trace_affinity")
					case "cpu-null":
						m["requested_cpu"] = nil
					case "cpu-wrong":
						m["requested_cpu"] = 4
					case "mask-overlap":
						m["parent_allowed_cpus"] = "3-5,4"
					case "mask-malformed":
						m["parent_allowed_cpus"] = "03-5"
					case "tool":
						m["taskset_path"] = "/bin/taskset"
					case "hash":
						m["taskset_sha256"] = "missing"
					case "size-null":
						m["taskset_bytes"] = nil
					case "extra-field":
						m["observed_child_mask"] = "invented"
					case "index-null":
						changed.Measurements["traced_command"] = json.RawMessage(`null`)
					case "index-wrong":
						changed.Measurements["traced_command"] = json.RawMessage(`0`)
					case "index-absent":
						delete(changed.Measurements, "traced_command")
					}
					if strings.HasPrefix(defect, "cpu-") || strings.HasPrefix(defect, "mask-") || defect == "tool" || defect == "hash" || defect == "size-null" || defect == "extra-field" {
						changed.Measurements["trace_affinity"], _ = json.Marshal(m)
					}
					if _, err := auditInventoryObserverIndex(changed, trace); err == nil {
						t.Fatal("malformed observer/index admitted")
					}
				})
			}
		})
	}
}

func TestHubnotObserverZeroPresence(t *testing.T) {
	trace := testcampaign.HubnotArtifact{Path: "/trace", SHA256: strings.Repeat("a", 64), Bytes: 1}
	c := testcampaign.HubnotCommand{Argv: []string{"/usr/bin/taskset", "--cpu-list", "0", "/usr/bin/strace", "--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-e", "signal=none", "-o", "/trace", "/product"}}
	m := map[string]any{"mode": "diagnostic-only-first-allowed-cpu/1", "parent_allowed_cpus": "0,3-5", "requested_cpu": 0, "taskset_path": "/usr/bin/taskset", "taskset_sha256": strings.Repeat("a", 64), "taskset_bytes": 1}
	raw, _ := json.Marshal(m)
	r := testcampaign.HubnotScenarioResult{Commands: []testcampaign.HubnotCommand{c}, Measurements: map[string]json.RawMessage{"trace_affinity": raw, "traced_command": json.RawMessage(`0`)}}
	if i, e := auditInventoryObserverIndex(r, trace); e != nil || i != 0 {
		t.Fatalf("actual zero refused: %v", e)
	}
	for _, key := range []string{"requested_cpu", "traced_command"} {
		for _, missing := range []bool{false, true} {
			changed := r
			changed.Measurements = map[string]json.RawMessage{"trace_affinity": raw, "traced_command": json.RawMessage(`0`)}
			if key == "traced_command" {
				if missing {
					delete(changed.Measurements, key)
				} else {
					changed.Measurements[key] = json.RawMessage(`null`)
				}
			} else {
				values := map[string]any{}
				for k, v := range m {
					values[k] = v
				}
				if missing {
					delete(values, key)
				} else {
					values[key] = nil
				}
				changed.Measurements["trace_affinity"], _ = json.Marshal(values)
			}
			if _, e := auditInventoryObserverIndex(changed, trace); e == nil {
				t.Fatalf("%s missing=%v became zero", key, missing)
			}
		}
	}
}
