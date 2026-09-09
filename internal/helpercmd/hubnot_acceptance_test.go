package helpercmd_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
	"testing"
)

// These are audit-boundary regressions, not fabricated passing provider receipts.
func TestHubnotAcceptanceFieldMutants(t *testing.T) {
	original := auditJSON(t, []byte(`{"work_id":"one","title":"title","description":"body","design_context":"context","cross_domain_reason":"reason","criteria":[{"id":"z","text":"first","outcome":"ok","verifier":"test","required":true,"evidence_hint":"e"},{"id":"a","text":"second","outcome":"done","verifier":"test","required":false,"evidence_hint":"f"}],"write_scopes":[{"path":"src","domain":"core","access":"write_exclusive","serialization_key":"serial"}],"provenance":[{"id":"R1","kind":"required_outcome"}],"metadata_schema":1,"provider_metadata_json":"{\"big\":9007199254740993,\"decimal\":1.2300}"}`))
	good := auditCopy(original)
	good["project_id"] = "project"
	if err := auditDefinition(good, original); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"criteria", "write_scopes", "provenance", "design_context", "cross_domain_reason", "provider_metadata_json"} {
		t.Run(field, func(t *testing.T) {
			bad := auditCopy(good)
			switch field {
			case "criteria":
				v := bad[field].([]any)
				v[0], v[1] = v[1], v[0]
			case "provider_metadata_json":
				bad[field] = `{"big":9007199254740992,"decimal":1.23}`
			default:
				delete(bad, field)
			}
			if auditDefinition(bad, original) == nil {
				t.Fatal("accepted changed full field", field)
			}
		})
	}
}
func TestHubnotAcceptanceJSONRejectsAmbiguousEvidence(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":[]} true`, `{"a":"\ud800"}`, string([]byte{'{', '"', 'a', '"', ':', '"', 255, '"', '}'})} {
		if _, err := decodeAuditJSON([]byte(raw)); err == nil {
			t.Fatalf("accepted ambiguous JSON %q", raw)
		}
	}
	v, err := decodeAuditJSON([]byte(`{"n":9007199254740993,"a":[1.2300]}`))
	if err != nil {
		t.Fatal(err)
	}
	if v["n"] != json.Number("9007199254740993") {
		t.Fatal("coerced number")
	}
}

type auditObject = map[string]any

const auditLimit int64 = (256 << 20) + 1

// Evidence is parsed independently: exact number lexemes and duplicate-member
// rejection are necessary before field equality has a useful meaning.
func decodeAuditJSON(raw []byte) (auditObject, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for i < len(raw) && raw[i] != '"' {
			if raw[i] == '\\' {
				i++
				if i < len(raw) && raw[i] == 'u' {
					if i+4 >= len(raw) {
						return nil, fmt.Errorf("short Unicode escape")
					}
					n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
					if err != nil {
						return nil, err
					}
					i += 4
					if n >= 0xd800 && n <= 0xdbff {
						if i+6 >= len(raw) || string(raw[i+1:i+3]) != "\\u" {
							return nil, fmt.Errorf("unpaired high surrogate")
						}
						m, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
						if err != nil || m < 0xdc00 || m > 0xdfff {
							return nil, fmt.Errorf("unpaired high surrogate")
						}
						i += 6
					} else if n >= 0xdc00 && n <= 0xdfff {
						return nil, fmt.Errorf("unpaired low surrogate")
					}
				}
			}
			i++
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) (any, error)
	value = func(depth int) (any, error) {
		if depth > 10000 {
			return nil, fmt.Errorf("JSON depth")
		}
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch tok {
		case json.Delim('{'):
			m := auditObject{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("object key")
				}
				if _, ok = m[key]; ok {
					return nil, fmt.Errorf("duplicate key %s", key)
				}
				v, err := value(depth + 1)
				if err != nil {
					return nil, err
				}
				m[key] = v
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("object end")
			}
			return m, nil
		case json.Delim('['):
			a := []any{}
			for d.More() {
				v, err := value(depth + 1)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("array end")
			}
			return a, nil
		default:
			if _, ok := tok.(json.Delim); ok {
				return nil, fmt.Errorf("unexpected delimiter")
			}
			return tok, nil
		}
	}
	v, err := value(0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	return m, nil
}
func auditJSON(t testing.TB, raw []byte) auditObject {
	t.Helper()
	v, e := decodeAuditJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func auditCopy(v auditObject) auditObject {
	raw, _ := json.Marshal(v)
	out, e := decodeAuditJSON(raw)
	if e != nil {
		panic(e)
	}
	return out
}
func auditMap(t testing.TB, v any) auditObject {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", v)
	}
	return m
}
func auditList(t testing.TB, v any) []any {
	t.Helper()
	a, ok := v.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", v)
	}
	return a
}
func auditString(t testing.TB, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string, got %T", v)
	}
	return s
}
func auditEqual(t testing.TB, a, b any, what string) {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("%s differs: got %.600v, want %.600v", what, a, b)
	}
}
func auditDefinition(got, want auditObject) error {
	for _, f := range []string{"work_id", "title", "description", "design_context", "cross_domain_reason", "criteria", "write_scopes", "provenance", "metadata_schema"} {
		g, gok := got[f]
		w, wok := want[f]
		if !gok || !wok || !reflect.DeepEqual(g, w) {
			return fmt.Errorf("definition %s differs", f)
		}
	}
	if got["project_id"] != "project" {
		return fmt.Errorf("definition project differs")
	}
	a, aok := got["provider_metadata_json"].(string)
	b, bok := want["provider_metadata_json"].(string)
	if !aok || !bok {
		return fmt.Errorf("opaque field missing")
	}
	x, e := decodeAuditJSON([]byte(a))
	if e != nil {
		return e
	}
	y, e := decodeAuditJSON([]byte(b))
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(x, y) {
		return fmt.Errorf("opaque fields differ")
	}
	return nil
}
func auditRead(path string, limit int64) ([]byte, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	s, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() || s.Size() > limit || s.Size() < 0 {
		return nil, fmt.Errorf("not a bounded regular artifact: %s", path)
	}
	raw, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(raw)) > limit || int64(len(raw)) != s.Size() {
		return nil, fmt.Errorf("artifact size changed: %s", path)
	}
	return raw, nil
}
func auditArtifact(t testing.TB, a testcampaign.HubnotArtifact) []byte {
	t.Helper()
	if a.Bytes < 0 || a.Bytes > auditLimit {
		t.Fatal("artifact bound")
	}
	raw, e := auditRead(a.Path, a.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != a.Bytes || hex.EncodeToString(sum[:]) != a.SHA256 {
		t.Fatal("artifact digest/size differs", a.Path)
	}
	return raw
}
func auditHex(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) != 64 {
		return false
	}
	raw, e := hex.DecodeString(s)
	return e == nil && len(raw) == 32 && strings.ToLower(s) == s
}
func auditNativeID(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "sha256:") && auditHex(strings.TrimPrefix(s, "sha256:"))
}
func validateAuditProof(p auditObject, wantKind, wantID string) (auditObject, error) {
	kind, ok := p["kind"].(string)
	if !ok || wantKind != "" && kind != wantKind {
		return nil, fmt.Errorf("unexpected proof kind")
	}
	switch kind {
	case "complete-current-inventory/1", "known-work-observation/1", "original-operation/1", "discussion-observation/1":
	default:
		return nil, fmt.Errorf("unknown proof kind %q", kind)
	}
	encoded, ok := p["canonical_fields_base64"].(string)
	if !ok {
		return nil, fmt.Errorf("proof payload missing")
	}
	raw, e := base64.StdEncoding.Strict().DecodeString(encoded)
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(raw)
	revision := binary.BigEndian.Uint64(sum[:8])
	if revision == 0 {
		revision = 1
	}
	if p["digest"] != hex.EncodeToString(sum[:]) || p["revision"] != json.Number(strconv.FormatUint(revision, 10)) {
		return nil, fmt.Errorf("proof digest/revision differs")
	}
	c, e := decodeAuditJSON(raw)
	if e != nil {
		return nil, e
	}
	if c["kind"] != kind {
		return nil, fmt.Errorf("canonical kind differs")
	}
	stamp, ok := p["observed_at"].(string)
	if !ok {
		return nil, fmt.Errorf("proof time missing")
	}
	parsed, e := time.Parse(time.RFC3339Nano, stamp)
	if e != nil || parsed.IsZero() {
		return nil, fmt.Errorf("proof time invalid")
	}
	id, ok := p["external_id"].(string)
	if !ok || wantID != "" && id != wantID {
		return nil, fmt.Errorf("proof external context differs")
	}
	token, ok := p["provider_token"].(string)
	if !ok || token == "" {
		return nil, fmt.Errorf("proof provider token missing")
	}
	switch kind {
	case "complete-current-inventory/1":
		if id != "inventory:project" || c["project"] != "project" || !auditNativeID(token) || c["snapshot"] != token {
			return nil, fmt.Errorf("inventory context differs")
		}
	case "known-work-observation/1":
		if !auditNativeID(id) || !auditNativeID(token) || c["snapshot"] != token || !auditHex(c["inventory_digest"]) {
			return nil, fmt.Errorf("known work identity/snapshot differs")
		}
	case "original-operation/1":
		event, ok := c["event"].(map[string]any)
		if !ok || !auditNativeID(id) || event["issue_id"] != id || !auditNativeID(event["event_id"]) || token != "original-operation:"+event["event_id"].(string) {
			return nil, fmt.Errorf("original issue/event/token differs")
		}
	case "discussion-observation/1":
		if !auditNativeID(id) || !auditNativeID(token) || !auditHex(c["work_digest"]) {
			return nil, fmt.Errorf("discussion context differs")
		}
		if _, ok := c["entries"].([]any); !ok {
			return nil, fmt.Errorf("discussion entries missing")
		}
		if _, ok := c["children"].([]any); !ok {
			return nil, fmt.Errorf("discussion children missing")
		}
	}
	return c, nil
}

// Generic validation is closed over the four supported kinds. Actual consumer
// audit sites additionally bind the expected kind and work/inventory identity.
func auditProof(t testing.TB, v any) auditObject {
	t.Helper()
	c, e := validateAuditProof(auditMap(t, v), "", "")
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func auditProofFor(t testing.TB, v any, kind, id string) auditObject {
	t.Helper()
	if kind == "" || id == "" {
		t.Fatal("proof context required")
	}
	c, e := validateAuditProof(auditMap(t, v), kind, id)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func auditRefID(t testing.TB, v any) string {
	t.Helper()
	return auditString(t, auditMap(t, v)["external_id"])
}
func auditEffectID(t testing.TB, request, result auditObject) string {
	t.Helper()
	if ref, ok := request["external_ref"]; ok {
		return auditRefID(t, ref)
	}
	return auditRefID(t, result["external_ref"])
}

type publicAudit struct {
	t        *testing.T
	evidence testcampaign.HubnotEvidence
	rows     map[string]testcampaign.HubnotScenarioResult
}

func loadPublicAudit(t *testing.T) *publicAudit {
	t.Helper()
	enabled := os.Getenv("GO_KITTY_HUBNOT_ACCEPTANCE")
	if enabled == "" {
		t.Skip("external full-local receipt audit disabled; enable explicitly after normal qualification")
	}
	if enabled != "1" {
		t.Fatal("GO_KITTY_HUBNOT_ACCEPTANCE must be1")
	}
	path := os.Getenv("GO_KITTY_HUBNOT_ACCEPTANCE_RECEIPT")
	commit := os.Getenv("GO_KITTY_HUBNOT_ACCEPTANCE_COMMIT")
	if path == "" || commit == "" {
		t.Fatal("explicit receipt and commit required")
	}
	raw, e := auditRead(path, auditLimit)
	if e != nil {
		t.Fatal(e)
	}
	auditJSON(t, raw)
	var ev testcampaign.HubnotEvidence
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&ev); e != nil {
		t.Fatal(e)
	}
	for _, ref := range []struct{ name, want string }{{"HEAD", commit}, {"HEAD^{tree}", ev.Source.Tree}} {
		b, e := exec.Command("/usr/bin/git", "rev-parse", ref.name).Output()
		if e != nil || strings.TrimSpace(string(b)) != ref.want {
			t.Fatal("receipt does not match audit source", ref.name, e)
		}
	}
	b, e := exec.Command("/usr/bin/git", "status", "--porcelain").Output()
	if e != nil || len(b) != 0 {
		t.Fatal("audit source must be clean", e, string(b))
	}
	if ev.Source.Commit != commit {
		t.Fatal("receipt source differs")
	}
	if e = testcampaign.ValidateHubnotEvidence(ev); e != nil {
		t.Fatal(e)
	}
	if e = testcampaign.VerifyHubnotArtifacts(ev); e != nil {
		t.Fatal(e)
	}
	a := &publicAudit{t: t, evidence: ev, rows: map[string]testcampaign.HubnotScenarioResult{}}
	for _, r := range ev.Results {
		a.rows[r.Scenario.ID] = r
		for _, u := range r.UnitCensus {
			raw := auditArtifact(t, r.Commands[u.Command].Stdout)
			if _, e = testcampaign.ValidateHubnotUnitOutput(raw, u.Package, u.Tests); e != nil {
				t.Fatal(e)
			}
		}
	}
	t.Logf("normal full-local source=%s tree=%s rows=%d receipt=%s", commit, ev.Source.Tree, len(ev.Results), path)
	return a
}
func (a *publicAudit) action(c testcampaign.HubnotCommand) string {
	if (len(c.Argv) == 11 || len(c.Argv) == 9) && c.Argv[0] == a.evidence.Source.Consumer.Path && c.Argv[5] == "tracker" {
		return c.Argv[6]
	}
	return ""
}
func (a *publicAudit) request(c testcampaign.HubnotCommand) auditObject {
	a.t.Helper()
	if c.Stdin == nil {
		a.t.Fatal("request input missing")
	}
	return auditMap(a.t, auditJSON(a.t, auditArtifact(a.t, *c.Stdin))["data"])
}
func (a *publicAudit) result(c testcampaign.HubnotCommand, action string) auditObject {
	a.t.Helper()
	v := auditJSON(a.t, auditArtifact(a.t, c.Stdout))
	auditEqual(a.t, v["schema"], "go-kitty.tracker-result/1", "envelope schema")
	auditEqual(a.t, v["action"], action, "envelope action")
	auditEqual(a.t, v["outcome"], "success", "envelope outcome")
	if c.Exit != 0 {
		a.t.Fatal("successful result has failed command")
	}
	return auditMap(a.t, v["result"])
}
func (a *publicAudit) commands(id, action string) []testcampaign.HubnotCommand {
	var out []testcampaign.HubnotCommand
	for _, c := range a.rows[id].Commands {
		if a.action(c) == action && c.Exit == 0 {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		a.t.Fatal("missing public action", id, action)
	}
	return out
}
func (a *publicAudit) native(c testcampaign.HubnotCommand, action string) bool {
	return len(c.Argv) >= 3 && c.Argv[0] == a.evidence.Source.ProviderExecutable.Path && c.Argv[1] == "issue" && c.Argv[2] == action
}
func auditSensor(c testcampaign.HubnotCommand) bool {
	return len(c.Argv) == 3 && filepath.Base(c.Argv[0]) == "git" && c.Argv[1] == "for-each-ref" && c.Argv[2] == "--format=%(refname) %(objectname)" && c.Exit == 0
}
func (a *publicAudit) noWrite(r testcampaign.HubnotScenarioResult, index int) {
	a.t.Helper()
	before, after := -1, -1
	for i := index - 1; i >= 0; i-- {
		if auditSensor(r.Commands[i]) {
			before = i
			break
		}
	}
	for i := index + 1; i < len(r.Commands); i++ {
		if auditSensor(r.Commands[i]) {
			after = i
			break
		}
	}
	if before < 0 || after < 0 {
		a.t.Fatal("no successful bracketing ref sensor", r.Scenario.ID, index)
	}
	b, c := r.Commands[before], r.Commands[after]
	if e := auditRefPair(b, r.Commands[index], c, auditArtifact(a.t, b.Stdout), auditArtifact(a.t, c.Stdout)); e != nil {
		a.t.Fatal(e)
	}
}

func TestHubnotIntegratedPublicAcceptance(t *testing.T) {
	a := loadPublicAudit(t)
	auditPublicLifecycle(a)
	auditPublicRecovery(a)
	auditPublicDiscussion(a)
	auditPublicReplicas(a)
	auditPublicHeads(a)
}

func auditPublicLifecycle(a *publicAudit) {
	t := a.t
	for _, id := range []string{"work.rich-roundtrip", "graph.lifecycle"} {
		definitions := map[string]auditObject{}
		for _, c := range a.commands(id, "define") {
			q := a.request(c)
			o := auditMap(t, a.result(c, "define")["observation"])
			if e := auditDefinition(auditMap(t, o["definition"]), q); e != nil {
				t.Fatal(e)
			}
			auditProofFor(t, o["proof"], "original-operation/1", auditRefID(t, o["external_ref"]))
			definitions[auditString(t, q["work_id"])] = q
		}
		for _, c := range a.commands(id, "definition") {
			o := a.result(c, "definition")
			d := auditMap(t, o["definition"])
			q := definitions[auditString(t, d["work_id"])]
			if q == nil {
				t.Fatal("read has no original request")
			}
			if e := auditDefinition(d, q); e != nil {
				t.Fatal(e)
			}
			auditProofFor(t, o["proof"], "known-work-observation/1", auditRefID(t, a.request(c)["external_ref"]))
		}
	}
	rich := a.rows["work.rich-roundtrip"]
	var foreign auditObject
	for _, c := range rich.Commands {
		if a.native(c, "revise") {
			foreign = auditJSON(t, auditArtifact(t, *c.Stdin))
		}
	}
	read := a.result(a.commands(rich.Scenario.ID, "definition")[0], "definition")
	auditEqual(t, auditProofFor(t, read["proof"], "known-work-observation/1", auditRefID(t, a.request(a.commands(rich.Scenario.ID, "definition")[0])["external_ref"]))["state"], foreign, "rich current full native state")
	r := a.rows["work.simple-update-guards"]
	q := a.request(a.commands(r.Scenario.ID, "update")[0])
	changed := auditMap(t, a.result(a.commands(r.Scenario.ID, "update")[0], "update")["record"])
	for _, f := range []string{"title", "description", "design_context", "criteria"} {
		auditEqual(t, changed[f], q[f], "simple update "+f)
	}
	var before, after auditObject
	for _, c := range r.Commands {
		if a.native(c, "revise") {
			before = auditJSON(t, auditArtifact(t, *c.Stdin))
		}
		if a.native(c, "heads") && before != nil {
			items := auditList(t, auditMap(t, auditJSON(t, auditArtifact(t, c.Stdout))["data"])["items"])
			after = auditMap(t, auditMap(t, items[0])["state"])
		}
	}
	for _, f := range []string{"labels", "assignees", "relations", "status"} {
		auditEqual(t, after[f], before[f], "simple native "+f)
	}
	bm, am := auditMap(t, before["metadata"]), auditMap(t, after["metadata"])
	for k, v := range bm {
		if !strings.HasPrefix(k, "go-kitty/") {
			auditEqual(t, am[k], v, "foreign metadata "+k)
		}
	}
	graph := a.rows["graph.lifecycle"]
	links := a.commands(graph.Scenario.ID, "link")
	edge := a.result(links[0], "link")["dependency"]
	request := a.request(links[0])
	for _, f := range []string{"blocked_work_id", "blocking_work_id"} {
		auditEqual(t, auditMap(t, edge)[f], request[f], "edge direction")
	}
	auditEqual(t, a.result(a.commands(graph.Scenario.ID, "dependencies")[0], "dependencies")["items"], []any{edge}, "listed edge")
	auditEqual(t, a.result(a.commands(graph.Scenario.ID, "unlink")[0], "unlink")["dependency"], edge, "stable removed relation")
	ready := a.commands(graph.Scenario.ID, "ready")
	if len(ready) != 3 {
		t.Fatal("ready census")
	}
	for i, w := range [][]any{{"supplier"}, {"target"}, {"supplier", "target"}} {
		v := a.result(ready[i], "ready")
		auditEqual(t, v["provider_ready_ids"], w, "ready WorkIDs")
		auditEqual(t, v["authority"], "diagnostic_only", "ready authority")
	}
	assignments := a.commands("assignment.provenance", "assign")
	if len(assignments) != 4 {
		t.Fatal("assignment observation census")
	}
	for i, c := range assignments {
		v := auditMap(t, a.result(c, "assign")["observation"])
		want := "assignment"
		if i == 3 {
			want = ""
		}
		auditEqual(t, v["operation_marker"], want, "assignment ancestry attribution")
	}
	for _, id := range []string{"binding.admitted", "binding.refusals", "work.rich-roundtrip", "work.simple-update-guards", "graph.invalid-suppliers", "assignment.provenance", "discussion.attribution", "operation.key-attempt-refusal", "bounds.public-input-output"} {
		row := a.rows[id]
		for i, c := range row.Commands {
			if c.Exit != 0 && c.Argv[0] == a.evidence.Source.Consumer.Path {
				e := auditJSON(t, auditArtifact(t, c.Stdout))
				if e["outcome"] == "success" || e["outcome"] == nil {
					t.Fatal("refusal lacks typed outcome", id)
				}
				a.noWrite(row, i)
			}
		}
	}
}
func auditPublicRecovery(a *publicAudit) {
	t := a.t
	for _, s := range []struct{ id, action, field string }{{"operation.historical-replay", "create", "record"}, {"graph.partial-definition", "define", "observation"}, {"discussion.partial-pair", "comment", "entry"}} {
		r := a.rows[s.id]
		cs := a.commands(s.id, s.action)
		if len(cs) < 2 {
			t.Fatal("recovery command census")
		}
		var last auditObject
		replays := 0
		for _, c := range cs {
			v := a.result(c, s.action)
			p := auditMap(t, v[s.field])
			auditProofFor(t, p["proof"], "original-operation/1", auditEffectID(t, a.request(c), p))
			if v["replayed"] == true {
				replays++
				if last != nil {
					auditEqual(t, p, last, "original recovered effect")
				}
				for i, other := range r.Commands {
					if other.Stdout.Path == c.Stdout.Path {
						a.noWrite(r, i)
					}
				}
			}
			last = p
		}
		if replays == 0 {
			t.Fatal("no historical replay", s.id)
		}
		if s.id != "operation.historical-replay" {
			prior := false
			for _, c := range cs {
				if a.result(c, s.action)["prior_mutation"] == true {
					prior = true
				}
			}
			if !prior {
				t.Fatal("persisted partial evidence missing")
			}
		}
	}
	r := a.rows["graph.partial-definition"]
	reads := a.commands(r.Scenario.ID, "definition")
	auditEqual(t, auditMap(t, a.result(reads[len(reads)-1], "definition")["definition"])["dependencies"], []any{}, "completed replay did not relink human-removed edge")
}
func auditCommentEntry(t testing.TB, entry, request auditObject) error {
	for _, pair := range [][2]string{{"body", "body"}, {"author", "author"}, {"operation_marker", "operation_id"}} {
		if !reflect.DeepEqual(entry[pair[0]], request[pair[1]]) {
			return fmt.Errorf("comment %s differs", pair[0])
		}
	}
	if entry["signing_actor"] == nil || entry["external_comment_id"] == nil || entry["created_at"] == nil {
		return fmt.Errorf("comment provenance missing")
	}
	return nil
}
func auditPublicDiscussion(a *publicAudit) {
	t := a.t
	id := "discussion.attribution"
	cs := a.commands(id, "comment")
	if len(cs) != 3 {
		t.Fatal("same-author two-signer/replay census")
	}
	entries := map[string]auditObject{}
	signers := map[string]bool{}
	for _, c := range cs {
		q := a.request(c)
		e := auditMap(t, a.result(c, "comment")["entry"])
		if err := auditCommentEntry(t, e, q); err != nil {
			t.Fatal(err)
		}
		p := auditProofFor(t, e["proof"], "original-operation/1", auditRefID(t, q["external_ref"]))
		event := auditMap(t, p["event"])
		for _, pair := range [][2]string{{"body", "body"}, {"actor", "signing_actor"}, {"timestamp", "created_at"}, {"event_id", "external_comment_id"}} {
			auditEqual(t, event[pair[0]], e[pair[1]], "original comment "+pair[0])
		}
		entries[auditString(t, e["external_comment_id"])] = e
		signers[auditString(t, e["signing_actor"])] = true
	}
	if len(entries) != 2 || len(signers) != 2 {
		t.Fatal("author conflated with signer")
	}
	auditEqual(t, a.request(cs[0])["author"], a.request(cs[1])["author"], "same accountable author")
	var native auditObject
	for _, c := range a.rows[id].Commands {
		if a.native(c, "operation") && c.Exit == 0 {
			native = auditMap(t, auditJSON(t, auditArtifact(t, c.Stdout))["data"])
		}
	}
	observed := a.result(a.commands(id, "discussion")[0], "discussion")
	common := auditMap(t, observed["common_proof"])
	can := auditProofFor(t, common, "discussion-observation/1", auditRefID(t, a.request(a.commands(id, "discussion")[0])["external_ref"]))
	current := auditMap(t, observed["current_work_observation"])
	auditProofFor(t, current, "known-work-observation/1", auditRefID(t, a.request(a.commands(id, "discussion")[0])["external_ref"]))
	auditEqual(t, can["work_digest"], current["digest"], "discussion/current-work proof separation")
	auditEqual(t, common["provider_token"], current["provider_token"], "coherent discussion token")
	seen := map[string]bool{}
	for _, v := range auditList(t, observed["entries"]) {
		e := auditMap(t, v)
		key := auditString(t, e["external_comment_id"])
		if seen[key] {
			t.Fatal("duplicate discussion entry")
		}
		seen[key] = true
		if w := entries[key]; w != nil {
			for _, f := range []string{"body", "author", "created_at", "signing_actor", "operation_marker"} {
				auditEqual(t, e[f], w[f], "observed managed "+f)
			}
		} else {
			for _, pair := range [][2]string{{"external_comment_id", "event_id"}, {"body", "body"}, {"created_at", "timestamp"}, {"signing_actor", "actor"}, {"author", "actor"}, {"operation_marker", "operation"}} {
				auditEqual(t, e[pair[0]], native[pair[1]], "native comment "+pair[0])
			}
		}
		ref := auditMap(t, e["proof_ref"])
		for _, f := range []string{"kind", "digest", "revision"} {
			auditEqual(t, ref[f], common[f], "shared proof "+f)
		}
		if _, ok := e["proof"]; ok {
			t.Fatal("entry duplicates common proof")
		}
	}
	if len(seen) != 3 {
		t.Fatal("discussion census")
	}
}
func auditPublicReplicas(a *publicAudit) {
	t := a.t
	for _, id := range []string{"replicas.selected-withheld", "replicas.reconstruction"} {
		r := a.rows[id]
		defs := map[string]auditObject{}
		for _, c := range a.commands(id, "define") {
			q := a.request(c)
			defs[auditString(t, q["work_id"])] = q
		}
		for _, c := range a.commands(id, "definition") {
			v := a.result(c, "definition")
			d := auditMap(t, v["definition"])
			if err := auditDefinition(d, defs[auditString(t, d["work_id"])]); err != nil {
				t.Fatal(err)
			}
			auditProofFor(t, v["proof"], "known-work-observation/1", auditRefID(t, a.request(c)["external_ref"]))
		}
		inv := a.result(a.commands(id, "inventory")[0], "inventory")
		if len(auditList(t, inv["items"])) != 2 {
			t.Fatal("reconstructed inventory incomplete")
		}
		for _, v := range auditList(t, inv["items"]) {
			d := auditMap(t, auditMap(t, v)["definition"])
			if e := auditDefinition(d, defs[auditString(t, d["work_id"])]); e != nil {
				t.Fatal(e)
			}
		}
		auditProofFor(t, inv["common_proof"], "complete-current-inventory/1", "inventory:project")
		for i, c := range r.Commands {
			if c.Exit == 1 && len(c.Argv) > 1 && c.Argv[0] == a.evidence.Source.ProviderExecutable.Path && c.Argv[1] == "sync" {
				a.noWrite(r, i)
			}
		}
	}
}

func TestHubnotAcceptanceCommentAttributionMutants(t *testing.T) {
	q := auditObject{"body": "exact\n雪\n", "author": "accountable", "operation_id": "op"}
	entry := auditObject{"body": q["body"], "author": q["author"], "operation_marker": "op", "signing_actor": "signer", "external_comment_id": "comment", "created_at": "2026-09-09T00:00:00Z"}
	if e := auditCommentEntry(t, entry, q); e != nil {
		t.Fatal(e)
	}
	for _, f := range []string{"body", "author", "operation_marker", "signing_actor", "created_at"} {
		bad := auditCopy(entry)
		delete(bad, f)
		if auditCommentEntry(t, bad, q) == nil {
			t.Fatal("missing attribution accepted", f)
		}
	}
}

func auditRefPair(before, operation, after testcampaign.HubnotCommand, left, right []byte) error {
	if !auditSensor(before) || !auditSensor(after) || before.Directory != operation.Directory || after.Directory != operation.Directory || operation.Directory == "" {
		return fmt.Errorf("ref sensors do not observe affected operation repository")
	}
	if before.StartedAt.IsZero() || operation.StartedAt.IsZero() || after.StartedAt.IsZero() || before.FinishedAt.Before(before.StartedAt) || operation.FinishedAt.Before(operation.StartedAt) || after.FinishedAt.Before(after.StartedAt) || before.FinishedAt.After(operation.StartedAt) || after.StartedAt.Before(operation.FinishedAt) {
		return fmt.Errorf("ref sensors do not bracket operation interval")
	}
	if !bytes.Equal(left, right) {
		return fmt.Errorf("signed refs changed")
	}
	return nil
}
func TestHubnotAcceptanceRefSensors(t *testing.T) {
	start := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	before := testcampaign.HubnotCommand{Argv: []string{"/usr/bin/git", "for-each-ref", "--format=%(refname) %(objectname)"}, Directory: "/fixture", StartedAt: start, FinishedAt: start.Add(time.Second)}
	operation := testcampaign.HubnotCommand{Directory: "/fixture", StartedAt: start.Add(2 * time.Second), FinishedAt: start.Add(3 * time.Second)}
	after := before
	after.StartedAt = start.Add(4 * time.Second)
	after.FinishedAt = start.Add(5 * time.Second)
	raw := []byte("refs/actors/a abc\n")
	if e := auditRefPair(before, operation, after, raw, raw); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"failed", "unrelated-both", "wrong-after", "late-before", "early-after", "unknown-time", "changed"} {
		t.Run(name, func(t *testing.T) {
			b, a, op := before, after, operation
			right := raw
			switch name {
			case "failed":
				b.Exit = 1
			case "unrelated-both":
				b.Directory = "/other"
				a.Directory = "/other"
			case "wrong-after":
				a.Directory = "/other"
			case "late-before":
				b.FinishedAt = op.FinishedAt
			case "early-after":
				a.StartedAt = op.StartedAt
			case "unknown-time":
				op.StartedAt = time.Time{}
			case "changed":
				right = []byte("changed")
			}
			if auditRefPair(b, op, a, raw, right) == nil {
				t.Fatal("accepted invalid no-write witness")
			}
		})
	}
}
func TestHubnotAcceptanceArtifactBounds(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := auditRead(fifo, 64); e == nil {
		t.Fatal("nonregular artifact accepted")
	}
	path := filepath.Join(dir, "artifact")
	if e := os.WriteFile(path, []byte("12345"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := auditRead(path, 4); e == nil {
		t.Fatal("oversized artifact accepted")
	}
	link := filepath.Join(dir, "link")
	if e := os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e := auditRead(link, 5); e == nil {
		t.Fatal("symlink artifact accepted")
	}
}
func TestHubnotAcceptanceDeepOpaquePrecision(t *testing.T) {
	opaque := `{"n":9007199254740993,"deep":` + strings.Repeat("[", 9997) + "1.2300" + strings.Repeat("]", 9997) + "}"
	v, e := decodeAuditJSON([]byte(opaque))
	if e != nil {
		t.Fatal(e)
	}
	auditEqual(t, v["n"], json.Number("9007199254740993"), "deep exact number")
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	other, e := decodeAuditJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	auditEqual(t, v, other, "deep opaque roundtrip")
}

func auditPublicHeads(a *publicAudit) {
	t := a.t
	r := a.rows["bounds.large-heads"]
	actors := map[string]bool{}
	var page, remaining auditObject
	resolves := 0
	for i, c := range r.Commands {
		if a.native(c, "revise") {
			for j, arg := range c.Argv {
				if arg == "--actor" && j+1 < len(c.Argv) {
					actors[c.Argv[j+1]] = true
				}
			}
		}
		if a.native(c, "heads") {
			o := auditJSON(t, auditArtifact(t, c.Stdout))
			items := auditList(t, auditMap(t, o["data"])["items"])
			if len(items) == 200 {
				page = o
			}
			if len(items) == 2 {
				remaining = o
			}
		}
		if a.native(c, "resolve") {
			want := page
			if resolves == 1 {
				want = remaining
			}
			if want == nil {
				t.Fatal("resolve missing prior complete head page")
			}
			expected := map[string]bool{}
			for _, v := range auditList(t, auditMap(t, want["data"])["items"]) {
				expected[auditString(t, auditMap(t, v)["id"])] = true
			}
			parents := 0
			partial := false
			for j, arg := range c.Argv {
				if arg == "--expect" && j+1 < len(c.Argv) {
					if !expected[c.Argv[j+1]] {
						t.Fatal("resolve consumes unselected/duplicate parent")
					}
					delete(expected, c.Argv[j+1])
					parents++
				}
				if arg == "--partial" {
					partial = true
				}
				if arg == "--snapshot" && j+1 < len(c.Argv) {
					auditEqual(t, c.Argv[j+1], want["snapshot"], "partial snapshot")
				}
			}
			if len(expected) != 0 || resolves == 0 && (parents != 200 || !partial) || resolves == 1 && (parents != 2 || partial) {
				t.Fatal("staged resolution parent census")
			}
			resolves++
		}
		if a.action(c) == "read" && c.Exit != 0 {
			a.noWrite(r, i)
		}
	}
	if len(actors) != 201 || resolves != 2 || page == nil || remaining == nil || page["next_cursor"] == nil || page["next_cursor"] == "" {
		t.Fatal("public201/200/2closure missing")
	}
}

func TestHubnotAcceptanceProofContextMutants(t *testing.T) {
	id := "sha256:" + strings.Repeat("a", 64)
	snapshot := "sha256:" + strings.Repeat("b", 64)
	makeProof := func(c auditObject, kind, id, token string) auditObject {
		raw, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		rev := binary.BigEndian.Uint64(sum[:8])
		if rev == 0 {
			rev = 1
		}
		return auditObject{"kind": kind, "external_id": id, "provider_token": token, "observed_at": "2026-09-09T00:00:00Z", "canonical_fields_base64": base64.StdEncoding.EncodeToString(raw), "digest": hex.EncodeToString(sum[:]), "revision": json.Number(strconv.FormatUint(rev, 10))}
	}
	known := auditObject{"kind": "known-work-observation/1", "snapshot": snapshot, "inventory_digest": strings.Repeat("c", 64)}
	good := makeProof(known, "known-work-observation/1", id, snapshot)
	auditProof(t, good)
	if _, e := validateAuditProof(good, "known-work-observation/1", id); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"unknown", "wrong-context-kind", "wrong-context-id", "missing-token", "missing-time", "bad-native-id", "bad-inventory-digest", "missing-snapshot", "wrong-snapshot", "discussion-missing-children"} {
		t.Run(name, func(t *testing.T) {
			p := auditCopy(good)
			kind, want := "known-work-observation/1", id
			switch name {
			case "unknown":
				p = makeProof(auditObject{"kind": "unknown-proof/99"}, "unknown-proof/99", id, snapshot)
				kind = ""
				want = ""
			case "wrong-context-kind":
				kind = "original-operation/1"
			case "wrong-context-id":
				want = "sha256:" + strings.Repeat("d", 64)
			case "missing-token":
				delete(p, "provider_token")
			case "missing-time":
				delete(p, "observed_at")
			case "bad-native-id":
				p["external_id"] = "sha256:short"
				want = ""
			case "bad-inventory-digest":
				c := auditCopy(known)
				c["inventory_digest"] = strings.Repeat("z", 64)
				p = makeProof(c, kind, id, snapshot)
			case "missing-snapshot":
				c := auditCopy(known)
				delete(c, "snapshot")
				p = makeProof(c, kind, id, snapshot)
			case "wrong-snapshot":
				c := auditCopy(known)
				c["snapshot"] = id
				p = makeProof(c, kind, id, snapshot)
			case "discussion-missing-children":
				kind = "discussion-observation/1"
				p = makeProof(auditObject{"kind": kind, "work_digest": strings.Repeat("c", 64), "entries": []any{}}, kind, id, snapshot)
			}
			if _, e := validateAuditProof(p, kind, want); e == nil {
				t.Fatal("accepted invalid proof context")
			}
		})
	}
}
