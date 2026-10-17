package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"unsafe"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// This is a test-only, source-logical ledger, NOT an allocation, peak-heap,
// process, latency, or complete observer bound. Counts are cumulative per call,
// not concurrent live sets. In particular, stack geometry is not heap usage.
// Every omitted per-call library object remains OWNED, not silently shared.
type retainedAccountingInput struct {
	acks, records, values, frames, decoded, privateBytes uint64
	attempts, hashes, imageBytes                         uint64
}

type retainedAccountingGap struct{ site, classification string }

var retainedAccountingOpenTerms = [...]retainedAccountingGap{
	{"FIRST custody: connection close owners/Once, transport dial rundown, shared teardown/error chains and retained actual Wait owner", "owned/unqualified"},
	{"v2KeyForSealedRecord / parseV2Key: strings, encoding, validation scratch", "owned"},
	{"retainedScanner.value / retainedShape: nodes, maps, slices, reflection scratch", "owned/delegated-owned"},
	{"retainedTyped / retainedRemoteError: small JSON, strings, error clones", "owned/delegated-owned"},
	{"exchange: request marshal buffers, request IDs, closures, timers, context state", "owned/delegated-owned"},
	{"private read/decode/canonical: maps, schema strings, JSON decoder and canonicalization scratch", "owned/delegated-owned"},
	{"retainedkernel checks: 32 KiB chunks, proc buffers, hash/file/syscall wrappers", "owned/delegated-owned"},
	{"DialContext / UnixConn / AfterFunc: wrappers, cancellation tasks, descriptors, socket kernel state", "owned/delegated-owned/kernel-owned"},
	{"append/make allocator size classes, spare capacity, stack frames and growth, GC metadata", "runtime"},
}

type retainedAccountingLedger struct {
	ackSnapshotLogical, orderLogical                            uint64
	occurrenceBackingsLogical, recordsBackingsLogical           uint64
	valueCopiesLogical                                          uint64
	framePayloadLogical, decodedBackingLogical                  uint64
	sdkValidationCloneLogicalUpper, base64RoundtripLogicalUpper uint64
	transportLargeBuffersLogicalUpper                           uint64 // includes preceding four terms ONCE
	adapterPrivateCopiesLogicalUpper, registryNodesLogicalUpper uint64
	builderCoveredSourceLogical, coveredSourceLogicalUpper      uint64
	hashReadWork                                                uint64 // work, never summed as retained bytes
	fullCallCoverage                                            bool
	missingRecipes                                              []retainedAccountingGap
}

func retainedAccountingAdd(a, b uint64) (uint64, bool) {
	if b > math.MaxUint64-a {
		return 0, false
	}
	return a + b, true
}

func retainedAccountingMul(a, b uint64) (uint64, bool) {
	if a != 0 && b > math.MaxUint64/a {
		return 0, false
	}
	return a * b, true
}

func retainedAccountingSum(terms ...uint64) (uint64, bool) {
	var sum uint64
	for _, term := range terms {
		var ok bool
		sum, ok = retainedAccountingAdd(sum, term)
		if !ok {
			return 0, false
		}
	}
	return sum, true
}

func retainedAccountResources(in retainedAccountingInput) (retainedAccountingLedger, bool) {
	refused := retainedAccountingLedger{}
	if in.acks > retainedPublicationLimitV2 || in.records > retainedRecordLimitV2 ||
		in.values > retainedValueBytesLimitV2 || in.frames > retainedObservationFrames ||
		in.privateBytes > 256<<10 || in.imageBytes == 0 || in.imageBytes > 64<<20 {
		return refused, false
	}
	attemptLimit, ok := retainedAccountingAdd(in.records, retainedObservationPrivateRows+3)
	if !ok || in.attempts > attemptLimit {
		return refused, false
	}
	threeAttempts, ok := retainedAccountingMul(3, in.attempts)
	if !ok {
		return refused, false
	}
	hashLimit, ok := retainedAccountingAdd(threeAttempts, 1)
	if !ok || in.hashes > hashLimit {
		return refused, false
	}
	hashWork, ok := retainedAccountingMul(in.imageBytes, in.hashes)
	if !ok || hashWork > retainedObservationHashes {
		return refused, false
	}
	threeFrames, ok := retainedAccountingMul(3, in.frames)
	if !ok || in.decoded > threeFrames/4 {
		return refused, false
	}
	l := retainedAccountingLedger{
		ackSnapshotLogical:  uint64(unsafe.Sizeof([retainedPublicationLimitV2]journalv2.AcknowledgedPublicationV2{})),
		orderLogical:        uint64(unsafe.Sizeof([retainedPublicationLimitV2]retainedPublicationOrderV2{})),
		framePayloadLogical: in.frames, decodedBackingLogical: in.decoded,
		// NewPutMutation clones Value even though the mutation is only validated.
		sdkValidationCloneLogicalUpper: in.decoded,
		// Each flush: EncodeToString's byte buffer AND returned string, plus
		// string(part). Sum chunk input lengths <= F, including JSON escapes.
		// Charge all three even if this compiler elides the comparison string.
		base64RoundtripLogicalUpper: threeFrames,
		hashReadWork:                hashWork, fullCallCoverage: false,
		missingRecipes: append([]retainedAccountingGap(nil), retainedAccountingOpenTerms[:]...),
	}
	products := []struct {
		destination *uint64
		a, b        uint64
	}{
		{&l.occurrenceBackingsLogical, 2 * uint64(unsafe.Sizeof(retainedPublicationOccurrenceV2{})), in.records},
		{&l.recordsBackingsLogical, uint64(unsafe.Sizeof(journalv2.SealedRecordV2{})), in.records},
		{&l.valueCopiesLogical, 2, in.values},
		{&l.adapterPrivateCopiesLogicalUpper, 2, in.privateBytes},
		{&l.registryNodesLogicalUpper, uint64(unsafe.Sizeof(retainedConnectionNode{})), in.attempts},
	}
	for _, p := range products {
		*p.destination, ok = retainedAccountingMul(p.a, p.b)
		if !ok {
			return refused, false
		}
	}
	// Accepted private prefix: adapter copy + private clone. A single terminal
	// refused response may first be copied by convertRecord up to 4 MiB.
	l.adapterPrivateCopiesLogicalUpper, ok = retainedAccountingAdd(l.adapterPrivateCopiesLogicalUpper, MaxCheckpointValueBytes)
	if !ok {
		return refused, false
	}
	l.transportLargeBuffersLogicalUpper, ok = retainedAccountingSum(l.framePayloadLogical, l.decodedBackingLogical,
		l.sdkValidationCloneLogicalUpper, l.base64RoundtripLogicalUpper)
	if !ok {
		return refused, false
	}
	elevenFrames, ok := retainedAccountingMul(11, in.frames)
	if !ok || l.transportLargeBuffersLogicalUpper > elevenFrames/2 { // <= 5.5 F, not an extra term
		return refused, false
	}
	l.builderCoveredSourceLogical, ok = retainedAccountingSum(l.ackSnapshotLogical, l.orderLogical,
		l.occurrenceBackingsLogical, l.recordsBackingsLogical, l.valueCopiesLogical)
	if !ok {
		return refused, false
	}
	l.coveredSourceLogicalUpper, ok = retainedAccountingSum(l.builderCoveredSourceLogical,
		l.transportLargeBuffersLogicalUpper, l.adapterPrivateCopiesLogicalUpper, l.registryNodesLogicalUpper)
	if !ok {
		return refused, false
	}
	return l, true
}

func TestRetainedResourceAccountingCompilerGeometry(t *testing.T) {
	// These literals are assertions against actual compiled types, not the
	// calculation's source of geometry. Other architectures require a re-audit.
	for name, pair := range map[string][2]uintptr{
		"ACK":             {unsafe.Sizeof(journalv2.AcknowledgedPublicationV2{}), 800},
		"fixed_ACK":       {unsafe.Sizeof([256]journalv2.AcknowledgedPublicationV2{}), 204800},
		"order":           {unsafe.Sizeof([256]retainedPublicationOrderV2{}), 8192},
		"occurrence":      {unsafe.Sizeof(retainedPublicationOccurrenceV2{}), 368},
		"Records_element": {unsafe.Sizeof(journalv2.SealedRecordV2{}), 360},
		// Revised source-projected amd64 geometry only; MUST be checked by
		// a later authorized compile. This is not a heap/layout certificate.
		"registry_node":   {unsafe.Sizeof(retainedConnectionNode{}), 40},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("%s geometry drift: compiled=%d audited=%d; re-audit recipes", name, pair[0], pair[1])
		}
	}
	if retainedPublicationLimitV2 != 256 || retainedRecordLimitV2 != 4096 || retainedValueBytesLimitV2 != 32<<20 ||
		retainedObservationFrames != 64<<20 || retainedObservationHashes != 32<<30 || retainedObservationPrivateRows != 195 ||
		retainedDecodeBatch != 4096 || MaxCheckpointValueBytes != 4<<20 {
		t.Fatal("source limits drifted: re-audit arithmetic envelope")
	}
	t.Logf("geometry audited against %s %s/%s; logical bytes, not heap placement", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func TestRetainedResourceAccountingZeroOddAndMax(t *testing.T) {
	for _, in := range []retainedAccountingInput{
		{imageBytes: 1},
		{acks: 1, records: 3, values: 7, frames: 5, decoded: 3, privateBytes: 9, attempts: 2, hashes: 7, imageBytes: 11},
		{acks: 256, records: 4096, values: 32 << 20, frames: 64 << 20, decoded: 48 << 20,
			privateBytes: 256 << 10, attempts: 4294, hashes: 12883, imageBytes: (32 << 30) / 12883},
	} {
		l, ok := retainedAccountResources(in)
		if !ok {
			t.Fatalf("valid arithmetic envelope refused: %+v", in)
		}
		builder := uint64(204800+8192) + 2*368*in.records + 360*in.records + 2*in.values
		transport := 4*in.frames + 2*in.decoded
		want := builder + transport + 2*in.privateBytes + (4 << 20) + 40*in.attempts
		if l.builderCoveredSourceLogical != builder || l.transportLargeBuffersLogicalUpper != transport ||
			l.coveredSourceLogicalUpper != want || l.hashReadWork != in.imageBytes*in.hashes ||
			l.fullCallCoverage || len(l.missingRecipes) != len(retainedAccountingOpenTerms) {
			t.Fatalf("inexact or falsely complete ledger: %+v want covered=%d", l, want)
		}
		t.Logf("input=%+v builder-covered-logical=%d transport-large-buffer-upper=%d covered-source-logical-upper=%d incomplete=true", in, builder, transport, want)
	}
	// No dependence on ACK payload size/length for the fixed snapshot/order.
	a, _ := retainedAccountResources(retainedAccountingInput{imageBytes: 1})
	b, _ := retainedAccountResources(retainedAccountingInput{acks: 256, imageBytes: 1})
	if a.ackSnapshotLogical != b.ackSnapshotLogical || a.orderLogical != b.orderLogical {
		t.Fatal("fixed geometry became input-sized")
	}
}

func TestRetainedResourceAccountingInvalidAndJointMax(t *testing.T) {
	base := retainedAccountingInput{acks: 1, records: 1, values: 1, frames: 4, decoded: 3, privateBytes: 1, attempts: 1, hashes: 4, imageBytes: 1}
	changes := map[string]func(*retainedAccountingInput){
		"acks":       func(i *retainedAccountingInput) { i.acks = 257 },
		"records":    func(i *retainedAccountingInput) { i.records = 4097 },
		"values":     func(i *retainedAccountingInput) { i.values = 32<<20 + 1 },
		"frames":     func(i *retainedAccountingInput) { i.frames = 64<<20 + 1 },
		"private":    func(i *retainedAccountingInput) { i.privateBytes = 256<<10 + 1 },
		"decoded":    func(i *retainedAccountingInput) { i.decoded = 4 },
		"attempts":   func(i *retainedAccountingInput) { i.attempts = i.records + 199 },
		"hashes":     func(i *retainedAccountingInput) { i.hashes = 3*i.attempts + 2 },
		"image_zero": func(i *retainedAccountingInput) { i.imageBytes = 0 },
		"image":      func(i *retainedAccountingInput) { i.imageBytes = 64<<20 + 1 },
		"work":       func(i *retainedAccountingInput) { i.attempts = 199; i.hashes = 598; i.imageBytes = (32<<30)/598 + 1 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			in := base
			change(&in)
			if got, ok := retainedAccountResources(in); ok || !reflect.DeepEqual(got, retainedAccountingLedger{}) {
				t.Fatal("invalid accepted or partial ledger returned", in, got)
			}
		})
	}
	// Every independently maximal scalar is NOT jointly admissible.
	joint := retainedAccountingInput{acks: 256, records: 4096, values: 32 << 20, frames: 64 << 20,
		decoded: 48 << 20, privateBytes: 256 << 10, attempts: 4294, hashes: 12883, imageBytes: 64 << 20}
	if _, ok := retainedAccountResources(joint); ok {
		t.Fatal("joint maxima bypassed 32 GiB hash-work constraint")
	}
	// Probe every input's overflow-shaped values, including derived counts.
	for field := 0; field < reflect.TypeOf(base).NumField(); field++ {
		in := base
		// Fields are unexported: use explicit pointer list, not reflection mutation.
		fields := []*uint64{&in.acks, &in.records, &in.values, &in.frames, &in.decoded, &in.privateBytes, &in.attempts, &in.hashes, &in.imageBytes}
		*fields[field] = math.MaxUint64
		if got, ok := retainedAccountResources(in); ok || !reflect.DeepEqual(got, retainedAccountingLedger{}) {
			t.Fatalf("overflow-shaped input %d accepted", field)
		}
	}
}

func TestRetainedResourceAccountingCheckedArithmetic(t *testing.T) {
	for _, p := range [][2]uint64{{0, 0}, {0, math.MaxUint64}, {1, math.MaxUint64}, {3, 5}, {math.MaxUint64 / 11, 11}} {
		if got, ok := retainedAccountingMul(p[0], p[1]); !ok || got != p[0]*p[1] {
			t.Fatal("valid product refused", p)
		}
	}
	for _, p := range [][2]uint64{{2, math.MaxUint64}, {math.MaxUint64, 2}, {math.MaxUint64/11 + 1, 11}} {
		if got, ok := retainedAccountingMul(p[0], p[1]); ok || got != 0 {
			t.Fatal("overflow product accepted", p)
		}
	}
	if got, ok := retainedAccountingAdd(math.MaxUint64-1, 1); !ok || got != math.MaxUint64 {
		t.Fatal("exact sum refused")
	}
	if got, ok := retainedAccountingSum(math.MaxUint64-1, 1, 1); ok || got != 0 {
		t.Fatal("sum overflow accepted or partial returned")
	}
}

func TestRetainedResourceAccountingMonotonicity(t *testing.T) {
	previous := uint64(0)
	for n := uint64(0); n <= 4096; n++ {
		in := retainedAccountingInput{acks: min(n, 256), records: n, values: n * 33,
			frames: n * 101, decoded: n * 75, privateBytes: n * 7,
			attempts: n, hashes: 3*n + 1, imageBytes: 1}
		l, ok := retainedAccountResources(in)
		if !ok || l.coveredSourceLogicalUpper < previous {
			t.Fatal("nonmonotone accepted envelope", in)
		}
		previous = l.coveredSourceLogicalUpper
	}
	// Independent coordinates: changing work does not masquerade as memory.
	base := retainedAccountingInput{acks: 1, records: 3, values: 7, frames: 5, decoded: 3, privateBytes: 9, attempts: 2, hashes: 6, imageBytes: 11}
	before, _ := retainedAccountResources(base)
	for field := 0; field < 9; field++ {
		in := base
		fields := []*uint64{&in.acks, &in.records, &in.values, &in.frames, &in.decoded, &in.privateBytes, &in.attempts, &in.hashes, &in.imageBytes}
		*fields[field]++
		if field == 4 { // D=4 needs F>=ceil(16/3); invalid alone is intentional.
			if _, ok := retainedAccountResources(in); ok {
				t.Fatal("decoded floor bypassed")
			}
			continue
		}
		after, ok := retainedAccountResources(in)
		if !ok || after.coveredSourceLogicalUpper < before.coveredSourceLogicalUpper || after.hashReadWork < before.hashReadWork {
			t.Fatal("coordinate monotonicity", field)
		}
	}
}

func TestRetainedResourceAccountingGenuineCopiesAndOverwrite(t *testing.T) {
	port := &retainedFixturePortV2{revision: 7}
	coordinator := retainedCoordinatorV2(t, port)
	first := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "accounting-chain", nil, 0))
	prior := first.Plan().OperationState()
	port.revision = 29
	second := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "accounting-chain", &prior, 7))
	acks := []journalv2.AcknowledgedPublicationV2{second, first}
	_, budget, err := preflightRetainedPublicationsV2(t.Context(), acks)
	if err != nil {
		t.Fatal(err)
	}
	in := retainedAccountingInput{acks: uint64(len(acks)), records: budget.records, values: budget.values, imageBytes: 1}
	ledger, ok := retainedAccountResources(in)
	if !ok {
		t.Fatal("genuine footprint refused")
	}
	got, err := buildRetainedPublicationMapV2(t.Context(), acks)
	if err != nil || uint64(len(got)) >= budget.records {
		t.Fatal("fixture did not overwrite", err)
	}
	var finalValues uint64
	for _, row := range got {
		finalValues += uint64(len(row.canonical))
	}
	if ledger.valueCopiesLogical != 2*budget.values || budget.values <= finalValues ||
		ledger.occurrenceBackingsLogical != 2*368*budget.records || cap(got) != int(budget.records) {
		t.Fatal("charged final rows rather than all occurrences")
	}
	// Records clones its backing and values; CanonicalValue clones once more.
	records := first.Plan().Records()
	original := records[0].CanonicalValue()
	copyValue := records[0].CanonicalValue()
	copyValue[0] ^= 1
	if bytes.Equal(copyValue, records[0].CanonicalValue()) || !bytes.Equal(original, records[0].CanonicalValue()) {
		t.Fatal("CanonicalValue no longer independently owned")
	}
	records[0] = journalv2.SealedRecordV2{}
	if first.Plan().Records()[0].Role() == "" {
		t.Fatal("Records backing aliased ACK")
	}
	want := retainedCopiedOracleV2(t, acks)
	for i := range got {
		got[i].canonical[0] ^= 1
	}
	again, err := buildRetainedPublicationMapV2(t.Context(), acks)
	if err != nil || !reflect.DeepEqual(retainedDenseOracleViewV2(again), want) {
		t.Fatal("builder mutated ACK bytes", err)
	}
	ledger.missingRecipes[0].site = "caller overwrite"
	fresh, _ := retainedAccountResources(in)
	if fresh.fullCallCoverage || fresh.missingRecipes[0].site == "caller overwrite" {
		t.Fatal("coverage disclosure aliased")
	}
	t.Logf("genuine ACKs=%d occurrences=%d final-rows=%d all-value-bytes=%d final-value-bytes=%d copies-logical=%d", len(acks), budget.records, len(again), budget.values, finalValues, ledger.valueCopiesLogical)
}

func TestRetainedResourceAccountingSourceSites(t *testing.T) {
	// Fail closed on drift at the principal local recipe sites. These anchors
	// supplement (not replace) the audit; they do not certify unlisted objects.
	for path, anchors := range map[string][]string{
		"retained_publication_map.go": {
			"var snapshot [retainedPublicationLimitV2]journalv2.AcknowledgedPublicationV2",
			"rows := make(retainedPublicationMapV2, int(budget.records))",
			"scratch := make(retainedPublicationMapV2, int(budget.records))",
			".Plan().Records()", "value := record.CanonicalValue()", "clear(rows[used:])", "clear(scratch)",
		},
		"retained_observation.go": {"var snapshot [retainedPublicationLimitV2]journalv2.AcknowledgedPublicationV2"},
		"retained_transport.go": {
			"node := &retainedConnectionNode{", "payload := make([]byte, int(n))",
			"decoded := make([]byte, base64.StdEncoding.DecodedLen(count))",
			"var chunk [retainedDecodeBatch]byte", "base64.StdEncoding.EncodeToString(decoded[written:written+n]) != string(part)",
			"gapdbapi.NewPutMutation(record.Key, record.Value,", "return decoded[:written]",
		},
		"adapter.go": {"Value: append([]byte{}, record.Value...)"},
		filepath.Join("..", "..", "controljournal", "v2", "commit_plan.go"): {
			"result := append([]SealedRecordV2(nil), p.records...)",
			"result[index].canonical = append([]byte(nil), result[index].canonical...)",
			"func (r SealedRecordV2) CanonicalValue() []byte", ".canonical...) }",
		},
		filepath.Join("..", "..", "productionruntime", "retained_private_records.go"): {
			"retainedPrivateMaxTotalBytes = 256 << 10", "value: bytes.Clone(record.Value)",
		},
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, anchor := range anchors {
			if !bytes.Contains(raw, []byte(anchor)) {
				t.Fatalf("recipe source drift %s: %q", path, anchor)
			}
		}
		t.Logf("source %s sha256=%x", path, sha256.Sum256(raw))
	}
	base64Source := filepath.Join(runtime.GOROOT(), "src", "encoding", "base64", "base64.go")
	raw, err := os.ReadFile(base64Source)
	if err != nil || !bytes.Contains(raw, []byte("buf := make([]byte, enc.EncodedLen(len(src)))")) || !bytes.Contains(raw, []byte("return string(buf)")) {
		t.Fatal("EncodeToString delegated-owned recipe needs re-audit", err)
	}
	t.Logf("delegated source %s sha256=%x", base64Source, sha256.Sum256(raw))
	// Resolve the actual module source without importing provider types or
	// relying on a particular user's GOPATH/module-cache layout.
	command := exec.Command("go", "list", "-m", "-json", "github.com/spec-kitty/gapdb")
	encoded, err := command.Output()
	if err != nil {
		t.Fatal("cannot resolve audited SDK source", err)
	}
	var module struct {
		Dir, Version string
		Replace      json.RawMessage
	}
	if err := json.Unmarshal(encoded, &module); err != nil {
		t.Fatal(err)
	}
	if module.Version != "v0.0.0-20260907114832-006e9d469d40" || len(module.Replace) != 0 || module.Dir == "" {
		t.Fatal("SDK source pin/replacement drift: re-audit validation clone")
	}
	sdkSource := filepath.Join(module.Dir, "gapdb", "types.go")
	raw, err = os.ReadFile(sdkSource)
	if err != nil {
		t.Fatal(err)
	}
	for _, anchor := range []string{
		"func NewPutMutation(key string, value []byte, condition Condition, expiresAt *time.Time) Mutation",
		"Value: cloneOpaqueBytes(value)", "func cloneOpaqueBytes(value []byte) []byte {\n\treturn append([]byte{}, value...)\n}",
	} {
		if !bytes.Contains(raw, []byte(anchor)) {
			t.Fatalf("SDK clone source drift: %q", anchor)
		}
	}
	t.Logf("delegated SDK source %s sha256=%x", sdkSource, sha256.Sum256(raw))
}

// Minimal operation publication fixture usable by benchmarks without creating a
// fake testing.T. It is sealed, coordinator-published, and projection-reduced;
// the existing test port is NOT a native provider receipt or I/O benchmark.
func retainedAccountingBenchmarkACK(tb testing.TB) journalv2.AcknowledgedPublicationV2 {
	tb.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	var registrations []journalv2.ReducerRegistration
	for _, tuple := range journalv2.ExpectedEventTuples() {
		reduce := journalv2.Reducer(func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		if tuple.Family == "operation" {
			reduce = journalv2.ReduceOperationStateV2
		}
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion,
			ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: v2TestDigest(tuple.Family), Reduce: reduce})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		tb.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		tb.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(&retainedFixturePortV2{revision: 7}, tracker)
	if err != nil {
		tb.Fatal(err)
	}
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: "accounting-bench-op", LogicalVersion: 1,
		Status: journalv2.StatusAccepted, EventIDs: []journalv2.EventID{"accounting-bench-event"}})
	if err != nil {
		tb.Fatal(err)
	}
	payload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
	if err != nil {
		tb.Fatal(err)
	}
	coordinate, err := journalv2.NewAggregateCoordinate("operation", "accounting-bench-op", journalv2.Scope{Kind: journalv2.ScopeHost})
	if err != nil {
		tb.Fatal(err)
	}
	digest, err := journalv2.ReducerStateDigestV2(payload)
	if err != nil {
		tb.Fatal(err)
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: state.OperationID(), CommandKind: "accounting.benchmark",
		CommandSchemaVersion: 1, CanonicalIntentDigest: v2TestDigest("accounting-benchmark"), Scope: journalv2.Scope{Kind: journalv2.ScopeHost},
		ActorID: "accounting-actor", ProcessID: "accounting-process", OriginatingRuntimeEpochID: "accounting-epoch", ObservedCreatedAt: "2026-09-07T00:00:00Z"})
	if err != nil {
		tb.Fatal(err)
	}
	head := journalv2.HeadTransitionV2{Coordinate: coordinate, NextLogicalVersion: 1, EventID: "accounting-bench-event",
		CommittedRuntimeEpochID: "accounting-epoch", CanonicalStateDigest: digest, AuthoritySummary: payload}
	condition, err := journalv2.ConditionForHeadTransitionV2(head)
	if err != nil {
		tb.Fatal(err)
	}
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: state,
		Events: []journalv2.EventDraftV2{{EventID: head.EventID, EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2,
			ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "accounting-epoch", Payload: payload}},
		Heads: []journalv2.HeadTransitionV2{head}, Conditions: []journalv2.ProviderCondition{condition}}, registry)
	if err != nil {
		tb.Fatal(err)
	}
	ack, err := coordinator.Publish(context.Background(), plan)
	if err != nil {
		tb.Fatal(err)
	}
	return ack
}

func TestRetainedResourceAccountingBenchmarkFixture(t *testing.T) {
	ack := retainedAccountingBenchmarkACK(t)
	rows, err := buildRetainedPublicationMapV2(t.Context(), []journalv2.AcknowledgedPublicationV2{ack})
	if err != nil || len(rows) != ack.MutationCount() {
		t.Fatal("benchmark fixture not genuinely buildable", err)
	}
	t.Logf("benchmark fixture genuine records=%d publication=%s", len(rows), ack.PublicationID())
}

var retainedAccountingBenchmarkSink retainedPublicationMapV2

func BenchmarkRetainedResourceAccountingGenuineACKBuilder(b *testing.B) {
	b.StopTimer()
	ack := retainedAccountingBenchmarkACK(b)
	acks := []journalv2.AcknowledgedPublicationV2{ack}
	_, budget, err := preflightRetainedPublicationsV2(context.Background(), acks)
	if err != nil {
		b.Fatal(err)
	}
	ledger, ok := retainedAccountResources(retainedAccountingInput{acks: 1, records: budget.records, values: budget.values, imageBytes: 1})
	if !ok {
		b.Fatal("fixture outside scalar envelope")
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		rows, err := buildRetainedPublicationMapV2(context.Background(), acks)
		if err != nil {
			b.Fatal(err)
		}
		retainedAccountingBenchmarkSink = rows
	}
	b.StopTimer()
	b.ReportMetric(float64(ledger.builderCoveredSourceLogical), "covered-source-logical-B/op")
	// Go's B/op remains empirical cumulative allocation, not a proof or peak.
}
