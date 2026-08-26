package main

import (
	"context"
	"testing"
	"time"
)

func TestQualificationFailsBeforeFixtureForMissingOrUnverifiedBinary(t *testing.T) {
	result, err := runQualification(context.Background(), options{
		BinaryPath: "bd", Digest: "not-a-digest", Evidence: "/tmp/not-used.json",
		Repository: "/tmp", Timeout: time.Minute,
	})
	if err == nil || result.Verdict != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestQualificationTimeoutIsBounded(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second, 31 * time.Minute} {
		if _, err := runQualification(context.Background(), options{Timeout: timeout}); err == nil {
			t.Fatalf("timeout %s was accepted", timeout)
		}
	}
}
