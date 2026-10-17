//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"golang.org/x/sys/unix"
)

// ExpiryHostInitialDiagnosticInputs selects only physical experiment inputs.
// No Config, Host, callbacks, provider, observer or admission override is accepted.
// This surface is absent from ordinary builds; it is NOT production admission.
type ExpiryHostInitialDiagnosticInputs struct {
	Image, InputRecord, RunParent, M1AdoptionRecord, SourceReviewSHA256 string
}

// ExpiryHostInitialDiagnosticReport contains bounded copy-only observations, not
// proof authority. Added work/storage is OWNED_UNQUALIFIED. Neither a diagnostic
// pass nor the existing cleanup10s allowance qualifies the original cleanup5s.
type ExpiryHostInitialDiagnosticReport struct {
	Scenario                               string   `json:"scenario"`
	Root                                   string   `json:"preserved_root,omitempty"`
	Stages                                 []string `json:"completed_stages"`
	FailedAt                               string   `json:"failed_at,omitempty"`
	DatabaseID                             string   `json:"database_id,omitempty"`
	RuntimeEpochID                         string   `json:"runtime_epoch_id,omitempty"`
	ProviderPID                            int      `json:"provider_pid,omitempty"`
	ExecutedImageSHA256                    string   `json:"executed_image_sha256,omitempty"`
	BaselinePrivate, BaselinePublication   uint64
	InsertedRevision, ExpiryRevision       uint64
	RefreshedPrivate, RefreshedPublication uint64
	FirstCleanupAttempted                  bool   `json:"first_cleanup_attempted"`
	FirstCleanupSucceeded                  bool   `json:"first_cleanup_succeeded"`
	PrimaryError                           string `json:"primary_error,omitempty"`
	CleanupError                           string `json:"cleanup_error,omitempty"`
	DiagnosticPassed                       bool   `json:"diagnostic_passed"`
	Qualified                              bool   `json:"qualified"` // ALWAYS false: no whole-area acceptance
	Limits                                 string `json:"limits"`
}

func expiryDiagnosticErrorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 2048 {
		text = text[:2048]
	}
	return text
}

func validateExpiryDiagnosticInputs(ctx context.Context, in ExpiryHostInitialDiagnosticInputs) error {
	if ctx == nil {
		return expiryFailure("expiry_diagnostic_context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !hexDigest(in.SourceReviewSHA256) {
		return expiryFailure("expiry_diagnostic_review")
	}
	for _, path := range []string{in.Image, in.InputRecord, in.RunParent, in.M1AdoptionRecord} {
		if len(path) > 4096 || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
			return expiryFailure("expiry_diagnostic_path")
		}
	}
	// Do not spend a constructor/root on the known tmpfs/Git-parent mistakes.
	// These preconditions do NOT replace real held input/image/filesystem checks.
	for path := in.RunParent; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) ||
			(info.Mode().Perm()&0022 != 0 && !(info.Mode()&os.ModeSticky != 0 && st.Uid == 0)) {
			return expiryFailure("expiry_diagnostic_parent")
		}
		if path == in.RunParent && (st.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700) {
			return expiryFailure("expiry_diagnostic_parent")
		}
		if _, err := os.Lstat(filepath.Join(path, ".git")); !errors.Is(err, os.ErrNotExist) {
			return expiryFailure("expiry_diagnostic_git_parent")
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(in.RunParent, &fs); err != nil {
		return err
	}
	switch fs.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.F2FS_SUPER_MAGIC:
		return nil
	default:
		return expiryFailure("expiry_diagnostic_filesystem")
	}
}

// RunExpiryHostInitialDiagnostic is a fixed E-only composition entry for an
// ordinary go-build-linked binary. It NEVER bypasses linked SDK metadata proof,
// manufactures testing.T/build metadata/ACKs or changes normal Start selection.
// Every owned root is preserved. Constructor residuals remain residuals.
func RunExpiryHostInitialDiagnostic(ctx context.Context, in ExpiryHostInitialDiagnosticInputs) (report ExpiryHostInitialDiagnosticReport, resultErr error) {
	report.Scenario = "initial/private_only"
	report.Limits = "component experiment only; OWNED_UNQUALIFIED; cleanup10s NOT original5s qualification; no hard containment/compiler closure/all19 acceptance"
	report.FailedAt = "preflight"
	defer func() {
		if !report.FirstCleanupAttempted {
			report.PrimaryError = expiryDiagnosticErrorText(resultErr)
		}
	}()
	if err := validateExpiryDiagnosticInputs(ctx, in); err != nil {
		return report, err
	}
	root, err := os.MkdirTemp(in.RunParent, "expiry-host-")
	if err != nil {
		return report, err
	}
	report.Root = root
	report.Stages = append(report.Stages, "exclusive-root-created")
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeRoot, 0700); err != nil {
		return report, err
	}
	config := Config{ControlRoot: filepath.Join(root, "authority"), HostAuthorityID: "host-expiry-native", AuthorityBindingOperationID: "operation-expiry-native-binding",
		M1AdoptionRecordPath: in.M1AdoptionRecord,
		Sidecar:              sidecargapdb.LifecycleConfig{ExecutablePath: in.Image, AdmissionRecordPath: in.InputRecord, RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(root, "authority"), SupervisorRunID: "supervisor-expiry-native-initial", ProductPolicySHA256: strings.Repeat("e", 64)},
		CapabilityReadiness:  expiryDiagnosticReadiness{}, DecompositionProvider: expiryDiagnosticDecomposition{}, CandidateVerifier: expiryDiagnosticVerifier{}, IntegrationGit: expiryDiagnosticGit{}, MonitorInterval: 250 * time.Millisecond}
	report.FailedAt = "host-start"
	h, run, err := startExpiryHostQualification(ctx, config)
	if err != nil {
		return report, err
	}
	// Install canonical cleanup IMMEDIATELY after successful construction.
	defer func() {
		report.PrimaryError = expiryDiagnosticErrorText(resultErr)
		report.FirstCleanupAttempted = true
		cleanupErr := run.close(context.Background()) // existing canonical budget, never renewed here
		report.FirstCleanupSucceeded = cleanupErr == nil
		report.CleanupError = expiryDiagnosticErrorText(cleanupErr)
		if resultErr == nil && cleanupErr != nil {
			report.FailedAt = "FIRST-canonical-cleanup"
		}
		resultErr = errors.Join(resultErr, cleanupErr)
		report.DiagnosticPassed = resultErr == nil
		if resultErr == nil {
			report.FailedAt = ""
			report.Stages = append(report.Stages, "FIRST-canonical-cleanup-succeeded")
		}
	}()
	p := run.owner.lifecycle.Provenance()
	report.DatabaseID, report.RuntimeEpochID, report.ProviderPID = string(h.DatabaseID()), string(h.RuntimeEpochID()), p.Process.PID
	report.ExecutedImageSHA256 = p.ExecutedImage.SHA256
	report.Stages = append(report.Stages, "genuine-host-started")
	op, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resultErr = expiryDiagnosticInitialTTL(op, h, run, &report)
	return report, resultErr
}

func expiryDiagnosticInitialTTL(ctx context.Context, h *Host, run *expiryHostRun, report *ExpiryHostInitialDiagnosticReport) error {
	stage := func(name string) { report.FailedAt = name }
	pass := func(name string) { report.Stages = append(report.Stages, name) }
	stage("baseline-six-private-zero-publication")
	proof, err := h.VerifyRetainedRecords(ctx, nil)
	if err != nil {
		return err
	}
	report.BaselinePrivate, report.BaselinePublication = proof.MatchedPrivateRecords(), proof.MatchedPublicationRecords()
	if report.BaselinePrivate != 6 || report.BaselinePublication != 0 || proof.MatchedRecords() != 6 {
		return expiryFailure("expiry_diagnostic_baseline")
	}
	pass(report.FailedAt)
	stage("durable-TTL-insert-and-refresh")
	owner := run.owner
	expires := time.Now().UTC().Add(2 * time.Second)
	inserted, err := owner.adapter.ConditionalMutate(ctx, "expiry-host-extra-insert", storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: "spk/expiry-evidence/extra", Value: []byte("expiry-host-extra-v1"), Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, ExpiresAt: &expires}, storegapdb.AckDurable)
	if err != nil {
		return err
	}
	if inserted.Revision == 0 || inserted.Ack != storegapdb.AckDurable || inserted.DurableThrough < inserted.Revision || inserted.MutationCount != 1 {
		return expiryFailure("expiry_diagnostic_TTL_ack")
	}
	report.InsertedRevision = inserted.Revision
	if err := h.refreshProjection(ctx); err != nil {
		return err
	}
	pass(report.FailedAt)
	stage("actual-H-notfound-attributed-flanks")
	if err := run.gate.WaitHeld(ctx); err != nil {
		return err
	}
	attribution := h.projection.Attribution()
	before, err := owner.adapter.Inspect(ctx)
	if err != nil {
		return err
	}
	if _, err := owner.adapter.Get(ctx, "spk/expiry-evidence/extra"); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
		return errors.Join(err, expiryFailure("expiry_diagnostic_held_notfound"))
	}
	after, err := owner.adapter.Inspect(ctx)
	if err != nil {
		return err
	}
	p := owner.lifecycle.Provenance()
	for _, status := range []storegapdb.Inspection{before, after} {
		if !status.Healthy || status.Lifecycle != "ready" || status.DatabaseID != string(h.databaseID) || status.OwnerPID != p.Process.PID || status.OwnerStartedAt != p.Observation.Status.OwnerStartedAt || status.CurrentRevision != inserted.Revision || status.DurableThrough != status.CurrentRevision || attribution.Revision != status.CurrentRevision {
			return expiryFailure("expiry_diagnostic_held_coordinates")
		}
	}
	pass(report.FailedAt)
	stage("exact-public-zero-sanitized-refusal")
	public, publicErr := h.VerifyRetainedRecords(ctx, nil)
	refusal, sanitized := publicErr.(*ProgressError)
	if !reflect.ValueOf(public).IsZero() || !sanitized || refusal == nil || refusal.Kind != ProgressIncomplete || refusal.Field != "retained_records" || publicErr.Error() != proofIncomplete("retained_records").Error() {
		return errors.Join(publicErr, expiryFailure("expiry_diagnostic_public_refusal"))
	}
	pass(report.FailedAt)
	stage("fresh-armed-S1-S2-terminal-Consume-outer")
	if err := run.observeS1Refusal(ctx, nil); err != nil {
		return err
	}
	pass(report.FailedAt)
	stage("actual-R-A")
	if err := run.release(ctx); err != nil {
		return err
	}
	pass(report.FailedAt)
	stage("actual-durable-expiry-revision")
	for {
		status, err := owner.adapter.Inspect(ctx)
		if err != nil {
			return err
		}
		if status.DatabaseID != p.DatabaseID || status.OwnerPID != p.Process.PID || status.OwnerStartedAt != p.Observation.Status.OwnerStartedAt || status.Lifecycle != "ready" || !status.Healthy {
			return expiryFailure("expiry_diagnostic_post_A_owner")
		}
		if status.CurrentRevision > inserted.Revision && status.DurableThrough == status.CurrentRevision {
			report.ExpiryRevision = status.CurrentRevision
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond): // existing observation loop, never an expiry witness
		}
	}
	if _, err := owner.adapter.Get(ctx, "spk/expiry-evidence/extra"); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
		return errors.Join(err, expiryFailure("expiry_diagnostic_expired_notfound"))
	}
	pass(report.FailedAt)
	stage("refreshed-fresh-matched-six-record-closure")
	if err := h.refreshProjection(ctx); err != nil {
		return err
	}
	if err := run.observeMatchedAfterExpiry(ctx, nil, inserted.Revision); err != nil {
		return err
	}
	refreshed, err := h.VerifyRetainedRecords(ctx, nil)
	if err != nil {
		return err
	}
	report.RefreshedPrivate, report.RefreshedPublication = refreshed.MatchedPrivateRecords(), refreshed.MatchedPublicationRecords()
	if report.RefreshedPrivate != 6 || report.RefreshedPublication != 0 || refreshed.MatchedRecords() != 6 {
		return expiryFailure("expiry_diagnostic_refreshed_counts")
	}
	pass(report.FailedAt)
	return nil
}
