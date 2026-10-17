package gapdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func realStartupObservationConfig(t *testing.T) LifecycleConfig {
	t.Helper()
	binary := os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	if binary == "" {
		t.Skip("set SPEC_KITTY_WP13_GAPDBD to the admitted assertion-extension sidecar")
	}
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(repository, "docs", "programs", "evidence")
	admissionPath := filepath.Join(evidence, "m1-assertion-extension-admission.json")
	admission, err := VerifyAssertionExtensionAdoption(filepath.Join(evidence, "m1-assertion-extension-adoption.json"), admissionPath)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "spk-start-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return LifecycleConfig{
		ExecutablePath: binary, AdmissionRecordPath: admissionPath, Admission: admission,
		RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(root, "authority"),
		SupervisorRunID: "run-startup-observation", ProductPolicySHA256: testPolicyDigest,
		StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	}
}

// Exercise the unchanged Start entry point with real descriptor-bound exec.
// The observer records evidence but does not retry, change admission, or return
// different process facts to production code.
func TestRealAdmittedStartupIdentityObservation(t *testing.T) {
	parentIdentity, parentDigest, err := processExecutable(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 32; attempt++ {
		config := realStartupObservationConfig(t)
		api := systemProcessAPI
		var observations []string
		executable := api.executable
		api.executable = func(pid int) (fileIdentity, string, error) {
			identity, digest, err := executable(pid)
			observations = append(observations, fmt.Sprintf("pid=%d identity=%+v digest=%s err=%v", pid, identity, digest, err))
			return identity, digest, err
		}
		config.processes = api
		lifecycle, err := Start(t.Context(), config)
		if err != nil {
			t.Fatalf("attempt=%d start=%v observations=%v supervisor_identity=%+v supervisor_digest=%s", attempt, err, observations, parentIdentity, parentDigest)
		}
		if err := lifecycle.Stop(t.Context()); err != nil {
			t.Fatalf("attempt=%d stop=%v observations=%v", attempt, err, observations)
		}
	}
}

func TestStartDoesNotMistakeExecTransitionForWrongImage(t *testing.T) {
	config := realStartupObservationConfig(t)
	parentIdentity, parentDigest, err := processExecutable(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	// Hold only the fixture child before it can create its endpoint. The real
	// executable and admission remain unchanged. Return the inherited image
	// during that window, modelling exec_mm_release preceding the mm switch.
	resumed := make(chan error, 1)
	config.commandStarted = func(process *os.Process) {
		if err := process.Signal(syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		go func() {
			timer := time.NewTimer(30 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-t.Context().Done():
			}
			resumed <- process.Signal(syscall.SIGCONT)
		}()
	}
	api := systemProcessAPI
	executable := api.executable
	api.executable = func(pid int) (fileIdentity, string, error) {
		endpoints, err := filepath.Glob(filepath.Join(config.RuntimeRoot, "s-*", "gapdb.sock"))
		if err != nil {
			return fileIdentity{}, "", err
		}
		if len(endpoints) == 0 {
			return parentIdentity, parentDigest, nil
		}
		return executable(pid)
	}
	config.processes = api
	lifecycle, err := Start(t.Context(), config)
	resumeErr := <-resumed
	if err != nil {
		t.Fatalf("inherited image was mistaken for an executed-image substitution: start=%v resume=%v", err, resumeErr)
	}
	defer stopTestLifecycle(t, lifecycle)
	if resumeErr != nil {
		t.Fatal(resumeErr)
	}
	if lifecycle.process.SHA256 != config.Admission.sidecarSHA256 || lifecycle.process.PID == os.Getpid() {
		t.Fatal("transition wait admitted the supervisor instead of the exact child image")
	}
}

func TestStartCancellationBeforeEndpointReapsUnobservedChild(t *testing.T) {
	config := realStartupObservationConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var child *os.Process
	config.commandStarted = func(process *os.Process) {
		child = process
		if err := process.Signal(syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
	api := systemProcessAPI
	api.executable = func(int) (fileIdentity, string, error) {
		return fileIdentity{}, "", errors.New("unready image was observed")
	}
	config.processes = api
	lifecycle, err := Start(ctx, config)
	if lifecycle != nil || !errors.Is(err, context.Canceled) || !IsFailure(err, FailureReadiness) {
		t.Fatalf("startup cancellation became image failure or ownership: lifecycle=%v err=%v", lifecycle, err)
	}
	if child == nil || !errors.Is(child.Signal(syscall.Signal(0)), os.ErrProcessDone) {
		t.Fatal("cancelled unobserved child was not reaped by its owned handle")
	}
}

func TestStartMetadataRefusalClosesPidfdAndReapsUnobservedChild(t *testing.T) {
	config := realStartupObservationConfig(t)
	var child *os.Process
	config.commandStarted = func(process *os.Process) { child = process }
	api := systemProcessAPI
	api.executableIdentity = func(int) (fileIdentity, error) { return fileIdentity{}, errors.New("metadata observation failed") }
	closed := false
	closeFD := api.closeFD
	api.closeFD = func(fd int) error { closed = true; return closeFD(fd) }
	config.processes = api
	lifecycle, err := Start(t.Context(), config)
	if lifecycle != nil || !IsFailure(err, FailureIdentityUnprovable) || !closed {
		t.Fatalf("partial ownership survived failed metadata proof: lifecycle=%v err=%v pidfd_closed=%v", lifecycle, err, closed)
	}
	if child == nil || !errors.Is(child.Signal(syscall.Signal(0)), os.ErrProcessDone) {
		t.Fatal("metadata-refused child was not reaped")
	}
}

func TestStartStillRefusesWrongImageAfterEndpoint(t *testing.T) {
	config := realStartupObservationConfig(t)
	parentIdentity, parentDigest, err := processExecutable(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	api := systemProcessAPI
	api.executable = func(int) (fileIdentity, string, error) { return parentIdentity, parentDigest, nil }
	config.processes = api
	if lifecycle, err := Start(t.Context(), config); !IsFailure(err, FailureIdentityUnprovable) || lifecycle != nil {
		if lifecycle != nil {
			stopTestLifecycle(t, lifecycle)
		}
		t.Fatalf("wrong executable became an owned sidecar: lifecycle=%v err=%v", lifecycle, err)
	}
}
