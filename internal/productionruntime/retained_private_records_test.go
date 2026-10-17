package productionruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

// These are source-valid endpoint fixtures, NOT fabricated private admission
// ACKs. Only recovery.go's bytes/schema are reconstructed; no writer/CAS claim.
type retainedPrivateFixture struct {
	binding  AuthorityBindingV2
	captured journalv2.StoreRevisionV2
	current  []runtimeCurrentV2 // Oldest first.
	reader   *retainedPrivateFakeReader
}

type retainedPrivateFakeReader struct {
	rows  map[string]storegapdb.Record
	trace []string
	hook  func(context.Context, string) (storegapdb.Record, error, bool)
}

func (r *retainedPrivateFakeReader) Get(ctx context.Context, key string) (storegapdb.Record, error) {
	r.trace = append(r.trace, key)
	if r.hook != nil {
		if record, err, handled := r.hook(ctx, key); handled {
			return record, err
		}
	}
	if record, ok := r.rows[key]; ok {
		return record, nil
	}
	return storegapdb.Record{}, errors.New("provider SECRET missing " + key)
}

func retainedPrivateSourceCanonical(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func retainedPrivateSourceLiteral(t *testing.T, raw string) []byte {
	t.Helper()
	value, err := journalv2.CanonicalObject([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func newRetainedPrivateFixture(t *testing.T, n int) retainedPrivateFixture {
	t.Helper()
	f := retainedPrivateFixture{binding: AuthorityBindingV2{SchemaVersion: 2,
		HostAuthorityID: "host-private", DatabaseID: "database-private",
		AdmittedTupleSHA: strings.Repeat("a", 64), SidecarSHA: strings.Repeat("b", 64),
		WireProtocol: 1, StorageProtocol: 1, CreatedOperationID: "authority-binding-private"},
		reader: &retainedPrivateFakeReader{rows: make(map[string]storegapdb.Record)}}
	bindingBytes, err := f.binding.canonical()
	if err != nil {
		t.Fatal(err)
	}
	f.reader.rows[authorityBindingKey] = storegapdb.Record{Key: authorityBindingKey, Value: bindingBytes, Revision: 1}
	for i := 1; i <= n; i++ {
		c := runtimeCurrentV2{SchemaVersion: 2, RuntimeEpochID: journalv2.RuntimeEpochID(fmt.Sprintf("epoch-%032x", i)),
			HostAuthorityID: f.binding.HostAuthorityID, DatabaseID: f.binding.DatabaseID,
			RecoveredStoreRevision: uint64(2*i - 1), ProjectionSHA256: strings.Repeat("c", 64),
			CreatedOperationID: fmt.Sprintf("runtime-admit-%032x", i), RecoveryAttemptID: fmt.Sprintf("restore-%032x", i), Status: "ready"}
		if i > 1 {
			c.PredecessorEpochID = f.current[i-2].RuntimeEpochID
		}
		f.current = append(f.current, c)
		f.replaceCurrent(t, i-1, c)
	}
	f.captured, err = journalv2.NewStoreRevisionV2(f.binding.DatabaseID, uint64(n*2+7), uint64(n*2+7))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *retainedPrivateFixture) replaceCurrent(t *testing.T, index int, c runtimeCurrentV2) {
	t.Helper()
	f.current[index] = c
	revision := uint64((index + 1) * 2)
	put := func(key string, value []byte) {
		f.reader.rows[key] = storegapdb.Record{Key: key, Value: value, Revision: revision}
	}
	epochBytes, err := runtimeEpochRecordBytes(c) // Actual existing source helper.
	if err != nil {
		t.Fatal(err)
	}
	put(runtimeEpochRecordKey(c.RuntimeEpochID), epochBytes)
	put("spk/runtime-admission/v2/operations/"+c.CreatedOperationID, retainedPrivateSourceLiteral(t,
		fmt.Sprintf(`{"operation_id":%q,"runtime_epoch_id":%q,"schema_version":2,"status":"resolved"}`, c.CreatedOperationID, c.RuntimeEpochID)))
	put("spk/runtime-admission/v2/events/"+string(c.RuntimeEpochID), retainedPrivateSourceLiteral(t,
		fmt.Sprintf(`{"event_type":"runtime_host.epoch_admitted","operation_id":%q,"runtime_epoch_id":%q,"schema_version":2}`, c.CreatedOperationID, c.RuntimeEpochID)))
	if index == len(f.current)-1 {
		put(runtimeCurrentKey, retainedPrivateSourceCanonical(t, c))
		put(retainedPrivateHeadKey, retainedPrivateSourceLiteral(t,
			fmt.Sprintf(`{"runtime_epoch_id":%q,"schema_version":2,"status":"ready"}`, c.RuntimeEpochID)))
	}
}

func (f retainedPrivateFixture) observe(ctx context.Context, p retainedPrivatePolicy) (retainedPrivateRecords, error) {
	return observeRetainedPrivateRecords(ctx, f.reader, f.binding, f.current[len(f.current)-1].RuntimeEpochID, f.captured, p)
}

func retainedPrivateAssertRefused(t *testing.T, result retainedPrivateRecords, err error) {
	t.Helper()
	if err == nil || result.rows != nil {
		t.Fatalf("partial/accepted result: rows=%d err=%v", len(result.rows), err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "spk/") || strings.Contains(err.Error(), "epoch-") || strings.Contains(err.Error(), "database-private") {
		t.Fatalf("content-bearing error: %v", err)
	}
}

func TestRetainedPrivateSourceMembership(t *testing.T) {
	for _, n := range []int{1, 2, 64} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newRetainedPrivateFixture(t, n)
			p := defaultRetainedPrivatePolicy()
			p.maximumEpochs, p.maximumRows = n, 3+3*n
			var actualBytes uint64
			for _, row := range f.reader.rows {
				actualBytes += uint64(len(row.Value))
			}
			p.maximumTotalBytes = actualBytes
			// Tight RPC reserve: every accepted prefix plus one ordinary-value
			// response and two maximum private values for a starting triple.
			p.maximumRPCBytes = actualBytes + storegapdb.MaxOrdinaryValueBytes + 2*p.maximumValueBytes
			t.Logf("epochs=%d independent_rows=%d actual_value_bytes=%d rpc_budget=%d", n, len(f.reader.rows), actualBytes, p.maximumRPCBytes)
			result, err := f.observe(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.rows) != len(f.reader.rows) || len(f.reader.trace) != len(f.reader.rows) {
				t.Fatalf("rows=%d reads=%d source=%d", len(result.rows), len(f.reader.trace), len(f.reader.rows))
			}
			seen := make(map[string]bool)
			for _, key := range f.reader.trace {
				if seen[key] {
					t.Fatalf("duplicate read %q", key)
				}
				seen[key] = true
				got, ok := result.rows[key]
				want := f.reader.rows[key]
				if !ok || got.key != key || got.revision != want.Revision || !bytes.Equal(got.value, want.Value) {
					t.Fatalf("unread/wrong member %q", key)
				}
			}
		})
	}
}

func TestRetainedPrivateEveryRowEnvelope(t *testing.T) {
	base := newRetainedPrivateFixture(t, 2)
	mutations := []struct {
		name   string
		change func(*storegapdb.Record)
	}{
		{"echo", func(r *storegapdb.Record) { r.Key = "SECRET-elsewhere" }},
		{"zero_revision", func(r *storegapdb.Record) { r.Revision = 0 }},
		{"future_revision", func(r *storegapdb.Record) { r.Revision = 999 }},
		{"expiry", func(r *storegapdb.Record) { stamp := time.Unix(1, 0); r.ExpiresAt = &stamp }},
		{"empty", func(r *storegapdb.Record) { r.Value = nil }},
		{"content", func(r *storegapdb.Record) { r.Value = []byte(`{"SECRET":true}`) }},
		{"private_oversize", func(r *storegapdb.Record) { r.Value = bytes.Repeat([]byte("x"), retainedPrivateMaxValueBytes+1) }},
		{"product_oversize", func(r *storegapdb.Record) { r.Value = bytes.Repeat([]byte("x"), storegapdb.MaxOrdinaryValueBytes+1) }},
	}
	for key := range base.reader.rows {
		for _, mutation := range mutations {
			t.Run(key+"/"+mutation.name, func(t *testing.T) {
				f := newRetainedPrivateFixture(t, 2)
				row := f.reader.rows[key]
				mutation.change(&row)
				f.reader.rows[key] = row
				result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
				retainedPrivateAssertRefused(t, result, err)
			})
		}
		t.Run(key+"/missing", func(t *testing.T) {
			f := newRetainedPrivateFixture(t, 2)
			delete(f.reader.rows, key)
			result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
			retainedPrivateAssertRefused(t, result, err)
		})
	}
}

func TestRetainedPrivateEveryRowStrictSourceBytes(t *testing.T) {
	base := newRetainedPrivateFixture(t, 2)
	changes := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"unknown", func(b []byte) []byte { return append([]byte(`{"SECRET_unknown":true,`), b[1:]...) }},
		{"duplicate", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema_version":2`), []byte(`"schema_version":2,"schema_version":2`), 1)
		}},
		{"trailing", func(b []byte) []byte { return append(b, []byte(` {"SECRET":1}`)...) }},
		{"whitespace", func(b []byte) []byte { return append([]byte(" "), b...) }},
		{"schema", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema_version":2`), []byte(`"schema_version":3`), 1)
		}},
		{"null_schema", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema_version":2`), []byte(`"schema_version":null`), 1)
		}},
		{"field_case", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema_version"`), []byte(`"SCHEMA_VERSION"`), 1)
		}},
	}
	for key := range base.reader.rows {
		for _, change := range changes {
			t.Run(key+"/"+change.name, func(t *testing.T) {
				f := newRetainedPrivateFixture(t, 2)
				row := f.reader.rows[key]
				row.Value = change.mutate(row.Value)
				f.reader.rows[key] = row
				result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
				retainedPrivateAssertRefused(t, result, err)
			})
		}
	}
}

func TestRetainedPrivateCurrentAndEpochValidation(t *testing.T) {
	changes := []struct {
		name   string
		change func(*runtimeCurrentV2)
	}{
		{"host", func(c *runtimeCurrentV2) { c.HostAuthorityID = "SECRET-host" }},
		{"database", func(c *runtimeCurrentV2) { c.DatabaseID = "SECRET-db" }},
		{"schema", func(c *runtimeCurrentV2) { c.SchemaVersion = 1 }},
		{"not_ready", func(c *runtimeCurrentV2) { c.Status = "recovering" }},
		{"hash_upper", func(c *runtimeCurrentV2) { c.ProjectionSHA256 = strings.Repeat("A", 64) }},
		{"hash_short", func(c *runtimeCurrentV2) { c.ProjectionSHA256 = "a" }},
		{"epoch_traversal", func(c *runtimeCurrentV2) { c.RuntimeEpochID = "epoch-../SECRET" }},
		{"epoch_upper", func(c *runtimeCurrentV2) {
			c.RuntimeEpochID = journalv2.RuntimeEpochID("epoch-" + strings.Repeat("A", 32))
		}},
		{"predecessor_traversal", func(c *runtimeCurrentV2) { c.PredecessorEpochID = "epoch-../SECRET" }},
		{"operation_traversal", func(c *runtimeCurrentV2) { c.CreatedOperationID = "runtime-admit-../SECRET" }},
		{"operation_freeform", func(c *runtimeCurrentV2) { c.CreatedOperationID = "operation-valid-token" }},
		{"attempt_wrong_prefix", func(c *runtimeCurrentV2) { c.RecoveryAttemptID = "runtime-admit-" + strings.Repeat("a", 32) }},
		{"attempt_source_not_post_restore", func(c *runtimeCurrentV2) { c.RecoveryAttemptID = "restore-post-epoch-" + strings.Repeat("a", 32) }},
		{"attempt_traversal", func(c *runtimeCurrentV2) { c.RecoveryAttemptID = "restore-../SECRET" }},
		{"recovered_before_binding", func(c *runtimeCurrentV2) { c.RecoveredStoreRevision = 0 }},
		{"recovered_at_own_row", func(c *runtimeCurrentV2) { c.RecoveredStoreRevision = 2 }},
		{"recovered_after_own_row", func(c *runtimeCurrentV2) { c.RecoveredStoreRevision = 999 }},
	}
	for _, role := range []string{"current", "old_epoch"} {
		for _, change := range changes {
			t.Run(role+"/"+change.name, func(t *testing.T) {
				f := newRetainedPrivateFixture(t, 2)
				c := f.current[0]
				key := runtimeEpochRecordKey(c.RuntimeEpochID)
				if role == "current" {
					c = f.current[1]
					key = runtimeCurrentKey
				}
				change.change(&c)
				if role == "current" && change.name == "recovered_at_own_row" {
					c.RecoveredStoreRevision = 4
				}
				row := f.reader.rows[key]
				if role == "current" {
					row.Value = retainedPrivateSourceCanonical(t, c)
				} else {
					value, err := runtimeEpochRecordBytes(c)
					if err != nil {
						t.Fatal(err)
					}
					row.Value = value
				}
				f.reader.rows[key] = row
				result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
				retainedPrivateAssertRefused(t, result, err)
				for _, lookup := range f.reader.trace {
					if strings.Contains(lookup, "SECRET") {
						t.Fatalf("unvalidated derived key: %q", lookup)
					}
				}
			})
		}
	}
}

func TestRetainedPrivateBindingBootstrapAndInputs(t *testing.T) {
	changes := []struct {
		name   string
		change func(*retainedPrivateFixture)
	}{
		{"binding_schema", func(f *retainedPrivateFixture) { f.binding.SchemaVersion = 1 }},
		{"binding_host_token", func(f *retainedPrivateFixture) { f.binding.HostAuthorityID = "../SECRET" }},
		{"binding_host_oversize", func(f *retainedPrivateFixture) {
			f.binding.HostAuthorityID = journalv2.HostAuthorityID(strings.Repeat("a", 129))
		}},
		{"binding_db_token", func(f *retainedPrivateFixture) { f.binding.DatabaseID = "../SECRET" }},
		{"binding_operation_token", func(f *retainedPrivateFixture) { f.binding.CreatedOperationID = "../SECRET" }},
		{"binding_hash", func(f *retainedPrivateFixture) { f.binding.SidecarSHA = strings.Repeat("A", 64) }},
		{"binding_tuple_hash", func(f *retainedPrivateFixture) { f.binding.AdmittedTupleSHA = "" }},
		{"binding_wire", func(f *retainedPrivateFixture) { f.binding.WireProtocol = 0 }},
		{"binding_storage", func(f *retainedPrivateFixture) { f.binding.StorageProtocol = 0 }},
		{"captured_zero", func(f *retainedPrivateFixture) { f.captured = journalv2.StoreRevisionV2{} }},
		{"captured_wrong_db", func(f *retainedPrivateFixture) { f.captured, _ = journalv2.NewStoreRevisionV2("SECRET-db", 99, 99) }},
		{"expected_epoch_token", func(f *retainedPrivateFixture) { f.current[0].RuntimeEpochID = "epoch-../SECRET" }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			f := newRetainedPrivateFixture(t, 1)
			change.change(&f)
			result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
			retainedPrivateAssertRefused(t, result, err)
			if len(f.reader.trace) != 0 {
				t.Fatalf("invalid owner inputs read %v", f.reader.trace)
			}
		})
	}
	t.Run("binding_row_cannot_select_database", func(t *testing.T) {
		f := newRetainedPrivateFixture(t, 1)
		altered := f.binding
		altered.DatabaseID = "SECRET-db"
		row := f.reader.rows[authorityBindingKey]
		row.Value = retainedPrivateSourceCanonical(t, altered)
		f.reader.rows[authorityBindingKey] = row
		result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
		retainedPrivateAssertRefused(t, result, err)
		if len(f.reader.trace) != 1 {
			t.Fatalf("binding mismatch allowed further reads: %v", f.reader.trace)
		}
	})
}

func TestRetainedPrivateChronologyAgreementAndCycles(t *testing.T) {
	changes := []struct {
		name   string
		change func(*testing.T, *retainedPrivateFixture)
	}{
		{"current_epoch_mismatch", func(t *testing.T, f *retainedPrivateFixture) {
			c := f.current[1]
			c.ProjectionSHA256 = strings.Repeat("d", 64)
			key := runtimeEpochRecordKey(c.RuntimeEpochID)
			row := f.reader.rows[key]
			row.Value, _ = runtimeEpochRecordBytes(c)
			f.reader.rows[key] = row
		}},
		{"current_revision_mismatch", func(t *testing.T, f *retainedPrivateFixture) {
			row := f.reader.rows[runtimeCurrentKey]
			row.Revision++
			f.reader.rows[runtimeCurrentKey] = row
		}},
		{"head_revision_mismatch", func(t *testing.T, f *retainedPrivateFixture) {
			row := f.reader.rows[retainedPrivateHeadKey]
			row.Revision--
			f.reader.rows[retainedPrivateHeadKey] = row
		}},
		{"head_epoch_mismatch", func(t *testing.T, f *retainedPrivateFixture) {
			row := f.reader.rows[retainedPrivateHeadKey]
			row.Value = retainedPrivateSourceLiteral(t, fmt.Sprintf(`{"runtime_epoch_id":%q,"schema_version":2,"status":"ready"}`, f.current[0].RuntimeEpochID))
			f.reader.rows[retainedPrivateHeadKey] = row
		}},
		{"successor_prefix_before_predecessor", func(t *testing.T, f *retainedPrivateFixture) {
			c := f.current[1]
			c.RecoveredStoreRevision = 1
			f.replaceCurrent(t, 1, c)
		}},
		{"binding_after_oldest_prefix", func(t *testing.T, f *retainedPrivateFixture) {
			row := f.reader.rows[authorityBindingKey]
			row.Revision = 2
			f.reader.rows[authorityBindingKey] = row
		}},
		{"nondecreasing_old_admission", func(t *testing.T, f *retainedPrivateFixture) {
			key := runtimeEpochRecordKey(f.current[0].RuntimeEpochID)
			row := f.reader.rows[key]
			row.Revision = 4
			f.reader.rows[key] = row
		}},
		{"latest_self_cycle", func(t *testing.T, f *retainedPrivateFixture) {
			c := f.current[1]
			c.PredecessorEpochID = c.RuntimeEpochID
			f.replaceCurrent(t, 1, c)
		}},
		{"oldest_self_cycle", func(t *testing.T, f *retainedPrivateFixture) {
			c := f.current[0]
			c.PredecessorEpochID = c.RuntimeEpochID
			f.replaceCurrent(t, 0, c)
		}},
		{"two_epoch_cycle", func(t *testing.T, f *retainedPrivateFixture) {
			c := f.current[0]
			c.PredecessorEpochID = f.current[1].RuntimeEpochID
			f.replaceCurrent(t, 0, c)
		}},
		{"operation_alias", func(t *testing.T, f *retainedPrivateFixture) {
			c := f.current[0]
			c.CreatedOperationID = f.current[1].CreatedOperationID
			key := runtimeEpochRecordKey(c.RuntimeEpochID)
			row := f.reader.rows[key]
			row.Value, _ = runtimeEpochRecordBytes(c)
			f.reader.rows[key] = row
		}},
		{"missing_earlier_link", func(t *testing.T, f *retainedPrivateFixture) {
			c := f.current[0]
			c.PredecessorEpochID = journalv2.RuntimeEpochID("epoch-" + strings.Repeat("f", 32))
			f.replaceCurrent(t, 0, c)
		}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			f := newRetainedPrivateFixture(t, 2)
			change.change(t, &f)
			result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
			retainedPrivateAssertRefused(t, result, err)
			seen := make(map[string]bool)
			for _, key := range f.reader.trace {
				if seen[key] {
					t.Fatalf("alias reread %q", key)
				}
				seen[key] = true
			}
		})
	}
	for _, index := range []int{0, 1} {
		for _, role := range []string{"operation", "event"} {
			t.Run(fmt.Sprintf("%d_%s_revision", index, role), func(t *testing.T) {
				f := newRetainedPrivateFixture(t, 2)
				c := f.current[index]
				key := "spk/runtime-admission/v2/operations/" + c.CreatedOperationID
				if role == "event" {
					key = "spk/runtime-admission/v2/events/" + string(c.RuntimeEpochID)
				}
				row := f.reader.rows[key]
				row.Revision++
				f.reader.rows[key] = row
				result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
				retainedPrivateAssertRefused(t, result, err)
			})
		}
	}
	t.Run("predecessor_revision_equals_recovered_prefix", func(t *testing.T) {
		f := newRetainedPrivateFixture(t, 2)
		c := f.current[1]
		c.RecoveredStoreRevision = 2
		f.replaceCurrent(t, 1, c)
		if _, err := f.observe(context.Background(), defaultRetainedPrivatePolicy()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRetainedPrivateBudgets(t *testing.T) {
	for _, name := range []string{"epochs", "rows", "rpc", "aggregate", "value", "invalid_policy", "invalid_widening"} {
		t.Run(name, func(t *testing.T) {
			f := newRetainedPrivateFixture(t, 2)
			p := defaultRetainedPrivatePolicy()
			switch name {
			case "epochs":
				p.maximumEpochs = 1
			case "rows":
				p.maximumRows = 5
			case "rpc":
				var fixedBytes uint64
				for _, key := range []string{authorityBindingKey, runtimeCurrentKey, retainedPrivateHeadKey} {
					fixedBytes += uint64(len(f.reader.rows[key].Value))
				}
				p.maximumRPCBytes = fixedBytes + storegapdb.MaxOrdinaryValueBytes + 2*p.maximumValueBytes - 1
			case "aggregate":
				p.maximumTotalBytes = uint64(len(f.reader.rows[authorityBindingKey].Value)) - 1
			case "value":
				p.maximumValueBytes = uint64(len(f.reader.rows[authorityBindingKey].Value)) - 1
			case "invalid_policy":
				p.maximumRows = 0
			case "invalid_widening":
				p.maximumValueBytes = storegapdb.MaxOrdinaryValueBytes
			}
			result, err := f.observe(context.Background(), p)
			retainedPrivateAssertRefused(t, result, err)
			if (name == "rows" || name == "rpc") && len(f.reader.trace) != 3 {
				t.Fatalf("triple not reserved before Get: %d", len(f.reader.trace))
			}
			if (name == "aggregate" || name == "value") && len(f.reader.trace) != 1 {
				t.Fatalf("oversized first row continued: %d", len(f.reader.trace))
			}
			if strings.HasPrefix(name, "invalid") && len(f.reader.trace) != 0 {
				t.Fatalf("invalid policy read: %d", len(f.reader.trace))
			}
		})
	}
	t.Run("65_epochs_no_partial_no_196th_rpc", func(t *testing.T) {
		f := newRetainedPrivateFixture(t, 65)
		result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
		retainedPrivateAssertRefused(t, result, err)
		if len(f.reader.trace) != retainedPrivateMaxRows {
			t.Fatalf("reads=%d", len(f.reader.trace))
		}
		oldest := runtimeEpochRecordKey(f.current[0].RuntimeEpochID)
		for _, key := range f.reader.trace {
			if key == oldest {
				t.Fatal("looked up 65th epoch")
			}
		}
	})
	t.Run("aggregate_exact_minus_one", func(t *testing.T) {
		f := newRetainedPrivateFixture(t, 64)
		p := defaultRetainedPrivatePolicy()
		p.maximumTotalBytes = 0
		for _, row := range f.reader.rows {
			p.maximumTotalBytes += uint64(len(row.Value))
		}
		p.maximumTotalBytes--
		result, err := f.observe(context.Background(), p)
		retainedPrivateAssertRefused(t, result, err)
	})
	t.Run("all_policy_ceiling_refusals", func(t *testing.T) {
		p := defaultRetainedPrivatePolicy()
		bad := []retainedPrivatePolicy{p, p, p, p, p, p, p, p, p, p}
		bad[0].maximumEpochs++
		bad[1].maximumRows++
		bad[2].maximumTotalBytes++
		bad[3].maximumRPCBytes++
		bad[4].maximumDuration++
		bad[5].maximumValueBytes = 0
		bad[6].maximumTotalBytes = 0
		bad[7].maximumRPCBytes = 0
		bad[8].maximumDuration = 0
		bad[9].maximumEpochs = -1
		for i, p := range bad {
			f := newRetainedPrivateFixture(t, 1)
			result, err := f.observe(context.Background(), p)
			retainedPrivateAssertRefused(t, result, err)
			if len(f.reader.trace) != 0 {
				t.Fatalf("invalid policy %d issued Get", i)
			}
		}
	})
}

func TestRetainedPrivateCancellationAndProviderSanitization(t *testing.T) {
	for _, position := range []int{0, 1, 2, 3, 4, 5, 6} {
		t.Run(fmt.Sprintf("cancel_%d", position), func(t *testing.T) {
			f := newRetainedPrivateFixture(t, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if position == 0 {
				cancel()
			}
			f.reader.hook = func(_ context.Context, key string) (storegapdb.Record, error, bool) {
				if len(f.reader.trace) == position {
					cancel()
				}
				return f.reader.rows[key], nil, true
			}
			result, err := f.observe(ctx, defaultRetainedPrivatePolicy())
			retainedPrivateAssertRefused(t, result, err)
			if err != context.Canceled || len(f.reader.trace) != position {
				t.Fatalf("err=%v reads=%d", err, len(f.reader.trace))
			}
		})
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, errors.New("SECRET provider diagnostic")} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			f := newRetainedPrivateFixture(t, 1)
			f.reader.hook = func(context.Context, string) (storegapdb.Record, error, bool) {
				return storegapdb.Record{}, fmt.Errorf("SECRET content: %w", sentinel), true
			}
			result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
			retainedPrivateAssertRefused(t, result, err)
			if errors.Is(sentinel, context.Canceled) && err != context.Canceled || errors.Is(sentinel, context.DeadlineExceeded) && err != context.DeadlineExceeded {
				t.Fatalf("not sanitized cancellation: %v", err)
			}
		})
	}
	t.Run("private_deadline", func(t *testing.T) {
		f := newRetainedPrivateFixture(t, 1)
		p := defaultRetainedPrivatePolicy()
		p.maximumDuration = time.Millisecond
		f.reader.hook = func(ctx context.Context, _ string) (storegapdb.Record, error, bool) {
			<-ctx.Done()
			return storegapdb.Record{}, fmt.Errorf("SECRET: %w", ctx.Err()), true
		}
		result, err := f.observe(context.Background(), p)
		retainedPrivateAssertRefused(t, result, err)
		if err != context.DeadlineExceeded {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("nil_context", func(t *testing.T) {
		f := newRetainedPrivateFixture(t, 1)
		result, err := f.observe(nil, defaultRetainedPrivatePolicy())
		retainedPrivateAssertRefused(t, result, err)
	})
	t.Run("nil_reader", func(t *testing.T) {
		f := newRetainedPrivateFixture(t, 1)
		result, err := observeRetainedPrivateRecords(context.Background(), nil, f.binding, f.current[0].RuntimeEpochID, f.captured, defaultRetainedPrivatePolicy())
		retainedPrivateAssertRefused(t, result, err)
	})
}

func TestRetainedPrivateSourceReconstruction(t *testing.T) {
	changes := []struct {
		name   string
		change func([]byte) []byte
	}{
		{"missing_attempt", func(b []byte) []byte {
			var value map[string]any
			if json.Unmarshal(b, &value) != nil {
				t.Fatal("fixture")
			}
			delete(value, "recovery_attempt_id")
			return retainedPrivateSourceCanonical(t, value)
		}},
		{"explicit_empty_predecessor", func(b []byte) []byte {
			var value map[string]any
			if json.Unmarshal(b, &value) != nil {
				t.Fatal("fixture")
			}
			value["predecessor_runtime_epoch_id"] = ""
			return retainedPrivateSourceCanonical(t, value)
		}},
		{"null_predecessor", func(b []byte) []byte {
			var value map[string]any
			if json.Unmarshal(b, &value) != nil {
				t.Fatal("fixture")
			}
			value["predecessor_runtime_epoch_id"] = nil
			return retainedPrivateSourceCanonical(t, value)
		}},
		{"escaped_id", func(b []byte) []byte { return bytes.Replace(b, []byte(`"epoch-`), []byte(`"\u0065poch-`), 1) }},
		{"fractional_schema", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema_version":2`), []byte(`"schema_version":2.0`), 1)
		}},
		{"negative_revision", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"recovered_store_revision":1`), []byte(`"recovered_store_revision":-1`), 1)
		}},
		{"overflow_revision", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"recovered_store_revision":1`), []byte(`"recovered_store_revision":18446744073709551616`), 1)
		}},
		{"invalid_utf8", func(b []byte) []byte { return bytes.Replace(b, []byte("host-private"), []byte{0xff}, 1) }},
		{"deep_unknown", func(b []byte) []byte {
			return append([]byte(`{"SECRET_deep":`+strings.Repeat("[", 100)+"0"+strings.Repeat("]", 100)+","), b[1:]...)
		}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			f := newRetainedPrivateFixture(t, 1)
			row := f.reader.rows[runtimeCurrentKey]
			row.Value = change.change(row.Value)
			f.reader.rows[runtimeCurrentKey] = row
			result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
			retainedPrivateAssertRefused(t, result, err)
			if len(f.reader.trace) != 2 {
				t.Fatalf("unvalidated current derived keys: %v", f.reader.trace)
			}
		})
	}
}

func TestRetainedPrivateWorstResponsePrecharge(t *testing.T) {
	f := newRetainedPrivateFixture(t, 1)
	w := retainedPrivateWork{ctx: context.Background(), reader: f.reader, policy: defaultRetainedPrivatePolicy(), capturedRevision: f.captured.CurrentRevision(), rows: make(map[string]retainedPrivateRecord)}
	f.reader.hook = func(_ context.Context, key string) (storegapdb.Record, error, bool) {
		if w.rpcBytes != storegapdb.MaxOrdinaryValueBytes {
			t.Fatalf("Get without full worst-value charge: %d", w.rpcBytes)
		}
		return f.reader.rows[key], nil, true
	}
	row, err := w.read(authorityBindingKey)
	if err != nil {
		t.Fatal(err)
	}
	if w.rpcBytes != uint64(len(row.value)) {
		t.Fatalf("exact charge after valid response: %d", w.rpcBytes)
	}
	if _, err := w.read(authorityBindingKey); err == nil || len(f.reader.trace) != 1 {
		t.Fatal("duplicate key credited/read")
	}
	f.reader.trace = nil
	w.rows = make(map[string]retainedPrivateRecord)
	w.rpcBytes = 0
	w.policy.maximumRPCBytes = storegapdb.MaxOrdinaryValueBytes - 1
	if _, err := w.read(authorityBindingKey); err == nil || len(f.reader.trace) != 0 {
		t.Fatal("worst response budget not checked before Get")
	}
}

func TestRetainedPrivateMaximumTrustedTokens(t *testing.T) {
	f := newRetainedPrivateFixture(t, 64)
	f.binding.HostAuthorityID = journalv2.HostAuthorityID(strings.Repeat("h", journalv2.MaxIdentityBytes))
	f.binding.DatabaseID = journalv2.DatabaseID(strings.Repeat("d", journalv2.MaxIdentityBytes))
	f.binding.CreatedOperationID = strings.Repeat("o", journalv2.MaxIdentityBytes)
	bindingBytes, err := f.binding.canonical()
	if err != nil {
		t.Fatal(err)
	}
	row := f.reader.rows[authorityBindingKey]
	row.Value = bindingBytes
	f.reader.rows[authorityBindingKey] = row
	for i, c := range f.current {
		c.HostAuthorityID = f.binding.HostAuthorityID
		c.DatabaseID = f.binding.DatabaseID
		f.replaceCurrent(t, i, c)
	}
	f.captured, err = journalv2.NewStoreRevisionV2(f.binding.DatabaseID, f.captured.CurrentRevision(), f.captured.DurableThroughRevision())
	if err != nil {
		t.Fatal(err)
	}
	largest, total := 0, 0
	for _, row := range f.reader.rows {
		if len(row.Value) > largest {
			largest = len(row.Value)
		}
		total += len(row.Value)
	}
	t.Logf("maximum-token 64-epoch fixture largest_value=%d total_value_bytes=%d", largest, total)
	if largest >= 1024 || total > retainedPrivateMaxTotalBytes {
		t.Fatal("fixed source schema ceiling assumption false")
	}
	if _, err := f.observe(context.Background(), defaultRetainedPrivatePolicy()); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedPrivateCopyIsolationAndClosedLookups(t *testing.T) {
	f := newRetainedPrivateFixture(t, 2)
	// A disconnected old record isn't included or guessed. Global count closure
	// must later refuse such extras; this component cannot claim that closure.
	unvisited := "spk/v2/runtime/epochs/epoch-" + strings.Repeat("f", 32)
	f.reader.rows[unvisited] = storegapdb.Record{Key: unvisited, Value: []byte("SECRET hidden extra"), Revision: 1}
	f.reader.hook = func(_ context.Context, key string) (storegapdb.Record, error, bool) {
		if _, ok := f.reader.rows[key]; !ok {
			return storegapdb.Record{Key: key, Value: []byte("SECRET provider ignored prefix"), Revision: 1}, nil, true
		}
		return storegapdb.Record{}, nil, false
	}
	result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.rows[unvisited]; ok {
		t.Fatal("disconnected row credited")
	}
	for _, key := range f.reader.trace {
		if key == unvisited {
			t.Fatal("unvisited row lookup")
		}
		owned := result.rows[key]
		source := f.reader.rows[key]
		expected := bytes.Clone(source.Value)
		source.Value[0] ^= 1
		if !bytes.Equal(owned.value, expected) {
			t.Fatalf("provider alias %q", key)
		}
		owned.value[1] ^= 1
		if source.Value[1] != expected[1] {
			t.Fatalf("result aliases provider %q", key)
		}
	}
	t.Run("malicious_ids_never_used_even_if_provider_ignores_prefix", func(t *testing.T) {
		f := newRetainedPrivateFixture(t, 1)
		c := f.current[0]
		c.CreatedOperationID = "../../SECRET"
		row := f.reader.rows[runtimeCurrentKey]
		row.Value = retainedPrivateSourceCanonical(t, c)
		f.reader.rows[runtimeCurrentKey] = row
		f.reader.hook = func(_ context.Context, key string) (storegapdb.Record, error, bool) {
			if row, ok := f.reader.rows[key]; ok {
				return row, nil, true
			}
			return f.reader.rows[runtimeCurrentKey], nil, true
		}
		result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
		retainedPrivateAssertRefused(t, result, err)
		if len(f.reader.trace) != 2 || f.reader.trace[0] != authorityBindingKey || f.reader.trace[1] != runtimeCurrentKey {
			t.Fatalf("unsafe lookups: %v", f.reader.trace)
		}
	})
}
