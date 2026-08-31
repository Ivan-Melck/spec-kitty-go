// Package external executes the fixed M1 public GapDB scenario catalog against
// one admitted sidecar lifecycle. It owns neither lifecycle startup nor final
// shutdown; after Run returns, the caller owns Result.Lifecycle.
package external

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

var scenarioIDs = [...]string{
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

// ScenarioIDs returns the immutable schema order for the fourteen external
// scenarios. The returned slice is a copy.
func ScenarioIDs() []string {
	return append([]string(nil), scenarioIDs[:]...)
}

// Outcome is the terminal scenario disposition consumed by M1 evidence.
type Outcome string

const (
	OutcomePass       Outcome = "pass"
	OutcomeFail       Outcome = "fail"
	OutcomeIncomplete Outcome = "incomplete"
)

// Record binds one terminal scenario to its immutable umbrella RunID and to
// the public store revision interval observed around its execution.
type Record struct {
	ScenarioID     string  `json:"scenario_id"`
	RunID          string  `json:"run_id"`
	Sequence       int     `json:"sequence"`
	Started        bool    `json:"started"`
	Terminal       bool    `json:"terminal"`
	Outcome        Outcome `json:"outcome"`
	BeforeRevision uint64  `json:"before_revision"`
	AfterRevision  uint64  `json:"after_revision"`
	ArtifactSHA256 string  `json:"artifact_sha256"`
}

// Result contains records and artifact bytes in the same exact schema order.
// Lifecycle is the current owner after any scenario-triggered durable restart;
// callers must use and eventually stop this value rather than the predecessor.
type Result struct {
	RunID     string
	Records   []Record
	Artifacts [][]byte
	Lifecycle *sidecargapdb.Lifecycle
}

type scenarioArtifactV1 struct {
	SchemaVersion  uint64  `json:"schema_version"`
	RunID          string  `json:"run_id"`
	ScenarioID     string  `json:"scenario_id"`
	Sequence       int     `json:"sequence"`
	Started        bool    `json:"started"`
	Terminal       bool    `json:"terminal"`
	Outcome        Outcome `json:"outcome"`
	BeforeRevision uint64  `json:"before_revision"`
	AfterRevision  uint64  `json:"after_revision"`
	Diagnostic     string  `json:"diagnostic,omitempty"`
}

// Run executes the exact fourteen scenarios through the public Adapter. The
// supplied lifecycle must already have completed sidecar admission. A scenario
// failure emits its terminal artifact and stops the ordered run immediately.
func Run(ctx context.Context, runID string, lifecycle *sidecargapdb.Lifecycle) (Result, error) {
	result := Result{RunID: runID, Lifecycle: lifecycle}
	if ctx == nil {
		return result, errors.New("external scenario context is absent")
	}
	if _, err := ids.ParseRunID(runID); err != nil {
		return result, fmt.Errorf("external scenario umbrella RunID: %w", err)
	}
	if lifecycle == nil {
		return result, errors.New("external scenario lifecycle is absent")
	}
	provenance := lifecycle.Provenance()
	if !provenance.Readiness.Admitted || !provenance.Readiness.Healthy || !provenance.Readiness.Configured ||
		provenance.DatabaseID == "" || provenance.ConfiguredSocket == "" || provenance.ConnectedSocket != provenance.ConfiguredSocket ||
		provenance.Executable.SHA256 != storegapdb.AdmittedSidecarSHA256 || provenance.ExecutedImage.SHA256 != storegapdb.AdmittedSidecarSHA256 {
		return result, errors.New("external scenario lifecycle is not admitted")
	}
	runtime, err := newProductionRuntime(runID, lifecycle)
	if err != nil {
		return result, err
	}
	defer runtime.close()
	return runWithRuntime(ctx, runID, runtime)
}

type scenarioExecution struct {
	RunID string
}

type scenarioRuntime interface {
	currentRevision(context.Context) (uint64, error)
	execute(context.Context, string, string, int) (scenarioExecution, error)
	currentLifecycle() *sidecargapdb.Lifecycle
}

func runWithRuntime(ctx context.Context, runID string, runtime scenarioRuntime) (Result, error) {
	result := Result{RunID: runID}
	if ctx == nil {
		return result, errors.New("external scenario context is absent")
	}
	if _, err := ids.ParseRunID(runID); err != nil {
		return result, fmt.Errorf("external scenario umbrella RunID: %w", err)
	}
	if runtime == nil {
		return result, errors.New("external scenario runtime is absent")
	}
	result.Lifecycle = runtime.currentLifecycle()
	for index, scenarioID := range scenarioIDs {
		sequence := index + 1
		before, beforeErr := runtime.currentRevision(ctx)
		started := beforeErr == nil
		execution := scenarioExecution{RunID: runID}
		executeErr := beforeErr
		if executeErr == nil {
			execution, executeErr = runtime.execute(ctx, runID, scenarioID, sequence)
			if executeErr == nil && execution.RunID != runID {
				executeErr = fmt.Errorf("child run ID %q differs from umbrella RunID", execution.RunID)
			}
		}
		after := before
		if afterRevision, afterErr := runtime.currentRevision(ctx); afterErr != nil {
			executeErr = errors.Join(executeErr, fmt.Errorf("observe after revision: %w", afterErr))
		} else {
			after = afterRevision
		}
		outcome := OutcomePass
		if executeErr != nil {
			outcome = OutcomeFail
			if errors.Is(executeErr, context.Canceled) || errors.Is(executeErr, context.DeadlineExceeded) {
				outcome = OutcomeIncomplete
			}
		}
		diagnostic := ""
		if executeErr != nil {
			diagnostic = bounded(executeErr.Error(), storegapdb.MaxDiagnosticBytes)
		}
		artifact, artifactErr := json.Marshal(scenarioArtifactV1{
			SchemaVersion: 1, RunID: runID, ScenarioID: scenarioID, Sequence: sequence,
			Started: started, Terminal: true, Outcome: outcome,
			BeforeRevision: before, AfterRevision: after, Diagnostic: diagnostic,
		})
		if artifactErr != nil {
			return result, fmt.Errorf("marshal scenario artifact %q: %w", scenarioID, artifactErr)
		}
		digest := sha256.Sum256(artifact)
		result.Records = append(result.Records, Record{
			ScenarioID: scenarioID, RunID: runID, Sequence: sequence,
			Started: started, Terminal: true, Outcome: outcome,
			BeforeRevision: before, AfterRevision: after,
			ArtifactSHA256: hex.EncodeToString(digest[:]),
		})
		result.Artifacts = append(result.Artifacts, artifact)
		result.Lifecycle = runtime.currentLifecycle()
		if executeErr != nil {
			return result, fmt.Errorf("external scenario %q: %w", scenarioID, executeErr)
		}
	}
	return result, nil
}

type productionRuntime struct {
	runID          string
	lifecycle      *sidecargapdb.Lifecycle
	adapter        *storegapdb.Adapter
	closer         io.Closer
	databaseID     string
	restartOrdinal int
	authorities    map[storegapdb.AuthorityNamespace]string
}

func newProductionRuntime(runID string, lifecycle *sidecargapdb.Lifecycle) (*productionRuntime, error) {
	runtime := &productionRuntime{
		runID: runID, lifecycle: lifecycle,
		databaseID:  lifecycle.Provenance().DatabaseID,
		authorities: make(map[storegapdb.AuthorityNamespace]string),
	}
	if err := runtime.dial(); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *productionRuntime) dial() error {
	provenance := r.lifecycle.Provenance()
	adapter, closer, err := storegapdb.Dial(provenance.ConfiguredSocket, storegapdb.DefaultRequestTimeout)
	if err != nil {
		return fmt.Errorf("dial admitted lifecycle: %w", err)
	}
	r.adapter, r.closer = adapter, closer
	return nil
}

func (r *productionRuntime) close() {
	if r.closer != nil {
		_ = r.closer.Close()
		r.closer = nil
		r.adapter = nil
	}
}

func (r *productionRuntime) currentLifecycle() *sidecargapdb.Lifecycle { return r.lifecycle }

func (r *productionRuntime) currentRevision(ctx context.Context) (uint64, error) {
	if r.adapter == nil {
		return 0, errors.New("public GapDB adapter is unavailable")
	}
	inspection, err := r.adapter.Inspect(ctx)
	if err != nil {
		return 0, err
	}
	if inspection.DatabaseID != r.databaseID {
		return 0, errors.New("database identity changed")
	}
	return inspection.CurrentRevision, nil
}

func (r *productionRuntime) execute(ctx context.Context, runID, scenarioID string, _ int) (scenarioExecution, error) {
	if runID != r.runID {
		return scenarioExecution{RunID: runID}, errors.New("runtime received a foreign umbrella RunID")
	}
	var err error
	switch scenarioID {
	case "data/get-put":
		err = r.scenarioGetPut(ctx)
	case "conditional/cas-delete":
		err = r.scenarioConditional(ctx)
	case "batch/atomic":
		err = r.scenarioBatch(ctx)
	case "scan/sorted-prefix":
		err = r.scenarioScan(ctx)
	case "watch/registered-order":
		err = r.scenarioWatch(ctx)
	case "expiry/release-authority":
		err = r.scenarioExpiry(ctx)
	case "ack/memory-visible":
		err = r.scenarioMemoryAck(ctx)
	case "ack/durable-restart":
		err = r.scenarioDurableRestart(ctx)
	case "inspect/authority":
		err = r.scenarioInspect(ctx)
	case "authority/lease":
		err = r.scenarioAuthority(ctx, storegapdb.AuthorityLease)
	case "authority/review":
		err = r.scenarioAuthority(ctx, storegapdb.AuthorityReview)
	case "authority/workflow":
		err = r.scenarioAuthority(ctx, storegapdb.AuthorityWorkflow)
	case "authority/integration":
		err = r.scenarioAuthority(ctx, storegapdb.AuthorityIntegration)
	case "response-loss/reconcile":
		err = r.scenarioResponseLoss(ctx)
	default:
		err = fmt.Errorf("unknown external scenario %q", scenarioID)
	}
	if err == nil {
		err = r.assertUniversalAuthorityInvariants(ctx)
	}
	return scenarioExecution{RunID: runID}, err
}

func (r *productionRuntime) scenarioGetPut(ctx context.Context) error {
	result, err := r.adapter.ConditionalMutate(ctx, "external-get-put", put("external/data", []byte{0, 1, 2, 255}, storegapdb.ConditionAny, 0), storegapdb.AckMemory)
	if err != nil || result.Revision == 0 || result.Ack != storegapdb.AckMemory {
		return fmt.Errorf("put result=%+v: %w", result, err)
	}
	record, err := r.adapter.Get(ctx, "external/data")
	if err != nil || !bytes.Equal(record.Value, []byte{0, 1, 2, 255}) || record.Revision != result.Revision {
		return fmt.Errorf("get record=%+v: %w", record, err)
	}
	return nil
}

func (r *productionRuntime) scenarioConditional(ctx context.Context) error {
	winner, err := r.adapter.ConditionalMutate(ctx, "external-conditional-create", put("external/conditional", []byte("one"), storegapdb.ConditionAbsent, 0), storegapdb.AckDurable)
	if err := requireDurable(winner, err); err != nil {
		return err
	}
	if _, err := r.adapter.ConditionalMutate(ctx, "external-conditional-loser", put("external/conditional", []byte("loser"), storegapdb.ConditionAbsent, 0), storegapdb.AckDurable); !storegapdb.IsKind(err, storegapdb.OutcomeConflict) {
		return fmt.Errorf("absent loser: %w", err)
	}
	if _, err := r.adapter.ConditionalMutate(ctx, "external-cas-stale", put("external/conditional", []byte("bad"), storegapdb.ConditionRevision, winner.Revision+1), storegapdb.AckDurable); !storegapdb.IsKind(err, storegapdb.OutcomeConflict) {
		return fmt.Errorf("stale CAS: %w", err)
	}
	replaced, err := r.adapter.ConditionalMutate(ctx, "external-cas-winner", put("external/conditional", []byte("two"), storegapdb.ConditionRevision, winner.Revision), storegapdb.AckDurable)
	if err := requireDurable(replaced, err); err != nil {
		return err
	}
	staleDelete := storegapdb.Mutation{Kind: storegapdb.MutationDelete, Key: "external/conditional", Condition: storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: winner.Revision}}
	if _, err := r.adapter.ConditionalMutate(ctx, "external-delete-stale", staleDelete, storegapdb.AckDurable); !storegapdb.IsKind(err, storegapdb.OutcomeConflict) {
		return fmt.Errorf("stale delete: %w", err)
	}
	deleteMutation := storegapdb.Mutation{Kind: storegapdb.MutationDelete, Key: "external/conditional", Condition: storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: replaced.Revision}}
	deleted, err := r.adapter.ConditionalMutate(ctx, "external-delete-winner", deleteMutation, storegapdb.AckDurable)
	if err := requireDurable(deleted, err); err != nil {
		return err
	}
	if _, err := r.adapter.Get(ctx, "external/conditional"); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
		return fmt.Errorf("deleted record remains: %w", err)
	}
	return nil
}

func (r *productionRuntime) scenarioBatch(ctx context.Context) error {
	batch := storegapdb.ProductBatch{
		OperationID: "external-batch", Operation: put("external/batch/operation", []byte("accepted"), storegapdb.ConditionAbsent, 0),
		Events:         []storegapdb.Mutation{put("external/batch/event", []byte("event"), storegapdb.ConditionAbsent, 0)},
		AggregateHeads: []storegapdb.Mutation{put("external/batch/head", []byte("head"), storegapdb.ConditionAbsent, 0)},
	}
	result, err := r.adapter.AtomicBatch(ctx, batch)
	if err := requireDurable(result, err); err != nil {
		return err
	}
	for _, key := range []string{"external/batch/operation", "external/batch/event", "external/batch/head"} {
		record, err := r.adapter.Get(ctx, key)
		if err != nil || record.Revision != result.Revision {
			return fmt.Errorf("atomic key=%q record=%+v: %w", key, record, err)
		}
	}
	loser := storegapdb.ProductBatch{
		OperationID: "external-batch-loser", Operation: put("external/batch/loser-operation", []byte("must-not-publish"), storegapdb.ConditionAbsent, 0),
		Events:         []storegapdb.Mutation{put("external/batch/loser-event", []byte("must-not-publish"), storegapdb.ConditionAbsent, 0)},
		AggregateHeads: []storegapdb.Mutation{put("external/batch/head", []byte("conflict"), storegapdb.ConditionAbsent, 0)},
	}
	if _, err := r.adapter.AtomicBatch(ctx, loser); !storegapdb.IsKind(err, storegapdb.OutcomeConflict) {
		return fmt.Errorf("conflicting batch: %w", err)
	}
	for _, absent := range []string{"external/batch/loser-operation", "external/batch/loser-event"} {
		if _, err := r.adapter.Get(ctx, absent); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
			return fmt.Errorf("conflicting batch partially published %q: %w", absent, err)
		}
	}
	return nil
}

func (r *productionRuntime) scenarioScan(ctx context.Context) error {
	for _, key := range []string{"external/scan/c", "external/scan/a", "external/scan/b"} {
		if _, err := r.adapter.ConditionalMutate(ctx, "scan-"+filepath.Base(key), put(key, []byte(key), storegapdb.ConditionAny, 0), storegapdb.AckMemory); err != nil {
			return err
		}
	}
	first, err := r.adapter.Scan(ctx, storegapdb.ScanRequest{Prefix: "external/scan/", Limit: 2})
	if err != nil || !first.Truncated || first.Cursor == "" || len(first.Records) != 2 || first.ObservedRevision == 0 {
		return fmt.Errorf("first pinned scan page=%+v: %w", first, err)
	}
	if _, err := r.adapter.ConditionalMutate(ctx, "scan-d", put("external/scan/d", []byte("external/scan/d"), storegapdb.ConditionAny, 0), storegapdb.AckDurable); err != nil {
		return err
	}
	if _, err := r.adapter.Scan(ctx, storegapdb.ScanRequest{Prefix: "external/scan/", Limit: 2, Cursor: first.Cursor}); !storegapdb.IsKind(err, storegapdb.OutcomeConflict) {
		return fmt.Errorf("stale scan cursor: %w", err)
	}
	var records []storegapdb.Record
	var observed uint64
	for cursor := ""; ; {
		page, err := r.adapter.Scan(ctx, storegapdb.ScanRequest{Prefix: "external/scan/", Limit: 2, Cursor: cursor})
		if err != nil {
			return err
		}
		if observed == 0 {
			observed = page.ObservedRevision
		} else if page.ObservedRevision != observed {
			return fmt.Errorf("restarted scan mixed revisions: first=%d page=%d", observed, page.ObservedRevision)
		}
		records = append(records, page.Records...)
		if !page.Truncated {
			break
		}
		cursor = page.Cursor
	}
	keys := make([]string, len(records))
	for index := range records {
		keys[index] = records[index].Key
	}
	if len(keys) != 4 || !sort.StringsAreSorted(keys) || strings.Join(keys, "\x00") != strings.Join([]string{"external/scan/a", "external/scan/b", "external/scan/c", "external/scan/d"}, "\x00") {
		return fmt.Errorf("sorted scan keys=%v", keys)
	}
	return nil
}

func (r *productionRuntime) scenarioWatch(ctx context.Context) error {
	inspection, err := r.adapter.Inspect(ctx)
	if err != nil {
		return err
	}
	watch, err := r.adapter.Watch(ctx, "external/watch/", inspection.CurrentRevision)
	if err != nil {
		return err
	}
	if watch.RegistrationRevision() < inspection.CurrentRevision {
		_ = watch.Close()
		return errors.New("watch registration precedes requested revision")
	}
	result, err := r.adapter.ConditionalMutate(ctx, "external-watch-put", put("external/watch/event", []byte("event"), storegapdb.ConditionAny, 0), storegapdb.AckDurable)
	if durableErr := requireDurable(result, err); durableErr != nil {
		_ = watch.Close()
		return durableErr
	}
	nextCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	event, nextErr := watch.Next(nextCtx)
	cancel()
	closeErr := watch.Close()
	if nextErr != nil || event.Key != "external/watch/event" || event.Revision != result.Revision {
		return errors.Join(fmt.Errorf("watch event=%+v: %w", event, nextErr), closeErr)
	}
	record, err := r.adapter.Get(ctx, event.Key)
	if err != nil || record.Revision != result.Revision {
		return errors.Join(fmt.Errorf("watch reconciliation record=%+v: %w", record, err), closeErr)
	}
	return closeErr
}

func (r *productionRuntime) scenarioExpiry(ctx context.Context) error {
	expires := time.Now().Add(100 * time.Millisecond).UTC()
	mutation := put("external/expiry/lease", []byte("holder-a"), storegapdb.ConditionAbsent, 0)
	mutation.ExpiresAt = &expires
	result, err := r.adapter.ConditionalMutate(ctx, "external-expiry-create", mutation, storegapdb.AckDurable)
	if err := requireDurable(result, err); err != nil {
		return err
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err = r.adapter.Get(ctx, mutation.Key)
		if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
			break
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("expiry did not release storage record")
		case <-ticker.C:
		}
	}
	replacement, err := r.adapter.ConditionalMutate(ctx, "external-expiry-replace", put(mutation.Key, []byte("holder-b"), storegapdb.ConditionAbsent, 0), storegapdb.AckDurable)
	return requireDurable(replacement, err)
}

func (r *productionRuntime) scenarioMemoryAck(ctx context.Context) error {
	result, err := r.adapter.ConditionalMutate(ctx, "external-memory", put("external/ack/memory", []byte("visible"), storegapdb.ConditionAny, 0), storegapdb.AckMemory)
	if err != nil || result.Ack != storegapdb.AckMemory {
		return fmt.Errorf("memory ack=%+v: %w", result, err)
	}
	_, err = r.adapter.Get(ctx, "external/ack/memory")
	return err
}

func (r *productionRuntime) scenarioDurableRestart(ctx context.Context) error {
	result, err := r.adapter.ConditionalMutate(ctx, "external-durable", put("external/ack/durable", []byte("restart"), storegapdb.ConditionAny, 0), storegapdb.AckDurable)
	if err := requireDurable(result, err); err != nil {
		return err
	}
	if err := r.restart(ctx); err != nil {
		return err
	}
	record, err := r.adapter.Get(ctx, "external/ack/durable")
	if err != nil || string(record.Value) != "restart" || record.Revision != result.Revision {
		return fmt.Errorf("restarted record=%+v: %w", record, err)
	}
	return nil
}

func (r *productionRuntime) scenarioInspect(ctx context.Context) error {
	inspection, err := r.adapter.Inspect(ctx)
	if err != nil || inspection.DatabaseID != r.databaseID || inspection.EndpointID == "" || inspection.CurrentRevision < inspection.DurableThrough {
		return fmt.Errorf("inspection=%+v: %w", inspection, err)
	}
	return nil
}

func (r *productionRuntime) scenarioAuthority(ctx context.Context, namespace storegapdb.AuthorityNamespace) error {
	key := "spk/v1/external/mission/heads/" + string(namespace) + "/singleton"
	mutation := storegapdb.AuthorityMutation{
		OperationID: "external-authority-" + string(namespace), Namespace: namespace, Codec: storegapdb.AuthorityCodec(namespace),
		Mutation: put(key, []byte("holder"), storegapdb.ConditionAbsent, 0),
	}
	result, err := r.mutateAuthority(ctx, mutation)
	if err := requireDurable(result, err); err != nil {
		return err
	}
	stale := mutation
	stale.OperationID += "-stale"
	stale.Mutation = put(key, []byte("other"), storegapdb.ConditionRevision, result.Revision+1)
	if _, err := r.mutateAuthority(ctx, stale); !storegapdb.IsKind(err, storegapdb.OutcomeConflict) {
		return fmt.Errorf("stale authority mutation: %w", err)
	}
	r.authorities[namespace] = key
	if err := r.restart(ctx); err != nil {
		return err
	}
	record, err := r.adapter.Get(ctx, key)
	if err != nil || string(record.Value) != "holder" || record.Revision != result.Revision {
		return fmt.Errorf("authority after restart record=%+v: %w", record, err)
	}
	return nil
}

func (r *productionRuntime) mutateAuthority(ctx context.Context, mutation storegapdb.AuthorityMutation) (storegapdb.MutationResult, error) {
	switch mutation.Namespace {
	case storegapdb.AuthorityLease:
		return r.adapter.LeaseAuthority(ctx, mutation, storegapdb.AckDurable)
	case storegapdb.AuthorityReview:
		return r.adapter.ReviewAuthority(ctx, mutation, storegapdb.AckDurable)
	case storegapdb.AuthorityWorkflow:
		return r.adapter.WorkflowAuthority(ctx, mutation, storegapdb.AckDurable)
	case storegapdb.AuthorityIntegration:
		return r.adapter.IntegrationAuthority(ctx, mutation, storegapdb.AckDurable)
	default:
		return storegapdb.MutationResult{}, fmt.Errorf("unknown authority namespace %q", mutation.Namespace)
	}
}

func (r *productionRuntime) scenarioResponseLoss(ctx context.Context) error {
	proxy, err := startResponseDropProxy(r.lifecycle.Provenance().ConfiguredSocket)
	if err != nil {
		return err
	}
	defer proxy.close()
	client, err := gapdbapi.Dial(proxy.socket, gapdbapi.ClientOptions{Timeout: 2 * time.Second})
	if err != nil {
		return err
	}
	adapter, err := storegapdb.New(client, proxy.socket)
	if err != nil {
		_ = client.Close()
		return err
	}
	operationID := "external-response-loss"
	batch := storegapdb.ProductBatch{
		OperationID: operationID, Operation: put("external/response-loss/operation", []byte("accepted"), storegapdb.ConditionAbsent, 0),
		Events:         []storegapdb.Mutation{put("external/response-loss/event", []byte("event"), storegapdb.ConditionAbsent, 0)},
		AggregateHeads: []storegapdb.Mutation{put("external/response-loss/head", []byte("head"), storegapdb.ConditionAbsent, 0)},
	}
	_, mutationErr := adapter.AtomicBatch(ctx, batch)
	_ = client.Close()
	if !storegapdb.IsKind(mutationErr, storegapdb.OutcomeAmbiguous) {
		return fmt.Errorf("lost response was not ambiguous: %w", mutationErr)
	}
	var typed *storegapdb.Error
	if !errors.As(mutationErr, &typed) || typed.OperationID != operationID || typed.Recovery != storegapdb.RecoveryReconcile {
		return fmt.Errorf("lost response diagnostic=%+v", typed)
	}
	if err := proxy.wait(ctx); err != nil {
		return err
	}
	if err := r.restart(ctx); err != nil {
		return err
	}
	record, err := r.adapter.Get(ctx, "external/response-loss/operation")
	if err != nil || string(record.Value) != "accepted" {
		return fmt.Errorf("same-operation reconciliation record=%+v: %w", record, err)
	}
	if _, err := r.adapter.AtomicBatch(ctx, batch); !storegapdb.IsKind(err, storegapdb.OutcomeConflict) {
		return fmt.Errorf("same operation could duplicate publication: %w", err)
	}
	return nil
}

func (r *productionRuntime) restart(ctx context.Context) error {
	r.close()
	r.restartOrdinal++
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00external-owner\x00%d", r.runID, r.restartOrdinal)))
	supervisorRunID := "external-owner-" + hex.EncodeToString(digest[:12])
	next, err := r.lifecycle.RestartOwned(ctx, sidecargapdb.RestartClean, supervisorRunID)
	if err != nil {
		return err
	}
	r.lifecycle = next
	return r.dial()
}

func (r *productionRuntime) assertUniversalAuthorityInvariants(ctx context.Context) error {
	inspection, err := r.adapter.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("authority invariant inspect: %w", err)
	}
	if inspection.DatabaseID != r.databaseID || inspection.CurrentRevision < inspection.DurableThrough || inspection.Lifecycle != string(gapdbapi.LifecycleReady) {
		return fmt.Errorf("authority invariant inspection=%+v database=%s", inspection, r.databaseID)
	}
	for namespace, expectedKey := range r.authorities {
		prefix := "spk/v1/external/mission/heads/" + string(namespace) + "/"
		page, err := r.adapter.Scan(ctx, storegapdb.ScanRequest{Prefix: prefix, Limit: storegapdb.MaxScanRecords})
		if err != nil {
			return fmt.Errorf("authority invariant scan %s: %w", namespace, err)
		}
		matches := 0
		for _, record := range page.Records {
			if !strings.HasPrefix(record.Key, prefix) {
				return fmt.Errorf("authority namespace crossover: %q under %q", record.Key, prefix)
			}
			if record.Key == expectedKey {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("authority %s has %d current records for %q", namespace, matches, expectedKey)
		}
	}
	return nil
}

func put(key string, value []byte, condition storegapdb.ConditionKind, revision uint64) storegapdb.Mutation {
	return storegapdb.Mutation{
		Kind: storegapdb.MutationPut, Key: key, Value: append([]byte(nil), value...),
		Condition: storegapdb.Condition{Kind: condition, ExpectedRevision: revision},
	}
}

func requireDurable(result storegapdb.MutationResult, err error) error {
	if err != nil || result.Revision == 0 || result.Ack != storegapdb.AckDurable || result.DurableThrough < result.Revision {
		return fmt.Errorf("durable result=%+v: %w", result, err)
	}
	return nil
}

type responseDropProxy struct {
	socket   string
	listener net.Listener
	done     chan error
}

func startResponseDropProxy(upstream string) (*responseDropProxy, error) {
	socket := filepath.Join(filepath.Dir(upstream), "drop.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	proxy := &responseDropProxy{socket: socket, listener: listener, done: make(chan error, 1)}
	go func() {
		defer listener.Close()
		for {
			client, err := listener.Accept()
			if err != nil {
				proxy.done <- err
				return
			}
			if err := relayAndDrop(client, upstream); errors.Is(err, io.EOF) {
				continue
			} else {
				proxy.done <- err
				return
			}
		}
	}()
	return proxy, nil
}

func (p *responseDropProxy) wait(ctx context.Context) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-p.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("response-drop proxy did not terminate")
	}
}

func (p *responseDropProxy) close() {
	_ = p.listener.Close()
	_ = os.Remove(p.socket)
}

func relayAndDrop(client net.Conn, upstreamPath string) error {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	request, err := readFrame(client, storegapdb.MaxWireFrameBytes)
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
	if envelope.SchemaVersion != storegapdb.AdmittedWireVersion || !envelope.OK || envelope.DatabaseID == "" || envelope.Operation != "atomic_batch" || envelope.RequestID != "external-response-loss" || envelope.Result.Revision == 0 || envelope.Result.Ack != string(gapdbapi.AckDurable) || envelope.Result.DurableThroughRevision < envelope.Result.Revision || envelope.Result.MutationCount != 3 {
		return errors.New("drop proxy observed a non-accepted response")
	}
	return nil
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

func bounded(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
