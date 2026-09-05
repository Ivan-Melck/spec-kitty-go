package gapdb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

const (
	AdoptedM1SourceCommit     = "e05a5e46feb3e65ff42c4544c16678c440ef3818"
	AdoptedM1SourceTree       = "c7b932b3d85833beee1a84d8a1ae7f4e4a75ec73"
	AdoptedEvidenceManifest   = "397f93a9b0f1ea24715ba57b0cb58aeb489e13a7e27e8bc76259597f36f2f309"
	AdoptedEvidenceRelease    = "m1-gapdb-evidence-v1"
	AdoptedSigner             = "github:lynncoleart"
	AdoptedSignedAt           = "2026-09-02T10:46:41Z"
	AdoptedSignatureKind      = "approved-local-reference"
	AdoptedSignatureValue     = "conversation-approval:2026-09-02T10:46:41Z"
	AdoptedSigningEnvelopeSHA = "fefefdbbebdd390ce6ce48ccaa6d97fc9b271fedbfdd5180dc82d69c9d35dc55"
	adoptionSigningDomain     = "spec-kitty.m1.adoption.v1"
	adoptedRationale          = "Human approval supplied in-session: \"yes! that would be awesome. please continue\" in direct response to the proposed exact-evidence GapDB adoption decision followed by M2."
	maximumAdoptionRecordSize = 64 << 10
)

// AdoptionRecordV1 is the independently approved M1 decision. It is distinct
// from the provider tuple record consumed by VerifyExecutable.
type AdoptionRecordV1 struct {
	SchemaVersion          uint32 `json:"schema_version"`
	ProgramID              string `json:"program_id"`
	MissionID              string `json:"mission_id"`
	Decision               string `json:"decision"`
	EvidenceManifestSHA256 string `json:"evidence_manifest_sha256"`
	AdmittedTupleSHA256    string `json:"admitted_tuple_sha256"`
	SourceCommit           string `json:"source_commit"`
	SourceTree             string `json:"source_tree"`
	SidecarSHA256          string `json:"sidecar_sha256"`
	Signer                 string `json:"signer"`
	Rationale              string `json:"rationale"`
	SignedAt               string `json:"signed_at"`
	Signature              struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"signature"`
}

// VerifyAdoptionRecord reads an exact no-symlink record and rejects every
// coordinate independently. It returns a value copy, never raw record bytes.
func VerifyAdoptionRecord(path string) (AdoptionRecordV1, error) {
	if err := validateAbsolutePath(path); err != nil {
		return AdoptionRecordV1{}, failure(FailureInvalidConfiguration, "verify_adoption", "adoption path is not absolute and normalized", err)
	}
	if err := validateRecordPath(path); err != nil {
		return AdoptionRecordV1{}, failure(FailureUnsafePath, "verify_adoption", "adoption path ownership or mode is unsafe", err)
	}
	raw, _, err := readExactNoFollow(path, maximumAdoptionRecordSize)
	if err != nil {
		return AdoptionRecordV1{}, failure(FailureAdmissionMismatch, "verify_adoption", "adoption record cannot be read", err)
	}
	var record AdoptionRecordV1
	if !singleJSONValue(raw, &record) {
		return AdoptionRecordV1{}, failure(FailureAdmissionMismatch, "verify_adoption", "adoption record is malformed", nil)
	}
	if record.SchemaVersion != 1 || record.ProgramID != "production-runtime" || record.MissionID != "M1" || record.Decision != "adopt" ||
		record.EvidenceManifestSHA256 != AdoptedEvidenceManifest || record.AdmittedTupleSHA256 != storegapdb.AdmittedTupleSHA256 ||
		record.SourceCommit != AdoptedM1SourceCommit || record.SourceTree != AdoptedM1SourceTree || record.SidecarSHA256 != storegapdb.AdmittedSidecarSHA256 ||
		record.Signer != AdoptedSigner || record.Rationale != adoptedRationale || record.SignedAt != AdoptedSignedAt ||
		record.Signature.Kind != AdoptedSignatureKind || record.Signature.Value != AdoptedSignatureValue {
		return AdoptionRecordV1{}, failure(FailureAdmissionMismatch, "verify_adoption", "adoption coordinates differ", nil)
	}
	digest, err := adoptionSigningEnvelopeDigest(record)
	if err != nil || digest != AdoptedSigningEnvelopeSHA {
		return AdoptionRecordV1{}, failure(FailureAdmissionMismatch, "verify_adoption", "adoption signing envelope differs", err)
	}
	return record, nil
}

// adoptionSigningEnvelopeDigest reproduces the M1 signing contract without
// importing the proving-ground package into production. The trailing newline
// is part of that frozen contract.
func adoptionSigningEnvelopeDigest(record AdoptionRecordV1) (string, error) {
	signedAt, err := time.Parse(time.RFC3339, record.SignedAt)
	if err != nil {
		return "", err
	}
	envelope := struct {
		EnvelopeVersion        int       `json:"envelope_version"`
		SigningDomain          string    `json:"signing_domain"`
		ProgramID              string    `json:"program_id"`
		MissionID              string    `json:"mission_id"`
		Decision               string    `json:"decision"`
		EvidenceManifestSHA256 string    `json:"evidence_manifest_sha256"`
		AdmittedTupleSHA256    string    `json:"admitted_tuple_sha256"`
		SourceCommit           string    `json:"source_commit"`
		SourceTree             string    `json:"source_tree"`
		SidecarSHA256          string    `json:"sidecar_sha256"`
		Signer                 string    `json:"signer"`
		Rationale              string    `json:"rationale"`
		SignedAt               time.Time `json:"signed_at"`
	}{1, adoptionSigningDomain, record.ProgramID, record.MissionID, record.Decision, record.EvidenceManifestSHA256, record.AdmittedTupleSHA256, record.SourceCommit, record.SourceTree, record.SidecarSHA256, record.Signer, record.Rationale, signedAt.UTC()}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func singleJSONValue(raw []byte, target *AdoptionRecordV1) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF
}
