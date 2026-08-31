// Package m1 contains the observation-only qualification contracts for the M1
// proving ground. It deliberately owns no product state and cannot mutate the
// control journal, Git repository, tracker, or GapDB.
package m1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

const (
	InvariantOneAuthority       = "one_valid_authority_per_work_revision"
	InvariantWorkspaceImmutable = "workspace_binding_immutable"
	InvariantStaleRefused       = "stale_authority_refused"
	InvariantPrimaryIsolation   = "primary_isolation"
	InvariantReviewIndependent  = "reviewer_actor_independence"
	InvariantIntegrationSerial  = "serialized_target_authority"
	InvariantEffectAttribution  = "effect_attribution"
	InvariantJournalComplete    = "journal_complete"
)

var InvariantIDs = []string{
	InvariantOneAuthority, InvariantWorkspaceImmutable, InvariantStaleRefused,
	InvariantPrimaryIsolation, InvariantReviewIndependent,
	InvariantIntegrationSerial, InvariantEffectAttribution, InvariantJournalComplete,
}

// SourceBinding is minted from the checkout Git object identities and an
// admitted, live external sidecar observation. The private seal prevents a
// caller from turning well-shaped hexadecimal strings into qualification
// provenance.
type SourceBinding struct {
	Commit                  string `json:"commit"`
	Tree                    string `json:"tree"`
	WorktreeTree            string `json:"worktree_tree"`
	WorktreeStatusSHA256    string `json:"worktree_status_sha256"`
	AdmittedTupleSHA256     string `json:"admitted_tuple_sha256"`
	SidecarProvenanceSHA256 string `json:"sidecar_provenance_sha256"`
	SidecarExecutableSHA256 string `json:"sidecar_executable_sha256"`
	DatabaseID              string `json:"database_id"`
	OwnerPID                int    `json:"owner_pid"`
	seal                    [32]byte
}

// ObserveSource crosses both inspectable authorities used by M1 evidence: Git
// for the candidate source and the admitted public GapDB adapter for the
// sidecar process. A stopped, foreign, skewed, or caller-invented lifecycle
// cannot mint a binding.
func ObserveSource(ctx context.Context, root string, lifecycle *sidecargapdb.Lifecycle) (SourceBinding, error) {
	if strings.TrimSpace(root) == "" || lifecycle == nil {
		return SourceBinding{}, fmt.Errorf("source root and admitted sidecar lifecycle are required")
	}
	commit, tree, worktreeTree, statusDigest, err := observeGitSource(ctx, root)
	if err != nil {
		return SourceBinding{}, fmt.Errorf("observe executable source: %w", err)
	}
	evidence, err := lifecycle.Evidence()
	if err != nil {
		return SourceBinding{}, fmt.Errorf("observe admitted sidecar evidence: %w", err)
	}
	provenance := lifecycle.Provenance()
	adapter, closer, err := storegapdb.Dial(provenance.ConfiguredSocket, 2*time.Second)
	if err != nil {
		return SourceBinding{}, fmt.Errorf("dial admitted sidecar: %w", err)
	}
	defer closer.Close()
	inspection, err := adapter.Inspect(ctx)
	if err != nil {
		return SourceBinding{}, fmt.Errorf("inspect admitted sidecar: %w", err)
	}
	if !inspection.Healthy || inspection.DatabaseID == "" || inspection.DatabaseID != evidence.Status.DatabaseID || inspection.OwnerPID <= 0 || inspection.OwnerPID != evidence.HostPID ||
		evidence.AdmittedTupleSHA256 != storegapdb.AdmittedTupleSHA256 || evidence.ExecutableSHA256 != storegapdb.AdmittedSidecarSHA256 || evidence.HostProcessStartTicks == 0 {
		return SourceBinding{}, fmt.Errorf("git/sidecar source authorities are incomplete, foreign, or unadmitted")
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return SourceBinding{}, fmt.Errorf("encode sidecar provenance: %w", err)
	}
	provenanceDigest := sha256.Sum256(encoded)
	binding := SourceBinding{
		Commit: commit, Tree: tree, WorktreeTree: worktreeTree, WorktreeStatusSHA256: statusDigest,
		AdmittedTupleSHA256:     evidence.AdmittedTupleSHA256,
		SidecarProvenanceSHA256: hex.EncodeToString(provenanceDigest[:]), SidecarExecutableSHA256: evidence.ExecutableSHA256,
		DatabaseID: inspection.DatabaseID, OwnerPID: inspection.OwnerPID,
	}
	binding.seal = sourceSeal(binding)
	return binding, nil
}

func (s SourceBinding) Valid() bool {
	return hexSized(s.Commit, 40) && hexSized(s.Tree, 40) && s.WorktreeTree == s.Tree && s.WorktreeStatusSHA256 == emptySHA256 && s.AdmittedTupleSHA256 == storegapdb.AdmittedTupleSHA256 &&
		hexSized(s.SidecarProvenanceSHA256, 64) && s.SidecarExecutableSHA256 == storegapdb.AdmittedSidecarSHA256 &&
		s.DatabaseID != "" && s.OwnerPID > 0 && s.seal == sourceSeal(s)
}

func sourceSeal(source SourceBinding) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d", source.Commit, source.Tree, source.WorktreeTree, source.WorktreeStatusSHA256, source.AdmittedTupleSHA256, source.SidecarProvenanceSHA256, source.SidecarExecutableSHA256, source.DatabaseID, source.OwnerPID)))
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// observeGitSource proves that the checkout Git will compile is exactly the
// pinned HEAD tree. The ordinary status census detects tracked and untracked
// bytes, while the ignored-file census rejects every ignored untracked file.
// Qualification cannot know which arbitrary paths a compiler, generator, or
// Go embed directive may consume, so an extension allowlist is not sound.
func observeGitSource(ctx context.Context, root string) (commit, tree, worktreeTree, statusDigest string, err error) {
	commit, err = gitObject(ctx, root, "HEAD")
	if err != nil {
		return "", "", "", "", fmt.Errorf("source commit: %w", err)
	}
	tree, err = gitObject(ctx, root, "HEAD^{tree}")
	if err != nil {
		return "", "", "", "", fmt.Errorf("source tree: %w", err)
	}
	worktreeTree, err = gitOutput(ctx, root, "write-tree")
	if err != nil {
		return "", "", "", "", fmt.Errorf("source index tree is unavailable: %w", err)
	}
	if !hexSized(worktreeTree, 40) {
		return "", "", "", "", fmt.Errorf("source index tree has invalid shape")
	}
	status, err := gitOutput(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", "", "", "", fmt.Errorf("source status: %w", err)
	}
	ignoredFiles, err := gitOutput(ctx, root, "ls-files", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return "", "", "", "", fmt.Errorf("ignored worktree file census: %w", err)
	}
	digest := sha256.Sum256([]byte(status))
	statusDigest = hex.EncodeToString(digest[:])
	if status != "" || ignoredFiles != "" || worktreeTree != tree || statusDigest != emptySHA256 {
		return "", "", "", "", fmt.Errorf("working tree contains bytes outside pinned HEAD tree")
	}
	return commit, tree, worktreeTree, statusDigest, nil
}

func gitObject(ctx context.Context, root, revision string) (string, error) {
	value, err := gitOutput(ctx, root, "rev-parse", "--verify", revision)
	if err != nil {
		return "", err
	}
	if !hexSized(value, 40) {
		return "", fmt.Errorf("git object identity has invalid shape")
	}
	return value, nil
}

func gitOutput(ctx context.Context, root string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, arguments...)...)
	encoded, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(encoded)), nil
}

func hexSized(value string, size int) bool {
	if len(value) != size {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ObservationPort is the only state seam used by qualification assertions.
// Implementations must inspect public product facts. A port that cannot inspect
// a required fact returns an error; the evaluator records false, never true.
type ObservationPort interface {
	ObserveM1(context.Context) (Observation, error)
	ProbeStaleAuthority(context.Context) (StaleProbe, error)
}

type Observation struct {
	Capabilities CapabilityObservation
	Journal      JournalObservation
	Authorities  []AuthorityObservation
	Workspaces   []WorkspaceObservation
	Epoch        EpochObservation
	Reviews      []ReviewObservation
	Integration  []IntegrationObservation
	Effects      []EffectObservation
	Playback     PlaybackObservation
	Primary      PrimaryObservation
}

// CapabilityObservation distinguishes an inspected empty domain from a domain
// the runner could not inspect. Absence of capability always fails closed.
type CapabilityObservation struct {
	Authority   bool
	Workspace   bool
	Review      bool
	Integration bool
	Effects     bool
}

type JournalObservation struct {
	Inspectable bool
	Operations  []JournalOperation
	Heads       []JournalHead
}

type JournalOperation struct {
	OperationID      string
	RequestDigest    string
	ResultDigest     string
	RecordRevision   uint64
	EventCount       int
	Events           []JournalEvent
	Terminal         bool
	CanonicalResult  []byte
	ObservedHeadKeys []string
}

type JournalEvent struct {
	EventID        string
	Index          int
	Count          int
	RecordRevision uint64
	AggregateKey   string
	LogicalVersion uint64
	PayloadDigest  string
}

type JournalHead struct {
	AggregateKey   string
	LogicalVersion uint64
	RecordRevision uint64
	LastEventID    string
	PayloadDigest  string
}

type AuthorityObservation struct {
	WorkRevisionID string
	WorkID         string
	ActorID        string
	ProcessID      string
	LeaseID        string
	RuntimeEpochID string
	Generation     uint64
	FenceToken     uint64
	Current        bool
	Expired        bool
}

type WorkspaceObservation struct {
	WorkspaceID    string
	WorkRevisionID string
	WorkID         string
	LeaseID        string
	RuntimeEpochID string
	Generation     uint64
	CanonicalPath  string
	OwnedBranch    string
	Status         string
	PrimaryPath    string
	Head           string
	Transferred    bool
	Registered     bool
}

type GitSnapshotObservation struct {
	Head        string
	IndexDigest string
	TreeDigest  string
}

type PrimaryObservation struct {
	Inspectable bool
	Before      GitSnapshotObservation
	After       GitSnapshotObservation
}

type EpochObservation struct {
	Inspectable      bool
	Current          string
	Predecessor      string
	EveryCurrentUses bool
}

type ReviewObservation struct {
	CandidateID        string
	ContributorIDs     []string
	ReviewerActorID    string
	Verdict            string
	CandidateImmutable bool
}

type IntegrationObservation struct {
	RepositoryID        string
	TargetRef           string
	ActiveAuthorities   int
	CandidateID         string
	SuccessfulPublishes int
	GitExpectedOld      string
	GitObservedOld      string
	GitResult           string
	JournalResult       string
}

type EffectObservation struct {
	EffectID              string
	OperationID           string
	ExpectedRevision      string
	AppliedRevision       string
	ExpectedValue         string
	AppliedValue          string
	ExpectedMarker        string
	AppliedMarker         string
	CurrentRevision       string
	CurrentValue          string
	CurrentMarker         string
	Dispatches            int
	CompensationAttempted bool
	CompensationApplied   bool
	Attributable          bool
	HumanValuePreserved   bool
}

type PlaybackObservation struct {
	Inspectable     bool
	RestoreDigest   string
	PlaybackDigest  string
	AdapterCalls    uint64
	DurableWrites   uint64
	StableCanonical bool
}

type StaleProbe struct {
	Attempted        bool
	Refused          bool
	LeaseStale       bool
	EpochStale       bool
	AuthorityChanges uint64
	WorkspaceChanges uint64
}

type Assertion struct {
	ID         string `json:"id"`
	Passed     bool   `json:"passed"`
	Diagnostic string `json:"diagnostic"`
}

type InvariantSummary struct {
	OneValidAuthorityPerWorkRevision bool        `json:"one_valid_authority_per_work_revision"`
	WorkspaceBindingImmutable        bool        `json:"workspace_binding_immutable"`
	StaleAuthorityRefused            bool        `json:"stale_authority_refused"`
	PrimaryIsolation                 bool        `json:"primary_isolation"`
	ReviewerActorIndependence        bool        `json:"reviewer_actor_independence"`
	SerializedTargetAuthority        bool        `json:"serialized_target_authority"`
	EffectAttribution                bool        `json:"effect_attribution"`
	JournalComplete                  bool        `json:"journal_complete"`
	Assertions                       []Assertion `json:"assertions"`
}

func (s InvariantSummary) Passed() bool {
	return s.OneValidAuthorityPerWorkRevision && s.WorkspaceBindingImmutable &&
		s.StaleAuthorityRefused && s.PrimaryIsolation && s.ReviewerActorIndependence &&
		s.SerializedTargetAuthority && s.EffectAttribution && s.JournalComplete
}

// AssertUniversal evaluates every invariant even if observation or a previous
// invariant fails. This makes failure evidence complete and prevents a missing
// inspection capability from becoming a vacuous pass.
func AssertUniversal(ctx context.Context, port ObservationPort) InvariantSummary {
	if port == nil {
		return failedSummary("observation port is absent")
	}
	observation, observeErr := port.ObserveM1(ctx)
	probe, probeErr := port.ProbeStaleAuthority(ctx)
	checks := []struct {
		id string
		fn func() error
	}{
		{InvariantOneAuthority, func() error { return assertAuthority(observation) }},
		{InvariantWorkspaceImmutable, func() error { return assertWorkspace(observation) }},
		{InvariantStaleRefused, func() error { return assertEpochAndStale(observation, probe, probeErr) }},
		{InvariantPrimaryIsolation, func() error { return assertPrimary(observation) }},
		{InvariantReviewIndependent, func() error { return assertReview(observation) }},
		{InvariantIntegrationSerial, func() error { return assertIntegration(observation) }},
		{InvariantEffectAttribution, func() error { return assertEffects(observation) }},
		{InvariantJournalComplete, func() error { return assertJournalAndPlayback(observation) }},
	}
	result := InvariantSummary{Assertions: make([]Assertion, 0, len(checks))}
	for _, check := range checks {
		err := observeErr
		if err == nil {
			err = check.fn()
		}
		assertion := Assertion{ID: check.id, Passed: err == nil}
		if err != nil {
			assertion.Diagnostic = boundedDiagnostic(err.Error())
		}
		result.Assertions = append(result.Assertions, assertion)
		setSummary(&result, check.id, assertion.Passed)
	}
	return result
}

func failedSummary(reason string) InvariantSummary {
	result := InvariantSummary{Assertions: make([]Assertion, 0, len(InvariantIDs))}
	for _, id := range InvariantIDs {
		result.Assertions = append(result.Assertions, Assertion{ID: id, Diagnostic: boundedDiagnostic(reason)})
	}
	return result
}

func setSummary(result *InvariantSummary, id string, value bool) {
	switch id {
	case InvariantOneAuthority:
		result.OneValidAuthorityPerWorkRevision = value
	case InvariantWorkspaceImmutable:
		result.WorkspaceBindingImmutable = value
	case InvariantStaleRefused:
		result.StaleAuthorityRefused = value
	case InvariantPrimaryIsolation:
		result.PrimaryIsolation = value
	case InvariantReviewIndependent:
		result.ReviewerActorIndependence = value
	case InvariantIntegrationSerial:
		result.SerializedTargetAuthority = value
	case InvariantEffectAttribution:
		result.EffectAttribution = value
	case InvariantJournalComplete:
		result.JournalComplete = value
	}
}

func assertAuthority(observation Observation) error {
	if !observation.Capabilities.Authority {
		return fmt.Errorf("authority facts are not inspectable")
	}
	seen := make(map[string]AuthorityObservation)
	for _, authority := range observation.Authorities {
		if authority.WorkRevisionID == "" || authority.WorkID == "" || authority.LeaseID == "" || authority.RuntimeEpochID == "" || authority.Generation == 0 || authority.FenceToken == 0 {
			return fmt.Errorf("authority observation is incomplete")
		}
		if !authority.Current {
			continue
		}
		key := authority.WorkRevisionID + "\x00" + authority.WorkID
		if prior, exists := seen[key]; exists {
			return fmt.Errorf("multiple current authorities work_revision=%s work=%s leases=%s,%s", authority.WorkRevisionID, authority.WorkID, prior.LeaseID, authority.LeaseID)
		}
		seen[key] = authority
	}
	return nil
}

func assertWorkspace(observation Observation) error {
	if !observation.Capabilities.Workspace {
		return fmt.Errorf("workspace facts are not inspectable")
	}
	seenID := make(map[string]WorkspaceObservation)
	seenLease := make(map[string]string)
	for _, workspace := range observation.Workspaces {
		if workspace.WorkspaceID == "" || workspace.WorkRevisionID == "" || workspace.WorkID == "" || workspace.LeaseID == "" || workspace.RuntimeEpochID == "" || workspace.Generation == 0 || workspace.CanonicalPath == "" || workspace.OwnedBranch == "" || workspace.Status == "" {
			return fmt.Errorf("workspace observation is incomplete")
		}
		if workspace.Transferred {
			return fmt.Errorf("workspace %s was transferred across lease authority", workspace.WorkspaceID)
		}
		if prior, exists := seenID[workspace.WorkspaceID]; exists && (prior.LeaseID != workspace.LeaseID || prior.RuntimeEpochID != workspace.RuntimeEpochID || prior.Generation != workspace.Generation || prior.CanonicalPath != workspace.CanonicalPath) {
			return fmt.Errorf("workspace %s has multiple authority bindings", workspace.WorkspaceID)
		}
		seenID[workspace.WorkspaceID] = workspace
		if workspace.Status == "active-bound" {
			key := workspace.LeaseID + "\x00" + fmt.Sprint(workspace.Generation) + "\x00" + workspace.RuntimeEpochID
			if prior, exists := seenLease[key]; exists && prior != workspace.WorkspaceID {
				return fmt.Errorf("lease authority is bound to workspaces %s and %s", prior, workspace.WorkspaceID)
			}
			seenLease[key] = workspace.WorkspaceID
		}
	}
	return nil
}

func assertEpochAndStale(observation Observation, probe StaleProbe, probeErr error) error {
	if !observation.Epoch.Inspectable || observation.Epoch.Current == "" || !observation.Epoch.EveryCurrentUses {
		return fmt.Errorf("current runtime epoch is not fully inspectable or consistent")
	}
	if observation.Epoch.Predecessor != "" && observation.Epoch.Predecessor == observation.Epoch.Current {
		return fmt.Errorf("runtime predecessor equals current epoch")
	}
	if probeErr != nil {
		return fmt.Errorf("stale probe unavailable: %w", probeErr)
	}
	if !probe.Attempted || !probe.Refused || (!probe.LeaseStale && !probe.EpochStale) {
		return fmt.Errorf("required stale lease/epoch capsule probe did not refuse")
	}
	if observation.Epoch.Predecessor != "" && !probe.EpochStale {
		return fmt.Errorf("predecessor runtime epoch was not exercised by an epoch-stale capsule probe")
	}
	if observation.Epoch.Predecessor == "" && !probe.LeaseStale {
		return fmt.Errorf("lease replacement was not exercised by a lease-stale capsule probe")
	}
	if probe.AuthorityChanges != 0 || probe.WorkspaceChanges != 0 {
		return fmt.Errorf("stale probe mutated authority=%d workspace=%d", probe.AuthorityChanges, probe.WorkspaceChanges)
	}
	return nil
}

func assertPrimary(observation Observation) error {
	if !observation.Capabilities.Workspace {
		return fmt.Errorf("workspace/primary facts are not inspectable")
	}
	before, after := observation.Primary.Before, observation.Primary.After
	if !observation.Primary.Inspectable || !hexSized(before.Head, 40) || !isSHA256(before.IndexDigest) || !isSHA256(before.TreeDigest) ||
		!hexSized(after.Head, 40) || !isSHA256(after.IndexDigest) || !isSHA256(after.TreeDigest) {
		return fmt.Errorf("primary HEAD/index/tree observations are incomplete")
	}
	if before != after {
		return fmt.Errorf("primary HEAD/index/tree changed during campaign attempt")
	}
	for _, workspace := range observation.Workspaces {
		if workspace.PrimaryPath == "" {
			return fmt.Errorf("workspace %s omits primary path observation", workspace.WorkspaceID)
		}
		if workspace.CanonicalPath == workspace.PrimaryPath {
			return fmt.Errorf("workspace %s aliases the primary checkout", workspace.WorkspaceID)
		}
		if workspace.Status == "active-bound" && !workspace.Registered {
			return fmt.Errorf("active workspace %s is not registered", workspace.WorkspaceID)
		}
	}
	return nil
}

func assertReview(observation Observation) error {
	if !observation.Capabilities.Review {
		return fmt.Errorf("review facts are not inspectable")
	}
	for _, review := range observation.Reviews {
		if review.CandidateID == "" || review.ReviewerActorID == "" || len(review.ContributorIDs) == 0 || !review.CandidateImmutable {
			return fmt.Errorf("review observation is incomplete or candidate mutable")
		}
		for _, actor := range review.ContributorIDs {
			if actor == "" || actor == review.ReviewerActorID {
				return fmt.Errorf("candidate %s reviewer is not actor-independent", review.CandidateID)
			}
		}
	}
	return nil
}

func assertIntegration(observation Observation) error {
	if !observation.Capabilities.Integration {
		return fmt.Errorf("integration facts are not inspectable")
	}
	publication := make(map[string]map[string]int)
	for _, integration := range observation.Integration {
		if integration.RepositoryID == "" || integration.TargetRef == "" || integration.CandidateID == "" {
			return fmt.Errorf("integration observation is incomplete")
		}
		if integration.ActiveAuthorities < 0 || integration.ActiveAuthorities > 1 || integration.SuccessfulPublishes < 0 || integration.SuccessfulPublishes > 1 {
			return fmt.Errorf("integration authority/publication is not serialized")
		}
		if integration.SuccessfulPublishes == 1 && (integration.GitExpectedOld == "" || integration.GitObservedOld != integration.GitExpectedOld || integration.GitResult == "" || integration.GitResult != integration.JournalResult) {
			return fmt.Errorf("integration Git facts disagree with journal result")
		}
		key := integration.RepositoryID + "\x00" + integration.TargetRef
		if publication[key] == nil {
			publication[key] = make(map[string]int)
		}
		publication[key][integration.CandidateID] += integration.SuccessfulPublishes
		if publication[key][integration.CandidateID] > 1 {
			return fmt.Errorf("candidate %s integrated more than once", integration.CandidateID)
		}
	}
	return nil
}

func assertEffects(observation Observation) error {
	if !observation.Capabilities.Effects {
		return fmt.Errorf("effect facts are not inspectable")
	}
	for _, effect := range observation.Effects {
		if effect.EffectID == "" || effect.OperationID == "" || effect.ExpectedRevision == "" || effect.ExpectedMarker == "" {
			return fmt.Errorf("effect observation is incomplete")
		}
		if effect.Dispatches < 0 || effect.Dispatches > 1 {
			return fmt.Errorf("effect %s dispatched %d times", effect.EffectID, effect.Dispatches)
		}
		if effect.CompensationApplied {
			if !effect.CompensationAttempted || !effect.Attributable || effect.CurrentRevision == effect.AppliedRevision || effect.CurrentValue != effect.ExpectedValue || effect.CurrentMarker != effect.ExpectedMarker {
				return fmt.Errorf("effect %s compensation is not compare-before-compensate attributable", effect.EffectID)
			}
		}
		if effect.CompensationAttempted && !effect.CompensationApplied && !effect.HumanValuePreserved {
			return fmt.Errorf("effect %s failed to preserve a human reassignment", effect.EffectID)
		}
		if effect.AppliedRevision != "" && effect.AppliedRevision == effect.ExpectedRevision {
			return fmt.Errorf("effect %s fabricated application from stale revision", effect.EffectID)
		}
	}
	return nil
}

func assertJournalAndPlayback(observation Observation) error {
	if !observation.Journal.Inspectable {
		return fmt.Errorf("journal is not inspectable")
	}
	heads := make(map[string]JournalHead, len(observation.Journal.Heads))
	for _, head := range observation.Journal.Heads {
		if head.AggregateKey == "" || head.LogicalVersion == 0 || head.RecordRevision == 0 || head.LastEventID == "" || !isSHA256(head.PayloadDigest) {
			return fmt.Errorf("aggregate head is incomplete")
		}
		if _, exists := heads[head.AggregateKey]; exists {
			return fmt.Errorf("duplicate aggregate head %s", head.AggregateKey)
		}
		heads[head.AggregateKey] = head
	}
	seenOperations := make(map[string]struct{})
	seenEvents := make(map[string]struct{})
	latest := make(map[string]JournalEvent)
	var priorOperationRevision uint64
	for _, operation := range observation.Journal.Operations {
		if operation.OperationID == "" || operation.RequestDigest == "" || operation.ResultDigest == "" || operation.RecordRevision == 0 || !operation.Terminal || operation.EventCount <= 0 || operation.EventCount != len(operation.Events) {
			return fmt.Errorf("operation record is incomplete or nonterminal")
		}
		if _, exists := seenOperations[operation.OperationID]; exists {
			return fmt.Errorf("duplicate operation record %s", operation.OperationID)
		}
		seenOperations[operation.OperationID] = struct{}{}
		if operation.RecordRevision <= priorOperationRevision {
			return fmt.Errorf("operation %s record revision is duplicate or nonmonotonic", operation.OperationID)
		}
		priorOperationRevision = operation.RecordRevision
		resultDigest := sha256.Sum256(operation.CanonicalResult)
		if hex.EncodeToString(resultDigest[:]) != operation.ResultDigest {
			return fmt.Errorf("operation %s canonical result digest mismatch", operation.OperationID)
		}
		seenAggregate := make(map[string]struct{})
		observedKeys := append([]string(nil), operation.ObservedHeadKeys...)
		sort.Strings(observedKeys)
		for index, event := range operation.Events {
			if event.EventID == "" || event.Index != index || event.Count != operation.EventCount || event.RecordRevision != operation.RecordRevision || event.AggregateKey == "" || event.LogicalVersion == 0 || !isSHA256(event.PayloadDigest) {
				return fmt.Errorf("operation %s event group is non-atomic or unordered", operation.OperationID)
			}
			if _, exists := seenAggregate[event.AggregateKey]; exists {
				return fmt.Errorf("operation %s repeats aggregate %s", operation.OperationID, event.AggregateKey)
			}
			if _, exists := seenEvents[event.EventID]; exists {
				return fmt.Errorf("duplicate event ID %s across journal operations", event.EventID)
			}
			seenEvents[event.EventID] = struct{}{}
			seenAggregate[event.AggregateKey] = struct{}{}
			if prior, exists := latest[event.AggregateKey]; exists {
				if event.LogicalVersion != prior.LogicalVersion+1 {
					return fmt.Errorf("aggregate %s logical version is duplicate or nonmonotonic", event.AggregateKey)
				}
				if event.RecordRevision <= prior.RecordRevision {
					return fmt.Errorf("aggregate %s record revision is duplicate or nonmonotonic", event.AggregateKey)
				}
			} else if event.LogicalVersion != 1 {
				return fmt.Errorf("aggregate %s first observed logical version is not one", event.AggregateKey)
			}
			latest[event.AggregateKey] = event
		}
		eventKeys := make([]string, 0, len(seenAggregate))
		for key := range seenAggregate {
			eventKeys = append(eventKeys, key)
		}
		sort.Strings(eventKeys)
		if len(observedKeys) != len(eventKeys) {
			return fmt.Errorf("operation %s observed-head membership differs from event membership", operation.OperationID)
		}
		for index := range eventKeys {
			if observedKeys[index] != eventKeys[index] {
				return fmt.Errorf("operation %s observed-head membership differs from event membership", operation.OperationID)
			}
		}
	}
	if len(latest) != len(heads) {
		return fmt.Errorf("aggregate head census differs from terminal event census")
	}
	for key, event := range latest {
		head, ok := heads[key]
		if !ok || head.LogicalVersion != event.LogicalVersion || head.RecordRevision != event.RecordRevision || head.LastEventID != event.EventID || head.PayloadDigest != event.PayloadDigest {
			return fmt.Errorf("aggregate %s head does not bind its exact terminal event", key)
		}
	}
	if !observation.Playback.Inspectable || !observation.Playback.StableCanonical || observation.Playback.RestoreDigest == "" || observation.Playback.RestoreDigest != observation.Playback.PlaybackDigest || observation.Playback.AdapterCalls != 0 || observation.Playback.DurableWrites != 0 {
		return fmt.Errorf("restore/playback digest or zero-I/O semantics failed")
	}
	return nil
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func boundedDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}

// SortedAssertionIDs provides a stable anti-vacuity census to evidence code.
func SortedAssertionIDs(summary InvariantSummary) []string {
	ids := make([]string, 0, len(summary.Assertions))
	for _, assertion := range summary.Assertions {
		ids = append(ids, assertion.ID)
	}
	sort.Strings(ids)
	return ids
}
