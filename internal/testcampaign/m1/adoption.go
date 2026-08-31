package m1

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"time"
)

const (
	AdoptionSigningDomain = "spec-kitty.m1.adoption.v1"
	AccountableSigner     = "github:lynncoleart"
	AdoptionNotAdopted    = "not_adopted"
	AdoptionAdopted       = "adopted"
)

type RecoveryState struct {
	ByteRecovered    bool `json:"byte_recovered"`
	ProductRestored  bool `json:"product_restored"`
	ReplayEqual      bool `json:"replay_equal"`
	NewEpochAdmitted bool `json:"new_epoch_admitted"`
	OperatorReady    bool `json:"operator_ready"`
}

func (s RecoveryState) Ready() bool {
	return s.ByteRecovered && s.ProductRestored && s.ReplayEqual && s.NewEpochAdmitted && s.OperatorReady
}

type AdoptionSignature struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type AdoptionRecord struct {
	SchemaVersion          int               `json:"schema_version"`
	ProgramID              string            `json:"program_id"`
	MissionID              string            `json:"mission_id"`
	Decision               string            `json:"decision"`
	EvidenceManifestSHA256 string            `json:"evidence_manifest_sha256"`
	AdmittedTupleSHA256    string            `json:"admitted_tuple_sha256"`
	SourceCommit           string            `json:"source_commit"`
	SourceTree             string            `json:"source_tree"`
	SidecarSHA256          string            `json:"sidecar_sha256"`
	Signer                 string            `json:"signer"`
	Rationale              string            `json:"rationale"`
	SignedAt               time.Time         `json:"signed_at"`
	Signature              AdoptionSignature `json:"signature"`
}

type UnsignedAdoptionTemplate struct {
	EnvelopeVersion        int    `json:"envelope_version"`
	SigningDomain          string `json:"signing_domain"`
	ProgramID              string `json:"program_id"`
	MissionID              string `json:"mission_id"`
	EvidenceManifestSHA256 string `json:"evidence_manifest_sha256"`
	AdmittedTupleSHA256    string `json:"admitted_tuple_sha256"`
	SourceCommit           string `json:"source_commit"`
	SourceTree             string `json:"source_tree"`
	SidecarSHA256          string `json:"sidecar_sha256"`
	RequiredSigner         string `json:"required_signer"`
	Decision               string `json:"decision"`
	Rationale              string `json:"rationale"`
	SignedAt               string `json:"signed_at"`
	DetachedEd25519        string `json:"detached_ed25519"`
}

type TrustedSigner struct {
	Signer    string
	PublicKey ed25519.PublicKey
}

type AdoptionVerdict struct {
	State    string `json:"state"`
	Decision string `json:"decision,omitempty"`
	Signer   string `json:"signer,omitempty"`
}

func AdoptionTemplate(verification Verification, manifest EvidenceManifest) (UnsignedAdoptionTemplate, error) {
	if !verification.AdoptionEligible || verification.Outcome != "pass" || verification.ManifestSHA256 == "" || verification.RunID != manifest.RunID || manifest.Outcome != "pass" {
		return UnsignedAdoptionTemplate{}, fmt.Errorf("only independently verified complete pass evidence can produce an adoption template")
	}
	return UnsignedAdoptionTemplate{EnvelopeVersion: 1, SigningDomain: AdoptionSigningDomain, ProgramID: "production-runtime", MissionID: "M1", EvidenceManifestSHA256: verification.ManifestSHA256, AdmittedTupleSHA256: AdmittedTupleSHA256, SourceCommit: manifest.SourceCommit, SourceTree: manifest.SourceTree, SidecarSHA256: AdmittedSidecarSHA256, RequiredSigner: AccountableSigner, Decision: "<adopt|refuse>", Rationale: "<human rationale>", SignedAt: "<RFC3339>", DetachedEd25519: "<base64url Ed25519 signature>"}, nil
}

func AdoptionSigningBytes(record AdoptionRecord) ([]byte, error) {
	if err := validateAdoptionFields(record); err != nil {
		return nil, err
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
	}{1, AdoptionSigningDomain, record.ProgramID, record.MissionID, record.Decision, record.EvidenceManifestSHA256, record.AdmittedTupleSHA256, record.SourceCommit, record.SourceTree, record.SidecarSHA256, record.Signer, record.Rationale, record.SignedAt.UTC()}
	return CanonicalJSON(envelope)
}

func VerifyAdoption(record AdoptionRecord, trusted TrustedSigner, verification Verification, manifest EvidenceManifest, recovery RecoveryState) (AdoptionVerdict, error) {
	refused := AdoptionVerdict{State: AdoptionNotAdopted}
	if !recovery.Ready() {
		return refused, fmt.Errorf("product recovery is not operator-ready; no force-ready waiver exists")
	}
	if !verification.AdoptionEligible || verification.Outcome != "pass" || verification.RunID != manifest.RunID || manifest.Outcome != "pass" {
		return refused, fmt.Errorf("adoption evidence is not an independently verified pass")
	}
	if record.EvidenceManifestSHA256 != verification.ManifestSHA256 || record.SourceCommit != manifest.SourceCommit || record.SourceTree != manifest.SourceTree || record.AdmittedTupleSHA256 != manifest.AdmittedTupleSHA256 || record.SidecarSHA256 != AdmittedSidecarSHA256 {
		return refused, fmt.Errorf("adoption tuple is stale or foreign")
	}
	if trusted.Signer != AccountableSigner || record.Signer != trusted.Signer || len(trusted.PublicKey) != ed25519.PublicKeySize {
		return refused, fmt.Errorf("trusted signer configuration is absent or unauthorized")
	}
	if record.Signature.Kind != "detached" || record.Signature.Value == "" {
		return refused, fmt.Errorf("detached Ed25519 signature is absent")
	}
	signature, err := base64.RawURLEncoding.DecodeString(record.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return refused, fmt.Errorf("detached Ed25519 signature is malformed")
	}
	message, err := AdoptionSigningBytes(record)
	if err != nil {
		return refused, err
	}
	if !ed25519.Verify(trusted.PublicKey, message, signature) {
		return refused, fmt.Errorf("detached Ed25519 signature is invalid")
	}
	state := AdoptionNotAdopted
	if record.Decision == "adopt" {
		state = AdoptionAdopted
	}
	return AdoptionVerdict{State: state, Decision: record.Decision, Signer: record.Signer}, nil
}

func validateAdoptionFields(record AdoptionRecord) error {
	if record.SchemaVersion != 1 || record.ProgramID != "production-runtime" || record.MissionID != "M1" || (record.Decision != "adopt" && record.Decision != "refuse") || !isSHA256String(record.EvidenceManifestSHA256) || record.AdmittedTupleSHA256 != AdmittedTupleSHA256 || !hexSized(record.SourceCommit, 40) || !hexSized(record.SourceTree, 40) || record.SidecarSHA256 != AdmittedSidecarSHA256 || record.Signer != AccountableSigner || record.Rationale == "" || record.SignedAt.IsZero() {
		return fmt.Errorf("adoption signing envelope is incomplete or outside the admitted tuple")
	}
	return nil
}
