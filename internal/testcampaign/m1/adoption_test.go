package m1

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAdoptionRequiresVerifiedPassAndSeparateAuthorizedHumanSignature(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verification, manifest := adoptionFixture()
	template, err := AdoptionTemplate(verification, manifest)
	if err != nil || template.DetachedEd25519 == "" || template.Decision != "<adopt|refuse>" {
		t.Fatalf("template=%+v err=%v", template, err)
	}
	record := adoptionRecord(verification, manifest)
	message, err := AdoptionSigningBytes(record)
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = AdoptionSignature{Kind: "detached", Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, message))}
	verdict, err := VerifyAdoption(record, TrustedSigner{Signer: AccountableSigner, PublicKey: public}, verification, manifest, RecoveryState{true, true, true, true, true})
	if err != nil || verdict.State != AdoptionAdopted || verdict.Decision != "adopt" {
		t.Fatalf("verdict=%+v err=%v", verdict, err)
	}
	mixed := TrustedSigner{Signer: AccountableSigner, PublicKey: public, LocalApprovals: []TrustedLocalApproval{{Reference: "foreign", SigningBytesSHA256: strings.Repeat("a", 64)}}}
	if verdict, err := VerifyAdoption(record, mixed, verification, manifest, RecoveryState{true, true, true, true, true}); err == nil || verdict.State != AdoptionNotAdopted {
		t.Fatalf("mixed detached/local trust adopted: verdict=%+v err=%v", verdict, err)
	}

	t.Run("approved local reference requires exact operator trust binding", testAdoptionApprovedLocalReferenceRequiresExactOperatorTrustBinding)
	t.Run("signature kinds mirror frozen schema closed set", testAdoptionSignatureKindsMirrorFrozenSchemaClosedSet)
}

func testAdoptionApprovedLocalReferenceRequiresExactOperatorTrustBinding(t *testing.T) {
	verification, manifest := adoptionFixture()
	record := adoptionRecord(verification, manifest)
	record.Signature = AdoptionSignature{Kind: "approved-local-reference", Value: "local-approval-2026-08-31"}
	message, err := AdoptionSigningBytes(record)
	if err != nil {
		t.Fatal(err)
	}
	approval := TrustedLocalApproval{Reference: record.Signature.Value, SigningBytesSHA256: SHA256Hex(message)}
	trusted := TrustedSigner{Signer: AccountableSigner, LocalApprovals: []TrustedLocalApproval{approval}}
	ready := RecoveryState{true, true, true, true, true}
	verdict, err := VerifyAdoption(record, trusted, verification, manifest, ready)
	if err != nil || verdict.State != AdoptionAdopted || verdict.Signer != AccountableSigner {
		t.Fatalf("local-reference verdict=%+v err=%v", verdict, err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*AdoptionRecord, *TrustedSigner)
	}{
		{"trust absent", func(_ *AdoptionRecord, trust *TrustedSigner) { trust.LocalApprovals = nil }},
		{"foreign reference", func(candidate *AdoptionRecord, _ *TrustedSigner) {
			candidate.Signature.Value = "foreign-local-approval"
		}},
		{"foreign envelope", func(_ *AdoptionRecord, trust *TrustedSigner) {
			trust.LocalApprovals[0].SigningBytesSHA256 = strings.Repeat("9", 64)
		}},
		{"duplicate trust", func(_ *AdoptionRecord, trust *TrustedSigner) {
			trust.LocalApprovals = append(trust.LocalApprovals, trust.LocalApprovals[0])
		}},
		{"mixed detached trust", func(_ *AdoptionRecord, trust *TrustedSigner) {
			trust.PublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
		}},
		{"post-approval rationale", func(candidate *AdoptionRecord, _ *TrustedSigner) { candidate.Rationale += " changed" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := record
			candidateTrust := trusted
			candidateTrust.LocalApprovals = append([]TrustedLocalApproval(nil), trusted.LocalApprovals...)
			test.mutate(&candidate, &candidateTrust)
			verdict, err := VerifyAdoption(candidate, candidateTrust, verification, manifest, ready)
			if err == nil || verdict.State != AdoptionNotAdopted {
				t.Fatalf("untrusted local reference adopted: verdict=%+v err=%v", verdict, err)
			}
		})
	}
}

func testAdoptionSignatureKindsMirrorFrozenSchemaClosedSet(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts", "adoption-record-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	properties := document["properties"].(map[string]any)
	signature := properties["signature"].(map[string]any)
	signatureProperties := signature["properties"].(map[string]any)
	kind := signatureProperties["kind"].(map[string]any)
	values := kind["enum"].([]any)
	got := make([]string, len(values))
	for i := range values {
		got[i] = values[i].(string)
	}
	if !slices.Equal(got, adoptionSignatureKinds) {
		t.Fatalf("verifier signature kinds=%v schema=%v", adoptionSignatureKinds, got)
	}

	verification, manifest := adoptionFixture()
	record := adoptionRecord(verification, manifest)
	record.Signature = AdoptionSignature{Kind: "unratified-reference", Value: "some-value"}
	if verdict, err := VerifyAdoption(record, TrustedSigner{Signer: AccountableSigner}, verification, manifest, RecoveryState{true, true, true, true, true}); err == nil || verdict.State != AdoptionNotAdopted {
		t.Fatalf("schema-foreign signature kind adopted: verdict=%+v err=%v", verdict, err)
	}
}

func TestAdoptionFailClosedRecoveryUnsignedWrongSignerStaleAndMutated(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verification, manifest := adoptionFixture()
	record := adoptionRecord(verification, manifest)
	message, _ := AdoptionSigningBytes(record)
	record.Signature = AdoptionSignature{Kind: "detached", Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, message))}
	ready := RecoveryState{true, true, true, true, true}
	mutants := []struct {
		name   string
		mutate func(*AdoptionRecord, *TrustedSigner, *Verification, *EvidenceManifest, *RecoveryState)
	}{
		{"byte-only recovery", func(_ *AdoptionRecord, _ *TrustedSigner, _ *Verification, _ *EvidenceManifest, r *RecoveryState) {
			r.ProductRestored = false
		}},
		{"unsigned", func(r *AdoptionRecord, _ *TrustedSigner, _ *Verification, _ *EvidenceManifest, _ *RecoveryState) {
			r.Signature.Value = ""
		}},
		{"wrong signer", func(_ *AdoptionRecord, s *TrustedSigner, _ *Verification, _ *EvidenceManifest, _ *RecoveryState) {
			s.Signer = "github:mallory"
		}},
		{"stale evidence", func(r *AdoptionRecord, _ *TrustedSigner, _ *Verification, _ *EvidenceManifest, _ *RecoveryState) {
			r.EvidenceManifestSHA256 = strings.Repeat("9", 64)
		}},
		{"changed source", func(r *AdoptionRecord, _ *TrustedSigner, _ *Verification, _ *EvidenceManifest, _ *RecoveryState) {
			r.SourceTree = strings.Repeat("8", 40)
		}},
		{"post-signature decision", func(r *AdoptionRecord, _ *TrustedSigner, _ *Verification, _ *EvidenceManifest, _ *RecoveryState) {
			r.Decision = "refuse"
		}},
		{"incomplete evidence", func(_ *AdoptionRecord, _ *TrustedSigner, v *Verification, _ *EvidenceManifest, _ *RecoveryState) {
			v.AdoptionEligible = false
		}},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			candidate, signer, v, m, recovery := record, TrustedSigner{Signer: AccountableSigner, PublicKey: public}, verification, manifest, ready
			mutant.mutate(&candidate, &signer, &v, &m, &recovery)
			verdict, err := VerifyAdoption(candidate, signer, v, m, recovery)
			if err == nil || verdict.State != AdoptionNotAdopted {
				t.Fatalf("mutant adopted: verdict=%+v err=%v", verdict, err)
			}
		})
	}
}

func TestAdoptionRefusalIsValidButNeverAdopted(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	verification, manifest := adoptionFixture()
	record := adoptionRecord(verification, manifest)
	record.Decision = "refuse"
	message, _ := AdoptionSigningBytes(record)
	record.Signature = AdoptionSignature{Kind: "detached", Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, message))}
	verdict, err := VerifyAdoption(record, TrustedSigner{Signer: AccountableSigner, PublicKey: public}, verification, manifest, RecoveryState{true, true, true, true, true})
	if err != nil || verdict.State != AdoptionNotAdopted || verdict.Decision != "refuse" {
		t.Fatalf("verdict=%+v err=%v", verdict, err)
	}
}

func adoptionFixture() (Verification, EvidenceManifest) {
	manifest := EvidenceManifest{RunID: "run-adoption", SourceCommit: strings.Repeat("1", 40), SourceTree: strings.Repeat("2", 40), AdmittedTupleSHA256: AdmittedTupleSHA256, Outcome: "pass"}
	return Verification{Outcome: "pass", RunID: manifest.RunID, ManifestSHA256: strings.Repeat("3", 64), AdoptionEligible: true}, manifest
}

func adoptionRecord(verification Verification, manifest EvidenceManifest) AdoptionRecord {
	return AdoptionRecord{SchemaVersion: 1, ProgramID: "production-runtime", MissionID: "M1", Decision: "adopt", EvidenceManifestSHA256: verification.ManifestSHA256, AdmittedTupleSHA256: AdmittedTupleSHA256, SourceCommit: manifest.SourceCommit, SourceTree: manifest.SourceTree, SidecarSHA256: AdmittedSidecarSHA256, Signer: AccountableSigner, Rationale: "I accept only this exact M1 evidence tuple.", SignedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)}
}
