package productionruntime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

type retainedCustomErrorContext struct {
	context.Context
	err error
}

func (c retainedCustomErrorContext) Err() error { return c.err }

func TestRetainedRecordsProofPublicZeroSanitizedRefusals(t *testing.T) {
	h, f := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeHost})
	// Actual coordinator ACKs do NOT grant an ordinary fixture owner authority.
	for _, host := range []*Host{nil, {}, h, {state: HostReady, owner: &productionOwner{adapter: &storegapdb.Adapter{}, lifecycle: &sidecargapdb.Lifecycle{}}}} {
		p, err := host.VerifyRetainedRecords(t.Context(), f.observerACKs)
		if !reflect.ValueOf(p).IsZero() || !IsProgressKind(err, ProgressIncomplete) || err.Error() != proofIncomplete("retained_records").Error() {
			t.Fatal("ordinary/proxy-shaped composition earned partial proof", err)
		}
	}
	for _, fault := range []error{context.Canceled, context.DeadlineExceeded, errors.New("SECRET custom context"), fmt.Errorf("SECRET diagnostic: %w", context.Canceled), fmt.Errorf("SECRET path: %w", context.DeadlineExceeded)} {
		ctx := retainedCustomErrorContext{Context: t.Context(), err: fault}
		p, err := h.VerifyRetainedRecords(ctx, f.observerACKs)
		if !reflect.ValueOf(p).IsZero() || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("custom context diagnostic escaped")
		}
		if errors.Is(fault, context.Canceled) && err != context.Canceled || errors.Is(fault, context.DeadlineExceeded) && err != context.DeadlineExceeded {
			t.Fatal("not a fixed cancellation sentinel")
		}
	}
	if p, err := h.VerifyRetainedRecords(nil, nil); !reflect.ValueOf(p).IsZero() || !IsProgressKind(err, ProgressIncomplete) {
		t.Fatal("nil context accepted")
	}
}

func TestRetainedRecordsProofNormalizeEveryHighError(t *testing.T) {
	for _, inner := range []error{errors.New("SECRET provider/path"), context.Canceled, context.DeadlineExceeded} {
		wrapped := fmt.Errorf("SECRET /provider/path: %w", inner)
		got := retainedRecordsError(t.Context(), wrapped)
		if strings.Contains(got.Error(), "SECRET") || strings.Contains(got.Error(), "/provider") {
			t.Fatal("diagnostic wrapper escaped")
		}
		if inner == context.Canceled && got != context.Canceled || inner == context.DeadlineExceeded && got != context.DeadlineExceeded {
			t.Fatal("wrapped sentinel not normalized")
		}
	}
}

// Structural scalar-close tests only: zero lifecycle/adapter objects are NOT
// attributed and never earn a public positive proof. The projection and its
// changing rolling prefixes are genuine coordinator-minted ACKs.
func retainedStructuralComposition(t *testing.T) (*Host, *retainedHostComposition, *publicationProofFixture) {
	t.Helper()
	h, f := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeHost})
	o := &productionOwner{lifecycle: &sidecargapdb.Lifecycle{}, adapter: &storegapdb.Adapter{}, database: h.databaseID, binding: AuthorityBindingV2{DatabaseID: h.databaseID}}
	c := &retainedHostComposition{host: h, owner: o, lifecycle: o.lifecycle, adapter: o.adapter, projection: h.projection, database: h.databaseID, epoch: h.epoch, binding: o.binding}
	h.owner, h.retained = o, c
	return h, c, f
}

func TestRetainedRecordsProofFrozenCompositionAndAllScalarClose(t *testing.T) {
	for _, name := range []string{"owner", "lifecycle", "adapter", "projection", "database", "owner_database", "epoch", "state", "binding", "composition", "host_copy", "rolling_operation_prefix"} {
		t.Run(name, func(t *testing.T) {
			h, c, f := retainedStructuralComposition(t)
			before, ok := h.retainedCapture(c)
			if !ok {
				t.Fatal("structural capture refused")
			}
			h.mu.Lock()
			switch name {
			case "owner":
				h.owner = &productionOwner{}
			case "lifecycle":
				c.owner.lifecycle = &sidecargapdb.Lifecycle{}
			case "adapter":
				c.owner.adapter = &storegapdb.Adapter{}
			case "projection":
				other, _ := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeHost}, "operation-other")
				h.projection = other.projection
			case "database":
				h.databaseID = "database-other"
			case "owner_database":
				c.owner.database = "database-other"
			case "epoch":
				h.epoch = "epoch-other"
			case "state":
				h.state = HostClosing
			case "binding":
				c.owner.binding.HostAuthorityID = "host-other"
			case "composition":
				copy := *c
				h.retained = &copy
			case "host_copy":
				c.host = &Host{}
			}
			h.mu.Unlock()
			if name == "rolling_operation_prefix" {
				// A legitimate same-root operation-only continuation changes the rolling
				// prefix even though it is not a mission/domain aggregate change.
				_, continued := newPublicationProofFixture(t, true, journalv2.Scope{Kind: journalv2.ScopeHost})
				if _, err := f.coordinator.Publish(t.Context(), continued.observerACKs[1].Plan()); err != nil {
					t.Fatal(err)
				}
				after, ok := h.retainedCapture(c)
				if !ok || after.CursorToken == before.CursorToken || after == before {
					t.Fatal("operation prefix movement invisible")
				}
			} else if _, ok := h.retainedCapture(c); ok {
				t.Fatal("original composition substitution accepted")
			}
			p, err := h.VerifyRetainedRecords(t.Context(), f.observerACKs)
			if !reflect.ValueOf(p).IsZero() || !IsProgressKind(err, ProgressIncomplete) {
				t.Fatal("structural infrastructure awarded high authority")
			}
		})
	}
}

func TestRetainedRecordsProofImmutableMatchedOnlyScalars(t *testing.T) {
	h, c, _ := retainedStructuralComposition(t)
	attribution, ok := h.retainedCapture(c)
	if !ok {
		t.Fatal("capture")
	}
	// Private structural value, not a production-issued positive proof.
	p := RetainedRecordsProof{attribution: attribution, publications: 11, private: 6}
	copy := p.Attribution()
	copy.DatabaseID = "altered"
	if p.Attribution() != attribution || p.MatchedPublicationRecords() != 11 || p.MatchedPrivateRecords() != 6 || p.MatchedRecords() != 17 {
		t.Fatal("matched scalar accessors")
	}
	typ := reflect.TypeOf(p)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.IsExported() || field.Type.Kind() == reflect.Pointer || field.Type.Kind() == reflect.Slice || field.Type.Kind() == reflect.Map {
			t.Fatal("mutable/unknown proof field")
		}
	}
}
