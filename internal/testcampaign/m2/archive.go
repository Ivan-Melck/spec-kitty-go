package m2

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type ProducerIdentity struct {
	SchemaVersion              int      `json:"schema_version"`
	ExecutorActorID            string   `json:"executor_actor_id"`
	ProcessID                  string   `json:"process_id"`
	ImplementationContributors []string `json:"implementation_contributors"`
	ProfileID                  string   `json:"profile_id"`
	ProfileRevision            string   `json:"profile_revision"`
}

type ProducerArchiveReceipt struct {
	SchemaVersion  int    `json:"schema_version"`
	RunID          string `json:"run_id"`
	SourceCommit   string `json:"source_commit"`
	SourceTree     string `json:"source_tree"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ArchiveSHA256  string `json:"archive_sha256"`
	Members        int    `json:"members"`
}

type IndependentReviewReceipt struct {
	SchemaVersion              int                    `json:"schema_version"`
	RunID                      string                 `json:"run_id"`
	SourceCommit               string                 `json:"source_commit"`
	SourceTree                 string                 `json:"source_tree"`
	ManifestSHA256             string                 `json:"manifest_sha256"`
	ProducerArchiveSHA         string                 `json:"producer_archive_sha256"`
	ImplementationContributors []string               `json:"implementation_contributors"`
	ReviewerActor              string                 `json:"reviewer_actor_id"`
	ReviewerProcess            string                 `json:"reviewer_process_id"`
	ReviewerProfile            string                 `json:"reviewer_profile_id"`
	ProfileRevision            string                 `json:"profile_revision"`
	Commands                   []ReviewCommandReceipt `json:"commands"`
	Findings                   []ReviewFinding        `json:"findings"`
	CriticalOpen               int                    `json:"critical_open"`
	HighOpen                   int                    `json:"high_open"`
	Verdict                    string                 `json:"verdict"`
}

type ReviewCommandReceipt struct {
	ID           string   `json:"id"`
	Argv         []string `json:"argv"`
	ExitCode     int      `json:"exit_code"`
	OutputPath   string   `json:"output_path"`
	OutputSHA256 string   `json:"output_sha256"`
}

type ReviewFinding struct {
	ID             string `json:"id"`
	Severity       string `json:"severity"`
	Disposition    string `json:"disposition"`
	EvidencePath   string `json:"evidence_path"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

type ReleaseReceipt struct {
	SchemaVersion      int    `json:"schema_version"`
	RunID              string `json:"run_id"`
	SourceCommit       string `json:"source_commit"`
	SourceTree         string `json:"source_tree"`
	ManifestSHA256     string `json:"manifest_sha256"`
	ProducerArchiveSHA string `json:"producer_archive_sha256"`
	ReviewReceiptSHA   string `json:"review_receipt_sha256"`
	ReleaseArchiveSHA  string `json:"release_archive_sha256"`
	ReviewEligible     bool   `json:"review_eligible"`
	ReleaseReady       bool   `json:"release_ready"`
	Adopted            bool   `json:"adopted"`
}

func VerifyProducerArchive(evidenceDirectory, archivePath, receiptPath string, expected Source, expectedManifestSHA, expectedArchiveSHA string) (Verification, ProducerArchiveReceipt, error) {
	verification, _, err := Verify(context.Background(), evidenceDirectory, filepath.Join(evidenceDirectory, "contract-m2.json"), &expected)
	if err != nil {
		return Verification{}, ProducerArchiveReceipt{}, err
	}
	manifestBytes, err := os.ReadFile(filepath.Join(evidenceDirectory, ManifestName))
	if err != nil {
		return Verification{}, ProducerArchiveReceipt{}, err
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		return Verification{}, ProducerArchiveReceipt{}, err
	}
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		return Verification{}, ProducerArchiveReceipt{}, err
	}
	var receipt ProducerArchiveReceipt
	if err := DecodeStrict(receiptBytes, &receipt); err != nil {
		return Verification{}, receipt, err
	}
	if receipt.SchemaVersion != 1 || receipt.RunID != verification.RunID || receipt.SourceCommit != expected.Commit || receipt.SourceTree != expected.Tree || receipt.ManifestSHA256 != SHA256(manifestBytes) || receipt.ArchiveSHA256 != SHA256(archiveBytes) || receipt.ManifestSHA256 != expectedManifestSHA || receipt.ArchiveSHA256 != expectedArchiveSHA {
		return Verification{}, receipt, fmt.Errorf("producer archive receipt identity differs")
	}
	rebuilt, members, err := BuildDeterministicArchive(evidenceDirectory)
	if err != nil || !bytes.Equal(rebuilt, archiveBytes) || members != receipt.Members {
		return Verification{}, receipt, fmt.Errorf("producer archive is not the deterministic evidence archive")
	}
	return verification, receipt, nil
}

func FinalizeIndependentReview(outputRoot, evidenceDirectory, producerArchivePath, producerReceiptPath, reviewBundle string, expected Source, expectedManifestSHA, expectedArchiveSHA string) (ReleaseReceipt, error) {
	verification, producer, err := VerifyProducerArchive(evidenceDirectory, producerArchivePath, producerReceiptPath, expected, expectedManifestSHA, expectedArchiveSHA)
	if err != nil || !verification.ReviewEligible {
		return ReleaseReceipt{}, fmt.Errorf("producer evidence is not review eligible: %w", err)
	}
	producerIdentityBytes, err := os.ReadFile(filepath.Join(evidenceDirectory, "producer-identity.json"))
	if err != nil {
		return ReleaseReceipt{}, err
	}
	var implementation ProducerIdentity
	if err := DecodeStrict(producerIdentityBytes, &implementation); err != nil {
		return ReleaseReceipt{}, err
	}
	reviewReceiptPath := filepath.Join(reviewBundle, "review.json")
	reviewBytes, err := os.ReadFile(reviewReceiptPath)
	if err != nil {
		return ReleaseReceipt{}, err
	}
	var review IndependentReviewReceipt
	if err := DecodeStrict(reviewBytes, &review); err != nil {
		return ReleaseReceipt{}, err
	}
	if review.SchemaVersion != 1 || review.RunID != producer.RunID || review.SourceCommit != expected.Commit || review.SourceTree != expected.Tree || review.ManifestSHA256 != expectedManifestSHA || review.ProducerArchiveSHA != expectedArchiveSHA || !slices.Equal(review.ImplementationContributors, implementation.ImplementationContributors) || review.ReviewerActor == "" || slices.Contains(implementation.ImplementationContributors, review.ReviewerActor) || review.ReviewerProcess == "" || review.ReviewerProfile == "" || review.ProfileRevision == "" || len(review.Commands) == 0 || review.Verdict != "pass" {
		return ReleaseReceipt{}, fmt.Errorf("independent review receipt is absent, non-independent, mismatched, or non-pass")
	}
	reviewMembers, criticalOpen, highOpen, err := verifyReviewBundle(reviewBundle, review)
	if err != nil || criticalOpen != 0 || highOpen != 0 || review.CriticalOpen != criticalOpen || review.HighOpen != highOpen {
		return ReleaseReceipt{}, fmt.Errorf("review evidence or recomputed findings do not permit release: %w", err)
	}
	producerArchive, err := os.ReadFile(producerArchivePath)
	if err != nil {
		return ReleaseReceipt{}, err
	}
	producerReceiptBytes, err := os.ReadFile(producerReceiptPath)
	if err != nil {
		return ReleaseReceipt{}, err
	}
	releaseMembers := []tarMember{{"producer/producer.tar", producerArchive}, {"producer/producer.sha256.json", producerReceiptBytes}}
	for _, member := range reviewMembers {
		releaseMembers = append(releaseMembers, tarMember{"review/" + member.name, member.data})
	}
	finalMatrix, err := buildFinalAcceptanceMatrix(filepath.Join(evidenceDirectory, "acceptance-matrix.json"), reviewBytes)
	if err != nil {
		return ReleaseReceipt{}, err
	}
	releaseMembers = append(releaseMembers, tarMember{"final-acceptance-matrix.json", finalMatrix})
	releasePayload, err := deterministicTar(releaseMembers)
	if err != nil {
		return ReleaseReceipt{}, err
	}
	release := ReleaseReceipt{SchemaVersion: 1, RunID: producer.RunID, SourceCommit: expected.Commit, SourceTree: expected.Tree, ManifestSHA256: expectedManifestSHA, ProducerArchiveSHA: expectedArchiveSHA, ReviewReceiptSHA: SHA256(reviewBytes), ReleaseArchiveSHA: SHA256(releasePayload), ReviewEligible: true, ReleaseReady: true, Adopted: false}
	releaseBytes, err := CanonicalJSON(release)
	if err != nil {
		return ReleaseReceipt{}, err
	}
	if err := writeCreateOnce(filepath.Join(outputRoot, producer.RunID+".release.tar"), releasePayload); err != nil {
		return ReleaseReceipt{}, err
	}
	if err := writeCreateOnce(filepath.Join(outputRoot, producer.RunID+".release.json"), releaseBytes); err != nil {
		return ReleaseReceipt{}, err
	}
	verified, err := VerifyReleaseArchive(filepath.Join(outputRoot, producer.RunID+".release.tar"), filepath.Join(outputRoot, producer.RunID+".release.json"), expected, expectedManifestSHA, expectedArchiveSHA, release.ReleaseArchiveSHA)
	if err != nil || verified != release {
		return ReleaseReceipt{}, fmt.Errorf("published release archive did not independently verify: %w", err)
	}
	return release, nil
}

func VerifyReleaseArchive(archivePath, receiptPath string, expected Source, expectedManifestSHA, expectedProducerSHA, expectedReleaseSHA string) (ReleaseReceipt, error) {
	var receipt ReleaseReceipt
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		return receipt, err
	}
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		return receipt, err
	}
	if err := DecodeStrict(receiptBytes, &receipt); err != nil {
		return receipt, err
	}
	if receipt.SchemaVersion != 1 || receipt.SourceCommit != expected.Commit || receipt.SourceTree != expected.Tree || !receipt.ReviewEligible || !receipt.ReleaseReady || receipt.Adopted || receipt.ManifestSHA256 != expectedManifestSHA || receipt.ProducerArchiveSHA != expectedProducerSHA || receipt.ReleaseArchiveSHA != expectedReleaseSHA || receipt.ReleaseArchiveSHA != SHA256(archiveBytes) || !validDigest(receipt.ReviewReceiptSHA) {
		return receipt, fmt.Errorf("release receipt identity or verdict is invalid")
	}
	reader := tar.NewReader(bytes.NewReader(archiveBytes))
	members := map[string][]byte{}
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return receipt, err
		}
		if !safeRelativePath(header.Name) || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maximumFile || members[header.Name] != nil {
			return receipt, fmt.Errorf("release archive contains unsafe, duplicate, or oversized member")
		}
		total += header.Size
		if total > maximumTotal {
			return receipt, fmt.Errorf("release archive exceeds total budget")
		}
		data, err := io.ReadAll(io.LimitReader(reader, maximumFile+1))
		if err != nil || int64(len(data)) != header.Size {
			return receipt, fmt.Errorf("release archive member is truncated")
		}
		members[header.Name] = data
	}
	for _, required := range []string{"producer/producer.tar", "producer/producer.sha256.json", "review/review.json", "final-acceptance-matrix.json"} {
		if members[required] == nil {
			return receipt, fmt.Errorf("release archive is missing %s", required)
		}
	}
	if SHA256(members["producer/producer.tar"]) != receipt.ProducerArchiveSHA || SHA256(members["review/review.json"]) != receipt.ReviewReceiptSHA {
		return receipt, fmt.Errorf("release archive nested digests differ")
	}
	workspace, err := os.MkdirTemp("", "spk-m2-release-verify-")
	if err != nil {
		return receipt, err
	}
	defer os.RemoveAll(workspace)
	evidenceRoot := filepath.Join(workspace, receipt.RunID)
	if err := extractBoundedTar(members["producer/producer.tar"], evidenceRoot); err != nil {
		return receipt, err
	}
	producerArchivePath := filepath.Join(workspace, "producer.tar")
	producerReceiptPath := filepath.Join(workspace, "producer.sha256.json")
	if err := os.WriteFile(producerArchivePath, members["producer/producer.tar"], 0o400); err != nil {
		return receipt, err
	}
	if err := os.WriteFile(producerReceiptPath, members["producer/producer.sha256.json"], 0o400); err != nil {
		return receipt, err
	}
	manifestBytes, err := os.ReadFile(filepath.Join(evidenceRoot, ManifestName))
	if err != nil {
		return receipt, err
	}
	var innerManifest Manifest
	if err := DecodeStrict(manifestBytes, &innerManifest); err != nil || innerManifest.Source.Commit != expected.Commit || innerManifest.Source.Tree != expected.Tree {
		return receipt, fmt.Errorf("nested producer source differs from expected commit/tree")
	}
	expectedFullSource := innerManifest.Source
	if _, producer, err := VerifyProducerArchive(evidenceRoot, producerArchivePath, producerReceiptPath, expectedFullSource, expectedManifestSHA, expectedProducerSHA); err != nil || producer.RunID != receipt.RunID {
		return receipt, fmt.Errorf("nested producer evidence did not verify: %w", err)
	}
	reviewRoot := filepath.Join(workspace, "review")
	if err := os.Mkdir(reviewRoot, 0o700); err != nil {
		return receipt, err
	}
	expectedOuter := map[string]bool{"producer/producer.tar": true, "producer/producer.sha256.json": true, "final-acceptance-matrix.json": true}
	for name, data := range members {
		if !strings.HasPrefix(name, "review/") {
			continue
		}
		relative := strings.TrimPrefix(name, "review/")
		if !safeRelativePath(relative) {
			return receipt, fmt.Errorf("review archive path is unsafe")
		}
		path := filepath.Join(reviewRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return receipt, err
		}
		if err := os.WriteFile(path, data, 0o400); err != nil {
			return receipt, err
		}
		expectedOuter[name] = true
	}
	var review IndependentReviewReceipt
	if err := DecodeStrict(members["review/review.json"], &review); err != nil {
		return receipt, err
	}
	producerIdentityBytes, err := os.ReadFile(filepath.Join(evidenceRoot, "producer-identity.json"))
	if err != nil {
		return receipt, err
	}
	var producerIdentity ProducerIdentity
	if err := DecodeStrict(producerIdentityBytes, &producerIdentity); err != nil {
		return receipt, err
	}
	_, critical, high, err := verifyReviewBundle(reviewRoot, review)
	if err != nil || critical != 0 || high != 0 || review.CriticalOpen != 0 || review.HighOpen != 0 || review.Verdict != "pass" || !slices.Equal(review.ImplementationContributors, producerIdentity.ImplementationContributors) || review.ReviewerActor == "" || slices.Contains(producerIdentity.ImplementationContributors, review.ReviewerActor) || review.ReviewerProcess == "" || review.ReviewerProfile == "" || review.ProfileRevision == "" || review.RunID != receipt.RunID || review.SourceCommit != expected.Commit || review.SourceTree != expected.Tree || review.ManifestSHA256 != expectedManifestSHA || review.ProducerArchiveSHA != expectedProducerSHA {
		return receipt, fmt.Errorf("nested independent review did not verify: %w", err)
	}
	wantMatrix, err := buildFinalAcceptanceMatrix(filepath.Join(evidenceRoot, "acceptance-matrix.json"), members["review/review.json"])
	if err != nil || !bytes.Equal(wantMatrix, members["final-acceptance-matrix.json"]) {
		return receipt, fmt.Errorf("final acceptance matrix was not recomputed from producer and review evidence")
	}
	if len(expectedOuter) != len(members) {
		return receipt, fmt.Errorf("release archive contains unrecognized members")
	}
	return receipt, nil
}

func extractBoundedTar(data []byte, directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	reader := tar.NewReader(bytes.NewReader(data))
	seen := map[string]bool{}
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !safeRelativePath(header.Name) || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maximumFile || seen[header.Name] {
			return fmt.Errorf("nested archive member is unsafe")
		}
		seen[header.Name] = true
		total += header.Size
		if total > maximumTotal {
			return fmt.Errorf("nested archive exceeds total budget")
		}
		contents, err := io.ReadAll(io.LimitReader(reader, maximumFile+1))
		if err != nil || int64(len(contents)) != header.Size {
			return fmt.Errorf("nested archive member is truncated")
		}
		path := filepath.Join(directory, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, contents, 0o400); err != nil {
			return err
		}
	}
}

func verifyReviewBundle(directory string, review IndependentReviewReceipt) ([]tarMember, int, int, error) {
	members, err := readBoundedTree(directory)
	if err != nil {
		return nil, 0, 0, err
	}
	byPath := make(map[string][]byte, len(members))
	for _, member := range members {
		byPath[member.name] = member.data
	}
	want := map[string]bool{"review.json": true}
	commandIDs := map[string]bool{}
	for _, command := range review.Commands {
		if !safeToken(command.ID) || commandIDs[command.ID] || len(command.Argv) == 0 || len(command.Argv) > 64 || command.ExitCode != 0 || !safeRelativePath(command.OutputPath) || command.OutputPath == "review.json" || !validDigest(command.OutputSHA256) || SHA256(byPath[command.OutputPath]) != command.OutputSHA256 {
			return nil, 0, 0, fmt.Errorf("review command receipt is missing, duplicated, or non-pass")
		}
		commandIDs[command.ID] = true
		want[command.OutputPath] = true
	}
	criticalOpen, highOpen := 0, 0
	findingIDs := map[string]bool{}
	for _, finding := range review.Findings {
		if !safeToken(finding.ID) || findingIDs[finding.ID] || !slices.Contains([]string{"critical", "high", "medium", "low"}, finding.Severity) || !slices.Contains([]string{"open", "resolved"}, finding.Disposition) || !safeRelativePath(finding.EvidencePath) || finding.EvidencePath == "review.json" || !validDigest(finding.EvidenceSHA256) || SHA256(byPath[finding.EvidencePath]) != finding.EvidenceSHA256 {
			return nil, 0, 0, fmt.Errorf("review finding is malformed, duplicated, or lacks exact evidence")
		}
		findingIDs[finding.ID] = true
		want[finding.EvidencePath] = true
		if finding.Disposition == "open" && finding.Severity == "critical" {
			criticalOpen++
		}
		if finding.Disposition == "open" && finding.Severity == "high" {
			highOpen++
		}
	}
	if len(want) != len(byPath) {
		return nil, 0, 0, fmt.Errorf("review bundle has unreferenced or missing members")
	}
	for path := range want {
		if _, ok := byPath[path]; !ok {
			return nil, 0, 0, fmt.Errorf("review bundle member %q is absent", path)
		}
	}
	return members, criticalOpen, highOpen, nil
}

func buildFinalAcceptanceMatrix(innerPath string, reviewBytes []byte) ([]byte, error) {
	inner, err := os.ReadFile(innerPath)
	if err != nil {
		return nil, err
	}
	var matrix AcceptanceMatrix
	if err := DecodeStrict(inner, &matrix); err != nil {
		return nil, err
	}
	if len(matrix.Requirements) != 85 || matrix.Requirements[84].ID != "SC-013" || matrix.Requirements[84].Status != "review_required" || len(matrix.Requirements[84].Evidence) != 0 {
		return nil, fmt.Errorf("producer acceptance matrix does not reserve SC-013 for independent review")
	}
	matrix.Requirements[84].Status = "proven"
	matrix.Requirements[84].Evidence = []AcceptanceEvidence{{Path: "review/review.json", SHA256: SHA256(reviewBytes)}}
	return CanonicalJSON(matrix)
}

type tarMember struct {
	name string
	data []byte
}

func BuildDeterministicArchive(directory string) ([]byte, int, error) {
	members, err := readBoundedTree(directory)
	if err != nil {
		return nil, 0, err
	}
	archive, err := deterministicTar(members)
	return archive, len(members), err
}

func readBoundedTree(directory string) ([]tarMember, error) {
	var members []tarMember
	var total int64
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive source contains symlink")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maximumFile {
			return fmt.Errorf("archive member is not a bounded regular file")
		}
		total += info.Size()
		if total > maximumTotal {
			return fmt.Errorf("archive source exceeds total budget")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil || !safeRelativePath(filepath.ToSlash(relative)) {
			return fmt.Errorf("archive member path is unsafe")
		}
		members = append(members, tarMember{filepath.ToSlash(relative), data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return members, nil
}

func deterministicTar(members []tarMember) ([]byte, error) {
	slices.SortFunc(members, func(a, b tarMember) int { return strings.Compare(a.name, b.name) })
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, member := range members {
		if !safeRelativePath(member.name) || len(member.data) > maximumFile {
			return nil, fmt.Errorf("unsafe archive member")
		}
		header := &tar.Header{Name: member.name, Mode: 0o400, Size: int64(len(member.data)), ModTime: time.Unix(0, 0), AccessTime: time.Unix(0, 0), ChangeTime: time.Unix(0, 0), Uid: 0, Gid: 0, Format: tar.FormatPAX}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := writer.Write(member.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
