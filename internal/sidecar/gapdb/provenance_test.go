package gapdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

var sidecarBinary = flag.String("gapdbd", "", "absolute path to the exact admitted gapdbd")

func TestProvenanceAdmittedImmutableExecutablePasses(t *testing.T) {
	binary := safeAdmittedCopy(t)
	verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: binary, AdmissionRecordPath: admissionRecordPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	provenance := verified.Provenance()
	if provenance.SHA256 != "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e" ||
		provenance.AdmissionRecordSHA256 != "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d" ||
		provenance.Size != admittedExecutableSize || provenance.Device == 0 || provenance.Inode == 0 ||
		provenance.ContractRevision != contractRevision {
		t.Fatalf("incomplete provenance: %+v", provenance)
	}
}

func TestProvenanceSymlinkDigestModeAncestorAndHardlinkMutantsFail(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		binary := safeAdmittedCopy(t)
		link := filepath.Join(t.TempDir(), "gapdbd")
		if err := os.Symlink(binary, link); err != nil {
			t.Fatal(err)
		}
		_, err := VerifyExecutable(VerifyConfig{ExecutablePath: link, AdmissionRecordPath: admissionRecordPath(t)})
		if !IsFailure(err, FailureUnsafePath) {
			t.Fatalf("symlink err=%v", err)
		}
	})
	t.Run("digest", func(t *testing.T) {
		binary := safeAdmittedCopy(t)
		original := make([]byte, 1)
		reader, err := os.Open(binary)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ReadAt(original, 1024); err != nil {
			t.Fatal(err)
		}
		_ = reader.Close()
		file, err := os.OpenFile(binary, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteAt([]byte{original[0] ^ 0xff}, 1024); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		_, err = VerifyExecutable(VerifyConfig{ExecutablePath: binary, AdmissionRecordPath: admissionRecordPath(t)})
		if !IsFailure(err, FailureAdmissionMismatch) {
			t.Fatalf("digest err=%v", err)
		}
	})
	t.Run("unsafe-mode", func(t *testing.T) {
		binary := safeAdmittedCopy(t)
		if err := os.Chmod(binary, 0o775); err != nil {
			t.Fatal(err)
		}
		_, err := VerifyExecutable(VerifyConfig{ExecutablePath: binary, AdmissionRecordPath: admissionRecordPath(t)})
		if !IsFailure(err, FailureUnsafePath) {
			t.Fatalf("mode err=%v", err)
		}
	})
	t.Run("writable-ancestor", func(t *testing.T) {
		parent := t.TempDir()
		if err := os.Chmod(parent, 0o777); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(parent, "gapdbd")
		copyFile(t, admittedBinary(t), binary, 0o755)
		_, err := VerifyExecutable(VerifyConfig{ExecutablePath: binary, AdmissionRecordPath: admissionRecordPath(t)})
		if !IsFailure(err, FailureUnsafePath) {
			t.Fatalf("ancestor err=%v", err)
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		binary := safeAdmittedCopy(t)
		link := filepath.Join(filepath.Dir(binary), "gapdbd-hardlink")
		if err := os.Link(binary, link); err != nil {
			t.Fatal(err)
		}
		_, err := VerifyExecutable(VerifyConfig{ExecutablePath: binary, AdmissionRecordPath: admissionRecordPath(t)})
		if !IsFailure(err, FailureUnsafePath) {
			t.Fatalf("hardlink err=%v", err)
		}
	})
}

func TestProvenanceChangedDescriptorIdentityFailsBeforeExec(t *testing.T) {
	verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: safeAdmittedCopy(t), AdmissionRecordPath: admissionRecordPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	verified.identity.inode++ // executable mutant: post-verify identity substitution
	_, err = verified.command([]string{"-db", t.TempDir()}, []string{"LANG=C"})
	if !IsFailure(err, FailureIdentityUnprovable) {
		t.Fatalf("changed identity err=%v", err)
	}
}

func TestProvenanceVerifyThenSwapExecutesHeldDescriptor(t *testing.T) {
	binary := safeAdmittedCopy(t)
	verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: binary, AdmissionRecordPath: admissionRecordPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	moved := binary + ".verified"
	if err := os.Rename(binary, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 97\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(t.TempDir(), "database")
	socket := filepath.Join(t.TempDir(), "gapdb.sock")
	command, err := verified.command([]string{"-db", database, "-socket", socket, "-json=true"}, []string{"LANG=C", "TZ=UTC"})
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatalf("held descriptor did not execute: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	client, err := waitPublicClient(t.Context(), socket, 5*time.Second)
	if err != nil {
		_ = command.Process.Kill()
		<-done
		t.Fatal(err)
	}
	status, err := client.Status(t.Context())
	_ = client.Close()
	if err != nil || status.OwnerPID != command.Process.Pid {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("held descriptor process did not stop")
	}
}

func TestProvenanceDiagnosticsAreBoundedAndSecretFree(t *testing.T) {
	secret := strings.Repeat("credential", 1024)
	err := failure(FailureUnsafePath, secret, secret, errors.New(secret))
	if len(err.Error()) > maxDiagnosticBytes || strings.Contains(err.Error(), secret) {
		t.Fatalf("unbounded diagnostic length=%d", len(err.Error()))
	}
}

func TestProvenanceLifecycleAndResponseDropMutantControls(t *testing.T) {
	directory := filepath.Dir(admissionRecordPath(t))
	packageDirectory := filepath.Clean(filepath.Join(directory, "..", "..", "..", "internal", "sidecar", "gapdb"))
	production := make(map[string]string)
	for _, name := range []string{"provenance.go", "lifecycle.go", "proxy.go"} {
		encoded, err := os.ReadFile(filepath.Join(packageDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		production[name] = string(encoded)
	}
	if !strings.Contains(production["provenance.go"], `exec.Command("/proc/self/fd/3"`) {
		t.Fatal("descriptor-bound exec control is absent")
	}
	for _, forbidden := range []string{"Look" + "Path", "Command(config.ExecutablePath", "Combined" + "Output", "internal/" + "server", "controlstore/" + "sqlite", "Remove" + "All("} {
		for name, source := range production {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s contains forbidden mutant surface %q", name, forbidden)
			}
		}
	}
	for _, required := range []string{
		"PidfdOpen", "PidfdSendSignal", "processStartTicks", "peerProcess",
		"result.DurableThroughRevision != result.Revision", "after_durable_acceptance_before_client_response",
		"validateNoDuplicateJSON", "writeWireFrame(daemon, requestBytes)",
	} {
		joined := production["provenance.go"] + production["lifecycle.go"] + production["proxy.go"]
		if !strings.Contains(joined, required) {
			t.Fatalf("mutant control %q is absent", required)
		}
	}
}

func safeAdmittedCopy(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "gapdbd-62219cf")
	copyFile(t, admittedBinary(t), path, 0o755)
	return path
}

func admittedBinary(t *testing.T) string {
	t.Helper()
	path := *sidecarBinary
	if path == "" {
		path = os.Getenv("SPEC_KITTY_M1_GAPDBD")
	}
	if path == "" {
		t.Skip("exact admitted gapdbd not supplied with -gapdbd or SPEC_KITTY_M1_GAPDBD")
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("admitted binary must be absolute: %q", path)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	if int64(len(encoded)) != admittedExecutableSize || hex.EncodeToString(digest[:]) != "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e" {
		t.Fatal("supplied binary is not the admitted artifact")
	}
	return path
}

func admissionRecordPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "docs", "programs", "evidence", "m1-admission.json"))
}

func copyFile(t *testing.T, source, target string, mode os.FileMode) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, mode); err != nil {
		t.Fatal(err)
	}
}

func waitPublicClient(ctx context.Context, socket string, timeout time.Duration) (*gapdbapi.Client, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		client, err := gapdbapi.Dial(socket, gapdbapi.ClientOptions{Timeout: 100 * time.Millisecond})
		if err == nil {
			if _, err = client.Status(ctx); err == nil {
				return client, nil
			}
			_ = client.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, errors.New("public endpoint did not become ready")
}
