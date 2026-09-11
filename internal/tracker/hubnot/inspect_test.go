package hubnot

import (
	"bytes"
	"context"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"testing"
)

func TestInspectExistingBindingReadOnlyAndActorDrift(t *testing.T) {
	b, refs := operationFixture(t)
	before := refs()
	got, err := b.Inspect(context.Background(), tracker.InspectRequest{ProjectID: "project"})
	if err != nil || got.Health != tracker.HealthHealthy || got.Binding.Provider != "hubnot" || !bytes.Equal(before, refs()) {
		t.Fatalf("inspect: %+v %v", got, err)
	}
	config := b.config
	config.ReadOnly = true
	readOnly, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readOnly.Inspect(context.Background(), tracker.InspectRequest{ProjectID: "project"}); err != nil {
		t.Fatal(err)
	}
	if _, err = readOnly.CreateWork(context.Background(), simpleRequest()); err == nil || !bytes.Equal(before, refs()) {
		t.Fatalf("read-only create wrote: %v", err)
	}
	b.config.ExpectedActor = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err = b.Inspect(context.Background(), tracker.InspectRequest{ProjectID: "project"}); err == nil || !bytes.Equal(before, refs()) {
		t.Fatalf("inspection ignored actor mismatch: %v", err)
	}
}
