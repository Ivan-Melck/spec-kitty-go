package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

var (
	externalGapDBD                  = flag.String("gapdbd", "", "absolute path to the exact admitted gapdbd")
	externalDataRoot                = flag.String("data-root", "", "absolute disposable data root for the external GapDB scenarios")
	externalQualification           = flag.Bool("qualification", false, "fail closed when external scenario prerequisites are absent")
	assertionCandidateGapDBD        = flag.String("assertion-candidate-gapdbd", "", "absolute path to the exact assertion-candidate gapdbd")
	assertionCandidateDataRoot      = flag.String("assertion-candidate-data-root", "", "absolute disposable data root for assertion-candidate scenarios")
	assertionCandidateQualification = flag.Bool("assertion-candidate-qualification", false, "fail closed when assertion-candidate prerequisites are absent")
)

var externalScenarioIDs = []string{
	"data/get-put",
	"conditional/cas-delete",
	"batch/atomic",
	"scan/sorted-prefix",
	"watch/registered-order",
	"expiry/release-authority",
	"ack/memory-visible",
	"ack/durable-restart",
	"inspect/authority",
	"authority/lease",
	"authority/review",
	"authority/workflow",
	"authority/integration",
	"response-loss/reconcile",
}

func TestExternalAdapterScenarios(t *testing.T) {
	if len(externalScenarioIDs) != 14 {
		t.Fatalf("external scenario census=%d", len(externalScenarioIDs))
	}
	configuration, err := externalTestConfiguration()
	if err != nil {
		for _, scenarioID := range externalScenarioIDs {
			t.Run(scenarioID, func(t *testing.T) {
				if qualificationRequested() {
					t.Fatalf("qualification incomplete: %v", err)
				}
				t.Skipf("external precondition unmet: %v", err)
			})
		}
		return
	}
	harness := startExternalHarness(t, configuration)
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Errorf("external owner did not terminate cleanly: %v", err)
		}
	})

	scenarios := map[string]func(*testing.T, *externalHarness){
		"data/get-put":             scenarioGetPut,
		"conditional/cas-delete":   scenarioConditional,
		"batch/atomic":             scenarioBatch,
		"scan/sorted-prefix":       scenarioScan,
		"watch/registered-order":   scenarioWatch,
		"expiry/release-authority": scenarioExpiry,
		"ack/memory-visible":       scenarioMemoryAck,
		"ack/durable-restart":      scenarioDurableRestart,
		"inspect/authority":        scenarioInspect,
		"authority/lease":          authorityScenario(AuthorityLease),
		"authority/review":         authorityScenario(AuthorityReview),
		"authority/workflow":       authorityScenario(AuthorityWorkflow),
		"authority/integration":    authorityScenario(AuthorityIntegration),
		"response-loss/reconcile":  scenarioResponseLoss,
	}
	seen := make(map[string]bool, len(externalScenarioIDs))
	for _, scenarioID := range externalScenarioIDs {
		run, known := scenarios[scenarioID]
		if !known || seen[scenarioID] {
			t.Fatalf("external scenario census contains unknown or duplicate ID %q", scenarioID)
		}
		seen[scenarioID] = true
		t.Run(scenarioID, func(t *testing.T) {
			run(t, harness)
			harness.assertUniversalAuthorityInvariants(t)
		})
	}
}

type externalConfiguration struct {
	binary   string
	dataRoot string
}

func externalTestConfiguration() (externalConfiguration, error) {
	binaryPath := *externalGapDBD
	dataRoot := *externalDataRoot
	if binaryPath == "" {
		binaryPath = os.Getenv("SPEC_KITTY_M1_GAPDBD")
	}
	if dataRoot == "" {
		dataRoot = os.Getenv("SPEC_KITTY_M1_DATA_ROOT")
	}
	if binaryPath == "" || dataRoot == "" {
		return externalConfiguration{}, errors.New("both -gapdbd and -data-root must be explicit (or their SPEC_KITTY_M1_* development equivalents)")
	}
	if !filepath.IsAbs(binaryPath) || !filepath.IsAbs(dataRoot) {
		return externalConfiguration{}, errors.New("sidecar and data-root paths must be absolute")
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return externalConfiguration{}, fmt.Errorf("admitted sidecar is inaccessible: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != 5_300_621 {
		return externalConfiguration{}, errors.New("sidecar type or size does not match the admitted artifact")
	}
	contents, err := os.ReadFile(binaryPath)
	if err != nil {
		return externalConfiguration{}, fmt.Errorf("admitted sidecar cannot be hashed: %w", err)
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != AdmittedSidecarSHA256 {
		return externalConfiguration{}, errors.New("sidecar digest does not match the admitted artifact")
	}
	rootInfo, err := os.Stat(dataRoot)
	if err != nil || !rootInfo.IsDir() {
		return externalConfiguration{}, errors.New("data-root must name an existing directory")
	}
	return externalConfiguration{binary: binaryPath, dataRoot: dataRoot}, nil
}

func qualificationRequested() bool {
	if *externalQualification {
		return true
	}
	value, _ := strconv.ParseBool(os.Getenv("SPEC_KITTY_M1_QUALIFICATION"))
	return value
}

func assertionCandidateTestConfiguration() (externalConfiguration, error) {
	binaryPath := *assertionCandidateGapDBD
	dataRoot := *assertionCandidateDataRoot
	if binaryPath == "" {
		binaryPath = os.Getenv("SPEC_KITTY_ASSERTION_CANDIDATE_GAPDBD")
	}
	if dataRoot == "" {
		dataRoot = os.Getenv("SPEC_KITTY_ASSERTION_CANDIDATE_DATA_ROOT")
	}
	if binaryPath == "" || dataRoot == "" {
		return externalConfiguration{}, errors.New("assertion-candidate sidecar and data-root must be explicit")
	}
	if !filepath.IsAbs(binaryPath) || !filepath.IsAbs(dataRoot) {
		return externalConfiguration{}, errors.New("assertion-candidate sidecar and data-root paths must be absolute")
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return externalConfiguration{}, fmt.Errorf("assertion-candidate sidecar is inaccessible: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != AssertionCandidateSidecarBytes {
		return externalConfiguration{}, errors.New("sidecar type or size does not match the assertion candidate")
	}
	contents, err := os.ReadFile(binaryPath)
	if err != nil {
		return externalConfiguration{}, fmt.Errorf("assertion-candidate sidecar cannot be hashed: %w", err)
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != AssertionCandidateSidecarSHA256 {
		return externalConfiguration{}, errors.New("sidecar digest does not match the assertion candidate")
	}
	rootInfo, err := os.Stat(dataRoot)
	if err != nil || !rootInfo.IsDir() {
		return externalConfiguration{}, errors.New("assertion-candidate data-root must name an existing directory")
	}
	return externalConfiguration{binary: binaryPath, dataRoot: dataRoot}, nil
}

func assertionCandidateQualificationRequested() bool {
	if *assertionCandidateQualification {
		return true
	}
	value, _ := strconv.ParseBool(os.Getenv("SPEC_KITTY_ASSERTION_CANDIDATE_QUALIFICATION"))
	return value
}

type externalHarness struct {
	configuration externalConfiguration
	directory     string
	socket        string
	command       *exec.Cmd
	client        *gapdbapi.Client
	adapter       *Adapter
	databaseID    string
	authorities   map[AuthorityNamespace]string
	mu            sync.Mutex
}

func startExternalHarness(t *testing.T, configuration externalConfiguration) *externalHarness {
	t.Helper()
	directory, err := os.MkdirTemp(configuration.dataRoot, "wp03-gapdb-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	harness := &externalHarness{
		configuration: configuration,
		directory:     directory,
		socket:        filepath.Join(directory, "gapdb.sock"),
		authorities:   make(map[AuthorityNamespace]string),
	}
	if err := harness.start(); err != nil {
		_ = os.RemoveAll(directory)
		t.Fatal(err)
	}
	return harness
}

func (h *externalHarness) start() error {
	command := exec.Command(h.configuration.binary,
		"-db", filepath.Join(h.directory, "database"),
		"-socket", h.socket,
		"-json=true",
		"-read-timeout=5s",
		"-write-timeout=5s",
		"-idle-timeout=30s",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &boundedBuffer{maximum: MaxDiagnosticBytes}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return err
	}
	ready := make(chan error, 1)
	go func() {
		var frame struct {
			SchemaVersion uint16 `json:"schema_version"`
			Ready         bool   `json:"ready"`
			SocketPath    string `json:"socket_path"`
		}
		decoder := json.NewDecoder(io.LimitReader(stdout, MaxDiagnosticBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&frame); err != nil {
			ready <- fmt.Errorf("readiness frame unavailable: %w", err)
			return
		}
		if frame.SchemaVersion != AdmittedWireVersion || !frame.Ready || frame.SocketPath != h.socket {
			ready <- errors.New("readiness frame does not match configured endpoint")
			return
		}
		ready <- nil
	}()
	select {
	case err := <-ready:
		if err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return fmt.Errorf("%w; daemon diagnostic=%s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		return errors.New("timed out waiting for typed readiness")
	}
	client, err := gapdbapi.Dial(h.socket, gapdbapi.ClientOptions{Timeout: 5 * time.Second})
	if err != nil {
		_ = command.Process.Signal(os.Interrupt)
		_ = command.Wait()
		return err
	}
	adapter, err := New(client, h.socket)
	if err != nil {
		_ = client.Close()
		_ = command.Process.Signal(os.Interrupt)
		_ = command.Wait()
		return err
	}
	h.command, h.client, h.adapter = command, client, adapter
	inspection, err := h.adapter.Inspect(context.Background())
	if err != nil {
		_ = h.stop()
		return err
	}
	if h.databaseID != "" && h.databaseID != inspection.DatabaseID {
		_ = h.stop()
		return errors.New("database identity changed across restart")
	}
	h.databaseID = inspection.DatabaseID
	return nil
}

func (h *externalHarness) stop() error {
	if h.client != nil {
		_ = h.client.Close()
		h.client = nil
		h.adapter = nil
	}
	if h.command == nil {
		return nil
	}
	command := h.command
	h.command = nil
	if err := command.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("sidecar exit: %w", err)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-done
		return errors.New("sidecar did not terminate after interrupt")
	}
	if _, err := os.Stat(h.socket); !errors.Is(err, os.ErrNotExist) {
		return errors.New("sidecar terminated without removing its socket")
	}
	return nil
}

func (h *externalHarness) restart() error {
	if err := h.stop(); err != nil {
		return err
	}
	return h.start()
}

func (h *externalHarness) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	stopErr := h.stop()
	removeErr := os.RemoveAll(h.directory)
	return errors.Join(stopErr, removeErr)
}

func (h *externalHarness) assertUniversalAuthorityInvariants(t *testing.T) {
	t.Helper()
	inspection, err := h.adapter.Inspect(context.Background())
	if err != nil {
		t.Fatalf("authority invariant inspect: %v", err)
	}
	if inspection.DatabaseID != h.databaseID || inspection.CurrentRevision < inspection.DurableThrough || inspection.Lifecycle != string(gapdbapi.LifecycleReady) {
		t.Fatalf("authority invariant inspection=%+v database=%s", inspection, h.databaseID)
	}
	for namespace, expectedKey := range h.authorities {
		prefix := "spk/v1/external/mission/heads/" + string(namespace) + "/"
		page, err := h.adapter.Scan(context.Background(), ScanRequest{Prefix: prefix, Limit: MaxScanRecords})
		if err != nil {
			t.Fatalf("authority invariant scan %s: %v", namespace, err)
		}
		matches := 0
		for _, record := range page.Records {
			if !strings.HasPrefix(record.Key, prefix) {
				t.Fatalf("authority namespace crossover: %q under %q", record.Key, prefix)
			}
			if record.Key == expectedKey {
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("authority %s has %d current records for %q", namespace, matches, expectedKey)
		}
	}
}

func scenarioGetPut(t *testing.T, h *externalHarness) {
	result, err := h.adapter.ConditionalMutate(t.Context(), "external-get-put", put("external/data", []byte{0, 1, 2, 255}, ConditionAny, 0), AckMemory)
	if err != nil || result.Revision == 0 || result.Ack != AckMemory {
		t.Fatalf("put result=%+v err=%v", result, err)
	}
	record, err := h.adapter.Get(t.Context(), "external/data")
	if err != nil || !bytes.Equal(record.Value, []byte{0, 1, 2, 255}) || record.Revision != result.Revision {
		t.Fatalf("get record=%+v err=%v", record, err)
	}
}

func scenarioConditional(t *testing.T, h *externalHarness) {
	winner, err := h.adapter.ConditionalMutate(t.Context(), "external-conditional-create", put("external/conditional", []byte("one"), ConditionAbsent, 0), AckDurable)
	requireDurable(t, winner, err)
	if _, err := h.adapter.ConditionalMutate(t.Context(), "external-conditional-loser", put("external/conditional", []byte("loser"), ConditionAbsent, 0), AckDurable); !IsKind(err, OutcomeConflict) {
		t.Fatalf("absent loser=%v", err)
	}
	if _, err := h.adapter.ConditionalMutate(t.Context(), "external-cas-stale", put("external/conditional", []byte("bad"), ConditionRevision, winner.Revision+1), AckDurable); !IsKind(err, OutcomeConflict) {
		t.Fatalf("stale CAS=%v", err)
	}
	replaced, err := h.adapter.ConditionalMutate(t.Context(), "external-cas-winner", put("external/conditional", []byte("two"), ConditionRevision, winner.Revision), AckDurable)
	requireDurable(t, replaced, err)
	staleDelete := Mutation{Kind: MutationDelete, Key: "external/conditional", Condition: Condition{Kind: ConditionRevision, ExpectedRevision: winner.Revision}}
	if _, err := h.adapter.ConditionalMutate(t.Context(), "external-delete-stale", staleDelete, AckDurable); !IsKind(err, OutcomeConflict) {
		t.Fatalf("stale delete=%v", err)
	}
	delete := Mutation{Kind: MutationDelete, Key: "external/conditional", Condition: Condition{Kind: ConditionRevision, ExpectedRevision: replaced.Revision}}
	deleted, err := h.adapter.ConditionalMutate(t.Context(), "external-delete-winner", delete, AckDurable)
	requireDurable(t, deleted, err)
	if _, err := h.adapter.Get(t.Context(), "external/conditional"); !IsKind(err, OutcomeNotFound) {
		t.Fatalf("deleted record remains: %v", err)
	}
}

func scenarioBatch(t *testing.T, h *externalHarness) {
	batch := ProductBatch{
		OperationID: "external-batch", Operation: put("external/batch/operation", []byte("accepted"), ConditionAbsent, 0),
		Events:         []Mutation{put("external/batch/event", []byte("event"), ConditionAbsent, 0)},
		AggregateHeads: []Mutation{put("external/batch/head", []byte("head"), ConditionAbsent, 0)},
	}
	result, err := h.adapter.AtomicBatch(t.Context(), batch)
	requireDurable(t, result, err)
	for _, key := range []string{"external/batch/operation", "external/batch/event", "external/batch/head"} {
		record, err := h.adapter.Get(t.Context(), key)
		if err != nil || record.Revision != result.Revision {
			t.Fatalf("atomic key=%q record=%+v err=%v", key, record, err)
		}
	}
	loser := ProductBatch{
		OperationID: "external-batch-loser", Operation: put("external/batch/loser-operation", []byte("must-not-publish"), ConditionAbsent, 0),
		Events:         []Mutation{put("external/batch/loser-event", []byte("must-not-publish"), ConditionAbsent, 0)},
		AggregateHeads: []Mutation{put("external/batch/head", []byte("conflict"), ConditionAbsent, 0)},
	}
	if _, err := h.adapter.AtomicBatch(t.Context(), loser); !IsKind(err, OutcomeConflict) {
		t.Fatalf("conflicting batch error=%v", err)
	}
	for _, absent := range []string{"external/batch/loser-operation", "external/batch/loser-event"} {
		if _, err := h.adapter.Get(t.Context(), absent); !IsKind(err, OutcomeNotFound) {
			t.Fatalf("conflicting batch partially published %q: %v", absent, err)
		}
	}
}

func scenarioScan(t *testing.T, h *externalHarness) {
	for _, key := range []string{"external/scan/c", "external/scan/a", "external/scan/b"} {
		if _, err := h.adapter.ConditionalMutate(t.Context(), "scan-"+filepath.Base(key), put(key, []byte(key), ConditionAny, 0), AckMemory); err != nil {
			t.Fatal(err)
		}
	}
	first, err := h.adapter.Scan(t.Context(), ScanRequest{Prefix: "external/scan/", Limit: 2})
	if err != nil || !first.Truncated || first.Cursor == "" || len(first.Records) != 2 || first.ObservedRevision == 0 {
		t.Fatalf("first pinned scan page=%+v err=%v", first, err)
	}
	// Advance provider state after the cursor was issued. The old cursor must
	// be rejected as SCAN_STALE rather than mixing two observed revisions.
	if _, err := h.adapter.ConditionalMutate(t.Context(), "scan-d", put("external/scan/d", []byte("external/scan/d"), ConditionAny, 0), AckDurable); err != nil {
		t.Fatal(err)
	}
	if _, err := h.adapter.Scan(t.Context(), ScanRequest{Prefix: "external/scan/", Limit: 2, Cursor: first.Cursor}); !IsKind(err, OutcomeConflict) {
		t.Fatalf("stale scan cursor error=%v", err)
	}

	var records []Record
	cursor := ""
	var observed uint64
	for {
		page, err := h.adapter.Scan(t.Context(), ScanRequest{Prefix: "external/scan/", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if observed == 0 {
			observed = page.ObservedRevision
		} else if page.ObservedRevision != observed {
			t.Fatalf("restarted scan mixed revisions: first=%d page=%d", observed, page.ObservedRevision)
		}
		records = append(records, page.Records...)
		if !page.Truncated {
			break
		}
		cursor = page.Cursor
	}
	if len(records) != 4 {
		t.Fatalf("scan record count=%d records=%+v", len(records), records)
	}
	keys := make([]string, len(records))
	for index := range records {
		keys[index] = records[index].Key
	}
	if !sort.StringsAreSorted(keys) || fmt.Sprint(keys) != "[external/scan/a external/scan/b external/scan/c external/scan/d]" {
		t.Fatalf("scan=%v", keys)
	}
}

func scenarioWatch(t *testing.T, h *externalHarness) {
	inspection, err := h.adapter.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	watch, err := h.adapter.Watch(t.Context(), "external/watch/", inspection.CurrentRevision)
	if err != nil || watch.RegistrationRevision() < inspection.CurrentRevision {
		t.Fatalf("watch registration=%v err=%v", watch, err)
	}
	defer watch.Close()
	result, err := h.adapter.ConditionalMutate(t.Context(), "external-watch-put", put("external/watch/event", []byte("event"), ConditionAny, 0), AckDurable)
	requireDurable(t, result, err)
	nextCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	event, err := watch.Next(nextCtx)
	if err != nil || event.Key != "external/watch/event" || event.Revision != result.Revision {
		t.Fatalf("watch event=%+v err=%v", event, err)
	}
	record, err := h.adapter.Get(t.Context(), event.Key)
	if err != nil || record.Revision != result.Revision {
		t.Fatalf("watch reconciliation record=%+v err=%v", record, err)
	}
}

func scenarioExpiry(t *testing.T, h *externalHarness) {
	expires := time.Now().Add(100 * time.Millisecond).UTC()
	mutation := put("external/expiry/lease", []byte("holder-a"), ConditionAbsent, 0)
	mutation.ExpiresAt = &expires
	result, err := h.adapter.ConditionalMutate(t.Context(), "external-expiry-create", mutation, AckDurable)
	requireDurable(t, result, err)
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err = h.adapter.Get(t.Context(), mutation.Key)
		if IsKind(err, OutcomeNotFound) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("expiry did not release storage record: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	replacement, err := h.adapter.ConditionalMutate(t.Context(), "external-expiry-replace", put(mutation.Key, []byte("holder-b"), ConditionAbsent, 0), AckDurable)
	requireDurable(t, replacement, err)
}

func scenarioMemoryAck(t *testing.T, h *externalHarness) {
	result, err := h.adapter.ConditionalMutate(t.Context(), "external-memory", put("external/ack/memory", []byte("visible"), ConditionAny, 0), AckMemory)
	if err != nil || result.Ack != AckMemory {
		t.Fatalf("memory ack=%+v err=%v", result, err)
	}
	if _, err := h.adapter.Get(t.Context(), "external/ack/memory"); err != nil {
		t.Fatal(err)
	}
}

func scenarioDurableRestart(t *testing.T, h *externalHarness) {
	result, err := h.adapter.ConditionalMutate(t.Context(), "external-durable", put("external/ack/durable", []byte("restart"), ConditionAny, 0), AckDurable)
	requireDurable(t, result, err)
	if err := h.restart(); err != nil {
		t.Fatal(err)
	}
	record, err := h.adapter.Get(t.Context(), "external/ack/durable")
	if err != nil || string(record.Value) != "restart" || record.Revision != result.Revision {
		t.Fatalf("restarted record=%+v err=%v", record, err)
	}
}

func scenarioInspect(t *testing.T, h *externalHarness) {
	inspection, err := h.adapter.Inspect(t.Context())
	if err != nil || inspection.DatabaseID != h.databaseID || inspection.EndpointID == "" || inspection.CurrentRevision < inspection.DurableThrough {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func authorityScenario(namespace AuthorityNamespace) func(*testing.T, *externalHarness) {
	return func(t *testing.T, h *externalHarness) {
		key := "spk/v1/external/mission/heads/" + string(namespace) + "/singleton"
		mutation := AuthorityMutation{
			OperationID: "external-authority-" + string(namespace), Namespace: namespace, Codec: AuthorityCodec(namespace),
			Mutation: put(key, []byte("holder"), ConditionAbsent, 0),
		}
		var result MutationResult
		var err error
		switch namespace {
		case AuthorityLease:
			result, err = h.adapter.LeaseAuthority(t.Context(), mutation, AckDurable)
		case AuthorityReview:
			result, err = h.adapter.ReviewAuthority(t.Context(), mutation, AckDurable)
		case AuthorityWorkflow:
			result, err = h.adapter.WorkflowAuthority(t.Context(), mutation, AckDurable)
		case AuthorityIntegration:
			result, err = h.adapter.IntegrationAuthority(t.Context(), mutation, AckDurable)
		}
		requireDurable(t, result, err)
		stale := mutation
		stale.OperationID += "-stale"
		stale.Mutation = put(key, []byte("other"), ConditionRevision, result.Revision+1)
		switch namespace {
		case AuthorityLease:
			_, err = h.adapter.LeaseAuthority(t.Context(), stale, AckDurable)
		case AuthorityReview:
			_, err = h.adapter.ReviewAuthority(t.Context(), stale, AckDurable)
		case AuthorityWorkflow:
			_, err = h.adapter.WorkflowAuthority(t.Context(), stale, AckDurable)
		case AuthorityIntegration:
			_, err = h.adapter.IntegrationAuthority(t.Context(), stale, AckDurable)
		}
		if !IsKind(err, OutcomeConflict) {
			t.Fatalf("stale authority mutation=%v", err)
		}
		h.authorities[namespace] = key
		if err := h.restart(); err != nil {
			t.Fatal(err)
		}
		record, err := h.adapter.Get(t.Context(), key)
		if err != nil || string(record.Value) != "holder" || record.Revision != result.Revision {
			t.Fatalf("authority after restart record=%+v err=%v", record, err)
		}
	}
}

func scenarioResponseLoss(t *testing.T, h *externalHarness) {
	proxy := startResponseDropProxy(t, h.socket)
	client, err := gapdbapi.Dial(proxy.socket, gapdbapi.ClientOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(client, proxy.socket)
	if err != nil {
		t.Fatal(err)
	}
	operationID := "external-response-loss"
	batch := ProductBatch{
		OperationID: operationID, Operation: put("external/response-loss/operation", []byte("accepted"), ConditionAbsent, 0),
		Events:         []Mutation{put("external/response-loss/event", []byte("event"), ConditionAbsent, 0)},
		AggregateHeads: []Mutation{put("external/response-loss/head", []byte("head"), ConditionAbsent, 0)},
	}
	_, err = adapter.AtomicBatch(t.Context(), batch)
	_ = client.Close()
	if !IsKind(err, OutcomeAmbiguous) {
		t.Fatalf("lost response was not ambiguous: %v", err)
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.OperationID != operationID || typed.Recovery != RecoveryReconcile {
		t.Fatalf("lost response diagnostic=%+v", typed)
	}
	proxy.wait(t)
	if err := h.restart(); err != nil {
		t.Fatal(err)
	}
	record, err := h.adapter.Get(t.Context(), "external/response-loss/operation")
	if err != nil || string(record.Value) != "accepted" {
		t.Fatalf("same-operation reconciliation record=%+v err=%v", record, err)
	}
	if _, err := h.adapter.AtomicBatch(t.Context(), batch); !IsKind(err, OutcomeConflict) {
		t.Fatalf("same operation could duplicate publication: %v", err)
	}
}

func requireDurable(t *testing.T, result MutationResult, err error) {
	t.Helper()
	if err != nil || result.Revision == 0 || result.Ack != AckDurable || result.DurableThrough < result.Revision {
		t.Fatalf("durable result=%+v err=%v", result, err)
	}
}

type responseDropProxy struct {
	socket string
	done   chan error
}

func startResponseDropProxy(t *testing.T, upstream string) *responseDropProxy {
	return startResponseDropProxyExpect(t, upstream, "external-response-loss", 3, 0)
}

func startResponseDropProxyExpect(t *testing.T, upstream, requestID string, mutationCount, assertionCount int) *responseDropProxy {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "drop.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &responseDropProxy{socket: socket, done: make(chan error, 1)}
	go func() {
		defer listener.Close()
		for {
			client, err := listener.Accept()
			if err != nil {
				proxy.done <- err
				return
			}
			if err := relayAndDrop(client, upstream, requestID, mutationCount, assertionCount); errors.Is(err, io.EOF) {
				continue // the public client's Dial readiness probe carries no request
			} else {
				proxy.done <- err
				return
			}
		}
	}()
	return proxy
}

func relayAndDrop(client net.Conn, upstreamPath, requestID string, mutationCount, assertionCount int) error {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	request, err := readFrame(client, MaxWireFrameBytes)
	if err != nil {
		return err
	}
	upstream, err := net.DialTimeout("unix", upstreamPath, 5*time.Second)
	if err != nil {
		return err
	}
	defer upstream.Close()
	_ = upstream.SetDeadline(time.Now().Add(5 * time.Second))
	if err := writeFrame(upstream, request); err != nil {
		return err
	}
	response, err := readFrame(upstream, gapdbapi.DefaultMaxFrameBytes)
	if err != nil {
		return err
	}
	var envelope struct {
		SchemaVersion uint16 `json:"schema_version"`
		OK            bool   `json:"ok"`
		Operation     string `json:"operation"`
		RequestID     string `json:"request_id"`
		DatabaseID    string `json:"database_id"`
		Result        struct {
			Revision               uint64 `json:"revision"`
			Ack                    string `json:"ack"`
			DurableThroughRevision uint64 `json:"durable_through_revision"`
			MutationCount          int    `json:"mutation_count"`
			AssertionCount         int    `json:"assertion_count,omitempty"`
		} `json:"result"`
	}
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("drop proxy rejected response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("drop proxy response contains trailing data")
	}
	if envelope.SchemaVersion != AdmittedWireVersion || !envelope.OK || envelope.DatabaseID == "" || envelope.Operation != "atomic_batch" || envelope.RequestID != requestID || envelope.Result.Revision == 0 || envelope.Result.Ack != string(gapdbapi.AckDurable) || envelope.Result.DurableThroughRevision < envelope.Result.Revision || envelope.Result.MutationCount != mutationCount || envelope.Result.AssertionCount != assertionCount {
		return errors.New("drop proxy observed a non-accepted response")
	}
	// The complete accepted frame is intentionally consumed and never written
	// to the product-facing connection.
	return nil
}

func (p *responseDropProxy) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("response-drop proxy did not terminate")
	}
}

func readFrame(reader io.Reader, maximum int) ([]byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(prefix[:]))
	if length <= 0 || length > maximum {
		return nil, fmt.Errorf("invalid frame length %d", length)
	}
	payload := make([]byte, length)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func writeFrame(writer io.Writer, payload []byte) error {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(payload)))
	if _, err := writer.Write(prefix[:]); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}

type boundedBuffer struct {
	mu      sync.Mutex
	maximum int
	buffer  bytes.Buffer
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.maximum - b.buffer.Len()
	if remaining > 0 {
		_, _ = b.buffer.Write(value[:min(remaining, len(value))])
	}
	return len(value), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
