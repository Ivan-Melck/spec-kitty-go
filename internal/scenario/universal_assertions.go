package scenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
	_ "modernc.org/sqlite"
)

// universalAssertionState is the evidence visible at one named fault. The
// optional probes must be read-only or expected-to-refuse public operations.
type universalAssertionState struct {
	ScenarioID         string
	FaultID            string
	Database           string
	TrackerPath        string
	RepositoryPath     string
	Repository         gitrepo.Repository
	ExpectedPrimary    workspace.PrimarySnapshot
	ExpectedWorkspaces map[ids.WorkspaceID]workspaceBindingFact
	ExpectedAuthority  map[string]activeLeaseFact
	CandidateCommits   []string
	StaleProbe         func(context.Context) error
	RequireStaleProbe  bool
	AttributionProbe   func(context.Context) error
}

type workspaceBindingFact struct {
	RepositoryID ids.RepositoryID
	OperationID  ids.OperationID
	RunID        ids.RunID
	LeaseID      ids.LeaseID
	Generation   int64
	LaneID       ids.LaneID
	Path         string
	Branch       string
	GitDir       string
	IndexPath    string
	PinnedBase   string
}

type activeLeaseFact struct {
	WorkRevisionID ids.WorkRevisionID
	WorkID         ids.WorkID
	LeaseID        ids.LeaseID
	Generation     int64
	TokenDigest    string
	Deadline       string
}

type pendingAttributionFact struct {
	PriorRevision int64
	Assignee      string
}

func assertUniversalInvariants(ctx context.Context, state universalAssertionState) error {
	if state.ScenarioID == "" || state.FaultID == "" {
		return fmt.Errorf("scenario_id=%q fault_id=%q invariant=envelope recovery=record stable scenario and fault IDs", state.ScenarioID, state.FaultID)
	}
	if state.ExpectedAuthority == nil {
		return invariantFailure(state, "one_valid_lease", errors.New("expected authority map is absent"), "wire independently derived exact authority expectations into the scenario")
	}
	if state.ExpectedWorkspaces == nil {
		return invariantFailure(state, "immutable_workspace_binding", errors.New("expected workspace map is absent"), "wire independently derived exact workspace expectations into the scenario")
	}
	if err := assertPrimaryIsolation(ctx, state); err != nil {
		return invariantFailure(state, "primary_isolation", err, "quarantine the mutator and preserve the primary checkout")
	}
	db, err := sql.Open("sqlite", state.Database)
	if err != nil {
		return invariantFailure(state, "control_store_read", err, "preserve the database and retry the read-only audit")
	}
	defer db.Close()
	if err := assertLeaseCardinality(ctx, db, state.ExpectedAuthority); err != nil {
		return invariantFailure(state, "one_valid_lease", err, "expire conflicting authority before any publication")
	}
	if err := assertWorkspaceBindings(ctx, db, state.ExpectedWorkspaces); err != nil {
		return invariantFailure(state, "immutable_workspace_binding", err, "quarantine the workspace and retain its branch and commit evidence")
	}
	if err := assertTargetSerialization(ctx, db); err != nil {
		return invariantFailure(state, "target_ref_serialization", err, "stop integration and recover from the repository fence")
	}
	if err := assertCandidateReachability(ctx, db, state.RepositoryPath, state.CandidateCommits); err != nil {
		return invariantFailure(state, "candidate_reachability", err, "retain candidate and conflict refs until reconciliation")
	}
	if err := assertAttributionFacts(ctx, db, state.TrackerPath); err != nil {
		return invariantFailure(state, "attribution_safe_compensation", err, "preserve provider state and require compare-before-compensate evidence")
	}
	if state.RequireStaleProbe && state.StaleProbe == nil {
		return invariantFailure(state, "stale_token_refusal", errors.New("required stale probe is absent"), "provision expired authority in this scenario fixture")
	}
	if state.StaleProbe != nil {
		err := state.StaleProbe(ctx)
		var diagnostic *execution.DiagnosticError
		if !errors.As(err, &diagnostic) || diagnostic.Kind != execution.KindStaleAuthority || diagnostic.RetrySafety != execution.RetryDecisionRequired || len(diagnostic.RecoveryActions) == 0 {
			return invariantFailure(state, "stale_token_refusal", fmt.Errorf("diagnostic=%v", err), "reject the old generation and inspect current authority")
		}
	}
	if state.AttributionProbe != nil {
		if err := state.AttributionProbe(ctx); err != nil {
			return invariantFailure(state, "attribution_safe_compensation", err, "leave the human/provider assignment unchanged")
		}
	}
	return nil
}

func assertPrimaryIsolation(ctx context.Context, state universalAssertionState) error {
	observed, err := state.Repository.SnapshotPrimary(ctx)
	if err != nil {
		return err
	}
	if observed != state.ExpectedPrimary {
		return fmt.Errorf("expected_head=%s observed_head=%s expected_index=%s observed_index=%s expected_tree=%s observed_tree=%s", state.ExpectedPrimary.Head, observed.Head, state.ExpectedPrimary.IndexDigest, observed.IndexDigest, state.ExpectedPrimary.TreeDigest, observed.TreeDigest)
	}
	return nil
}

func assertLeaseCardinality(ctx context.Context, db *sql.DB, expected map[string]activeLeaseFact) error {
	rows, err := db.QueryContext(ctx, `SELECT work_revision_id,work_id,COUNT(*),GROUP_CONCAT(lease_id),GROUP_CONCAT(generation) FROM leases WHERE status='active' GROUP BY work_revision_id,work_id HAVING COUNT(*)>1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var revision, work, leases, generations string
		var count int
		if err := rows.Scan(&revision, &work, &count, &leases, &generations); err != nil {
			return err
		}
		return fmt.Errorf("revision=%s work=%s active=%d leases=%s generations=%s", revision, work, count, leases, generations)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	observedRows, err := db.QueryContext(ctx, `SELECT work_revision_id,work_id,lease_id,generation,token_digest,deadline FROM leases WHERE status='active' ORDER BY work_revision_id,work_id`)
	if err != nil {
		return err
	}
	defer observedRows.Close()
	observed := make(map[string]activeLeaseFact)
	for observedRows.Next() {
		var fact activeLeaseFact
		if err := observedRows.Scan(&fact.WorkRevisionID, &fact.WorkID, &fact.LeaseID, &fact.Generation, &fact.TokenDigest, &fact.Deadline); err != nil {
			return err
		}
		observed[leaseFactKey(fact.WorkRevisionID, fact.WorkID)] = fact
	}
	if err := observedRows.Err(); err != nil {
		return err
	}
	if expected != nil {
		if len(observed) != len(expected) {
			return fmt.Errorf("expected_active=%v observed_active=%v", expected, observed)
		}
		for key, want := range expected {
			if got, ok := observed[key]; !ok || got != want {
				return fmt.Errorf("authority_key=%s expected=%+v observed=%+v present=%t", key, want, got, ok)
			}
		}
	}
	return nil
}

func leaseFactKey(revision ids.WorkRevisionID, work ids.WorkID) string {
	return revision.String() + "/" + work.String()
}

func assertWorkspaceBindings(ctx context.Context, db *sql.DB, expected map[ids.WorkspaceID]workspaceBindingFact) error {
	var triggerCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name IN ('immutable_workspace_binding_update','immutable_workspace_binding_delete')`).Scan(&triggerCount); err != nil {
		return err
	}
	if triggerCount != 2 {
		return fmt.Errorf("immutable_triggers=%d expected=2", triggerCount)
	}
	rows, err := db.QueryContext(ctx, `SELECT workspace_id,repository_id,operation_id,run_id,lease_id,lease_generation,lane_id,canonical_path,owned_branch,git_dir,index_path,pinned_base FROM workspaces ORDER BY workspace_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	seenPaths, seenBranches := map[string]string{}, map[string]string{}
	observed := make(map[ids.WorkspaceID]workspaceBindingFact)
	for rows.Next() {
		var workspaceID ids.WorkspaceID
		var fact workspaceBindingFact
		if err := rows.Scan(&workspaceID, &fact.RepositoryID, &fact.OperationID, &fact.RunID, &fact.LeaseID, &fact.Generation, &fact.LaneID, &fact.Path, &fact.Branch, &fact.GitDir, &fact.IndexPath, &fact.PinnedBase); err != nil {
			return err
		}
		if workspaceID == "" || fact.RepositoryID == "" || fact.OperationID == "" || fact.RunID == "" || fact.LeaseID == "" || fact.Generation <= 0 || fact.LaneID == "" || fact.Path == "" || fact.Branch == "" || fact.PinnedBase == "" {
			return fmt.Errorf("workspace=%s incomplete_binding=%+v", workspaceID, fact)
		}
		if fact.GitDir == "" != (fact.IndexPath == "") {
			return fmt.Errorf("workspace=%s partial_git_metadata git_dir=%q index_path=%q", workspaceID, fact.GitDir, fact.IndexPath)
		}
		if prior := seenPaths[fact.Path]; prior != "" {
			return fmt.Errorf("workspace=%s path=%s already_owned_by=%s", workspaceID, fact.Path, prior)
		}
		if prior := seenBranches[fact.Branch]; prior != "" {
			return fmt.Errorf("workspace=%s branch=%s already_owned_by=%s", workspaceID, fact.Branch, prior)
		}
		seenPaths[fact.Path], seenBranches[fact.Branch] = workspaceID.String(), workspaceID.String()
		observed[workspaceID] = fact
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if expected != nil && len(observed) != len(expected) {
		return fmt.Errorf("expected_workspace_count=%d observed_workspace_count=%d expected=%v observed=%v", len(expected), len(observed), expected, observed)
	}
	for workspaceID, want := range expected {
		got, ok := observed[workspaceID]
		if !ok || got != want {
			return fmt.Errorf("workspace=%s expected=%+v observed=%+v present=%t", workspaceID, want, got, ok)
		}
	}
	return nil
}

func assertTargetSerialization(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT repository_id,target_ref,COUNT(*),GROUP_CONCAT(operation_id),GROUP_CONCAT(generation) FROM integration_mutation_leases WHERE status IN ('pending','active') GROUP BY repository_id,target_ref HAVING COUNT(*)>1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var repository, target, operations, generations string
		var count int
		if err := rows.Scan(&repository, &target, &count, &operations, &generations); err != nil {
			return err
		}
		return fmt.Errorf("repository=%s target=%s authorities=%d operations=%s generations=%s", repository, target, count, operations, generations)
	}
	return rows.Err()
}

func assertCandidateReachability(ctx context.Context, db *sql.DB, repository string, supplied []string) error {
	commits := append([]string(nil), supplied...)
	rows, err := db.QueryContext(ctx, `SELECT candidate,result_commit FROM integrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var candidate, result string
		if err := rows.Scan(&candidate, &result); err != nil {
			_ = rows.Close()
			return err
		}
		commits = append(commits, candidate)
		if result != "" {
			commits = append(commits, result)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	sort.Strings(commits)
	runner := gitrepo.Runner{}
	for index, commit := range commits {
		if commit == "" || (index > 0 && commits[index-1] == commit) {
			continue
		}
		if _, err := runner.Run(ctx, repository, "cat-file", "-e", commit+"^{commit}"); err != nil {
			return fmt.Errorf("commit=%s reachable=false error=%v", commit, err)
		}
		refs, err := runner.Run(ctx, repository, "for-each-ref", "--format=%(refname)", "--contains", commit)
		if err != nil {
			return fmt.Errorf("commit=%s retained_ref_query=%v", commit, err)
		}
		if strings.TrimSpace(refs) == "" {
			return fmt.Errorf("commit=%s object_exists=true retained_by_ref=false", commit)
		}
	}
	return nil
}

func assertAttributionFacts(ctx context.Context, db *sql.DB, trackerPath string) error {
	providerStore, err := fake.Open(trackerPath)
	if err != nil {
		return err
	}
	provider := fake.NewProvider(providerStore)
	rows, err := db.QueryContext(ctx, `SELECT operation_id,work_key,marker,prior_revision,prior_value,applied_revision,applied_value,compensation_status,compensation_revision,conflict_revision,conflict_value,conflict_marker FROM external_effects ORDER BY operation_id`)
	if err != nil {
		return err
	}
	allowedByWork := map[string]map[string]bool{}
	pendingByWork := map[string]map[string]pendingAttributionFact{}
	operationsByWork := map[string][]string{}
	for rows.Next() {
		var operation, workKey, marker, priorValue, appliedValue, status, conflictValue, conflictMarker string
		var prior, applied, compensation, conflict int64
		if err := rows.Scan(&operation, &workKey, &marker, &prior, &priorValue, &applied, &appliedValue, &status, &compensation, &conflict, &conflictValue, &conflictMarker); err != nil {
			return err
		}
		allowed := allowedByWork[workKey]
		if allowed == nil {
			allowed = map[string]bool{}
			allowedByWork[workKey] = allowed
		}
		operationsByWork[workKey] = append(operationsByWork[workKey], operation)
		allowed[providerFactKey(prior, priorValue, "")] = true
		if applied > 0 {
			allowed[providerFactKey(applied, appliedValue, marker)] = true
		} else if status == string(execution.CompensationPending) && compensation == 0 && conflict == 0 {
			pending := pendingByWork[workKey]
			if pending == nil {
				pending = map[string]pendingAttributionFact{}
				pendingByWork[workKey] = pending
			}
			pending[marker] = pendingAttributionFact{PriorRevision: prior, Assignee: appliedValue}
		}
		if compensation > 0 {
			allowed[providerFactKey(compensation, priorValue, "")] = true
		}
		if conflict > 0 {
			allowed[providerFactKey(conflict, conflictValue, conflictMarker)] = true
		}
		observations, err := db.QueryContext(ctx, `SELECT provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence`, operation)
		if err != nil {
			return err
		}
		for observations.Next() {
			var revision int64
			var value, observedMarker string
			if err := observations.Scan(&revision, &value, &observedMarker); err != nil {
				_ = observations.Close()
				return err
			}
			allowed[providerFactKey(revision, value, observedMarker)] = true
		}
		if err := observations.Close(); err != nil {
			return err
		}
		switch status {
		case string(execution.CompensationDone):
			if compensation <= applied || marker == "" || conflict != 0 {
				return fmt.Errorf("operation=%s marker=%s applied_revision=%d compensation_revision=%d conflict_revision=%d", operation, marker, applied, compensation, conflict)
			}
		case string(execution.CompensationAttributionLost):
			if conflict <= applied || strings.TrimSpace(conflictValue) == "" || conflictMarker == marker {
				return fmt.Errorf("operation=%s marker=%s applied_revision=%d conflict_revision=%d conflict_value=%q conflict_marker=%q", operation, marker, applied, conflict, conflictValue, conflictMarker)
			}
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for workKey, allowed := range allowedByWork {
		work, err := provider.Read(ctx, workKey)
		if err != nil {
			return fmt.Errorf("work=%s provider_read=%v", workKey, err)
		}
		current := providerFactKey(int64(work.Revision), work.Assignee, work.AssignmentMarker)
		pending, pendingOK := pendingByWork[workKey][work.AssignmentMarker]
		inFlight := pendingOK && work.AssignmentMarker != "" && work.Assignee == pending.Assignee && int64(work.Revision) > pending.PriorRevision
		if !allowed[current] && !inFlight {
			return fmt.Errorf("operations=%v work=%s provider_current=%s attributable_facts=%v", operationsByWork[workKey], workKey, current, allowed)
		}
	}
	return nil
}

func providerFactKey(revision int64, value, marker string) string {
	return fmt.Sprintf("%d/%s/%s", revision, value, marker)
}

func invariantFailure(state universalAssertionState, invariant string, cause error, recovery string) error {
	return fmt.Errorf("scenario_id=%s fault_id=%s invariant=%s state=%v recovery=%s", state.ScenarioID, state.FaultID, invariant, cause, recovery)
}
