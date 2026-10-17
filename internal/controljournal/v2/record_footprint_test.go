package v2

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This is the existing synthetic sealed fixture, not a native provider witness.
func footprintFixtureInput(t *testing.T) CommitPlanInput {
	t.Helper()
	input := fixturePlanInput(t)
	readHead, err := NewAggregateCoordinate("execution_slot", "slot-read", fixtureScope())
	if err != nil {
		t.Fatal(err)
	}
	observedDigest, err := ReducerStateDigestV2([]byte(`{"slot":"current"}`))
	if err != nil {
		t.Fatal(err)
	}
	present, err := NewAuthorityAssertionV2(AuthorityAssertionInputV2{
		Target: ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: readHead}, Kind: ConditionRecordRevision,
		ObservedRevision: 41, ExpectedLogicalVersion: 7, ObservedValueDigest: observedDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	absent, err := NewAuthorityAssertionV2(AuthorityAssertionInputV2{
		Target: ConditionTarget{Kind: ConditionTargetEligibilityGuard, MissionID: "mission", ShardID: "guard-read"}, Kind: ConditionAbsent,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := NewWorkspacePathClaimV2(digest("workspace.path.claim", []byte("/managed/workspace")), []byte(`{"workspace_id":"workspace-01"}`))
	if err != nil {
		t.Fatal(err)
	}
	input.Assertions = []AuthorityAssertionV2{present, absent}
	input.WorkspacePathClaim = &claim
	return input
}

func sealFootprintFixture(t *testing.T, input CommitPlanInput) SealedCommitPlanV2 {
	t.Helper()
	plan, err := SealCommitPlanV2(input, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// Independent owned-copy length/count oracle, used OUTSIDE measurement. It does
// not validate ACKs, contents, targets, digests, or semantic membership.
func copiedFootprintLengths(plan SealedCommitPlanV2) SealedRecordFootprintV2 {
	records := plan.Records()
	result := SealedRecordFootprintV2{
		RecordCount: uint64(len(records)), AssertionCount: uint64(len(plan.Assertions())),
		CanonicalPlanBytes: uint64(len(plan.CanonicalBytes())),
	}
	for _, record := range records {
		size := uint64(len(record.CanonicalValue()))
		result.RecordValueBytes += size
		if size > result.LargestRecordValueBytes {
			result.LargestRecordValueBytes = size
		}
		if record.Role() == RecordCheckpointRecord {
			result.CheckpointRecordCount++
		}
	}
	result.RecordKeyBytesUpperBound = result.RecordCount * uint64(MaxProductKeyBytes)
	result.ProductRawBytesUpperBound = result.RecordKeyBytesUpperBound + result.RecordValueBytes + result.AssertionCount*(uint64(MaxProductKeyBytes)+20)
	return result
}

func TestRecordFootprintRealSealedFixtureAllRolesAndScalarCopies(t *testing.T) {
	plan := sealFootprintFixture(t, footprintFixtureInput(t))
	want := copiedFootprintLengths(plan)
	if want.RecordCount != 11 || want.AssertionCount != 2 || want.CheckpointRecordCount != 1 {
		t.Fatalf("fixture counts: %+v", want)
	}
	roles := map[SealedRecordRole]int{}
	for _, record := range plan.Records() {
		roles[record.Role()]++
	}
	for _, role := range footprintRoles() {
		if roles[role] == 0 {
			t.Fatalf("fixture missing role %s", role)
		}
	}
	if len(roles) != 10 {
		t.Fatalf("unexpected fixture roles: %v", roles)
	}
	raw, err := validateSealedPlanProductBudget(plan.Records(), plan.Assertions())
	if err != nil || raw != want.ProductRawBytesUpperBound {
		t.Fatalf("existing raw helper charge=%d want=%d err=%v", raw, want.ProductRawBytesUpperBound, err)
	}
	for i := 0; i < 10; i++ {
		got, err := plan.MeasureRecordFootprintV2(context.Background())
		if err != nil || got != want {
			t.Fatalf("measurement=%+v want=%+v err=%v", got, want, err)
		}
		// Mutate every returned scalar; no returned alias or capability exists.
		got.RecordCount++
		got.CheckpointRecordCount++
		got.AssertionCount++
		got.RecordValueBytes++
		got.LargestRecordValueBytes++
		got.CanonicalPlanBytes++
		got.RecordKeyBytesUpperBound++
		got.ProductRawBytesUpperBound++
		if got == want {
			t.Fatal("scalar mutation did not take effect on local copy")
		}
	}
	input := footprintFixtureInput(t)
	input.Assertions = nil
	without := sealFootprintFixture(t, input)
	noAssertions, err := without.MeasureRecordFootprintV2(context.Background())
	if err != nil || noAssertions.RecordCount != want.RecordCount || noAssertions.RecordValueBytes != want.RecordValueBytes ||
		noAssertions.RecordKeyBytesUpperBound != want.RecordKeyBytesUpperBound || noAssertions.AssertionCount != 0 ||
		want.ProductRawBytesUpperBound-noAssertions.ProductRawBytesUpperBound != 2*(MaxProductKeyBytes+20) {
		t.Fatalf("assertions became records or lost conservative charge: %+v err=%v", noAssertions, err)
	}
	shape := reflect.TypeOf(SealedRecordFootprintV2{})
	fields := []string{"RecordCount", "CheckpointRecordCount", "AssertionCount", "RecordValueBytes", "LargestRecordValueBytes", "CanonicalPlanBytes", "RecordKeyBytesUpperBound", "ProductRawBytesUpperBound"}
	if shape.NumField() != len(fields) {
		t.Fatalf("non-scalar result shape: %v", shape)
	}
	for i, name := range fields {
		field := shape.Field(i)
		if field.Name != name || field.Type.Kind() != reflect.Uint64 || !field.IsExported() {
			t.Fatalf("result field drift: %v", field)
		}
	}
}

func TestRecordFootprintSuccessfulMeasurementZeroHeapAllocations(t *testing.T) {
	for _, name := range []string{"all_roles_fixture", "valid_large_checkpoint"} {
		t.Run(name, func(t *testing.T) {
			input := footprintFixtureInput(t)
			if name == "valid_large_checkpoint" {
				input.Checkpoints[0].Canonical = []byte(`{"x":"` + strings.Repeat("x", MaxCheckpointValueBytes-len(`{"x":""}`)) + `"}`)
			}
			plan := sealFootprintFixture(t, input)
			want := copiedFootprintLengths(plan)
			if name == "valid_large_checkpoint" && want.LargestRecordValueBytes != MaxCheckpointValueBytes {
				t.Fatal("large checkpoint did not reach the existing value ceiling")
			}
			ctx := context.Background()
			allocs := testing.AllocsPerRun(100, func() {
				got, err := plan.MeasureRecordFootprintV2(ctx)
				if err != nil || got != want {
					t.Fatalf("measurement=%+v want=%+v err=%v", got, want, err)
				}
			})
			if allocs != 0 {
				t.Fatalf("successful measurement allocated %g heap objects", allocs)
			}
		})
	}
}

func footprintRoles() []SealedRecordRole {
	return []SealedRecordRole{RecordOperationBinding, RecordJournalEvent, RecordOperationHead, RecordAggregateHead,
		RecordEffectIntent, RecordEligibilityGuard, RecordCheckpointRecord, RecordCheckpointSlot, RecordWorkspacePathClaim, RecordPublicationWitness}
}

func requireFootprintRefusal(t *testing.T, plan SealedCommitPlanV2, ctx context.Context, kind ErrorKind) {
	t.Helper()
	got, err := plan.MeasureRecordFootprintV2(ctx)
	if got != (SealedRecordFootprintV2{}) || !IsKind(err, kind) {
		t.Fatalf("refusal returned footprint=%+v err=%v; want zero/%s", got, err, kind)
	}
}

func TestRecordFootprintZeroUnsealedAndNilContextRefuse(t *testing.T) {
	base := sealFootprintFixture(t, footprintFixtureInput(t))
	requireFootprintRefusal(t, SealedCommitPlanV2{}, context.Background(), ErrorInvalidDigest)
	requireFootprintRefusal(t, base, nil, ErrorInvalidCanonicalData)
	for name, mutate := range map[string]func(*SealedCommitPlanV2){
		"missing_publication":   func(p *SealedCommitPlanV2) { p.publicationID = "" },
		"malformed_publication": func(p *SealedCommitPlanV2) { p.publicationID = PublicationID(strings.Repeat("s", 64)) },
		"short_publication":     func(p *SealedCommitPlanV2) { p.publicationID = "secret" },
		"missing_plan_digest":   func(p *SealedCommitPlanV2) { p.digest = Digest{} },
		"missing_record_digest": func(p *SealedCommitPlanV2) { p.recordSetDigest = Digest{} },
		"missing_canonical":     func(p *SealedCommitPlanV2) { p.canonical = nil },
		"missing_records":       func(p *SealedCommitPlanV2) { p.records = nil },
	} {
		t.Run(name, func(t *testing.T) {
			p := base
			mutate(&p)
			requireFootprintRefusal(t, p, context.Background(), ErrorInvalidDigest)
			_, err := p.MeasureRecordFootprintV2(context.Background())
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), strings.Repeat("s", 64)) {
				t.Fatal("content-bearing refusal")
			}
		})
	}
}

// Deterministic real cancellation at the nth Err observation exercises loop
// cancellation without goroutine timing or a production callback/hook.
type footprintCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	at, calls int
}

func (c *footprintCancelContext) Err() error {
	c.calls++
	if c.calls == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestRecordFootprintContextCancellationZeroResult(t *testing.T) {
	plan := sealFootprintFixture(t, footprintFixtureInput(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := plan.MeasureRecordFootprintV2(ctx)
	if got != (SealedRecordFootprintV2{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled: %+v %v", got, err)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	got, err = plan.MeasureRecordFootprintV2(deadline)
	if got != (SealedRecordFootprintV2{}) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %+v %v", got, err)
	}
	// Every check, including each assertion and the final pre-success check.
	for at := 1; at <= 2+len(plan.records)+len(plan.assertions); at++ {
		t.Run(fmt.Sprintf("sealed_fixture_check_%d", at), func(t *testing.T) {
			inner, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &footprintCancelContext{Context: inner, cancel: cancel, at: at}
			got, err := plan.MeasureRecordFootprintV2(ctx)
			if got != (SealedRecordFootprintV2{}) || !errors.Is(err, context.Canceled) || ctx.calls != at {
				t.Fatalf("mid-loop cancellation: %+v %v calls=%d want=%d", got, err, ctx.calls, at)
			}
		})
	}
}

// All structural probes below test only the getter's size/shape laws. Their
// package-local replacements are NOT manufactured valid seals or ACK proofs.
func footprintStructuralProbe(base SealedCommitPlanV2, records, assertions int) SealedCommitPlanV2 {
	p := base
	p.records = make([]SealedRecordV2, records)
	for i := range p.records {
		p.records[i] = SealedRecordV2{role: RecordOperationHead, valueClass: RecordValueOrdinary, canonical: []byte{'x'}}
	}
	p.assertions = make([]AuthorityAssertionV2, assertions)
	return p
}

func TestRecordFootprintStructuralRoleClassAndEmptyValueRefusals(t *testing.T) {
	base := sealFootprintFixture(t, footprintFixtureInput(t))
	for _, role := range append(footprintRoles(), SealedRecordRole("secret_unknown_role"), SealedRecordRole("")) {
		for _, class := range []SealedRecordValueClass{RecordValueOrdinary, RecordValueCheckpoint, "unknown_class", ""} {
			t.Run(string(role)+"/"+string(class), func(t *testing.T) {
				p := footprintStructuralProbe(base, 1, 0)
				p.records[0].role, p.records[0].valueClass = role, class
				validRole := role != "secret_unknown_role" && role != ""
				validClass := role == RecordCheckpointRecord && class == RecordValueCheckpoint || role != RecordCheckpointRecord && class == RecordValueOrdinary
				if !validRole || !validClass {
					requireFootprintRefusal(t, p, context.Background(), ErrorInvalidCanonicalData)
					return
				}
				got, err := p.MeasureRecordFootprintV2(context.Background())
				if err != nil || got.RecordCount != 1 || got.RecordValueBytes != 1 || (got.CheckpointRecordCount == 1) != (role == RecordCheckpointRecord) {
					t.Fatalf("structural allowed role/class: %+v %v", got, err)
				}
				p.records[0].canonical = nil
				requireFootprintRefusal(t, p, context.Background(), ErrorInvalidCanonicalData)
			})
		}
	}
}

func TestRecordFootprintStructuralCountsAndCanonicalBoundaries(t *testing.T) {
	base := sealFootprintFixture(t, footprintFixtureInput(t))
	for _, tc := range []struct{ records, assertions int }{{MaxMembers, 0}, {1, MaxMembers - 1}, {MaxMembers / 2, MaxMembers / 2}} {
		t.Run(fmt.Sprintf("exact_%d_records_%d_assertions", tc.records, tc.assertions), func(t *testing.T) {
			p := footprintStructuralProbe(base, tc.records, tc.assertions)
			got, err := p.MeasureRecordFootprintV2(context.Background())
			if err != nil || got.RecordCount != uint64(tc.records) || got.AssertionCount != uint64(tc.assertions) {
				t.Fatalf("exact bounded operations: %+v %v", got, err)
			}
			// Record, assertion, and final-check cancellation at the maximum
			// operation count; zero partial result even after bounded work.
			for at := 2; at <= 2+tc.records+tc.assertions; at++ {
				inner, cancel := context.WithCancel(context.Background())
				ctx := &footprintCancelContext{Context: inner, cancel: cancel, at: at}
				got, err := p.MeasureRecordFootprintV2(ctx)
				cancel()
				if got != (SealedRecordFootprintV2{}) || !errors.Is(err, context.Canceled) || ctx.calls != at {
					t.Fatalf("bounded-loop cancel at %d: %+v %v calls=%d", at, got, err, ctx.calls)
				}
			}
		})
	}
	for _, tc := range []struct{ records, assertions int }{{MaxMembers + 1, 0}, {0, MaxConditionsPerPlan + 1}, {1, MaxConditionsPerPlan}, {MaxMembers, 1}} {
		t.Run(fmt.Sprintf("over_%d_records_%d_assertions_before_loop", tc.records, tc.assertions), func(t *testing.T) {
			p := footprintStructuralProbe(base, tc.records, tc.assertions)
			inner, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &footprintCancelContext{Context: inner, cancel: cancel, at: 2}
			requireFootprintRefusal(t, p, ctx, ErrorBudgetExceeded)
			if ctx.calls != 1 {
				t.Fatalf("count overflow entered loop: checks=%d", ctx.calls)
			}
		})
	}
	p := footprintStructuralProbe(base, 1, 0)
	p.canonical = make([]byte, MaxCommitPlanBytes+1)
	inner, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &footprintCancelContext{Context: inner, cancel: cancel, at: 2}
	requireFootprintRefusal(t, p, ctx, ErrorBudgetExceeded)
	if ctx.calls != 1 {
		t.Fatal("canonical overflow entered loop")
	}
	p.canonical = p.canonical[:MaxCommitPlanBytes]
	got, err := p.MeasureRecordFootprintV2(context.Background())
	if err != nil || got.CanonicalPlanBytes != MaxCommitPlanBytes {
		t.Fatalf("structural exact canonical bytes: %+v %v", got, err)
	}
}

func TestRecordFootprintStructuralValueBoundaries(t *testing.T) {
	base := sealFootprintFixture(t, footprintFixtureInput(t))
	for _, tc := range []struct {
		role    SealedRecordRole
		class   SealedRecordValueClass
		maximum int
	}{
		{RecordOperationHead, RecordValueOrdinary, MaxCanonicalPayloadBytes},
		{RecordCheckpointRecord, RecordValueCheckpoint, MaxCheckpointValueBytes},
	} {
		t.Run(string(tc.class), func(t *testing.T) {
			p := footprintStructuralProbe(base, 1, 0)
			p.records[0].role, p.records[0].valueClass = tc.role, tc.class
			p.records[0].canonical = make([]byte, tc.maximum+1)
			requireFootprintRefusal(t, p, context.Background(), ErrorBudgetExceeded)
			p.records[0].canonical = p.records[0].canonical[:tc.maximum]
			got, err := p.MeasureRecordFootprintV2(context.Background())
			if err != nil || got.RecordValueBytes != uint64(tc.maximum) || got.LargestRecordValueBytes != uint64(tc.maximum) ||
				got.ProductRawBytesUpperBound != uint64(tc.maximum+MaxProductKeyBytes) {
				t.Fatalf("exact structural value: %+v %v", got, err)
			}
		})
	}
}

func TestRecordFootprintStructuralRawBoundariesIncludingAssertionCharges(t *testing.T) {
	base := sealFootprintFixture(t, footprintFixtureInput(t))
	for _, assertions := range []int{0, 2} {
		t.Run(fmt.Sprintf("assertions_%d", assertions), func(t *testing.T) {
			p := footprintStructuralProbe(base, 9, assertions)
			p.records[0].role, p.records[0].valueClass = RecordCheckpointRecord, RecordValueCheckpoint
			p.records[0].canonical = make([]byte, MaxCheckpointValueBytes)
			remaining := MaxProductRawBatchBytes - MaxCheckpointValueBytes - len(p.records)*MaxProductKeyBytes - assertions*(MaxProductKeyBytes+20)
			for i := 1; i < len(p.records); i++ {
				size := remaining / 8
				if i <= remaining%8 {
					size++
				}
				p.records[i].canonical = make([]byte, size)
			}
			got, err := p.MeasureRecordFootprintV2(context.Background())
			raw, rawErr := validateSealedPlanProductBudget(p.records, p.assertions)
			if err != nil || rawErr != nil || raw != MaxProductRawBatchBytes || got.ProductRawBytesUpperBound != uint64(raw) ||
				got.RecordValueBytes != uint64(MaxProductRawBatchBytes-9*MaxProductKeyBytes-assertions*(MaxProductKeyBytes+20)) {
				t.Fatalf("exact structural raw batch: %+v %v raw=%d rawErr=%v", got, err, raw, rawErr)
			}
			over := p
			over.records = append([]SealedRecordV2(nil), p.records...)
			over.records[8].canonical = make([]byte, len(over.records[8].canonical)+1)
			requireFootprintRefusal(t, over, context.Background(), ErrorBudgetExceeded)
			// Overflow reached during assertion charging after all records fit.
			over = p
			over.assertions = make([]AuthorityAssertionV2, assertions+1)
			requireFootprintRefusal(t, over, context.Background(), ErrorBudgetExceeded)
		})
	}
}
