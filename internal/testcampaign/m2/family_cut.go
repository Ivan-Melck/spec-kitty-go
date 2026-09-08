package m2

import (
	"fmt"
	"slices"
)

const familyCutProofMethod = "executing-family-bound-publication-cut-receipts-v1"

type FamilyCutMatrix struct {
	SchemaVersion             int                 `json:"schema_version"`
	RunID                     string              `json:"run_id"`
	SourceCommit              string              `json:"source_commit"`
	SourceTree                string              `json:"source_tree"`
	CompositionArtifactPath   string              `json:"composition_artifact_path"`
	CompositionArtifactSHA256 string              `json:"composition_artifact_sha256"`
	CompositionSourcePath     string              `json:"composition_source_path"`
	CompositionSourceSHA256   string              `json:"composition_source_sha256"`
	BoundarySourcePath        string              `json:"boundary_source_path"`
	BoundarySourceSHA256      string              `json:"boundary_source_sha256"`
	Families                  []FamilyCutEvidence `json:"families"`
}

type FamilyCutEvidence struct {
	FamilyID             string        `json:"family_id"`
	AuthorityMode        string        `json:"authority_mode"`
	FamilyArtifactPath   string        `json:"family_artifact_path"`
	FamilyArtifactSHA256 string        `json:"family_artifact_sha256"`
	CampaignSourcePath   string        `json:"campaign_source_path,omitempty"`
	CampaignSourceSHA256 string        `json:"campaign_source_sha256,omitempty"`
	Cuts                 []CutEvidence `json:"cuts"`
}

type CutEvidence struct {
	CutID          string `json:"cut_id"`
	Applicability  string `json:"applicability"`
	Reason         string `json:"reason"`
	ProofMethod    string `json:"proof_method,omitempty"`
	ArtifactPath   string `json:"artifact_path,omitempty"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
	ObservedCount  int    `json:"observed_count,omitempty"`
}

var familyCutIDs = []string{
	"fault/cut-before-publication",
	"fault/cut-durable-before-response",
	"fault/cut-ack-before-projection",
	"fault/cut-lost-response",
	"fault/cut-malformed-ack",
	"fault/runtime-host-restart",
	"fault/external-before-call",
	"fault/external-after-success-before-observation",
	"fault/external-after-observation-before-result",
}

func BuildFamilyCutMatrix(runID string, source Source, census Census, inputs []ArtifactInput) ([]byte, error) {
	artifacts := make(map[string]Artifact, len(inputs))
	for _, input := range inputs {
		if _, duplicate := artifacts[input.Path]; duplicate {
			return nil, fmt.Errorf("family/cut input path %q is duplicated", input.Path)
		}
		artifacts[input.Path] = Artifact{Path: input.Path, Kind: input.Kind, SHA256: SHA256(input.Data)}
	}
	matrix, err := expectedFamilyCutMatrix(runID, source, census, artifacts)
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(matrix)
}

func expectedFamilyCutMatrix(runID string, source Source, census Census, artifacts map[string]Artifact) (FamilyCutMatrix, error) {
	entries := make(map[string]CensusEntry, len(census.Entries))
	for _, entry := range census.Entries {
		entries[entry.ID] = entry
	}
	composition, err := exactlyOneInputArtifact(artifacts, "campaign-structure")
	if err != nil {
		return FamilyCutMatrix{}, err
	}
	compositionSource, err := exactlyOneInputArtifact(artifacts, "campaign-contract-source")
	if err != nil {
		return FamilyCutMatrix{}, err
	}
	boundarySource, err := exactlyOneInputArtifact(artifacts, "campaign-boundary-source")
	if err != nil {
		return FamilyCutMatrix{}, err
	}
	hostSource, ok := artifacts["contracts/family-bound-publication-cuts-source.go"]
	if !ok || hostSource.Kind != "campaign-cut-source" {
		return FamilyCutMatrix{}, fmt.Errorf("family/cut matrix lacks Host campaign source")
	}
	outboxSource, ok := artifacts["contracts/outbox-bound-publication-cuts-source.go"]
	if !ok || outboxSource.Kind != "campaign-cut-source" {
		return FamilyCutMatrix{}, fmt.Errorf("family/cut matrix lacks outbox campaign source")
	}
	matrix := FamilyCutMatrix{
		SchemaVersion: 1, RunID: runID, SourceCommit: source.Commit, SourceTree: source.Tree,
		CompositionArtifactPath: composition.Path, CompositionArtifactSHA256: composition.SHA256,
		CompositionSourcePath: compositionSource.Path, CompositionSourceSHA256: compositionSource.SHA256,
		BoundarySourcePath: boundarySource.Path, BoundarySourceSHA256: boundarySource.SHA256,
	}
	for _, family := range authorityFamilies {
		entry, ok := entries[family.ID]
		if !ok {
			return FamilyCutMatrix{}, fmt.Errorf("family/cut matrix lacks family census %s", family.ID)
		}
		familyEvidence := FamilyCutEvidence{FamilyID: family.ID, AuthorityMode: familyAuthorityMode(family.ID), FamilyArtifactPath: entry.ArtifactPath, FamilyArtifactSHA256: entry.ArtifactSHA256}
		if family.ID == "family/outbox" {
			familyEvidence.CampaignSourcePath, familyEvidence.CampaignSourceSHA256 = outboxSource.Path, outboxSource.SHA256
		} else if family.ID != "family/runtime_host" {
			familyEvidence.CampaignSourcePath, familyEvidence.CampaignSourceSHA256 = hostSource.Path, hostSource.SHA256
		}
		for _, cutID := range familyCutIDs {
			cut := CutEvidence{CutID: cutID, Applicability: "not_applicable", Reason: "closed-cut-not-applicable-to-family"}
			if isExternalEffectCut(cutID) && family.ID != "family/external_effect" {
				cut = CutEvidence{CutID: cutID, Applicability: "not_applicable", Reason: "not-external-effect-family"}
			} else if family.ID == "family/runtime_host" && cutID != "fault/runtime-host-restart" {
				cut = CutEvidence{CutID: cutID, Applicability: "not_applicable", Reason: "runtime-host-bootstrap-precedes-command-plan"}
			} else if cutID == "fault/runtime-host-restart" {
				if family.ID == "family/runtime_host" {
					cut = cutEvidenceFromEntry(cutID, "bootstrap-owner-restart", familyCutProofMethod, entry)
				}
			} else if isExternalEffectCut(cutID) {
				cutEntry, exists := entries[cutID]
				if !exists {
					return FamilyCutMatrix{}, fmt.Errorf("family/cut matrix lacks external cut census %s", cutID)
				}
				cut = cutEvidenceFromEntry(cutID, "external-effect-provider-boundary", familyCutProofMethod, cutEntry)
			} else if family.ID == "family/outbox" {
				cutEntry, exists := entries["fault/outbox-bound-publication-cuts"]
				if !exists {
					return FamilyCutMatrix{}, fmt.Errorf("family/cut matrix lacks direct outbox campaign")
				}
				cut = cutEvidenceFromEntry(cutID, "direct-provider-capability-publication-boundary", familyCutProofMethod, cutEntry)
			} else {
				cutEntry, exists := entries["fault/family-bound-publication-cuts"]
				if !exists {
					return FamilyCutMatrix{}, fmt.Errorf("family/cut matrix lacks Host family-bound campaign")
				}
				cut = cutEvidenceFromEntry(cutID, "captured-real-plan-publication-boundary", familyCutProofMethod, cutEntry)
			}
			familyEvidence.Cuts = append(familyEvidence.Cuts, cut)
		}
		matrix.Families = append(matrix.Families, familyEvidence)
	}
	return matrix, nil
}

func cutEvidenceFromEntry(cutID, reason, method string, entry CensusEntry) CutEvidence {
	return CutEvidence{CutID: cutID, Applicability: "applicable", Reason: reason, ProofMethod: method, ArtifactPath: entry.ArtifactPath, ArtifactSHA256: entry.ArtifactSHA256, ObservedCount: 100}
}

func isExternalEffectCut(id string) bool {
	return id == "fault/external-before-call" || id == "fault/external-after-success-before-observation" || id == "fault/external-after-observation-before-result"
}

func VerifyFamilyCutMatrix(data []byte, manifest Manifest, census Census, artifacts map[string]Artifact) error {
	var got FamilyCutMatrix
	if err := DecodeStrict(data, &got); err != nil {
		return fmt.Errorf("decode family/cut matrix: %w", err)
	}
	want, err := expectedFamilyCutMatrix(manifest.RunID, manifest.Source, census, artifacts)
	if err != nil {
		return err
	}
	if !slices.EqualFunc(got.Families, want.Families, equalFamilyCutEvidence) || got.SchemaVersion != want.SchemaVersion || got.RunID != want.RunID || got.SourceCommit != want.SourceCommit || got.SourceTree != want.SourceTree || got.CompositionArtifactPath != want.CompositionArtifactPath || got.CompositionArtifactSHA256 != want.CompositionArtifactSHA256 || got.CompositionSourcePath != want.CompositionSourcePath || got.CompositionSourceSHA256 != want.CompositionSourceSHA256 || got.BoundarySourcePath != want.BoundarySourcePath || got.BoundarySourceSHA256 != want.BoundarySourceSHA256 {
		return fmt.Errorf("family/cut matrix is missing, reordered, or substituted")
	}
	return nil
}

func equalFamilyCutEvidence(a, b FamilyCutEvidence) bool {
	return a.FamilyID == b.FamilyID && a.AuthorityMode == b.AuthorityMode && a.FamilyArtifactPath == b.FamilyArtifactPath && a.FamilyArtifactSHA256 == b.FamilyArtifactSHA256 && a.CampaignSourcePath == b.CampaignSourcePath && a.CampaignSourceSHA256 == b.CampaignSourceSHA256 && slices.Equal(a.Cuts, b.Cuts)
}

func exactlyOneInputArtifact(artifacts map[string]Artifact, kind string) (Artifact, error) {
	var result Artifact
	count := 0
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			result, count = artifact, count+1
		}
	}
	if count != 1 {
		return Artifact{}, fmt.Errorf("family/cut matrix requires exactly one %s artifact", kind)
	}
	return result, nil
}

func familyAuthorityMode(id string) string {
	switch id {
	case "family/runtime_host":
		return "bootstrap-boundary"
	case "family/operation", "family/outbox":
		return "mandatory-plan-member"
	default:
		return "host-command-family"
	}
}
