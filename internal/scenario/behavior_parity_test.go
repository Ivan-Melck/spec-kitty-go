package scenario

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const frozenBehaviorIDs = "M-01 M-02 M-04 W-01 W-02 W-03 W-04 W-05 W-06 W-07 C-01 C-02 C-03 C-04 C-05 C-06 C-08 C-09 C-10 C-11 C-12 S-01 S-02 S-03 S-04 S-05 S-07 S-08 R-01 R-02 R-03 R-04 R-05 R-06 R-07 A-01 A-02 A-03 A-05 I-01 I-02 I-03 I-04 I-05 I-06 T-02 T-04 T-05 T-06 T-07 X-01 X-03 X-04 X-06 X-07 D-01 D-02 D-03 D-05"

// behaviorMapping is deliberately an explicit row, rather than a prefix-based
// template. Proof names are executed below. The prose fields state the exact
// observable boundary proved by that executable scenario; they are evidence
// labels, never an alternative source of authority.
type behaviorMapping struct {
	ID, Treatment, Capability         string
	Setup, CommandOrQuery, ResultKind string
	AuthorityMutation, EvidenceKind   string
	RefusalPosition                   string
	Proof                             executableProof
}

func proof(pkg, test string) executableProof {
	return executableProof{Package: pkg, Test: test, TrialsPerRun: 1}
}

func proofTrials(pkg, test string, trialsPerRun int) executableProof {
	return executableProof{Package: pkg, Test: test, TrialsPerRun: trialsPerRun}
}

var frozenBehaviorProofs = []behaviorMapping{
	{ID: "M-01", Capability: "mission", Setup: "active mission and tracker observation", CommandOrQuery: "mission authority transition", ResultKind: "typed lifecycle result", AuthorityMutation: "one mission publication", EvidenceKind: "mission head and tracker non-authority", RefusalPosition: "tracker result cannot grant authority", Proof: proof("./internal/mission", "TestDecompositionTrackerResultCannotGrantExecutionAuthorityV2")},
	{ID: "M-02", Capability: "mission_type", Setup: "canonical and unsupported definitions", CommandOrQuery: "definition registry resolution", ResultKind: "exact definition or typed refusal", AuthorityMutation: "zero", EvidenceKind: "canonical registry bytes", RefusalPosition: "before mission creation", Proof: proof("./internal/mission", "TestDefinitionRegistryIsCanonicalExactAndImmutable")},
	{ID: "M-04", Capability: "mission", Setup: "complete mission facts with no phase cursor", CommandOrQuery: "workgraph finalization", ResultKind: "generation-one authority", AuthorityMutation: "one atomic mission/revision publication", EvidenceKind: "pinned revision and complete receipt", RefusalPosition: "before execution eligibility", Proof: proof("./internal/mission", "TestFinalizationIsCapabilityReadyWithoutWorkDraftPhaseAndDoesNotCompleteMission")},
	{ID: "W-01", Capability: "workgraph", Setup: "work lane run and workspace identities", CommandOrQuery: "revision construction", ResultKind: "separate typed identities", AuthorityMutation: "zero", EvidenceKind: "canonical revision", RefusalPosition: "before identity aliasing", Proof: proof("./internal/workgraph", "TestRevisionV2RoundTripsStrictlyAndDefendsEveryCollection")},
	{ID: "W-02", Capability: "workgraph", Setup: "valid and invalid ownership/dependency graphs", CommandOrQuery: "generation-one finalization", ResultKind: "revision or typed graph finding", AuthorityMutation: "zero before publish", EvidenceKind: "complete provider and scope evidence", RefusalPosition: "before revision publication", Proof: proof("./internal/workgraph", "TestGenerationOneCandidateValidatesAndPinsCompleteEvidence")},
	{ID: "W-03", Capability: "workgraph", Setup: "overlapping and independent write scopes", CommandOrQuery: "lane collapse", ResultKind: "deterministic lane topology", AuthorityMutation: "zero", EvidenceKind: "lane membership and dependency edges", RefusalPosition: "before invalid collapse", Proof: proof("./internal/workgraph", "TestCollapseLanesIsTransitiveDeterministicAndKeepsIndependentWork")},
	{ID: "W-04", Capability: "workgraph", Setup: "running work and changed graph", CommandOrQuery: "mixed reconciliation", ResultKind: "explicit dispositions", AuthorityMutation: "one successor revision", EvidenceKind: "generation lineage and affected work", RefusalPosition: "before mutable graph substitution", Proof: proof("./internal/workgraph", "TestWorkgraphV2FinalizationAndMixedReconciliationAreAtomicAuthority")},
	{ID: "W-05", Capability: "workgraph", Setup: "planning source and provider evidence", CommandOrQuery: "revision-v3 construction", ResultKind: "content-addressed revision", AuthorityMutation: "zero", EvidenceKind: "pinned planning source commit", RefusalPosition: "before incomplete provenance", Proof: proof("./internal/workgraph", "TestRevisionV3ContentIdentityCommitsFinalizationProvenance")},
	{ID: "W-06", Capability: "workgraph", Setup: "approved done and ineligible dependencies", CommandOrQuery: "eligibility derivation", ResultKind: "complete eligibility fact", AuthorityMutation: "zero", EvidenceKind: "dependency satisfaction level", RefusalPosition: "before execution admission", Proof: proof("./internal/workgraph", "TestCompleteEligibilityFactDerivesAndValidatesEveryAuthorityField")},
	{ID: "W-07", Capability: "workgraph", Setup: "existing generation and changed observation", CommandOrQuery: "successor publication", ResultKind: "new generation or conflict", AuthorityMutation: "one pointer/revision pair", EvidenceKind: "immutable predecessor and disposition", RefusalPosition: "before unlabeled second authority", Proof: proof("./internal/mission", "TestSuccessorPublicationAtomicallyAdvancesPointerAndReplays")},
	{ID: "C-01", Capability: "workspace", Setup: "independent work and distinct path claims", CommandOrQuery: "concurrent workspace publication", ResultKind: "both independent claims admitted", AuthorityMutation: "one binding per workspace", EvidenceKind: "distinct permanent path claims", RefusalPosition: "before shared checkout", Proof: proof("./internal/controlstore/gapdb", "TestWorkspacePathClaimAndIndependentWorkspaceCampaignV2")},
	{ID: "C-02", Capability: "workspace", Setup: "lane identity with ordered work", CommandOrQuery: "binding construction", ResultKind: "durable lane/workspace separation", AuthorityMutation: "one binding", EvidenceKind: "lane ID and disposable workspace ID", RefusalPosition: "before lineage alias", Proof: proof("./internal/workspace", "TestWorkspaceBindingPathAndClaimAreImmutableV2")},
	{ID: "C-03", Capability: "workspace", Setup: "two contenders for one path", CommandOrQuery: "workspace bind", ResultKind: "one winner and typed loser", AuthorityMutation: "one path claim", EvidenceKind: "permanent claim CAS", RefusalPosition: "before second mutable owner", Proof: proof("./internal/controlstore/gapdb", "TestWorkspacePathClaimContentionAndIndependentProgressV2")},
	{ID: "C-04", Capability: "workspace", Setup: "expired lease and replacement generation", CommandOrQuery: "quarantine then bind replacement", ResultKind: "fresh workspace authority", AuthorityMutation: "expiry plus new binding", EvidenceKind: "distinct workspace and generation", RefusalPosition: "before workspace transfer", Proof: proof("./internal/controlstore/gapdb", "TestWorkspaceProductionZombieReplacementCampaignV2")},
	{ID: "C-05", Capability: "workspace", Setup: "pinned dependency heads", CommandOrQuery: "staged preparation", ResultKind: "ready with exact dependency observation", AuthorityMutation: "one stage per durable boundary", EvidenceKind: "dependency head observations", RefusalPosition: "before stale dependency use", Proof: proof("./internal/workspace", "TestPreparationIntentPrecedesActionAndRestartReconcilesV2")},
	{ID: "C-06", Capability: "workspace", Setup: "multi-stage dependency preparation and faults", CommandOrQuery: "restart from each durable stage", ResultKind: "canonical ready or intact prior", AuthorityMutation: "all-or-nothing ready publication", EvidenceKind: "stage-by-stage recovery", RefusalPosition: "before partial readiness", Proof: proof("./internal/workspace", "TestPreparationRestartFromEveryDurableStageIsCanonicalV2")},
	{ID: "C-08", Capability: "workspace", Setup: "directory exists without ready authority", CommandOrQuery: "ensure ready", ResultKind: "continued staged recovery", AuthorityMutation: "missing stages only", EvidenceKind: "workspace_ready authority", RefusalPosition: "directory existence never admits", Proof: proof("./internal/workspace", "TestEnsureReadyRecoversCrashAfterWorktreeEffectWithoutExistenceAsReadiness")},
	{ID: "C-09", Capability: "workspace", Setup: "primary checkout and managed root", CommandOrQuery: "canonical path validation", ResultKind: "scoped managed workspace", AuthorityMutation: "zero on primary/outside path", EvidenceKind: "canonical managed-root identity", RefusalPosition: "before filesystem mutation", Proof: proof("./internal/workspace", "TestWorkspaceCanonicalPathRefusesAliasesAndOutsideRootV2")},
	{ID: "C-10", Capability: "workspace", Setup: "dirty or moved branch observations", CommandOrQuery: "ready replay probe", ResultKind: "quarantine or decision-required refusal", AuthorityMutation: "no destructive cleanup", EvidenceKind: "typed drift observation", RefusalPosition: "before reuse/delete", Proof: proof("./internal/workspace", "TestReadyReplayReprobesAndQuarantinesDrift")},
	{ID: "C-11", Capability: "workspace", Setup: "writable expired workspace and replacement", CommandOrQuery: "zombie authority probes", ResultKind: "all stale operations refused", AuthorityMutation: "replacement only", EvidenceKind: "writable W1 plus authoritative W2", RefusalPosition: "epoch/lease fence before domain effects", Proof: proof("./internal/workspace", "TestWritableZombieWorkspaceNeverRegainsAuthorityCampaignV2")},
	{ID: "C-12", Capability: "workspace", Setup: "aliases outside roots and explicit claim", CommandOrQuery: "binding validation", ResultKind: "canonical scoped handle", AuthorityMutation: "zero for ambient/aliased path", EvidenceKind: "path claim digest", RefusalPosition: "before repository observation", Proof: proof("./internal/workspace", "TestCurrentWorkspacePredicateUsesWholeCapsuleV2")},
	{ID: "S-01", Capability: "execution", Setup: "finalized revision eligibility", CommandOrQuery: "execution admit", ResultKind: "planned-to-running slot", AuthorityMutation: "one slot publication", EvidenceKind: "complete eligibility assertion", RefusalPosition: "before missing genesis", Proof: proof("./internal/execution", "TestExecutionAuthorityAdmitsWithExactEligibilityAndOneBatchV2")},
	{ID: "S-02", Capability: "execution", Setup: "same-operation retry and competing actor", CommandOrQuery: "execution admission", ResultKind: "replay or typed conflict", AuthorityMutation: "exactly one slot advancement", EvidenceKind: "operation binding and fence", RefusalPosition: "collision before authority reads", Proof: proof("./internal/execution", "TestExecutionAuthorityReplayAndCollisionPrecedeEpochAndCapabilityReadsV2")},
	{ID: "S-03", Capability: "runtime", Setup: "committed journal and discarded projection", CommandOrQuery: "host restart", ResultKind: "byte-identical recovered projection", AuthorityMutation: "fresh epoch only", EvidenceKind: "checkpoint plus journal suffix", RefusalPosition: "before serving unsealed state", Proof: proof("./internal/productionruntime", "TestProductionReducerCompositionIsCompleteAndRuntimePlaybackPure")},
	{ID: "S-04", Capability: "execution", Setup: "transition and related eligibility/effect evidence", CommandOrQuery: "sealed execution publication", ResultKind: "one atomic result", AuthorityMutation: "complete multi-head batch", EvidenceKind: "one publication membership", RefusalPosition: "before partial batch", Proof: proof("./internal/execution", "TestExecutionAuthorityAdmitsWithExactEligibilityAndOneBatchV2")},
	{ID: "S-05", Capability: "execution", Setup: "same slot and unrelated slot contenders", CommandOrQuery: "concurrent authority publications", ResultKind: "one winner and independent progress", AuthorityMutation: "exactly once per aggregate", EvidenceKind: "typed contention outcomes", RefusalPosition: "stale loser before mutation", Proof: proof("./internal/controlstore/gapdb", "TestExecutionAuthorityProductionContentionMatrixV2")},
	{ID: "S-07", Capability: "execution", Setup: "approved review and pending integration", CommandOrQuery: "accept then integrate", ResultKind: "approved remains distinct from completed", AuthorityMutation: "acceptance then integration/outcome", EvidenceKind: "separate family heads", RefusalPosition: "done before integration refused", Proof: proof("./internal/integration", "TestAcceptanceIsExactEvidenceReadinessNotPhaseCeremonyV2")},
	{ID: "S-08", Capability: "execution", Setup: "expired authority and replacement", CommandOrQuery: "heartbeat expiry replacement", ResultKind: "typed attributed recovery", AuthorityMutation: "new generation only", EvidenceKind: "operation and actor-bound transition", RefusalPosition: "stale recovery before mutation", Proof: proof("./internal/execution", "TestExecutionAuthorityRestartCutsHeartbeatExpiryAndReplacementV2")},
	{ID: "R-01", Capability: "handoff", Setup: "ready workspace and immutable candidate", CommandOrQuery: "submit handoff", ResultKind: "candidate-recorded state", AuthorityMutation: "one handoff slot", EvidenceKind: "candidate base criteria digest", RefusalPosition: "before live-directory review", Proof: proof("./internal/handoff", "TestHandoffCandidateRefusalIsDurableBoundedAndReplaysBeforeGitV2")},
	{ID: "R-02", Capability: "review", Setup: "submitted handoff and criterion evidence", CommandOrQuery: "submit review", ResultKind: "first-class verdict", AuthorityMutation: "criterion stages then terminal verdict", EvidenceKind: "review ID actor process candidate", RefusalPosition: "before incomplete verdict", Proof: proof("./internal/review", "TestReviewAuthorityStagesEveryCriterionThenOneTerminalVerdictAndReplaysV2")},
	{ID: "R-03", Capability: "review", Setup: "rejected candidate and rework attempt", CommandOrQuery: "review lifecycle replay", ResultKind: "preserved prior verdict and new attempt", AuthorityMutation: "append-only review evidence", EvidenceKind: "distinct review operation IDs", RefusalPosition: "before verdict overwrite", Proof: proof("./internal/review", "TestReviewDroppedAcknowledgementAtEveryStageResumesSameOperationV2")},
	{ID: "R-04", Capability: "review", Setup: "same actor different process and distinct actor", CommandOrQuery: "submit review", ResultKind: "self-review refused independent review admitted", AuthorityMutation: "zero or one review", EvidenceKind: "candidate contributor digest", RefusalPosition: "actor check before publication", Proof: proof("./internal/review", "TestReviewerIndependenceUsesActorIDNotProcessAndNeedsExactNarrowExceptionV2")},
	{ID: "R-05", Capability: "review", Setup: "concurrent review writers", CommandOrQuery: "review publication", ResultKind: "one complete winner", AuthorityMutation: "one atomic review batch", EvidenceKind: "durable criterion and verdict heads", RefusalPosition: "loser before partial evidence", Proof: proof("./internal/controlstore/gapdb", "TestReviewGapDBPublisherCommitsOneCompleteBatchAndContendsV2")},
	{ID: "R-06", Capability: "review", Setup: "candidate criteria and work revision substitutions", CommandOrQuery: "review submit replay", ResultKind: "typed stale refusal", AuthorityMutation: "zero on substitution", EvidenceKind: "exact pinned assertions", RefusalPosition: "before verdict publication", Proof: proof("./internal/review", "TestReviewRejectsSubstitutedWorkRevisionAssertionV2")},
	{ID: "R-07", Capability: "review", Setup: "same actor alternate process and no policy fact", CommandOrQuery: "review eligibility", ResultKind: "closed self-review refusal", AuthorityMutation: "zero", EvidenceKind: "ActorID independence", RefusalPosition: "before waiver inference", Proof: proof("./internal/review", "TestReviewerIndependenceUsesActorIDNotProcessAndNeedsExactNarrowExceptionV2")},
	{ID: "A-01", Capability: "acceptance", Setup: "complete graph reviews criteria and candidates", CommandOrQuery: "accept", ResultKind: "granted or typed evidence refusal", AuthorityMutation: "one acceptance state", EvidenceKind: "mission-wide evidence set", RefusalPosition: "before ticket closure semantics", Proof: proof("./internal/integration", "TestAcceptanceCannotOmitArtifactEvidenceV2")},
	{ID: "A-02", Capability: "acceptance", Setup: "approved and integrated work eligibility", CommandOrQuery: "accept", ResultKind: "both terminal eligibility states accepted", AuthorityMutation: "one acceptance", EvidenceKind: "complete mixed eligibility", RefusalPosition: "before legacy downgrade", Proof: proof("./internal/integration", "TestCompleteIntegrationEligibilityConfigurationCannotBeDowngraded")},
	{ID: "A-03", Capability: "acceptance", Setup: "missing stale required evidence", CommandOrQuery: "diagnose then accept", ResultKind: "typed cannot-evaluate refusal", AuthorityMutation: "zero", EvidenceKind: "bounded diagnostic", RefusalPosition: "before publication", Proof: proof("./internal/integration", "TestAcceptanceRefusesStaleEvidenceAndSameActorBeforePublicationV2")},
	{ID: "A-05", Capability: "acceptance", Setup: "structured criterion evidence and applicability", CommandOrQuery: "acceptance evaluation", ResultKind: "derived matrix semantics", AuthorityMutation: "one sealed evidence result", EvidenceKind: "criterion evidence coordinates", RefusalPosition: "before hand-maintained projection", Proof: proof("./internal/integration", "TestAcceptancePolicyWaiverAndApplicabilityNeedCurrentDurableFactsV2")},
	{ID: "I-01", Capability: "integration", Setup: "accepted candidates targeting one ref", CommandOrQuery: "concurrent integrate", ResultKind: "serialized target authority", AuthorityMutation: "one target owner at a time", EvidenceKind: "repository-target fence", RefusalPosition: "loser before Git", Proof: proof("./internal/integration", "TestIntegrationTargetAuthorityAdmitsOneOfOneHundredContendersV2")},
	{ID: "I-02", Capability: "integration", Setup: "local repository capability", CommandOrQuery: "integration publish", ResultKind: "local completed attempt", AuthorityMutation: "Git ref plus authority", EvidenceKind: "attributable local observation", RefusalPosition: "no network capability exists", Proof: proof("./internal/integration", "TestIntegrationAuthorityStagesIntentBeforeEffectsAndReplaysV2")},
	{ID: "I-03", Capability: "integration", Setup: "lost responses at each stage", CommandOrQuery: "same-operation resume", ResultKind: "one completed attempt", AuthorityMutation: "missing stages only", EvidenceKind: "durable outcome observations", RefusalPosition: "before blind replay", Proof: proof("./internal/integration", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2")},
	{ID: "I-04", Capability: "integration", Setup: "merge conflict or manual movement", CommandOrQuery: "integrate", ResultKind: "repair-required state", AuthorityMutation: "preserved candidate and conflict evidence", EvidenceKind: "typed repository observation", RefusalPosition: "before destructive reset", Proof: proof("./internal/integration", "TestIntegrationConflictAndManualMovementRequireRepairV2")},
	{ID: "I-05", Capability: "integration", Setup: "ref moved and bookkeeping response lost", CommandOrQuery: "integration reconcile", ResultKind: "completed exact admission", AuthorityMutation: "one outcome", EvidenceKind: "attributable ref publication", RefusalPosition: "before duplicate Git mutation", Proof: proof("./internal/integration", "TestIntegrationLostRefUpdateResponseIsOneAdmissionV2")},
	{ID: "I-06", Capability: "integration", Setup: "same and independent target refs", CommandOrQuery: "concurrent integration", ResultKind: "same target serialized distinct targets progress", AuthorityMutation: "one fence per repository/ref", EvidenceKind: "target-scoped contention", RefusalPosition: "before same-target effect", Proof: proof("./internal/integration", "TestIndependentTargetsProgressConcurrentlyWithoutGlobalLockV2")},
	{ID: "T-02", Capability: "tracker_effect", Setup: "stable WorkID and external mapping", CommandOrQuery: "assignment effect", ResultKind: "attributed external observation", AuthorityMutation: "effect state only", EvidenceKind: "internal external identity pair", RefusalPosition: "provider identity cannot rewrite WorkID", Proof: proof("./internal/execution", "TestExternalProviderBuildsAcceptedObservationFromRealFakeTrackerV2")},
	{ID: "T-04", Capability: "tracker_effect", Setup: "unavailable stale malformed provider", CommandOrQuery: "dispatch reconcile", ResultKind: "typed bounded diagnostic", AuthorityMutation: "intent retained product authority unchanged", EvidenceKind: "provider observation classification", RefusalPosition: "before erasing consent authority", Proof: proof("./internal/execution", "TestExternalEffectAuthorityExternalFailuresUseBoundedRequestDiagnosticsV2")},
	{ID: "T-05", Capability: "tracker_effect", Setup: "missing dispatch authorization", CommandOrQuery: "effect dispatch", ResultKind: "fail-closed refusal", AuthorityMutation: "zero external mutation", EvidenceKind: "durable one-shot permission", RefusalPosition: "before provider call", Proof: proof("./internal/controlstore/gapdb", "TestEffectDispatchAuthorizationIsDurableOneShotThroughGapDBV2")},
	{ID: "T-06", Capability: "tracker_effect", Setup: "real fake tracker assignment", CommandOrQuery: "effect saga", ResultKind: "coordination observation only", AuthorityMutation: "effect outbox heads no execution grant", EvidenceKind: "tracker result plus authority separation", RefusalPosition: "before tracker grants execution", Proof: proof("./internal/execution", "TestExternalProviderRunsEndToEndThroughPublicServicesAndRealFakeTrackerV2")},
	{ID: "T-07", Capability: "tracker_effect", Setup: "human-changed provider revision value marker", CommandOrQuery: "conditional compensation", ResultKind: "safe compensation or refusal evidence", AuthorityMutation: "only still-attributable external state", EvidenceKind: "revision value operation marker compare", RefusalPosition: "before unsafe compensation", Proof: proof("./internal/execution", "TestAssignmentEffectDispatcherPreservesBeadsSafeCompensationRefusalEvidenceV2")},
	{ID: "X-01", Capability: "outbox", Setup: "hosted delivery unavailable", CommandOrQuery: "local authority plus enqueue", ResultKind: "local success and pending outbox", AuthorityMutation: "atomic product outbox intent", EvidenceKind: "durable local publication", RefusalPosition: "delivery is not local authority", Proof: proof("./internal/controlstore", "TestOutboxLifecycleRequiresIntentAndCompareBeforeCompensate")},
	{ID: "X-03", Capability: "external_surface", Setup: "remote tracker observation", CommandOrQuery: "decomposition effect projection", ResultKind: "request or observation only", AuthorityMutation: "no execution Git grant", EvidenceKind: "non-authoritative external result", RefusalPosition: "before authority promotion", Proof: proof("./internal/controlstore", "TestOutboxDeliveryEvidenceCannotBecomeProductAuthorityMutant")},
	{ID: "X-04", Capability: "external_surface", Setup: "typed host command and query", CommandOrQuery: "production host capability", ResultKind: "versioned typed result", AuthorityMutation: "declared capability only", EvidenceKind: "operation ID and stable error kind", RefusalPosition: "schema admission before command", Proof: proof("./internal/productionruntime", "TestProductionHostSurfaceHasNoRawAuthorityOrBackendSelector")},
	{ID: "X-06", Capability: "external_surface", Setup: "host-owned control root and scoped workspace", CommandOrQuery: "production host command", ResultKind: "opaque IDs and typed result", AuthorityMutation: "host-owned publication", EvidenceKind: "no raw storage surface", RefusalPosition: "before direct storage Git access", Proof: proof("./internal/productionruntime", "TestProductionHostSurfaceHasNoRawAuthorityOrBackendSelector")},
	{ID: "X-07", Capability: "external_surface", Setup: "missing mission and stale epoch", CommandOrQuery: "progress command", ResultKind: "typed not-found stale refusal", AuthorityMutation: "zero", EvidenceKind: "stable diagnostic kind", RefusalPosition: "host barrier before capability", Proof: proof("./internal/productionruntime", "TestHostBarrierRejectsEveryNonReadyStateAndStaleEpochBeforeCommand")},
	{ID: "D-01", Capability: "diagnosis", Setup: "current host projection", CommandOrQuery: "progress status query", ResultKind: "copy-only typed snapshot", AuthorityMutation: "zero", EvidenceKind: "same-revision snapshot digest", RefusalPosition: "query never acquires mutation authority", Proof: proof("./internal/productionruntime", "TestProgressTypedFamilyGetAndListAreCopyOnly")},
	{ID: "D-02", Capability: "recovery", Setup: "committed response lost and projection discarded", CommandOrQuery: "same-operation reconcile restart", ResultKind: "original committed result", AuthorityMutation: "zero duplicate publications", EvidenceKind: "journal membership and restored projection", RefusalPosition: "before cached progress trust", Proof: proof("./internal/controljournal/application", "TestServiceAmbiguityVisibleThenSameIDReadReconciles")},
	{ID: "D-03", Capability: "recovery", Setup: "ambiguous incomplete membership", CommandOrQuery: "reconcile", ResultKind: "typed indeterminate ambiguous", AuthorityMutation: "zero", EvidenceKind: "complete membership proof required", RefusalPosition: "before selecting any active operation", Proof: proof("./internal/controlstore/gapdb", "TestV2AmbiguityPartialMixedAndContradictoryMembershipCloseAuthority")},
	{ID: "D-05", Capability: "migration", Setup: "historical mixed unsafe root", CommandOrQuery: "startup classification", ResultKind: "offline-disposition-required", AuthorityMutation: "zero filesystem provider writes", EvidenceKind: "canonical classification evidence", RefusalPosition: "before sidecar acquisition", Proof: proof("./internal/productionruntime", "TestStartupRefusesMismatchedAndHistoricalRootsBeforeSidecarAcquisition")},
}

func TestBehaviorParityCensus(t *testing.T) {
	inventory := loadFrozenInventory(t)
	want := strings.Fields(frozenBehaviorIDs)
	got := make([]string, 0, len(inventory))
	for id := range inventory {
		got = append(got, id)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("frozen behavior census changed\nwant=%v\ngot=%v", want, got)
	}
	mappings := cloneBehaviorMappings(frozenBehaviorProofs)
	for index := range mappings {
		mappings[index].Treatment = inventory[mappings[index].ID]
	}
	if err := validateBehaviorMappings(mappings, want); err != nil {
		t.Fatal(err)
	}
	for _, mapping := range mappings {
		mapping := mapping
		t.Run(mapping.ID, func(t *testing.T) {
			if !strings.Contains(mapping.Treatment, "Exact") && !strings.Contains(mapping.Treatment, "Strengthen") {
				t.Fatalf("unqualified treatment %q", mapping.Treatment)
			}
			runExecutableProof(t, repositoryRoot(t), mapping.Proof, 1)
		})
	}
}

func TestBehaviorParityCensusMutants(t *testing.T) {
	want := strings.Fields(frozenBehaviorIDs)
	valid := cloneBehaviorMappings(frozenBehaviorProofs)
	if err := validateBehaviorMappings(valid, want); err != nil {
		t.Fatalf("valid control: %v", err)
	}
	mutants := map[string]func([]behaviorMapping) []behaviorMapping{
		"missing":                   func(v []behaviorMapping) []behaviorMapping { return v[1:] },
		"duplicate":                 func(v []behaviorMapping) []behaviorMapping { return append(v, v[0]) },
		"substitute_id":             func(v []behaviorMapping) []behaviorMapping { v[0].ID = "M-99"; return v },
		"substitute_proof":          func(v []behaviorMapping) []behaviorMapping { v[0].Proof = v[3].Proof; return v },
		"missing_mutation_evidence": func(v []behaviorMapping) []behaviorMapping { v[0].AuthorityMutation = ""; return v },
		"missing_refusal_edge":      func(v []behaviorMapping) []behaviorMapping { v[0].RefusalPosition = ""; return v },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			if err := validateBehaviorMappings(mutate(cloneBehaviorMappings(valid)), want); err == nil {
				t.Fatal("behavior census mutant survived")
			}
		})
	}
}

func cloneBehaviorMappings(input []behaviorMapping) []behaviorMapping {
	return append([]behaviorMapping(nil), input...)
}

func validateBehaviorMappings(mappings []behaviorMapping, expected []string) error {
	want := make(map[string]struct{}, len(expected))
	for _, id := range expected {
		want[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(mappings))
	proofs := make(map[string]string, len(mappings))
	for _, m := range mappings {
		if _, ok := want[m.ID]; !ok {
			return fmt.Errorf("unexpected behavior %s", m.ID)
		}
		if _, duplicate := seen[m.ID]; duplicate {
			return fmt.Errorf("duplicate behavior %s", m.ID)
		}
		if m.Capability == "" || m.Setup == "" || m.CommandOrQuery == "" || m.ResultKind == "" || m.AuthorityMutation == "" || m.EvidenceKind == "" || m.RefusalPosition == "" || m.Proof.Package == "" || m.Proof.Test == "" {
			return fmt.Errorf("incomplete behavior %s", m.ID)
		}
		key := m.Proof.Package + "\x00" + m.Proof.Test
		if prior, duplicate := proofs[key]; duplicate && strings.SplitN(prior, "-", 2)[0] != strings.SplitN(m.ID, "-", 2)[0] {
			return fmt.Errorf("cross-family proof substitution %s -> %s", m.ID, prior)
		}
		proofs[key] = m.ID
		seen[m.ID] = struct{}{}
	}
	if len(seen) != len(want) {
		return fmt.Errorf("behavior coverage want=%d got=%d", len(want), len(seen))
	}
	return nil
}

func loadFrozenInventory(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "docs", "PYTHON_BEHAVIOR_INVENTORY.md")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "|")
		if len(fields) < 5 {
			continue
		}
		id, treatment := strings.TrimSpace(fields[1]), strings.TrimSpace(fields[3])
		if !strings.Contains(treatment, "Exact") && !strings.Contains(treatment, "Strengthen") {
			continue
		}
		if _, duplicate := result[id]; duplicate {
			t.Fatalf("duplicate frozen inventory ID %s", id)
		}
		result[id] = treatment
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
