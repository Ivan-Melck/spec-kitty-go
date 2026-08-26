package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type GovernanceProvenance string

const (
	GovernanceVerified             GovernanceProvenance = "verified"
	GovernanceUnknownPreProvenance GovernanceProvenance = "unknown_pre_provenance"
)

// RunGovernance is the adapter's inspection projection. Historical runs have
// UnknownPreProvenance and a zero Context; current runs always have a verified
// Context. Lease inspection resolves this record through the immutable run_id.
type RunGovernance struct {
	RunID      ids.RunID
	LeaseID    ids.LeaseID
	Provenance GovernanceProvenance
	Context    governancecontext.Context
}

func (s *Store) GovernanceForRun(ctx context.Context, runID ids.RunID) (RunGovernance, error) {
	return loadRunGovernance(ctx, s.db, runID, "")
}

func (s *Store) GovernanceForLease(ctx context.Context, leaseID ids.LeaseID) (RunGovernance, error) {
	var runID ids.RunID
	if err := s.db.QueryRowContext(ctx, `SELECT run_id FROM leases WHERE lease_id=?`, leaseID).Scan(&runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RunGovernance{}, governanceDiagnostic("lease governance was not found", map[string]string{"lease_id": leaseID.String()}, nil)
		}
		return RunGovernance{}, governanceDiagnostic("read lease governance binding", map[string]string{"lease_id": leaseID.String()}, err)
	}
	result, err := s.GovernanceForRun(ctx, runID)
	if err != nil {
		return RunGovernance{}, err
	}
	result.LeaseID = leaseID
	return result, nil
}

func saveGovernanceContextTx(ctx context.Context, tx *sql.Tx, value governancecontext.Context, operationID ids.OperationID, now time.Time) error {
	if err := value.Validate(); err != nil {
		diagnostic := execution.NewDiagnostic(execution.KindInvalidCommand, "governance_context is invalid", execution.RetryNever,
			operationID.String(), operationID, map[string]string{"governance_context_id": value.ID().String()},
			map[string]string{"governance_context": "invalid"}, map[string]string{"governance_context": "validated immutable context"}, now)
		diagnostic.Cause = err
		return diagnostic
	}

	existing, found, err := loadGovernanceContext(ctx, tx, value.ID())
	if err != nil {
		return governanceDiagnostic("inspect existing governance context", map[string]string{"governance_context_id": value.ID().String()}, err)
	}
	if found {
		if sameGovernanceSemantics(existing, value) {
			return nil
		}
		return execution.NewDiagnostic(execution.KindIdempotencyConflict, "governance context identity is already bound to different content", execution.RetryNever,
			operationID.String(), operationID, map[string]string{"governance_context_id": value.ID().String()},
			map[string]string{"aggregate": value.Aggregate().Algorithm() + ":" + value.Aggregate().Digest()},
			map[string]string{"aggregate": existing.Aggregate().Algorithm() + ":" + existing.Aggregate().Digest()}, now)
	}

	for position, entry := range value.Entries() {
		commitment := entry.Commitment()
		if _, err := tx.ExecContext(ctx, `INSERT INTO governance_context_entries(context_id,position,kind,entry_ref,revision,commitment_algorithm,commitment_digest) VALUES(?,?,?,?,?,?,?)`,
			value.ID(), position, entry.Kind(), entry.Ref(), entry.Revision(), commitment.Algorithm(), commitment.Digest()); err != nil {
			return governanceDiagnostic("persist governance context entry", map[string]string{"governance_context_id": value.ID().String(), "position": fmt.Sprint(position)}, err)
		}
	}
	aggregate := value.Aggregate()
	if _, err := tx.ExecContext(ctx, `INSERT INTO governance_contexts(context_id,schema_version,aggregate_algorithm,aggregate_digest,created_at) VALUES(?,?,?,?,?)`,
		value.ID(), value.SchemaVersion(), aggregate.Algorithm(), aggregate.Digest(), formatTime(value.CreatedAt())); err != nil {
		return governanceDiagnostic("persist governance context", map[string]string{"governance_context_id": value.ID().String()}, err)
	}
	return nil
}

type governanceQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadGovernanceContext(ctx context.Context, query governanceQuery, contextID ids.GovernanceContextID) (governancecontext.Context, bool, error) {
	var schema uint16
	var algorithm, digest, createdText string
	err := query.QueryRowContext(ctx, `SELECT schema_version,aggregate_algorithm,aggregate_digest,created_at FROM governance_contexts WHERE context_id=?`, contextID).
		Scan(&schema, &algorithm, &digest, &createdText)
	if errors.Is(err, sql.ErrNoRows) {
		return governancecontext.Context{}, false, nil
	}
	if err != nil {
		return governancecontext.Context{}, false, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdText)
	if err != nil {
		return governancecontext.Context{}, false, fmt.Errorf("parse created_at: %w", err)
	}
	rows, err := query.QueryContext(ctx, `SELECT position,kind,entry_ref,revision,commitment_algorithm,commitment_digest FROM governance_context_entries WHERE context_id=? ORDER BY position`, contextID)
	if err != nil {
		return governancecontext.Context{}, false, err
	}
	defer rows.Close()
	entries := make([]governancecontext.Entry, 0)
	wantPosition := 0
	for rows.Next() {
		var position int
		var kind governancecontext.EntryKind
		var ref, revision, entryAlgorithm, entryDigest string
		if err := rows.Scan(&position, &kind, &ref, &revision, &entryAlgorithm, &entryDigest); err != nil {
			return governancecontext.Context{}, false, err
		}
		if position != wantPosition {
			return governancecontext.Context{}, false, fmt.Errorf("entry positions are not contiguous: got %d want %d", position, wantPosition)
		}
		commitment, err := governancecontext.NewCommitment(entryAlgorithm, entryDigest)
		if err != nil {
			return governancecontext.Context{}, false, err
		}
		entry, err := governancecontext.NewEntry(kind, ref, revision, commitment)
		if err != nil {
			return governancecontext.Context{}, false, err
		}
		entries = append(entries, entry)
		wantPosition++
	}
	if err := rows.Err(); err != nil {
		return governancecontext.Context{}, false, err
	}
	aggregate, err := governancecontext.NewCommitment(algorithm, digest)
	if err != nil {
		return governancecontext.Context{}, false, err
	}
	value, err := governancecontext.RestoreContext(contextID, schema, entries, aggregate, createdAt)
	if err != nil {
		return governancecontext.Context{}, false, err
	}
	return value, true, nil
}

func loadRunGovernance(ctx context.Context, query governanceQuery, runID ids.RunID, leaseID ids.LeaseID) (RunGovernance, error) {
	var contextID sql.NullString
	if err := query.QueryRowContext(ctx, `SELECT governance_context_id FROM runs WHERE run_id=?`, runID).Scan(&contextID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RunGovernance{}, governanceDiagnostic("run governance was not found", map[string]string{"run_id": runID.String()}, nil)
		}
		return RunGovernance{}, governanceDiagnostic("read run governance binding", map[string]string{"run_id": runID.String()}, err)
	}
	result := RunGovernance{RunID: runID, LeaseID: leaseID}
	if !contextID.Valid || contextID.String == "" {
		result.Provenance = GovernanceUnknownPreProvenance
		return result, nil
	}
	parsedID, err := ids.ParseGovernanceContextID(contextID.String)
	if err != nil {
		return RunGovernance{}, governanceDiagnostic("stored run governance identity is corrupt", map[string]string{"run_id": runID.String(), "governance_context_id": contextID.String}, err)
	}
	value, found, err := loadGovernanceContext(ctx, query, parsedID)
	if err != nil || !found {
		if err == nil {
			err = errors.New("referenced context is missing")
		}
		return RunGovernance{}, governanceDiagnostic("stored run governance context is corrupt", map[string]string{"run_id": runID.String(), "governance_context_id": contextID.String}, err)
	}
	result.Provenance = GovernanceVerified
	result.Context = value
	return result, nil
}

func sameGovernanceSemantics(left, right governancecontext.Context) bool {
	if left.ID() != right.ID() || left.SchemaVersion() != right.SchemaVersion() || left.Aggregate() != right.Aggregate() {
		return false
	}
	leftEntries, rightEntries := left.Entries(), right.Entries()
	if len(leftEntries) != len(rightEntries) {
		return false
	}
	for index := range leftEntries {
		if leftEntries[index] != rightEntries[index] {
			return false
		}
	}
	return true
}

func governanceDiagnostic(message string, aggregates map[string]string, cause error) error {
	diagnostic := execution.NewDiagnostic(execution.KindAuthorityConflict, message, execution.RetryDecisionRequired, "", "", aggregates,
		map[string]string{"governance_context": "corrupt_or_missing"}, map[string]string{"governance_context": "validated immutable context"}, time.Now().UTC())
	diagnostic.Cause = cause
	return diagnostic
}
