package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/playback"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

func TestQualificationCaptureBoundsActualObservedBytes(t *testing.T) {
	canceled := 0
	w := &limitedOutput{limit: 7, overflow: func() { canceled++ }}
	if n, err := w.Write(bytes.Repeat([]byte{'x'}, 1024)); err != nil || n != 1024 {
		t.Fatal("capture accounting lost chunk")
	}
	if len(w.bytes()) != 7 || w.observed != 1024 || !w.exceeded || canceled != 1 {
		t.Fatal("retained or observed bound incorrect")
	}
	w.Write([]byte("more"))
	if len(w.bytes()) != 7 || w.observed != 1028 {
		t.Fatal("overflow forgot known observed bytes")
	}
}
func buildQualifierTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "qualifier")
	c := exec.Command(qualificationTestGo(t), "build", "-trimpath", "-buildvcs=false", "-mod=readonly", "-ldflags=-s -w", "-o", target, testcampaign.HubnotQualificationPackage)
	c.Dir = root
	c.Env = buildEnvironment()
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("external build: %v %s", err, out)
	}
	return target
}
func TestNormalQualifierIsolatesWorkerAndPreservesMissingPrerequisite(t *testing.T) {
	probe := exec.Command("unshare", "--user", "--map-root-user", "--net", "--", "true")
	if err := probe.Run(); err != nil {
		t.Skip("host lacks unprivileged namespaces; active qualification must refuse")
	}
	binary := buildQualifierTest(t)
	evidence := filepath.Join(t.TempDir(), "attempt")
	c := exec.Command(binary, "--scope", "identity-smoke", "--repository", "/missing/qualified-source", "--evidence-dir", evidence, "--timeout", "30s")
	poison := t.TempDir()
	marker := filepath.Join(poison, "ambient-launcher-executed")
	if err := os.WriteFile(filepath.Join(poison, "unshare"), []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\nexit 92\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c.Env = append(buildEnvironment(), "PATH="+poison)
	if err := c.Run(); err == nil {
		t.Fatal("missing prerequisite admitted")
	}
	raw, err := os.ReadFile(filepath.Join(evidence, "qualification.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("ambient launcher executed")
	}
	var e testcampaign.HubnotEvidence
	if err = json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.Verdict != "non_admissible" || e.FullAdmissible || !e.Isolation.Verified || e.Isolation.ParentNamespace == e.Isolation.ChildNamespace || len(e.Results) != 0 {
		t.Fatalf("missing prerequisite/isolation result: %+v", e)
	}
	before := bytes.Clone(raw)
	if err = c.Run(); err == nil {
		t.Fatal("same attempt directory overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(evidence, "qualification.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("prior failure receipt overwritten")
	}
	direct := exec.Command(binary, "__worker")
	if err = direct.Run(); err == nil {
		t.Fatal("direct worker bypass admitted")
	}
}
func TestQualificationCommandDeadlineRetainsArtifacts(t *testing.T) {
	o := qualificationOptions{EvidenceDir: t.TempDir()}
	r := newScenarioRunner(o)
	r.row = &testcampaign.HubnotScenarioResult{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := r.command(ctx, t.TempDir(), nil, 0, "/bin/sh", "-c", "printf started; sleep 30")
	if err == nil || len(r.row.Commands) != 1 {
		t.Fatal("deadline failed to retain command")
	}
	c := r.row.Commands[0]
	out, err := os.ReadFile(c.Stdout.Path)
	if err != nil || string(out) != "started" || c.Exit == 0 {
		t.Fatal("deadline lost partial output/exit")
	}
}
func realQualificationWrapper(t *testing.T, scope string) {
	t.Helper()
	if os.Getenv("GO_KITTY_HUBNOT_RUN") != "1" {
		t.Skip("explicit real qualification activation required")
	}
	qualifier := os.Getenv("GO_KITTY_HUBNOT_QUALIFIER")
	root := os.Getenv("GO_KITTY_HUBNOT_EVIDENCE_ROOT")
	if !filepath.IsAbs(root) || !filepath.IsAbs(qualifier) {
		t.Fatal("active qualification requires explicit qualifier and external evidence root")
	}
	evidence := filepath.Join(root, scope)
	args := []string{"--scope", scope, "--repository", os.Getenv("GO_KITTY_HUBNOT_REPOSITORY"), "--product", os.Getenv("GO_KITTY_HUBNOT_PRODUCT"), "--hn", os.Getenv("GO_KITTY_HUBNOT_BINARY"), "--go-bin", os.Getenv("GO_KITTY_HUBNOT_GO_BINARY"), "--evidence-dir", evidence, "--timeout", "55m"}
	if scope == "full-local" {
		args = append(args, "--original-repository", os.Getenv("GO_KITTY_HUBNOT_ORIGINAL_REPOSITORY"))
	}
	c := exec.Command(qualifier, args...)
	c.Env = buildEnvironment()
	output, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("real qualifier failed (retained %s): %v %s", evidence, err, output)
	}
	raw, err := os.ReadFile(filepath.Join(evidence, "qualification.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result testcampaign.HubnotEvidence
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if scope == "full-local" {
		err = testcampaign.ValidateHubnotEvidence(result)
	} else {
		err = testcampaign.ValidateHubnotSelectionEvidence(result, scope)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = testcampaign.VerifyHubnotArtifacts(result); err != nil {
		t.Fatal(err)
	}
}
func TestRealHubnotQualification(t *testing.T)   { realQualificationWrapper(t, "full-local") }
func TestRealHubnotHostedProvider(t *testing.T)  { realQualificationWrapper(t, "hosted-provider") }
func TestRealHubnotMissionIdentity(t *testing.T) { realQualificationWrapper(t, "identity-smoke") }

// This test-only worker injects a stalled child through the production command
// supervisor. It cannot emit a passing qualification receipt.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "__worker" {
		syscall.CloseOnExec(3)
		syscall.CloseOnExec(4)
		input := os.NewFile(3, "fault-request")
		var req workerRequest
		if input == nil || json.NewDecoder(input).Decode(&req) != nil {
			os.Exit(72)
		}
		input.Close()
		r := newScenarioRunner(req.Options)
		r.row = &testcampaign.HubnotScenarioResult{Scenario: testcampaign.HubnotScenario{ID: "fault.active-command"}}
		r.runChallenge = req.Challenge
		_, _ = r.command(context.Background(), req.Options.EvidenceDir, []byte("owner-input-intent\n"), 0, "/bin/sh", "-c", `read -r got; [ "$got" = owner-input-intent ] || exit 74; printf "owner-stdout-prefix\n"; printf "owner-stderr-prefix\n" >&2; while read key host rest; do if [ "$key" = NSpid: ]; then echo "$host" > "$1"; break; fi; done < /proc/self/status; exec sleep 30`, "fault-child", filepath.Join(req.Options.EvidenceDir, "child.pid"))
		os.Exit(73)
	}
	os.Exit(m.Run())
}
func TestQualificationCancellationRemovesDescendantGroups(t *testing.T) {
	root := t.TempDir()
	evidence := filepath.Join(root, "attempt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- qualificationMain(ctx, []string{"--scope", "identity-smoke", "--evidence-dir", evidence, "--timeout", "10s"}, io.Discard, io.Discard)
	}()
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(evidence, "child.pid"))
		if err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			if pid > 1 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid <= 1 {
		cancel()
		<-done
		t.Fatal("fault descendant did not start")
	}
	defer syscall.Kill(-pid, syscall.SIGKILL)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor stuck")
	}
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err == nil && !strings.Contains(string(raw), ") Z ") {
		t.Fatalf("descendant survived: %s", raw)
	}
	for suffix, want := range map[string]string{"stdin": "owner-input-intent\n", "stdout": "owner-stdout-prefix\n", "stderr": "owner-stderr-prefix\n"} {
		got, err := os.ReadFile(filepath.Join(evidence, "00001."+suffix))
		if err != nil || string(got) != want {
			t.Fatalf("hard cancellation lost %s: %q %v", suffix, got, err)
		}
	}
	var intent, launch, receipt object
	for name, dest := range map[string]*object{"00001.start.json": &intent, "00001.launched.json": &launch, "qualification.json": &receipt} {
		data, err := os.ReadFile(filepath.Join(evidence, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, dest); err != nil {
			t.Fatal(err)
		}
	}
	if intent["phase"] != "prelaunch_intent" || intent["scenario"] != "fault.active-command" || intent["run_directory"] != evidence || len(str(intent["run_challenge"])) != 64 || str(list(intent["argv"])[0]) != "/bin/sh" || obj(intent["stdin"])["sha256"] != shaBytes([]byte("owner-input-intent\n")) {
		t.Fatalf("launch intent lost identity: %+v", intent)
	}
	if launch["phase"] != "process_start_confirmed" || launch["intent_path"] != filepath.Join(evidence, "00001.start.json") {
		t.Fatalf("missing confirmed start: %+v", launch)
	}
	for _, record := range []object{intent, launch} {
		for _, forbidden := range []string{"exit", "finished_at", "stdout_sha256", "stderr_sha256"} {
			if _, exists := record[forbidden]; exists {
				t.Fatal("incomplete command invented terminal evidence")
			}
		}
	}
	if _, err := os.Stat(filepath.Join(evidence, "00001.terminal.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("killed worker invented terminal record")
	}
	if receipt["verdict"] != "non_admissible" || receipt["full_admissible"] != false {
		t.Fatal("interrupted run admitted")
	}
}

func TestQualificationProductionPlaybackMaterializedCheckpoint(t *testing.T) {
	registry := journal.DefaultEventRegistryV2()
	reducers, err := productionruntime.BuildReducerSetV2(registry)
	if err != nil {
		t.Fatal(err)
	}
	eventDescriptor, err := registry.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	reducerDescriptor, err := reducers.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	// Inspect the actual production descriptor, never replace its callbacks.
	var descriptor struct {
		EventRegistrySHA256 string `json:"event_registry_sha256"`
		Reducers            []struct {
			Family             string `json:"family"`
			EventSchemaVersion uint32 `json:"event_schema_version"`
			ReducerVersion     uint32 `json:"reducer_version"`
			Owner              string `json:"owner"`
			Implementation     string `json:"implementation_sha256"`
		} `json:"reducers"`
	}
	if err = json.Unmarshal(reducerDescriptor.CanonicalBytes(), &descriptor); err != nil {
		t.Fatal(err)
	}
	tuples := journal.ExpectedEventTuples()
	if descriptor.EventRegistrySHA256 != eventDescriptor.Digest().String() || len(descriptor.Reducers) != len(tuples) {
		t.Fatal("production descriptor is incomplete")
	}
	seen := map[string]bool{}
	for _, got := range descriptor.Reducers {
		found := false
		for _, want := range tuples {
			if got.Family == want.Family && got.EventSchemaVersion == want.EventSchemaVersion && got.ReducerVersion == want.ReducerVersion && got.Owner == want.Owner {
				found = true
			}
		}
		if !found || seen[got.Family] || len(got.Implementation) != 64 {
			t.Fatalf("unexpected production reducer descriptor: %+v", got)
		}
		seen[got.Family] = true
		switch got.Family {
		case "mission":
			if got.Implementation != mission.MissionReducerImplementationDigestV2().String() {
				t.Fatal("mission implementation identity")
			}
		case "operation":
			if got.Implementation != journal.OperationReducerImplementationDigestV2().String() {
				t.Fatal("operation implementation identity")
			}
		case "runtime_host":
			if got.Implementation != productionruntime.RuntimeReducerRegistrationV2().ImplementationDigest.String() {
				t.Fatal("runtime implementation identity")
			}
		}
	}
	digest := func(s string) journal.Digest { return journal.Digest(sha256.Sum256([]byte(s))) }
	input := mission.MissionAuthorityStateInputV2{ProjectID: "project-witness", MissionID: "mission-witness", DefinitionRevision: "definition-1", DefinitionDigest: digest("definition"), Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "planning", LastOperationID: "operation-create", ObservedAt: "2026-09-09T12:00:00Z"}
	created, err := mission.NewMissionAuthorityStateV2(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Version = 2
	input.ActiveBlockerIDs = []string{"blocker-witness"}
	input.LastOperationID = "operation-blocker"
	input.ObservedAt = "2026-09-09T12:01:00Z"
	blocked, err := mission.NewMissionAuthorityStateV2(input)
	if err != nil {
		t.Fatal(err)
	}
	payload := func(kind mission.MissionEventTypeV2, prior, next mission.MissionAuthorityStateV2) []byte {
		t.Helper()
		p, e := mission.NewMissionEventPayloadV2(kind, prior, next)
		if e != nil {
			t.Fatal(e)
		}
		b, e := mission.EncodeMissionEventPayloadV2(p)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	scope := journal.Scope{Kind: journal.ScopeMission, ProjectID: "project-witness", MissionID: "mission-witness"}
	coord, err := journal.NewAggregateCoordinate("mission", "mission-witness", scope)
	if err != nil {
		t.Fatal(err)
	}
	runtimeCoord, err := journal.NewAggregateCoordinate("runtime_host", "host-witness", journal.Scope{Kind: journal.ScopeHost})
	if err != nil {
		t.Fatal(err)
	}
	opCoord, err := journal.NewAggregateCoordinate("operation", "operation-blocker", journal.Scope{Kind: journal.ScopeHost})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := journal.NewOperationStateV2(journal.OperationStateInput{OperationID: "operation-blocker", LogicalVersion: 1, Status: journal.StatusAccepted, EventIDs: []journal.EventID{"event-blocker"}})
	if err != nil {
		t.Fatal(err)
	}
	opPayload, err := journal.EncodeOperationStateEventPayloadV2(operation)
	if err != nil {
		t.Fatal(err)
	}
	materialize := func(revision uint64, id journal.EventID, kind, family string, schema, reducer uint32, coordinate journal.AggregateCoordinate, version journal.LogicalVersion, op journal.OperationID, payload []byte) playback.MaterializedEventV2 {
		t.Helper()
		event, e := journal.NewEventEnvelopeV2(journal.EventEnvelopeInput{EventID: id, EventType: kind, Family: family, EventSchemaVersion: schema, ReducerVersion: reducer, Coordinate: coordinate, AggregateLogicalVersion: version, OperationID: op, OperationLogicalVersion: 1, PublicationID: journal.PublicationID(digest(string(id)).String()), CommittedRuntimeEpochID: "epoch-witness", EventIndex: 0, EventCount: 1, Payload: payload, Observations: []byte(`{}`)}, registry)
		if e != nil {
			t.Fatal(e)
		}
		m, e := playback.NewMaterializedEventV2(revision, event, false)
		if e != nil {
			t.Fatal(e)
		}
		return m
	}
	runtimePayload := []byte(`{"database_id":"database-witness","host_authority_id":"host-witness","lifecycle":"ready","runtime_epoch_id":"epoch-witness","schema_version":2}`)
	events := []playback.MaterializedEventV2{
		materialize(10, "event-runtime", "runtime_host.epoch_admitted", "runtime_host", 1, 1, runtimeCoord, 1, "operation-runtime", runtimePayload),
		materialize(20, "event-create", string(mission.MissionEventCreatedV2), "mission", 1, 1, coord, 1, "operation-create", payload(mission.MissionEventCreatedV2, mission.MissionAuthorityStateV2{}, created)),
		materialize(30, "event-operation", "operation_transitioned", "operation", 2, 2, opCoord, 1, "operation-blocker", opPayload),
		materialize(40, "event-blocker", string(mission.MissionEventBlockerRaisedV2), "mission", 1, 1, coord, 2, "operation-blocker", payload(mission.MissionEventBlockerRaisedV2, created, blocked)),
	}
	run := func(base []playback.StateV2, events []playback.MaterializedEventV2) playback.ResultV2 {
		t.Helper()
		got, e := playback.RunV2(playback.InputV2{Base: base, Events: events, EventRegistry: registry, Reducers: reducers, ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest(), MaximumWorkers: 2})
		if e != nil {
			t.Fatal(e)
		}
		if e = got.Validate(); e != nil {
			t.Fatal(e)
		}
		if got.EventRegistryDigest() != eventDescriptor.Digest() || got.ReducerRegistryDigest() != reducerDescriptor.Digest() {
			t.Fatal("playback descriptor drift")
		}
		return got
	}
	origin := run(nil, events)
	prefix := run(nil, events[:2])
	// A materialized checkpoint prefix carries canonical states, logical versions,
	// last event IDs, state digests and publication IDs; no storage is opened.
	restored := run(prefix.States(), events[2:])
	if origin.CanonicalProjectionDigest() != restored.CanonicalProjectionDigest() || origin.ThroughRecordRevision() != 40 || restored.ThroughRecordRevision() != 40 || origin.EventCount() != 4 || restored.EventCount() != 2 {
		t.Fatal("origin/checkpoint suffix mismatch")
	}
	if len(origin.States()) != 3 || len(restored.States()) != 3 {
		t.Fatal("projection census")
	}
	for i, left := range origin.States() {
		right := restored.States()[i]
		if left.Coordinate() != right.Coordinate() || left.LogicalVersion() != right.LogicalVersion() || left.LastEventID() != right.LastEventID() || left.LastPublicationID() != right.LastPublicationID() || left.StateDigest() != right.StateDigest() || left.Tombstone() != right.Tombstone() || !bytes.Equal(left.CanonicalState(), right.CanonicalState()) {
			t.Fatal("projection field mismatch")
		}
	}
	for _, result := range []playback.ResultV2{origin, restored} {
		projection, e := mission.NewMissionRestoredProjectionV2(result)
		if e != nil {
			t.Fatal(e)
		}
		got, found := projection.Mission("project-witness", "mission-witness")
		if !found || !bytes.Equal(got.CanonicalBytes(), blocked.CanonicalBytes()) || got.Version() != 2 || got.Status() != mission.AuthorityMissionActiveV2 || !reflect.DeepEqual(got.ActiveBlockerIDs(), []string{"blocker-witness"}) {
			t.Fatal("typed mission/blocker projection mismatch")
		}
		sawOperation, sawRuntime := false, false
		for _, state := range result.States() {
			switch state.Coordinate().Kind {
			case "operation":
				got, e := journal.DecodeOperationStateV2(state.CanonicalState())
				if e != nil {
					t.Fatal(e)
				}
				if got.OperationID() != "operation-blocker" || got.LogicalVersion() != 1 || got.Status() != journal.StatusAccepted || !reflect.DeepEqual(got.EventIDs(), []journal.EventID{"event-blocker"}) {
					t.Fatal("typed operation projection mismatch")
				}
				sawOperation = true
			case "runtime_host":
				if !bytes.Equal(state.CanonicalState(), runtimePayload) {
					t.Fatal("canonical runtime projection mismatch")
				}
				sawRuntime = true
			}
		}
		if !sawOperation || !sawRuntime {
			t.Fatal("missing actual reducer output")
		}
	}
	t.Logf("production tuples=%d event_registry=%s reducer_registry=%s projection=%s origin_events=%d suffix_events=%d states=%d", len(tuples), eventDescriptor.Digest(), reducerDescriptor.Digest(), origin.CanonicalProjectionDigest(), origin.EventCount(), restored.EventCount(), len(restored.States()))
}

func TestQualificationDurableCaptureBoundsAndStorageFailure(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "capture")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	canceled := 0
	capture := &limitedOutput{limit: 7, sink: file, overflow: func() { canceled++ }}
	capture.Write(bytes.Repeat([]byte{'x'}, 1024))
	capture.Write([]byte("more"))
	got, err := os.ReadFile(file.Name())
	if err != nil || string(got) != "xxxxxxx" || capture.observed != 1028 || !capture.exceeded || canceled != 2 {
		t.Fatal("streamed retention bound/accounting incorrect")
	}
	file.Close()
	failed := &limitedOutput{limit: 10, sink: file, overflow: func() { canceled++ }}
	failed.Write([]byte("lost"))
	if failed.sinkErr == nil || canceled != 3 {
		t.Fatal("storage failure did not cancel")
	}
}

func TestQualificationCommandLaunchFailureKeepsIntent(t *testing.T) {
	r := newScenarioRunner(qualificationOptions{EvidenceDir: t.TempDir()})
	r.row = &testcampaign.HubnotScenarioResult{}
	if _, err := r.command(context.Background(), t.TempDir(), []byte("input"), 0, "/missing/command"); err == nil {
		t.Fatal("missing command accepted")
	}
	if len(r.row.Commands) != 1 || r.row.Commands[0].Exit != -1 {
		t.Fatal("launch failure lost terminal refusal")
	}
	if _, err := os.Stat(filepath.Join(r.options.EvidenceDir, "00001.launched.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed launch claimed process start")
	}
	raw, err := os.ReadFile(filepath.Join(r.options.EvidenceDir, "00001.terminal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var terminal object
	if err = json.Unmarshal(raw, &terminal); err != nil || terminal["phase"] != "launch_failed" {
		t.Fatal("wrong terminal phase")
	}
}
