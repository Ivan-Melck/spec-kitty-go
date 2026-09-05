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
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
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

type productionSession struct{ host *productionruntime.Host }

func (s productionSession) Status() productionruntime.HostStatus { return s.host.Status() }
func (s productionSession) Mission(project journalv2.ProjectID, missionID journalv2.MissionID) (productionruntime.MissionProgress, error) {
	return s.host.Progress().Mission(project, missionID)
}
func (s productionSession) Operation(project journalv2.ProjectID, missionID journalv2.MissionID, operation string) (productionruntime.ProgressItem, error) {
	return s.host.Progress().Get(project, missionID, productionruntime.ProgressOperations, operation)
}
func (s productionSession) Execute(ctx context.Context, operation decodedOperation) (json.RawMessage, error) {
	epoch := s.host.RuntimeEpochID()
	if operation.mission != nil {
		result, err := s.host.ExecuteMission(ctx, epoch, *operation.mission)
		if err != nil {
			return nil, err
		}
		return result.CanonicalBytes(), nil
	}
	if operation.readiness != nil {
		result, err := s.host.ExecuteReadiness(ctx, epoch, *operation.readiness)
		if err != nil {
			return nil, err
		}
		return result.CanonicalBytes(), nil
	}
	return nil, errors.New("operation has no admitted capability")
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
	return "", errors.New("Git integration is not composed in M2")
}
func (unavailableIntegrationGit) InstallFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, error) {
	return integration.IntegrationFenceObservationV2{}, errors.New("Git integration is not composed in M2")
}
func (unavailableIntegrationGit) ObserveFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, bool, error) {
	return integration.IntegrationFenceObservationV2{}, false, nil
}
func (unavailableIntegrationGit) EnsureWorkspace(context.Context, integration.IntegrationWorkspaceIntentV2) (integration.WorkspaceFacts, error) {
	return integration.WorkspaceFacts{}, errors.New("Git integration is not composed in M2")
}
func (unavailableIntegrationGit) MergeCandidate(context.Context, integration.IntegrationMergeIntentV2, integration.WorkspaceFacts) (string, *integration.ConflictEvidence, error) {
	return "", nil, errors.New("Git integration is not composed in M2")
}
func (unavailableIntegrationGit) CompareAndSwapTarget(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, error) {
	return integration.IntegrationPublishObservationV2{}, errors.New("Git integration is not composed in M2")
}
func (unavailableIntegrationGit) ObserveTargetPublication(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, bool, error) {
	return integration.IntegrationPublishObservationV2{}, false, nil
}
func (unavailableIntegrationGit) CleanupWorkspace(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	return integration.IntegrationCleanupObservationV2{}, errors.New("Git integration is not composed in M2")
}
func (unavailableIntegrationGit) ObserveCleanup(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, bool, error) {
	return integration.IntegrationCleanupObservationV2{}, false, nil
}
func (unavailableIntegrationGit) IsAncestor(context.Context, ids.RepositoryID, string, string) (bool, error) {
	return false, errors.New("Git integration is not composed in M2")
}
