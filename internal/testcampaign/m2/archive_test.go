package m2

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentReviewIsSeparateActorAndOuterArchiveRecomputesEverything(t *testing.T) {
	manifest, census, inputs := completeFixture(t)
	output := t.TempDir()
	publication, err := Publish(output, manifest, census, inputs)
	if err != nil {
		t.Fatal(err)
	}
	reviewRoot := t.TempDir()
	commandOutput := []byte("{\"outcome\":\"pass\"}\n")
	if err := os.Mkdir(filepath.Join(reviewRoot, "commands"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reviewRoot, "commands", "verify.jsonl"), commandOutput, 0o400); err != nil {
		t.Fatal(err)
	}
	review := IndependentReviewReceipt{
		SchemaVersion: 1, RunID: manifest.RunID, SourceCommit: manifest.Source.Commit, SourceTree: manifest.Source.Tree,
		ManifestSHA256: publication.ManifestSHA256, ProducerArchiveSHA: publication.ProducerArchiveSHA256,
		ImplementationActor: "implementer", ReviewerActor: "reviewer", ReviewerProcess: "review-process", ReviewerProfile: "reviewer-renata", ProfileRevision: stringOf('f', 64),
		Commands: []ReviewCommandReceipt{{ID: "verify", Argv: []string{"spk-m2-qualification", "verify"}, ExitCode: 0, OutputPath: "commands/verify.jsonl", OutputSHA256: SHA256(commandOutput)}},
		Verdict:  "pass",
	}
	reviewBytes, _ := CanonicalJSON(review)
	if err := os.WriteFile(filepath.Join(reviewRoot, "review.json"), reviewBytes, 0o400); err != nil {
		t.Fatal(err)
	}
	release, err := FinalizeIndependentReview(output, publication.Directory, publication.ProducerArchivePath, publication.ProducerReceiptPath, reviewRoot, manifest.Source, publication.ManifestSHA256, publication.ProducerArchiveSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if !release.ReleaseReady || release.Adopted {
		t.Fatalf("release=%+v", release)
	}
	verified, err := VerifyReleaseArchive(filepath.Join(output, manifest.RunID+".release.tar"), filepath.Join(output, manifest.RunID+".release.json"), manifest.Source, publication.ManifestSHA256, publication.ProducerArchiveSHA256, release.ReleaseArchiveSHA)
	if err != nil || verified != release {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
}

func TestReleaseVerifierKillsSelfConsistentOuterMutants(t *testing.T) {
	manifest, census, inputs := completeFixture(t)
	output := t.TempDir()
	publication, err := Publish(output, manifest, census, inputs)
	if err != nil {
		t.Fatal(err)
	}
	reviewRoot := t.TempDir()
	commandOutput := []byte("pass\n")
	if err := os.WriteFile(filepath.Join(reviewRoot, "verify.out"), commandOutput, 0o400); err != nil {
		t.Fatal(err)
	}
	review := IndependentReviewReceipt{SchemaVersion: 1, RunID: manifest.RunID, SourceCommit: manifest.Source.Commit, SourceTree: manifest.Source.Tree, ManifestSHA256: publication.ManifestSHA256, ProducerArchiveSHA: publication.ProducerArchiveSHA256, ImplementationActor: "implementer", ReviewerActor: "reviewer", ReviewerProcess: "p", ReviewerProfile: "reviewer-renata", ProfileRevision: "v1", Commands: []ReviewCommandReceipt{{ID: "verify", Argv: []string{"verify"}, OutputPath: "verify.out", OutputSHA256: SHA256(commandOutput)}}, Verdict: "pass"}
	reviewBytes, _ := CanonicalJSON(review)
	if err := os.WriteFile(filepath.Join(reviewRoot, "review.json"), reviewBytes, 0o400); err != nil {
		t.Fatal(err)
	}
	release, err := FinalizeIndependentReview(output, publication.Directory, publication.ProducerArchivePath, publication.ProducerReceiptPath, reviewRoot, manifest.Source, publication.ManifestSHA256, publication.ProducerArchiveSHA256)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(output, manifest.RunID+".release.tar"))
	if err != nil {
		t.Fatal(err)
	}
	members := testTarMembers(t, original)
	mutants := map[string]func([]tarMember, *ReleaseReceipt) []tarMember{
		"extra-member": func(in []tarMember, _ *ReleaseReceipt) []tarMember {
			return append(in, tarMember{"unrecognized.txt", []byte("x")})
		},
		"inner-tamper": func(in []tarMember, r *ReleaseReceipt) []tarMember {
			for i := range in {
				if in[i].name == "producer/producer.tar" {
					in[i].data = append(bytes.Clone(in[i].data), 0)
					r.ProducerArchiveSHA = SHA256(in[i].data)
				}
			}
			return in
		},
		"swapped-review": func(in []tarMember, r *ReleaseReceipt) []tarMember {
			for i := range in {
				if in[i].name == "review/review.json" {
					var v IndependentReviewReceipt
					_ = DecodeStrict(in[i].data, &v)
					v.ReviewerActor = "implementer"
					in[i].data, _ = CanonicalJSON(v)
					r.ReviewReceiptSHA = SHA256(in[i].data)
				}
			}
			return in
		},
		"fabricated-producer": func(in []tarMember, r *ReleaseReceipt) []tarMember {
			for i := range in {
				if in[i].name == "producer/producer.tar" {
					in[i].data = []byte("fabricated")
					r.ProducerArchiveSHA = SHA256(in[i].data)
				}
			}
			return in
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			candidate := make([]tarMember, len(members))
			copy(candidate, members)
			claimed := release
			candidate = mutate(candidate, &claimed)
			archive, err := deterministicTar(candidate)
			if err != nil {
				t.Fatal(err)
			}
			claimed.ReleaseArchiveSHA = SHA256(archive)
			receipt, _ := CanonicalJSON(claimed)
			archivePath := filepath.Join(t.TempDir(), "release.tar")
			receiptPath := filepath.Join(filepath.Dir(archivePath), "release.json")
			_ = os.WriteFile(archivePath, archive, 0o400)
			_ = os.WriteFile(receiptPath, receipt, 0o400)
			if _, err := VerifyReleaseArchive(archivePath, receiptPath, manifest.Source, publication.ManifestSHA256, claimed.ProducerArchiveSHA, claimed.ReleaseArchiveSHA); err == nil {
				t.Fatal("mutated outer archive verified")
			}
		})
	}
}

func testTarMembers(t *testing.T, data []byte) []tarMember {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(data))
	var result []tarMember
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, tarMember{header.Name, contents})
	}
	return result
}

func TestIndependentReviewRejectsSameActorOpenFindingsAndMissingOutput(t *testing.T) {
	manifest, census, inputs := completeFixture(t)
	publication, err := Publish(t.TempDir(), manifest, census, inputs)
	if err != nil {
		t.Fatal(err)
	}
	commandOutput := []byte("pass\n")
	base := IndependentReviewReceipt{SchemaVersion: 1, RunID: manifest.RunID, SourceCommit: manifest.Source.Commit, SourceTree: manifest.Source.Tree, ManifestSHA256: publication.ManifestSHA256, ProducerArchiveSHA: publication.ProducerArchiveSHA256, ImplementationActor: "implementer", ReviewerActor: "reviewer", ReviewerProcess: "p", ReviewerProfile: "reviewer-renata", ProfileRevision: "v1", Commands: []ReviewCommandReceipt{{ID: "verify", Argv: []string{"verify"}, OutputPath: "verify.out", OutputSHA256: SHA256(commandOutput)}}, Verdict: "pass"}
	for name, mutate := range map[string]func(*IndependentReviewReceipt){
		"same-actor": func(value *IndependentReviewReceipt) { value.ReviewerActor = "implementer" },
		"open-high": func(value *IndependentReviewReceipt) {
			value.HighOpen = 0
			value.Findings = []ReviewFinding{{ID: "finding-1", Severity: "high", Disposition: "open", EvidencePath: "finding.json", EvidenceSHA256: SHA256([]byte("finding\n"))}}
		},
		"missing-output": func(value *IndependentReviewReceipt) {
			value.Commands = []ReviewCommandReceipt{{ID: "verify", Argv: []string{"verify"}, OutputPath: "missing.json", OutputSHA256: SHA256([]byte("missing\n"))}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			reviewRoot := t.TempDir()
			review := base
			mutate(&review)
			if err := os.WriteFile(filepath.Join(reviewRoot, "verify.out"), commandOutput, 0o400); err != nil {
				t.Fatal(err)
			}
			if name == "open-high" {
				if err := os.WriteFile(filepath.Join(reviewRoot, "finding.json"), []byte("finding\n"), 0o400); err != nil {
					t.Fatal(err)
				}
			}
			data, _ := CanonicalJSON(review)
			if err := os.WriteFile(filepath.Join(reviewRoot, "review.json"), data, 0o400); err != nil {
				t.Fatal(err)
			}
			if _, err := FinalizeIndependentReview(t.TempDir(), publication.Directory, publication.ProducerArchivePath, publication.ProducerReceiptPath, reviewRoot, manifest.Source, publication.ManifestSHA256, publication.ProducerArchiveSHA256); err == nil {
				t.Fatal("invalid review finalized")
			}
		})
	}
}
