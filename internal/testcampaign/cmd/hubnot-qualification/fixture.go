package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
)

type object = map[string]any
type scenarioRunner struct {
	sourceCommit, sourceTree, runChallenge string
	options                                qualificationOptions
	row                                    *testcampaign.HubnotScenarioResult
	sequence                               int
	fixtures                               []string
	performance                            *publicFixture
	performanceRequests                    []object
	performanceRefs                        []object
	performanceStates                      []object
	performanceHeads                       []object
	performanceOpening                     []object
	performanceComments                    []object
}
type publicFixture struct {
	runner                    *scenarioRunner
	directory, binding, actor string
}

func newScenarioRunner(o qualificationOptions) *scenarioRunner { return &scenarioRunner{options: o} }
func (r *scenarioRunner) close() {
	for _, p := range r.fixtures {
		_ = os.RemoveAll(p)
	}
}
func (r *scenarioRunner) measure(key string, value any) {
	r.row.Measurements[key] = rawMeasurement(value)
}
func (r *scenarioRunner) command(ctx context.Context, dir string, input []byte, want int, program string, args ...string) ([]byte, error) {
	if !filepath.IsAbs(program) {
		p, err := exec.LookPath(program)
		if err != nil {
			return nil, prerequisite("tool %s unavailable", program)
		}
		program, err = filepath.Abs(p)
		if err != nil {
			return nil, err
		}
	}
	r.sequence++
	stem := fmt.Sprintf("%05d", r.sequence)
	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	c := exec.CommandContext(cctx, program, args...)
	c.Dir = dir
	c.Env = cleanFixtureEnvironment(dir)
	if program == r.options.GoBinary {
		c.Env = buildEnvironment()
		c.Env = append(c.Env, "GO_KITTY_TEST_HUBNOT_BINARY="+r.options.HN)
		for i, v := range c.Env {
			if strings.HasPrefix(v, "PATH=") {
				c.Env[i] = "PATH=" + filepath.Dir(r.options.GoBinary) + ":/usr/bin:/bin"
			}
		}
	}
	c.Stdin = bytes.NewReader(input)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.WaitDelay = time.Second
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	limit := 8 << 20
	if program == r.options.Product || slices.Contains(args, r.options.Product) {
		limit = consumerOutputLimit
	}
	// Prepare immutable launch intent and input before allowing a child to run.
	// Capture paths deliberately have no digest or exit until the command returns.
	// A killed worker leaves these files and no terminal record: incomplete evidence.
	record := testcampaign.HubnotCommand{Argv: append([]string{program}, args...), Directory: dir, ExpectedExit: want, StartedAt: time.Now().UTC()}
	if input != nil {
		path := filepath.Join(r.options.EvidenceDir, stem+".stdin")
		record.Stdin = &testcampaign.HubnotArtifact{Path: path, SHA256: shaBytes(input), Bytes: int64(len(input))}
		if err := writeNew(path, input); err != nil {
			return nil, err
		}
	}
	stdoutPath := filepath.Join(r.options.EvidenceDir, stem+".stdout")
	stderrPath := filepath.Join(r.options.EvidenceDir, stem+".stderr")
	intent := object{"schema": "go-kitty.hubnot-command-start/1", "phase": "prelaunch_intent", "run_directory": r.options.EvidenceDir, "run_challenge": r.runChallenge, "scope": r.options.Scope, "source_commit": r.sourceCommit, "source_tree": r.sourceTree, "scenario": r.row.Scenario.ID, "sequence": r.sequence, "argv": record.Argv, "directory": dir, "prepared_at": record.StartedAt, "expected_exit": want, "stdin": record.Stdin, "stdout_path": stdoutPath, "stderr_path": stderrPath, "stdout_limit": limit, "stderr_limit": 64 << 10}
	intentRaw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = writeNew(filepath.Join(r.options.EvidenceDir, stem+".start.json"), intentRaw); err != nil {
		return nil, err
	}
	outFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer outFile.Close()
	errFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer errFile.Close()
	stdout := &limitedOutput{limit: limit, overflow: cancel, sink: outFile}
	stderr := &limitedOutput{limit: 64 << 10, overflow: cancel, sink: errFile}
	c.Stdout, c.Stderr = stdout, stderr
	err = c.Start()
	phase := "launch_failed"
	var evidenceErr error
	if err == nil {
		// This proves successful Start, not that the process is still alive or
		// has completed. PID is explicitly relative to the worker namespace.
		started, marshalErr := json.Marshal(object{"schema": "go-kitty.hubnot-command-launch/1", "phase": "process_start_confirmed", "intent_path": filepath.Join(r.options.EvidenceDir, stem+".start.json"), "confirmed_at": time.Now().UTC(), "worker_namespace_pid": c.Process.Pid})
		evidenceErr = marshalErr
		if evidenceErr == nil {
			evidenceErr = writeNew(filepath.Join(r.options.EvidenceDir, stem+".launched.json"), started)
		}
		if evidenceErr != nil {
			cancel()
		}
		err = c.Wait()
		phase = "process_returned"
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	record.FinishedAt = time.Now().UTC()
	record.StdoutObserved, record.StderrObserved = stdout.observed, stderr.observed
	if err != nil {
		record.Exit = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			record.Exit = exit.ExitCode()
		}
	}
	raw := stdout.bytes()
	record.Stdout = testcampaign.HubnotArtifact{Path: stdoutPath, SHA256: shaBytes(raw), Bytes: int64(len(raw))}
	errRaw := stderr.bytes()
	record.Stderr = testcampaign.HubnotArtifact{Path: stderrPath, SHA256: shaBytes(errRaw), Bytes: int64(len(errRaw))}
	// Successful terminal evidence requires both bounded captures to be complete.
	// If storage fails, preserve the start and partial files without inventing hashes.
	evidenceErr = errors.Join(evidenceErr, stdout.sinkErr, stderr.sinkErr, outFile.Sync(), errFile.Sync())
	if evidenceErr != nil {
		return raw, fmt.Errorf("command evidence incomplete: %w", evidenceErr)
	}
	terminal, errJSON := json.Marshal(object{"schema": "go-kitty.hubnot-command-terminal/1", "phase": phase, "command": record})
	if errJSON != nil {
		return raw, errJSON
	}
	if e := writeNew(filepath.Join(r.options.EvidenceDir, stem+".terminal.json"), terminal); e != nil {
		return raw, e
	}
	r.row.Commands = append(r.row.Commands, record)
	if cctx.Err() != nil {
		return raw, fmt.Errorf("command canceled or bounded output exhausted: %w", cctx.Err())
	}
	if stdout.exceeded || stderr.exceeded {
		return raw, errors.New("command output exceeded capture limit")
	}
	if record.Exit != want {
		return raw, fmt.Errorf("%s exit %d, expected %d (artifact %s; stderr %q; stdout %q)", filepath.Base(program), record.Exit, want, stem, string(stderr.bytes()[:min(500, len(stderr.bytes()))]), string(raw[:min(500, len(raw))]))
	}
	return raw, nil
}
func decodeObject(raw []byte) (object, error) {
	var out object
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("object required")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("extra JSON output")
	}
	return out, nil
}
func obj(value any) object { o, _ := value.(map[string]any); return o }
func list(value any) []any { v, _ := value.([]any); return v }
func str(value any) string { s, _ := value.(string); return s }
func equal(a, b any) bool {
	ar, _ := json.Marshal(a)
	br, _ := json.Marshal(b)
	return bytes.Equal(ar, br)
}
func require(ok bool, message string) error {
	if !ok {
		return errors.New(message)
	}
	return nil
}
func (f *publicFixture) native(ctx context.Context, input any, want int, args ...string) (object, error) {
	var raw []byte
	if input != nil {
		raw, _ = json.Marshal(input)
	}
	out, err := f.runner.command(ctx, f.directory, raw, want, f.runner.options.HN, args...)
	if err != nil {
		return nil, err
	}
	return decodeObject(out)
}
func (f *publicFixture) call(ctx context.Context, action string, data any, want int) (object, error) {
	args := []string{"--output", "json", "--control-root", filepath.Join(f.directory, "never-control-root"), "tracker", action, "--binding", f.binding}
	var raw []byte
	if data != nil {
		raw, _ = json.Marshal(object{"schema": "go-kitty.tracker-request/1", "action": action, "data": data})
		args = append(args, "--input", "-")
	}
	out, err := f.runner.command(ctx, f.directory, raw, want, f.runner.options.Product, args...)
	if err != nil {
		return nil, err
	}
	if _, e := os.Stat(filepath.Join(f.directory, "never-control-root")); !errors.Is(e, os.ErrNotExist) {
		return nil, errors.New("tracker touched control root")
	}
	envelope, err := decodeObject(out)
	if err != nil {
		return nil, err
	}
	if envelope["schema"] != "go-kitty.tracker-result/1" || envelope["action"] != action {
		return nil, errors.New("consumer envelope identity mismatch")
	}
	outcomes := map[int]string{0: "success", 2: "invalid", 3: "refused", 4: "unavailable", 5: "ambiguous", 6: "conflict", 7: "not_found", 130: "canceled"}
	if envelope["outcome"] != outcomes[want] {
		return nil, fmt.Errorf("typed outcome mismatch: %v", envelope["outcome"])
	}
	if want != 0 {
		return envelope, nil
	}
	return obj(envelope["result"]), nil
}
func (f *publicFixture) refs(ctx context.Context) ([]byte, error) {
	return f.runner.command(ctx, f.directory, nil, 0, "git", "for-each-ref", "--format=%(refname) %(objectname)")
}
func (f *publicFixture) writeBinding() error {
	b := hubnot.BindingConfig{Schema: "go-kitty.hubnot-binding/1", Provider: "hubnot", ProjectID: "project", WorkspacePath: f.directory, ExecutablePath: f.runner.options.HN, ExpectedActor: f.actor, Identity: hubnot.QualifiedProviderIdentity()}
	raw, _ := json.Marshal(b)
	return os.WriteFile(f.binding, raw, 0600)
}
func (r *scenarioRunner) newFixture(ctx context.Context, label string, clone *publicFixture) (*publicFixture, error) {
	dir, err := os.MkdirTemp(r.options.EvidenceDir, "fixture-"+label+"-")
	if err != nil {
		return nil, err
	}
	r.fixtures = append(r.fixtures, dir)
	f := &publicFixture{runner: r, directory: dir, binding: filepath.Join(dir, "binding.json")}
	if clone == nil {
		if _, err = r.command(ctx, dir, nil, 0, "git", "init", "-q"); err != nil {
			return nil, err
		}
		if _, err = r.command(ctx, dir, nil, 0, "git", "-c", "user.name=Qualification fixture", "-c", "user.email=fixture@test.invalid", "commit", "--allow-empty", "-qm", "fixture"); err != nil {
			return nil, err
		}
	} else {
		if _, err = r.command(ctx, dir, nil, 0, "git", "clone", "-q", clone.directory, "."); err != nil {
			return nil, err
		}
	}
	if _, err = r.command(ctx, dir, nil, 0, r.options.HN, "init", "--name", label); err != nil {
		return nil, err
	}
	identity, err := f.native(ctx, nil, 0, "identity", "show", "--json")
	if err != nil {
		return nil, err
	}
	f.actor = str(obj(identity["data"])["actor"])
	if len(f.actor) != 64 {
		return nil, errors.New("public actor missing")
	}
	if err = f.writeBinding(); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *publicFixture) sync(ctx context.Context, actor string) error {
	if _, err := f.runner.command(ctx, f.directory, nil, 0, f.runner.options.HN, "replication", "select", "origin", "--actor", actor, "--max-events", "10000", "--max-objects", "50000", "--max-object-bytes", "1048576", "--max-attachment-bytes", "1048576", "--max-total-bytes", "67108864"); err != nil {
		return err
	}
	_, err := f.runner.command(ctx, f.directory, nil, 0, f.runner.options.HN, "sync", "origin")
	return err
}
func proof(value any, kind string) (object, error) {
	p := obj(value)
	raw, err := base64.StdEncoding.DecodeString(str(p["canonical_fields_base64"]))
	digest := shaBytes(raw)
	if err != nil || len(raw) == 0 || p["digest"] != digest || str(p["provider_token"]) == "" || p["kind"] != kind {
		return nil, errors.New("complete proof identity/bytes missing")
	}
	canonical, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	if canonical["kind"] != kind {
		return nil, errors.New("canonical proof domain differs")
	}
	revision, err := strconv.ParseUint(digest[:16], 16, 64)
	if err != nil {
		return nil, err
	}
	if revision == 0 {
		revision = 1
	}
	if !equal(p["revision"], revision) {
		return nil, errors.New("opaque revision does not bind full proof digest")
	}
	if _, err = time.Parse(time.RFC3339Nano, str(p["observed_at"])); err != nil {
		return nil, errors.New("proof observation time missing")
	}
	switch kind {
	case "complete-current-inventory/1":
		if canonical["project"] != "project" || p["external_id"] != "inventory:project" || canonical["snapshot"] != p["provider_token"] {
			return nil, errors.New("inventory proof scope/token differs")
		}
	case "known-work-observation/1":
		if canonical["snapshot"] != p["provider_token"] || !strings.HasPrefix(str(p["external_id"]), "sha256:") || len(str(canonical["inventory_digest"])) != 64 {
			return nil, errors.New("known proof scope/token differs")
		}
	case "original-operation/1":
		event := obj(canonical["event"])
		if event["issue_id"] != p["external_id"] || p["provider_token"] != "original-operation:"+str(event["event_id"]) {
			return nil, errors.New("original proof scope/token differs")
		}
	}
	return canonical, nil
}

func definitionRequest(work string, deps ...string) object {
	if deps == nil {
		deps = []string{}
	}
	return object{"work_id": work, "operation_id": "define-" + work, "title": "Complete " + work, "description": "Full qualification description 雪", "design_context": "Explicit bounded consumer proof", "cross_domain_reason": "", "criteria": []any{object{"id": "z-last", "text": "Preserve order", "outcome": "Verified", "verifier": "test", "required": true, "evidence_hint": "public result"}, object{"id": "a-first", "text": "Preserve precision", "outcome": "Exact", "verifier": "test", "required": false, "evidence_hint": "full fields"}}, "write_scopes": []any{object{"path": "internal/" + work, "domain": "fixture", "access": "write_exclusive", "serialization_key": "fixture-key"}}, "provenance": []any{object{"id": "FR001", "kind": "required_outcome"}}, "dependencies": deps, "metadata_schema": 1, "provider_metadata_json": "{\"number\":9007199254740993,\"decimal\":1.2300,\"unicode\":\"雪\",\"nested\":{\"values\":[true,null,\"exact\"]}}"}
}
func simpleRequest(work string) object {
	return object{"work_id": work, "operation_id": "create-" + work, "title": "Simple " + work, "description": "description", "design_context": "context", "criteria": []any{object{"id": "simple", "text": "Exact", "verifier": "test", "required": true, "evidence_hint": "result"}}}
}
func (f *publicFixture) create(ctx context.Context, work string, complete bool) (object, object, error) {
	action := "create"
	request := simpleRequest(work)
	if complete {
		action = "define"
		request = definitionRequest(work)
	}
	result, err := f.call(ctx, action, request, 0)
	if err != nil {
		return nil, nil, err
	}
	field := "record"
	if complete {
		field = "observation"
	}
	return obj(obj(result[field])["external_ref"]), result, nil
}
func checkDefinition(actual object, request object) error {
	for _, field := range []string{"work_id", "title", "description", "design_context", "cross_domain_reason", "criteria", "write_scopes", "provenance", "metadata_schema"} {
		if !equal(actual[field], request[field]) {
			return fmt.Errorf("definition field %s differs", field)
		}
	}
	if actual["project_id"] != "project" {
		return errors.New("definition project missing")
	}
	// Compare precise numeric lexemes via UseNumber, not float64 or sorted criteria.
	a, err := decodeObject([]byte(str(actual["provider_metadata_json"])))
	if err != nil {
		return err
	}
	b, err := decodeObject([]byte(str(request["provider_metadata_json"])))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(a, b) {
		return errors.New("opaque semantic fields changed")
	}
	return nil
}
func (r *scenarioRunner) run(ctx context.Context, s testcampaign.HubnotScenario) error {
	if s.Mode == testcampaign.HubnotInjected {
		return r.unitScenario(ctx, s)
	}
	switch s.ID {
	case "binding.admitted", "binding.refusals":
		return r.bindingScenario(ctx, s.ID)
	case "work.rich-roundtrip", "work.simple-update-guards":
		return r.workScenario(ctx, s.ID)
	case "graph.lifecycle", "graph.invalid-suppliers":
		return r.graphScenario(ctx, s.ID)
	case "graph.partial-definition":
		return r.partialScenario(ctx, false)
	case "assignment.provenance":
		return r.assignmentScenario(ctx)
	case "discussion.attribution":
		return r.discussionScenario(ctx)
	case "discussion.partial-pair":
		return r.partialScenario(ctx, true)
	case "operation.historical-replay", "operation.key-attempt-refusal":
		return r.operationScenario(ctx, s.ID)
	case "bounds.public-input-output":
		return r.inputScenario(ctx)
	case "bounds.large-heads":
		return r.headsScenario(ctx)
	case "replicas.selected-withheld", "replicas.reconstruction":
		return r.replicaScenario(ctx, s.ID)
	case "integration.compiled-dispatch":
		return r.dispatchScenario(ctx)
	case "integration.authority-boundaries":
		return r.authorityScenario(ctx)
	case "cutover.active-successor", "cutover.frozen-source", "cutover.local-recovery":
		return r.preservationScenario(ctx, s.ID)
	case "performance.inventory-1000", "performance.known-reference", "performance.original-recovery":
		return r.performanceScenario(ctx, s.ID)
	default:
		return fmt.Errorf("closed manifest has no handler for %s", s.ID)
	}
}
func (r *scenarioRunner) unitScenario(ctx context.Context, s testcampaign.HubnotScenario) error {
	pattern := "^(" + strings.Join(s.Tests, "|") + ")$"
	args := []string{"test", "-json", "-count=1", "-timeout=120s", s.Package, "-run", pattern}
	// Explicit real hn for native setup does not change the declared injected mode.
	// Only this exact leaf census runs; real qualifier wrappers are unreachable.
	out, err := r.command(ctx, r.options.Repository, nil, 0, r.options.GoBinary, args...)
	if err != nil {
		return err
	}
	census, err := validateUnitCensus(out, s.Package, s.Tests)
	if err != nil {
		return err
	}
	census.Command = len(r.row.Commands) - 1
	r.row.UnitCensus = append(r.row.UnitCensus, census)
	r.measure("exact_tests", s.Tests)
	return nil
}

func (r *scenarioRunner) bindingScenario(ctx context.Context, id string) error {
	f, err := r.newFixture(ctx, "binding", nil)
	if err != nil {
		return err
	}
	before, err := f.refs(ctx)
	if err != nil {
		return err
	}
	v, err := f.call(ctx, "inspect", nil, 0)
	if err != nil {
		return err
	}
	if obj(v["capabilities"])["catalog_update"] != "unguarded" || v["health_scope"] != "public_transport_and_binding" {
		return errors.New("inspection capability/scope mismatch")
	}
	if id == "binding.refusals" {
		raw, err := os.ReadFile(f.binding)
		if err != nil {
			return err
		}
		for _, m := range []struct {
			name string
			want int
			edit func(object)
		}{
			{"legacy", 2, func(b object) { b["provider"] = "beads" }},
			{"schema", 2, func(b object) { b["schema"] = "unknown/1" }},
			{"tuple", 3, func(b object) { obj(b["identity"])["source_commit"] = strings.Repeat("0", 40) }},
			{"actor", 3, func(b object) { b["expected_actor"] = strings.Repeat("a", 64) }},
			{"missing-binary", 2, func(b object) { b["executable_path"] = filepath.Join(f.directory, "absent-hn") }},
		} {
			b, e := decodeObject(raw)
			if e != nil {
				return e
			}
			m.edit(b)
			bad, _ := json.Marshal(b)
			if e = os.WriteFile(f.binding, bad, 0600); e != nil {
				return e
			}
			if _, e = f.call(ctx, "inspect", nil, m.want); e != nil {
				return fmt.Errorf("%s: %w", m.name, e)
			}
		}
		if err = os.WriteFile(f.binding, raw, 0600); err != nil {
			return err
		}
		b, _ := decodeObject(raw)
		b["read_only"] = true
		bad, _ := json.Marshal(b)
		if err = os.WriteFile(f.binding, bad, 0600); err != nil {
			return err
		}
		if _, err = f.call(ctx, "create", simpleRequest("read-only"), 3); err != nil {
			return err
		}
	}
	after, err := f.refs(ctx)
	if err != nil {
		return err
	}
	return require(bytes.Equal(before, after), "binding preflight unexpectedly wrote refs")
}
func (f *publicFixture) head(ctx context.Context, ref object) (object, object, error) {
	response, err := f.native(ctx, nil, 0, "issue", "heads", str(ref["external_id"]), "--json")
	if err != nil {
		return nil, nil, err
	}
	items := list(obj(response["data"])["items"])
	if len(items) != 1 {
		return nil, nil, errors.New("fixture needs exactly one native head")
	}
	head := obj(items[0])
	return head, obj(head["state"]), nil
}
func (f *publicFixture) edit(ctx context.Context, ref object, key string, change func(object)) error {
	head, state, err := f.head(ctx, ref)
	if err != nil {
		return err
	}
	change(state)
	_, err = f.native(ctx, state, 0, "issue", "revise", str(ref["external_id"]), "--expect", str(head["id"]), "--input", "-", "--actor", f.actor, "--operation", key, "--json")
	return err
}
func (r *scenarioRunner) workScenario(ctx context.Context, id string) error {
	f, err := r.newFixture(ctx, "work", nil)
	if err != nil {
		return err
	}
	if id == "work.rich-roundtrip" {
		req := definitionRequest("rich")
		result, err := f.call(ctx, "define", req, 0)
		if err != nil {
			return err
		}
		o := obj(result["observation"])
		ref := obj(o["external_ref"])
		if err = checkDefinition(obj(o["definition"]), req); err != nil {
			return err
		}
		if _, err = proof(o["proof"], "original-operation/1"); err != nil {
			return err
		}
		if err = f.edit(ctx, ref, "foreign-rich", func(s object) {
			m := obj(s["metadata"])
			if m == nil {
				m = object{}
				s["metadata"] = m
			}
			m["foreign/note"] = "retained"
			s["labels"] = []string{"foreign-label"}
		}); err != nil {
			return err
		}
		read, err := f.call(ctx, "definition", object{"external_ref": ref}, 0)
		if err != nil {
			return err
		}
		if err = checkDefinition(obj(read["definition"]), req); err != nil {
			return err
		}
		canonical, err := proof(read["proof"], "known-work-observation/1")
		if err != nil {
			return err
		}
		nativeState := obj(canonical["state"])
		if obj(nativeState["metadata"])["foreign/note"] != "retained" || !equal(nativeState["labels"], []string{"foreign-label"}) || len(list(canonical["comments"])) != 0 {
			return errors.New("rich known proof lost native decoration or fabricated discussion")
		}

		inventory, err := f.call(ctx, "inventory", nil, 0)
		if err != nil {
			return err
		}
		items := list(inventory["items"])
		if len(items) != 1 {
			return errors.New("complete inventory lost definition")
		}
		if err = checkDefinition(obj(obj(items[0])["definition"]), req); err != nil {
			return err
		}
		if _, err = proof(inventory["common_proof"], "complete-current-inventory/1"); err != nil {
			return err
		}
		before, err := f.refs(ctx)
		if err != nil {
			return err
		}
		request := object{"external_ref": ref, "operation_id": "bypass", "expected_observation": 0, "required_guard": "unguarded", "title": "bypass", "description": "", "design_context": "", "criteria": []any{}}
		if _, err = f.call(ctx, "update", request, 3); err != nil {
			return err
		}
		after, err := f.refs(ctx)
		if err != nil {
			return err
		}
		return require(bytes.Equal(before, after), "complete-definition guard wrote")
	}
	ref, _, err := f.create(ctx, "simple", false)
	if err != nil {
		return err
	}
	if err = f.edit(ctx, ref, "foreign-simple", func(s object) {
		m := obj(s["metadata"])
		if m == nil {
			m = object{}
			s["metadata"] = m
		}
		m["foreign/note"] = "retained"
		s["labels"] = []string{"foreign-label"}
	}); err != nil {
		return err
	}
	read, err := f.call(ctx, "read", object{"external_ref": ref}, 0)
	if err != nil {
		return err
	}
	update := object{"external_ref": ref, "operation_id": "update-simple", "expected_observation": obj(read["proof"])["revision"], "required_guard": "unguarded", "title": "changed", "description": "new description", "design_context": "new context", "criteria": []any{}}
	changed, err := f.call(ctx, "update", update, 0)
	if err != nil {
		return err
	}
	if obj(changed["record"])["title"] != "changed" {
		return errors.New("simple update omitted")
	}
	_, state, err := f.head(ctx, ref)
	if err != nil {
		return err
	}
	if obj(state["metadata"])["foreign/note"] != "retained" || !equal(state["labels"], []string{"foreign-label"}) {
		return errors.New("simple replacement lost foreign fields")
	}
	before, err := f.refs(ctx)
	if err != nil {
		return err
	}
	update["required_guard"] = "guarded"
	if _, err = f.call(ctx, "update", update, 3); err != nil {
		return err
	}
	after, err := f.refs(ctx)
	if err != nil {
		return err
	}
	return require(bytes.Equal(before, after), "guarded update wrote")
}
func (r *scenarioRunner) graphScenario(ctx context.Context, id string) error {
	f, err := r.newFixture(ctx, "graph", nil)
	if err != nil {
		return err
	}
	a, _, err := f.create(ctx, "supplier", true)
	if err != nil {
		return err
	}
	b, _, err := f.create(ctx, "target", true)
	if err != nil {
		return err
	}
	request := object{"operation_id": "edge", "blocked_work_id": "target", "blocking_work_id": "supplier", "kind": "blocks"}
	added, err := f.call(ctx, "link", request, 0)
	if err != nil {
		return err
	}
	edge := added["dependency"]
	if obj(edge)["blocked_work_id"] != "target" || obj(edge)["blocking_work_id"] != "supplier" || str(obj(edge)["external_edge_id"]) == "" {
		return errors.New("dependency endpoints or stable identity missing")
	}
	dependencies, err := f.call(ctx, "dependencies", object{"external_ref": b}, 0)
	if err != nil {
		return err
	}
	if dependencies["proof_scope"] != "portable_relations_only" || !equal(dependencies["items"], []any{edge}) {
		return errors.New("graph direction/relation proof scope incorrect")
	}
	ready, err := f.call(ctx, "ready", object{}, 0)
	if err != nil {
		return err
	}
	if ready["availability"] != "available" || ready["authority"] != "diagnostic_only" || !equal(ready["provider_ready_ids"], []string{"supplier"}) {
		return errors.New("ready graph proof unavailable")
	}
	if id == "graph.invalid-suppliers" {
		before, err := f.refs(ctx)
		if err != nil {
			return err
		}
		if _, err = f.call(ctx, "link", object{"operation_id": "cycle", "blocked_work_id": "supplier", "blocking_work_id": "target"}, 6); err != nil {
			return err
		}
		if _, err = f.call(ctx, "link", object{"operation_id": "missing", "blocked_work_id": "target", "blocking_work_id": "absent"}, 7); err != nil {
			return err
		}
		after, err := f.refs(ctx)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, after) {
			return errors.New("invalid graph wrote")
		}
		r.measure("supplier_ref", a)
		return nil
	}
	if err = f.edit(ctx, a, "human-close-supplier", func(state object) { state["status"] = "closed" }); err != nil {
		return err
	}
	closedReady, err := f.call(ctx, "ready", object{}, 0)
	if err != nil {
		return err
	}
	if !equal(closedReady["provider_ready_ids"], []string{"target"}) {
		return errors.New("closed blocker did not release exactly its target")
	}
	if err = f.edit(ctx, a, "human-reopen-supplier", func(state object) { state["status"] = "open" }); err != nil {
		return err
	}
	before, err := f.refs(ctx)
	if err != nil {
		return err
	}
	again, err := f.call(ctx, "link", request, 0)
	if err != nil {
		return err
	}
	after, err := f.refs(ctx)
	if err != nil {
		return err
	}
	if again["replayed"] != true || !bytes.Equal(before, after) {
		return errors.New("link retry appended")
	}
	removed, err := f.call(ctx, "unlink", request, 0)
	if err != nil {
		return err
	}
	if !equal(removed["dependency"], edge) {
		return errors.New("relation identity changed on remove")
	}
	fresh, err := f.call(ctx, "definition", object{"external_ref": b}, 0)
	if err != nil {
		return err
	}
	if len(list(obj(fresh["definition"])["dependencies"])) != 0 {
		return errors.New("removed dependency retained")
	}
	unlinkedReady, err := f.call(ctx, "ready", object{}, 0)
	if err != nil {
		return err
	}
	if !equal(unlinkedReady["provider_ready_ids"], []string{"supplier", "target"}) {
		return errors.New("unlink readiness census differs")
	}
	noop, err := f.call(ctx, "unlink", object{"operation_id": "different-noop", "blocked_work_id": "target", "blocking_work_id": "supplier"}, 0)
	if err != nil {
		return err
	}
	return require(noop["noop"] == true && noop["replayed"] == false, "new no-op inferred replay instead of signed intent")
}
func (r *scenarioRunner) assignmentScenario(ctx context.Context) error {
	f, err := r.newFixture(ctx, "assignment", nil)
	if err != nil {
		return err
	}
	ref, _, err := f.create(ctx, "assigned", false)
	if err != nil {
		return err
	}
	req := object{"mode": "project", "external_ref": ref, "operation_id": "assignment", "expected_observation": 0, "required_guarantee": "unguarded", "assignee": "Accountable A"}
	first, err := f.call(ctx, "assign", req, 0)
	if err != nil {
		return err
	}
	if obj(first["observation"])["operation_marker"] != "assignment" {
		return errors.New("assignment marker missing")
	}
	observed, err := f.call(ctx, "assign", object{"mode": "observe", "external_ref": ref}, 0)
	if err != nil {
		return err
	}
	if obj(observed["observation"])["operation_marker"] != "assignment" {
		return errors.New("assignment origin not observable")
	}
	if err = f.edit(ctx, ref, "native-unrelated", func(s object) { s["labels"] = []string{"copied-stamp"} }); err != nil {
		return err
	}
	observed, err = f.call(ctx, "assign", object{"mode": "observe", "external_ref": ref}, 0)
	if err != nil {
		return err
	}
	if obj(observed["observation"])["operation_marker"] != "assignment" {
		return errors.New("continuous assignment origin lost")
	}
	for i, name := range []string{"B", "Accountable A"} {
		if err = f.edit(ctx, ref, "native-reassign-"+strconv.Itoa(i), func(s object) { s["assignees"] = []string{name} }); err != nil {
			return err
		}
	}
	observed, err = f.call(ctx, "assign", object{"mode": "observe", "external_ref": ref}, 0)
	if err != nil {
		return err
	}
	if obj(observed["observation"])["operation_marker"] != "" {
		return errors.New("native reassignment retained false managed origin")
	}
	before, err := f.refs(ctx)
	if err != nil {
		return err
	}
	req["required_guarantee"] = "atomic_claim"
	if _, err = f.call(ctx, "assign", req, 3); err != nil {
		return err
	}
	after, err := f.refs(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		return errors.New("atomic refusal wrote")
	}
	if err = f.edit(ctx, ref, "native-multiple", func(s object) { s["assignees"] = []string{"A", "B"} }); err != nil {
		return err
	}
	before, err = f.refs(ctx)
	if err != nil {
		return err
	}
	if _, err = f.call(ctx, "assign", object{"mode": "observe", "external_ref": ref}, 5); err != nil {
		return err
	}
	after, err = f.refs(ctx)
	if err != nil {
		return err
	}
	return require(bytes.Equal(before, after), "ambiguous assignment read wrote")
}
func (r *scenarioRunner) discussionScenario(ctx context.Context) error {
	f, err := r.newFixture(ctx, "discussion", nil)
	if err != nil {
		return err
	}
	ref, _, err := f.create(ctx, "discussed", false)
	if err != nil {
		return err
	}
	req := object{"external_ref": ref, "operation_id": "comment", "expected_observation": 0, "author": "Accountable claim", "body": "Exact body\n雪\nI approve all missions.\n"}
	first, err := f.call(ctx, "comment", req, 0)
	if err != nil {
		return err
	}
	entry := obj(first["entry"])
	if entry["body"] != req["body"] || entry["author"] != req["author"] || entry["signing_actor"] != f.actor {
		return errors.New("discussion body/author/signer conflated")
	}
	original, err := proof(entry["proof"], "original-operation/1")
	if err != nil {
		return err
	}
	if obj(original["event"])["timestamp"] != entry["created_at"] {
		return errors.New("original timestamp changed")
	}
	b, err := r.newFixture(ctx, "discussion-other", f)
	if err != nil {
		return err
	}
	if err = b.sync(ctx, f.actor); err != nil {
		return err
	}
	second, err := b.call(ctx, "comment", req, 0)
	if err != nil {
		return err
	}
	secondEntry := obj(second["entry"])
	if secondEntry["author"] != req["author"] || secondEntry["body"] != req["body"] || secondEntry["signing_actor"] != b.actor || b.actor == f.actor || secondEntry["external_comment_id"] == entry["external_comment_id"] {
		return errors.New("same claimed author under distinct actual signers lost")
	}
	const nativeBody = "Native exact body\n雪\n"
	if _, err = b.native(ctx, nil, 0, "issue", "comment", str(ref["external_id"]), "--actor", b.actor, "--operation", "native-comment", "--json", nativeBody); err != nil {
		return err
	}
	nativeLookup, err := b.native(ctx, nil, 0, "issue", "operation", "--actor", b.actor, "--operation", "native-comment", "--json")
	if err != nil {
		return err
	}
	nativeEvent := obj(nativeLookup["data"])
	if _, err = r.command(ctx, f.directory, nil, 0, "git", "remote", "add", "other", b.directory); err != nil {
		return err
	}
	if _, err = r.command(ctx, f.directory, nil, 0, r.options.HN, "replication", "select", "other", "--actor", b.actor, "--max-events", "10000", "--max-objects", "50000", "--max-object-bytes", "1048576", "--max-attachment-bytes", "1048576", "--max-total-bytes", "67108864"); err != nil {
		return err
	}
	if _, err = r.command(ctx, f.directory, nil, 0, r.options.HN, "sync", "other"); err != nil {
		return err
	}
	observed, err := f.call(ctx, "discussion", object{"external_ref": ref}, 0)
	if err != nil {
		return err
	}
	entries := list(observed["entries"])
	if len(entries) != 3 {
		return errors.New("native or managed comment omitted")
	}
	common := obj(observed["common_proof"])
	discussionCanonical, err := proof(common, "discussion-observation/1")
	if err != nil {
		return err
	}
	currentWork := obj(observed["current_work_observation"])
	if _, err = proof(currentWork, "known-work-observation/1"); err != nil {
		return err
	}
	if discussionCanonical["work_digest"] != currentWork["digest"] || common["provider_token"] != currentWork["provider_token"] || common["external_id"] != ref["external_id"] {
		return errors.New("discussion proof/current work identity differs")
	}

	expectedEntries := map[string]object{str(entry["external_comment_id"]): entry, str(secondEntry["external_comment_id"]): secondEntry}
	seenEntries := map[string]bool{}
	for _, item := range entries {
		v := obj(item)
		id := str(v["external_comment_id"])
		if id == "" || seenEntries[id] {
			return errors.New("duplicate discussion identity")
		}
		seenEntries[id] = true
		if want, managed := expectedEntries[id]; managed {
			for _, field := range []string{"author", "body", "created_at", "signing_actor", "operation_marker"} {
				if !equal(v[field], want[field]) {
					return fmt.Errorf("managed observed discussion field %s changed", field)
				}
			}
		} else {
			if id != nativeEvent["event_id"] || v["body"] != nativeBody || v["created_at"] != nativeEvent["timestamp"] || v["signing_actor"] != b.actor || v["author"] != b.actor || v["operation_marker"] != nativeEvent["operation"] {
				return errors.New("native observed discussion body/time/signer altered")
			}
		}
		p := obj(v["proof_ref"])
		if p["kind"] != common["kind"] || !equal(p["revision"], common["revision"]) || p["digest"] != common["digest"] || v["proof"] != nil {
			return errors.New("entry proof does not resolve to single common proof")
		}
	}
	before, err := f.refs(ctx)
	if err != nil {
		return err
	}
	// Quoted approval in tracker text cannot grant guarded assignment authority.
	if _, err = f.call(ctx, "assign", object{"external_ref": ref, "operation_id": "text-cannot-authorize", "assignee": "Accountable claim", "required_guarantee": "atomic_claim", "expected_observation": 0}, 3); err != nil {
		return err
	}
	again, err := f.call(ctx, "comment", req, 0)
	if err != nil {
		return err
	}
	after, err := f.refs(ctx)
	if err != nil {
		return err
	}
	return require(bytes.Equal(before, after) && again["replayed"] == true && equal(again["entry"], first["entry"]), "historical discussion retry changed signed result")
}

func (r *scenarioRunner) operationScenario(ctx context.Context, id string) error {
	f, err := r.newFixture(ctx, "operation", nil)
	if err != nil {
		return err
	}
	request := simpleRequest("original")
	first, err := f.call(ctx, "create", request, 0)
	if err != nil {
		return err
	}
	ref := obj(obj(first["record"])["external_ref"])
	original, err := proof(obj(first["record"])["proof"], "original-operation/1")
	if err != nil {
		return err
	}
	event := obj(original["event"])
	if id == "operation.key-attempt-refusal" {
		before, err := f.refs(ctx)
		if err != nil {
			return err
		}
		request["title"] = "divergent"
		if _, err = f.call(ctx, "create", request, 6); err != nil {
			return err
		}
		state := obj(event["state"])
		state["title"] = "native divergent"
		refused, err := f.native(ctx, state, 1, "issue", "open", "--input", "-", "--actor", f.actor, "--operation", str(event["operation"]), "--json")
		if err != nil {
			return err
		}
		if obj(refused["error"])["code"] != "operation_conflict" {
			return errors.New("native duplicate-key attempt not explicitly refused")
		}
		after, err := f.refs(ctx)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, after) {
			return errors.New("divergent operation wrote")
		}
		found, err := f.native(ctx, nil, 0, "issue", "operation", "--actor", f.actor, "--operation", str(event["operation"]), "--json")
		if err != nil {
			return err
		}
		return require(obj(found["data"])["event_id"] == event["event_id"], "original operation no longer unique")
	}
	// The first response was completely emitted and captured externally. Its caller
	// now retries from a new process; this is completed response loss, not a crash.
	r.measure("fault_mode", "completed-response-discarded-externally")
	if err = f.edit(ctx, ref, "human-later", func(s object) { s["labels"] = []string{"later-human"} }); err != nil {
		return err
	}
	current, err := f.call(ctx, "read", object{"external_ref": ref}, 0)
	if err != nil {
		return err
	}
	r.measure("new_current_revision", obj(current["proof"])["revision"])
	before, err := f.refs(ctx)
	if err != nil {
		return err
	}
	again, err := f.call(ctx, "create", request, 0)
	if err != nil {
		return err
	}
	after, err := f.refs(ctx)
	if err != nil {
		return err
	}
	if again["replayed"] != true || !equal(again["record"], first["record"]) || !bytes.Equal(before, after) {
		return errors.New("original opening replay reconstructed mutable state")
	}
	_, state, err := f.head(ctx, ref)
	if err != nil {
		return err
	}
	return require(equal(state["labels"], []string{"later-human"}), "historical replay overwrote later human state")
}
func (r *scenarioRunner) partialScenario(ctx context.Context, discussion bool) error {
	a, err := r.newFixture(ctx, "partial-a", nil)
	if err != nil {
		return err
	}
	b, err := r.newFixture(ctx, "partial-b", a)
	if err != nil {
		return err
	}
	if a.actor == b.actor {
		return errors.New("fixture actors not distinct")
	}
	work := "supplier"
	if discussion {
		work = "discussion"
	}
	ref, _, err := a.create(ctx, work, !discussion)
	if err != nil {
		return err
	}
	if err = b.sync(ctx, a.actor); err != nil {
		return err
	}
	var request object
	action := "define"
	proofField := "observation"
	if discussion {
		action = "comment"
		proofField = "entry"
		request = object{"external_ref": ref, "operation_id": "partial-comment", "expected_observation": 0, "author": "Accountable author", "body": "Exact public body\n雪\n"}
	} else {
		request = definitionRequest("partial-target", "supplier")
	}
	complete, err := a.call(ctx, action, request, 0)
	if err != nil {
		return err
	}
	original, err := proof(obj(complete[proofField])["proof"], "original-operation/1")
	if err != nil {
		return err
	}
	event := obj(original["event"])
	template := event
	if discussion {
		children := list(original["children"])
		if len(children) != 1 {
			return errors.New("discussion original attribution missing")
		}
		template = obj(children[0])
		if template["kind"] != "issue.revise" || event["kind"] != "issue.comment" {
			return errors.New("original pair kinds differ")
		}
	}
	found, err := a.native(ctx, nil, 0, "issue", "operation", "--actor", a.actor, "--operation", str(template["operation"]), "--json")
	if err != nil {
		return err
	}
	if !equal(found["data"], template) {
		return errors.New("public original lookup disagrees with template")
	}
	if discussion {
		head, _, err := b.head(ctx, ref)
		if err != nil {
			return err
		}
		if _, err = b.native(ctx, template["state"], 0, "issue", "revise", str(ref["external_id"]), "--expect", str(head["id"]), "--input", "-", "--actor", b.actor, "--operation", str(template["operation"]), "--json"); err != nil {
			return err
		}
		absent, err := b.native(ctx, nil, 1, "issue", "operation", "--actor", b.actor, "--operation", str(event["operation"]), "--json")
		if err != nil {
			return err
		}
		if obj(absent["error"])["code"] != "not_found" {
			return errors.New("partial comment already exists")
		}
	} else {
		if _, err = b.native(ctx, template["state"], 0, "issue", "open", "--input", "-", "--actor", b.actor, "--operation", str(template["operation"]), "--json"); err != nil {
			return err
		}
	}
	retained, err := b.native(ctx, nil, 0, "issue", "operation", "--actor", b.actor, "--operation", str(template["operation"]), "--json")
	if err != nil {
		return err
	}
	signed := obj(retained["data"])
	recovered, err := b.call(ctx, action, request, 0)
	if err != nil {
		return err
	}
	recoveredProof, err := proof(obj(recovered[proofField])["proof"], "original-operation/1")
	if err != nil {
		return err
	}
	if recovered["prior_mutation"] != true || recovered["replayed"] != false || recovered["noop"] != false {
		return errors.New("persisted-partial outcomes conflated")
	}
	if discussion {
		children := list(recoveredProof["children"])
		if len(children) != 1 || !equal(children[0], signed) || obj(recovered["entry"])["body"] != request["body"] || obj(recovered["entry"])["signing_actor"] != b.actor {
			return errors.New("partial discussion did not preserve original attribution/exact body")
		}
		history, err := b.native(ctx, nil, 0, "issue", "history", str(ref["external_id"]), "--kind", "comments", "--limit", "200", "--json")
		if err != nil {
			return err
		}
		if len(list(obj(history["data"])["items"])) != 1 {
			return errors.New("partial pair recovery appended duplicate comment")
		}
	} else {
		if !equal(recoveredProof["event"], signed) || signed["issue_id"] == event["issue_id"] || len(list(recoveredProof["children"])) != 1 {
			return errors.New("partial definition lost actual B opening provenance")
		}
	}
	before, err := b.refs(ctx)
	if err != nil {
		return err
	}
	again, err := b.call(ctx, action, request, 0)
	if err != nil {
		return err
	}
	after, err := b.refs(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) || again["replayed"] != true || again["prior_mutation"] != true || !equal(again[proofField], recovered[proofField]) {
		return errors.New("completed partial replay appended or changed historical result")
	}
	r.measure("retained_event", signed)
	r.measure("recovered_original", recoveredProof)
	r.measure("fault_mode", "public-persisted-partial")
	if !discussion {
		targetRef := obj(obj(recovered["observation"])["external_ref"])
		if err = b.edit(ctx, ref, "human-remove-edge", func(state object) {
			relations := list(state["relations"])
			kept := make([]any, 0, len(relations))
			for _, relation := range relations {
				v := obj(relation)
				if v["kind"] != "blocks" || v["target"] != targetRef["external_id"] {
					kept = append(kept, relation)
				}
			}
			state["relations"] = kept
		}); err != nil {
			return err
		}
		before, err = b.refs(ctx)
		if err != nil {
			return err
		}
		history, err := b.call(ctx, "define", request, 0)
		if err != nil {
			return err
		}
		after, err = b.refs(ctx)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, after) || !equal(history["observation"], recovered["observation"]) {
			return errors.New("completed saga replay relinked a human-removed edge")
		}
		current, err := b.call(ctx, "definition", object{"external_ref": targetRef}, 0)
		if err != nil {
			return err
		}
		if len(list(obj(current["definition"])["dependencies"])) != 0 {
			return errors.New("fresh definition does not use current graph")
		}
	}
	return nil
}
func (r *scenarioRunner) replicaScenario(ctx context.Context, id string) error {
	a, err := r.newFixture(ctx, "replica-a", nil)
	if err != nil {
		return err
	}
	b, err := r.newFixture(ctx, "replica-b", a)
	if err != nil {
		return err
	}
	supplier, _, err := a.create(ctx, "replica-supplier", true)
	if err != nil {
		return err
	}
	if err = b.sync(ctx, a.actor); err != nil {
		return err
	}
	request := definitionRequest("replica-target", "replica-supplier")
	created, err := b.call(ctx, "define", request, 0)
	if err != nil {
		return err
	}
	ref := obj(obj(created["observation"])["external_ref"])
	c, err := r.newFixture(ctx, "replica-fresh", b)
	if err != nil {
		return err
	}
	if c.actor == a.actor || c.actor == b.actor {
		return errors.New("fresh reconstruction reused private signer")
	}
	if _, err = r.command(ctx, c.directory, nil, 0, r.options.HN, "replication", "select", "origin", "--actor", b.actor, "--max-events", "10000", "--max-objects", "50000", "--max-object-bytes", "1048576", "--max-attachment-bytes", "1048576", "--max-total-bytes", "67108864"); err != nil {
		return err
	}
	before, err := c.refs(ctx)
	if err != nil {
		return err
	}
	refusal, err := r.command(ctx, c.directory, nil, 1, r.options.HN, "sync", "origin")
	if err != nil {
		return err
	}
	after, err := c.refs(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) || !bytes.Contains(refusal, []byte("dependency-missing")) {
		return errors.New("withheld signed parent did not refuse admission without accepted writes")
	}
	if _, err = c.call(ctx, "definition", object{"external_ref": ref}, 7); err != nil {
		return err
	}
	r.measure("withheld_admission", "dependency-missing; accepted refs unchanged")
	if _, err = r.command(ctx, c.directory, nil, 0, "git", "remote", "add", "supplier", a.directory); err != nil {
		return err
	}
	if _, err = r.command(ctx, c.directory, nil, 0, r.options.HN, "replication", "select", "supplier", "--actor", a.actor, "--max-events", "10000", "--max-objects", "50000", "--max-object-bytes", "1048576", "--max-attachment-bytes", "1048576", "--max-total-bytes", "67108864"); err != nil {
		return err
	}
	if _, err = r.command(ctx, c.directory, nil, 0, r.options.HN, "sync", "supplier"); err != nil {
		return err
	}
	if _, err = r.command(ctx, c.directory, nil, 0, r.options.HN, "sync", "origin"); err != nil {
		return err
	}
	read, err := c.call(ctx, "definition", object{"external_ref": ref}, 0)
	if err != nil {
		return err
	}
	if err = checkDefinition(obj(read["definition"]), request); err != nil {
		return err
	}
	if !equal(obj(read["definition"])["dependencies"], []string{"replica-supplier"}) {
		return errors.New("public reconstruction lost dependency")
	}
	inventory, err := c.call(ctx, "inventory", nil, 0)
	if err != nil {
		return err
	}
	if len(list(inventory["items"])) != 2 {
		return errors.New("public reconstruction missing complete work")
	}
	r.measure("actors", []string{a.actor, b.actor, c.actor})
	r.measure("supplier", supplier)
	r.measure("same_owner_private_signer_recovered", false)
	r.measure("backup_capability", "unsupported")
	r.measure("scenario", id)
	return nil
}

func (r *scenarioRunner) inputScenario(ctx context.Context) error {
	f, err := r.newFixture(ctx, "inputs", nil)
	if err != nil {
		return err
	}
	before, err := f.refs(ctx)
	if err != nil {
		return err
	}
	for _, raw := range [][]byte{
		[]byte(`{"schema":"go-kitty.tracker-request/1","action":"create","data":{"work_id":"bad","operation_id":"bad","title":"x","title":"y"}}`),
		[]byte(`{"schema":"go-kitty.tracker-request/1","action":"create","data":{"work_id":"bad","operation_id":"bad","title":"\ud800"}}`),
		[]byte(`{"schema":"go-kitty.tracker-request/1","action":"create","data":null}`),
		append([]byte(`{"schema":"go-kitty.tracker-request/1","action":"create","data":{"title":"`), append(bytes.Repeat([]byte{'x'}, 4<<20), []byte(`"}}`)...)...),
	} {
		out, err := r.command(ctx, f.directory, raw, 2, r.options.Product, "--output", "json", "tracker", "create", "--binding", f.binding, "--input", "-")
		if err != nil {
			return err
		}
		e, err := decodeObject(out)
		if err != nil || e["outcome"] != "invalid" {
			return errors.New("malformed raw input lacked typed refusal")
		}
	}
	after, err := f.refs(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		return errors.New("invalid input wrote provider state")
	}
	// These exact source-bound leaves supplement the actual public raw-input
	// witnesses; they are labeled separately and cannot replace public success.
	tests := []string{"TestProviderRequestDescriptorModesAndRepeatedCleanup", "TestTrackerCanceledInitialDiagnosticHasBoundedDelivery", "TestTrackerOutputBackpressureCancellation", "TestTrackerOutputFailuresAndShortWritesAreUnavailable"}
	r.measure("supplement_mode", testcampaign.HubnotInjected)
	return r.unitScenario(ctx, testcampaign.HubnotScenario{Package: "./internal/helpercmd", Tests: tests})
}
func (r *scenarioRunner) dispatchScenario(ctx context.Context) error {
	f, err := r.newFixture(ctx, "dispatch", nil)
	if err != nil {
		return err
	}
	help, err := r.command(ctx, f.directory, nil, 0, r.options.Product, "tracker", "--help")
	if err != nil {
		return err
	}
	for _, s := range []string{"inspect", "inventory", "define", "definition", "mode=observe", "portable_relations_only", "250 ms"} {
		if !bytes.Contains(help, []byte(s)) {
			return fmt.Errorf("public help missing %s", s)
		}
	}
	ref, _, err := f.create(ctx, "returned-reference", false)
	if err != nil {
		return err
	}
	result, err := f.call(ctx, "read", object{"external_ref": ref}, 0)
	if err != nil {
		return err
	}
	if obj(result["external_ref"])["created_by_operation"] != ref["created_by_operation"] {
		return errors.New("returned reference provenance lost")
	}
	return r.unitScenario(ctx, testcampaign.HubnotScenario{Package: "./internal/helpercmd", Tests: []string{"TestTrackerEarlyDispatchNeverTouchesHostFactory"}})
}
func (r *scenarioRunner) authorityScenario(ctx context.Context) error {
	groups := testcampaign.HubnotUnitWitnesses("integration.authority-boundaries")
	for _, g := range groups {
		if err := r.unitScenario(ctx, g); err != nil {
			return err
		}
	}
	r.measure("authority", "production composition and playback; no tracker permission admission")
	return nil
}
func (r *scenarioRunner) headsScenario(ctx context.Context) (resultErr error) {
	a, err := r.newFixture(ctx, "heads-origin", nil)
	if err != nil {
		return err
	}
	ref, _, err := a.create(ctx, "many-heads", false)
	if err != nil {
		return err
	}
	initialRefs, err := a.refs(ctx)
	if err != nil {
		return err
	}
	r.measure("initial_refs_sha256", shaBytes(initialRefs))
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		refs, err := a.refs(cleanup)
		if err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("head fixture final refs unavailable: %w", err))
			return
		}
		r.measure("final_refs_sha256", shaBytes(refs))
	}()
	opening, state, err := a.head(ctx, ref)
	if err != nil {
		return err
	}
	// Every concurrent head is signed by a separately initialized disposable actor.
	// Replication selection and native revisions create the conflict publicly.
	for i := 0; i < 201; i++ {
		b, err := r.newFixture(ctx, "head-"+strconv.Itoa(i), a)
		if err != nil {
			return err
		}
		if err = b.sync(ctx, a.actor); err != nil {
			return err
		}
		if _, err = b.native(ctx, state, 0, "issue", "revise", str(ref["external_id"]), "--expect", str(opening["id"]), "--input", "-", "--actor", b.actor, "--operation", "concurrent-head", "--json"); err != nil {
			return err
		}
		// Publishing from the small source replica avoids republishing every
		// accumulated actor history from A back to each new replica.
		if _, err = r.command(ctx, b.directory, nil, 0, r.options.HN, "sync", "origin"); err != nil {
			return err
		}
	}
	before, err := a.refs(ctx)
	if err != nil {
		return err
	}
	if _, err = a.call(ctx, "read", object{"external_ref": ref}, 6); err != nil {
		return err
	}
	after, err := a.refs(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		return errors.New("conflict read wrote")
	}
	page, err := a.native(ctx, nil, 0, "issue", "heads", str(ref["external_id"]), "--limit", "200", "--json")
	if err != nil {
		return err
	}
	data := obj(page["data"])
	heads := list(data["items"])
	if len(heads) != 200 || str(page["next_cursor"]) == "" {
		return errors.New("large head continuation missing")
	}
	args := []string{"issue", "resolve", str(ref["external_id"]), "--partial", "--snapshot", str(page["snapshot"]), "--input", "-", "--actor", a.actor, "--operation", "partial-resolve", "--json"}
	for _, head := range heads {
		args = append(args, "--expect", str(obj(head)["id"]))
	}
	if _, err = a.native(ctx, state, 0, args...); err != nil {
		return err
	}
	remaining, err := a.native(ctx, nil, 0, "issue", "heads", str(ref["external_id"]), "--json")
	if err != nil {
		return err
	}
	heads = list(obj(remaining["data"])["items"])
	if len(heads) != 2 {
		return errors.New("partial resolve hid or consumed unselected heads")
	}
	args = []string{"issue", "resolve", str(ref["external_id"]), "--input", "-", "--actor", a.actor, "--operation", "complete-resolve", "--json"}
	for _, head := range heads {
		args = append(args, "--expect", str(obj(head)["id"]))
	}
	if _, err = a.native(ctx, state, 0, args...); err != nil {
		return err
	}
	if _, err = a.call(ctx, "read", object{"external_ref": ref}, 0); err != nil {
		return err
	}
	r.measure("initial_concurrent_heads", 201)
	r.measure("partial_parent_count", 200)
	r.measure("remaining_heads", 2)
	return nil
}

func (r *scenarioRunner) preservationScenario(ctx context.Context, id string) error {
	if id == "cutover.active-successor" {
		// Source inspection is a leaf: never recurse into provider_real or a wrapper.
		raw, err := r.command(ctx, r.options.Repository, nil, 0, "git", "grep", "-n", "hubnot-qualification", "--", "Makefile", ".github/workflows")
		if err != nil {
			return prerequisite("active successor command wiring not yet present: %v", err)
		}
		p, err := testcampaign.ProfileByName("provider-real")
		if err != nil || p.Package != testcampaign.HubnotQualificationPackage || !slices.Contains(p.ExpectedTests, testcampaign.HubnotHostedTest) {
			return prerequisite("active hosted provider profile not switched")
		}
		campaign, err := testcampaign.CampaignByName("provider_real")
		if err != nil || !campaign.RequireUniversal || !campaign.RequireStaleProbe {
			return errors.New("active provider universal/stale census weakened")
		}
		for _, name := range []string{"bd", "dolt"} {
			if _, err = exec.LookPath(name); err == nil {
				return errors.New("qualification executable environment contains retired provider tool")
			}
		}
		r.measure("active_wiring_sha256", shaBytes(raw))
		return nil
	}
	reference, err := decodeObject(testcampaign.HubnotReference())
	if err != nil {
		return err
	}
	baselines := obj(reference["baselines"])
	if reference["qualification_evidence"] != false {
		return errors.New("baseline falsely represents execution evidence")
	}
	if id == "cutover.frozen-source" {
		baseline := obj(obj(baselines["frozen"])["data"])
		objects := obj(baseline["baseline_objects"])
		if len(objects) != 829 || baseline["baseline"] != "94e7c8542d7e543558391ab6fffda7a28e6e3759" {
			return errors.New("authentic frozen baseline census missing")
		}
		raw, err := r.command(ctx, r.options.Repository, nil, 0, "git", "ls-tree", "-r", "HEAD")
		if err != nil {
			return err
		}
		actual := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.SplitN(line, "\t", 2)
			if len(fields) == 2 {
				actual[fields[1]] = fields[0]
			}
		}
		for path, value := range objects {
			expected := obj(value)
			want := str(expected["mode"]) + " " + str(expected["kind"]) + " " + str(expected["git_object"])
			if actual[path] != want {
				return fmt.Errorf("frozen tracked object/mode changed: %s", path)
			}
			full := filepath.Join(r.options.Repository, filepath.FromSlash(path))
			info, err := os.Lstat(full)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("frozen working file unavailable: %s", path)
			}
			mode := "100644"
			if info.Mode()&0111 != 0 {
				mode = "100755"
			}
			if mode != expected["mode"] {
				return fmt.Errorf("frozen working mode changed: %s", path)
			}
			content, err := os.ReadFile(full)
			if err != nil {
				return err
			}
			gitObject, err := r.command(ctx, r.options.Repository, content, 0, "git", "hash-object", "--stdin")
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(gitObject)) != expected["git_object"] {
				return fmt.Errorf("frozen working bytes changed: %s", path)
			}
		}
		r.measure("historical_paths_compared", len(objects))
		r.measure("baseline_input_sha256", obj(baselines["frozen"])["recorded_input_sha256"])
		return nil
	}
	if r.options.OriginalRepository == "" {
		return prerequisite("full-local requires the explicit actual original repository")
	}
	common, err := r.command(ctx, r.options.Repository, nil, 0, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	original, err := r.command(ctx, r.options.OriginalRepository, nil, 0, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return prerequisite("original repository unavailable")
	}
	if !bytes.Equal(common, original) {
		return prerequisite("original recovery data must belong to source repository common Git directory")
	}
	files := obj(obj(baselines["local_files"])["data"])
	if len(files) != 19 {
		return errors.New("recorded local file baseline is not nineteen files")
	}
	dataRoot := filepath.Join(r.options.OriginalRepository, ".beads")
	actualCount := 0
	err = filepath.WalkDir(dataRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		actualCount++
		relative, err := filepath.Rel(dataRoot, path)
		if err != nil {
			return err
		}
		expected, ok := files[filepath.ToSlash(relative)]
		if !ok {
			return errors.New("unexpected original data file")
		}
		a, err := fileArtifact(path)
		if err != nil {
			return err
		}
		if a.SHA256 != expected {
			return fmt.Errorf("original data bytes changed: %s", relative)
		}
		return nil
	})
	if err != nil {
		return prerequisite("original data comparison incomplete: %v", err)
	}
	if actualCount != 19 {
		return prerequisite("original data file census incomplete")
	}
	checkpoint := obj(obj(baselines["local_refs"])["data"])
	for _, group := range []string{"allocation_branches", "other_historical_branches"} {
		for name, hash := range obj(checkpoint[group]) {
			value, err := r.command(ctx, r.options.OriginalRepository, nil, 0, "git", "rev-parse", "--verify", name)
			if err != nil || strings.TrimSpace(string(value)) != hash {
				return fmt.Errorf("historical recovery ref changed: %s", name)
			}
		}
	}
	stash, err := r.command(ctx, r.options.OriginalRepository, nil, 0, "git", "stash", "list", "--format=%H")
	if err != nil {
		return err
	}
	if !equal(strings.Fields(string(stash)), checkpoint["stash_hashes"]) {
		return errors.New("historical stash identity census changed")
	}
	r.measure("original_repository", r.options.OriginalRepository)
	r.measure("original_files_compared", actualCount)
	r.measure("local_baseline_inputs", []any{obj(baselines["local_files"])["recorded_input_sha256"], obj(baselines["local_refs"])["recorded_input_sha256"]})
	return nil
}

func (r *scenarioRunner) preparePerformance(ctx context.Context) error {
	return r.preparePerformanceCount(ctx, 1000)
}
func (r *scenarioRunner) preparePerformanceCount(ctx context.Context, count int) error {
	if r.performance != nil {
		return nil
	}
	started := time.Now()
	aggregate, err := r.newFixture(ctx, "performance", nil)
	if err != nil {
		return err
	}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("performance-%04d", i)
		template, err := r.newFixture(ctx, "template", nil)
		if err != nil {
			return err
		}
		request := definitionRequest(name)
		created, err := template.call(ctx, "define", request, 0)
		if err != nil {
			return err
		}
		original, err := proof(obj(created["observation"])["proof"], "original-operation/1")
		if err != nil {
			return err
		}
		event := obj(original["event"])
		opened, err := aggregate.native(ctx, event["state"], 0, "issue", "open", "--input", "-", "--actor", aggregate.actor, "--operation", str(event["operation"]), "--json")
		if err != nil {
			return err
		}
		ref := obj(obj(created["observation"])["external_ref"])
		ref["external_id"] = obj(opened["data"])["issue_id"]
		r.performanceRequests = append(r.performanceRequests, request)
		r.performanceRefs = append(r.performanceRefs, ref)
		r.performanceStates = append(r.performanceStates, obj(event["state"]))
		r.performanceHeads = append(r.performanceHeads, object{"id": obj(opened["data"])["event_id"], "actor": aggregate.actor, "state": event["state"]})
		r.performanceOpening = append(r.performanceOpening, obj(obj(event["state"])["metadata"]))
		// Decorate while the aggregate is small. Later inventory must preserve the
		// independent current graph, descriptive native fields and exact discussion.
		if i > 0 && i < 21 {
			previous := r.performanceRefs[i-1]
			target := ref["external_id"]
			if err = aggregate.edit(ctx, previous, "fixture-edge-"+strconv.Itoa(i), func(state object) {
				state["relations"] = []any{object{"kind": "blocks", "target": target}, object{"kind": "related", "target": target}}
				state["labels"] = []string{"foreign-label"}
				state["assignees"] = []string{"Descriptive human"}
				metadata := obj(state["metadata"])
				metadata["foreign/note"] = "preserved"
			}); err != nil {
				return err
			}
			comment, err := aggregate.call(ctx, "comment", object{"external_ref": previous, "operation_id": "fixture-comment-" + strconv.Itoa(i), "expected_observation": 0, "author": "Fixture human", "body": "Representative current discussion 雪\n"}, 0)
			if err != nil {
				return err
			}
			entry := obj(comment["entry"])
			if entry["body"] != "Representative current discussion 雪\n" || entry["author"] != "Fixture human" || entry["signing_actor"] != aggregate.actor || entry["operation_marker"] != "fixture-comment-"+strconv.Itoa(i) {
				return errors.New("performance setup comment differs from original request")
			}
			if _, err = proof(entry["proof"], "original-operation/1"); err != nil {
				return err
			}
			r.performanceComments = append(r.performanceComments, entry)
			head, current, err := aggregate.head(ctx, previous)
			if err != nil {
				return err
			}
			r.performanceStates[i-1] = current
			r.performanceHeads[i-1] = head
		}
		// The template is disposable public setup; only captured public input/output
		// survives. No private identity data is opened or copied into the aggregate.
		if err = os.RemoveAll(template.directory); err != nil {
			return err
		}
	}
	r.performance = aggregate
	r.measure("setup_seconds", time.Since(started).Seconds())
	r.measure("setup_mode", "compiled-single-issue-template-and-public-exact-opening-copy")
	r.measure("fixture_counts", object{"definitions": count, "ordered_criteria": count * 2, "blocking_edges": min(count-1, 20), "unrelated_edges": min(count-1, 20), "attributed_comments": min(count-1, 20), "details_page_size": 8})
	return nil
}
func (r *scenarioRunner) performanceScenario(ctx context.Context, id string) (resultErr error) {
	if err := r.preparePerformance(ctx); err != nil {
		return err
	}
	f := r.performance
	before, err := f.refs(ctx)
	if err != nil {
		return err
	}
	defer func() {
		after, err := f.refs(ctx)
		if err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("measurement final refs unavailable: %w", err))
			return
		}
		if !bytes.Equal(before, after) {
			resultErr = errors.Join(resultErr, errors.New("measurement mutated signed refs"))
		}
	}()
	r.measure("host", object{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU(), "race": false, "toolchain": hubnot.QualifiedProviderIdentity().Toolchain})
	load, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return err
	}
	r.measure("loadavg", strings.TrimSpace(string(load)))
	started := time.Now()
	switch id {
	case "performance.inventory-1000":
		inventory, err := f.call(ctx, "inventory", nil, 0)
		if err != nil {
			return err
		}
		seconds := time.Since(started).Seconds()
		r.measure("consumer_seconds", seconds)
		if seconds > 120 {
			return fmt.Errorf("compiled complete inventory exceeded120seconds: %.6f", seconds)
		}
		compareStart := time.Now()
		items := list(inventory["items"])
		if len(items) != 1000 {
			return errors.New("timed inventory incomplete")
		}

		if err = r.verifyPerformanceInventory(inventory); err != nil {
			return err
		}

		r.measure("comparison_seconds", time.Since(compareStart).Seconds())
		r.measure("rows", len(items))
		if err = r.verifyPerformanceComments(ctx); err != nil {
			return err
		}
		// Filtered tracing is a separate query; it cannot replace untraced admission
		// latency or change the adapter's120second budget.
		trace, err := exec.LookPath("strace")
		if err != nil {
			return prerequisite("filtered process census requires strace")
		}
		trace, err = filepath.Abs(trace)
		if err != nil {
			return err
		}
		tracePath := filepath.Join(r.options.EvidenceDir, "inventory.execve.trace")
		output, err := r.traceInventory(ctx, f, trace, tracePath)
		if err != nil {
			return err
		}
		traced, err := decodeObject(output)
		if err != nil || traced["schema"] != "go-kitty.tracker-result/1" || traced["action"] != "inventory" || traced["outcome"] != "success" || !sameObservedProof(obj(traced["result"])["common_proof"], inventory["common_proof"]) || !equal(obj(traced["result"])["items"], inventory["items"]) {
			return errors.New("traced inventory differs from timed complete result")
		}
		data, err := os.ReadFile(tracePath)
		if err != nil {
			return err
		}
		executions, err := parseExecTrace(string(data))
		if err != nil {
			return err
		}
		pages, gitProcesses := 0, 0
		for _, record := range executions {
			if !record.Success {
				continue
			}
			if record.Path == r.options.HN && len(record.Argv) > 3 && slices.Equal(record.Argv[1:4], []string{"issue", "list", "--details"}) {
				pages++
			}
			if filepath.Base(record.Path) == "git" {
				gitProcesses++
			}
		}

		if pages != 125 {
			return fmt.Errorf("measured page process census %d, expected125", pages)
		}
		r.measure("details_processes", pages)
		r.measure("git_processes", gitProcesses)
	case "performance.known-reference":
		read, err := f.call(ctx, "definition", object{"external_ref": r.performanceRefs[0]}, 0)
		if err != nil {
			return err
		}
		seconds := time.Since(started).Seconds()
		if err = checkDefinition(obj(read["definition"]), r.performanceRequests[0]); err != nil {
			return err
		}
		canonical, err := proof(read["proof"], "known-work-observation/1")
		if err != nil {
			return err
		}
		if !equal(canonical["state"], r.performanceStates[0]) || len(list(canonical["comments"])) != 1 {
			return errors.New("known decorated reference lost native state or comment")
		}
		heads := obj(canonical["source_heads"])
		id := str(r.performanceRefs[0]["external_id"])
		if len(heads) != 1 || !equal(heads[id], []any{r.performanceHeads[0]}) || len(list(canonical["graph"])) != 2 {
			return errors.New("known reference current source head/edge census differs")
		}
		kinds := map[string]bool{}
		for _, row := range list(canonical["graph"]) {
			edge := obj(obj(row)["edge"])
			kind := str(edge["kind"])
			if obj(row)["type"] != "edge" || kinds[kind] || (kind != "blocks" && kind != "related") || edge["source"] != id || edge["target"] != r.performanceRefs[1]["external_id"] || edge["actor"] != r.performance.actor || edge["revision"] != r.performanceHeads[0]["id"] || edge["ambiguous"] != false {
				return errors.New("known reference source attribution differs")
			}
			kinds[kind] = true
		}
		comment := obj(list(canonical["comments"])[0])
		want := r.performanceComments[0]
		if comment["id"] != want["external_comment_id"] || comment["body"] != want["body"] || comment["actor"] != want["signing_actor"] || comment["timestamp"] != want["created_at"] {
			return errors.New("known reference changed signed discussion")
		}
		r.measure("consumer_seconds", seconds)
		return r.traceSeparateQuery(ctx, "performance.known-reference", "definition", read)
	case "performance.original-recovery":
		replay, err := f.call(ctx, "define", r.performanceRequests[len(r.performanceRequests)-1], 0)
		if err != nil {
			return err
		}
		if replay["replayed"] != true || replay["prior_mutation"] != true {
			return errors.New("original opening recovery not historical replay")
		}
		if _, err = proof(obj(replay["observation"])["proof"], "original-operation/1"); err != nil {
			return err
		}
		r.measure("consumer_seconds", time.Since(started).Seconds())
		return r.traceSeparateQuery(ctx, id, "define", replay)
	}

	return nil
}

func validateUnitCensus(raw []byte, pkg string, tests []string) (testcampaign.HubnotUnitCensus, error) {
	return testcampaign.ValidateHubnotUnitOutput(raw, pkg, tests)
}

// traceInventory executes the separate diagnostic query on the prepared fixture.
func (r *scenarioRunner) traceInventory(ctx context.Context, f *publicFixture, trace, tracePath string) ([]byte, error) {
	file, err := os.OpenFile(tracePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	tracedIndex := len(r.row.Commands)
	started := time.Now()
	// Suppress signal notices only; signal delivery and exec tracing are unchanged.
	output, commandErr := r.command(ctx, f.directory, nil, 0, trace, "--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-e", "signal=none", "-o", tracePath, r.options.Product, "--output", "json", "tracker", "inventory", "--binding", f.binding)
	r.measure("traced_seconds", time.Since(started).Seconds())
	r.measure("traced_command", tracedIndex)
	a, artifactErr := fileArtifact(tracePath)
	if artifactErr != nil {
		return output, errors.Join(commandErr, artifactErr)
	}
	r.row.Artifacts = append(r.row.Artifacts, a)
	r.measure("process_trace", a)
	return output, commandErr
}

// Separate query tracing follows the untraced measurement on its existing fixture.
// The exact original argv/input are reused; tracing never supplies admission latency.
func (r *scenarioRunner) traceSeparateQuery(ctx context.Context, id, action string, original object) error {
	untracedIndex := len(r.row.Commands) - 1
	if untracedIndex < 0 {
		return errors.New("untraced query missing")
	}
	untraced := r.row.Commands[untracedIndex]
	if len(untraced.Argv) == 0 || untraced.Argv[0] != r.options.Product || untraced.Stdin == nil {
		return errors.New("untraced query identity missing")
	}
	input, err := os.ReadFile(untraced.Stdin.Path)
	if err != nil || int64(len(input)) != untraced.Stdin.Bytes || shaBytes(input) != untraced.Stdin.SHA256 {
		return errors.New("untraced query input changed")
	}
	trace, err := exec.LookPath("strace")
	if err != nil {
		return prerequisite("separate query census requires strace")
	}
	trace, err = filepath.Abs(trace)
	if err != nil {
		return err
	}
	tracePath := filepath.Join(r.options.EvidenceDir, fmt.Sprintf("%s-%05d.execve.trace", id, r.sequence+1))
	file, err := os.OpenFile(tracePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	args := append([]string{"--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-e", "signal=none", "-o", tracePath}, untraced.Argv...)
	tracedIndex := len(r.row.Commands)
	started := time.Now()
	output, commandErr := r.command(ctx, untraced.Directory, input, 0, trace, args...)
	r.measure("traced_seconds", time.Since(started).Seconds())
	a, err := fileArtifact(tracePath)
	if err != nil {
		return errors.Join(commandErr, err)
	}
	r.row.Artifacts = append(r.row.Artifacts, a)
	r.measure("process_trace", a)
	r.measure("untraced_command", untracedIndex)
	r.measure("traced_command", tracedIndex)
	if commandErr != nil {
		return commandErr
	}
	if _, err = os.Stat(filepath.Join(untraced.Directory, "never-control-root")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("traced query touched control root")
	}
	envelope, err := decodeObject(output)
	if err != nil || envelope["schema"] != "go-kitty.tracker-result/1" || envelope["action"] != action || envelope["outcome"] != "success" || !sameSeparateQueryResult(action, original, obj(envelope["result"])) {
		return errors.New("traced query differs from untraced complete result")
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		return err
	}
	records, err := parseExecTrace(string(data))
	if err != nil {
		return err
	}
	counts := map[string]int{"consumer": 0, "hubnot": 0, "git": 0, "other": 0, "failed_exec": 0}
	consumerPID := ""
	childPIDs := map[string]bool{}
	for _, record := range records {
		if record.Success {
			childPIDs[record.PID] = true
		}
		switch {
		case !record.Success:
			counts["failed_exec"]++
		case record.Path == r.options.Product:
			consumerPID = record.PID
			counts["consumer"]++
		case record.Path == r.options.HN:
			counts["hubnot"]++
		case filepath.Base(record.Path) == "git":
			counts["git"]++
		default:
			counts["other"]++
		}
	}
	if counts["consumer"] != 1 || counts["hubnot"] == 0 || counts["git"] == 0 {
		return errors.New("separate query execution census incomplete")
	}
	delete(childPIDs, consumerPID)
	r.measure("observed_exec_child_pids", len(childPIDs))
	r.measure("exec_counts", counts)
	return nil
}

func sameSeparateQueryResult(action string, a, b object) bool {
	kind := "known-work-observation/1"
	if action == "define" {
		kind = "original-operation/1"
	} else if action != "definition" {
		return false
	}
	copies := []object{}
	for _, original := range []object{a, b} {
		raw, err := json.Marshal(original)
		if err != nil {
			return false
		}
		copy, err := decodeObject(raw)
		if err != nil {
			return false
		}
		copies = append(copies, copy)
	}
	proofs := []object{}
	observations := []object{}
	for _, copy := range copies {
		observation := copy
		p := obj(copy["proof"])
		if action == "define" {
			if copy["replayed"] != true || copy["prior_mutation"] != true {
				return false
			}
			observation = obj(copy["observation"])
			p = obj(observation["proof"])
		}
		if _, err := proof(p, kind); err != nil {
			return false
		}
		if observation["observed_at"] != p["observed_at"] {
			return false
		}
		observations = append(observations, observation)
		proofs = append(proofs, p)
	}
	if !sameObservedProof(proofs[0], proofs[1]) {
		return false
	}
	proofs[1]["observed_at"] = proofs[0]["observed_at"]
	observations[1]["observed_at"] = observations[0]["observed_at"]
	return equal(copies[0], copies[1])
}

// Observation time is per invocation; every immutable proof field remains exact.
func sameObservedProof(a, b any) bool {
	x, y := obj(a), obj(b)
	if x == nil || y == nil {
		return false
	}
	left, right := object{}, object{}
	for k, v := range x {
		if k != "observed_at" {
			left[k] = v
		}
	}
	for k, v := range y {
		if k != "observed_at" {
			right[k] = v
		}
	}
	for _, p := range []object{x, y} {
		if _, err := time.Parse(time.RFC3339Nano, str(p["observed_at"])); err != nil {
			return false
		}
	}
	return equal(left, right)
}

type execRecord struct {
	PID, Path string
	Argv      []string
	Success   bool
}

func parseExecTrace(raw string) ([]execRecord, error) {
	pending := map[string]string{}
	var records []execRecord
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, body := fields[0], strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if _, err := strconv.Atoi(pid); err != nil {
			return nil, errors.New("trace lacks explicit process identity")
		}
		if strings.HasPrefix(body, "execve(") {
			if _, exists := pending[pid]; exists {
				return nil, errors.New("overlapping exec trace")
			}
			if strings.Contains(body, "<unfinished ...>") {
				pending[pid] = strings.TrimSuffix(body, "<unfinished ...>")
				continue
			}
		}
		if strings.HasPrefix(body, "<... execve resumed>") {
			prefix, ok := pending[pid]
			if !ok {
				return nil, errors.New("orphan exec completion")
			}
			delete(pending, pid)
			body = prefix + strings.TrimPrefix(body, "<... execve resumed>")
		}
		if !strings.HasPrefix(body, "execve(") {
			continue
		}
		text := strings.TrimPrefix(body, "execve(")
		quoted, err := strconv.QuotedPrefix(text)
		if err != nil {
			return nil, err
		}
		path, err := strconv.Unquote(quoted)
		if err != nil {
			return nil, err
		}
		text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text[len(quoted):]), ","))
		if !strings.HasPrefix(text, "[") {
			return nil, errors.New("trace argv missing")
		}
		text = text[1:]
		var args []string
		for {
			text = strings.TrimSpace(text)
			if strings.HasPrefix(text, "]") {
				text = text[1:]
				break
			}
			q, err := strconv.QuotedPrefix(text)
			if err != nil {
				return nil, err
			}
			arg, err := strconv.Unquote(q)
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			text = strings.TrimSpace(text[len(q):])
			if strings.HasPrefix(text, ",") {
				text = text[1:]
			} else if !strings.HasPrefix(text, "]") {
				return nil, errors.New("truncated trace argv")
			}
		}
		resultAt := strings.LastIndex(text, "= ")
		if resultAt < 0 {
			return nil, errors.New("exec terminal absent")
		}
		result := strings.TrimSpace(text[resultAt+2:])
		if result != "0" && !strings.HasPrefix(result, "-1 ") {
			return nil, errors.New("unknown exec terminal")
		}
		records = append(records, execRecord{PID: pid, Path: path, Argv: args, Success: result == "0"})
	}
	if len(pending) > 0 {
		return nil, errors.New("unfinished exec trace")
	}
	return records, nil
}

func (r *scenarioRunner) verifyPerformanceInventory(inventory object) error {
	canonical, err := proof(inventory["common_proof"], "complete-current-inventory/1")
	if err != nil {
		return err
	}
	items := list(inventory["items"])
	if len(items) != len(r.performanceRefs) {
		return errors.New("complete portable inventory census differs")
	}
	byID := map[string]object{}
	for _, value := range items {
		item := obj(value)
		d := obj(item["definition"])
		if byID[str(d["work_id"])] != nil {
			return errors.New("duplicate inventory WorkID")
		}
		byID[str(d["work_id"])] = item
	}
	for i, request := range r.performanceRequests {
		item := byID[str(request["work_id"])]
		if err = checkDefinition(obj(item["definition"]), request); err != nil {
			return err
		}
		actualRef := obj(item["external_ref"])
		expectedRef := r.performanceRefs[i]
		for _, field := range []string{"project_id", "work_id", "provider", "external_id", "metadata_schema", "created_by_operation"} {
			if !equal(actualRef[field], expectedRef[field]) {
				return fmt.Errorf("inventory reference %s differs", field)
			}
		}
		expected := []string{}
		if i > 0 && i < 21 {
			expected = []string{fmt.Sprintf("performance-%04d", i-1)}
		}
		if !equal(obj(item["definition"])["dependencies"], expected) {
			return errors.New("inventory did not derive current graph dependencies")
		}
	}
	rows := list(canonical["rows"])
	if len(rows) != len(r.performanceRefs) {
		return errors.New("native proof row census differs")
	}
	nativeByID := map[string]object{}
	for _, v := range rows {
		row := obj(v)
		id := str(row["id"])
		if id == "" || nativeByID[id] != nil {
			return errors.New("native proof duplicate identity")
		}
		nativeByID[id] = row
	}
	for i, ref := range r.performanceRefs {
		row := nativeByID[str(ref["external_id"])]
		if row["creator"] != r.performance.actor || row["conflict"] != false || !equal(row["head_count"], 1) || !equal(row["state"], r.performanceStates[i]) || !equal(row["opening_metadata"], r.performanceOpening[i]) {
			return fmt.Errorf("native full state/opening identity differs at fixture %d", i)
		}
	}
	for _, v := range list(inventory["items"]) {
		if !equal(obj(obj(v)["external_ref"])["last_observation"], obj(inventory["common_proof"])["revision"]) {
			return errors.New("inventory reference revision differs from common proof")
		}
	}
	return nil
}
func (r *scenarioRunner) verifyPerformanceComments(ctx context.Context) error {
	for i, want := range r.performanceComments {
		observed, err := r.performance.native(ctx, nil, 0, "issue", "history", str(r.performanceRefs[i]["external_id"]), "--kind", "comments", "--limit", "200", "--json")
		if err != nil {
			return err
		}
		data := obj(observed["data"])
		entries := list(data["items"])
		if len(entries) != 1 || str(observed["next_cursor"]) != "" {
			return errors.New("representative native discussion census differs")
		}
		got := obj(entries[0])
		for field, expected := range map[string]any{"id": want["external_comment_id"], "body": want["body"], "timestamp": want["created_at"], "actor": want["signing_actor"]} {
			if !equal(got[field], expected) {
				return fmt.Errorf("representative native discussion field %s changed", field)
			}
		}
	}
	return nil
}
