package productionruntime

// INSTALLED AUTHOR WORK (V2). NOT independently reviewed or qualified.
// Original FIVE-base operation+workspace-claim roots only: 256*5 + 671/672
// effects = 1951/1952 rows; SIX physical roles, not seven/all ten. Provider-free
// overlay diagnostics are not qualification. Native use still requires
// independent method review and a fresh ordinary rebuild.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"syscall"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

const hashWorkReportEnv = "SPEC_KITTY_OBSERVATION_HASH_WORK_REPORT"
const hashWorkCustodyEnv = "SPEC_KITTY_OBSERVATION_HASH_WORK_CUSTODY"
const hashWorkImageSHA256 = "947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad"
const hashWorkImageBytes uint64 = 5327126
const hashWorkCeiling uint64 = 32 << 30
const hashWorkSamples = 30

// A test-only independent checked planner, NOT a replacement for the private
// production gate. Separate ordinary tests must check the actual store planner
// and source ordering. The result is a reservation, not measured hashing.
func hashWorkPlanWork(image, rows uint64) (calls, hashes, bytes uint64, accepted bool) {
	if image == 0 || rows > 4096 {
		return
	}
	calls = rows + 195 + 3
	hashes = 3*calls + 1
	if image > ^uint64(0)/hashes {
		return calls, hashes, 0, false
	}
	bytes = hashes * image
	return calls, hashes, bytes, image <= hashWorkCeiling/hashes
}

// Every distinct root preserves the original operation-only event/head and
// permanent claim. No nonoperation schema or extra aggregate is introduced.
func hashWorkSealPlan(epoch journalv2.RuntimeEpochID, database journalv2.DatabaseID, authority journalv2.HostAuthorityID, index int, oneExtra bool) (journalv2.SealedCommitPlanV2, error) {
	if index < 0 || index >= 256 || epoch == "" || database == "" || authority == "" {
		return journalv2.SealedCommitPlanV2{}, errors.New("invalid hash-work fixture coordinate")
	}
	label := fmt.Sprintf("%s-%03d", authority, index) // Also disjoint across independent Host roots.
	opID := journalv2.OperationID("operation-hash-work-" + label)
	opEvent := journalv2.EventID("event-hash-operation-" + label)
	eventIDs := []journalv2.EventID{opEvent}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	effectCount := 2
	if index < 159 {
		effectCount++ // 159*3 + 97*2 = 671 legitimate effect records
	}
	if oneExtra && index == 255 {
		effectCount++ // Independent second root has 672 effects, not a forged ACK.
	}
	effects := make([]journalv2.EffectIntentReference, 0, effectCount)
	effectIDs := make([]journalv2.EffectID, 0, effectCount)
	for i := 0; i < effectCount; i++ {
		id := journalv2.EffectID(fmt.Sprintf("effect-hash-%s-%02d", label, i))
		// A generic canonical retained intent fixture, not an executable effect
		// grant or a claim that a capability-owned effect command was executed.
		canonical, err := journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"action":"retain_hash_work_fixture","effect_id":%q,"operation_id":%q,"schema_version":1}`, id, opID)))
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		digest, err := journalv2.ReducerStateDigestV2(canonical)
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		effects = append(effects, journalv2.EffectIntentReference{EffectID: id, Digest: digest, Canonical: canonical})
		effectIDs = append(effectIDs, id)
	}
	sort.Slice(effectIDs, func(i, j int) bool { return effectIDs[i] < effectIDs[j] })
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: opID, LogicalVersion: 1, Status: journalv2.StatusAccepted, EventIDs: eventIDs, EffectIDs: effectIDs})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	payload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	coordinate, err := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(opID), scope)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	digest, err := journalv2.ReducerStateDigestV2(payload)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	heads := []journalv2.HeadTransitionV2{{Coordinate: coordinate, NextLogicalVersion: 1, EventID: opEvent, CommittedRuntimeEpochID: epoch, CanonicalStateDigest: digest, AuthoritySummary: payload}}
	events := []journalv2.EventDraftV2{{EventID: opEvent, EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: epoch, ActorID: "actor-hash-work", ProcessID: "process-hash-work", Payload: payload}}
	conditions := make([]journalv2.ProviderCondition, 0, len(heads))
	for _, head := range heads {
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		conditions = append(conditions, condition)
	}
	// One canonical operation-head absent condition; sealing validates it.
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "proof.hash_work", CommandSchemaVersion: 1, Scope: scope, Command: []byte(fmt.Sprintf(`{"root":%q}`, label))})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: opID, CommandKind: "proof.hash_work", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope, ActorID: "actor-hash-work", ProcessID: "process-hash-work", OriginatingRuntimeEpochID: epoch, ObservedCreatedAt: "2026-09-05T12:01:00Z"})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	claimDigest, err := journalv2.ReducerStateDigestV2([]byte(fmt.Sprintf(`{"path":%q}`, "/hash-work-fixture/"+label)))
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	claim, err := journalv2.NewWorkspacePathClaimV2(claimDigest, []byte(fmt.Sprintf(`{"workspace_id":%q}`, "workspace-hash-work-"+label)))
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	return journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: events, Heads: heads, EffectIntents: effects, Conditions: conditions, WorkspacePathClaim: &claim}, journalv2.DefaultEventRegistryV2())
}

type hashWorkGeometry struct {
	ACKs          int                                `json:"actual_acks"`
	Rows          uint64                             `json:"measured_publication_rows"`
	Effects       int                                `json:"effects"`
	Roles         map[journalv2.SealedRecordRole]int `json:"record_role_counts"`
	Unique        bool                               `json:"all_semantic_rows_unique"`
	FiveBaseRoots int                                `json:"five_base_roots"`
}

type hashWorkRow struct {
	Role   journalv2.SealedRecordRole
	Target journalv2.SealedRecordTargetV2
}

// Pure source/unit surface; only REAL sealed plans enter. No ACK constructor,
// provider, count callback, structural Host or native execution is needed.
func hashWorkInspectPlans(ctx context.Context, plans []journalv2.SealedCommitPlanV2, want uint64) (hashWorkGeometry, error) {
	g := hashWorkGeometry{Roles: make(map[journalv2.SealedRecordRole]int), Unique: true}
	seen := make(map[hashWorkRow]bool)
	reducers, err := BuildReducerSetV2(journalv2.DefaultEventRegistryV2())
	if err != nil {
		return g, err
	}
	for _, plan := range plans {
		footprint, err := plan.MeasureRecordFootprintV2(ctx)
		if err != nil {
			return g, err
		}
		base := 5
		g.FiveBaseRoots++
		if footprint.RecordCount != uint64(base+len(plan.EffectIntents())) || footprint.CheckpointRecordCount != 0 || footprint.AssertionCount != 0 || plan.BindingCreate() == nil || plan.WorkspacePathClaim() == nil {
			return g, errors.New("sealed plan geometry mismatch")
		}
		g.Rows += footprint.RecordCount
		g.Effects += len(plan.EffectIntents())
		stateIDs := plan.OperationState().EffectIDs()
		for j, effect := range plan.EffectIntents() {
			digest, err := journalv2.ReducerStateDigestV2(effect.Canonical)
			if err != nil || digest != effect.Digest || len(stateIDs) != len(plan.EffectIntents()) || stateIDs[j] != effect.EffectID || (j > 0 && stateIDs[j-1] >= stateIDs[j]) {
				return g, errors.New("effect bytes/digest/operation membership mismatch")
			}
		}
		for _, record := range plan.Records() {
			row := hashWorkRow{record.Role(), record.Target()}
			if seen[row] || record.Condition().Kind() != journalv2.ConditionAbsent || record.Condition().ObservedRevision() != 0 {
				return g, errors.New("duplicate or non-root row")
			}
			seen[row] = true
			g.Roles[record.Role()]++
		}
		for _, event := range plan.Events() {
			if event.Coordinate().Scope != (journalv2.Scope{Kind: journalv2.ScopeHost}) {
				return g, errors.New("non-host fixture coordinate")
			}
			// Actual closed reducers must accept each initial event. The
			// coordinator and live sink repeat this validation before timing.
			if _, _, err := reducers.Apply([]byte(`{}`), event); err != nil {
				return g, err
			}
		}
	}
	wantEffects := 671
	if want == 1952 {
		wantEffects++
	}
	if len(plans) != 256 || g.Rows != want || len(seen) != int(want) || g.Effects != wantEffects || g.FiveBaseRoots != 256 || len(g.Roles) != 6 || g.Roles[journalv2.RecordOperationBinding] != 256 || g.Roles[journalv2.RecordOperationHead] != 256 || g.Roles[journalv2.RecordJournalEvent] != 256 || g.Roles[journalv2.RecordAggregateHead] != 0 || g.Roles[journalv2.RecordWorkspacePathClaim] != 256 || g.Roles[journalv2.RecordPublicationWitness] != 256 || g.Roles[journalv2.RecordEffectIntent] != wantEffects {
		return g, errors.New("aggregate sealed geometry mismatch")
	}
	return g, nil
}

type hashWorkACKRecorder struct {
	ACKs []journalv2.AcknowledgedPublicationV2
}

func (r *hashWorkACKRecorder) ObservePublicationBoundary(_ context.Context, snapshot journalv2.PublicationBoundarySnapshotV2) {
	if snapshot.Stage == journalv2.PublicationAckCreatedBeforeProjectionV2 && snapshot.Ack != nil {
		r.ACKs = append(r.ACKs, *snapshot.Ack)
	}
}

type hashWorkFileBinding struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type hashWorkCustody struct {
	// The runner must supply ALL package/module/dependency/adoption inputs in
	// Sources and a complete source inventory artifact, not merely this draft.
	Sources                   []hashWorkFileBinding `json:"sources"`
	SourceInventory           hashWorkFileBinding   `json:"source_inventory"`
	BuildCommand              hashWorkFileBinding   `json:"build_command"`
	RunCommand                hashWorkFileBinding   `json:"run_command"`
	GoEnvironment             hashWorkFileBinding   `json:"go_environment"`
	RawEnvironment            hashWorkFileBinding   `json:"raw_environment"`
	ToolchainVersion          hashWorkFileBinding   `json:"toolchain_version"`
	MethodReview              hashWorkFileBinding   `json:"method_review"`
	ExecutableSHA256          string                `json:"test_executable_sha256"`
	ProviderSHA256            string                `json:"provider_sha256"`
	IndependentMethodApproved bool                  `json:"independent_method_approved"`
	OrdinaryBuild             bool                  `json:"ordinary_build"`
}

type hashWorkInvariant struct {
	AttributionSame   bool `json:"attribution_unchanged"`
	BaselineSame      bool `json:"baseline_unchanged"`
	Ready             bool `json:"host_still_ready"`
	CompositionSame   bool `json:"composition_identity_unchanged"`
	DatabaseSame      bool `json:"database_unchanged"`
	EpochSame         bool `json:"epoch_unchanged"`
	DataDirectorySame bool `json:"data_directory_identity_unchanged"`
	StatusSame        bool `json:"host_status_unchanged"`
}

func (v hashWorkInvariant) all() bool {
	return v.AttributionSame && v.BaselineSame && v.Ready && v.CompositionSame && v.DatabaseSame && v.EpochSame && v.DataDirectorySame && v.StatusSame
}

type hashWorkSample struct {
	Index              int                      `json:"index"`
	Nanoseconds        int64                    `json:"nanoseconds"`
	ExpectedOutcome    string                   `json:"expected_outcome"`
	ObservedOutcome    string                   `json:"observed_outcome"`
	ExpectedOutcomeMet bool                     `json:"expected_outcome_met"`
	Matched            bool                     `json:"matched"` // Positive proof ONLY; an expected refusal is false.
	ProofZero          bool                     `json:"proof_zero"`
	ErrorPresent       bool                     `json:"error_present"`
	ErrorType          string                   `json:"error_type"`
	PublicationRows    uint64                   `json:"matched_publication_rows"`
	PrivateRows        uint64                   `json:"matched_private_rows"`
	TotalRows          uint64                   `json:"matched_total_rows"`
	ACKs               int                      `json:"supplied_acks"`
	Before             projection.AttributionV2 `json:"before"`
	After              projection.AttributionV2 `json:"after"`
	ProofAttribution   projection.AttributionV2 `json:"proof_attribution"`
	Invariants         hashWorkInvariant        `json:"invariants"`
	FailureClass       string                   `json:"failure_class,omitempty"`
}

type hashWorkCase struct {
	Name              string                   `json:"name"`
	Root              int                      `json:"independent_root"`
	ExpectedOutcome   string                   `json:"expected_outcome"`
	ACKs              int                      `json:"supplied_acks"`
	Rows              uint64                   `json:"sealed_publication_rows"`
	ReservedCalls     uint64                   `json:"reserved_calls"`
	ReservedHashes    uint64                   `json:"reserved_hashes"`
	ReservedHashBytes uint64                   `json:"reserved_hash_bytes"`
	PlannerAccepts    bool                     `json:"independent_planner_accepts"`
	Samples           []hashWorkSample         `json:"samples"`
	Statistics        retainedPerformanceStats `json:"statistics"`
}

func hashWorkCases() []hashWorkCase {
	cases := []hashWorkCase{
		{Name: "pub1951_match", Root: 0, ExpectedOutcome: "matched", ACKs: 256, Rows: 1951},
		{Name: "pub1951_already_canceled", Root: 0, ExpectedOutcome: "canceled", ACKs: 256, Rows: 1951},
		{Name: "ack257_scalar_refuse", Root: 0, ExpectedOutcome: "sanitized_refusal", ACKs: 257, Rows: 1951},
		{Name: "pub1952_hash_preflight_refuse", Root: 1, ExpectedOutcome: "sanitized_refusal", ACKs: 256, Rows: 1952},
	}
	for i := range cases {
		c := &cases[i]
		c.ReservedCalls, c.ReservedHashes, c.ReservedHashBytes, c.PlannerAccepts = hashWorkPlanWork(hashWorkImageBytes, c.Rows)
	}
	return cases
}
func hashWorkObserved(proof RetainedRecordsProof, err error) string {
	if err == nil {
		if proof == (RetainedRecordsProof{}) {
			return "zero_success"
		}
		return "matched"
	}
	if proof != (RetainedRecordsProof{}) {
		return "nonzero_error_proof"
	}
	if err == context.Canceled {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if e, ok := err.(*ProgressError); ok && e != nil && e.Kind == ProgressIncomplete && e.Field == "retained_records" && e.Error() == "PROGRESS_INCOMPLETE: retained_records" {
		return "sanitized_refusal"
	}
	return "other_error"
}
func hashWorkExpected(c hashWorkCase, s hashWorkSample) bool {
	if s.Index < 0 || s.Index >= hashWorkSamples || s.Nanoseconds <= 0 || !s.Invariants.all() || s.Before != s.After || s.ACKs != c.ACKs || s.ExpectedOutcome != c.ExpectedOutcome || s.ObservedOutcome != c.ExpectedOutcome || s.Before.DatabaseID == "" || s.Before.RuntimeEpochID == "" || s.Before.Revision == 0 || s.Before.DurableThrough != s.Before.Revision || s.Before.CursorToken.IsZero() {
		return false
	}
	// These types follow from hashWorkObserved's exact class definitions: nil,
	// the exact context.Canceled sentinel, or a direct nonnil *ProgressError.
	// Type text alone is not evidence that the underlying error was observed.
	if c.ExpectedOutcome == "matched" {
		return c.Rows == 1951 && s.Matched && !s.ProofZero && !s.ErrorPresent && s.ErrorType == "<nil>" && s.PublicationRows == 1951 && s.PrivateRows == 6 && s.TotalRows == 1957 && s.ProofAttribution == s.Before
	}
	switch c.ExpectedOutcome {
	case "canceled":
		if s.ErrorType != fmt.Sprintf("%T", context.Canceled) {
			return false
		}
	case "sanitized_refusal":
		if s.ErrorType != fmt.Sprintf("%T", (*ProgressError)(nil)) {
			return false
		}
	default:
		return false
	}
	return !s.Matched && s.ProofZero && s.ErrorPresent && s.PublicationRows == 0 && s.PrivateRows == 0 && s.TotalRows == 0 && s.ProofAttribution == (projection.AttributionV2{})
}

type hashWorkRoot struct {
	Root             string                   `json:"evidence_root"`
	Authority        string                   `json:"authority_root"`
	Runtime          string                   `json:"runtime_root"`
	Database         journalv2.DatabaseID     `json:"database_id"`
	Epoch            journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	RuntimeDirectory string                   `json:"runtime_directory"`
	AdmissionSHA256  string                   `json:"admission_sha256"`
	Geometry         hashWorkGeometry         `json:"geometry"`
	Closed           bool                     `json:"closed"`
	CloseError       string                   `json:"close_error,omitempty"`
}
type hashWorkReport struct {
	Schema                  int                 `json:"schema_version"`
	Stage                   string              `json:"stage"`
	GoVersion               string              `json:"go_version"`
	GOOS                    string              `json:"goos"`
	GOARCH                  string              `json:"goarch"`
	Environment             []string            `json:"raw_environment"`
	BuildInfo               *debug.BuildInfo    `json:"build_info"`
	Provider                hashWorkFileBinding `json:"provider"`
	ProviderBytes           int                 `json:"provider_bytes"`
	Executable              hashWorkFileBinding `json:"test_executable"`
	CustodyFile             hashWorkFileBinding `json:"custody_file"`
	Custody                 hashWorkCustody     `json:"custody"`
	Roots                   []hashWorkRoot      `json:"roots"`
	Cases                   []hashWorkCase      `json:"cases"`
	Failures                []string            `json:"failures"`
	Complete                bool                `json:"complete"`
	WholeLayerQualified     bool                `json:"whole_layer_qualified"`
	WholeMemoryQualified    bool                `json:"whole_memory_qualified"`
	GenuineExpiryQualified  bool                `json:"genuine_expiry_qualified"`
	PerformanceSLOQualified bool                `json:"performance_slo_qualified"`
	Disclosures             []string            `json:"disclosures"`
}

// Complete describes in-memory timed cases and Host cleanup checked BEFORE the
// final report descriptor write/sync/Close. Measurement acceptance additionally
// requires external raw command exit0, successful write/sync/Close outcomes and
// independently reconciled source/ELF/profile custody. This JSON is not its own
// attestation. A late failure invalidates that attempt; a rerun cannot repair it.
func hashWorkFinalize(r *hashWorkReport, failed, closed bool) {
	r.Complete = false
	r.WholeLayerQualified, r.WholeMemoryQualified, r.GenuineExpiryQualified, r.PerformanceSLOQualified = false, false, false, false
	valid := r.Schema == 1 && r.Stage == "final" && !failed && closed && len(r.Failures) == 0 && len(r.Roots) == 2 && len(r.Cases) == 4 && r.GoVersion == "go1.26.7" && r.GOOS == "linux" && r.GOARCH == "amd64" && r.Provider.Path != "" && r.Provider.SHA256 == hashWorkImageSHA256 && uint64(r.ProviderBytes) == hashWorkImageBytes && r.Executable.Path != "" && r.Executable.SHA256 != "" && r.CustodyFile.Path != "" && r.CustodyFile.SHA256 != "" && r.Custody.ExecutableSHA256 == r.Executable.SHA256 && r.Custody.ProviderSHA256 == hashWorkImageSHA256 && r.Custody.IndependentMethodApproved && r.Custody.OrdinaryBuild
	want := hashWorkCases()
	paths := make(map[string]bool)
	for i, root := range r.Roots {
		// Constructor topology: independent sibling evidence roots, each with
		// separate authority/runtime children and one runtime session child.
		// This is lexical report consistency, not filesystem authentication.
		if root.Root == "" || root.Authority == "" || root.Runtime == "" || root.RuntimeDirectory == "" || root.AdmissionSHA256 == "" || filepath.Dir(root.Root) == root.Root || root.Authority != filepath.Join(root.Root, "authority") || root.Runtime != filepath.Join(root.Root, "runtime") || filepath.Dir(root.RuntimeDirectory) != root.Runtime {
			valid = false
		}
		for _, path := range []string{root.Root, root.Authority, root.Runtime, root.RuntimeDirectory} {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path || paths[path] {
				valid = false
			}
			paths[path] = true
		}
		g := root.Geometry
		if i > 1 || !root.Closed || root.CloseError != "" || root.Database == "" || root.Epoch == "" || g.ACKs != 256 || !g.Unique || g.Rows != uint64(1951+i) || g.Effects != 671+i || g.FiveBaseRoots != 256 || len(g.Roles) != 6 || g.Roles[journalv2.RecordOperationBinding] != 256 || g.Roles[journalv2.RecordOperationHead] != 256 || g.Roles[journalv2.RecordJournalEvent] != 256 || g.Roles[journalv2.RecordAggregateHead] != 0 || g.Roles[journalv2.RecordWorkspacePathClaim] != 256 || g.Roles[journalv2.RecordPublicationWitness] != 256 || g.Roles[journalv2.RecordEffectIntent] != 671+i {
			valid = false
		}
	}
	if len(r.Roots) == 2 && (filepath.Dir(r.Roots[0].Root) != filepath.Dir(r.Roots[1].Root) || r.Roots[0].Database == r.Roots[1].Database || r.Roots[0].Epoch == r.Roots[1].Epoch) {
		valid = false
	}
	for i := range r.Cases {
		c := &r.Cases[i]
		rootValid := c.Root >= 0 && c.Root < len(r.Roots)
		if !rootValid || i >= len(want) || c.Root != want[i].Root {
			valid = false
		}
		durations := make([]time.Duration, 0, len(c.Samples))
		for j, s := range c.Samples {
			durations = append(durations, time.Duration(s.Nanoseconds))
			if s.Index != j || !s.ExpectedOutcomeMet || !hashWorkExpected(*c, s) || s.FailureClass != "" {
				valid = false
			}
			if rootValid {
				root := r.Roots[c.Root]
				if s.Before.DatabaseID != root.Database || s.After.DatabaseID != root.Database || s.Before.RuntimeEpochID != root.Epoch || s.After.RuntimeEpochID != root.Epoch || (c.ExpectedOutcome == "matched" && (s.ProofAttribution.DatabaseID != root.Database || s.ProofAttribution.RuntimeEpochID != root.Epoch)) {
					valid = false
				}
			}
		}
		var ok bool
		c.Statistics, ok = retainedPerformanceStatistics(durations)
		if !ok || len(c.Samples) != hashWorkSamples || i >= len(want) {
			valid = false
			continue
		}
		w := want[i]
		if c.Name != w.Name || c.Root != w.Root || c.ExpectedOutcome != w.ExpectedOutcome || c.ACKs != w.ACKs || c.Rows != w.Rows || c.ReservedCalls != w.ReservedCalls || c.ReservedHashes != w.ReservedHashes || c.ReservedHashBytes != w.ReservedHashBytes || c.PlannerAccepts != w.PlannerAccepts {
			valid = false
		}
	}
	r.Complete = valid
}
func hashWorkReserve(path string) (*os.File, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	parent := filepath.Dir(absolute)
	info, err := os.Lstat(parent)
	if err != nil {
		return nil, "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	real, realErr := filepath.EvalSymlinks(parent)
	if !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !ok || stat.Uid != uint32(os.Geteuid()) || realErr != nil || real != parent {
		return nil, "", errors.New("requires real runner-owned0700 report directory without symlink ancestry")
	}
	out, err := os.OpenFile(absolute, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, parent, err
	}
	// Requested mode is not actual mode (umask/ACLs may restrict it). Inspect
	// the exclusive descriptor, not a pathname that may have been replaced.
	created, err := out.Stat() // fstat on the newly created descriptor
	if err == nil {
		actual, ok := created.Sys().(*syscall.Stat_t)
		if !created.Mode().IsRegular() || created.Mode().Perm() != 0600 || created.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !ok || actual.Uid != uint32(os.Geteuid()) || actual.Nlink != 1 {
			err = errors.New("requires actual regular runner-owned0600 single-link report file")
		}
	}
	if err != nil {
		// Preserve the exclusive file even on refusal; never erase evidence.
		return nil, parent, errors.Join(err, out.Close())
	}
	return out, parent, nil
}
func hashWorkWrite(out *os.File, report hashWorkReport) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if _, err := out.WriteAt(body, 0); err != nil {
		return err
	}
	if err := out.Truncate(int64(len(body))); err != nil {
		return err
	}
	return out.Sync()
}
func hashWorkBindFile(path string) (hashWorkFileBinding, []byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return hashWorkFileBinding{}, nil, err
	}
	body, err := os.ReadFile(absolute)
	if err != nil {
		return hashWorkFileBinding{}, nil, err
	}
	digest := sha256.Sum256(body)
	return hashWorkFileBinding{Path: absolute, SHA256: hex.EncodeToString(digest[:])}, body, nil
}
func hashWorkCheckCustody(c hashWorkCustody, executable string) error {
	if !c.IndependentMethodApproved || !c.OrdinaryBuild || c.ExecutableSHA256 != executable || c.ProviderSHA256 != hashWorkImageSHA256 || len(c.Sources) == 0 {
		return errors.New("missing independently reviewed source/build/image custody")
	}
	files := append([]hashWorkFileBinding(nil), c.Sources...)
	files = append(files, c.SourceInventory, c.BuildCommand, c.RunCommand, c.GoEnvironment, c.RawEnvironment, c.ToolchainVersion, c.MethodReview)
	for _, input := range files {
		if input.Path == "" || input.SHA256 == "" {
			return errors.New("empty custody binding")
		}
		got, _, err := hashWorkBindFile(input.Path)
		if err != nil || got != input {
			return errors.New("custody bytes/path mismatch")
		}
	}
	// These booleans and hashes are custody inputs, NOT independent-review
	// authentication. The reviewer must externally verify manifest completeness,
	// review authorship, exact build/run command and effective environment.
	return nil
}

func TestRetainedHashWorkOriginalHostNative(t *testing.T) {
	binary, reportPath, custodyPath := os.Getenv("SPEC_KITTY_WP13_GAPDBD"), os.Getenv(hashWorkReportEnv), os.Getenv(hashWorkCustodyEnv)
	if binary == "" && reportPath == "" && custodyPath == "" {
		t.Skip("requires independent method review before explicit original-image hash-work observation")
	}
	if binary == "" || reportPath == "" || custodyPath == "" {
		t.Fatal("exact image, own exclusive report, and reviewed custody manifest are all required")
	}
	out, parent, err := hashWorkReserve(reportPath) // BEFORE any provider, root or execution.
	if err != nil {
		t.Fatal(err)
	}
	r := hashWorkReport{Schema: 1, Stage: "reserved", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Environment: os.Environ(), Cases: hashWorkCases(), Disclosures: []string{
		"V2: all256 original five-base roots, 671/672 effects, exactly1951/1952 publication rows; SIX physical roles, not seven/all ten. No nonoperation schema.",
		"Original WP13 actual Start-owned Host/adapter/coordinator/live sink; no fake ACK, structural Host, altered provider, counters or policy changes.",
		"30 first-inclusive sequential samples EACH case; ONLY VerifyRetainedRecords timed; no warmup, retry, discarded samples, forcedGC, allocation instrumentation, calibration or intentional background load/compilation during timing.",
		"1951/1952 are reservation gate boundaries at this exact image: 6448/6451 reserved full hashes (34349308448/34365289826 bytes). Positive nominal actual-six-private path is 31328828006 bytes, ratio0.911789, NOT full32GiB consumed. Private reads actually visit six rows, not the reserved195; near32GiB describes conservative reservation, NOT actual measured hash bytes.",
		"An expected sanitized refusal is NOT a successful positive proof. Source/unit gate ordering, not latency, establishes the intended refusal seam; public error alone cannot prove its private cause.",
		"ACK257 is a bounded scalar probe with an extra COPY of a genuine opaque ACK; it is not 257 distinct successful publications. Cancellation uses an independent already-canceled context and valid-root ACKs.",
		"All roots/raw samples and first unexpected prefix preserved beneath report directory on failure; Close may remove lifecycle-owned ephemeral sockets. No evidence-root deletion.",
		"Aggregate-head, checkpoint record/slot, eligibility-guard and large-value/all-role work remain OPEN; no memory/RSS/alloc, SLO, whole-layer, concurrent-load or genuine-expiry qualification. Cooperative deadlines remain; no hard-wall guarantee.",
		"Runner source/build/raw environment/command custody and independent review are mandatory; manifest flags/self hashes alone are NOT qualification.",
		"Complete means in-memory timed cases and Host cleanup checked BEFORE final report descriptor write/sync/Close. ANY measurement acceptance additionally requires external raw command exit0, successful write/sync/Close outcomes and independently reconciled source/ELF/profile custody. Own JSON is not self-attestation; a late failure invalidates this attempt and cannot be repaired by rerunning.",
	}}
	var h *Host
	activeRoot := -1
	closeHost := func() {
		if h == nil {
			return
		}
		err := h.Close(context.Background())
		r.Roots[activeRoot].Closed = err == nil && h.State() == HostClosed
		if err != nil {
			r.Roots[activeRoot].CloseError = err.Error()
			r.Failures = append(r.Failures, "Host Close failed: "+err.Error())
			t.Error(err)
		}
		h = nil // Never retry a failed Close into apparent success.
	}
	defer func() {
		r.Stage = "pre_close_prefix"
		hashWorkFinalize(&r, t.Failed(), false)
		if err := hashWorkWrite(out, r); err != nil {
			r.Failures = append(r.Failures, "prefix write: "+err.Error())
			t.Error(err)
		}
		closeHost()
		r.Stage = "final"
		hashWorkFinalize(&r, t.Failed(), true)
		if err := hashWorkWrite(out, r); err != nil {
			t.Error(err)
		}
		if err := out.Close(); err != nil {
			t.Error(err)
		}
	}()
	fail := func(message string, err error) {
		if err != nil {
			message += ": " + err.Error()
		}
		r.Failures = append(r.Failures, message)
		t.Fatal(message)
	}
	if runtime.Version() != "go1.26.7" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Getenv("GOAMD64") != "v1" || os.Getenv("CGO_ENABLED") != "1" {
		fail("requires ordinary Go1.26.7/linux/amd64/GOAMD64=v1/CGO_ENABLED=1", nil)
	}
	for _, key := range []string{"GOFLAGS", "GOEXPERIMENT", "GODEBUG"} {
		if os.Getenv(key) != "" {
			fail("nonordinary profile: "+key, nil)
		}
	}
	r.BuildInfo, _ = debug.ReadBuildInfo()
	if r.BuildInfo == nil {
		fail("missing build information", nil)
	}
	for _, setting := range r.BuildInfo.Settings {
		switch setting.Key {
		case "-race", "-msan", "-asan", "-cover", "-tags", "-gcflags", "-asmflags", "-ldflags":
			if setting.Value != "" && setting.Value != "false" {
				fail("nonordinary executable setting "+setting.Key, nil)
			}
		}
	}
	var image []byte
	r.Provider, image, err = hashWorkBindFile(binary)
	if err != nil {
		fail("read image", err)
	}
	r.ProviderBytes = len(image)
	if r.Provider.SHA256 != hashWorkImageSHA256 || uint64(len(image)) != hashWorkImageBytes {
		fail("not exact historical WP13 image", nil)
	}
	self, err := os.Executable()
	if err != nil {
		fail("executable path", err)
	}
	r.Executable, _, err = hashWorkBindFile(self)
	if err != nil {
		fail("executable binding", err)
	}
	var custodyBytes []byte
	r.CustodyFile, custodyBytes, err = hashWorkBindFile(custodyPath)
	if err != nil {
		fail("custody read", err)
	}
	if err := json.Unmarshal(custodyBytes, &r.Custody); err != nil {
		fail("custody JSON", err)
	}
	if err := hashWorkCheckCustody(r.Custody, r.Executable.SHA256); err != nil {
		fail("custody verification", err)
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fail("repository path", err)
	}
	for rootIndex := 0; rootIndex < 2; rootIndex++ {
		root, err := os.MkdirTemp(parent, fmt.Sprintf("hash-root-%d-", rootIndex))
		if err != nil {
			fail("create evidence root", err)
		}
		r.Roots = append(r.Roots, hashWorkRoot{Root: root, Authority: filepath.Join(root, "authority"), Runtime: filepath.Join(root, "runtime")})
		activeRoot = rootIndex
		record := &r.Roots[rootIndex]
		for _, path := range []string{record.Authority, record.Runtime} {
			if err := os.Mkdir(path, 0700); err != nil {
				fail("private root", err)
			}
		}
		dataInfo, err := os.Stat(record.Authority)
		if err != nil {
			fail("data identity", err)
		}
		observer := &hashWorkACKRecorder{}
		config := Config{ControlRoot: record.Authority, HostAuthorityID: journalv2.HostAuthorityID(fmt.Sprintf("host-hash-work-%d", rootIndex)), AuthorityBindingOperationID: fmt.Sprintf("operation-hash-binding-%d", rootIndex),
			M1AdoptionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-adoption.json"), AssertionExtensionAdoptionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-adoption.json"),
			Sidecar:             sidecargapdb.LifecycleConfig{ExecutablePath: r.Provider.Path, AdmissionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-admission.json"), RuntimeRoot: record.Runtime, DataDirectory: record.Authority, SupervisorRunID: fmt.Sprintf("supervisor-hash-work-%d", rootIndex), ProductPolicySHA256: repeatByte('e', 64)},
			CapabilityReadiness: absentReadinessSource{}, DecompositionProvider: refusingDecompositionProvider{}, CandidateVerifier: refusingCandidateVerifier{}, IntegrationGit: refusingIntegrationGit{}, PublicationBoundaryObserver: observer, MonitorInterval: time.Hour}
		h, err = Start(t.Context(), config)
		if err != nil {
			fail("Start original Host", err)
		}
		if h.State() != HostReady || len(observer.ACKs) != 0 {
			fail("unexpected startup ACK/state", nil)
		}
		record.Database, record.Epoch = h.DatabaseID(), h.RuntimeEpochID()
		owner := h.owner.(*productionOwner)
		provenance := owner.lifecycle.Provenance()
		record.RuntimeDirectory, record.AdmissionSHA256 = provenance.RuntimeDirectory, provenance.AdmissionRecordSHA256
		plans := make([]journalv2.SealedCommitPlanV2, 0, 256)
		for i := 0; i < 256; i++ {
			plan, err := hashWorkSealPlan(h.RuntimeEpochID(), h.DatabaseID(), config.HostAuthorityID, i, rootIndex == 1)
			if err != nil {
				fail("seal root plan", err)
			}
			plans = append(plans, plan)
		}
		record.Geometry, err = hashWorkInspectPlans(t.Context(), plans, uint64(1951+rootIndex))
		if err != nil {
			fail("sealed geometry", err)
		}
		registry := journalv2.DefaultEventRegistryV2()
		reducers, err := BuildReducerSetV2(registry)
		if err != nil {
			fail("actual reducers", err)
		}
		tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
		if err != nil {
			fail("actual tracker", err)
		}
		coordinator, err := journalv2.NewPublicationCoordinatorWithObserverAndSinkV2(owner.adapter, tracker, observer, &liveProofSink{host: h})
		if err != nil {
			fail("actual coordinator", err)
		}
		for i, plan := range plans {
			ack, err := coordinator.Publish(t.Context(), plan)
			if err != nil {
				fail("actual Publish", err)
			}
			if len(observer.ACKs) != i+1 || observer.ACKs[i].PublicationID() != ack.PublicationID() || ack.PlanDigest() != plan.Digest() || ack.MutationCount() != plan.MutationCount() {
				fail("actual opaque ACK binding", nil)
			}
		}
		ackPlans := make([]journalv2.SealedCommitPlanV2, 0, len(observer.ACKs))
		for _, ack := range observer.ACKs {
			ackPlans = append(ackPlans, ack.Plan())
		}
		record.Geometry, err = hashWorkInspectPlans(t.Context(), ackPlans, uint64(1951+rootIndex))
		if err != nil {
			fail("actual ACK Plan measured geometry", err)
		}
		record.Geometry.ACKs = len(observer.ACKs)
		if record.Geometry.ACKs != 256 {
			fail("ACK cardinality", nil)
		}
		baseline, projectionObject, ownerObject, retainedObject := h.projection.Attribution(), h.projection, h.owner, h.retained
		baselineStatus := h.Status()
		for caseIndex := range r.Cases {
			c := &r.Cases[caseIndex]
			if c.Root != rootIndex {
				continue
			}
			acks := append([]journalv2.AcknowledgedPublicationV2(nil), observer.ACKs...)
			if c.ACKs == 257 {
				acks = append(acks, observer.ACKs[0])
			} // Opaque copy, no fake ACK.
			ctx := t.Context()
			if c.ExpectedOutcome == "canceled" {
				independent, cancel := context.WithCancel(context.Background())
				cancel()
				ctx = independent
			}
			if len(acks) != c.ACKs {
				fail("supplied ACK count", nil)
			}
			for i := 0; i < hashWorkSamples; i++ {
				before, stateBefore, statusBefore := h.projection.Attribution(), h.State(), h.Status()
				start := time.Now()
				proof, proofErr := h.VerifyRetainedRecords(ctx, acks)
				duration := time.Since(start)
				// Every assertion, report operation and filesystem check is outside timing.
				after := h.projection.Attribution()
				currentData, dataErr := os.Stat(record.Authority)
				class := hashWorkObserved(proof, proofErr)
				s := hashWorkSample{Index: i, Nanoseconds: int64(duration), ExpectedOutcome: c.ExpectedOutcome, ObservedOutcome: class, Matched: class == "matched", ProofZero: proof == (RetainedRecordsProof{}), ErrorPresent: proofErr != nil, ErrorType: fmt.Sprintf("%T", proofErr), PublicationRows: proof.MatchedPublicationRecords(), PrivateRows: proof.MatchedPrivateRecords(), TotalRows: proof.MatchedRecords(), ACKs: len(acks), Before: before, After: after, ProofAttribution: proof.Attribution(), Invariants: hashWorkInvariant{AttributionSame: before == after, BaselineSame: before == baseline && after == baseline, Ready: stateBefore == HostReady && h.State() == HostReady, CompositionSame: h.projection == projectionObject && h.owner == ownerObject && h.retained == retainedObject, DatabaseSame: h.DatabaseID() == record.Database, EpochSame: h.RuntimeEpochID() == record.Epoch, DataDirectorySame: dataErr == nil && os.SameFile(dataInfo, currentData) && owner.lifecycle.Provenance().DataDirectory == record.Authority, StatusSame: statusBefore == baselineStatus && h.Status() == baselineStatus}}
				s.ExpectedOutcomeMet = hashWorkExpected(*c, s)
				if !s.ExpectedOutcomeMet {
					s.FailureClass = "unexpected_outcome_or_invariant"
				}
				c.Samples = append(c.Samples, s) // Preserve first unexpected sample BEFORE Fatal.
				if !s.ExpectedOutcomeMet {
					fail(fmt.Sprintf("%s sample%d: %s", c.Name, i, class), nil)
				}
			}
		}
		closeHost() // No other deliberately live workload/provider during samples.
		if t.Failed() {
			fail("Close failed", nil)
		}
	}
}

// Provider-free diagnostic units below are intentionally selected alone by the
// ADD-ONLY overlay. They never Start a Host or invoke a provider/native test.
func TestHashWorkV2DiagnosticPlanner(t *testing.T) {
	const H uint64 = 32 << 30
	if got := ((H/hashWorkImageBytes - 1) / 3) - 198; got != 1951 {
		t.Fatalf("rmax=%d", got)
	}
	for _, tc := range []struct {
		r, calls, hashes, bytes uint64
		ok                      bool
	}{
		{1951, 2149, 6448, 34349308448, true}, {1952, 2150, 6451, 34365289826, false},
	} {
		c, h, b, ok := hashWorkPlanWork(hashWorkImageBytes, tc.r)
		if c != tc.calls || h != tc.hashes || b != tc.bytes || ok != tc.ok {
			t.Fatalf("row%d got %d,%d,%d,%v", tc.r, c, h, b, ok)
		}
		t.Logf("rows=%d reserved_calls=%d hashes=%d bytes=%d accepted=%v", tc.r, c, h, b, ok)
	}
	for _, tc := range [][2]uint64{{0, 1951}, {hashWorkImageBytes, 4097}, {^uint64(0), 1951}, {hashWorkImageBytes, ^uint64(0)}} {
		if _, _, _, ok := hashWorkPlanWork(tc[0], tc[1]); ok {
			t.Fatal("invalid/overflow planner accepted", tc)
		}
	}
	if actual := (3*(uint64(1951)+6+3) + 1) * hashWorkImageBytes; actual != 31328828006 {
		t.Fatal("nominal actual hash arithmetic", actual)
	}
}
func TestHashWorkV2DiagnosticSealedGeometry(t *testing.T) {
	allRows := make(map[hashWorkRow]bool)
	for root := 0; root < 2; root++ {
		plans := make([]journalv2.SealedCommitPlanV2, 0, 256)
		epoch := journalv2.RuntimeEpochID(fmt.Sprintf("epoch-hash-diagnostic-%d", root))
		db := journalv2.DatabaseID(fmt.Sprintf("database-hash-diagnostic-%d", root))
		authority := journalv2.HostAuthorityID(fmt.Sprintf("host-hash-diagnostic-%d", root))
		var measured uint64
		for i := 0; i < 256; i++ {
			plan, err := hashWorkSealPlan(epoch, db, authority, i, root == 1)
			if err != nil {
				t.Fatalf("root%d plan%d seal: %v", root, i, err)
			}
			f, err := plan.MeasureRecordFootprintV2(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			effects := 2
			if i < 159 {
				effects++
			}
			if root == 1 && i == 255 {
				effects++
			}
			if f.RecordCount != uint64(5+effects) || len(plan.Events()) != 1 || len(plan.Heads()) != 1 || len(plan.Conditions()) != 1 {
				t.Fatalf("root%d plan%d geometry=%+v", root, i, f)
			}
			state := plan.OperationState()
			if len(state.EventIDs()) != 1 || len(state.EffectIDs()) != effects {
				t.Fatal("operation membership count")
			}
			for _, event := range plan.Events() {
				if event.Family() != "operation" || event.EventSchemaVersion() != 2 || event.ReducerVersion() != 2 || event.CommittedRuntimeEpochID() != epoch {
					t.Fatal("non-original operation tuple/epoch")
				}
			}
			for _, row := range plan.Records() {
				key := hashWorkRow{row.Role(), row.Target()}
				if allRows[key] {
					t.Fatal("row repeated within/across roots")
				}
				allRows[key] = true
			}
			measured += f.RecordCount
			plans = append(plans, plan)
		}
		g, err := hashWorkInspectPlans(t.Context(), plans, uint64(1951+root))
		if err != nil {
			t.Fatalf("root%d measured geometry: %v", root, err)
		}
		if measured != uint64(1951+root) || g.Effects != 671+root || len(g.Roles) != 6 || g.FiveBaseRoots != 256 {
			t.Fatal("wrong summed real footprint", g)
		}
		t.Logf("root=%d REAL_SEAL plans=%d measured_rows=%d effects=%d roles=%d unique=true operation_reducer=accepted", root, len(plans), measured, g.Effects, len(g.Roles))
	}
	if len(allRows) != 3903 {
		t.Fatal("cross-root row uniqueness", len(allRows))
	}
}
