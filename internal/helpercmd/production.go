package helpercmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/review"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

const productPolicy = "spec-kitty-native-local-authority/v1"

// Installation is process installation data, not a user-selectable backend.
// Production uses one admitted GapDB tuple and fails closed when it is absent.
type Installation struct {
	GapDBExecutable string
	EvidenceRoot    string
	RuntimeRoot     string
}

func InstallationFromEnvironment() Installation {
	return Installation{
		GapDBExecutable: os.Getenv("SPEC_KITTY_GAPDBD_PATH"),
		EvidenceRoot:    os.Getenv("SPEC_KITTY_EVIDENCE_ROOT"),
		RuntimeRoot:     os.Getenv("SPEC_KITTY_RUNTIME_ROOT"),
	}
}

type productionFactory struct{ installation Installation }

func NewProductionFactory(installation Installation) HostFactory {
	return &productionFactory{installation: installation}
}

func (f *productionFactory) Classify(ctx context.Context, root string) (controlroot.Decision, error) {
	classifier, err := controlroot.NewClassifier(controlroot.DefaultBudgets(), controlroot.TrustPolicy{
		OwnerUID: uint32(os.Geteuid()), OwnerGID: uint32(os.Getegid()), AllowGroupWritable: true,
	})
	if err != nil {
		return controlroot.Decision{}, err
	}
	return classifier.Decide(ctx, root)
}

func (f *productionFactory) Start(ctx context.Context, root string) (hostSession, error) {
	decision, err := f.Classify(ctx, root)
	if err != nil {
		return nil, err
	}
	if decision.Action != controlroot.Initialize && decision.Action != controlroot.Recover {
		return nil, &controlroot.OfflineDispositionError{Code: controlroot.OfflineDispositionRequiredCode, Disposition: decision.Disposition}
	}
	installation, err := f.resolve(root)
	if err != nil {
		return nil, &productionruntime.Error{Kind: productionruntime.ErrorInvalidConfiguration, State: productionruntime.HostRecovering, Field: "installation"}
	}
	if err := os.MkdirAll(installation.RuntimeRoot, 0o700); err != nil {
		return nil, &productionruntime.Error{Kind: productionruntime.ErrorStartup, State: productionruntime.HostRecovering, Field: "runtime_root"}
	}
	if err := os.Chmod(installation.RuntimeRoot, 0o700); err != nil {
		return nil, &productionruntime.Error{Kind: productionruntime.ErrorStartup, State: productionruntime.HostRecovering, Field: "runtime_root"}
	}
	hostDigest := sha256.Sum256([]byte(filepath.Clean(root)))
	policyDigest := sha256.Sum256([]byte(productPolicy))
	runID, err := randomToken("run-", 12)
	if err != nil {
		return nil, &productionruntime.Error{Kind: productionruntime.ErrorStartup, State: productionruntime.HostRecovering, Field: "run_identity"}
	}
	host, err := productionruntime.Start(ctx, productionruntime.Config{
		ControlRoot:                          root,
		HostAuthorityID:                      journalv2.HostAuthorityID("host-" + hex.EncodeToString(hostDigest[:12])),
		AuthorityBindingOperationID:          "bind-" + hex.EncodeToString(hostDigest[:12]),
		M1AdoptionRecordPath:                 filepath.Join(installation.EvidenceRoot, "m1-adoption.json"),
		AssertionExtensionAdoptionRecordPath: filepath.Join(installation.EvidenceRoot, "m1-assertion-extension-adoption.json"),
		Sidecar: sidecargapdb.LifecycleConfig{
			ExecutablePath:      installation.GapDBExecutable,
			AdmissionRecordPath: filepath.Join(installation.EvidenceRoot, "m1-assertion-extension-admission.json"),
			RuntimeRoot:         installation.RuntimeRoot,
			DataDirectory:       root,
			SupervisorRunID:     runID,
			ProductPolicySHA256: hex.EncodeToString(policyDigest[:]),
		},
		CapabilityReadiness:   unavailableReadinessSource{},
		DecompositionProvider: unavailableDecompositionProvider{},
		CandidateVerifier:     gitrepo.NewHandoffVerifier(),
		IntegrationGit:        unavailableIntegrationGit{},
	})
	if err != nil {
		return nil, err
	}
	return productionSession{host: host}, nil
}

func (f *productionFactory) Observe(ctx context.Context, root string) (observationSession, error) {
	installation, err := f.resolve(root)
	if err != nil {
		return nil, &productionruntime.Error{Kind: productionruntime.ErrorInvalidConfiguration, State: productionruntime.HostRecovering, Field: "installation"}
	}
	hostDigest := sha256.Sum256([]byte(filepath.Clean(root)))
	policyDigest := sha256.Sum256([]byte(productPolicy))
	runID, err := randomToken("observe-", 12)
	if err != nil {
		return nil, &productionruntime.Error{Kind: productionruntime.ErrorStartup, State: productionruntime.HostRecovering, Field: "run_identity"}
	}
	observation, err := productionruntime.Observe(ctx, productionruntime.Config{
		ControlRoot: root, HostAuthorityID: journalv2.HostAuthorityID("host-" + hex.EncodeToString(hostDigest[:12])),
		AuthorityBindingOperationID:          "bind-" + hex.EncodeToString(hostDigest[:12]),
		M1AdoptionRecordPath:                 filepath.Join(installation.EvidenceRoot, "m1-adoption.json"),
		AssertionExtensionAdoptionRecordPath: filepath.Join(installation.EvidenceRoot, "m1-assertion-extension-adoption.json"),
		Sidecar:                              sidecargapdb.LifecycleConfig{ExecutablePath: installation.GapDBExecutable, AdmissionRecordPath: filepath.Join(installation.EvidenceRoot, "m1-assertion-extension-admission.json"), RuntimeRoot: installation.RuntimeRoot, DataDirectory: root, SupervisorRunID: runID, ProductPolicySHA256: hex.EncodeToString(policyDigest[:])},
		CapabilityReadiness:                  unavailableReadinessSource{},
	})
	if err != nil {
		return nil, err
	}
	return productionObservation{observation: observation}, nil
}

func (f *productionFactory) resolve(root string) (Installation, error) {
	installation := f.installation
	if installation.RuntimeRoot == "" {
		installation.RuntimeRoot = filepath.Join(filepath.Dir(root), ".spec-kitty-runtime")
	}
	for _, value := range []string{installation.GapDBExecutable, installation.EvidenceRoot, installation.RuntimeRoot} {
		if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return Installation{}, errors.New("installation paths must be absolute and canonical")
		}
	}
	return installation, nil
}

func randomToken(prefix string, bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}

type operationCapabilityHost interface {
	RuntimeEpochID() journalv2.RuntimeEpochID
	OperationOrigin(context.Context, journalv2.OperationID) (journalv2.RuntimeEpochID, bool, error)
	ExecuteMission(context.Context, journalv2.RuntimeEpochID, mission.MissionAuthorityCommandV2) (mission.MissionAuthorityResultV2, error)
	ExecuteReadiness(context.Context, journalv2.RuntimeEpochID, mission.ReadinessAuthorityCommandV2) (mission.ReadinessAuthorityResultV2, error)
	ExecuteWork(context.Context, journalv2.RuntimeEpochID, execution.ExecutionAuthorityRequestV2) (execution.ExecutionAuthorityResultV2, error)
	RebindWorkspaceRuntimeEpoch(context.Context, journalv2.RuntimeEpochID, execution.WorkspaceEpochRebindRequestV2) (execution.WorkspaceAuthorityResultV2, error)
	SubmitHandoff(context.Context, journalv2.RuntimeEpochID, handoff.AuthorityRequestV2) (handoff.AuthorityResultV2, error)
	SubmitReview(context.Context, journalv2.RuntimeEpochID, review.AuthorityRequestV2) (review.AuthorityResultV2, error)
	ExecuteAcceptance(context.Context, journalv2.RuntimeEpochID, integration.AcceptanceAuthorityRequestV2) (integration.AcceptanceResultV2, error)
	ExecuteIntegration(context.Context, journalv2.RuntimeEpochID, integration.IntegrationAuthorityRequestV2) (integration.IntegrationAttemptStateV2, error)
	ExecuteIntegrationCleanup(context.Context, journalv2.RuntimeEpochID, integration.IntegrationCleanupRequestV2) (integration.IntegrationAttemptStateV2, error)
	ExecuteEffect(context.Context, journalv2.RuntimeEpochID, execution.EffectAuthorityRequestV2) (execution.EffectAuthorityResultV2, error)
}

type productionSession struct {
	host         *productionruntime.Host
	capabilities operationCapabilityHost
}

type productionObservation struct {
	observation *productionruntime.Observation
}

func (s productionObservation) Status() productionruntime.HostStatus { return s.observation.Status() }
func (s productionObservation) Mission(project journalv2.ProjectID, missionID journalv2.MissionID) (productionruntime.MissionProgress, error) {
	return s.observation.Progress().Mission(project, missionID)
}
func (s productionObservation) Operation(ctx context.Context, project journalv2.ProjectID, missionID journalv2.MissionID, operation string) (productionruntime.ProgressItem, error) {
	return s.observation.InspectOperation(ctx, project, missionID, operation)
}
func (s productionObservation) Close(ctx context.Context) error { return s.observation.Close(ctx) }

func (s productionSession) Status() productionruntime.HostStatus { return s.host.Status() }
func (s productionSession) Mission(project journalv2.ProjectID, missionID journalv2.MissionID) (productionruntime.MissionProgress, error) {
	return s.host.Progress().Mission(project, missionID)
}
func (s productionSession) Operation(ctx context.Context, project journalv2.ProjectID, missionID journalv2.MissionID, operation string) (productionruntime.ProgressItem, error) {
	return s.host.InspectOperation(ctx, project, missionID, operation)
}
func (s productionSession) Execute(ctx context.Context, operation decodedOperation) (TypedResult, error) {
	capabilities := s.capabilities
	if capabilities == nil {
		capabilities = s.host
	}
	hostEpoch := capabilities.RuntimeEpochID()
	callerEpoch := operationEpoch(operation)
	origin, found, err := capabilities.OperationOrigin(ctx, operation.operation)
	if err != nil {
		return TypedResult{}, err
	}
	if found {
		if callerEpoch != RuntimeEpochCurrent {
			return TypedResult{}, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityIdempotencyConflictV2, Field: "operation_id"}
		}
		operation = resolveCurrentEpochSelector(operation, origin)
	} else {
		if callerEpoch != RuntimeEpochCurrent {
			return TypedResult{}, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityStaleEpochV2, Field: "runtime_epoch"}
		}
		operation = resolveCurrentEpochSelector(operation, hostEpoch)
	}
	if operation.mission != nil {
		result, err := capabilities.ExecuteMission(ctx, hostEpoch, *operation.mission)
		if err != nil {
			return TypedResult{}, err
		}
		return TypedResult{Kind: ResultMission, Payload: result.CanonicalBytes()}, nil
	}
	if operation.readiness != nil {
		result, err := capabilities.ExecuteReadiness(ctx, hostEpoch, *operation.readiness)
		if err != nil {
			return TypedResult{}, err
		}
		return TypedResult{Kind: ResultReadiness, Payload: result.CanonicalBytes()}, nil
	}
	if operation.execution != nil {
		result, err := capabilities.ExecuteWork(ctx, hostEpoch, *operation.execution)
		if err != nil {
			return TypedResult{}, err
		}
		payload, err := journalv2.CanonicalObject(mustJSON(map[string]any{"schema_version": 1, "state": json.RawMessage(result.State.CanonicalBytes()), "replayed": result.Replayed}))
		if err != nil {
			return TypedResult{}, err
		}
		return TypedResult{Kind: ResultExecution, Payload: payload}, nil
	}
	if operation.rebind != nil {
		result, err := capabilities.RebindWorkspaceRuntimeEpoch(ctx, hostEpoch, *operation.rebind)
		if err != nil {
			return TypedResult{}, err
		}
		payload, err := typedPayload(map[string]any{"schema_version": 1, "execution": json.RawMessage(result.Execution.CanonicalBytes()), "workspace": json.RawMessage(result.Workspace.CanonicalBytes()), "replayed": result.Replayed})
		return TypedResult{Kind: ResultWorkspace, Payload: payload}, err
	}
	if operation.handoff != nil {
		result, err := capabilities.SubmitHandoff(ctx, hostEpoch, *operation.handoff)
		if err != nil {
			return TypedResult{}, err
		}
		payload, err := typedPayload(map[string]any{"schema_version": 1, "state": json.RawMessage(result.State.CanonicalBytes()), "publication_id": result.PublicationID, "replayed": result.Replayed})
		return TypedResult{Kind: ResultHandoff, Payload: payload}, err
	}
	if operation.review != nil {
		result, err := capabilities.SubmitReview(ctx, hostEpoch, *operation.review)
		if err != nil {
			return TypedResult{}, err
		}
		payload, err := typedPayload(map[string]any{"schema_version": 1, "state": json.RawMessage(result.State.CanonicalBytes()), "publication_id": result.PublicationID, "replayed": result.Replayed})
		return TypedResult{Kind: ResultReview, Payload: payload}, err
	}
	if operation.acceptance != nil {
		result, err := capabilities.ExecuteAcceptance(ctx, hostEpoch, *operation.acceptance)
		if err != nil {
			return TypedResult{}, err
		}
		payload, err := typedPayload(map[string]any{"schema_version": 1, "state": json.RawMessage(result.State.CanonicalBytes()), "publication_id": result.PublicationID, "replayed": result.Replayed})
		return TypedResult{Kind: ResultAcceptance, Payload: payload}, err
	}
	if operation.integration != nil {
		result, err := capabilities.ExecuteIntegration(ctx, hostEpoch, *operation.integration)
		if err != nil {
			return TypedResult{}, err
		}
		return TypedResult{Kind: ResultIntegration, Payload: result.CanonicalBytes()}, nil
	}
	if operation.cleanup != nil {
		result, err := capabilities.ExecuteIntegrationCleanup(ctx, hostEpoch, *operation.cleanup)
		if err != nil {
			return TypedResult{}, err
		}
		return TypedResult{Kind: ResultIntegrationCleanup, Payload: result.CanonicalBytes()}, nil
	}
	if operation.effect != nil {
		result, err := capabilities.ExecuteEffect(ctx, hostEpoch, *operation.effect)
		if err != nil {
			return TypedResult{}, err
		}
		state, err := result.State.CanonicalResultBytesV2()
		if err != nil {
			return TypedResult{}, err
		}
		payload, err := typedPayload(map[string]any{"schema_version": 1, "state": json.RawMessage(state), "execution": json.RawMessage(result.ExecutionSlot.CanonicalBytes()), "replayed": result.Replayed})
		return TypedResult{Kind: ResultEffect, Payload: payload}, err
	}
	return TypedResult{}, errors.New("operation has no admitted capability")
}

func operationEpoch(operation decodedOperation) journalv2.RuntimeEpochID {
	switch {
	case operation.mission != nil:
		return operation.mission.OriginatingRuntimeEpochID
	case operation.readiness != nil:
		return operation.readiness.OriginatingRuntimeEpochID
	case operation.execution != nil:
		return operation.execution.RuntimeEpochID
	case operation.rebind != nil:
		return operation.rebind.OriginatingRuntimeEpochID
	case operation.handoff != nil:
		return operation.handoff.RuntimeEpochID
	case operation.review != nil:
		return operation.review.RuntimeEpochID
	case operation.acceptance != nil:
		return operation.acceptance.RuntimeEpochID
	case operation.integration != nil:
		return operation.integration.RuntimeEpochID
	case operation.cleanup != nil:
		return operation.cleanup.RuntimeEpochID
	case operation.effect != nil:
		return operation.effect.RuntimeEpochID
	default:
		return ""
	}
}

func resolveCurrentEpochSelector(operation decodedOperation, epoch journalv2.RuntimeEpochID) decodedOperation {
	if operation.mission != nil {
		command := *operation.mission
		command.OriginatingRuntimeEpochID = epoch
		operation.mission = &command
	}
	if operation.readiness != nil {
		command := *operation.readiness
		command.OriginatingRuntimeEpochID = epoch
		operation.readiness = &command
	}
	if operation.execution != nil {
		command := *operation.execution
		command.RuntimeEpochID = epoch
		operation.execution = &command
	}
	if operation.rebind != nil {
		command := *operation.rebind
		command.OriginatingRuntimeEpochID = epoch
		operation.rebind = &command
	}
	if operation.handoff != nil {
		command := *operation.handoff
		command.RuntimeEpochID = epoch
		if command.Workspace.RuntimeEpochID == RuntimeEpochCurrent {
			command.Workspace.RuntimeEpochID = epoch
		}
		operation.handoff = &command
	}
	if operation.review != nil {
		command := *operation.review
		command.RuntimeEpochID = epoch
		if command.Workspace.RuntimeEpochID == RuntimeEpochCurrent {
			command.Workspace.RuntimeEpochID = epoch
		}
		operation.review = &command
	}
	if operation.acceptance != nil {
		command := *operation.acceptance
		command.RuntimeEpochID = epoch
		operation.acceptance = &command
	}
	if operation.integration != nil {
		command := *operation.integration
		command.RuntimeEpochID = epoch
		operation.integration = &command
	}
	if operation.cleanup != nil {
		command := *operation.cleanup
		command.RuntimeEpochID = epoch
		operation.cleanup = &command
	}
	if operation.effect != nil {
		command := *operation.effect
		command.RuntimeEpochID = epoch
		if command.Intent.OriginRuntimeEpochID == RuntimeEpochCurrent {
			command.Intent.OriginRuntimeEpochID = epoch
		}
		if command.ExecutionSlot.RuntimeEpochID == RuntimeEpochCurrent {
			command.ExecutionSlot.RuntimeEpochID = epoch
		}
		operation.effect = &command
	}
	return operation
}

func typedPayload(value any) ([]byte, error) {
	return journalv2.CanonicalObject(mustJSON(value))
}
func (s productionSession) Close(ctx context.Context) error { return s.host.Close(ctx) }

// unavailableReadinessSource is explicit M2 absence evidence. Future provider
// composition replaces it; it never guesses readiness from prose or files.
type unavailableReadinessSource struct{}

func (unavailableReadinessSource) ObserveCapabilityReadiness(_ context.Context, query mission.CapabilityReadinessQueryV2) (mission.CapabilityReadinessEvidenceV2, error) {
	identity := "m2-capability-absent:" + string(query.Action())
	digest := sha256.Sum256([]byte(string(query.Capture().DatabaseID()) + ":" + strconv.FormatUint(query.Capture().CurrentRevision(), 10) + ":" + identity))
	return mission.CapabilityReadinessEvidenceV2{
		Capture: query.Capture(), ProjectID: query.ProjectID(), MissionID: query.MissionID(), Action: query.Action(),
		ObservedAbsent: true, EvidenceIdentity: identity, EvidenceDigest: hex.EncodeToString(digest[:]),
	}, nil
}

type unavailableDecompositionProvider struct{}

func (unavailableDecompositionProvider) RequireCapabilities(context.Context, []mission.DecompositionEffectKindV2) error {
	return errors.New("decomposition provider is not composed in M2")
}
func (unavailableDecompositionProvider) ObserveEffect(context.Context, journalv2.EffectID) (mission.DecompositionProviderObservationV2, bool, error) {
	return mission.DecompositionProviderObservationV2{}, false, nil
}
func (unavailableDecompositionProvider) MutateEffect(context.Context, mission.DecompositionProviderMutationV2) (mission.DecompositionProviderMutationOutcomeV2, error) {
	return mission.DecompositionProviderMutationRefusedV2, errors.New("decomposition provider is not composed in M2")
}
func (unavailableDecompositionProvider) CompensateEffect(context.Context, mission.DecompositionProviderMutationV2, mission.DecompositionProviderObservationV2) error {
	return errors.New("decomposition provider is not composed in M2")
}

type unavailableIntegrationGit struct{}

func (unavailableIntegrationGit) TargetHead(context.Context, ids.RepositoryID, string) (string, error) {
	return "", errors.New("git integration is not composed in M2")
}
func (unavailableIntegrationGit) InstallFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, error) {
	return integration.IntegrationFenceObservationV2{}, errors.New("git integration is not composed in M2")
}
func (unavailableIntegrationGit) ObserveFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, bool, error) {
	return integration.IntegrationFenceObservationV2{}, false, nil
}
func (unavailableIntegrationGit) EnsureWorkspace(context.Context, integration.IntegrationWorkspaceIntentV2) (integration.WorkspaceFacts, error) {
	return integration.WorkspaceFacts{}, errors.New("git integration is not composed in M2")
}
func (unavailableIntegrationGit) MergeCandidate(context.Context, integration.IntegrationMergeIntentV2, integration.WorkspaceFacts) (string, *integration.ConflictEvidence, error) {
	return "", nil, errors.New("git integration is not composed in M2")
}
func (unavailableIntegrationGit) CompareAndSwapTarget(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, error) {
	return integration.IntegrationPublishObservationV2{}, errors.New("git integration is not composed in M2")
}
func (unavailableIntegrationGit) ObserveTargetPublication(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, bool, error) {
	return integration.IntegrationPublishObservationV2{}, false, nil
}
func (unavailableIntegrationGit) CleanupWorkspace(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	return integration.IntegrationCleanupObservationV2{}, errors.New("git integration is not composed in M2")
}
func (unavailableIntegrationGit) ObserveCleanup(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, bool, error) {
	return integration.IntegrationCleanupObservationV2{}, false, nil
}
func (unavailableIntegrationGit) IsAncestor(context.Context, ids.RepositoryID, string, string) (bool, error) {
	return false, errors.New("git integration is not composed in M2")
}
