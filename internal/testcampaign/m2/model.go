package m2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const (
	SchemaVersion = 1
	ProgramID     = "production-runtime"
	MissionID     = "M2"
)

type Source struct {
	Commit      string `json:"commit"`
	Tree        string `json:"tree"`
	GoVersion   string `json:"go_version"`
	GoModSHA256 string `json:"go_mod_sha256"`
	GoSumSHA256 string `json:"go_sum_sha256"`
}

type M1Admission struct {
	AdoptionSHA256      string `json:"adoption_sha256"`
	EvidenceSHA256      string `json:"evidence_manifest_sha256"`
	AdmittedTupleSHA256 string `json:"admitted_tuple_sha256"`
	SidecarSHA256       string `json:"sidecar_sha256"`
}

type CensusSummary struct {
	AuthorityFamiliesTotal  int `json:"authority_families_total"`
	AuthorityFamiliesPassed int `json:"authority_families_passed"`
	BehaviorsTotal          int `json:"behaviors_total"`
	BehaviorsPassed         int `json:"behaviors_passed"`
	FaultCutsTotal          int `json:"fault_cuts_total"`
	FaultCutsPassed         int `json:"fault_cuts_passed"`
	MutantsTotal            int `json:"mutants_total"`
	MutantsKilled           int `json:"mutants_killed"`
	SQLiteEdges             int `json:"sqlite_edges"`
}

type Performance struct {
	RecoveryOperations   int     `json:"recovery_operations"`
	RecoveryRuns         int     `json:"recovery_runs"`
	RecoveryP95MS        float64 `json:"recovery_p95_ms"`
	QueryObservations    int     `json:"query_observations"`
	QueryP95MS           float64 `json:"query_p95_ms"`
	MutationObservations int     `json:"mutation_observations"`
	MutationP95MS        float64 `json:"mutation_p95_ms"`
}

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

type Manifest struct {
	SchemaVersion int           `json:"schema_version"`
	ProgramID     string        `json:"program_id"`
	MissionID     string        `json:"mission_id"`
	RunID         string        `json:"run_id"`
	Source        Source        `json:"source"`
	M1Admission   M1Admission   `json:"m1_admission"`
	Census        CensusSummary `json:"census"`
	Performance   Performance   `json:"performance"`
	Artifacts     []Artifact    `json:"artifacts"`
	Verdict       string        `json:"verdict"`
}

type CensusEntry struct {
	ID                  string `json:"id"`
	Category            string `json:"category"`
	OwnerWP             string `json:"owner_wp"`
	Package             string `json:"package"`
	Selector            string `json:"selector"`
	ExpectedRepetitions int    `json:"expected_repetitions"`
	TrialsPerRun        int    `json:"trials_per_run"`
	ObservedCount       int    `json:"observed_count"`
	PassedCount         int    `json:"passed_count"`
	FailedCount         int    `json:"failed_count"`
	SkippedCount        int    `json:"skipped_count"`
	Substituted         bool   `json:"substituted"`
	ArtifactPath        string `json:"artifact_path"`
	ArtifactSHA256      string `json:"artifact_sha256"`
}

type Census struct {
	SchemaVersion int           `json:"schema_version"`
	RunID         string        `json:"run_id"`
	SourceCommit  string        `json:"source_commit"`
	SourceTree    string        `json:"source_tree"`
	Entries       []CensusEntry `json:"entries"`
}

func CanonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return data, nil
}

func DecodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func SHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == hex.EncodeToString(decoded)
}
