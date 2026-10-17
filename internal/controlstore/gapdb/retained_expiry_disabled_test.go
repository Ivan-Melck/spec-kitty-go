//go:build linux && amd64 && !spec_kitty_expiry_host_qualification

package gapdb

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
)

// Captured against the ordinary pre-slice source. This is layout, NOT heap
// escape/allocation/memory acceptance, and does not apply to tagged state.
func TestRetainedObservationOrdinaryLayoutAndMethods(t *testing.T) {
	if got := unsafe.Sizeof(retainedObservationState{}); got != 136 {
		t.Fatalf("ordinary state size=%d, pre-slice=136", got)
	}
	typ := reflect.TypeOf(retainedObservationState{})
	for name, offset := range map[string]uintptr{
		"mu": 0, "invalid": 8, "phase": 12, "countMismatch": 13,
		"adapter": 16, "provider": 24, "transport": 32, "lifetime": 40,
		"captured": 48, "expected": 80, "ctx": 104, "cancel": 120, "budget": 128,
	} {
		field, ok := typ.FieldByName(name)
		if !ok || field.Offset != offset {
			t.Fatalf("ordinary %s offset=%d present=%t, pre-slice=%d", name, field.Offset, ok, offset)
		}
	}
	if _, ok := reflect.TypeOf(&retainedkernel.Lifetime{}).MethodByName("CheckExpiryQualificationLive"); ok {
		t.Fatal("tag-only kernel method is present in ordinary build")
	}
	for _, name := range []string{"ArmExpiryQualificationRefusal", "ConsumeExpiryQualificationRefusal"} {
		if _, ok := reflect.TypeOf(&RetainedObservationV2{}).MethodByName(name); ok {
			t.Fatalf("tag-only method %s is present in ordinary build", name)
		}
	}
}
