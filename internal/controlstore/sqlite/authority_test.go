package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestOpenVerifiesRequiredPragmas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	settings, err := store.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if settings.JournalMode != "wal" || !settings.ForeignKeys || settings.BusyTimeout != 5000 {
		t.Fatalf("unexpected settings: %+v", settings)
	}
}

func TestClaimIsIdempotentAndFenced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	seed := Seed{
		ProjectID: ids.ProjectID("project-1"), MissionID: ids.MissionID("mission-1"),
		WorkRevisionID: ids.WorkRevisionID("revision-1"), WorkID: ids.WorkID("work-1"),
	}
	seedCanonicalAuthority(t, ctx, store, seed)

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	req := execution.StartRequest{
		OperationID: ids.OperationID("operation-1"), ProjectID: seed.ProjectID,
		MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID,
		ActorID: ids.ActorID("actor-1"), ProcessID: ids.ProcessID("process-1"),
		Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-1", now),
	}
	first, err := store.Claim(ctx, req, ids.RunID("run-1"), ids.LeaseID("lease-1"))
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	replay, err := store.Claim(ctx, req, ids.RunID("run-unused"), ids.LeaseID("lease-unused"))
	if err != nil {
		t.Fatalf("replay Claim: %v", err)
	}
	if !replay.Replayed || replay.RunID != first.RunID || replay.LeaseID != first.LeaseID || replay.Token != first.Token {
		t.Fatalf("replay = %+v, first = %+v", replay, first)
	}

	req.OperationID = ids.OperationID("operation-2")
	req.ActorID = ids.ActorID("actor-2")
	req.ProcessID = ids.ProcessID("process-2")
	if _, err := store.Claim(ctx, req, ids.RunID("run-2"), ids.LeaseID("lease-2")); !execution.IsKind(err, execution.KindAuthorityConflict) {
		t.Fatalf("second authority error = %v", err)
	}
	_, err = store.Claim(ctx, req, ids.RunID("run-unused"), ids.LeaseID("lease-unused"))
	var replayedRefusal *execution.DiagnosticError
	if !errors.As(err, &replayedRefusal) || replayedRefusal.Kind != execution.KindAuthorityConflict || len(replayedRefusal.RecoveryActions) == 0 || replayedRefusal.AggregateIDs["work_id"] != "work-1" {
		t.Fatalf("replayed refusal = %#v", err)
	}
}

func TestConnectionPolicyAppliesAcrossAcquiredConnections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	for index := 0; index < 2; index++ {
		connection, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn %d: %v", index, err)
		}
		var foreignKeys, busyTimeout int
		if err := connection.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("foreign_keys %d: %v", index, err)
		}
		if err := connection.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("busy_timeout %d: %v", index, err)
		}
		_ = connection.Close()
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Fatalf("connection %d policy: foreign_keys=%d busy_timeout=%d", index, foreignKeys, busyTimeout)
		}
	}
}

func TestWriterContentionReturnsTypedBusyAfterBoundedWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "control.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	defer second.Close()
	seed := Seed{ProjectID: "project-busy", MissionID: "mission-busy", WorkRevisionID: "revision-busy", WorkID: "work-busy"}
	seedCanonicalAuthority(t, ctx, first, seed)

	writer, err := first.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(ctx, `INSERT INTO projects(project_id, created_at) VALUES('writer-lock', 'now')`); err != nil {
		t.Fatalf("acquire writer: %v", err)
	}

	started := time.Now()
	_, err = second.Claim(ctx, execution.StartRequest{
		OperationID: "operation-busy", ProjectID: seed.ProjectID, MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID,
		ActorID: "actor-busy", ProcessID: "process-busy", Now: time.Now().UTC(), LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-busy", time.Now().UTC()),
	}, "run-busy", "lease-busy")
	elapsed := time.Since(started)
	var diagnostic *execution.DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != execution.KindControlStoreBusy {
		t.Fatalf("busy diagnostic = %#v", err)
	}
	if diagnostic.RetrySafety != execution.RetrySafeSameOperation || len(diagnostic.RecoveryActions) == 0 || diagnostic.ObservedState["control_store"] != "busy" || diagnostic.ExpectedState["control_store"] != "writable" {
		t.Fatalf("busy retry diagnostic = %+v", diagnostic)
	}
	if elapsed > 7*time.Second {
		t.Fatalf("busy wait = %s, want no more than 7s", elapsed)
	}
}

func TestHeartbeatAndExpiryRequireCurrentGenerationAndToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	seed := Seed{ProjectID: ids.ProjectID("project-fence"), MissionID: ids.MissionID("mission-fence"), WorkRevisionID: ids.WorkRevisionID("revision-fence"), WorkID: ids.WorkID("work-fence")}
	seedCanonicalAuthority(t, ctx, store, seed)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	req := execution.StartRequest{OperationID: ids.OperationID("operation-fence"), ProjectID: seed.ProjectID, MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, ActorID: ids.ActorID("actor-fence"), ProcessID: ids.ProcessID("process-fence"), Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-fence", now)}
	capsule, err := store.Claim(ctx, req, ids.RunID("run-fence"), ids.LeaseID("lease-fence"))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	heartbeat := execution.HeartbeatRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-fence", OperationID: req.OperationID, ActorID: req.ActorID, ProcessID: req.ProcessID, LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: "token-stale", Now: now.Add(time.Second), LeaseTTL: time.Minute}
	if _, err := store.Heartbeat(ctx, heartbeat); !execution.IsKind(err, execution.KindStaleAuthority) {
		t.Fatalf("stale heartbeat error = %v", err)
	} else {
		assertLeaseDiagnostic(t, err, heartbeat.CorrelationID, heartbeat.OperationID, heartbeat.ActorID, heartbeat.ProcessID)
	}
	heartbeat.Token = capsule.Token
	heartbeat.LeaseTTL = 2 * time.Minute
	deadline, err := store.Heartbeat(ctx, heartbeat)
	if err != nil || !deadline.Equal(now.Add(time.Second+2*time.Minute)) {
		t.Fatalf("heartbeat deadline=%s err=%v", deadline, err)
	}
	expire := execution.ExpireRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-expire", OperationID: req.OperationID, ActorID: req.ActorID, ProcessID: req.ProcessID, LeaseID: capsule.LeaseID, Generation: capsule.Generation + 1, Token: capsule.Token, Now: now.Add(2 * time.Second)}
	if err := store.Expire(ctx, expire); !execution.IsKind(err, execution.KindStaleAuthority) {
		t.Fatalf("stale expiry error = %v", err)
	}
	expire.Generation = capsule.Generation
	if err := store.Expire(ctx, expire); !execution.IsKind(err, execution.KindAuthorityConflict) {
		t.Fatalf("early expiry error: %v", err)
	} else {
		assertLeaseDiagnostic(t, err, expire.CorrelationID, expire.OperationID, expire.ActorID, expire.ProcessID)
	}
	expire.Now = deadline
	if err := store.Expire(ctx, expire); err != nil {
		t.Fatalf("due expiry: %v", err)
	}
	heartbeat.Now = now.Add(3 * time.Second)
	heartbeat.LeaseTTL = time.Minute
	if _, err := store.Heartbeat(ctx, heartbeat); !execution.IsKind(err, execution.KindStaleAuthority) {
		t.Fatalf("post-expiry heartbeat error = %v", err)
	}
}

func assertLeaseDiagnostic(t *testing.T, err error, correlationID string, operationID ids.OperationID, actorID ids.ActorID, processID ids.ProcessID) {
	t.Helper()
	var diagnostic *execution.DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.SchemaVersion != execution.ResultSchemaVersion || diagnostic.CorrelationID != correlationID || diagnostic.OperationID != operationID || diagnostic.AggregateIDs["actor_id"] != actorID.String() || diagnostic.AggregateIDs["process_id"] != processID.String() || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || diagnostic.RetrySafety == "" || len(diagnostic.RecoveryActions) == 0 {
		t.Fatalf("incomplete lease diagnostic: %#v", err)
	}
}

func TestProcessIdentityCannotMoveBetweenActors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	firstSeed := Seed{ProjectID: ids.ProjectID("project-process"), MissionID: ids.MissionID("mission-process"), WorkRevisionID: ids.WorkRevisionID("revision-process"), WorkID: ids.WorkID("work-process-1")}
	secondSeed := firstSeed
	secondSeed.WorkID = ids.WorkID("work-process-2")
	seedCanonicalAuthority(t, ctx, store, firstSeed, secondSeed)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	first := execution.StartRequest{OperationID: ids.OperationID("operation-process-1"), ProjectID: firstSeed.ProjectID, MissionID: firstSeed.MissionID, WorkRevisionID: firstSeed.WorkRevisionID, WorkID: firstSeed.WorkID, ActorID: ids.ActorID("actor-process-1"), ProcessID: ids.ProcessID("process-stable"), Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-process", now)}
	if _, err := store.Claim(ctx, first, ids.RunID("run-process-1"), ids.LeaseID("lease-process-1")); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	second := first
	second.OperationID = ids.OperationID("operation-process-2")
	second.WorkID = secondSeed.WorkID
	second.ActorID = ids.ActorID("actor-process-2")
	if _, err := store.Claim(ctx, second, ids.RunID("run-process-2"), ids.LeaseID("lease-process-2")); !execution.IsKind(err, execution.KindInvalidCommand) {
		t.Fatalf("moved process identity error = %v", err)
	}
}

func TestClaimRejectsWorkRevisionFromAnotherScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seed := Seed{ProjectID: ids.ProjectID("project-owner"), MissionID: ids.MissionID("mission-owner"), WorkRevisionID: ids.WorkRevisionID("revision-owner"), WorkID: ids.WorkID("work-owner")}
	seedCanonicalAuthority(t, ctx, store, seed)
	now := time.Now().UTC()
	request := execution.StartRequest{OperationID: ids.OperationID("operation-scope"), ProjectID: ids.ProjectID("project-other"), MissionID: ids.MissionID("mission-other"), WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, ActorID: ids.ActorID("actor-scope"), ProcessID: ids.ProcessID("process-scope"), Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-scope", now)}
	_, err = store.Claim(ctx, request, ids.RunID("run-scope"), ids.LeaseID("lease-scope"))
	var diagnostic *execution.DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != execution.KindInvalidCommand || diagnostic.ObservedState["project_id"] != seed.ProjectID.String() || diagnostic.ExpectedState["project_id"] != request.ProjectID.String() {
		t.Fatalf("scope error = %#v", err)
	}
}

func TestSeedAuthorityRejectsConflictingImmutableIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := Seed{ProjectID: ids.ProjectID("project-seed-a"), MissionID: ids.MissionID("mission-seed-a"), WorkRevisionID: ids.WorkRevisionID("revision-seed"), WorkID: ids.WorkID("work-seed")}
	seedCanonicalAuthority(t, ctx, store, first)
	conflict := first
	conflict.ProjectID = ids.ProjectID("project-seed-b")
	conflict.MissionID = ids.MissionID("mission-seed-b")
	err = store.SeedAuthority(ctx, conflict)
	var diagnostic *execution.DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != execution.KindIdempotencyConflict || diagnostic.ObservedState["project_id"] != first.ProjectID.String() || diagnostic.ExpectedState["project_id"] != conflict.ProjectID.String() {
		t.Fatalf("seed conflict = %#v", err)
	}
}

func TestBearerTokenIsNeverPersisted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	seed := Seed{ProjectID: ids.ProjectID("project-token"), MissionID: ids.MissionID("mission-token"), WorkRevisionID: ids.WorkRevisionID("revision-token"), WorkID: ids.WorkID("work-token")}
	seedCanonicalAuthority(t, ctx, store, seed)
	now := time.Now().UTC()
	request := execution.StartRequest{OperationID: ids.OperationID("operation-token"), ProjectID: seed.ProjectID, MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, ActorID: ids.ActorID("actor-token"), ProcessID: ids.ProcessID("process-token"), Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-token", now)}
	capsule, err := store.Claim(ctx, request, ids.RunID("run-token"), ids.LeaseID("lease-token"))
	if err != nil {
		t.Fatal(err)
	}
	var persistedDigest, payload string
	if err := store.db.QueryRowContext(ctx, `SELECT token_digest FROM leases WHERE lease_id=?`, capsule.LeaseID).Scan(&persistedDigest); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT payload FROM events WHERE operation_id=?`, request.OperationID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if persistedDigest == capsule.Token || strings.Contains(payload, capsule.Token) || persistedDigest != tokenDigest(capsule.Token) {
		t.Fatalf("token persistence digest=%q payload=%q token=%q", persistedDigest, payload, capsule.Token)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(path + suffix)
		if err == nil && strings.Contains(string(data), capsule.Token) {
			t.Fatalf("raw bearer token persisted in %s", path+suffix)
		}
	}
}

func TestMigrationUpgradesExactPriorV1EmptyAndUsedDatabases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, used := range []bool{false, true} {
		used := used
		t.Run(map[bool]string{false: "empty", true: "used"}[used], func(t *testing.T) {
			path, request, legacyToken := createExactLegacyV1(t, used)
			store, err := Open(ctx, path)
			if err != nil {
				t.Fatalf("upgrade: %v", err)
			}
			var version int
			if err := store.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != currentSchemaVersion {
				t.Fatalf("version=%d err=%v", version, err)
			}
			if used {
				replay, err := store.Claim(ctx, request, ids.RunID("run-unused"), ids.LeaseID("lease-unused"))
				if err != nil {
					t.Fatalf("replay migrated operation: %v", err)
				}
				if !replay.Replayed || replay.Token != legacyToken || replay.RunID != ids.RunID("run-legacy") || replay.LeaseID != ids.LeaseID("lease-legacy") {
					t.Fatalf("migrated replay = %+v", replay)
				}
				var digest, payload string
				if err := store.db.QueryRowContext(ctx, `SELECT token_digest FROM leases WHERE lease_id='lease-legacy'`).Scan(&digest); err != nil {
					t.Fatal(err)
				}
				if err := store.db.QueryRowContext(ctx, `SELECT payload FROM events WHERE operation_id='operation-legacy'`).Scan(&payload); err != nil {
					t.Fatal(err)
				}
				if digest != tokenDigest(legacyToken) || strings.Contains(payload, legacyToken) {
					t.Fatalf("legacy secret not sanitized: digest=%q payload=%q", digest, payload)
				}
			} else {
				seed := Seed{ProjectID: ids.ProjectID("project-new"), MissionID: ids.MissionID("mission-new"), WorkRevisionID: ids.WorkRevisionID("revision-new"), WorkID: ids.WorkID("work-new")}
				seedCanonicalAuthority(t, ctx, store, seed)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if used {
				for _, suffix := range []string{"", "-wal"} {
					data, err := os.ReadFile(path + suffix)
					if err == nil && strings.Contains(string(data), legacyToken) {
						t.Fatalf("legacy raw token remains in %s", path+suffix)
					}
				}
			}
		})
	}
}

func seedCanonicalAuthority(t *testing.T, ctx context.Context, store *Store, seeds ...Seed) {
	t.Helper()
	if len(seeds) == 0 {
		t.Fatal("canonical authority seed requires at least one WorkID")
	}
	first := seeds[0]
	draft := workgraph.NewDraft(first.ProjectID, first.MissionID)
	if err := draft.AddLane(workgraph.Lane{ID: "lane-fixture"}); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		if seed.ProjectID != first.ProjectID || seed.MissionID != first.MissionID || seed.WorkRevisionID != first.WorkRevisionID {
			t.Fatalf("canonical authority fixture crosses revision scope: %+v", seeds)
		}
		if err := draft.AddWork(workgraph.WorkItem{ID: seed.WorkID, LaneID: "lane-fixture", Criteria: []workgraph.Criterion{{ID: "criterion-" + seed.WorkID.String(), Text: "fixture ready"}}}); err != nil {
			t.Fatal(err)
		}
	}
	finalizedAt := time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
	revision, err := draft.Finalize(workgraph.FinalizeMetadata{ID: first.WorkRevisionID, Number: 1, ActorID: "actor-fixture", FinalizedAt: finalizedAt})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.finalizeWorkRevisionForTest(ctx, revision, 0); err != nil {
		t.Fatalf("save canonical authority fixture: %v", err)
	}
	for _, seed := range seeds {
		if err := store.SeedAuthority(ctx, seed); err != nil {
			t.Fatalf("verify canonical authority fixture: %v", err)
		}
	}
}

func TestMigrationRefusesFutureVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	futurePath := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", futurePath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.ExecContext(ctx, `PRAGMA user_version=99`)
	_ = db.Close()
	if _, err := Open(ctx, futurePath); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("future schema error = %v", err)
	}
}

func createExactLegacyV1(t *testing.T, used bool) (string, execution.StartRequest, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, exactLegacyV1Schema); err != nil {
		t.Fatal(err)
	}
	request := execution.StartRequest{
		OperationID: ids.OperationID("operation-legacy"), ProjectID: ids.ProjectID("project-legacy"), MissionID: ids.MissionID("mission-legacy"),
		WorkRevisionID: ids.WorkRevisionID("revision-legacy"), WorkID: ids.WorkID("work-legacy"), ActorID: ids.ActorID("actor-legacy"), ProcessID: ids.ProcessID("process-legacy"),
		Now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC), LeaseTTL: time.Minute,
	}
	legacyToken := "legacy-raw-bearer-token"
	if used {
		now := request.Now.Format(time.RFC3339Nano)
		deadline := request.Now.Add(request.LeaseTTL).Format(time.RFC3339Nano)
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO projects VALUES(?,?)`, []any{request.ProjectID, now}},
			{`INSERT INTO work_revisions VALUES(?,?,?,1,?)`, []any{request.WorkRevisionID, request.ProjectID, request.MissionID, now}},
			{`INSERT INTO work_items VALUES(?,?,'ready')`, []any{request.WorkID, request.WorkRevisionID}},
			{`INSERT INTO actors VALUES(?,?)`, []any{request.ActorID, now}},
			{`INSERT INTO processes VALUES(?,?,?)`, []any{request.ProcessID, request.ActorID, now}},
			{`INSERT INTO operations(operation_id,operation_kind,idempotency_key,request_digest,status,result_run_id,result_lease_id,result_generation,result_token,result_deadline,created_at) VALUES(?,'start_run',?,?, 'succeeded','run-legacy','lease-legacy',1,?,?,?)`, []any{request.OperationID, request.OperationID, requestDigest(request), legacyToken, deadline, now}},
			{`INSERT INTO runs VALUES('run-legacy',?,?,?,?,?,?,?,?, 'active',?)`, []any{request.OperationID, request.ProjectID, request.MissionID, request.WorkID, request.WorkRevisionID, request.ActorID, request.ProcessID, 1, now}},
			{`INSERT INTO leases VALUES('lease-legacy','run-legacy',?,?,1,?,'active',?,?)`, []any{request.WorkID, request.WorkRevisionID, legacyToken, now, deadline}},
			{`INSERT INTO events(operation_id,kind,payload,occurred_at) VALUES(?,'lease_granted',?,?)`, []any{request.OperationID, "{\"operation_id\":\"operation-legacy\",\"run_id\":\"run-legacy\",\"lease_id\":\"lease-legacy\",\"generation\":1,\"token\":\"" + legacyToken + "\",\"deadline\":\"" + deadline + "\"}", now}},
		}
		for _, statement := range statements {
			if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
				_ = db.Close()
				t.Fatalf("create used legacy fixture: %v", err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path, request, legacyToken
}

const exactLegacyV1Schema = `
CREATE TABLE projects (project_id TEXT PRIMARY KEY, created_at TEXT NOT NULL);
CREATE TABLE work_revisions (work_revision_id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(project_id), mission_id TEXT NOT NULL, revision_number INTEGER NOT NULL, finalized_at TEXT NOT NULL, UNIQUE(project_id, mission_id, revision_number));
CREATE TABLE work_items (work_id TEXT NOT NULL, work_revision_id TEXT NOT NULL REFERENCES work_revisions(work_revision_id), state TEXT NOT NULL CHECK(state IN ('ready', 'blocked', 'done')), PRIMARY KEY(work_id, work_revision_id));
CREATE TABLE actors (actor_id TEXT PRIMARY KEY, created_at TEXT NOT NULL);
CREATE TABLE processes (process_id TEXT PRIMARY KEY, actor_id TEXT NOT NULL REFERENCES actors(actor_id), observed_at TEXT NOT NULL, UNIQUE(process_id, actor_id));
CREATE TABLE operations (operation_id TEXT PRIMARY KEY, operation_kind TEXT NOT NULL, idempotency_key TEXT NOT NULL, request_digest TEXT NOT NULL, status TEXT NOT NULL, result_run_id TEXT, result_lease_id TEXT, result_generation INTEGER, result_token TEXT, result_deadline TEXT, result_error_kind TEXT, result_error_payload TEXT, created_at TEXT NOT NULL, UNIQUE(operation_kind, idempotency_key));
CREATE TABLE runs (run_id TEXT PRIMARY KEY, operation_id TEXT NOT NULL UNIQUE REFERENCES operations(operation_id), project_id TEXT NOT NULL REFERENCES projects(project_id), mission_id TEXT NOT NULL, work_id TEXT NOT NULL, work_revision_id TEXT NOT NULL, actor_id TEXT NOT NULL REFERENCES actors(actor_id), process_id TEXT NOT NULL REFERENCES processes(process_id), generation INTEGER NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, FOREIGN KEY(work_id, work_revision_id) REFERENCES work_items(work_id, work_revision_id), FOREIGN KEY(process_id, actor_id) REFERENCES processes(process_id, actor_id));
CREATE TABLE leases (lease_id TEXT PRIMARY KEY, run_id TEXT NOT NULL UNIQUE REFERENCES runs(run_id), work_id TEXT NOT NULL, work_revision_id TEXT NOT NULL, generation INTEGER NOT NULL, token TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active', 'expired', 'surrendered')), heartbeat_at TEXT NOT NULL, deadline TEXT NOT NULL, FOREIGN KEY(work_id, work_revision_id) REFERENCES work_items(work_id, work_revision_id), UNIQUE(work_id, work_revision_id, generation));
CREATE UNIQUE INDEX one_active_implementation_lease ON leases(work_id, work_revision_id) WHERE status='active';
CREATE TABLE events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, operation_id TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL, occurred_at TEXT NOT NULL);
PRAGMA user_version=1;`
