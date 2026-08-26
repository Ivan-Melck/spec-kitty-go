package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifyBinaryPinsPathDigestVersionAndSchema(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	binary := filepath.Join(t.TempDir(), "bd")
	payload := "#!/bin/sh\nprintf '%s\\n' '{\"schema_version\":1,\"data\":{\"version\":\"1.2.2\",\"build\":\"fixture\"}}'\n"
	if err := os.WriteFile(binary, []byte(payload), 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(payload))
	identity, err := verifyBinary(context.Background(), binary, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if identity.Path != binary || identity.Version != qualifiedVersion || identity.Schema != qualifiedSchema || identity.Build != "fixture" {
		t.Fatalf("unexpected identity: %+v", identity)
	}

	wrong := sha256.Sum256([]byte("wrong"))
	if _, err := verifyBinary(context.Background(), binary, hex.EncodeToString(wrong[:])); err == nil {
		t.Fatal("wrong digest was accepted")
	}
	if _, err := verifyBinary(context.Background(), "bd", identity.Digest); err == nil {
		t.Fatal("relative binary path was accepted")
	}
}

func TestFixturePathsAreIndependentGuardedAndTargeted(t *testing.T) {
	identity := binaryIdentity{Path: "/qualified/bd", Version: qualifiedVersion, Schema: qualifiedSchema}
	left, err := newFixture(identity, false)
	if err != nil {
		t.Fatal(err)
	}
	right, err := newFixture(identity, false)
	if err != nil {
		_ = left.cleanup()
		t.Fatal(err)
	}
	if left.Root == right.Root || left.Workspace == right.Workspace {
		t.Fatal("parallel fixtures shared mutable state")
	}
	outside := filepath.Join(filepath.Dir(left.Root), "outside")
	if err := left.validateChild(outside, true); err == nil {
		t.Fatal("path escape was accepted")
	}
	if err := os.WriteFile(filepath.Join(left.Restore, "user-data"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := left.validateEmptyRestoreTarget(); err == nil {
		t.Fatal("non-empty restore target was accepted")
	}
	if err := left.cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left.Root); !os.IsNotExist(err) {
		t.Fatalf("fixture root remains after cleanup: %v", err)
	}
	if _, err := os.Stat(right.Root); err != nil {
		t.Fatalf("left cleanup affected right fixture: %v", err)
	}
	if err := right.cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestFixtureRefusesSymlinkRestoreAndMissingOwnershipMarker(t *testing.T) {
	fixture, err := newFixture(binaryIdentity{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.Restore); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), fixture.Restore); err != nil {
		t.Fatal(err)
	}
	if err := fixture.validateEmptyRestoreTarget(); err == nil {
		t.Fatal("symlink restore target was accepted")
	}
	if err := os.Remove(filepath.Join(fixture.Root, fixtureMarker)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.cleanup(); err == nil {
		t.Fatal("cleanup without ownership marker was accepted")
	}
	// This is test-owned cleanup after the production guard has refused.
	if err := os.RemoveAll(fixture.Root); err != nil {
		t.Fatal(err)
	}
}

func TestQualificationEnvironmentIsMinimalAndExplicit(t *testing.T) {
	environment := qualificationEnv("/tmp/fixture", "/tmp/fixture/workspace")
	for _, forbidden := range []string{"AWS_SECRET_ACCESS_KEY=", "GITHUB_TOKEN=", "ANTHROPIC_API_KEY="} {
		for _, item := range environment {
			if len(item) >= len(forbidden) && item[:len(forbidden)] == forbidden {
				t.Fatalf("credential leaked into environment: %s", forbidden)
			}
		}
	}
	want := map[string]bool{"BD_JSON_ENVELOPE=1": false, "BD_NON_INTERACTIVE=1": false, "BEADS_DIR=/tmp/fixture/workspace": false}
	for _, item := range environment {
		if _, ok := want[item]; ok {
			want[item] = true
		}
	}
	for item, present := range want {
		if !present {
			t.Errorf("missing environment item %q", item)
		}
	}
}
