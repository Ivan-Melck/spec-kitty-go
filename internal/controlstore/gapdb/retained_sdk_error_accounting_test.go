package gapdb

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

// This is a PARTIAL conditional source-event leaf, not actual escape/heap
// multiplicity or a full decoder/transport/observation allocation certificate.
type retainedSDKErrorLedger struct {
	calls, requestedCandidates, conditionalSlots uint64
	fullCallCoverage                             bool
}

// A remote classification may occur at most once per attempted fixed RPC.
// The structural list envelope is128, not frameBytes/2: retainedScanner rejects
// longer arrays BEFORE retainedTyped can materialize an SDK error.
func retainedSDKErrorEvents(calls, mutationIndexes, supportedVersions uint64) (retainedSDKErrorLedger, bool) {
	if calls > retainedObservationCalls || mutationIndexes > 128 || supportedVersions > 128 {
		return retainedSDKErrorLedger{}, false
	}
	definitions := gapdbapi.ErrorDefinitions() // model construction, outside observer scope
	if len(definitions) != 41 {
		return retainedSDKErrorLedger{}, false
	}
	var request, slots uint64
	charge := func(q uint64, scanned bool) bool {
		rounded, ok := retainedAllocatorRound(q, scanned)
		if !ok {
			return false
		}
		var valid bool
		request, valid = retainedAccountingAdd(request, q)
		if !valid {
			return false
		}
		slots, valid = retainedAccountingAdd(slots, rounded.slotUpper)
		return valid
	}
	if !charge(uint64(len(definitions))*uint64(unsafe.Sizeof(gapdbapi.ErrorDefinition{})), true) {
		return retainedSDKErrorLedger{}, false
	}
	maxActions := uint64(0)
	for _, d := range definitions {
		n := uint64(len(d.SafeActions))
		if n < 2 || n > 4 {
			return retainedSDKErrorLedger{}, false
		}
		maxActions = max(maxActions, n)
		// Literal backing is a CONDITIONAL compiler candidate, not a proved
		// fresh allocation. The SDK's append-from-nil clone is separate.
		if !charge(n*16, true) {
			return retainedSDKErrorLedger{}, false
		}
		clone, ok := retainedAllocatorAppendNil(n, 16, true)
		if !ok || !charge(clone.requested, true) {
			return retainedSDKErrorLedger{}, false
		}
	}
	// Success-through-classification branch superset: returned Error.Clone
	// object and its three slice clones. Typed original/error strings/maps,
	// decoding growth and reflection are NOT covered by these terms.
	if !charge(uint64(unsafe.Sizeof(gapdbapi.Error{})), true) {
		return retainedSDKErrorLedger{}, false
	}
	for _, in := range []struct {
		n, element uint64
		scanned    bool
	}{
		{maxActions, 16, true}, {mutationIndexes, 8, false}, {supportedVersions, 8, false},
	} {
		clone, ok := retainedAllocatorAppendNil(in.n, in.element, in.scanned)
		if !ok || !charge(clone.requested, in.scanned) {
			return retainedSDKErrorLedger{}, false
		}
	}
	requested, ok := retainedAccountingMul(request, calls)
	if !ok {
		return retainedSDKErrorLedger{}, false
	}
	totalSlots, ok := retainedAccountingMul(slots, calls)
	if !ok {
		return retainedSDKErrorLedger{}, false
	}
	return retainedSDKErrorLedger{calls: calls, requestedCandidates: requested, conditionalSlots: totalSlots}, true
}

func TestRetainedSDKErrorConditionalEvents(t *testing.T) {
	if runtime.Version() != "go1.26.7" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("requires pinned source/layout profile")
	}
	if unsafe.Sizeof(gapdbapi.ErrorDefinition{}) != 56 || unsafe.Sizeof(gapdbapi.SafeAction("")) != 16 {
		t.Fatal("SDK definition layout drift")
	}
	one, ok := retainedSDKErrorEvents(1, 128, 128)
	if !ok || one.fullCallCoverage || one.requestedCandidates == 0 || one.conditionalSlots < one.requestedCandidates {
		t.Fatal("invalid partial envelope", one)
	}
	all, ok := retainedSDKErrorEvents(retainedObservationCalls, 128, 128)
	if !ok || all.requestedCandidates != one.requestedCandidates*retainedObservationCalls || all.conditionalSlots != one.conditionalSlots*retainedObservationCalls || all.fullCallCoverage {
		t.Fatal("per-event multiplicity drift")
	}
	zero, ok := retainedSDKErrorEvents(0, 0, 0)
	if !ok || zero != (retainedSDKErrorLedger{}) {
		t.Fatal("zero schedule charged")
	}
	for _, v := range [][3]uint64{{retainedObservationCalls + 1, 0, 0}, {1, 129, 0}, {1, 0, 129}, {^uint64(0), 128, 128}} {
		if got, ok := retainedSDKErrorEvents(v[0], v[1], v[2]); ok || got != (retainedSDKErrorLedger{}) {
			t.Fatal("invalid input earned a ledger")
		}
	}
	t.Logf("SDK error object=%d; conditional source candidates/call requested=%d slots=%d; not full coverage", unsafe.Sizeof(gapdbapi.Error{}), one.requestedCandidates, one.conditionalSlots)
}

func TestRetainedSDKErrorCloneOwnership(t *testing.T) {
	first, second := gapdbapi.ErrorDefinitions(), gapdbapi.ErrorDefinitions()
	if len(first) != 41 || len(second) != 41 {
		t.Fatal("definition inventory drift")
	}
	for i := range first {
		original := second[i].SafeActions[0]
		first[i].SafeActions[0] = "test-only-mutated"
		if second[i].SafeActions[0] != original {
			t.Fatal("definitions share mutable clone backing")
		}
	}
	e := gapdbapi.Error{SafeActions: []gapdbapi.SafeAction{"a", "b"}, MutationIndexes: []int{0, 1}, SupportedVersions: []uint64{1, 2}}
	clone := e.Clone()
	clone.SafeActions[0], clone.MutationIndexes[0], clone.SupportedVersions[0] = "changed", 99, 99
	if e.SafeActions[0] != "a" || e.MutationIndexes[0] != 0 || e.SupportedVersions[0] != 1 {
		t.Fatal("SDK clone aliases owned backings")
	}
	if clone.AssertionIndex != nil {
		t.Fatal("forbidden assertion clone reached this envelope")
	}
}

func TestRetainedSDKErrorReachabilitySourceControls(t *testing.T) {
	local, err := os.ReadFile("retained_transport.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"len(n.items) >= 128", "if len(n.raw) > retainedSmallJSON", "remote.Message == \"\" || remote.AssertionIndex != nil", "range gapdbapi.ErrorDefinitions()", "return retainedResult{}, remote.Clone()"} {
		if !strings.Contains(string(local), fragment) {
			t.Fatal("source guard drift", fragment)
		}
	}
	// Exact caller ordering and scalar parser limits are also semantic-test
	// obligations; these fragments are drift leads, not a whole-graph proof.
}
