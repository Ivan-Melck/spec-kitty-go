package gitrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
)

// EnsureIntegrationWorkspace creates or inspects an operation-scoped detached
// worktree. Replacement mutation leases never reuse another operation's
// mutable filesystem, even when they integrate the same candidate.
func (r *Repository) EnsureIntegrationWorkspace(ctx context.Context, operationID, target, candidate string) (integration.WorkspaceFacts, error) {
	var facts integration.WorkspaceFacts
	err := r.withWorktreeMetadataLock(ctx, func() error {
		var innerErr error
		facts, innerErr = r.ensureIntegrationWorkspaceLocked(ctx, operationID, target, candidate)
		return innerErr
	})
	return facts, err
}

func (r *Repository) ensureIntegrationWorkspaceLocked(ctx context.Context, operationID, target, candidate string) (integration.WorkspaceFacts, error) {
	if !isObjectID(target) || !isObjectID(candidate) {
		return integration.WorkspaceFacts{}, &integration.Error{Kind: integration.KindInvalid, Message: "integration target and candidate must be full object IDs"}
	}
	if err := r.PrepareManagedRoot(); err != nil {
		return integration.WorkspaceFacts{}, err
	}
	if checkedOut, symbolicErr := r.runner.Run(ctx, r.binding.PrimaryPath, "symbolic-ref", "--quiet", "HEAD"); symbolicErr == nil && checkedOut == r.binding.TargetRef {
		return integration.WorkspaceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "refuse to mutate a target ref attached to the protected primary checkout", Observed: map[string]string{"primary_head_ref": checkedOut}, Expected: map[string]string{"primary_head_ref": "detached or different from target_ref"}}
	}
	if _, err := r.runner.Run(ctx, r.binding.CommonDir, "cat-file", "-e", candidate+"^{commit}"); err != nil {
		return integration.WorkspaceFacts{}, &integration.Error{Kind: integration.KindGitFailure, Message: "approved candidate is not reachable", Cause: err}
	}
	suffix := operationSuffix(operationID)
	preservationRef := "refs/spec-kitty/candidates/" + suffix
	if observed, err := r.runner.Run(ctx, r.binding.CommonDir, "rev-parse", "--verify", preservationRef+"^{commit}"); err == nil {
		if observed != candidate {
			return integration.WorkspaceFacts{}, &integration.Error{Kind: integration.KindIdempotencyConflict, Message: "integration operation preservation ref names another candidate", Observed: map[string]string{"candidate": observed}, Expected: map[string]string{"candidate": candidate}}
		}
	} else if _, err := r.runner.Run(ctx, r.binding.CommonDir, "update-ref", preservationRef, candidate); err != nil {
		return integration.WorkspaceFacts{}, &integration.Error{Kind: integration.KindGitFailure, Message: "preserve approved candidate ref", Cause: err}
	}
	path := filepath.Join(r.binding.ManagedRoot, "integration-"+suffix)
	if !within(path, r.binding.ManagedRoot) {
		return integration.WorkspaceFacts{}, &integration.Error{Kind: integration.KindInvalid, Message: "integration workspace escaped managed root"}
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if _, err := r.runner.Run(ctx, r.binding.CommonDir, "worktree", "add", "--detach", path, target); err != nil {
			return integration.WorkspaceFacts{}, &integration.Error{Kind: integration.KindGitFailure, Message: "create detached integration workspace", Cause: err}
		}
	} else if err != nil {
		return integration.WorkspaceFacts{}, &integration.Error{Kind: integration.KindGitFailure, Message: "inspect integration workspace", Cause: err}
	}
	return r.integrationWorkspaceFacts(ctx, path, candidate)
}

func (r *Repository) integrationWorkspaceFacts(ctx context.Context, path, candidate string) (integration.WorkspaceFacts, error) {
	facts := integration.WorkspaceFacts{Path: path, Registered: true, CandidateReachable: true}
	canonical, err := canonicalExistingDirectory(path)
	if err != nil || canonical != path || !within(canonical, r.binding.ManagedRoot) {
		return facts, &integration.Error{Kind: integration.KindGitFailure, Message: "integration workspace is not canonical under managed root", Cause: err}
	}
	facts.Head, err = r.runner.Run(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return facts, &integration.Error{Kind: integration.KindGitFailure, Message: "read integration workspace HEAD", Cause: err}
	}
	if branch, symbolicErr := r.runner.Run(ctx, path, "symbolic-ref", "--quiet", "HEAD"); symbolicErr == nil || branch != "" {
		return facts, &integration.Error{Kind: integration.KindRepairRequired, Message: "managed integration workspace must remain detached", Observed: map[string]string{"branch": branch}, Expected: map[string]string{"head": "detached"}}
	}
	status, err := r.runner.Run(ctx, path, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return facts, &integration.Error{Kind: integration.KindGitFailure, Message: "inspect integration workspace status", Cause: err}
	}
	conflicts, err := r.runner.Run(ctx, path, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return facts, &integration.Error{Kind: integration.KindGitFailure, Message: "inspect integration conflicts", Cause: err}
	}
	facts.Detached = true
	facts.Clean = status == ""
	facts.Conflicted = conflicts != ""
	if _, err := r.runner.Run(ctx, r.binding.CommonDir, "cat-file", "-e", candidate+"^{commit}"); err != nil {
		facts.CandidateReachable = false
		return facts, &integration.Error{Kind: integration.KindRepairRequired, Message: "approved candidate became unreachable", Cause: err}
	}
	return facts, nil
}

func (r *Repository) IntegrateCandidate(ctx context.Context, facts integration.WorkspaceFacts, target, candidate string) (string, *integration.ConflictEvidence, error) {
	if !facts.Registered || !facts.Detached || !facts.Clean || facts.Conflicted || facts.Head != target {
		return "", nil, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration requires a clean detached workspace at the durable preflight target", Observed: map[string]string{"head": facts.Head, "clean": fmt.Sprint(facts.Clean)}, Expected: map[string]string{"head": target, "clean": "true"}}
	}
	base, _ := r.runner.Run(ctx, r.binding.CommonDir, "merge-base", target, candidate)
	environment := []string{
		"GIT_AUTHOR_NAME=Spec Kitty", "GIT_AUTHOR_EMAIL=spec-kitty@example.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=Spec Kitty", "GIT_COMMITTER_EMAIL=spec-kitty@example.invalid", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	}
	_, mergeErr := r.runner.RunEnv(ctx, facts.Path, environment, "-c", "commit.gpgSign=false", "merge", "--no-ff", "--no-edit", "--no-verify", candidate)
	if mergeErr != nil {
		conflicts, _ := r.runner.Run(ctx, facts.Path, "diff", "--name-only", "--diff-filter=U")
		evidence := &integration.ConflictEvidence{
			Base: base, Target: target, Candidate: candidate, Workspace: facts.Path,
			ConflictFiles: nonemptyLines(conflicts), GitMessage: mergeErr.Error(),
		}
		return "", evidence, &integration.Error{Kind: integration.KindConflict, Message: "candidate conflicts with the durable preflight target", Cause: mergeErr}
	}
	result, err := r.runner.Run(ctx, facts.Path, "rev-parse", "HEAD")
	if err != nil || !isObjectID(result) {
		return "", nil, &integration.Error{Kind: integration.KindGitFailure, Message: "resolve integration result commit", Cause: err}
	}
	return result, nil, nil
}

func (r *Repository) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	if !isObjectID(ancestor) || !isObjectID(descendant) {
		return false, &integration.Error{Kind: integration.KindInvalid, Message: "ancestry probe requires full object IDs"}
	}
	base, err := r.runner.Run(ctx, r.binding.CommonDir, "merge-base", ancestor, descendant)
	if err != nil {
		return false, &integration.Error{Kind: integration.KindGitFailure, Message: "probe Git ancestry", Cause: err}
	}
	return base == ancestor, nil
}

// EstablishIntegrationAuthority serializes repository-fence installers without
// blocking a publisher that must be fenced. No SQLite authority becomes visible
// until the exact predecessor-bound Git fence is installed and the activation
// callback commits.
func (r *Repository) EstablishIntegrationAuthority(
	ctx context.Context,
	prepare func(integration.FenceFacts) (integration.Record, error),
	activate func(integration.Record, integration.FenceFacts) (integration.Record, error),
) (integration.Record, error) {
	var result integration.Record
	err := r.withIntegrationFenceLock(ctx, func() error {
		current, err := r.IntegrationFence(ctx)
		if err != nil {
			return err
		}
		intent, err := prepare(current)
		if err != nil {
			return err
		}
		installed, err := r.InstallIntegrationFence(ctx, intent, current)
		if err != nil {
			return err
		}
		result, err = activate(intent, installed)
		return err
	})
	return result, err
}

func (r *Repository) integrationFenceRef() string {
	refDigest := sha256.Sum256([]byte(r.binding.RepositoryID.String() + "\x00" + r.binding.TargetRef))
	return "refs/spec-kitty/fences/" + hex.EncodeToString(refDigest[:16])
}

func (r *Repository) integrationFenceHistoryRef(generation int64) string {
	refDigest := sha256.Sum256([]byte(r.binding.RepositoryID.String() + "\x00" + r.binding.TargetRef))
	return fmt.Sprintf("refs/spec-kitty/fence-history/%s/%020d", hex.EncodeToString(refDigest[:16]), generation)
}

// IntegrationFence reads and validates the repository-side monotonic
// authority fact. for-each-ref returns an empty successful result when the ref
// does not yet exist, avoiding ambiguity between absence and Git failure.
func (r *Repository) IntegrationFence(ctx context.Context) (integration.FenceFacts, error) {
	fenceRef := r.integrationFenceRef()
	object, err := r.runner.Run(ctx, r.binding.CommonDir, "for-each-ref", "--format=%(objectname)", fenceRef)
	if err != nil {
		return integration.FenceFacts{}, &integration.Error{Kind: integration.KindGitFailure, Message: "read integration fencing ref", Cause: err}
	}
	if object == "" {
		return integration.FenceFacts{Ref: fenceRef}, nil
	}
	payload, err := r.runner.Run(ctx, r.binding.CommonDir, "cat-file", "blob", object)
	if err != nil {
		return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing ref does not name a readable fence object", Cause: err}
	}
	facts, err := parseIntegrationFence(payload)
	if err != nil {
		return integration.FenceFacts{}, err
	}
	facts.Ref = fenceRef
	facts.Object = object
	if err := r.validateIntegrationFenceFacts(facts); err != nil {
		return integration.FenceFacts{}, err
	}
	if err := r.verifyIntegrationFenceHistoryObject(ctx, facts.Generation, object); err != nil {
		return integration.FenceFacts{}, err
	}
	seen := map[string]struct{}{object: {}}
	childGeneration := facts.Generation
	for predecessor := facts.PredecessorObject; predecessor != ""; {
		if !isObjectID(predecessor) {
			return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing history contains an invalid predecessor object"}
		}
		if _, duplicate := seen[predecessor]; duplicate {
			return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing history contains a predecessor cycle"}
		}
		seen[predecessor] = struct{}{}
		priorPayload, readErr := r.runner.Run(ctx, r.binding.CommonDir, "cat-file", "blob", predecessor)
		if readErr != nil {
			return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing history contains an unreadable predecessor", Cause: readErr}
		}
		prior, parseErr := parseIntegrationFence(priorPayload)
		if parseErr != nil {
			return integration.FenceFacts{}, parseErr
		}
		if err := r.validateIntegrationFenceFacts(prior); err != nil {
			return integration.FenceFacts{}, err
		}
		if err := r.verifyIntegrationFenceHistoryObject(ctx, prior.Generation, predecessor); err != nil {
			return integration.FenceFacts{}, err
		}
		if prior.Generation >= childGeneration {
			return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing history is not strictly monotonic"}
		}
		facts.SupersededOperations = append(facts.SupersededOperations, prior.OperationID)
		childGeneration = prior.Generation
		predecessor = prior.PredecessorObject
	}
	return facts, nil
}

func (r *Repository) verifyIntegrationFenceHistoryObject(ctx context.Context, generation int64, expected string) error {
	historyRef := r.integrationFenceHistoryRef(generation)
	object, err := r.runner.Run(ctx, r.binding.CommonDir, "for-each-ref", "--format=%(objectname)", historyRef)
	if err != nil {
		return &integration.Error{Kind: integration.KindGitFailure, Message: "read immutable integration fence history ref", Cause: err}
	}
	if object != expected {
		return &integration.Error{Kind: integration.KindRepairRequired, Message: "immutable integration fence history ref is absent or changed", Observed: map[string]string{"history_ref": historyRef, "object": object}, Expected: map[string]string{"object": expected}}
	}
	return nil
}

func (r *Repository) validateIntegrationFenceFacts(facts integration.FenceFacts) error {
	if facts.Generation <= 0 || facts.OperationID == "" || facts.RequestDigest == "" || facts.RepositoryID != r.binding.RepositoryID || facts.TargetRef != r.binding.TargetRef || facts.PreflightTarget == "" || facts.TokenDigest == "" || facts.Deadline.IsZero() {
		return &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing ref contains incomplete authority facts"}
	}
	return nil
}

// InstallIntegrationFence installs exactly the prepared successor of current.
// It never adopts whatever ref happens to be observed after preparation.
func (r *Repository) InstallIntegrationFence(ctx context.Context, record integration.Record, current integration.FenceFacts) (integration.FenceFacts, error) {
	if record.OperationID == "" || record.RepositoryID != r.binding.RepositoryID || record.TargetRef != r.binding.TargetRef || record.MutationGeneration <= 0 || record.Token == "" || record.FenceRequestDigest == "" || current.Ref != r.integrationFenceRef() {
		return integration.FenceFacts{}, &integration.Error{Kind: integration.KindInvalid, Message: "integration fence requires an exact repository-first authority intent"}
	}
	tokenDigest := sha256.Sum256([]byte(record.Token))
	payload := fmt.Sprintf("spec-kitty-integration-fence-v2\nrepository=%s\ntarget=%s\ngeneration=%d\noperation=%s\nrequest_digest=%s\npreflight_target=%s\ndeadline=%s\npredecessor=%s\ntoken=%x\n", record.RepositoryID, record.TargetRef, record.MutationGeneration, record.OperationID, record.FenceRequestDigest, record.PreflightTarget, record.LeaseDeadline.UTC().Format(time.RFC3339Nano), record.FencePredecessor, tokenDigest)
	fenceObject, err := r.runner.RunInput(ctx, r.binding.CommonDir, payload, "hash-object", "-w", "--stdin")
	if err != nil || !isObjectID(fenceObject) {
		return integration.FenceFacts{}, &integration.Error{Kind: integration.KindGitFailure, Message: "write integration fence object", Cause: err}
	}
	facts := integration.FenceFacts{Ref: current.Ref, Object: fenceObject, PredecessorObject: record.FencePredecessor, Generation: record.MutationGeneration, OperationID: record.OperationID, RepositoryID: record.RepositoryID, TargetRef: record.TargetRef, RequestDigest: record.FenceRequestDigest, PreflightTarget: record.PreflightTarget, TokenDigest: fmt.Sprintf("%x", tokenDigest), Deadline: record.LeaseDeadline}
	if current.Object == fenceObject {
		if current.Generation != facts.Generation || current.OperationID != facts.OperationID || current.RequestDigest != facts.RequestDigest || current.PredecessorObject != facts.PredecessorObject {
			return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "deterministic fence object disagrees with current repository facts"}
		}
		return current, nil
	}
	if current.Object != record.FencePredecessor || record.MutationGeneration <= current.Generation {
		return integration.FenceFacts{}, &integration.Error{Kind: integration.KindAuthorityBusy, Message: "repository fence predecessor or generation changed before installation", Observed: map[string]string{"fence_object": current.Object, "generation": fmt.Sprint(current.Generation)}, Expected: map[string]string{"fence_object": record.FencePredecessor, "generation_less_than": fmt.Sprint(record.MutationGeneration)}}
	}
	expected := strings.Repeat("0", len(fenceObject))
	if record.FencePredecessor != "" {
		expected = record.FencePredecessor
	}
	historyRef := r.integrationFenceHistoryRef(record.MutationGeneration)
	transaction := fmt.Sprintf("start\ncreate %s %s\nupdate %s %s %s\nprepare\ncommit\n", historyRef, fenceObject, current.Ref, fenceObject, expected)
	if _, err := r.runner.RunInput(ctx, r.binding.CommonDir, transaction, "update-ref", "--stdin"); err != nil {
		return integration.FenceFacts{}, &integration.Error{Kind: integration.KindAuthorityBusy, Message: "exact predecessor repository fence installation lost its compare-and-swap", Observed: map[string]string{"predecessor": expected}, Cause: err}
	}
	return facts, nil
}

func parseIntegrationFence(payload string) (integration.FenceFacts, error) {
	lines := strings.Split(payload, "\n")
	if len(lines) != 10 || lines[0] != "spec-kitty-integration-fence-v2" {
		return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing object has an unsupported format"}
	}
	values := map[string]string{}
	for _, line := range lines[1:10] {
		key, value, found := strings.Cut(line, "=")
		if !found {
			return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing object has malformed facts"}
		}
		values[key] = value
	}
	generation, err := strconv.ParseInt(values["generation"], 10, 64)
	deadline, deadlineErr := time.Parse(time.RFC3339Nano, values["deadline"])
	if err != nil || deadlineErr != nil || generation <= 0 || values["repository"] == "" || values["target"] == "" || values["operation"] == "" || values["request_digest"] == "" || values["preflight_target"] == "" || values["token"] == "" {
		return integration.FenceFacts{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration fencing object has invalid facts", Cause: err}
	}
	return integration.FenceFacts{PredecessorObject: values["predecessor"], Generation: generation, OperationID: ids.OperationID(values["operation"]), RepositoryID: ids.RepositoryID(values["repository"]), TargetRef: values["target"], RequestDigest: values["request_digest"], PreflightTarget: values["preflight_target"], TokenDigest: values["token"], Deadline: deadline}, nil
}

// PublishIntegrationTarget verifies the current generation's fencing ref and
// the expected-old target in one Git ref transaction. The primary attachment
// probe is deliberately inside the same repository flock, immediately before
// the transaction, so waiting for another publisher cannot stale the probe.
func (r *Repository) PublishIntegrationTarget(ctx context.Context, record integration.Record, expected, target string, immediatelyBefore func() error) error {
	if !isObjectID(expected) || !isObjectID(target) || record.FenceRef == "" || !isObjectID(record.FenceObject) {
		return &integration.Error{Kind: integration.KindInvalid, Message: "fenced target publication requires full object IDs and an active fence"}
	}
	return r.withTargetLock(ctx, func() error {
		if immediatelyBefore != nil {
			if err := immediatelyBefore(); err != nil {
				return err
			}
		}
		if checkedOut, symbolicErr := r.runner.Run(ctx, r.binding.PrimaryPath, "symbolic-ref", "--quiet", "HEAD"); symbolicErr == nil && checkedOut == r.binding.TargetRef {
			return &integration.Error{Kind: integration.KindRepairRequired, Message: "refuse to move a target ref attached to the protected primary checkout", Observed: map[string]string{"primary_head_ref": checkedOut}, Expected: map[string]string{"primary_head_ref": "detached or different from target_ref"}}
		}
		transaction := fmt.Sprintf("start\nverify %s %s\nupdate %s %s %s\nprepare\ncommit\n", record.FenceRef, record.FenceObject, r.binding.TargetRef, target, expected)
		if _, err := r.runner.RunInput(ctx, r.binding.CommonDir, transaction, "update-ref", "--stdin"); err != nil {
			return &integration.Error{Kind: integration.KindTargetMoved, Message: "fenced expected-old target publication failed", Observed: map[string]string{"fence_ref": record.FenceRef, "fence_object": record.FenceObject}, Expected: map[string]string{"target_head": expected}, Cause: err}
		}
		return nil
	})
}

// VerifyIntegrationResult recognizes only the merge commit this adapter would
// have produced: exact ordered parents and the tree computed by merge-tree.
// This closes the crash window after Git commits but before SQLite records the
// intended result without trusting a merely clean workspace HEAD.
func (r *Repository) VerifyIntegrationResult(ctx context.Context, result, target, candidate string) (bool, error) {
	if !isObjectID(result) || !isObjectID(target) || !isObjectID(candidate) {
		return false, &integration.Error{Kind: integration.KindInvalid, Message: "result verification requires full object IDs"}
	}
	parents, err := r.runner.Run(ctx, r.binding.CommonDir, "rev-list", "--parents", "-n", "1", result)
	if err != nil {
		return false, &integration.Error{Kind: integration.KindGitFailure, Message: "read integration result parents", Cause: err}
	}
	fields := strings.Fields(parents)
	if len(fields) != 3 || fields[0] != result || fields[1] != target || fields[2] != candidate {
		return false, nil
	}
	merged, mergeErr := r.runner.Run(ctx, r.binding.CommonDir, "merge-tree", "--write-tree", "--messages", target, candidate)
	lines := nonemptyLines(merged)
	if mergeErr != nil || len(lines) == 0 || !isObjectID(lines[0]) {
		return false, &integration.Error{Kind: integration.KindGitFailure, Message: "recompute intended integration tree", Cause: mergeErr}
	}
	resultTree, err := r.runner.Run(ctx, r.binding.CommonDir, "rev-parse", result+"^{tree}")
	if err != nil {
		return false, &integration.Error{Kind: integration.KindGitFailure, Message: "read integration result tree", Cause: err}
	}
	return resultTree == lines[0], nil
}

// CleanupIntegrationWorkspace intentionally refuses dirty or conflicted
// workspaces. Repair evidence is more important than eager cleanup.
func (r *Repository) CleanupIntegrationWorkspace(ctx context.Context, record integration.Record) error {
	return r.withWorktreeMetadataLock(ctx, func() error { return r.cleanupIntegrationWorkspaceLocked(ctx, record) })
}

func (r *Repository) cleanupIntegrationWorkspaceLocked(ctx context.Context, record integration.Record) error {
	expectedPath := filepath.Join(r.binding.ManagedRoot, "integration-"+operationSuffix(record.OperationID.String()))
	if record.Status != integration.StatusDone || (record.CleanupStatus != integration.CleanupEligible && record.CleanupStatus != integration.CleanupComplete) || record.OperationID == "" || record.WorkspacePath != expectedPath || record.Candidate == "" || record.ResultCommit == "" || record.ObservedTarget == "" {
		return &integration.Error{Kind: integration.KindRepairRequired, Message: "refuse cleanup without a terminal durable integration binding", Observed: map[string]string{"workspace": record.WorkspacePath, "status": string(record.Status)}, Expected: map[string]string{"workspace": expectedPath, "status": string(integration.StatusDone)}}
	}
	preservationRef := "refs/spec-kitty/candidates/" + operationSuffix(record.OperationID.String())
	if candidate, err := r.runner.Run(ctx, r.binding.CommonDir, "rev-parse", "--verify", preservationRef+"^{commit}"); err != nil || candidate != record.Candidate {
		return &integration.Error{Kind: integration.KindRepairRequired, Message: "refuse cleanup when the operation candidate preservation ref is absent or changed", Cause: err}
	}
	if _, err := os.Lstat(record.WorkspacePath); os.IsNotExist(err) {
		listed, listErr := r.runner.Run(ctx, r.binding.CommonDir, "worktree", "list", "--porcelain", "-z")
		if listErr != nil {
			return &integration.Error{Kind: integration.KindGitFailure, Message: "confirm removed integration workspace registration", Cause: listErr}
		}
		for _, field := range strings.Split(listed, "\x00") {
			if field == "worktree "+record.WorkspacePath {
				return &integration.Error{Kind: integration.KindRepairRequired, Message: "integration workspace path is absent but remains registered"}
			}
		}
		return nil
	} else if err != nil {
		return &integration.Error{Kind: integration.KindGitFailure, Message: "inspect cleanup-eligible integration workspace", Cause: err}
	}
	facts, err := r.integrationWorkspaceFacts(ctx, record.WorkspacePath, record.Candidate)
	if err != nil {
		return err
	}
	if !facts.Clean || facts.Conflicted {
		return &integration.Error{Kind: integration.KindRepairRequired, Message: "refuse cleanup of dirty or conflicted integration workspace", Observed: map[string]string{"workspace": record.WorkspacePath}, Expected: map[string]string{"repair": "preserve and inspect"}}
	}
	if _, err := r.runner.Run(ctx, r.binding.CommonDir, "worktree", "remove", record.WorkspacePath); err != nil {
		return &integration.Error{Kind: integration.KindGitFailure, Message: "remove clean integration workspace", Cause: err}
	}
	return nil
}

func operationSuffix(operationID string) string {
	digest := sha256.Sum256([]byte(operationID))
	return hex.EncodeToString(digest[:12])
}

func nonemptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}
