package sqlite

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
)

const currentSchemaVersion = 12

var migrations = []string{
	`CREATE TABLE projects (
  project_id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL
);
CREATE TABLE work_revisions (
  work_revision_id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(project_id),
  mission_id TEXT NOT NULL,
  revision_number INTEGER NOT NULL,
  finalized_at TEXT NOT NULL,
  UNIQUE(project_id, mission_id, revision_number),
  UNIQUE(work_revision_id, project_id, mission_id)
);
CREATE TABLE work_items (
  work_id TEXT NOT NULL,
  work_revision_id TEXT NOT NULL REFERENCES work_revisions(work_revision_id),
  state TEXT NOT NULL CHECK(state IN ('ready', 'blocked', 'done')),
  PRIMARY KEY(work_id, work_revision_id)
);
CREATE TABLE actors (
  actor_id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL
);
CREATE TABLE processes (
  process_id TEXT PRIMARY KEY,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id),
  observed_at TEXT NOT NULL,
  UNIQUE(process_id, actor_id)
);
CREATE TABLE operations (
  operation_id TEXT PRIMARY KEY,
  operation_kind TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  correlation_id TEXT NOT NULL,
  request_digest TEXT NOT NULL,
  status TEXT NOT NULL,
  result_run_id TEXT,
  result_lease_id TEXT,
  result_generation INTEGER,
  result_deadline TEXT,
  result_error_kind TEXT,
  result_error_payload TEXT,
  created_at TEXT NOT NULL,
  UNIQUE(operation_kind, idempotency_key)
);
CREATE TABLE runs (
  run_id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL UNIQUE REFERENCES operations(operation_id),
  project_id TEXT NOT NULL REFERENCES projects(project_id),
  mission_id TEXT NOT NULL,
  work_id TEXT NOT NULL,
  work_revision_id TEXT NOT NULL,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id),
  process_id TEXT NOT NULL REFERENCES processes(process_id),
  generation INTEGER NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(work_id, work_revision_id) REFERENCES work_items(work_id, work_revision_id),
  FOREIGN KEY(work_revision_id, project_id, mission_id) REFERENCES work_revisions(work_revision_id, project_id, mission_id),
  FOREIGN KEY(process_id, actor_id) REFERENCES processes(process_id, actor_id)
);
CREATE TABLE leases (
  lease_id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL UNIQUE REFERENCES runs(run_id),
  work_id TEXT NOT NULL,
  work_revision_id TEXT NOT NULL,
  generation INTEGER NOT NULL,
  token_digest TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('active', 'expired', 'surrendered')),
  heartbeat_at TEXT NOT NULL,
  deadline TEXT NOT NULL,
  FOREIGN KEY(work_id, work_revision_id) REFERENCES work_items(work_id, work_revision_id),
  UNIQUE(work_id, work_revision_id, generation)
);
CREATE UNIQUE INDEX one_active_implementation_lease
ON leases(work_id, work_revision_id) WHERE status = 'active';
CREATE TABLE events (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  operation_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  occurred_at TEXT NOT NULL
);`,
	`CREATE INDEX events_by_operation ON events(operation_id, sequence);`,
	`ALTER TABLE operations ADD COLUMN result_token_envelope BLOB;`,
	`CREATE TABLE external_effects (
  operation_id TEXT PRIMARY KEY,
  schema_version INTEGER NOT NULL,
  correlation_id TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  process_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  work_key TEXT NOT NULL,
  marker TEXT NOT NULL,
  prior_revision INTEGER NOT NULL,
  prior_value TEXT NOT NULL,
  applied_revision INTEGER NOT NULL,
  applied_value TEXT NOT NULL,
  response_status TEXT NOT NULL,
  compensation_status TEXT NOT NULL,
  compensation_revision INTEGER NOT NULL,
  conflict_revision INTEGER NOT NULL,
  conflict_value TEXT NOT NULL,
  conflict_marker TEXT NOT NULL,
  observed_at TEXT NOT NULL
);
CREATE TABLE external_effect_observations (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  operation_id TEXT NOT NULL REFERENCES external_effects(operation_id),
  response_status TEXT NOT NULL,
  compensation_status TEXT NOT NULL,
  provider_revision INTEGER NOT NULL,
  provider_value TEXT NOT NULL,
  provider_marker TEXT NOT NULL,
  observed_at TEXT NOT NULL
);`,
	workGraphMigrationV5,
	workGraphMigrationV6,
	startRunMigrationV7,
	startRecoveryMigrationV8,
	handoffReviewMigrationV9,
	integrationMigrationV10,
	handoffRefusalMigrationV11,
	governanceContextMigrationV12,
}

const governanceContextMigrationV12 = `
CREATE TABLE governance_contexts (
  context_id TEXT PRIMARY KEY,
  schema_version INTEGER NOT NULL CHECK(schema_version > 0),
  aggregate_algorithm TEXT NOT NULL CHECK(aggregate_algorithm <> ''),
  aggregate_digest TEXT NOT NULL CHECK(aggregate_digest <> ''),
  created_at TEXT NOT NULL CHECK(created_at <> '')
);
CREATE TABLE governance_context_entries (
  context_id TEXT NOT NULL REFERENCES governance_contexts(context_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  position INTEGER NOT NULL CHECK(position >= 0),
  kind TEXT NOT NULL CHECK(kind <> ''),
  entry_ref TEXT NOT NULL CHECK(entry_ref <> ''),
  revision TEXT NOT NULL CHECK(revision <> ''),
  commitment_algorithm TEXT NOT NULL CHECK(commitment_algorithm <> ''),
  commitment_digest TEXT NOT NULL CHECK(commitment_digest <> ''),
  PRIMARY KEY(context_id, position),
  UNIQUE(context_id, kind, entry_ref)
);
ALTER TABLE runs ADD COLUMN governance_context_id TEXT REFERENCES governance_contexts(context_id) ON DELETE RESTRICT;
CREATE INDEX runs_by_governance_context ON runs(governance_context_id);
CREATE TRIGGER sealed_governance_context_entry_insert
BEFORE INSERT ON governance_context_entries
WHEN EXISTS(SELECT 1 FROM governance_contexts WHERE context_id=NEW.context_id)
BEGIN SELECT RAISE(ABORT,'governance context entry set is sealed'); END;
CREATE TRIGGER immutable_governance_context_update
BEFORE UPDATE ON governance_contexts BEGIN SELECT RAISE(ABORT,'governance context is immutable'); END;
CREATE TRIGGER immutable_governance_context_delete
BEFORE DELETE ON governance_contexts BEGIN SELECT RAISE(ABORT,'governance context is immutable'); END;
CREATE TRIGGER immutable_governance_context_entry_update
BEFORE UPDATE ON governance_context_entries BEGIN SELECT RAISE(ABORT,'governance context entry is immutable'); END;
CREATE TRIGGER immutable_governance_context_entry_delete
BEFORE DELETE ON governance_context_entries BEGIN SELECT RAISE(ABORT,'governance context entry is immutable'); END;
CREATE TRIGGER require_governance_context_on_new_run
BEFORE INSERT ON runs
WHEN NEW.governance_context_id IS NULL OR NEW.governance_context_id=''
BEGIN SELECT RAISE(ABORT,'new run requires governance context'); END;
CREATE TRIGGER immutable_run_governance_context_update
BEFORE UPDATE OF governance_context_id ON runs
WHEN NEW.governance_context_id IS NOT OLD.governance_context_id
BEGIN SELECT RAISE(ABORT,'run governance context binding is immutable'); END;`

const integrationMigrationV10 = `
CREATE TABLE integration_commands (
  operation_id TEXT PRIMARY KEY,
  request_digest TEXT NOT NULL,
  review_id TEXT NOT NULL REFERENCES reviews(review_id) ON DELETE RESTRICT,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id) ON DELETE RESTRICT,
  process_id TEXT NOT NULL REFERENCES processes(process_id) ON DELETE RESTRICT,
  token_envelope BLOB NOT NULL,
  occurred_at TEXT NOT NULL
);
CREATE TABLE integration_mutation_leases (
  operation_id TEXT PRIMARY KEY REFERENCES integration_commands(operation_id) ON DELETE RESTRICT,
  repository_id TEXT NOT NULL REFERENCES repositories(repository_id) ON DELETE RESTRICT,
  target_ref TEXT NOT NULL,
  generation INTEGER NOT NULL CHECK(generation > 0),
  token_digest TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('pending','active','expired','released')),
  deadline TEXT NOT NULL,
  acquired_at TEXT NOT NULL,
	fence_ref TEXT NOT NULL DEFAULT '',
	fence_object TEXT NOT NULL DEFAULT '',
  UNIQUE(repository_id,target_ref,generation)
);
CREATE UNIQUE INDEX one_active_target_mutation_lease
ON integration_mutation_leases(repository_id,target_ref) WHERE status IN ('pending','active');
CREATE TABLE integrations (
  operation_id TEXT PRIMARY KEY REFERENCES integration_commands(operation_id) ON DELETE RESTRICT,
  review_id TEXT NOT NULL REFERENCES reviews(review_id) ON DELETE RESTRICT,
  handoff_id TEXT NOT NULL REFERENCES handoffs(handoff_id) ON DELETE RESTRICT,
  mission_id TEXT NOT NULL,
  work_revision_id TEXT NOT NULL,
  work_id TEXT NOT NULL,
  repository_id TEXT NOT NULL REFERENCES repositories(repository_id) ON DELETE RESTRICT,
  target_ref TEXT NOT NULL,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id) ON DELETE RESTRICT,
  process_id TEXT NOT NULL REFERENCES processes(process_id) ON DELETE RESTRICT,
  candidate TEXT NOT NULL,
  preflight_target TEXT NOT NULL,
  workspace_path TEXT NOT NULL DEFAULT '',
  result_commit TEXT NOT NULL DEFAULT '',
  observed_target TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK(status IN ('authorizing','prepared','result_ready','done','repair_required')),
  repair_kind TEXT NOT NULL DEFAULT '',
  repair_action TEXT NOT NULL DEFAULT '',
  evidence_json BLOB NOT NULL DEFAULT '{}',
  started_at TEXT NOT NULL,
  done_at TEXT NOT NULL DEFAULT '',
	cleanup_status TEXT NOT NULL DEFAULT 'pending' CHECK(cleanup_status IN ('pending','eligible','complete')),
	cleanup_eligible_at TEXT NOT NULL DEFAULT '',
	cleanup_completed_at TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(work_id,work_revision_id) REFERENCES work_items(work_id,work_revision_id)
);
CREATE TABLE integration_done (
  review_id TEXT PRIMARY KEY REFERENCES reviews(review_id) ON DELETE RESTRICT,
  operation_id TEXT NOT NULL UNIQUE REFERENCES integrations(operation_id) ON DELETE RESTRICT,
  repository_id TEXT NOT NULL,
  target_ref TEXT NOT NULL,
  candidate TEXT NOT NULL,
  result_commit TEXT NOT NULL,
  observed_target TEXT NOT NULL,
  done_at TEXT NOT NULL
);
CREATE TRIGGER immutable_integration_binding_update
BEFORE UPDATE OF review_id,handoff_id,mission_id,work_revision_id,work_id,repository_id,target_ref,actor_id,process_id,candidate,preflight_target,started_at ON integrations
BEGIN SELECT RAISE(ABORT,'integration binding is immutable'); END;
CREATE TRIGGER immutable_integration_binding_delete BEFORE DELETE ON integrations BEGIN SELECT RAISE(ABORT,'integration binding is immutable'); END;
CREATE TRIGGER immutable_integration_done_update BEFORE UPDATE ON integration_done BEGIN SELECT RAISE(ABORT,'integration done fact is immutable'); END;
CREATE TRIGGER immutable_integration_done_delete BEFORE DELETE ON integration_done BEGIN SELECT RAISE(ABORT,'integration done fact is immutable'); END;`

const handoffRefusalMigrationV11 = `
CREATE TABLE handoff_refusals (
  operation_id TEXT PRIMARY KEY,
  request_digest TEXT NOT NULL,
  error_kind TEXT NOT NULL CHECK(error_kind IN ('candidate_mismatch')),
  message TEXT NOT NULL,
  observed_json BLOB NOT NULL,
  expected_json BLOB NOT NULL,
  recovery_actions_json BLOB NOT NULL,
  occurred_at TEXT NOT NULL
);
CREATE TRIGGER immutable_handoff_refusal_update BEFORE UPDATE ON handoff_refusals BEGIN SELECT RAISE(ABORT,'handoff refusal is immutable'); END;
CREATE TRIGGER immutable_handoff_refusal_delete BEFORE DELETE ON handoff_refusals BEGIN SELECT RAISE(ABORT,'handoff refusal is immutable'); END;`

const handoffReviewMigrationV9 = `
CREATE TABLE handoff_commands (
  operation_id TEXT PRIMARY KEY,
  request_digest TEXT NOT NULL,
  result_handoff_id TEXT NOT NULL,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id),
  process_id TEXT NOT NULL REFERENCES processes(process_id),
  occurred_at TEXT NOT NULL
);
CREATE TABLE handoffs (
  handoff_id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL UNIQUE REFERENCES handoff_commands(operation_id) ON DELETE RESTRICT,
  handoff_generation INTEGER NOT NULL CHECK(handoff_generation > 0),
  project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE RESTRICT,
  mission_id TEXT NOT NULL,
  work_revision_id TEXT NOT NULL REFERENCES work_graph_revisions(work_revision_id) ON DELETE RESTRICT,
  work_id TEXT NOT NULL,
  run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE RESTRICT,
  workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id) ON DELETE RESTRICT,
  repository_id TEXT NOT NULL REFERENCES repositories(repository_id) ON DELETE RESTRICT,
  lease_id TEXT NOT NULL REFERENCES leases(lease_id) ON DELETE RESTRICT,
  lease_generation INTEGER NOT NULL CHECK(lease_generation > 0),
  lane_id TEXT NOT NULL,
  candidate TEXT NOT NULL,
  pinned_base TEXT NOT NULL,
  contributor_digest TEXT NOT NULL,
  criteria_digest TEXT NOT NULL,
  evidence_digest TEXT NOT NULL,
  verification_json BLOB NOT NULL,
  submitted_at TEXT NOT NULL,
  FOREIGN KEY(work_id,work_revision_id) REFERENCES work_items(work_id,work_revision_id),
  UNIQUE(work_revision_id,work_id,handoff_generation),
  UNIQUE(work_revision_id,work_id,candidate)
);
CREATE TABLE handoff_contributors (
  handoff_id TEXT NOT NULL REFERENCES handoffs(handoff_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id) ON DELETE RESTRICT,
  PRIMARY KEY(handoff_id,actor_id)
);
CREATE TABLE review_attempts (
  attempt_id INTEGER PRIMARY KEY AUTOINCREMENT,
  operation_id TEXT NOT NULL,
  request_digest TEXT NOT NULL,
  handoff_id TEXT NOT NULL REFERENCES handoffs(handoff_id) ON DELETE RESTRICT,
  review_id TEXT NOT NULL,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id) ON DELETE RESTRICT,
  process_id TEXT NOT NULL REFERENCES processes(process_id) ON DELETE RESTRICT,
  candidate TEXT NOT NULL,
  verdict TEXT NOT NULL CHECK(verdict IN ('approved','changes_requested')),
  outcome TEXT NOT NULL CHECK(outcome IN ('submitted')),
  occurred_at TEXT NOT NULL,
  UNIQUE(operation_id)
);
CREATE TABLE reviews (
  review_id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL UNIQUE,
  request_digest TEXT NOT NULL,
  handoff_id TEXT NOT NULL UNIQUE REFERENCES handoffs(handoff_id) ON DELETE RESTRICT,
  work_revision_id TEXT NOT NULL,
  work_id TEXT NOT NULL,
  candidate TEXT NOT NULL,
  reviewer_actor_id TEXT NOT NULL REFERENCES actors(actor_id) ON DELETE RESTRICT,
  reviewer_process_id TEXT NOT NULL REFERENCES processes(process_id) ON DELETE RESTRICT,
  verdict TEXT NOT NULL CHECK(verdict IN ('approved','changes_requested')),
  evidence_digest TEXT NOT NULL,
  submitted_at TEXT NOT NULL,
  FOREIGN KEY(work_id,work_revision_id) REFERENCES work_items(work_id,work_revision_id)
);
CREATE TABLE review_criteria (
  review_id TEXT NOT NULL REFERENCES reviews(review_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  criterion_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('passed','failed')),
  evidence TEXT NOT NULL,
  PRIMARY KEY(review_id,criterion_id)
);
CREATE TRIGGER immutable_handoff_command_update BEFORE UPDATE ON handoff_commands BEGIN SELECT RAISE(ABORT,'handoff command is immutable'); END;
CREATE TRIGGER immutable_handoff_command_delete BEFORE DELETE ON handoff_commands BEGIN SELECT RAISE(ABORT,'handoff command is immutable'); END;
CREATE TRIGGER immutable_handoff_update BEFORE UPDATE ON handoffs BEGIN SELECT RAISE(ABORT,'handoff is immutable'); END;
CREATE TRIGGER immutable_handoff_delete BEFORE DELETE ON handoffs BEGIN SELECT RAISE(ABORT,'handoff is immutable'); END;
CREATE TRIGGER sealed_handoff_contributor_insert BEFORE INSERT ON handoff_contributors WHEN EXISTS(SELECT 1 FROM handoffs WHERE handoff_id=NEW.handoff_id) BEGIN SELECT RAISE(ABORT,'handoff contributor set is sealed'); END;
CREATE TRIGGER immutable_handoff_contributor_update BEFORE UPDATE ON handoff_contributors BEGIN SELECT RAISE(ABORT,'handoff contributor is immutable'); END;
CREATE TRIGGER immutable_handoff_contributor_delete BEFORE DELETE ON handoff_contributors BEGIN SELECT RAISE(ABORT,'handoff contributor is immutable'); END;
CREATE TRIGGER immutable_review_attempt_update BEFORE UPDATE ON review_attempts BEGIN SELECT RAISE(ABORT,'review attempt is immutable'); END;
CREATE TRIGGER immutable_review_attempt_delete BEFORE DELETE ON review_attempts BEGIN SELECT RAISE(ABORT,'review attempt is immutable'); END;
CREATE TRIGGER immutable_review_update BEFORE UPDATE ON reviews BEGIN SELECT RAISE(ABORT,'review is immutable'); END;
CREATE TRIGGER immutable_review_delete BEFORE DELETE ON reviews BEGIN SELECT RAISE(ABORT,'review is immutable'); END;
CREATE TRIGGER sealed_review_criterion_insert BEFORE INSERT ON review_criteria WHEN EXISTS(SELECT 1 FROM reviews WHERE review_id=NEW.review_id) BEGIN SELECT RAISE(ABORT,'review criterion set is sealed'); END;
CREATE TRIGGER immutable_review_criterion_update BEFORE UPDATE ON review_criteria BEGIN SELECT RAISE(ABORT,'review criterion is immutable'); END;
CREATE TRIGGER immutable_review_criterion_delete BEFORE DELETE ON review_criteria BEGIN SELECT RAISE(ABORT,'review criterion is immutable'); END;`

const startRunMigrationV7 = `
CREATE TABLE repositories (
  repository_id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE RESTRICT,
  common_dir TEXT NOT NULL UNIQUE,
  primary_path TEXT NOT NULL UNIQUE,
  managed_root TEXT NOT NULL UNIQUE,
  target_ref TEXT NOT NULL,
  primary_head TEXT NOT NULL,
  primary_index_digest TEXT NOT NULL,
  primary_tree_digest TEXT NOT NULL,
  created_at TEXT NOT NULL,
  verified_at TEXT NOT NULL
);
CREATE TABLE workspaces (
  workspace_id TEXT PRIMARY KEY,
  repository_id TEXT NOT NULL REFERENCES repositories(repository_id) ON DELETE RESTRICT,
  operation_id TEXT NOT NULL UNIQUE REFERENCES operations(operation_id) ON DELETE RESTRICT,
  run_id TEXT NOT NULL UNIQUE REFERENCES runs(run_id) ON DELETE RESTRICT,
  lease_id TEXT NOT NULL UNIQUE REFERENCES leases(lease_id) ON DELETE RESTRICT,
  lease_generation INTEGER NOT NULL CHECK(lease_generation > 0),
  lane_id TEXT NOT NULL,
  canonical_path TEXT NOT NULL,
  owned_branch TEXT NOT NULL,
  pinned_base TEXT NOT NULL,
  dependencies_json BLOB NOT NULL,
  dependency_digest TEXT NOT NULL,
  dependency_head TEXT NOT NULL DEFAULT '',
  stage TEXT NOT NULL CHECK(stage IN ('reserved','worktree_created','base_verified','dependencies_prepared','ownership_verified','ready')),
  status TEXT NOT NULL CHECK(status IN ('preparing','ready','blocked','quarantined')),
  registered INTEGER NOT NULL CHECK(registered IN (0,1)),
  observed_head TEXT NOT NULL DEFAULT '',
  git_dir TEXT NOT NULL DEFAULT '',
  index_path TEXT NOT NULL DEFAULT '',
  quarantine_reason TEXT NOT NULL DEFAULT '',
  quarantined_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK((status='quarantined' AND quarantine_reason<>'' AND quarantined_at<>'') OR (status<>'quarantined' AND quarantine_reason='' AND quarantined_at='')),
  UNIQUE(repository_id, canonical_path),
  UNIQUE(repository_id, owned_branch)
);
CREATE TABLE operation_stages (
  operation_id TEXT NOT NULL REFERENCES operations(operation_id) ON DELETE RESTRICT,
  stage TEXT NOT NULL,
  observed_state BLOB NOT NULL,
  occurred_at TEXT NOT NULL,
  PRIMARY KEY(operation_id, stage)
);
CREATE TRIGGER immutable_repository_binding_update
BEFORE UPDATE ON repositories BEGIN
  SELECT RAISE(ABORT, 'repository binding is immutable');
END;
CREATE TRIGGER immutable_repository_binding_delete
BEFORE DELETE ON repositories BEGIN
  SELECT RAISE(ABORT, 'repository binding is immutable');
END;
CREATE TRIGGER immutable_workspace_binding_update
BEFORE UPDATE OF workspace_id,repository_id,operation_id,run_id,lease_id,lease_generation,lane_id,canonical_path,owned_branch,pinned_base,dependencies_json,dependency_digest ON workspaces BEGIN
  SELECT RAISE(ABORT, 'workspace lease and repository binding is immutable');
END;
CREATE TRIGGER immutable_workspace_binding_delete
BEFORE DELETE ON workspaces BEGIN
  SELECT RAISE(ABORT, 'workspace binding is immutable');
END;
CREATE TRIGGER monotonic_workspace_stage_update
BEFORE UPDATE OF stage ON workspaces
WHEN CASE OLD.stage
  WHEN 'reserved' THEN NEW.stage <> 'worktree_created'
  WHEN 'worktree_created' THEN NEW.stage <> 'base_verified'
  WHEN 'base_verified' THEN NEW.stage <> 'dependencies_prepared'
  WHEN 'dependencies_prepared' THEN NEW.stage <> 'ownership_verified'
  WHEN 'ownership_verified' THEN NEW.stage <> 'ready'
  ELSE 1
END
BEGIN SELECT RAISE(ABORT, 'workspace stage transition is not monotonic'); END;
CREATE TRIGGER terminal_workspace_status_update
BEFORE UPDATE OF status ON workspaces
WHEN OLD.status IN ('blocked','quarantined') AND NEW.status<>OLD.status
BEGIN SELECT RAISE(ABORT, 'terminal workspace status is immutable'); END;
CREATE TRIGGER immutable_workspace_quarantine_evidence
BEFORE UPDATE OF quarantine_reason,quarantined_at ON workspaces
WHEN OLD.quarantine_reason<>'' OR OLD.quarantined_at<>''
BEGIN SELECT RAISE(ABORT, 'workspace quarantine evidence is immutable'); END;
CREATE TRIGGER immutable_operation_stage_update
BEFORE UPDATE ON operation_stages BEGIN
  SELECT RAISE(ABORT, 'operation stage observation is immutable');
END;
CREATE TRIGGER immutable_operation_stage_delete
BEFORE DELETE ON operation_stages BEGIN
  SELECT RAISE(ABORT, 'operation stage observation is immutable');
END;`

const workGraphMigrationV5 = `
CREATE TABLE work_graph_revisions (
  work_revision_id TEXT PRIMARY KEY REFERENCES work_revisions(work_revision_id) ON DELETE RESTRICT,
  graph_digest TEXT NOT NULL,
  criteria_digest TEXT NOT NULL,
  author_actor_id TEXT NOT NULL REFERENCES actors(actor_id),
  encoded_revision BLOB NOT NULL
);
CREATE TABLE work_graph_items (
  work_revision_id TEXT NOT NULL REFERENCES work_graph_revisions(work_revision_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  work_id TEXT NOT NULL,
  PRIMARY KEY(work_revision_id, work_id)
);
CREATE TABLE reconciliations (
  reconciliation_id TEXT PRIMARY KEY,
  request_digest TEXT NOT NULL,
  old_work_revision_id TEXT NOT NULL REFERENCES work_graph_revisions(work_revision_id),
  new_work_revision_id TEXT NOT NULL REFERENCES work_graph_revisions(work_revision_id),
  disposition TEXT NOT NULL CHECK(disposition IN ('continue','supersede','invalidate','fork')),
  old_graph_digest TEXT NOT NULL,
  new_graph_digest TEXT NOT NULL,
  old_criteria_digest TEXT NOT NULL,
  new_criteria_digest TEXT NOT NULL,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id),
  rationale TEXT NOT NULL,
  old_work_ids BLOB NOT NULL,
  new_work_ids BLOB NOT NULL,
  occurred_at TEXT NOT NULL
);
CREATE TABLE reconciliation_fork_edges (
  reconciliation_id TEXT NOT NULL REFERENCES reconciliations(reconciliation_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  from_old_work_id TEXT NOT NULL,
  to_new_work_id TEXT NOT NULL,
  PRIMARY KEY(reconciliation_id, from_old_work_id, to_new_work_id)
);
CREATE TABLE reconciliation_consequences (
  reconciliation_id TEXT NOT NULL REFERENCES reconciliations(reconciliation_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  work_id TEXT NOT NULL,
  work_status TEXT NOT NULL CHECK(work_status IN ('active','handed_off','approved','integrated')),
  run_may_continue INTEGER NOT NULL CHECK(run_may_continue IN (0,1)),
  handoff_eligible INTEGER NOT NULL CHECK(handoff_eligible IN (0,1)),
  integration_eligible INTEGER NOT NULL CHECK(integration_eligible IN (0,1)),
  expire_authority INTEGER NOT NULL CHECK(expire_authority IN (0,1)),
  quarantine_workspace INTEGER NOT NULL CHECK(quarantine_workspace IN (0,1)),
  preserves_integrated_history INTEGER NOT NULL CHECK(preserves_integrated_history IN (0,1)),
  reason TEXT NOT NULL,
  PRIMARY KEY(reconciliation_id, work_id, work_status)
);
CREATE TABLE workspace_quarantine_intents (
  reconciliation_id TEXT NOT NULL REFERENCES reconciliations(reconciliation_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  lease_id TEXT NOT NULL REFERENCES leases(lease_id) ON DELETE RESTRICT,
  work_id TEXT NOT NULL,
  work_revision_id TEXT NOT NULL,
  reason TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(reconciliation_id, lease_id)
);
CREATE TRIGGER immutable_work_revision_identity_update
BEFORE UPDATE ON work_revisions
WHEN EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=OLD.work_revision_id)
BEGIN SELECT RAISE(ABORT, 'finalized WorkRevision identity is immutable'); END;
CREATE TRIGGER immutable_work_revision_identity_delete
BEFORE DELETE ON work_revisions
WHEN EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=OLD.work_revision_id)
BEGIN SELECT RAISE(ABORT, 'finalized WorkRevision identity is immutable'); END;
CREATE TRIGGER immutable_work_graph_revision_update
BEFORE UPDATE ON work_graph_revisions BEGIN
  SELECT RAISE(ABORT, 'finalized WorkRevision is immutable');
END;
CREATE TRIGGER immutable_work_graph_revision_delete
BEFORE DELETE ON work_graph_revisions BEGIN
  SELECT RAISE(ABORT, 'finalized WorkRevision is immutable');
END;
CREATE TRIGGER immutable_work_graph_item_insert
BEFORE INSERT ON work_graph_items
WHEN EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=NEW.work_revision_id)
BEGIN SELECT RAISE(ABORT, 'finalized WorkRevision membership is immutable'); END;
CREATE TRIGGER immutable_work_graph_item_update
BEFORE UPDATE ON work_graph_items BEGIN
  SELECT RAISE(ABORT, 'finalized WorkRevision membership is immutable');
END;
CREATE TRIGGER immutable_work_graph_item_delete
BEFORE DELETE ON work_graph_items BEGIN
  SELECT RAISE(ABORT, 'finalized WorkRevision membership is immutable');
END;
CREATE TRIGGER enforce_canonical_work_item_insert
BEFORE INSERT ON work_items
WHEN EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=NEW.work_revision_id)
 AND NOT EXISTS(SELECT 1 FROM work_graph_items WHERE work_revision_id=NEW.work_revision_id AND work_id=NEW.work_id)
BEGIN SELECT RAISE(ABORT, 'WorkID is absent from canonical WorkRevision'); END;
CREATE TRIGGER immutable_canonical_work_item_identity_update
BEFORE UPDATE OF work_id,work_revision_id ON work_items
WHEN EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=OLD.work_revision_id)
  OR EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=NEW.work_revision_id)
BEGIN SELECT RAISE(ABORT, 'canonical WorkID membership is immutable'); END;
CREATE TRIGGER immutable_canonical_work_item_delete
BEFORE DELETE ON work_items
WHEN EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=OLD.work_revision_id)
BEGIN SELECT RAISE(ABORT, 'canonical WorkID membership is immutable'); END;
CREATE TRIGGER immutable_reconciliation_update
BEFORE UPDATE ON reconciliations BEGIN
  SELECT RAISE(ABORT, 'reconciliation evidence is immutable');
END;
CREATE TRIGGER immutable_reconciliation_delete
BEFORE DELETE ON reconciliations BEGIN
  SELECT RAISE(ABORT, 'reconciliation evidence is immutable');
END;
CREATE TRIGGER immutable_reconciliation_consequence_insert
BEFORE INSERT ON reconciliation_consequences
WHEN EXISTS(SELECT 1 FROM reconciliations WHERE reconciliation_id=NEW.reconciliation_id)
BEGIN SELECT RAISE(ABORT, 'reconciliation consequence is sealed'); END;
CREATE TRIGGER immutable_reconciliation_consequence_update
BEFORE UPDATE ON reconciliation_consequences BEGIN
  SELECT RAISE(ABORT, 'reconciliation consequence is immutable');
END;
CREATE TRIGGER immutable_reconciliation_consequence_delete
BEFORE DELETE ON reconciliation_consequences BEGIN
  SELECT RAISE(ABORT, 'reconciliation consequence is immutable');
END;
CREATE TRIGGER immutable_reconciliation_fork_edge_insert
BEFORE INSERT ON reconciliation_fork_edges
WHEN EXISTS(SELECT 1 FROM reconciliations WHERE reconciliation_id=NEW.reconciliation_id)
BEGIN SELECT RAISE(ABORT, 'reconciliation fork edge is sealed'); END;
CREATE TRIGGER immutable_reconciliation_fork_edge_update
BEFORE UPDATE ON reconciliation_fork_edges BEGIN
  SELECT RAISE(ABORT, 'reconciliation fork edge is immutable');
END;
CREATE TRIGGER immutable_reconciliation_fork_edge_delete
BEFORE DELETE ON reconciliation_fork_edges BEGIN
  SELECT RAISE(ABORT, 'reconciliation fork edge is immutable');
END;
CREATE TRIGGER immutable_quarantine_intent_insert
BEFORE INSERT ON workspace_quarantine_intents
WHEN EXISTS(SELECT 1 FROM reconciliations WHERE reconciliation_id=NEW.reconciliation_id)
BEGIN SELECT RAISE(ABORT, 'reconciliation quarantine intent is sealed'); END;
CREATE TRIGGER immutable_quarantine_intent_update
BEFORE UPDATE ON workspace_quarantine_intents BEGIN
  SELECT RAISE(ABORT, 'reconciliation quarantine intent is immutable');
END;
CREATE TRIGGER immutable_quarantine_intent_delete
BEFORE DELETE ON workspace_quarantine_intents BEGIN
  SELECT RAISE(ABORT, 'reconciliation quarantine intent is immutable');
END;
CREATE TRIGGER enforce_reconciliation_lease_insert
BEFORE INSERT ON leases
WHEN EXISTS(
  SELECT 1 FROM reconciliations r
  JOIN reconciliation_consequences c ON c.reconciliation_id=r.reconciliation_id
  WHERE r.old_work_revision_id=NEW.work_revision_id AND c.work_id=NEW.work_id
    AND c.work_status='active' AND c.handoff_eligible=0
)
BEGIN SELECT RAISE(ABORT, 'WorkRevision reconciliation forbids new authority'); END;
CREATE TRIGGER immutable_lease_generation_identity_update
BEFORE UPDATE OF run_id,work_id,work_revision_id,generation ON leases BEGIN
  SELECT RAISE(ABORT, 'lease generation identity is immutable');
END;
CREATE TRIGGER prevent_invalidated_lease_reactivation
BEFORE UPDATE OF status ON leases
WHEN NEW.status='active' AND OLD.status<>'active'
 AND EXISTS(SELECT 1 FROM workspace_quarantine_intents WHERE lease_id=OLD.lease_id)
BEGIN SELECT RAISE(ABORT, 'invalidated lease cannot be reactivated'); END;`

const workGraphMigrationV6 = `
CREATE TABLE IF NOT EXISTS work_graph_operations (
  operation_id TEXT PRIMARY KEY,
  operation_kind TEXT NOT NULL CHECK(operation_kind IN ('finalize_work_revision','reconcile_work_revision')),
  request_digest TEXT NOT NULL,
  schema_version INTEGER NOT NULL CHECK(schema_version=1),
  correlation_id TEXT NOT NULL,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id),
  process_id TEXT NOT NULL REFERENCES processes(process_id),
  occurred_at TEXT NOT NULL,
  result_work_revision_id TEXT REFERENCES work_graph_revisions(work_revision_id) ON DELETE RESTRICT,
  result_reconciliation_id TEXT REFERENCES reconciliations(reconciliation_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CHECK((operation_kind='finalize_work_revision' AND result_work_revision_id IS NOT NULL AND result_reconciliation_id IS NULL)
     OR (operation_kind='reconcile_work_revision' AND result_work_revision_id IS NULL AND result_reconciliation_id IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS one_finalize_operation_per_work_revision
ON work_graph_operations(result_work_revision_id) WHERE operation_kind='finalize_work_revision';
CREATE UNIQUE INDEX IF NOT EXISTS one_reconcile_operation_per_reconciliation
ON work_graph_operations(result_reconciliation_id) WHERE operation_kind='reconcile_work_revision';
CREATE TRIGGER IF NOT EXISTS terminal_canonical_work_item_state
BEFORE UPDATE OF state ON work_items
WHEN OLD.state='done' AND NEW.state<>OLD.state
 AND EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=OLD.work_revision_id)
BEGIN SELECT RAISE(ABORT, 'terminal canonical WorkID state is immutable'); END;
CREATE TRIGGER IF NOT EXISTS immutable_work_graph_operation_update
BEFORE UPDATE ON work_graph_operations BEGIN
  SELECT RAISE(ABORT, 'work graph operation result is immutable');
END;
CREATE TRIGGER IF NOT EXISTS immutable_work_graph_operation_delete
BEFORE DELETE ON work_graph_operations BEGIN
  SELECT RAISE(ABORT, 'work graph operation result is immutable');
END;
CREATE TRIGGER IF NOT EXISTS enforce_finalized_work_graph_lease_insert
BEFORE INSERT ON leases
WHEN NOT EXISTS(
  SELECT 1 FROM work_graph_items
  WHERE work_revision_id=NEW.work_revision_id AND work_id=NEW.work_id
)
BEGIN SELECT RAISE(ABORT, 'authority requires finalized canonical WorkRevision membership'); END;
DROP TRIGGER IF EXISTS prevent_invalidated_lease_reactivation;
CREATE TRIGGER IF NOT EXISTS prevent_terminal_lease_reactivation
BEFORE UPDATE OF status ON leases
WHEN NEW.status='active' AND OLD.status<>'active'
BEGIN SELECT RAISE(ABORT, 'terminal lease cannot be reactivated'); END;`

func migrate(ctx context.Context, db *sql.DB, tokenKey []byte) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("control database schema version %d is newer than supported version %d", version, currentSchemaVersion)
	}
	legacy, err := isLegacyV1(ctx, tx, version)
	if err != nil {
		return err
	}
	if legacy {
		if err := upgradeLegacyV1(ctx, tx, tokenKey); err != nil {
			return fmt.Errorf("upgrade legacy schema version 1: %w", err)
		}
	}
	for version < currentSchemaVersion {
		next := version + 1
		if next == 1 {
			if _, err := tx.ExecContext(ctx, migrations[0]); err != nil {
				return fmt.Errorf("apply schema migration 1: %w", err)
			}
		} else if next == 3 {
			hasEnvelope, err := tableHasColumn(ctx, tx, "operations", "result_token_envelope")
			if err != nil {
				return err
			}
			if !hasEnvelope {
				if _, err := tx.ExecContext(ctx, migrations[2]); err != nil {
					return fmt.Errorf("apply schema migration 3: %w", err)
				}
			}
		} else if _, err := tx.ExecContext(ctx, migrations[next-1]); err != nil {
			return fmt.Errorf("apply schema migration %d: %w", next, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", next)); err != nil {
			return fmt.Errorf("record schema migration %d: %w", next, err)
		}
		version = next
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	if legacy {
		if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return fmt.Errorf("checkpoint migrated database: %w", err)
		}
		if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
			return fmt.Errorf("vacuum migrated database: %w", err)
		}
	}
	return nil
}

func isLegacyV1(ctx context.Context, tx *sql.Tx, version int) (bool, error) {
	if version != 1 {
		return false, nil
	}
	legacyToken, err := tableHasColumn(ctx, tx, "leases", "token")
	if err != nil {
		return false, err
	}
	legacyResult, err := tableHasColumn(ctx, tx, "operations", "result_token")
	return legacyToken || legacyResult, err
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func tableHasColumn(ctx context.Context, query queryer, table, column string) (bool, error) {
	rows, err := query.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("inspect table %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("inspect column in %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

type legacyOperation struct {
	operationID, operationKind, idempotencyKey, requestDigest, status string
	resultRunID, resultLeaseID, resultToken, resultDeadline           sql.NullString
	resultGeneration                                                  sql.NullInt64
	resultErrorKind, resultErrorPayload                               sql.NullString
	createdAt                                                         string
}

type legacyLease struct {
	leaseID, runID, workID, workRevisionID, token, status, heartbeatAt, deadline string
	generation                                                                   int64
}

func upgradeLegacyV1(ctx context.Context, tx *sql.Tx, tokenKey []byte) error {
	operations, err := readLegacyOperations(ctx, tx)
	if err != nil {
		return err
	}
	leases, err := readLegacyLeases(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS work_revision_scope ON work_revisions(work_revision_id, project_id, mission_id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, legacyReplacementSchema); err != nil {
		return err
	}
	for _, operation := range operations {
		var envelope []byte
		if operation.resultToken.Valid && operation.resultToken.String != "" {
			envelope, err = sealToken(tokenKey, operation.operationID, operation.resultToken.String)
			if err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO operations_v2(operation_id, operation_kind, idempotency_key, correlation_id, request_digest, status, result_run_id, result_lease_id, result_generation, result_deadline, result_error_kind, result_error_payload, created_at, result_token_envelope) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			operation.operationID, operation.operationKind, operation.idempotencyKey, operation.operationID, operation.requestDigest, operation.status,
			operation.resultRunID, operation.resultLeaseID, operation.resultGeneration, operation.resultDeadline, operation.resultErrorKind, operation.resultErrorPayload, operation.createdAt, envelope)
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runs_v2 SELECT * FROM runs`); err != nil {
		return err
	}
	for _, lease := range leases {
		if _, err := tx.ExecContext(ctx, `INSERT INTO leases_v2(lease_id, run_id, work_id, work_revision_id, generation, token_digest, status, heartbeat_at, deadline) VALUES(?,?,?,?,?,?,?,?,?)`,
			lease.leaseID, lease.runID, lease.workID, lease.workRevisionID, lease.generation, tokenDigest(lease.token), lease.status, lease.heartbeatAt, lease.deadline); err != nil {
			return err
		}
	}
	if err := redactLegacyEvents(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE leases; DROP TABLE runs; DROP TABLE operations; ALTER TABLE operations_v2 RENAME TO operations; ALTER TABLE runs_v2 RENAME TO runs; ALTER TABLE leases_v2 RENAME TO leases;`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX one_active_implementation_lease ON leases(work_id, work_revision_id) WHERE status='active'`); err != nil {
		return err
	}
	return nil
}

func readLegacyOperations(ctx context.Context, tx *sql.Tx) ([]legacyOperation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT operation_id, operation_kind, idempotency_key, request_digest, status, result_run_id, result_lease_id, result_generation, result_token, result_deadline, result_error_kind, result_error_payload, created_at FROM operations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []legacyOperation
	for rows.Next() {
		var operation legacyOperation
		if err := rows.Scan(&operation.operationID, &operation.operationKind, &operation.idempotencyKey, &operation.requestDigest, &operation.status, &operation.resultRunID, &operation.resultLeaseID, &operation.resultGeneration, &operation.resultToken, &operation.resultDeadline, &operation.resultErrorKind, &operation.resultErrorPayload, &operation.createdAt); err != nil {
			return nil, err
		}
		result = append(result, operation)
	}
	return result, rows.Err()
}

func readLegacyLeases(ctx context.Context, tx *sql.Tx) ([]legacyLease, error) {
	rows, err := tx.QueryContext(ctx, `SELECT lease_id, run_id, work_id, work_revision_id, generation, token, status, heartbeat_at, deadline FROM leases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []legacyLease
	for rows.Next() {
		var lease legacyLease
		if err := rows.Scan(&lease.leaseID, &lease.runID, &lease.workID, &lease.workRevisionID, &lease.generation, &lease.token, &lease.status, &lease.heartbeatAt, &lease.deadline); err != nil {
			return nil, err
		}
		result = append(result, lease)
	}
	return result, rows.Err()
}

func redactLegacyEvents(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT sequence, payload FROM events WHERE kind='lease_granted'`)
	if err != nil {
		return err
	}
	type eventUpdate struct {
		sequence int64
		payload  string
	}
	var updates []eventUpdate
	for rows.Next() {
		var update eventUpdate
		if err := rows.Scan(&update.sequence, &update.payload); err != nil {
			_ = rows.Close()
			return err
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(update.payload), &payload); err != nil {
			_ = rows.Close()
			return fmt.Errorf("decode legacy lease event %d: %w", update.sequence, err)
		}
		delete(payload, "token")
		operationID, _ := payload["operation_id"].(string)
		aggregates := map[string]any{"operation_id": operationID}
		for _, field := range []string{"run_id", "lease_id", "generation"} {
			if value, ok := payload[field]; ok {
				aggregates[field] = value
			}
		}
		payload["schema_version"] = 1
		payload["kind"] = "lease_granted"
		payload["correlation_id"] = operationID
		payload["aggregate_ids"] = aggregates
		payload["observed_state"] = map[string]string{"lease": "active"}
		payload["expected_state"] = map[string]string{"work_state": "ready"}
		payload["retry_safety"] = "never"
		payload["recovery_actions"] = []string{"heartbeat before the lease deadline"}
		encoded, err := json.Marshal(payload)
		if err != nil {
			_ = rows.Close()
			return err
		}
		update.payload = string(encoded)
		updates = append(updates, update)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, update := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE events SET payload=? WHERE sequence=?`, update.payload, update.sequence); err != nil {
			return err
		}
	}
	return nil
}

func sealToken(key []byte, operationID, token string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create token cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create token envelope: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate token envelope nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, []byte(token), []byte(operationID)), nil
}

func openToken(key []byte, operationID string, envelope []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(envelope) < aead.NonceSize() {
		return "", fmt.Errorf("legacy token envelope is truncated")
	}
	nonce := envelope[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, envelope[aead.NonceSize():], []byte(operationID))
	if err != nil {
		return "", fmt.Errorf("open legacy token envelope: %w", err)
	}
	return string(plaintext), nil
}

const legacyReplacementSchema = `
CREATE TABLE operations_v2 (
  operation_id TEXT PRIMARY KEY, operation_kind TEXT NOT NULL, idempotency_key TEXT NOT NULL,
  correlation_id TEXT NOT NULL, request_digest TEXT NOT NULL, status TEXT NOT NULL,
  result_run_id TEXT, result_lease_id TEXT, result_generation INTEGER, result_deadline TEXT,
  result_error_kind TEXT, result_error_payload TEXT, created_at TEXT NOT NULL,
  result_token_envelope BLOB, UNIQUE(operation_kind, idempotency_key)
);
CREATE TABLE runs_v2 (
  run_id TEXT PRIMARY KEY, operation_id TEXT NOT NULL UNIQUE REFERENCES operations_v2(operation_id),
  project_id TEXT NOT NULL REFERENCES projects(project_id), mission_id TEXT NOT NULL,
  work_id TEXT NOT NULL, work_revision_id TEXT NOT NULL,
  actor_id TEXT NOT NULL REFERENCES actors(actor_id), process_id TEXT NOT NULL REFERENCES processes(process_id),
  generation INTEGER NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL,
  FOREIGN KEY(work_id, work_revision_id) REFERENCES work_items(work_id, work_revision_id),
  FOREIGN KEY(work_revision_id, project_id, mission_id) REFERENCES work_revisions(work_revision_id, project_id, mission_id),
  FOREIGN KEY(process_id, actor_id) REFERENCES processes(process_id, actor_id)
);
CREATE TABLE leases_v2 (
  lease_id TEXT PRIMARY KEY, run_id TEXT NOT NULL UNIQUE REFERENCES runs_v2(run_id),
  work_id TEXT NOT NULL, work_revision_id TEXT NOT NULL, generation INTEGER NOT NULL,
  token_digest TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active', 'expired', 'surrendered')),
  heartbeat_at TEXT NOT NULL, deadline TEXT NOT NULL,
  FOREIGN KEY(work_id, work_revision_id) REFERENCES work_items(work_id, work_revision_id),
  UNIQUE(work_id, work_revision_id, generation)
);`
