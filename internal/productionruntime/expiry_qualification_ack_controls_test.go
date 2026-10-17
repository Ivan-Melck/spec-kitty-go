//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// W4 CODE ONLY, UNTESTED. These are coordinator/visitor component controls,
// NOT a genuine Host, provider receipt, private admission ACK or Native result.
// In particular, changing readback below is STRUCTURAL corruption, never an
// invalid opaque ACK. Native fixtures belong only in the private cache bundle.
func TestExpiryQualificationACKIndependentAddressGeometry(t *testing.T) {
	for _, admissions := range []int{1, 2} {
		t.Run(fmt.Sprintf("admissions_%d", admissions), func(t *testing.T) {
			_, f := newPublicationProofFixture(t, true, journalv2.Scope{Kind: journalv2.ScopeHost}, "operation-w4-geometry", "with-claim")
			if len(f.observerACKs) != 2 {
				t.Fatal("original coordinator did not mint both ACKs")
			}
			first, last := f.observerACKs[0], f.observerACKs[1]
			// Independent semantic address set: NOT expiryFixtureFinalPublicationRows
			// or the retained-map builder, and NOT just a cardinality comparison.
			wanted := map[string]uint64{
				"binding": first.ProviderRevision(), "head": last.ProviderRevision(),
				"event/1": first.ProviderRevision(), "event/2": last.ProviderRevision(),
				"claim": first.ProviderRevision(),
				"witness/" + string(first.PublicationID()): first.ProviderRevision(),
				"witness/" + string(last.PublicationID()): last.ProviderRevision(),
			}
			seen := make(map[string]uint64)
			var oldHead, finalHead []byte
			for index, ack := range f.observerACKs {
				for _, row := range ack.Plan().Records() {
					var name string
					switch row.Role() {
					case journalv2.RecordOperationBinding:
						name = "binding"
					case journalv2.RecordOperationHead:
						name = "head"
						if index == 0 { oldHead = row.CanonicalValue() } else { finalHead = row.CanonicalValue() }
					case journalv2.RecordJournalEvent:
						name = fmt.Sprintf("event/%d", row.Target().LogicalVersion())
					case journalv2.RecordWorkspacePathClaim:
						name = "claim"
					case journalv2.RecordPublicationWitness:
						name = "witness/" + string(row.Target().PublicationID())
					default:
						t.Fatal("unexpected publication address", row.Role())
					}
					if prior, exists := seen[name]; exists && (name != "head" || prior != first.ProviderRevision()) {
						t.Fatal("immutable address overwritten", name)
					}
					seen[name] = ack.ProviderRevision()
				}
			}
			if len(wanted) != 7 || !reflect.DeepEqual(seen, wanted) || bytes.Equal(oldHead, finalHead) {
				t.Fatal("seven-address final map differs, including actual overwritten head")
			}
			private := newRetainedPrivateFixture(t, admissions)
			w4ACKApplyBinding(t, &private)
			result, err := private.observe(t.Context(), defaultRetainedPrivatePolicy())
			if err != nil { t.Fatal(err) }
			wantTrace := w4ACKPrivateTrace(private)
			if !reflect.DeepEqual(private.reader.trace, wantTrace) || len(result.rows) != 3+3*admissions {
				t.Fatal("not the independent binding/current/head plus predecessor-triple read set", err)
			}
			for _, key := range wantTrace {
				got, ok := result.rows[key]
				row := private.reader.rows[key]
				if !ok || got.key != key || got.revision != row.Revision || !bytes.Equal(got.value, row.Value) {
					t.Fatal("unvisited or inexact private address", key)
				}
			}
		})
	}
}

func w4ACKApplyBinding(t *testing.T, f *retainedPrivateFixture) {
	t.Helper()
	f.binding = expiryTestBinding()
	raw, err := f.binding.canonical()
	if err != nil { t.Fatal(err) }
	row := f.reader.rows[authorityBindingKey]
	row.Value = raw
	f.reader.rows[authorityBindingKey] = row
	for i, current := range f.current {
		current.HostAuthorityID, current.DatabaseID = f.binding.HostAuthorityID, f.binding.DatabaseID
		f.replaceCurrent(t, i, current)
	}
	f.captured, err = journalv2.NewStoreRevisionV2(f.binding.DatabaseID, f.captured.CurrentRevision(), f.captured.DurableThroughRevision())
	if err != nil { t.Fatal(err) }
}

func w4ACKPrivateTrace(f retainedPrivateFixture) []string {
	trace := []string{authorityBindingKey, runtimeCurrentKey, retainedPrivateHeadKey}
	for i := len(f.current)-1; i >= 0; i-- {
		current := f.current[i]
		trace = append(trace, runtimeEpochRecordKey(current.RuntimeEpochID),
			"spk/runtime-admission/v2/operations/"+current.CreatedOperationID,
			"spk/runtime-admission/v2/events/"+string(current.RuntimeEpochID))
	}
	return trace
}

// The old owner overlay already covers 240 incompatible-type mutants and all
// 186 schema1 binding mutants. Do NOT run that overlay here or count them again.
// This literal residual list excludes the binding and every schema_version
// (old per-row strict-source cases). It covers each remaining PRESENT member,
// including wrapper.current itself and both distinct predecessor coordinates.
func w4ACKResidualMembers() map[string][]string {
	current := []string{"runtime_epoch_id", "host_authority_id", "database_id", "recovered_store_revision", "projection_sha256", "created_operation_id", "recovery_attempt_id", "status"}
	latest := append(append([]string(nil), current...), "predecessor_runtime_epoch_id")
	wrap := func(fields []string) []string {
		out := []string{"current"}
		for _, field := range fields { out = append(out, "current."+field) }
		return out
	}
	return map[string][]string{
		"current": latest,
		"head": {"runtime_epoch_id", "status"},
		"epoch_latest": wrap(latest), "epoch_oldest": wrap(current),
		"operation_latest": {"operation_id", "runtime_epoch_id", "status"},
		"operation_oldest": {"operation_id", "runtime_epoch_id", "status"},
		"event_latest": {"event_type", "operation_id", "runtime_epoch_id"},
		"event_oldest": {"event_type", "operation_id", "runtime_epoch_id"},
	}
}

func w4ACKRoleKeys(f retainedPrivateFixture) map[string]string {
	return map[string]string{
		"current": runtimeCurrentKey, "head": retainedPrivateHeadKey,
		"epoch_latest": runtimeEpochRecordKey(f.current[1].RuntimeEpochID),
		"epoch_oldest": runtimeEpochRecordKey(f.current[0].RuntimeEpochID),
		"operation_latest": "spk/runtime-admission/v2/operations/"+f.current[1].CreatedOperationID,
		"operation_oldest": "spk/runtime-admission/v2/operations/"+f.current[0].CreatedOperationID,
		"event_latest": "spk/runtime-admission/v2/events/"+string(f.current[1].RuntimeEpochID),
		"event_oldest": "spk/runtime-admission/v2/events/"+string(f.current[0].RuntimeEpochID),
	}
}

// Mutate ONE literal member at its actual nesting depth. Duplicate and case
// aliases are inserted without map re-encoding, which would erase duplicates.
func w4ACKChangeMember(t *testing.T, raw []byte, path []string, mode string) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil { t.Fatal(err) }
	value, exists := object[path[0]]
	if !exists { t.Fatal("residual inventory names absent member", path) }
	if len(path) > 1 {
		object[path[0]] = w4ACKChangeMember(t, value, path[1:], mode)
		// json.Marshal rejects deliberately malformed nested JSON. Replace its
		// exact original value in the canonical parent instead.
		needle := append([]byte(fmt.Sprintf("%q:", path[0])), value...)
		replacement := append([]byte(fmt.Sprintf("%q:", path[0])), object[path[0]]...)
		return bytes.Replace(raw, needle, replacement, 1)
	}
	switch mode {
	case "missing":
		delete(object, path[0])
		return retainedPrivateSourceCanonical(t, object)
	case "null":
		object[path[0]] = json.RawMessage("null")
		return retainedPrivateSourceCanonical(t, object)
	case "duplicate", "case_alias":
		name := path[0]
		if mode == "case_alias" { name = strings.ToUpper(name) }
		extra := append([]byte(fmt.Sprintf(",%q:", name)), value...)
		return append(append(bytes.Clone(raw[:len(raw)-1]), extra...), '}')
	case "malformed":
		needle := append([]byte(fmt.Sprintf("%q:", path[0])), value...)
		return bytes.Replace(raw, needle, []byte(fmt.Sprintf("%q:!", path[0])), 1)
	default:
		t.Fatal("unknown residual mutation", mode)
		return nil
	}
}

func TestExpiryQualificationPrivateResidualClosedMembers(t *testing.T) {
	members := w4ACKResidualMembers()
	roles := make([]string, 0, len(members))
	for role := range members { roles = append(roles, role) }
	sort.Strings(roles)
	for _, role := range roles {
		for _, member := range members[role] {
			for _, mode := range []string{"missing", "null", "duplicate", "case_alias", "malformed"} {
				t.Run(role+"/"+member+"/"+mode, func(t *testing.T) {
					f := newRetainedPrivateFixture(t, 2)
					w4ACKApplyBinding(t, &f)
					trace := w4ACKPrivateTrace(f)
					key := w4ACKRoleKeys(f)[role]
					row := f.reader.rows[key]
					original := bytes.Clone(row.Value)
					row.Value = w4ACKChangeMember(t, row.Value, strings.Split(member, "."), mode)
					if bytes.Equal(original, row.Value) || mode == "malformed" && json.Valid(row.Value) {
						t.Fatal("mutation was not literal at the inventoried member")
					}
					f.reader.rows[key] = row
					result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
					retainedPrivateAssertRefused(t, result, err)
					stopKey := key
					if role == "current" && member == "predecessor_runtime_epoch_id" && mode == "missing" {
						// Ordinary first-generation schema2 legitimately omits this
						// member (omitempty). A missing successor predecessor becomes
						// provably inconsistent at its immutable latest epoch copy,
						// not by requiring a new mandatory ordinary wire member.
						stopKey = w4ACKRoleKeys(f)["epoch_latest"]
					}
					stop := -1
					for i, address := range trace { if address == stopKey { stop = i; break } }
					if stop < 0 || !reflect.DeepEqual(f.reader.trace, trace[:stop+1]) || !reflect.ValueOf(result).IsZero() {
						t.Fatal("refusal passed its semantic validation frontier; suffix/partial records escaped", f.reader.trace)
					}
				})
			}
		}
	}
}
