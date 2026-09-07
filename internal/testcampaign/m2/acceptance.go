package m2

import (
	"fmt"
	"slices"
)

type AcceptanceMatrix struct {
	SchemaVersion     int                     `json:"schema_version"`
	ProgramID         string                  `json:"program_id"`
	MissionID         string                  `json:"mission_id"`
	RunID             string                  `json:"run_id"`
	SourceCommit      string                  `json:"source_commit"`
	SourceTree        string                  `json:"source_tree"`
	RequirementsTotal int                     `json:"requirements_total"`
	Requirements      []AcceptanceRequirement `json:"requirements"`
}

type AcceptanceRequirement struct {
	ID            string               `json:"id"`
	Status        string               `json:"status"`
	CoverageClass string               `json:"coverage_class"`
	Evidence      []AcceptanceEvidence `json:"evidence"`
}

type AcceptanceEvidence struct {
	CensusID string `json:"census_id,omitempty"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
}

type requirementProof struct {
	class     string
	censusIDs []string
	paths     []string
}

func BuildAcceptanceMatrix(manifest Manifest, census Census, inputs []ArtifactInput) ([]byte, error) {
	artifacts := make(map[string]string, len(inputs)+1)
	for _, input := range inputs {
		if _, exists := artifacts[input.Path]; exists {
			return nil, fmt.Errorf("duplicate acceptance evidence path %q", input.Path)
		}
		artifacts[input.Path] = SHA256(input.Data)
	}
	censusArtifacts := make(map[string]AcceptanceEvidence, len(census.Entries))
	for _, entry := range census.Entries {
		censusArtifacts[entry.ID] = AcceptanceEvidence{CensusID: entry.ID, Path: entry.ArtifactPath, SHA256: entry.ArtifactSHA256}
	}
	matrix := AcceptanceMatrix{SchemaVersion: 1, ProgramID: ProgramID, MissionID: MissionID, RunID: manifest.RunID, SourceCommit: manifest.Source.Commit, SourceTree: manifest.Source.Tree}
	for _, id := range expectedRequirementIDs() {
		proof := proofForRequirement(id)
		requirement := AcceptanceRequirement{ID: id, Status: "proven", CoverageClass: proof.class}
		if id == "SC-013" {
			requirement.Status = "review_required"
			matrix.Requirements = append(matrix.Requirements, requirement)
			continue
		}
		for _, censusID := range proof.censusIDs {
			reference, ok := censusArtifacts[censusID]
			if !ok {
				return nil, fmt.Errorf("requirement %s names absent census entry %s", id, censusID)
			}
			requirement.Evidence = append(requirement.Evidence, reference)
		}
		for _, path := range proof.paths {
			digest, ok := artifacts[path]
			if !ok {
				return nil, fmt.Errorf("requirement %s names absent artifact %s", id, path)
			}
			requirement.Evidence = append(requirement.Evidence, AcceptanceEvidence{Path: path, SHA256: digest})
		}
		if len(requirement.Evidence) == 0 {
			return nil, fmt.Errorf("requirement %s has no specific proof", id)
		}
		matrix.Requirements = append(matrix.Requirements, requirement)
	}
	matrix.RequirementsTotal = len(matrix.Requirements)
	return CanonicalJSON(matrix)
}

func VerifyAcceptanceMatrix(data []byte, manifest Manifest, census Census, artifacts map[string]Artifact) error {
	var matrix AcceptanceMatrix
	if err := DecodeStrict(data, &matrix); err != nil {
		return fmt.Errorf("decode acceptance matrix: %w", err)
	}
	ids := expectedRequirementIDs()
	if matrix.SchemaVersion != 1 || matrix.ProgramID != ProgramID || matrix.MissionID != MissionID || matrix.RunID != manifest.RunID || matrix.SourceCommit != manifest.Source.Commit || matrix.SourceTree != manifest.Source.Tree || matrix.RequirementsTotal != len(ids) || len(matrix.Requirements) != len(ids) {
		return fmt.Errorf("acceptance matrix identity or closed census differs")
	}
	censusByID := make(map[string]CensusEntry, len(census.Entries))
	for _, entry := range census.Entries {
		censusByID[entry.ID] = entry
	}
	for index, requirement := range matrix.Requirements {
		proof := proofForRequirement(ids[index])
		wantStatus := "proven"
		if requirement.ID == "SC-013" {
			wantStatus = "review_required"
		}
		if requirement.ID != ids[index] || requirement.Status != wantStatus || requirement.CoverageClass != proof.class || len(requirement.Evidence) != len(proof.censusIDs)+len(proof.paths) {
			return fmt.Errorf("acceptance mapping %d/%s is missing, reordered, generic, or substituted", index, ids[index])
		}
		for evidenceIndex, reference := range requirement.Evidence {
			if !safeRelativePath(reference.Path) || !validDigest(reference.SHA256) {
				return fmt.Errorf("acceptance reference %s/%d is unsafe", requirement.ID, evidenceIndex)
			}
			artifact, ok := artifacts[reference.Path]
			if !ok || artifact.SHA256 != reference.SHA256 {
				return fmt.Errorf("acceptance reference %s/%s is absent or changed", requirement.ID, reference.Path)
			}
			if evidenceIndex < len(proof.censusIDs) {
				wantID := proof.censusIDs[evidenceIndex]
				entry, ok := censusByID[wantID]
				if !ok || reference.CensusID != wantID || reference.Path != entry.ArtifactPath || reference.SHA256 != entry.ArtifactSHA256 {
					return fmt.Errorf("acceptance census reference %s/%s is not exact", requirement.ID, wantID)
				}
			} else if reference.CensusID != "" || reference.Path != proof.paths[evidenceIndex-len(proof.censusIDs)] {
				return fmt.Errorf("acceptance artifact reference %s/%d is substituted", requirement.ID, evidenceIndex)
			}
		}
	}
	return nil
}

func expectedRequirementIDs() []string {
	var ids []string
	for _, group := range []struct {
		prefix string
		count  int
	}{{"FR", 36}, {"NFR", 18}, {"C", 18}, {"SC", 13}} {
		for number := 1; number <= group.count; number++ {
			ids = append(ids, fmt.Sprintf("%s-%03d", group.prefix, number))
		}
	}
	return ids
}

func proofForRequirement(id string) requirementProof {
	proofs := requirementProofs()
	proof, ok := proofs[id]
	if !ok {
		panic("closed M2 requirement has no proof mapping: " + id)
	}
	return proof
}

func requirementProofs() map[string]requirementProof {
	result := make(map[string]requirementProof, 85)
	add := func(class string, ids []string, census []string, paths ...string) {
		for _, id := range ids {
			result[id] = requirementProof{class: class, censusIDs: slices.Clone(census), paths: slices.Clone(paths)}
		}
	}
	add("admission", []string{"FR-001", "C-001"}, nil, "m1-adoption.json", "m1-assertion-extension.json", "sidecar/gapdbd")
	add("authority", []string{"FR-002", "C-002", "C-005", "SC-002"}, []string{"family/mission", "family/operation", "family/runtime_host"})
	add("recovery", []string{"FR-003", "FR-020", "FR-021", "NFR-007", "NFR-008", "SC-005"}, []string{"family/runtime_host", "fault/clean-unclean-restart", "behavior/restore-deadline-runtime"})
	add("publication", []string{"FR-004", "FR-005", "FR-006", "NFR-001", "NFR-002", "SC-003"}, []string{"family/operation", "fault/family-bound-publication-cuts", "fault/outbox-bound-publication-cuts", "fault/cut-before-publication", "fault/cut-durable-before-response", "fault/cut-lost-response", "fault/cut-malformed-ack", "fault/cut-restart-reconciliation", "fault/external-before-call", "fault/external-after-success-before-observation", "fault/external-after-observation-before-result", "fault/acknowledgement-ambiguity"}, "campaigns/family-cut-applicability.json")
	add("autonomy", []string{"FR-007", "C-014", "C-015", "C-016", "SC-001"}, []string{"behavior/autonomous-capability-readiness", "behavior/two-item-restart-every-boundary", "mutant/autonomy-ceremony"})
	add("artifact", []string{"FR-008"}, []string{"family/artifact_catalog", "family/artifact_revision"})
	add("workgraph", []string{"FR-009", "FR-010"}, []string{"family/workgraph", "family/mission"})
	add("execution", []string{"FR-011", "NFR-003", "NFR-004", "SC-004"}, []string{"family/execution_slot", "fault/concurrent-claims"})
	add("workspace", []string{"FR-012", "C-011"}, []string{"family/workspace", "behavior/zombie-workspace-isolation"})
	add("handoff", []string{"FR-013"}, []string{"family/handoff_slot"})
	add("review", []string{"FR-014", "C-012"}, []string{"family/review_slot", "behavior/reviewer-independence"})
	add("integration", []string{"FR-015", "FR-016", "C-010"}, []string{"family/acceptance", "family/integration_target", "family/integration_attempt", "behavior/serialized-integration"})
	add("external_effect", []string{"FR-017", "FR-018", "C-009"}, []string{"family/external_effect", "fault/adversarial-tracker"})
	add("query", []string{"FR-019"}, []string{"family/runtime_host", "behavior/autonomous-capability-readiness"})
	add("playback", []string{"FR-022", "C-013", "NFR-005", "NFR-006", "SC-006"}, []string{"fault/clean-unclean-restart", "behavior/restore-deadline-runtime"})
	add("parity", []string{"FR-023", "FR-024", "NFR-015", "NFR-016", "SC-007"}, []string{"behavior/two-item-restart-every-boundary", "fault/adversarial-tracker"})
	add("fault", []string{"FR-025"}, []string{"fault/family-bound-publication-cuts", "fault/outbox-bound-publication-cuts", "fault/cut-before-publication", "fault/cut-durable-before-response", "fault/cut-lost-response", "fault/cut-malformed-ack", "fault/cut-restart-reconciliation", "fault/external-before-call", "fault/external-after-success-before-observation", "fault/external-after-observation-before-result", "fault/acknowledgement-ambiguity", "fault/concurrent-claims", "fault/clean-unclean-restart"}, "campaigns/family-cut-applicability.json")
	add("absence", []string{"FR-026", "FR-027", "FR-028", "FR-029", "FR-030", "FR-031", "FR-032", "C-003", "C-006", "C-007", "C-017", "C-018", "NFR-014", "SC-008", "SC-009"}, []string{"behavior/sqlite-absence", "mutant/sqlite-absence-layers"}, "absence/manifest.json")
	add("architecture", []string{"FR-033", "C-004"}, []string{"family/operation", "family/runtime_host"}, "contract-events.json")
	add("diagnostics", []string{"FR-034", "NFR-012", "NFR-013"}, []string{"behavior/autonomous-capability-readiness", "fault/adversarial-tracker"})
	add("evidence", []string{"FR-035", "NFR-018", "SC-012"}, []string{"behavior/sqlite-absence", "mutant/restore-deadline-overlay"}, "source.json", "sbom.cdx.json", "install/runtime-smoke.json", "contract-m2.json")
	add("successor", []string{"FR-036"}, nil, "successor-runtime.md", "successor-architecture.md")
	add("performance", []string{"NFR-009", "NFR-010", "NFR-011", "SC-010", "SC-011"}, nil, "performance.json")
	add("race", []string{"NFR-017"}, []string{"fault/concurrent-claims", "behavior/serialized-integration"}, "race.json")
	add("governance", []string{"C-008"}, nil, "m1-adoption.json", "source.json")
	add("independent_review", []string{"SC-013"}, nil)
	return result
}
