package m1

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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
