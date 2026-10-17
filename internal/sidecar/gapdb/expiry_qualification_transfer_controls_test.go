//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// These are component/API exclusion controls, not authenticated launch or Host
// evidence. The stripped-marker case reaches the ACTUAL ordinary verifier with
// exact R bytes and valid normalized prerequisites. The inert executable path
// is only a safe regular-file prerequisite; no self ELF or image is authenticated.
func TestExpiryW5TransferStrippedMarkerOrdinaryVerifierBoundary(t *testing.T) {
	root, err := os.MkdirTemp("", "expiry-w5-transfer-component-")
	if err != nil { t.Fatal(err) }
	t.Logf("preserved component prerequisite directory (not Native): %s", root)
	runtimeRoot, data := filepath.Join(root, "runtime"), filepath.Join(root, "data")
	for _, path := range []string{runtimeRoot, data} { if err := os.Mkdir(path, 0700); err != nil { t.Fatal(err) } }
	input, inert := filepath.Join(root, "input-record.json"), filepath.Join(root, "inert-path-prerequisite")
	if err := os.WriteFile(input, []byte(expiryInputCanonical+"\n"), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(inert, []byte("NOT AN AUTHENTICATED IMAGE; NEVER EXECUTED\n"), 0500); err != nil { t.Fatal(err) }
	if expiryRawSHA256([]byte(expiryInputCanonical+"\n")) != expiryInputRecordSHA256 { t.Fatal("R prerequisite bytes drifted") }
	var fsCalls, processCalls, observations, starts int
	unexpected := errors.New("forbidden process callback reached")
	processes := processAPI{
		startTicks: func(int) (uint64, error) { processCalls++; return 0, unexpected },
		executableIdentity: func(int) (fileIdentity, error) { processCalls++; return fileIdentity{}, unexpected },
		executable: func(int) (fileIdentity, string, error) { processCalls++; return fileIdentity{}, "", unexpected },
		pidfdOpen: func(int, int) (int, error) { processCalls++; return -1, unexpected },
		pidfdSignal: func(int, unix.Signal, *unix.Siginfo, int) error { processCalls++; return unexpected },
		closeFD: func(int) error { processCalls++; return unexpected },
		poll: func([]unix.PollFd, int) (int, error) { processCalls++; return 0, unexpected },
	}
	config := LifecycleConfig{
		ExecutablePath: inert, AdmissionRecordPath: input, RuntimeRoot: runtimeRoot, DataDirectory: data,
		SupervisorRunID: "supervisor-w5-transfer", ProductPolicySHA256: strings.Repeat("a", 64), processes: processes,
		// Configuration/source exclusion only: topology callback is a counted
		// existing private prerequisite, not a fake selected provider/caller.
		filesystem: func(string) (filesystemIdentity, error) { fsCalls++; return filesystemIdentity{Type: unix.EXT4_SUPER_MAGIC}, nil },
		observationHook: func(string) { observations++ }, commandStarted: func(*os.Process) { starts++ },
	}
	if err := normalizeLifecycleConfig(&config); err != nil { t.Fatal("required valid prerequisites", err) }
	if fsCalls != 2 || processCalls != 0 { t.Fatal("normalization did not reach exactly both topology checks") }
	fsCalls = 0
	beforeRuntime, err := os.ReadDir(runtimeRoot)
	if err != nil { t.Fatal(err) }
	beforeData, err := os.ReadDir(data)
	if err != nil { t.Fatal(err) }
	// Removing the contract marker does NOT mint ordinary admission.
	recorded := SidecarProvenance{SchemaVersion: 1, Executable: ExecutableProvenance{ContractRevision: expiryQualificationContract}}
	recorded.Executable.ContractRevision = ""
	if !expiryAttachAllowed(recorded) { t.Fatal("stripped marker must reach ordinary verification, not marked-entry refusal") }
	lifecycle, attachErr := Attach(context.Background(), config, recorded)
	var typed *Error
	if lifecycle != nil || !errors.As(attachErr, &typed) || typed.Kind != FailureAdmissionMismatch || typed.Operation != "verify" || typed.Reason != "admission record digest differs" {
		t.Fatalf("wrong ordinary-verifier boundary; escaped=%t error=%v", lifecycle != nil, attachErr)
	}
	if fsCalls != 2 || processCalls != 0 || observations != 0 || starts != 0 { t.Fatalf("side effects topology/process/probe/start=%d/%d/%d/%d", fsCalls, processCalls, observations, starts) }
	afterRuntime, err := os.ReadDir(runtimeRoot)
	if err != nil { t.Fatal(err) }
	afterData, err := os.ReadDir(data)
	if err != nil { t.Fatal(err) }
	if len(beforeRuntime) != 0 || len(beforeData) != 0 || len(afterRuntime) != 0 || len(afterData) != 0 { t.Fatal("ordinary verifier refusal mutated prerequisite directories") }
}

func TestExpiryW5TransferMarkedAttachEntryBeforeNormalization(t *testing.T) {
	calls := 0
	config := LifecycleConfig{filesystem: func(string) (filesystemIdentity, error) { calls++; return filesystemIdentity{}, errors.New("must not normalize") }}
	recorded := SidecarProvenance{Executable: ExecutableProvenance{ContractRevision: expiryQualificationContract}}
	lifecycle, err := Attach(nil, config, recorded)
	var typed *Error
	if lifecycle != nil || !errors.As(err, &typed) || typed.Kind != FailureAdmissionMismatch || typed.Operation != "attach" || typed.Reason != "expiry qualification cannot be attached" || calls != 0 {
		t.Fatalf("marked Attach did not refuse at entry: calls=%d escaped=%t err=%v", calls, lifecycle != nil, err)
	}
}

func TestExpiryW5TransferMarkedRestartEntryStateGuard(t *testing.T) {
	for _, mode := range []RestartMode{RestartClean, RestartUnclean} {
		t.Run(string(mode), func(t *testing.T) {
			// Marker-only state guard: NOT a live lifecycle or a Native restart.
			lifecycle := &Lifecycle{expiry: lifecycleExpiryState{gate: new(expiryGateState)}}
			lifecycle.mu.Lock() // entry refusal must not acquire lifecycle.mu
			next, err := lifecycle.RestartOwned(context.Background(), mode, "supervisor-w5-replacement")
			lifecycle.mu.Unlock()
			var typed *Error
			if next != nil || !errors.As(err, &typed) || typed.Kind != FailureAdmissionMismatch || typed.Operation != "restart" || typed.Reason != "expiry qualification requires its private generation transition" {
				t.Fatalf("ordinary restart escaped marked-entry exclusion: %v", err)
			}
			if lifecycle.stopped || lifecycle.ownedRevoked.Load() || lifecycle.expiry.gate.invalid.Load() { t.Fatal("refused ordinary restart performed teardown") }
		})
	}
}

func TestExpiryW5TransferUnsupportedCapabilityMethodSet(t *testing.T) {
	// Explicit false capability inventory. These method-set checks prevent a
	// new admin path being mistaken for authorized expiry qualification. They
	// are NOT protocol-admission tests and do not invent magic refusal RPCs.
	for _, object := range []struct{name string; value any}{
		{"HostGate", (*ExpiryQualification)(nil)}, {"Lifecycle", (*Lifecycle)(nil)},
	} {
		for _, method := range []string{"Snapshot", "Checkpoint", "Backup", "FullVerify", "Watch", "ImageRestore", "RestoreImage"} {
			t.Run(object.name+"/"+method, func(t *testing.T) {
				if _, exists := reflect.TypeOf(object.value).MethodByName(method); exists { t.Fatal("unsupported transfer/admin capability became present", method) }
			})
		}
	}
}
