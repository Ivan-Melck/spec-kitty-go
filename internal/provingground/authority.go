// Package provingground composes the durable control journal with retained
// execution and workspace concepts. It is an M1 qualification surface, not a
// second storage implementation.
package provingground

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/faults"
)

// OutcomeKind is the bounded proving-ground refusal vocabulary.
type OutcomeKind string

const (
	OutcomeInvalidCommand       OutcomeKind = "invalid_command"
	OutcomeStaleWorkRevision    OutcomeKind = "stale_work_revision"
	OutcomeAuthorityConflict    OutcomeKind = "authority_conflict"
	OutcomeStaleLease           OutcomeKind = "stale_lease"
	OutcomeStaleFence           OutcomeKind = "stale_fence"
	OutcomeStaleEpoch           OutcomeKind = "stale_epoch"
	OutcomeActorProcessMismatch OutcomeKind = "actor_process_mismatch"
	OutcomeWorkspaceNotReady    OutcomeKind = "workspace_not_ready"
	OutcomeAmbiguous            OutcomeKind = "ambiguous"
	OutcomeIndeterminate        OutcomeKind = "indeterminate"
)

type Error struct {
	Kind     OutcomeKind
	Action   string
	Detail   string
	Recovery []string
	Cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("proving ground: %s action=%s detail=%s", e.Kind, e.Action, e.Detail)
}
func (e *Error) Unwrap() error { return e.Cause }

func IsOutcome(err error, kind OutcomeKind) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Kind == kind
}

func refusal(kind OutcomeKind, action, detail string, recovery ...string) error {
	return &Error{Kind: kind, Action: action, Detail: detail, Recovery: append([]string(nil), recovery...)}
}

type batchApplier interface {
	Apply(context.Context, application.ValidatedBatchV1) (application.ResultV1, error)
}

// Compile-time proof that production uses the admitted application service.
var _ batchApplier = (*application.Service)(nil)

type Config struct {
	ProjectID      controljournal.ProjectID
	MissionID      controljournal.MissionID
	WorkRevisionID controljournal.WorkRevisionID
	RunID          controljournal.RunID
	RuntimeEpochID controljournal.RuntimeEpochID
	Actors         controljournal.TrialImplementationActorsV1
	WorkIDs        []controljournal.WorkID
	LeaseTTL       time.Duration
}

type ClaimRequest struct {
	OperationID                controljournal.OperationID
	WorkRevisionID             controljournal.WorkRevisionID
	RuntimeEpochID             controljournal.RuntimeEpochID
	WorkID                     controljournal.WorkID
	ActorID                    controljournal.ActorID
	ProcessID                  controljournal.ProcessID
	LeaseID                    controljournal.LeaseID
	GovernanceContextID        string
	GovernanceContextAlgorithm string
	GovernanceContextDigest    string
	Now                        time.Time
}

// Authority is a lease-scoped governed capability. It is intentionally not a
// filesystem capability; direct writes do not consult it.
type Authority struct {
	ProjectID                   controljournal.ProjectID      `json:"project_id"`
	MissionID                   controljournal.MissionID      `json:"mission_id"`
	WorkRevisionID              controljournal.WorkRevisionID `json:"work_revision_id"`
	WorkID                      controljournal.WorkID         `json:"work_id"`
	ActorID                     controljournal.ActorID        `json:"actor_id"`
	ProcessID                   controljournal.ProcessID      `json:"process_id"`
	RunID                       controljournal.RunID          `json:"run_id"`
	LeaseID                     controljournal.LeaseID        `json:"lease_id"`
	RuntimeEpochID              controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	Generation                  uint64                        `json:"generation"`
	FenceToken                  uint64                        `json:"fence_token"`
	AuthorityHeadRecordRevision uint64                        `json:"authority_head_record_revision"`
	Deadline                    time.Time                     `json:"deadline"`
	Status                      string                        `json:"status"`
	OperationID                 controljournal.OperationID    `json:"operation_id"`
	GovernanceContextID         string                        `json:"governance_context_id,omitempty"`
	GovernanceContextAlgorithm  string                        `json:"governance_context_algorithm,omitempty"`
	GovernanceContextDigest     string                        `json:"governance_context_digest,omitempty"`
}

type workState struct {
	mu           sync.Mutex
	logical      uint64
	headRevision uint64
	current      *Authority
	lastFence    uint64
	lastGen      uint64
	operations   map[controljournal.OperationID]Authority
}

type service struct {
	config Config

	epochMu             sync.RWMutex
	epoch               controljournal.RuntimeEpochID
	applier             batchApplier
	faults              faults.Hook
	runtimeLogical      uint64
	runtimeHeadRevision uint64
	predecessorEpoch    controljournal.RuntimeEpochID
	commandMu           sync.Mutex
	invalidations       map[controljournal.OperationID]invalidationReceipt

	works map[controljournal.WorkID]*workState

	workspaceMu sync.RWMutex
	workspaces  map[controljournal.WorkspaceID]WorkspaceBinding
	workspaceOp map[controljournal.OperationID]WorkspaceBinding
	git         GitWorkspace
}

type runtimeProjection struct {
	RuntimeEpochID     controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	PredecessorEpoch   controljournal.RuntimeEpochID `json:"predecessor_epoch_id,omitempty"`
	LogicalVersion     uint64                        `json:"logical_version"`
	HeadRecordRevision uint64                        `json:"head_record_revision"`
}

type workProjection struct {
	WorkID             controljournal.WorkID `json:"work_id"`
	LogicalVersion     uint64                `json:"logical_version"`
	HeadRecordRevision uint64                `json:"head_record_revision"`
	LastGeneration     uint64                `json:"last_generation"`
	LastFence          uint64                `json:"last_fence"`
	Current            *Authority            `json:"current,omitempty"`
}

// projection is diagnostic process memory only. It is deliberately private:
// production restoration must enter through the checkpoint package's sealed
// RestoreResultV1 capability and verified durable aggregate heads.
type projection struct {
	Runtime    runtimeProjection  `json:"runtime"`
	Works      []workProjection   `json:"works"`
	Workspaces []WorkspaceBinding `json:"workspaces"`
}

type invalidationReceipt struct {
	Kind             string
	WorkID           controljournal.WorkID
	LeaseID          controljournal.LeaseID
	Generation       uint64
	PredecessorEpoch controljournal.RuntimeEpochID
	RuntimeEpochID   controljournal.RuntimeEpochID
}

func newService(config Config, applier batchApplier, git GitWorkspace) (*service, error) {
	if applier == nil || config.LeaseTTL <= 0 || len(config.WorkIDs) != 2 {
		return nil, refusal(OutcomeInvalidCommand, "new", "applier, positive lease TTL, and exactly two work items are required")
	}
	if _, err := controljournal.NewProjectID(string(config.ProjectID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "project identity is invalid")
	}
	if _, err := controljournal.NewMissionID(string(config.MissionID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "mission identity is invalid")
	}
	if _, err := controljournal.NewWorkRevisionID(string(config.WorkRevisionID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "work revision identity is invalid")
	}
	if _, err := controljournal.NewRunID(string(config.RunID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "run identity is invalid")
	}
	if _, err := controljournal.NewRuntimeEpochID(string(config.RuntimeEpochID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "runtime epoch identity is invalid")
	}
	actors, err := controljournal.NewTrialImplementationActorsV1(config.Actors.ActorA, config.Actors.ActorB, config.Actors.ProcessA, config.Actors.ProcessB, config.Actors.ProcessC)
	if err != nil || actors != config.Actors {
		return nil, refusal(OutcomeInvalidCommand, "new", "trial actor and process vocabulary is invalid")
	}
	works := make(map[controljournal.WorkID]*workState, 2)
	for _, workID := range config.WorkIDs {
		if _, err := controljournal.NewWorkID(string(workID)); err != nil || works[workID] != nil {
			return nil, refusal(OutcomeInvalidCommand, "new", "work identities must be valid and unique")
		}
		works[workID] = &workState{operations: make(map[controljournal.OperationID]Authority)}
	}
	return &service{
		config: config, epoch: config.RuntimeEpochID, applier: applier, faults: faults.Noop{}, works: works, git: git,
		workspaces:    make(map[controljournal.WorkspaceID]WorkspaceBinding),
		workspaceOp:   make(map[controljournal.OperationID]WorkspaceBinding),
		invalidations: make(map[controljournal.OperationID]invalidationReceipt),
	}, nil
}

// EventRegistryV1 is the exact closed event vocabulary required to replay the
// product runtime admission plus this proving-ground composition.
func EventRegistryV1() (controljournal.EventRegistryV1, error) {
	versions := []controljournal.EventVersionV1{
		{EventType: "runtime.epoch_admitted", EventSchemaVersion: 1, ReducerVersion: "runtime-v1"},
		{EventType: "authority.claimed", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"},
		{EventType: "authority.heartbeat", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"},
		{EventType: "authority.expired", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"},
		{EventType: "authority.predecessor_invalidated", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"},
		{EventType: "workspace.bound", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"},
		{EventType: "workspace.quarantined", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"},
		{EventType: "runtime.owner_restarted", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"},
	}
	return controljournal.NewEventRegistryV1(versions)
}

// ReducerRegistryV1 mirrors EventRegistryV1 using the frozen declarative
// replace-with-event-payload program. No caller-provided reducer executes.
func ReducerRegistryV1() (reducer.RegistryV1, error) {
	entries := []reducer.EntryV1{
		{AggregateKind: "runtime", EventType: "runtime.epoch_admitted", EventSchemaVersion: 1, ReducerVersion: "runtime-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "authority", EventType: "authority.claimed", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "authority", EventType: "authority.heartbeat", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "authority", EventType: "authority.expired", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "authority", EventType: "authority.predecessor_invalidated", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "workspace", EventType: "workspace.bound", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "workspace", EventType: "workspace.quarantined", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "runtime-composition", EventType: "runtime.owner_restarted", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
	}
	return reducer.NewRegistryV1(entries)
}

type restoreHeadReader interface {
	Get(context.Context, string) (storegapdb.Record, error)
	Scan(context.Context, storegapdb.ScanRequest) (storegapdb.ScanPage, error)
}

func restoreValidated(ctx context.Context, config Config, applier batchApplier, git GitWorkspace, reader restoreHeadReader, restored checkpoint.RestoreResultV1) (*service, error) {
	admission, err := restored.AdmissionV1()
	if err != nil {
		return nil, refusal(OutcomeInvalidCommand, "restore", "checkpoint restore result is not sealed")
	}
	facts, err := admission.Facts()
	if err != nil || facts.Source.ProjectID != config.ProjectID || facts.Source.MissionID != config.MissionID || facts.Source.RunID != config.RunID ||
		facts.Binding.DatabaseID == "" || facts.Binding.HostInstanceID == "" || restored.Projection.GlobalDigest() != facts.CanonicalProjectionDigest {
		return nil, refusal(OutcomeInvalidCommand, "restore", "sealed restore identity or projection digest does not match the executable run")
	}
	service, err := newService(config, applier, git)
	if err != nil {
		return nil, err
	}
	prefix := "spk/v1/" + string(config.ProjectID) + "/" + string(config.MissionID) + "/heads/"
	journalBound := facts.TerminalWatermark.ThroughRecordRevision
	if err := validateAuthorityCensus(ctx, reader, prefix, restored.Projection, config, journalBound, facts.ObservedStoreRevision); err != nil {
		return nil, err
	}
	for _, workID := range config.WorkIDs {
		key := reducer.AggregateKeyV1{Kind: "authority", ID: controljournal.AggregateID(workID)}
		state, present := restored.Projection.State(key)
		logical, hasHead := restored.Projection.Head(key)
		if present != hasHead {
			return nil, refusal(OutcomeInvalidCommand, "restore", "authority projection has an incomplete head pair")
		}
		if !present {
			continue
		}
		record, head, err := readRestoredHead(ctx, reader, prefix, key, logical, config.RuntimeEpochID, state.Digest(), journalBound, facts.ObservedStoreRevision)
		if err != nil {
			return nil, err
		}
		var authority Authority
		if err := json.Unmarshal(state.CanonicalBytes(), &authority); err != nil {
			return nil, refusal(OutcomeInvalidCommand, "restore", "authority state is not the admitted proving-ground schema")
		}
		ws := service.works[workID]
		actor, known := config.Actors.ActorForProcess(authority.ProcessID)
		if authority.WorkID != workID || authority.ProjectID != config.ProjectID || authority.MissionID != config.MissionID || authority.WorkRevisionID != config.WorkRevisionID || authority.RunID != config.RunID || authority.Generation == 0 || authority.FenceToken == 0 || !known || actor != authority.ActorID {
			return nil, refusal(OutcomeInvalidCommand, "restore", "authority state does not match the sealed executable census")
		}
		ws.logical, ws.headRevision, ws.lastGen, ws.lastFence = head.LogicalVersion, record.Revision, authority.Generation, authority.FenceToken
		if authority.Status == "active" {
			if authority.RuntimeEpochID != config.RuntimeEpochID {
				return nil, refusal(OutcomeStaleEpoch, "restore", "active authority belongs to a predecessor epoch")
			}
			authority.AuthorityHeadRecordRevision = record.Revision
			ws.current = &authority
			ws.operations[authority.OperationID] = authority
		} else if authority.Status == "expired" {
			if authority.OperationID == "" {
				return nil, refusal(OutcomeInvalidCommand, "restore", "expired authority lacks its invalidation operation")
			}
			if prior, duplicate := service.invalidations[authority.OperationID]; duplicate && (prior.Kind != "lease_expired" || prior.WorkID != authority.WorkID || prior.LeaseID != authority.LeaseID || prior.Generation != authority.Generation) {
				return nil, refusal(OutcomeInvalidCommand, "restore", "invalidation operation identity is duplicated")
			}
			service.invalidations[authority.OperationID] = invalidationReceipt{Kind: "lease_expired", WorkID: authority.WorkID, LeaseID: authority.LeaseID, Generation: authority.Generation, RuntimeEpochID: authority.RuntimeEpochID}
		} else if authority.Status != "predecessor-epoch" {
			return nil, refusal(OutcomeInvalidCommand, "restore", "authority state has an unknown terminal status")
		}
	}
	runtimeKey := reducer.AggregateKeyV1{Kind: "runtime-composition", ID: controljournal.AggregateID(config.RunID)}
	if runtimeState, present := restored.Projection.State(runtimeKey); present {
		logical, ok := restored.Projection.Head(runtimeKey)
		if !ok {
			return nil, refusal(OutcomeInvalidCommand, "restore", "runtime projection lacks a head")
		}
		record, head, err := readRestoredHead(ctx, reader, prefix, runtimeKey, logical, config.RuntimeEpochID, runtimeState.Digest(), journalBound, facts.ObservedStoreRevision)
		if err != nil {
			return nil, err
		}
		var restart struct {
			OperationID      controljournal.OperationID    `json:"operation_id"`
			PredecessorEpoch controljournal.RuntimeEpochID `json:"predecessor_epoch_id"`
			RuntimeEpochID   controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
		}
		if json.Unmarshal(runtimeState.CanonicalBytes(), &restart) != nil || restart.OperationID == "" || restart.PredecessorEpoch == "" || restart.RuntimeEpochID != config.RuntimeEpochID {
			return nil, refusal(OutcomeInvalidCommand, "restore", "runtime state does not match the admitted epoch")
		}
		service.runtimeLogical, service.runtimeHeadRevision, service.predecessorEpoch = head.LogicalVersion, record.Revision, restart.PredecessorEpoch
		service.invalidations[restart.OperationID] = invalidationReceipt{Kind: "owner_restarted", PredecessorEpoch: restart.PredecessorEpoch, RuntimeEpochID: restart.RuntimeEpochID}
	}
	if err := restoreWorkspaceCensus(ctx, service, reader, prefix, restored.Projection, journalBound, facts.ObservedStoreRevision); err != nil {
		return nil, err
	}
	return service, nil
}

func validateAuthorityCensus(ctx context.Context, reader restoreHeadReader, prefix string, projection reducer.ProjectionV1, config Config, journalBound, observedBound uint64) error {
	configured := make(map[controljournal.AggregateID]struct{}, len(config.WorkIDs))
	for _, workID := range config.WorkIDs {
		configured[controljournal.AggregateID(workID)] = struct{}{}
	}
	cursor := ""
	seen := make(map[controljournal.AggregateID]struct{}, len(config.WorkIDs))
	pages := 0
	for {
		pages++
		if pages > checkpoint.MaxRestorePages {
			return refusal(OutcomeIndeterminate, "restore", "authority head census exceeded its page bound")
		}
		page, err := reader.Scan(ctx, storegapdb.ScanRequest{Prefix: prefix + "authority/", Limit: storegapdb.MaxScanRecords, Cursor: cursor, MaxBytes: storegapdb.MaxScanBytes})
		if err != nil || page.ObservedRevision != observedBound {
			return refusal(OutcomeIndeterminate, "restore", "authority head census moved beyond the sealed observation")
		}
		for _, record := range page.Records {
			if !strings.HasPrefix(record.Key, prefix+"authority/") {
				return refusal(OutcomeInvalidCommand, "restore", "authority census returned a foreign key")
			}
			id := controljournal.AggregateID(record.Key[len(prefix+"authority/"):])
			key := reducer.AggregateKeyV1{Kind: "authority", ID: id}
			state, present := projection.State(key)
			logical, hasHead := projection.Head(key)
			if _, known := configured[id]; !known || !present || !hasHead || id == "" {
				return refusal(OutcomeInvalidCommand, "restore", "durable authority census contradicts sealed playback")
			}
			if _, duplicate := seen[id]; duplicate {
				return refusal(OutcomeInvalidCommand, "restore", "durable authority census contains a duplicate")
			}
			seen[id] = struct{}{}
			if _, _, err := readRestoredHead(ctx, reader, prefix, key, logical, config.RuntimeEpochID, state.Digest(), journalBound, observedBound); err != nil {
				return err
			}
		}
		if !page.Truncated {
			break
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return refusal(OutcomeIndeterminate, "restore", "authority head census cursor made no progress")
		}
		cursor = page.Cursor
	}
	for id := range configured {
		_, projected := projection.State(reducer.AggregateKeyV1{Kind: "authority", ID: id})
		_, durable := seen[id]
		if projected != durable {
			return refusal(OutcomeInvalidCommand, "restore", "configured authority projection and durable census differ")
		}
	}
	eventCensus, err := journalAggregateCensus(ctx, reader, prefix, observedBound)
	if err != nil {
		return err
	}
	for id := range eventCensus["authority"] {
		if _, durable := seen[id]; !durable {
			return refusal(OutcomeInvalidCommand, "restore", "authority event lacks its durable aggregate head")
		}
	}
	for id := range seen {
		if _, journaled := eventCensus["authority"][id]; !journaled {
			return refusal(OutcomeInvalidCommand, "restore", "authority head lacks admitted journal history")
		}
	}
	return nil
}

func journalAggregateCensus(ctx context.Context, reader restoreHeadReader, headsPrefix string, observedBound uint64) (map[controljournal.AggregateKind]map[controljournal.AggregateID]struct{}, error) {
	base := strings.TrimSuffix(headsPrefix, "heads/")
	result := make(map[controljournal.AggregateKind]map[controljournal.AggregateID]struct{})
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > checkpoint.MaxRestorePages {
			return nil, refusal(OutcomeIndeterminate, "restore", "journal census exceeded its page bound")
		}
		page, err := reader.Scan(ctx, storegapdb.ScanRequest{Prefix: base + "journal/", Limit: storegapdb.MaxScanRecords, Cursor: cursor, MaxBytes: storegapdb.MaxScanBytes})
		if err != nil || page.ObservedRevision != observedBound {
			return nil, refusal(OutcomeIndeterminate, "restore", "journal census moved beyond the sealed observation")
		}
		for _, record := range page.Records {
			if !strings.HasPrefix(record.Key, base+"journal/") {
				return nil, refusal(OutcomeInvalidCommand, "restore", "journal census returned a foreign key")
			}
			event, err := controljournal.DecodeEventEnvelopeV1(record.Value)
			if err != nil || record.Revision == 0 || record.Revision > observedBound {
				return nil, refusal(OutcomeInvalidCommand, "restore", "journal census contains malformed or post-seal history")
			}
			if result[event.AggregateKind] == nil {
				result[event.AggregateKind] = make(map[controljournal.AggregateID]struct{})
			}
			result[event.AggregateKind][event.AggregateID] = struct{}{}
		}
		if !page.Truncated {
			return result, nil
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return nil, refusal(OutcomeIndeterminate, "restore", "journal census cursor made no progress")
		}
		cursor = page.Cursor
	}
}

func readRestoredHead(ctx context.Context, reader restoreHeadReader, prefix string, key reducer.AggregateKeyV1, logical uint64, epoch controljournal.RuntimeEpochID, digest [32]byte, journalBound, observedBound uint64) (storegapdb.Record, controljournal.AggregateHeadV1, error) {
	record, err := reader.Get(ctx, prefix+string(key.Kind)+"/"+string(key.ID))
	if err != nil || record.Revision == 0 || journalBound == 0 || observedBound == 0 || record.Revision > journalBound || record.Revision > observedBound {
		return storegapdb.Record{}, controljournal.AggregateHeadV1{}, refusal(OutcomeIndeterminate, "restore", "durable aggregate head is unavailable")
	}
	head, err := controljournal.DecodeAggregateHeadV1(record.Value)
	if err != nil || head.AggregateKind != key.Kind || head.AggregateID != key.ID || head.LogicalVersion != logical || head.RuntimeEpochID != epoch || head.StateDigest != digest {
		return storegapdb.Record{}, controljournal.AggregateHeadV1{}, refusal(OutcomeInvalidCommand, "restore", fmt.Sprintf("durable aggregate head contradicts sealed playback at %s/%s", key.Kind, key.ID))
	}
	return record, head, nil
}

func restoreWorkspaceCensus(ctx context.Context, service *service, reader restoreHeadReader, prefix string, projection reducer.ProjectionV1, journalBound, observedBound uint64) error {
	cursor := ""
	seenOps := make(map[controljournal.OperationID]struct{})
	seenIDs := make(map[controljournal.AggregateID]struct{})
	pages := 0
	for {
		pages++
		if pages > checkpoint.MaxRestorePages {
			return refusal(OutcomeIndeterminate, "restore", "workspace head census exceeded its page bound")
		}
		page, err := reader.Scan(ctx, storegapdb.ScanRequest{Prefix: prefix + "workspace/", Limit: storegapdb.MaxScanRecords, Cursor: cursor, MaxBytes: storegapdb.MaxScanBytes})
		if err != nil || page.ObservedRevision != observedBound {
			return refusal(OutcomeIndeterminate, "restore", "workspace head census is unavailable")
		}
		for _, record := range page.Records {
			if !strings.HasPrefix(record.Key, prefix+"workspace/") {
				return refusal(OutcomeInvalidCommand, "restore", "workspace census returned a foreign key")
			}
			id := controljournal.AggregateID(record.Key[len(prefix+"workspace/"):])
			key := reducer.AggregateKeyV1{Kind: "workspace", ID: id}
			state, present := projection.State(key)
			logical, hasHead := projection.Head(key)
			if !present || !hasHead || id == "" {
				return refusal(OutcomeInvalidCommand, "restore", "durable workspace census contradicts sealed playback")
			}
			_, _, err := readRestoredHead(ctx, reader, prefix, key, logical, service.epoch, state.Digest(), journalBound, observedBound)
			if err != nil {
				return err
			}
			var binding WorkspaceBinding
			if json.Unmarshal(state.CanonicalBytes(), &binding) != nil || binding.WorkspaceID != controljournal.WorkspaceID(id) || binding.WorkRevisionID != service.config.WorkRevisionID || binding.RunID != service.config.RunID || binding.LogicalVersion != logical || binding.Status != "active-bound" && binding.Status != "quarantined" {
				return refusal(OutcomeInvalidCommand, "restore", "workspace state is malformed or foreign")
			}
			if _, duplicate := seenOps[binding.OperationID]; duplicate {
				return refusal(OutcomeInvalidCommand, "restore", "workspace operation identity is duplicated")
			}
			seenOps[binding.OperationID] = struct{}{}
			seenIDs[id] = struct{}{}
			binding.RecordRevision = record.Revision
			if binding.Status == "active-bound" {
				work := service.works[binding.WorkID]
				if work == nil || work.current == nil || work.current.LeaseID != binding.LeaseID || work.current.Generation != binding.Generation || work.current.FenceToken != binding.FenceToken || binding.RuntimeEpochID != service.epoch {
					return refusal(OutcomeInvalidCommand, "restore", "active workspace lacks exact current authority")
				}
			} else if binding.QuarantineReason == "" {
				return refusal(OutcomeInvalidCommand, "restore", "quarantined workspace lacks a reason")
			}
			service.workspaces[binding.WorkspaceID] = binding
			service.workspaceOp[binding.OperationID] = binding
		}
		if !page.Truncated {
			break
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return refusal(OutcomeIndeterminate, "restore", "workspace head census cursor made no progress")
		}
		cursor = page.Cursor
	}
	eventCensus, err := journalAggregateCensus(ctx, reader, prefix, observedBound)
	if err != nil {
		return err
	}
	for id := range eventCensus["workspace"] {
		if _, durable := seenIDs[id]; !durable {
			return refusal(OutcomeInvalidCommand, "restore", "workspace event lacks its durable aggregate head")
		}
	}
	for id := range seenIDs {
		if _, journaled := eventCensus["workspace"][id]; !journaled {
			return refusal(OutcomeInvalidCommand, "restore", "workspace head lacks admitted journal history")
		}
	}
	return nil
}

func (s *service) WithFaults(hook faults.Hook) *service {
	if hook == nil {
		hook = faults.Noop{}
	}
	s.faults = hook
	return s
}

func (s *service) Claim(ctx context.Context, request ClaimRequest) (Authority, error) {
	state, err := s.validateClaim(request)
	if err != nil {
		return Authority{}, err
	}
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	if request.RuntimeEpochID != s.epoch {
		return Authority{}, refusal(OutcomeStaleEpoch, "claim", "runtime epoch changed before claim publication")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if replay, ok := state.operations[request.OperationID]; ok {
		if replay.WorkID != request.WorkID || replay.ActorID != request.ActorID || replay.ProcessID != request.ProcessID || replay.LeaseID != request.LeaseID {
			return Authority{}, refusal(OutcomeInvalidCommand, "claim", "operation identity was reused for a different claim")
		}
		return replay, nil
	}
	if state.current != nil && state.current.Status == "active" {
		return Authority{}, refusal(OutcomeAuthorityConflict, "claim", "one current authority already exists", "wait for expiry or select other work")
	}
	next := Authority{
		ProjectID: s.config.ProjectID, MissionID: s.config.MissionID, WorkRevisionID: s.config.WorkRevisionID,
		WorkID: request.WorkID, ActorID: request.ActorID, ProcessID: request.ProcessID, RunID: s.config.RunID,
		LeaseID: request.LeaseID, RuntimeEpochID: s.epoch, Generation: state.lastGen + 1,
		FenceToken: state.lastFence + 1, Deadline: request.Now.UTC().Add(s.config.LeaseTTL), Status: "active", OperationID: request.OperationID,
		GovernanceContextID: request.GovernanceContextID, GovernanceContextAlgorithm: request.GovernanceContextAlgorithm, GovernanceContextDigest: request.GovernanceContextDigest,
	}
	revision, err := s.publish(ctx, publication{
		operationID: request.OperationID, commandType: "authority.claim", eventType: "authority.claimed",
		aggregateKind: "authority", aggregateID: controljournal.AggregateID(request.WorkID), logical: state.logical + 1,
		observedRevision: state.headRevision, authority: next, resultType: "authority.claimed", payload: next,
	})
	if err != nil {
		return Authority{}, err
	}
	next.AuthorityHeadRecordRevision = revision
	state.logical++
	state.headRevision = revision
	state.lastFence, state.lastGen = next.FenceToken, next.Generation
	state.current = &next
	state.operations[request.OperationID] = next
	return next, nil
}

func (s *service) validateClaim(request ClaimRequest) (*workState, error) {
	if _, err := controljournal.NewOperationID(string(request.OperationID)); err != nil || request.Now.IsZero() {
		return nil, refusal(OutcomeInvalidCommand, "claim", "operation identity and authoritative time are required")
	}
	state := s.works[request.WorkID]
	if state == nil || request.WorkRevisionID != s.config.WorkRevisionID {
		return nil, refusal(OutcomeStaleWorkRevision, "claim", "work is not a member of the immutable executable revision")
	}
	s.epochMu.RLock()
	epoch := s.epoch
	s.epochMu.RUnlock()
	if request.RuntimeEpochID != epoch {
		return nil, refusal(OutcomeStaleEpoch, "claim", "claim belongs to a predecessor runtime epoch")
	}
	actor, known := s.config.Actors.ActorForProcess(request.ProcessID)
	if !known || actor != request.ActorID {
		return nil, refusal(OutcomeActorProcessMismatch, "claim", "process is not bound to the requested implementation actor")
	}
	if _, err := controljournal.NewLeaseID(string(request.LeaseID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "claim", "lease identity is invalid")
	}
	return state, nil
}

// Authorize validates every coordinate for heartbeat, handoff, review,
// integration, and any later governed operation. No caller may validate only
// a token or only a process identity.
func (s *service) Authorize(authority Authority, action string, now time.Time) error {
	state := s.works[authority.WorkID]
	if state == nil || authority.WorkRevisionID != s.config.WorkRevisionID {
		return refusal(OutcomeStaleWorkRevision, action, "work revision is not current")
	}
	s.epochMu.RLock()
	epoch := s.epoch
	s.epochMu.RUnlock()
	if authority.RuntimeEpochID != epoch {
		return refusal(OutcomeStaleEpoch, action, "capsule belongs to a predecessor runtime epoch")
	}
	actor, known := s.config.Actors.ActorForProcess(authority.ProcessID)
	if !known || actor != authority.ActorID {
		return refusal(OutcomeActorProcessMismatch, action, "actor and process do not match")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return s.authorizeLocked(state, authority, action, now, epoch)
}

func (s *service) authorizeLocked(state *workState, authority Authority, action string, now time.Time, epoch controljournal.RuntimeEpochID) error {
	current := state.current
	if current == nil || current.Status != "active" || authority.LeaseID != current.LeaseID || authority.Generation != current.Generation || authority.AuthorityHeadRecordRevision != current.AuthorityHeadRecordRevision {
		return refusal(OutcomeStaleLease, action, "lease is not current")
	}
	if authority.FenceToken != current.FenceToken {
		return refusal(OutcomeStaleFence, action, "fence is not current")
	}
	if authority.ActorID != current.ActorID || authority.ProcessID != current.ProcessID || authority.RunID != current.RunID {
		return refusal(OutcomeActorProcessMismatch, action, "capsule execution identity is not current")
	}
	if now.IsZero() || !now.Before(current.Deadline) {
		return refusal(OutcomeStaleLease, action, "lease deadline has passed")
	}
	return nil
}

// Heartbeat is a governed journal transition and returns a refreshed capsule.
// Merely checking a token is insufficient; every coordinate is revalidated.
func (s *service) Heartbeat(ctx context.Context, operationID controljournal.OperationID, authority Authority, now time.Time) (Authority, error) {
	state := s.works[authority.WorkID]
	if state == nil {
		return Authority{}, refusal(OutcomeStaleWorkRevision, "heartbeat", "work is unknown")
	}
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := s.authorizeLocked(state, authority, "heartbeat", now, s.epoch); err != nil {
		return Authority{}, err
	}
	next := *state.current
	next.Deadline = now.UTC().Add(s.config.LeaseTTL)
	next.OperationID = operationID
	revision, err := s.publish(ctx, publication{
		operationID: operationID, commandType: "authority.heartbeat", eventType: "authority.heartbeat",
		aggregateKind: "authority", aggregateID: controljournal.AggregateID(authority.WorkID), logical: state.logical + 1,
		observedRevision: state.headRevision, authority: next, resultType: "authority.heartbeat", payload: next,
	})
	if err != nil {
		return Authority{}, err
	}
	next.AuthorityHeadRecordRevision = revision
	state.logical++
	state.headRevision = revision
	state.current = &next
	return next, nil
}

func (s *service) Expire(ctx context.Context, operationID controljournal.OperationID, authority Authority, now time.Time) error {
	state := s.works[authority.WorkID]
	if state == nil {
		return refusal(OutcomeStaleWorkRevision, "expire", "work is unknown")
	}
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	s.commandMu.Lock()
	if receipt, ok := s.invalidations[operationID]; ok {
		s.commandMu.Unlock()
		if receipt.Kind == "lease_expired" && receipt.WorkID == authority.WorkID && receipt.LeaseID == authority.LeaseID && receipt.Generation == authority.Generation && receipt.RuntimeEpochID == authority.RuntimeEpochID {
			return nil
		}
		return refusal(OutcomeInvalidCommand, "expire", "operation identity was reused for a different invalidation")
	}
	s.commandMu.Unlock()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.current == nil {
		return refusal(OutcomeStaleLease, "expire", "lease is not current")
	}
	validationTime := state.current.Deadline.Add(-time.Nanosecond)
	if err := s.authorizeLocked(state, authority, "expire", validationTime, s.epoch); err != nil {
		return err
	}
	if now.Before(state.current.Deadline) {
		return refusal(OutcomeAuthorityConflict, "expire", "live lease cannot expire before its deadline")
	}
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	next := *state.current
	next.Status, next.OperationID = "expired", operationID
	transitions := []publication{{eventType: "authority.expired", aggregateKind: "authority", aggregateID: controljournal.AggregateID(authority.WorkID), logical: state.logical + 1, observedRevision: state.headRevision, authority: next, payload: next}}
	quarantined := make([]WorkspaceBinding, 0, 1)
	for _, binding := range s.workspaces {
		if binding.LeaseID != authority.LeaseID || binding.Generation != authority.Generation || binding.Status != "active-bound" {
			continue
		}
		binding.Status, binding.QuarantineReason = "quarantined", "lease generation expired"
		binding.LogicalVersion++
		quarantined = append(quarantined, binding)
	}
	sort.Slice(quarantined, func(i, j int) bool { return quarantined[i].WorkspaceID < quarantined[j].WorkspaceID })
	transitions = transitions[:1]
	for _, binding := range quarantined {
		transitions = append(transitions, publication{eventType: "workspace.quarantined", aggregateKind: "workspace", aggregateID: controljournal.AggregateID(binding.WorkspaceID), logical: binding.LogicalVersion, observedRevision: binding.RecordRevision, authority: authority, payload: binding})
	}
	result := invalidationResult{Kind: "lease_expired", RuntimeEpochID: s.epoch, Authorities: []Authority{next}, Workspaces: quarantined}
	if err := s.faults.Reach(ctx, "PG-EXPIRE-BEFORE-APPLY"); err != nil {
		return &Error{Kind: OutcomeAmbiguous, Action: "expire", Detail: "expiry was cut before durable application", Recovery: []string{"retry the same operation ID"}, Cause: err}
	}
	revision, err := s.publishCommand(ctx, s.applier, s.epoch, operationID, "authority.expire", "authority.expired", result, transitions)
	if err != nil {
		return err
	}
	if err := s.faults.Reach(ctx, "PG-EXPIRE-BEFORE-PROJECTION"); err != nil {
		return &Error{Kind: OutcomeAmbiguous, Action: "expire", Detail: "expiry committed before process projection", Recovery: []string{"retry the same operation ID"}, Cause: err}
	}
	next.AuthorityHeadRecordRevision = revision
	state.logical++
	state.headRevision = revision
	state.current = nil
	for _, binding := range quarantined {
		binding.RecordRevision = revision
		s.workspaces[binding.WorkspaceID] = binding
	}
	s.commandMu.Lock()
	s.invalidations[operationID] = invalidationReceipt{Kind: "lease_expired", WorkID: authority.WorkID, LeaseID: authority.LeaseID, Generation: authority.Generation, RuntimeEpochID: authority.RuntimeEpochID}
	s.commandMu.Unlock()
	return nil
}

// ReplaceRuntime separates owner restart from lease replacement. The caller
// must supply an application service admitted under the fresh runtime epoch.
// This process-local swap is the explicit M1 product-host boundary; WP10 owns
// multi-process qualification.
func (s *service) ReplaceRuntime(ctx context.Context, operationID controljournal.OperationID, epoch controljournal.RuntimeEpochID, applier batchApplier, now time.Time) error {
	if applier == nil || epoch == "" || now.IsZero() {
		return refusal(OutcomeInvalidCommand, "restart", "fresh epoch, admitted application service, and authoritative time are required")
	}
	s.epochMu.Lock()
	defer s.epochMu.Unlock()
	s.commandMu.Lock()
	if receipt, ok := s.invalidations[operationID]; ok {
		s.commandMu.Unlock()
		if receipt.Kind == "owner_restarted" && receipt.PredecessorEpoch == s.predecessorEpoch && receipt.RuntimeEpochID == epoch {
			return nil
		}
		return refusal(OutcomeInvalidCommand, "restart", "operation identity was reused for a different invalidation")
	}
	s.commandMu.Unlock()
	old := s.epoch
	if epoch == old {
		return refusal(OutcomeInvalidCommand, "restart", "runtime epoch must be fresh")
	}
	type invalidatedLease struct {
		WorkID     controljournal.WorkID    `json:"work_id"`
		ActorID    controljournal.ActorID   `json:"actor_id"`
		ProcessID  controljournal.ProcessID `json:"process_id"`
		LeaseID    controljournal.LeaseID   `json:"lease_id"`
		Generation uint64                   `json:"generation"`
		FenceToken uint64                   `json:"fence_token"`
	}
	invalidated := make([]invalidatedLease, 0, len(s.works))
	locked := make([]*workState, 0, len(s.config.WorkIDs))
	for _, workID := range s.config.WorkIDs {
		state := s.works[workID]
		state.mu.Lock()
		locked = append(locked, state)
		if state.current != nil && state.current.RuntimeEpochID == old {
			invalidated = append(invalidated, invalidatedLease{WorkID: workID, ActorID: state.current.ActorID, ProcessID: state.current.ProcessID, LeaseID: state.current.LeaseID, Generation: state.current.Generation, FenceToken: state.current.FenceToken})
		}
	}
	defer func() {
		for index := len(locked) - 1; index >= 0; index-- {
			locked[index].mu.Unlock()
		}
	}()
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	restart := struct {
		OperationID      controljournal.OperationID    `json:"operation_id"`
		RunID            controljournal.RunID          `json:"run_id"`
		PredecessorEpoch controljournal.RuntimeEpochID `json:"predecessor_epoch_id"`
		RuntimeEpochID   controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
		OccurredAt       time.Time                     `json:"occurred_at"`
		Invalidated      []invalidatedLease            `json:"invalidated_authorities"`
	}{operationID, s.config.RunID, old, epoch, now.UTC(), invalidated}
	transitions := []publication{{eventType: "runtime.owner_restarted", aggregateKind: "runtime-composition", aggregateID: controljournal.AggregateID(s.config.RunID), logical: s.runtimeLogical + 1, observedRevision: s.runtimeHeadRevision, payload: restart}}
	stale := make([]Authority, 0, len(invalidated))
	for _, workID := range s.config.WorkIDs {
		state := s.works[workID]
		if state.current == nil || state.current.RuntimeEpochID != old {
			continue
		}
		prior := *state.current
		next := prior
		next.Status, next.OperationID = "predecessor-epoch", operationID
		stale = append(stale, next)
		transitions = append(transitions, publication{eventType: "authority.predecessor_invalidated", aggregateKind: "authority", aggregateID: controljournal.AggregateID(workID), logical: state.logical + 1, observedRevision: state.headRevision, authority: prior, payload: next})
	}
	quarantined := make([]WorkspaceBinding, 0, len(s.workspaces))
	for _, binding := range s.workspaces {
		if binding.RuntimeEpochID != old || binding.Status != "active-bound" {
			continue
		}
		binding.Status, binding.QuarantineReason = "quarantined", "predecessor runtime epoch"
		binding.LogicalVersion++
		quarantined = append(quarantined, binding)
	}
	sort.Slice(quarantined, func(i, j int) bool { return quarantined[i].WorkspaceID < quarantined[j].WorkspaceID })
	for _, binding := range quarantined {
		transitions = append(transitions, publication{eventType: "workspace.quarantined", aggregateKind: "workspace", aggregateID: controljournal.AggregateID(binding.WorkspaceID), logical: binding.LogicalVersion, observedRevision: binding.RecordRevision, authority: Authority{RunID: binding.RunID, WorkID: binding.WorkID, ActorID: binding.ActorID, ProcessID: binding.ProcessID, LeaseID: binding.LeaseID, Generation: binding.Generation, FenceToken: binding.FenceToken}, payload: binding})
	}
	result := invalidationResult{Kind: "owner_restarted", RuntimeEpochID: epoch, PredecessorEpochID: old, Authorities: stale, Workspaces: quarantined}
	if err := s.faults.Reach(ctx, "PG-RESTART-BEFORE-APPLY"); err != nil {
		return &Error{Kind: OutcomeAmbiguous, Action: "restart", Detail: "restart was cut before durable application", Recovery: []string{"retry the same operation ID"}, Cause: err}
	}
	revision, err := s.publishCommand(ctx, applier, epoch, operationID, "runtime.owner_restart", "runtime.owner_restarted", result, transitions)
	if err != nil {
		return err
	}
	if err := s.faults.Reach(ctx, "PG-RESTART-BEFORE-PROJECTION"); err != nil {
		return &Error{Kind: OutcomeAmbiguous, Action: "restart", Detail: "restart committed before process projection", Recovery: []string{"retry the same operation ID"}, Cause: err}
	}
	s.epoch, s.applier = epoch, applier
	s.runtimeLogical++
	s.runtimeHeadRevision = revision
	s.predecessorEpoch = old
	for _, state := range locked {
		if state.current != nil && state.current.RuntimeEpochID == old {
			state.logical++
			state.headRevision = revision
			state.current = nil
		}
	}
	for _, binding := range quarantined {
		binding.RecordRevision = revision
		s.workspaces[binding.WorkspaceID] = binding
	}
	s.commandMu.Lock()
	s.invalidations[operationID] = invalidationReceipt{Kind: "owner_restarted", PredecessorEpoch: old, RuntimeEpochID: epoch}
	s.commandMu.Unlock()
	return nil
}

// Projection exposes a read-only diagnostic snapshot. Its concrete type is
// private and there is intentionally no production constructor accepting it.
func (s *service) Projection() projection {
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	projection := projection{Runtime: runtimeProjection{RuntimeEpochID: s.epoch, PredecessorEpoch: s.predecessorEpoch, LogicalVersion: s.runtimeLogical, HeadRecordRevision: s.runtimeHeadRevision}}
	for _, workID := range s.config.WorkIDs {
		state := s.works[workID]
		state.mu.Lock()
		entry := workProjection{WorkID: workID, LogicalVersion: state.logical, HeadRecordRevision: state.headRevision, LastGeneration: state.lastGen, LastFence: state.lastFence}
		if state.current != nil {
			current := *state.current
			entry.Current = &current
		}
		state.mu.Unlock()
		projection.Works = append(projection.Works, entry)
	}
	s.workspaceMu.RLock()
	for _, binding := range s.workspaces {
		projection.Workspaces = append(projection.Workspaces, binding)
	}
	s.workspaceMu.RUnlock()
	return projection
}

func (s *service) Current(workID controljournal.WorkID) (Authority, bool) {
	state := s.works[workID]
	if state == nil {
		return Authority{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.current == nil {
		return Authority{}, false
	}
	return *state.current, true
}

type publication struct {
	operationID      controljournal.OperationID
	commandType      string
	eventType        controljournal.EventType
	aggregateKind    controljournal.AggregateKind
	aggregateID      controljournal.AggregateID
	logical          uint64
	observedRevision uint64
	authority        Authority
	resultType       string
	payload          any
}

type invalidationResult struct {
	Kind               string                        `json:"kind"`
	RuntimeEpochID     controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	PredecessorEpochID controljournal.RuntimeEpochID `json:"predecessor_epoch_id,omitempty"`
	Authorities        []Authority                   `json:"authorities"`
	Workspaces         []WorkspaceBinding            `json:"workspaces"`
}

func (s *service) publish(ctx context.Context, input publication) (uint64, error) {
	return s.publishCommand(ctx, s.applier, s.epoch, input.operationID, input.commandType, input.resultType, input.payload, []publication{input})
}

func (s *service) publishCommand(ctx context.Context, applier batchApplier, epoch controljournal.RuntimeEpochID, operationID controljournal.OperationID, commandType, resultType string, canonicalResult any, inputs []publication) (uint64, error) {
	if len(inputs) == 0 {
		return 0, refusal(OutcomeInvalidCommand, commandType, "command must affect at least one aggregate")
	}
	resultPayload, err := json.Marshal(canonicalResult)
	if err != nil {
		return 0, refusal(OutcomeInvalidCommand, commandType, "result cannot be canonicalized")
	}
	resultPayload, err = controljournal.CanonicalJSONObject(resultPayload)
	if err != nil {
		return 0, refusal(OutcomeInvalidCommand, commandType, "result must be a canonical object")
	}
	commandPayload, err := json.Marshal(struct {
		OperationID controljournal.OperationID `json:"operation_id"`
		Result      json.RawMessage            `json:"result"`
	}{OperationID: operationID, Result: resultPayload})
	if err != nil {
		return 0, refusal(OutcomeInvalidCommand, commandType, "command cannot be canonicalized")
	}
	command, err := application.ValidateCommandEnvelopeV1(application.CommandEnvelopeInputV1{
		SchemaVersion: 1, ProjectID: s.config.ProjectID, MissionID: s.config.MissionID,
		OperationID: operationID, CommandType: commandType, RuntimeEpochID: epoch, Payload: commandPayload,
	})
	if err != nil {
		return 0, err
	}
	count, err := controljournal.NewEventCountV1(uint16(len(inputs)))
	if err != nil {
		return 0, err
	}
	events := make([]controljournal.EventEnvelopeV1, 0, len(inputs))
	expected := make([]controljournal.ExpectedAggregateHeadV1, 0, len(inputs))
	next := make([]controljournal.NewAggregateHeadV1, 0, len(inputs))
	for ordinal, input := range inputs {
		payload, err := json.Marshal(input.payload)
		if err != nil {
			return 0, refusal(OutcomeInvalidCommand, commandType, "payload cannot be canonicalized")
		}
		payload, err = controljournal.CanonicalJSONObject(payload)
		if err != nil {
			return 0, refusal(OutcomeInvalidCommand, commandType, "event payload must be a canonical object")
		}
		index, err := controljournal.NewEventIndexV1(uint16(ordinal))
		if err != nil {
			return 0, err
		}
		observations, _ := json.Marshal(map[string]any{
			"actor_id": input.authority.ActorID, "process_id": input.authority.ProcessID,
			"run_id": input.authority.RunID, "work_id": input.authority.WorkID,
		})
		eventID := stableIndexedEventID(operationID, ordinal)
		event, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
			EnvelopeVersion: 1, EventID: eventID, EventType: input.eventType, EventSchemaVersion: 1,
			ReducerVersion: "proving-ground-v1", ProjectID: s.config.ProjectID,
			AggregateKind: input.aggregateKind, AggregateID: input.aggregateID, AggregateLogicalVersion: input.logical,
			OperationID: operationID, RuntimeEpochID: epoch, EventIndex: index, EventCount: count,
			WorkRevisionID: s.config.WorkRevisionID, LeaseID: input.authority.LeaseID,
			LeaseGeneration: input.authority.Generation, FenceToken: input.authority.FenceToken,
			PolicyRevision: "m1", DoctrineRevision: "m1", ProfileRevision: "implementer-ivan",
			RecordedObservations: observations, Payload: payload,
		})
		if err != nil {
			return 0, err
		}
		digest := sha256.Sum256(payload)
		head, err := controljournal.ConstructAggregateHeadV1(input.aggregateKind, input.aggregateID, input.logical, epoch, eventID, digest)
		if err != nil {
			return 0, err
		}
		expected = append(expected, controljournal.ExpectedAggregateHeadV1{
			AggregateKind: input.aggregateKind, AggregateID: input.aggregateID,
			LogicalVersion: input.logical - 1, ObservedHeadRevision: input.observedRevision,
		})
		next = append(next, controljournal.NewAggregateHeadV1{Head: head})
		events = append(events, event)
	}
	preconditions, err := application.CanonicalPreconditionsDigestV1(expected)
	if err != nil {
		return 0, err
	}
	batch, err := application.NewValidatedBatchV1(application.BatchInputV1{
		Command: command, ExpectedHeads: expected, NextHeads: next,
		Events: events, ExpectedPreconditionsDigest: preconditions,
		ResultType: resultType, ResultSchemaVersion: 1, CanonicalResult: resultPayload,
	})
	if err != nil {
		return 0, err
	}
	result, err := applier.Apply(ctx, batch)
	if err != nil {
		return 0, err
	}
	switch result.Kind {
	case application.OutcomeCommitted, application.OutcomeAlreadyCommitted:
		if result.CommittedRevision == 0 {
			return 0, refusal(OutcomeIndeterminate, commandType, "accepted operation has no durable revision")
		}
		return result.CommittedRevision, nil
	case application.OutcomeLogicalVersionConflict, application.OutcomeHeadRevisionConflict:
		return 0, refusal(OutcomeAuthorityConflict, commandType, "aggregate authority changed concurrently")
	case application.OutcomeRuntimeEpochConflict:
		return 0, refusal(OutcomeStaleEpoch, commandType, "runtime epoch is no longer admitted")
	case application.OutcomeAmbiguousResponse, application.OutcomeIndeterminate:
		return 0, refusal(OutcomeAmbiguous, commandType, "durable result requires same-operation reconciliation", "retry the same operation ID")
	default:
		return 0, refusal(OutcomeIndeterminate, commandType, "journal application refused the transition")
	}
}

func stableIndexedEventID(operationID controljournal.OperationID, ordinal int) controljournal.EventID {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", operationID, ordinal)))
	return controljournal.EventID("event-" + hex.EncodeToString(digest[:16]))
}

// RunFocusedCampaign executes one source-bound proving scenario repeatedly.
// The trial owns concrete assertions; this helper supplies bounded cancellation
// and enforces the M1 minimum repetition count used by WP09's combined runner.
func RunFocusedCampaign(ctx context.Context, repetitions int, trial func(context.Context, int) error) error {
	if repetitions < 100 || trial == nil {
		return refusal(OutcomeInvalidCommand, "campaign", "at least 100 repetitions and a trial are required")
	}
	for index := 0; index < repetitions; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := trial(ctx, index); err != nil {
			return fmt.Errorf("campaign trial %d: %w", index, err)
		}
	}
	return nil
}
