package gapdb

import (
	"bytes"
	"encoding/json"
	"io"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

const (
	AssertionExtensionAdmissionSHA256 = "b4fdad208882a9257953582fd3d4d8533ff57945b9c358727cc748ce5600d232"
	AssertionExtensionAdoptionSHA256  = "da2180ce1373ee187bd33b6e8bfcfc942551841657149b1b99d34639141bd374"
	BaseM1AdoptionSHA256              = "b6506abc327ed2b22c6b51b4069cb0b8edb0af5ac2ea26a1aa3a33de6bca30de"
	AssertionUpstreamEvidenceSHA256   = "546e60d63f86b37a9914d2726f7babe02c30cf93141e2ebd7d0c298c384491da"
	AssertionConsumptionSHA256        = "6f4fc5925597abb3c11b2250886bdd2794d6a1657f6ea37d7778f08a3e50b8cd"
	assertionExtensionSigner          = "github:lynncoleart"
	assertionExtensionSignedAt        = "2026-09-05T13:25:05Z"
	assertionExtensionSignatureKind   = "delegated-conversation-authority"
	assertionExtensionSignatureValue  = "conversation-authorization:2026-09-05"
	assertionExtensionRationale       = "The project owner granted Codex blanket authority to make all calls needed to finish the project and explicitly required GapDB to be the production authority because a SQLite-dependent application is broken for this workload. This chained decision promotes only the independently qualified assertion extension required by the ratified M2 authority protocol; the original M1 record remains immutable."
)

// ExecutableAdmission is an exact, verified sidecar capability. Its
// coordinates are private so callers cannot construct a compatible-looking
// tuple and pass it to the lifecycle.
type ExecutableAdmission struct {
	decision       string
	recordSHA256   string
	sourceRevision string
	sourceTree     string
	moduleVersion  string
	sidecarSHA256  string
	executableSize int64
}

func historicalM1ExecutableAdmission() ExecutableAdmission {
	return ExecutableAdmission{
		decision:       "admitted",
		recordSHA256:   storegapdb.AdmittedTupleSHA256,
		sourceRevision: storegapdb.AdmittedSourceRevision,
		sourceTree:     storegapdb.AdmittedSourceTree,
		moduleVersion:  storegapdb.AdmittedModuleVersion,
		sidecarSHA256:  storegapdb.AdmittedSidecarSHA256,
		executableSize: admittedExecutableSize,
	}
}

type assertionExtensionAdoptionRecordV1 struct {
	SchemaVersion               uint32 `json:"schema_version"`
	ProgramID                   string `json:"program_id"`
	MissionID                   string `json:"mission_id"`
	Decision                    string `json:"decision"`
	BaseAdoptionSHA256          string `json:"base_adoption_sha256"`
	ExtensionAdmissionSHA256    string `json:"extension_admission_sha256"`
	UpstreamQualificationSHA256 string `json:"upstream_qualification_sha256"`
	ExternalConsumptionSHA256   string `json:"external_consumption_sha256"`
	SourceCommit                string `json:"source_commit"`
	SourceTree                  string `json:"source_tree"`
	ModuleVersion               string `json:"module_version"`
	SidecarSHA256               string `json:"sidecar_sha256"`
	SidecarSizeBytes            int64  `json:"sidecar_size_bytes"`
	Signer                      string `json:"signer"`
	Rationale                   string `json:"rationale"`
	SignedAt                    string `json:"signed_at"`
	Signature                   struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"signature"`
}

// VerifyAssertionExtensionAdoption verifies the immutable M1 decision chain,
// the separately admitted assertion extension, and every executable
// coordinate before returning an unforgeable admission value.
func VerifyAssertionExtensionAdoption(adoptionPath, admissionPath string) (ExecutableAdmission, error) {
	for _, candidate := range []string{adoptionPath, admissionPath} {
		if err := validateAbsolutePath(candidate); err != nil {
			return ExecutableAdmission{}, failure(FailureInvalidConfiguration, "verify_extension_adoption", "extension record path is not absolute and normalized", err)
		}
		if err := validateRecordPath(candidate); err != nil {
			return ExecutableAdmission{}, failure(FailureUnsafePath, "verify_extension_adoption", "extension record path ownership or mode is unsafe", err)
		}
	}
	adoptionBytes, adoptionDigest, err := readExactNoFollow(adoptionPath, maximumAdoptionRecordSize)
	if err != nil || adoptionDigest != AssertionExtensionAdoptionSHA256 {
		return ExecutableAdmission{}, failure(FailureAdmissionMismatch, "verify_extension_adoption", "extension adoption record differs", err)
	}
	var adoption assertionExtensionAdoptionRecordV1
	if !singleStrictJSONValue(adoptionBytes, &adoption) || !validAssertionExtensionAdoption(adoption) {
		return ExecutableAdmission{}, failure(FailureAdmissionMismatch, "verify_extension_adoption", "extension adoption coordinates differ", nil)
	}
	_, admissionDigest, err := readExactNoFollow(admissionPath, maximumAdoptionRecordSize)
	if err != nil || admissionDigest != AssertionExtensionAdmissionSHA256 || admissionDigest != adoption.ExtensionAdmissionSHA256 {
		return ExecutableAdmission{}, failure(FailureAdmissionMismatch, "verify_extension_adoption", "extension admission record differs", err)
	}
	return ExecutableAdmission{
		decision:       "admitted",
		recordSHA256:   admissionDigest,
		sourceRevision: storegapdb.AssertionCandidateSourceRevision,
		sourceTree:     storegapdb.AssertionCandidateSourceTree,
		moduleVersion:  storegapdb.AssertionCandidateModuleVersion,
		sidecarSHA256:  storegapdb.AssertionCandidateSidecarSHA256,
		executableSize: storegapdb.AssertionCandidateSidecarBytes,
	}, nil
}

func validAssertionExtensionAdoption(record assertionExtensionAdoptionRecordV1) bool {
	return record.SchemaVersion == 1 && record.ProgramID == "production-runtime" && record.MissionID == "M1-assertion-extension" && record.Decision == "adopt" &&
		record.BaseAdoptionSHA256 == BaseM1AdoptionSHA256 && record.ExtensionAdmissionSHA256 == AssertionExtensionAdmissionSHA256 &&
		record.UpstreamQualificationSHA256 == AssertionUpstreamEvidenceSHA256 && record.ExternalConsumptionSHA256 == AssertionConsumptionSHA256 &&
		record.SourceCommit == storegapdb.AssertionCandidateSourceRevision && record.SourceTree == storegapdb.AssertionCandidateSourceTree &&
		record.ModuleVersion == storegapdb.AssertionCandidateModuleVersion && record.SidecarSHA256 == storegapdb.AssertionCandidateSidecarSHA256 &&
		record.SidecarSizeBytes == storegapdb.AssertionCandidateSidecarBytes && record.Signer == assertionExtensionSigner &&
		record.Rationale == assertionExtensionRationale && record.SignedAt == assertionExtensionSignedAt &&
		record.Signature.Kind == assertionExtensionSignatureKind && record.Signature.Value == assertionExtensionSignatureValue
}

func singleStrictJSONValue(raw []byte, target any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF
}
