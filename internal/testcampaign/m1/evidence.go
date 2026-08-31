package m1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

const (
	EvidenceManifestName  = "evidence-manifest.json"
	TechnicalPassName     = "technical-pass.signature"
	TechnicalPassDomain   = "spec-kitty.m1.machine-pass.v1"
	AdmittedTupleSHA256   = "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d"
	AdmittedSidecarSHA256 = "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e"
)

var ScenarioIDs = []string{
	"data/get-put", "conditional/cas-delete", "batch/atomic",
	"scan/sorted-prefix", "watch/registered-order", "expiry/release-authority",
	"ack/memory-visible", "ack/durable-restart", "inspect/authority",
	"authority/lease", "authority/review", "authority/workflow",
	"authority/integration", "response-loss/reconcile",
}

var FaultIDs = []string{"GJ-01", "GJ-02", "GJ-03", "GJ-04", "GJ-05"}
var FaultCuts = []string{"before_request_publication", "after_durable_acceptance_before_client_response", "after_workspace_creation_before_durable_binding", "after_durable_binding_before_run_capsule_response", "after_effect_intent_before_observation"}
var FocusedIDs = []string{"atomic-publication-nfr001", "lost-response-idempotency-nfr002", "concurrent-claims-nfr003", "competing-integrations-nfr004", "restore-playback-nfr005", "restart-epoch-nfr007", "zombie-isolation-nfr008", "attribution-reassignment-nfr015"}
var MutantIDs = []string{"split-batch", "pre-ack-publish", "retry-new-operation-id", "revision-fill-in", "server-revision-in-value-or-key", "invalid-event-order", "current-batch-checkpoint", "global-authority-cas", "shared-projection-head", "runtime-coordinate-conflation", "workspace-reuse", "same-contributor-review", "invalid-checkpoint", "playback-adapter-or-write", "watch-as-journal", "misattributed-public-fields", "fake-timeout", "gapdb-import-allowlist-or-internal-server", "incomplete-signature"}

type ScenarioResult struct {
	ScenarioID     string `json:"scenario_id"`
	RunID          string `json:"run_id"`
	Seed           uint64 `json:"seed"`
	Started        bool   `json:"started"`
	Terminal       bool   `json:"terminal"`
	Outcome        string `json:"outcome"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}

type FaultResult struct {
	FaultID        string `json:"fault_id"`
	RunID          string `json:"run_id"`
	Seed           uint64 `json:"seed"`
	Cut            string `json:"cut"`
	Started        bool   `json:"started"`
	Terminal       bool   `json:"terminal"`
	Outcome        string `json:"outcome"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}

type FocusedSummary struct {
	ID                  string `json:"id"`
	RunID               string `json:"run_id"`
	RepetitionsStarted  int    `json:"repetitions_started"`
	RepetitionsTerminal int    `json:"repetitions_terminal"`
	RepetitionsPassed   int    `json:"repetitions_passed"`
	ArtifactSHA256      string `json:"artifact_sha256"`
}

type MutantResult struct {
	ID             string `json:"id"`
	RunID          string `json:"run_id"`
	Outcome        string `json:"outcome"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}

type IndependentReview struct {
	ReviewerActorID   string `json:"reviewer_actor_id,omitempty"`
	ReviewerProcessID string `json:"reviewer_process_id,omitempty"`
	Verdict           string `json:"verdict"`
	ArtifactSHA256    string `json:"artifact_sha256,omitempty"`
}

type EvidenceInvariantSummary struct {
	OneValidAuthorityPerWorkRevision bool `json:"one_valid_authority_per_work_revision"`
	WorkspaceBindingImmutable        bool `json:"workspace_binding_immutable"`
	StaleAuthorityRefused            bool `json:"stale_authority_refused"`
	PrimaryIsolation                 bool `json:"primary_isolation"`
	ReviewerActorIndependence        bool `json:"reviewer_actor_independence"`
	SerializedTargetAuthority        bool `json:"serialized_target_authority"`
	EffectAttribution                bool `json:"effect_attribution"`
	JournalComplete                  bool `json:"journal_complete"`
}

func (s EvidenceInvariantSummary) Passed() bool {
	return s.OneValidAuthorityPerWorkRevision && s.WorkspaceBindingImmutable && s.StaleAuthorityRefused && s.PrimaryIsolation && s.ReviewerActorIndependence && s.SerializedTargetAuthority && s.EffectAttribution && s.JournalComplete
}

func EvidenceInvariants(source InvariantSummary) EvidenceInvariantSummary {
	return EvidenceInvariantSummary{source.OneValidAuthorityPerWorkRevision, source.WorkspaceBindingImmutable, source.StaleAuthorityRefused, source.PrimaryIsolation, source.ReviewerActorIndependence, source.SerializedTargetAuthority, source.EffectAttribution, source.JournalComplete}
}

type EvidenceArtifact struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type EvidenceManifest struct {
	SchemaVersion            int                      `json:"schema_version"`
	ProgramID                string                   `json:"program_id"`
	MissionID                string                   `json:"mission_id"`
	RunID                    string                   `json:"run_id"`
	SourceCommit             string                   `json:"source_commit"`
	SourceTree               string                   `json:"source_tree"`
	AdmittedTupleSHA256      string                   `json:"admitted_tuple_sha256"`
	SidecarProvenanceSHA256  string                   `json:"sidecar_provenance_sha256"`
	ScenarioResults          []ScenarioResult         `json:"scenario_results"`
	FaultResults             []FaultResult            `json:"fault_results"`
	OrderedTrial             OrderedTrial             `json:"ordered_trial"`
	FocusedSubtrialSummaries []FocusedSummary         `json:"focused_subtrial_summaries"`
	MutantResults            []MutantResult           `json:"mutant_results"`
	InvariantSummary         EvidenceInvariantSummary `json:"invariant_summary"`
	IndependentReview        IndependentReview        `json:"independent_review"`
	Artifacts                []EvidenceArtifact       `json:"artifacts"`
	Outcome                  string                   `json:"outcome"`
}

type ArtifactInput struct {
	Kind string
	Data []byte
}

type Publication struct {
	Directory      string
	ManifestPath   string
	ManifestSHA256 string
	TechnicalPass  string
}

type VerifyOptions struct {
	RepositoryRoot string
	Lifecycle      *sidecargapdb.Lifecycle
	ExpectedSource *SourceBinding
}

type Verification struct {
	Outcome          string `json:"outcome"`
	RunID            string `json:"run_id"`
	ManifestSHA256   string `json:"manifest_sha256"`
	TechnicalPass    string `json:"technical_pass_signature,omitempty"`
	AdoptionEligible bool   `json:"adoption_eligible"`
}

// SemanticOutcome recomputes the machine verdict without trusting the
// manifest's claimed outcome.
func SemanticOutcome(manifest EvidenceManifest) error {
	if manifest.SchemaVersion != 1 || manifest.ProgramID != "production-runtime" || manifest.MissionID != "M1" || manifest.RunID == "" || manifest.AdmittedTupleSHA256 != AdmittedTupleSHA256 || !hexSized(manifest.SourceCommit, 40) || !hexSized(manifest.SourceTree, 40) || !isSHA256String(manifest.SidecarProvenanceSHA256) {
		return fmt.Errorf("manifest identity/provenance header is invalid")
	}
	if err := exactScenarios(manifest.RunID, manifest.ScenarioResults); err != nil {
		return err
	}
	if err := exactFaults(manifest.RunID, manifest.FaultResults); err != nil {
		return err
	}
	if err := ValidateOrderedTrial(manifest.OrderedTrial, manifest.RunID); err != nil {
		return err
	}
	if err := exactFocused(manifest.RunID, manifest.FocusedSubtrialSummaries); err != nil {
		return err
	}
	if err := exactMutants(manifest.RunID, manifest.MutantResults); err != nil {
		return err
	}
	if !manifest.InvariantSummary.Passed() {
		return fmt.Errorf("universal invariant summary is not a pass")
	}
	if manifest.IndependentReview.Verdict != "pass" || manifest.IndependentReview.ReviewerActorID == "" || manifest.IndependentReview.ReviewerProcessID == "" || !isSHA256String(manifest.IndependentReview.ArtifactSHA256) {
		return fmt.Errorf("independent campaign review is absent or non-pass")
	}
	if len(manifest.Artifacts) == 0 {
		return fmt.Errorf("evidence artifact census is empty")
	}
	return nil
}

func exactScenarios(runID string, got []ScenarioResult) error {
	if len(got) != len(ScenarioIDs) {
		return fmt.Errorf("scenario census=%d want=%d", len(got), len(ScenarioIDs))
	}
	for i, want := range ScenarioIDs {
		r := got[i]
		if r.ScenarioID != want || r.RunID != runID || !r.Started || !r.Terminal || r.Outcome != "pass" || !isSHA256String(r.ArtifactSHA256) {
			return fmt.Errorf("scenario %d/%s is reordered, foreign, or non-pass", i, want)
		}
	}
	return nil
}
func exactFaults(runID string, got []FaultResult) error {
	if len(got) != len(FaultIDs) {
		return fmt.Errorf("fault census=%d want=%d", len(got), len(FaultIDs))
	}
	for i, want := range FaultIDs {
		r := got[i]
		if r.FaultID != want || r.Cut != FaultCuts[i] || r.RunID != runID || !r.Started || !r.Terminal || r.Outcome != "pass" || !isSHA256String(r.ArtifactSHA256) {
			return fmt.Errorf("fault %d/%s is reordered, foreign, or non-pass", i, want)
		}
	}
	return nil
}
func exactFocused(runID string, got []FocusedSummary) error {
	if len(got) != len(FocusedIDs) {
		return fmt.Errorf("focused census=%d want=%d", len(got), len(FocusedIDs))
	}
	for i, want := range FocusedIDs {
		r := got[i]
		if r.ID != want || r.RunID != runID || r.RepetitionsStarted < 100 || r.RepetitionsStarted != r.RepetitionsTerminal || r.RepetitionsTerminal != r.RepetitionsPassed || !isSHA256String(r.ArtifactSHA256) {
			return fmt.Errorf("focused summary %d/%s is incomplete or foreign", i, want)
		}
	}
	return nil
}
func exactMutants(runID string, got []MutantResult) error {
	if len(got) != len(MutantIDs) {
		return fmt.Errorf("mutant census=%d want=%d", len(got), len(MutantIDs))
	}
	for i, want := range MutantIDs {
		r := got[i]
		if r.ID != want || r.RunID != runID || r.Outcome != "killed" || !isSHA256String(r.ArtifactSHA256) {
			return fmt.Errorf("mutant %d/%s survived, was reordered, or is foreign", i, want)
		}
	}
	return nil
}

func PublishEvidence(outputRoot string, manifest EvidenceManifest, inputs []ArtifactInput) (Publication, error) {
	if !filepath.IsAbs(outputRoot) || manifest.RunID == "" {
		return Publication{}, fmt.Errorf("absolute output root and RunID are required")
	}
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return Publication{}, err
	}
	temporary, err := os.MkdirTemp(outputRoot, "."+manifest.RunID+".tmp-")
	if err != nil {
		return Publication{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	artifactsDir := filepath.Join(temporary, "artifacts")
	if err := os.Mkdir(artifactsDir, 0o700); err != nil {
		return Publication{}, err
	}
	manifest.Artifacts = nil
	seenKinds := make(map[string]int)
	for _, input := range inputs {
		if input.Kind == "" || len(input.Data) > 16<<20 {
			return Publication{}, fmt.Errorf("artifact kind is absent or artifact exceeds 16 MiB")
		}
		seenKinds[input.Kind]++
		name := fmt.Sprintf("%03d-%s.json", len(manifest.Artifacts)+1, safeToken(input.Kind))
		relative := filepath.ToSlash(filepath.Join("artifacts", name))
		if err := durableWrite(filepath.Join(temporary, filepath.FromSlash(relative)), input.Data, 0o400); err != nil {
			return Publication{}, err
		}
		manifest.Artifacts = append(manifest.Artifacts, EvidenceArtifact{Kind: input.Kind, Path: relative, SHA256: SHA256Hex(input.Data), SizeBytes: int64(len(input.Data))})
	}
	for _, kind := range []string{"source-binding", "sidecar-provenance", "proving-binary", "contracts", "budgets", "crown-ledger"} {
		if seenKinds[kind] != 1 {
			return Publication{}, fmt.Errorf("mandatory artifact kind %q count=%d want=1", kind, seenKinds[kind])
		}
	}
	if manifest.Outcome == "pass" {
		if err := SemanticOutcome(manifest); err != nil {
			return Publication{}, err
		}
	}
	manifestBytes, err := CanonicalJSON(manifest)
	if err != nil {
		return Publication{}, err
	}
	manifestDigest := SHA256Hex(manifestBytes)
	if err := durableWrite(filepath.Join(temporary, EvidenceManifestName), manifestBytes, 0o400); err != nil {
		return Publication{}, err
	}
	technical := ""
	if manifest.Outcome == "pass" {
		technical = TechnicalSignature(manifestDigest)
		if err := durableWrite(filepath.Join(temporary, TechnicalPassName), []byte(technical+"\n"), 0o400); err != nil {
			return Publication{}, err
		}
	}
	if err := syncDirectory(artifactsDir); err != nil {
		return Publication{}, err
	}
	if err := syncDirectory(temporary); err != nil {
		return Publication{}, err
	}
	target := filepath.Join(outputRoot, manifest.RunID)
	if err := os.Rename(temporary, target); err != nil {
		return Publication{}, fmt.Errorf("publish immutable run directory: %w", err)
	}
	keep = true
	if err := syncDirectory(outputRoot); err != nil {
		return Publication{}, err
	}
	return Publication{Directory: target, ManifestPath: filepath.Join(target, EvidenceManifestName), ManifestSHA256: manifestDigest, TechnicalPass: technical}, nil
}

func VerifyEvidence(ctx context.Context, directory string, options VerifyOptions) (Verification, EvidenceManifest, error) {
	if !filepath.IsAbs(directory) {
		return Verification{}, EvidenceManifest{}, fmt.Errorf("evidence directory must be absolute")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return Verification{}, EvidenceManifest{}, err
	}
	defer root.Close()
	manifestBytes, err := readRootRegular(root, EvidenceManifestName, 8<<20)
	if err != nil {
		return Verification{}, EvidenceManifest{}, err
	}
	var manifest EvidenceManifest
	if err := jsonUnmarshalStrict(manifestBytes, &manifest); err != nil {
		return Verification{}, EvidenceManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	canonical, err := CanonicalJSON(manifest)
	if err != nil || !bytes.Equal(canonical, manifestBytes) {
		return Verification{}, manifest, fmt.Errorf("manifest is not canonical JSON v1")
	}
	manifestDigest := SHA256Hex(manifestBytes)
	if filepath.Base(directory) != manifest.RunID {
		return Verification{}, manifest, fmt.Errorf("run directory and manifest RunID differ")
	}
	if options.ExpectedSource != nil {
		if !options.ExpectedSource.Valid() || options.ExpectedSource.Commit != manifest.SourceCommit || options.ExpectedSource.Tree != manifest.SourceTree || options.ExpectedSource.SidecarProvenanceSHA256 != manifest.SidecarProvenanceSHA256 {
			return Verification{}, manifest, fmt.Errorf("expected source binding differs from manifest")
		}
	} else if options.RepositoryRoot != "" && options.Lifecycle == nil {
		commit, tree, worktreeTree, statusDigest, observeErr := observeGitSource(ctx, options.RepositoryRoot)
		if observeErr != nil || commit != manifest.SourceCommit || tree != manifest.SourceTree || worktreeTree != tree || statusDigest != emptySHA256 {
			return Verification{}, manifest, fmt.Errorf("reobserve Git source binding: %w", observeErr)
		}
	} else if options.RepositoryRoot != "" || options.Lifecycle != nil {
		observed, observeErr := ObserveSource(ctx, options.RepositoryRoot, options.Lifecycle)
		if observeErr != nil || observed.Commit != manifest.SourceCommit || observed.Tree != manifest.SourceTree || observed.SidecarProvenanceSHA256 != manifest.SidecarProvenanceSHA256 {
			return Verification{}, manifest, fmt.Errorf("reobserve source binding: %w", observeErr)
		}
	}
	seenPath := make(map[string]struct{}, len(manifest.Artifacts))
	seenKind := make(map[string]int, len(manifest.Artifacts))
	seenDigest := make(map[string]int, len(manifest.Artifacts))
	var crownBytes []byte
	var sourceBytes, provenanceBytes []byte
	for _, artifact := range manifest.Artifacts {
		if !cleanArtifactPath(artifact.Path) {
			return Verification{}, manifest, fmt.Errorf("artifact path %q is unsafe", artifact.Path)
		}
		if _, duplicate := seenPath[artifact.Path]; duplicate {
			return Verification{}, manifest, fmt.Errorf("artifact path %q is duplicated", artifact.Path)
		}
		seenPath[artifact.Path] = struct{}{}
		seenKind[artifact.Kind]++
		contents, readErr := readRootRegular(root, filepath.FromSlash(artifact.Path), 16<<20)
		if readErr != nil || int64(len(contents)) != artifact.SizeBytes || SHA256Hex(contents) != artifact.SHA256 {
			return Verification{}, manifest, fmt.Errorf("artifact %q bytes differ: %w", artifact.Path, readErr)
		}
		seenDigest[artifact.SHA256]++
		if artifact.Kind == "crown-ledger" {
			crownBytes = append([]byte(nil), contents...)
		}
		if artifact.Kind == "source-binding" {
			sourceBytes = append([]byte(nil), contents...)
		}
		if artifact.Kind == "sidecar-provenance" {
			provenanceBytes = append([]byte(nil), contents...)
		}
	}
	for _, kind := range []string{"source-binding", "sidecar-provenance", "proving-binary", "contracts", "budgets", "crown-ledger"} {
		if seenKind[kind] != 1 {
			return Verification{}, manifest, fmt.Errorf("mandatory artifact kind %q count=%d want=1", kind, seenKind[kind])
		}
	}
	var sourceArtifact struct {
		Commit                  string `json:"commit"`
		Tree                    string `json:"tree"`
		WorktreeTree            string `json:"worktree_tree"`
		WorktreeStatusSHA256    string `json:"worktree_status_sha256"`
		AdmittedTupleSHA256     string `json:"admitted_tuple_sha256"`
		SidecarProvenanceSHA256 string `json:"sidecar_provenance_sha256"`
		SidecarExecutableSHA256 string `json:"sidecar_executable_sha256"`
		DatabaseID              string `json:"database_id"`
		OwnerPID                int    `json:"owner_pid"`
	}
	if err := jsonUnmarshalStrict(sourceBytes, &sourceArtifact); err != nil {
		return Verification{}, manifest, fmt.Errorf("decode source-binding artifact: %w", err)
	}
	provenanceCanonical := bytes.TrimSuffix(provenanceBytes, []byte{'\n'})
	if sourceArtifact.Commit != manifest.SourceCommit || sourceArtifact.Tree != manifest.SourceTree || sourceArtifact.WorktreeTree != manifest.SourceTree || sourceArtifact.WorktreeStatusSHA256 != emptySHA256 || sourceArtifact.AdmittedTupleSHA256 != manifest.AdmittedTupleSHA256 || sourceArtifact.SidecarProvenanceSHA256 != manifest.SidecarProvenanceSHA256 || SHA256Hex(provenanceCanonical) != manifest.SidecarProvenanceSHA256 || sourceArtifact.SidecarExecutableSHA256 != AdmittedSidecarSHA256 || sourceArtifact.DatabaseID == "" || sourceArtifact.OwnerPID <= 0 {
		if manifest.Outcome == "pass" {
			return Verification{}, manifest, fmt.Errorf("source/provenance artifact binding is invalid")
		}
	}
	for _, digest := range referencedArtifactDigests(manifest) {
		if seenDigest[digest] != 1 {
			return Verification{}, manifest, fmt.Errorf("referenced artifact %s count=%d want=1", digest, seenDigest[digest])
		}
	}
	var crown CrownArtifact
	if err := jsonUnmarshalStrict(crownBytes, &crown); err != nil {
		return Verification{}, manifest, fmt.Errorf("decode crown ledger: %w", err)
	}
	if manifest.Outcome == "pass" {
		if err := ValidateCrownArtifact(crown); err != nil {
			return Verification{}, manifest, fmt.Errorf("crown ledger is invalid: %w", err)
		}
	} else if crown.SchemaVersion != 1 || crown.RunID != manifest.RunID || crown.Trial.RunID != manifest.RunID || crown.Trial.Outcome == "pass" {
		return Verification{}, manifest, fmt.Errorf("incomplete crown ledger identity/outcome is invalid")
	}
	for _, segment := range crown.Segments {
		if seenDigest[segment.ArtifactSHA256] != 1 {
			return Verification{}, manifest, fmt.Errorf("crown segment %q artifact %s count=%d want=1", segment.ID, segment.ArtifactSHA256, seenDigest[segment.ArtifactSHA256])
		}
	}
	if manifest.OrderedTrial.ArtifactSHA256 != crown.Trial.ArtifactSHA256 {
		return Verification{}, manifest, fmt.Errorf("ordered trial does not bind the exact crown ledger artifact")
	}
	if !reflect.DeepEqual(crown.Trial, manifest.OrderedTrial) {
		return Verification{}, manifest, fmt.Errorf("crown ledger differs from manifest ordered trial")
	}
	semanticErr := SemanticOutcome(manifest)
	technicalBytes, signatureErr := readRootRegular(root, TechnicalPassName, 1024)
	wantTechnical := TechnicalSignature(manifestDigest)
	if manifest.Outcome == "pass" {
		if semanticErr != nil {
			return Verification{}, manifest, semanticErr
		}
		if signatureErr != nil || strings.TrimSpace(string(technicalBytes)) != wantTechnical {
			return Verification{}, manifest, fmt.Errorf("technical pass signature is absent or invalid")
		}
	} else {
		if signatureErr == nil || !errors.Is(signatureErr, os.ErrNotExist) {
			return Verification{}, manifest, fmt.Errorf("non-pass evidence carries a technical pass signature")
		}
	}
	return Verification{Outcome: manifest.Outcome, RunID: manifest.RunID, ManifestSHA256: manifestDigest, TechnicalPass: strings.TrimSpace(string(technicalBytes)), AdoptionEligible: manifest.Outcome == "pass" && semanticErr == nil}, manifest, nil
}

func referencedArtifactDigests(manifest EvidenceManifest) []string {
	result := make([]string, 0, len(manifest.ScenarioResults)+len(manifest.FaultResults)+len(manifest.FocusedSubtrialSummaries)+len(manifest.MutantResults)+2)
	for _, item := range manifest.ScenarioResults {
		result = append(result, item.ArtifactSHA256)
	}
	for _, item := range manifest.FaultResults {
		result = append(result, item.ArtifactSHA256)
	}
	for _, item := range manifest.FocusedSubtrialSummaries {
		result = append(result, item.ArtifactSHA256)
	}
	for _, item := range manifest.MutantResults {
		result = append(result, item.ArtifactSHA256)
	}
	if manifest.IndependentReview.ArtifactSHA256 != "" {
		result = append(result, manifest.IndependentReview.ArtifactSHA256)
	}
	return result
}

func TechnicalSignature(manifestSHA string) string {
	digest := sha256.Sum256([]byte(TechnicalPassDomain + "\x00" + manifestSHA))
	return TechnicalPassDomain + ":" + hex.EncodeToString(digest[:])
}

func jsonUnmarshalStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func readRootRegular(root *os.Root, name string, maximum int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("evidence member is not a bounded regular file")
	}
	return io.ReadAll(io.LimitReader(file, maximum+1))
}

func cleanArtifactPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != filepath.FromSlash(path) || strings.Contains(path, "\\") {
		return false
	}
	parts := strings.Split(path, "/")
	return !slices.Contains(parts, "") && !slices.Contains(parts, ".") && !slices.Contains(parts, "..")
}

func safeToken(value string) string {
	var result strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			result.WriteRune(character)
		} else {
			result.WriteByte('-')
		}
	}
	if result.Len() == 0 {
		return "artifact"
	}
	return result.String()
}

func durableWrite(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".publish-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
