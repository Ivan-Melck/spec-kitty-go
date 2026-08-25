package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func (s *Store) BindRepository(ctx context.Context, binding workspace.RepositoryBinding) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO repositories(
repository_id,project_id,common_dir,primary_path,managed_root,target_ref,primary_head,primary_index_digest,primary_tree_digest,created_at,verified_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`, binding.RepositoryID, binding.ProjectID, binding.CommonDir, binding.PrimaryPath, binding.ManagedRoot, binding.TargetRef,
		binding.Primary.Head, binding.Primary.IndexDigest, binding.Primary.TreeDigest, formatTime(binding.CreatedAt), formatTime(binding.VerifiedAt))
	if err == nil {
		return nil
	}
	existing, readErr := s.Repository(ctx, binding.RepositoryID)
	if readErr == nil && sameBinding(existing, binding) {
		return nil
	}
	observed := map[string]string{"repository_id": existing.RepositoryID.String(), "common_dir": existing.CommonDir, "primary_path": existing.PrimaryPath, "managed_root": existing.ManagedRoot}
	if readErr != nil {
		observed = map[string]string{"binding": "conflicting canonical repository identity"}
	}
	return &workspace.Error{Kind: workspace.KindBindingConflict, Message: "control store is already bound to different repository facts", Observed: observed, Expected: map[string]string{"repository_id": binding.RepositoryID.String(), "common_dir": binding.CommonDir, "primary_path": binding.PrimaryPath, "managed_root": binding.ManagedRoot}, Cause: err}
}

func (s *Store) Repository(ctx context.Context, repositoryID ids.RepositoryID) (workspace.RepositoryBinding, error) {
	var binding workspace.RepositoryBinding
	var created, verified string
	err := s.db.QueryRowContext(ctx, `SELECT repository_id,project_id,common_dir,primary_path,managed_root,target_ref,primary_head,primary_index_digest,primary_tree_digest,created_at,verified_at FROM repositories WHERE repository_id=?`, repositoryID).Scan(
		&binding.RepositoryID, &binding.ProjectID, &binding.CommonDir, &binding.PrimaryPath, &binding.ManagedRoot, &binding.TargetRef,
		&binding.Primary.Head, &binding.Primary.IndexDigest, &binding.Primary.TreeDigest, &created, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return binding, &workspace.Error{Kind: workspace.KindInvalid, Message: "repository binding was not found", Observed: map[string]string{"repository_id": repositoryID.String()}, Expected: map[string]string{"repository": "bound"}}
	}
	if err != nil {
		return binding, &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "read repository binding", Cause: err}
	}
	var parseErr error
	if binding.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, created); parseErr != nil {
		return binding, parseErr
	}
	if binding.VerifiedAt, parseErr = time.Parse(time.RFC3339Nano, verified); parseErr != nil {
		return binding, parseErr
	}
	return binding, nil
}

func (s *Store) WorkAuthority(ctx context.Context, revisionID ids.WorkRevisionID, workID ids.WorkID) (workspace.WorkAuthority, error) {
	revision, err := s.WorkRevision(ctx, revisionID)
	if err != nil {
		return workspace.WorkAuthority{}, &workspace.Error{Kind: workspace.KindInvalid, Message: "finalized WorkRevision was not found", Cause: err}
	}
	for _, item := range revision.WorkItems() {
		if item.ID == workID {
			return workspace.WorkAuthority{ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), LaneID: ids.LaneID(item.LaneID)}, nil
		}
	}
	return workspace.WorkAuthority{}, &workspace.Error{Kind: workspace.KindInvalid, Message: "work is not a member of the finalized WorkRevision", Observed: map[string]string{"work_id": workID.String(), "work_revision_id": revisionID.String()}, Expected: map[string]string{"membership": "finalized"}}
}

func (s *Store) ReserveWorkspace(ctx context.Context, reservation workspace.Reservation, canonicalPath string) (workspace.Record, error) {
	if err := reservation.Validate(); err != nil {
		return workspace.Record{}, err
	}
	binding, err := s.Repository(ctx, reservation.RepositoryID)
	if err != nil {
		return workspace.Record{}, err
	}
	if !containedWorkspacePath(canonicalPath, binding.ManagedRoot, reservation.WorkspaceID.String()) {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindPathUnsafe, Message: "reserved workspace path is not the canonical child of the managed root", Observed: map[string]string{"path": canonicalPath}, Expected: map[string]string{"managed_root": binding.ManagedRoot, "workspace_id": reservation.WorkspaceID.String()}}
	}
	dependencies, _ := workspace.CanonicalDependencies(reservation.Dependencies)
	encodedDependencies, _ := json.Marshal(dependencies)
	dependencyDigest := digestBytes(encodedDependencies)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workspace.Record{}, storeWorkspaceError("begin workspace reservation", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := verifyWorkspaceAuthority(ctx, tx, reservation.Authority, reservation.RunID, reservation.Now); err != nil {
		return workspace.Record{}, err
	}
	existing, found, err := loadWorkspace(ctx, tx, reservation.WorkspaceID)
	if err != nil {
		return workspace.Record{}, err
	}
	if found {
		if sameReservation(existing, reservation, canonicalPath, dependencyDigest) {
			return existing, nil
		}
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "workspace ID is permanently bound to a different lease or Git identity", Observed: reservationState(existing), Expected: reservationExpected(reservation, canonicalPath)}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspaces(
workspace_id,repository_id,operation_id,run_id,lease_id,lease_generation,lane_id,canonical_path,owned_branch,pinned_base,dependencies_json,dependency_digest,stage,status,registered,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'preparing',0,?,?)`, reservation.WorkspaceID, reservation.RepositoryID, reservation.OperationID, reservation.RunID,
		reservation.Authority.LeaseID, reservation.Authority.Generation, reservation.LaneID, canonicalPath, reservation.OwnedBranch, reservation.PinnedBase,
		encodedDependencies, dependencyDigest, workspace.StageReserved, formatTime(reservation.Now), formatTime(reservation.Now))
	if err != nil {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "workspace path, branch, operation, run, and lease must be unique", Observed: map[string]string{"reservation": "conflict"}, Expected: reservationExpected(reservation, canonicalPath), Cause: err}
	}
	if err := insertStage(ctx, tx, reservation.OperationID, workspace.StageReserved, map[string]string{"canonical_path": canonicalPath, "owned_branch": reservation.OwnedBranch, "pinned_base": reservation.PinnedBase}, reservation.Now); err != nil {
		return workspace.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return workspace.Record{}, storeWorkspaceError("commit workspace reservation", err)
	}
	return s.Workspace(ctx, reservation.WorkspaceID)
}

func (s *Store) Workspace(ctx context.Context, workspaceID ids.WorkspaceID) (workspace.Record, error) {
	record, found, err := loadWorkspace(ctx, s.db, workspaceID)
	if err != nil {
		return record, err
	}
	if !found {
		return record, &workspace.Error{Kind: workspace.KindInvalid, Message: "workspace was not found", Observed: map[string]string{"workspace_id": workspaceID.String()}, Expected: map[string]string{"workspace": "reserved"}}
	}
	return record, nil
}

func (s *Store) AdvanceWorkspace(ctx context.Context, workspaceID ids.WorkspaceID, authority workspace.Authority, expected, target workspace.Stage, probe workspace.Probe, dependencyHead string, now time.Time) (workspace.Record, error) {
	if !validTransition(expected, target) {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindInvalid, Message: "workspace stage transition is invalid", Observed: map[string]string{"stage": string(expected)}, Expected: map[string]string{"next_stage": string(target)}}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workspace.Record{}, storeWorkspaceError("begin workspace stage", err)
	}
	defer func() { _ = tx.Rollback() }()
	record, found, err := loadWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return workspace.Record{}, err
	}
	if !found {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindInvalid, Message: "workspace was not found"}
	}
	if record.Authority.LeaseID != authority.LeaseID || record.Authority.Generation != authority.Generation {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindStaleAuthority, Message: "workspace cannot be rebound to another lease generation", Observed: map[string]string{"lease_id": authority.LeaseID.String(), "generation": fmt.Sprint(authority.Generation)}, Expected: map[string]string{"lease_id": record.Authority.LeaseID.String(), "generation": fmt.Sprint(record.Authority.Generation)}}
	}
	if err := verifyWorkspaceAuthority(ctx, tx, authority, record.RunID, now); err != nil {
		return workspace.Record{}, err
	}
	if record.Stage != expected {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "workspace stage changed concurrently", Observed: map[string]string{"stage": string(record.Stage)}, Expected: map[string]string{"stage": string(expected)}}
	}
	status := "preparing"
	if target == workspace.StageReady {
		status = "ready"
	}
	result, err := tx.ExecContext(ctx, `UPDATE workspaces SET stage=?,status=?,registered=?,observed_head=?,dependency_head=CASE WHEN ?<>'' THEN ? ELSE dependency_head END,git_dir=CASE WHEN ?<>'' THEN ? ELSE git_dir END,index_path=CASE WHEN ?<>'' THEN ? ELSE index_path END,updated_at=? WHERE workspace_id=? AND stage=?`,
		target, status, boolInt(probe.Registered), probe.Head, dependencyHead, dependencyHead, probe.GitDir, probe.GitDir, probe.IndexPath, probe.IndexPath, formatTime(now), workspaceID, expected)
	if err != nil {
		return workspace.Record{}, storeWorkspaceError("advance workspace stage", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "workspace stage compare-and-swap failed", Cause: err}
	}
	observed := map[string]string{"registered": fmt.Sprint(probe.Registered), "head": probe.Head, "branch": probe.Branch, "git_dir": probe.GitDir, "index_path": probe.IndexPath, "clean": fmt.Sprint(probe.Clean)}
	if dependencyHead != "" {
		observed["dependency_head"] = dependencyHead
	}
	if err := insertStage(ctx, tx, record.OperationID, target, observed, now); err != nil {
		return workspace.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return workspace.Record{}, storeWorkspaceError("commit workspace stage", err)
	}
	return s.Workspace(ctx, workspaceID)
}

func (s *Store) QuarantineWorkspace(ctx context.Context, workspaceID ids.WorkspaceID, authority workspace.Authority, reason string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storeWorkspaceError("begin workspace quarantine", err)
	}
	defer func() { _ = tx.Rollback() }()
	record, found, err := loadWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	if !found {
		return &workspace.Error{Kind: workspace.KindInvalid, Message: "workspace was not found for quarantine"}
	}
	if record.Authority.LeaseID != authority.LeaseID || record.Authority.Generation != authority.Generation {
		return &workspace.Error{Kind: workspace.KindStaleAuthority, Message: "workspace quarantine requires its bound authority"}
	}
	if err := verifyWorkspaceAuthority(ctx, tx, authority, record.RunID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET status='quarantined',quarantine_reason=?,quarantined_at=?,updated_at=? WHERE workspace_id=?`, reason, formatTime(now), formatTime(now), workspaceID); err != nil {
		return storeWorkspaceError("quarantine workspace", err)
	}
	return tx.Commit()
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadWorkspace(ctx context.Context, queryer rowQueryer, workspaceID ids.WorkspaceID) (workspace.Record, bool, error) {
	var record workspace.Record
	var encoded []byte
	var registered int
	var created, updated, quarantined string
	err := queryer.QueryRowContext(ctx, `SELECT operation_id,run_id,workspace_id,repository_id,lane_id,lease_id,lease_generation,canonical_path,owned_branch,pinned_base,dependencies_json,dependency_head,stage,status,registered,observed_head,git_dir,index_path,created_at,updated_at,quarantine_reason,quarantined_at FROM workspaces WHERE workspace_id=?`, workspaceID).Scan(
		&record.OperationID, &record.RunID, &record.WorkspaceID, &record.RepositoryID, &record.LaneID, &record.Authority.LeaseID, &record.Authority.Generation,
		&record.CanonicalPath, &record.OwnedBranch, &record.PinnedBase, &encoded, &record.DependencyHead, &record.Stage, &record.Status, &registered,
		&record.ObservedHead, &record.GitDir, &record.IndexPath, &created, &updated, &record.QuarantineReason, &quarantined)
	if errors.Is(err, sql.ErrNoRows) {
		return record, false, nil
	}
	if err != nil {
		return record, false, storeWorkspaceError("read workspace", err)
	}
	if err := json.Unmarshal(encoded, &record.Dependencies); err != nil {
		return record, false, fmt.Errorf("decode workspace dependencies: %w", err)
	}
	record.Registered = registered == 1
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err == nil {
		record.Now = record.CreatedAt
		record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	}
	if err == nil && quarantined != "" {
		record.QuarantinedAt, err = time.Parse(time.RFC3339Nano, quarantined)
	}
	return record, true, err
}

func verifyWorkspaceAuthority(ctx context.Context, tx *sql.Tx, authority workspace.Authority, runID ids.RunID, now time.Time) error {
	var generation int64
	var digest, status, deadline string
	err := tx.QueryRowContext(ctx, `SELECT generation,token_digest,status,deadline FROM leases WHERE lease_id=? AND run_id=?`, authority.LeaseID, runID).Scan(&generation, &digest, &status, &deadline)
	if err != nil {
		return &workspace.Error{Kind: workspace.KindStaleAuthority, Message: "workspace lease was not found", Cause: err}
	}
	deadlineTime, parseErr := time.Parse(time.RFC3339Nano, deadline)
	if parseErr != nil {
		return &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "workspace lease deadline is corrupt", Cause: parseErr}
	}
	if generation != authority.Generation || digest != tokenDigest(authority.Token) || status != "active" || !deadlineTime.After(now.UTC()) {
		return &workspace.Error{Kind: workspace.KindStaleAuthority, Message: "workspace mutation requires the current live fencing token", Observed: map[string]string{"generation": fmt.Sprint(authority.Generation), "status": status}, Expected: map[string]string{"generation": fmt.Sprint(generation), "status": "active"}}
	}
	return nil
}

func insertStage(ctx context.Context, tx *sql.Tx, operationID ids.OperationID, stage workspace.Stage, observed map[string]string, now time.Time) error {
	payload, _ := json.Marshal(observed)
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_stages(operation_id,stage,observed_state,occurred_at) VALUES(?,?,?,?)`, operationID, stage, payload, formatTime(now)); err != nil {
		return storeWorkspaceError("record operation stage", err)
	}
	return nil
}

func validTransition(expected, target workspace.Stage) bool {
	return (expected == workspace.StageReserved && target == workspace.StageWorktreeCreated) ||
		(expected == workspace.StageWorktreeCreated && target == workspace.StageBaseVerified) ||
		(expected == workspace.StageBaseVerified && target == workspace.StageDependenciesPrepared) ||
		(expected == workspace.StageDependenciesPrepared && target == workspace.StageOwnershipVerified) ||
		(expected == workspace.StageOwnershipVerified && target == workspace.StageReady)
}

func containedWorkspacePath(path, root, workspaceID string) bool {
	return filepath.Clean(path) == filepath.Join(filepath.Clean(root), workspaceID) && filepath.Dir(filepath.Clean(path)) == filepath.Clean(root)
}

func sameBinding(left, right workspace.RepositoryBinding) bool {
	left.CreatedAt, left.VerifiedAt, right.CreatedAt, right.VerifiedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return reflect.DeepEqual(left, right)
}

func sameReservation(record workspace.Record, reservation workspace.Reservation, path, dependencyDigest string) bool {
	encoded, _ := json.Marshal(record.Dependencies)
	return record.OperationID == reservation.OperationID && record.RunID == reservation.RunID && record.RepositoryID == reservation.RepositoryID && record.LaneID == reservation.LaneID &&
		record.Authority.LeaseID == reservation.Authority.LeaseID && record.Authority.Generation == reservation.Authority.Generation && record.CanonicalPath == path &&
		record.OwnedBranch == reservation.OwnedBranch && record.PinnedBase == reservation.PinnedBase && digestBytes(encoded) == dependencyDigest
}

func reservationState(record workspace.Record) map[string]string {
	return map[string]string{"operation_id": record.OperationID.String(), "run_id": record.RunID.String(), "lease_id": record.Authority.LeaseID.String(), "generation": fmt.Sprint(record.Authority.Generation), "repository_id": record.RepositoryID.String(), "path": record.CanonicalPath, "branch": record.OwnedBranch, "base": record.PinnedBase}
}

func reservationExpected(reservation workspace.Reservation, path string) map[string]string {
	return map[string]string{"operation_id": reservation.OperationID.String(), "run_id": reservation.RunID.String(), "lease_id": reservation.Authority.LeaseID.String(), "generation": fmt.Sprint(reservation.Authority.Generation), "repository_id": reservation.RepositoryID.String(), "path": path, "branch": reservation.OwnedBranch, "base": reservation.PinnedBase}
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func storeWorkspaceError(message string, cause error) error {
	kind := workspace.KindWorkspaceConflict
	if strings.Contains(strings.ToLower(cause.Error()), "foreign key") {
		kind = workspace.KindInvalid
	}
	return &workspace.Error{Kind: kind, Message: message, Cause: cause}
}
