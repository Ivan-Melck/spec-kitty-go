package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"golang.org/x/sys/unix"
)

const (
	defaultStartTimeout = 5 * time.Second
	defaultStopTimeout  = 5 * time.Second
	maxSocketPathBytes  = 103
)

// LifecycleConfig contains explicit trusted roots. RuntimeRoot and
// DataDirectory must be outside Git worktrees; Lifecycle never discovers or
// substitutes defaults for them.
type LifecycleConfig struct {
	ExecutablePath      string
	AdmissionRecordPath string
	RuntimeRoot         string
	DataDirectory       string
	SupervisorRunID     string
	ProductPolicySHA256 string
	StartTimeout        time.Duration
	StopTimeout         time.Duration
	filesystem          filesystemProbe
	processes           processAPI
	observationHook     func(string)
	commandStarted      func(*os.Process)
}

type filesystemIdentity struct {
	Type int64
	FSID unix.Fsid
}

type filesystemProbe func(string) (filesystemIdentity, error)

type processAPI struct {
	startTicks  func(int) (uint64, error)
	executable  func(int) (fileIdentity, string, error)
	pidfdOpen   func(int, int) (int, error)
	pidfdSignal func(int, unix.Signal, *unix.Siginfo, int) error
	closeFD     func(int) error
	poll        func([]unix.PollFd, int) (int, error)
}

var systemProcessAPI = processAPI{
	startTicks: processStartTicks, executable: processExecutable,
	pidfdOpen: unix.PidfdOpen, pidfdSignal: unix.PidfdSendSignal,
	closeFD: unix.Close, poll: unix.Poll,
}

// ProcessIdentity is sufficient to reject PID reuse. PidFD is intentionally
// not exported because it is a live signalling capability.
type ProcessIdentity struct {
	PID        int
	StartTicks uint64
	Device     uint64
	Inode      uint64
	Size       int64
	SHA256     string
}

// ReadinessPredicates keeps byte recovery, public health, configuration,
// admission, and product recovery separate.
type ReadinessPredicates struct {
	ByteRecovered           bool
	Healthy                 bool
	Configured              bool
	Admitted                bool
	ProductRecovered        bool
	ReadyForProductRecovery bool
}

// CompositeObservation attributes fields only to the public surface that
// actually supplied them.
type CompositeObservation struct {
	Status         gapdbapi.StatusResult
	Health         gapdbapi.HealthResult
	DescribeConfig gapdbapi.ConfigResult
}

// SidecarProvenance binds host and public observations for one owned process.
type SidecarProvenance struct {
	SchemaVersion          uint64
	SupervisorRunID        string
	AdmissionRecordSHA256  string
	Executable             ExecutableProvenance
	ExecutedImage          ExecutedImageProvenance
	Process                ProcessIdentity
	RuntimeDirectory       string
	RuntimeDevice          uint64
	RuntimeInode           uint64
	DataDirectory          string
	ConfiguredSocket       string
	ConnectedSocket        string
	SocketDevice           uint64
	SocketInode            uint64
	DatabaseID             string
	ProductPolicySHA256    string
	WireVersion            uint64
	WireReferenceSHA256    string
	StorageVersion         uint64
	StorageReferenceSHA256 string
	StdoutSHA256           string
	StderrSHA256           string
	Observation            CompositeObservation
	Readiness              ReadinessPredicates
	AdmittedAt             time.Time
}

// ProvenanceEvidenceV1 is the exact JSON surface frozen by
// sidecar-provenance-v1.schema.json. Host-only lifecycle fields remain outside
// this value rather than leaking into the additionalProperties:false record.
type ProvenanceEvidenceV1 struct {
	SchemaVersion              uint64                        `json:"schema_version"`
	SupervisorRunID            string                        `json:"supervisor_run_id"`
	AdmittedTupleSHA256        string                        `json:"admitted_tuple_sha256"`
	ExecutablePath             string                        `json:"executable_path"`
	ExecutableSHA256           string                        `json:"executable_sha256"`
	ExecutableSize             int64                         `json:"executable_size"`
	ExecutableDevice           uint64                        `json:"executable_device"`
	ExecutableInode            uint64                        `json:"executable_inode"`
	HostPID                    int                           `json:"host_pid"`
	HostProcessStartTicks      uint64                        `json:"host_process_start_ticks"`
	ConfiguredDaemonSocketPath string                        `json:"configured_daemon_socket_path"`
	ConnectedDaemonSocketPath  string                        `json:"connected_daemon_socket_path"`
	Status                     provenanceStatusV1            `json:"status"`
	Health                     provenanceHealthV1            `json:"health"`
	DescribeConfig             provenanceConfigV1            `json:"describe_config"`
	WireVersionProof           wireVersionProofV1            `json:"wire_version_proof"`
	StorageAdmissionFact       storageAdmissionFactV1        `json:"storage_admission_fact"`
	ProductPolicySHA256        string                        `json:"product_policy_sha256"`
	QualificationProxy         *QualificationProxyEvidenceV1 `json:"qualification_proxy"`
	AdmittedAt                 time.Time                     `json:"admitted_at"`
}

type provenanceStatusV1 struct {
	DatabaseID             string                  `json:"database_id"`
	OwnerPID               int                     `json:"owner_pid"`
	OwnerStartedAt         string                  `json:"owner_started_at"`
	Lifecycle              gapdbapi.LifecycleState `json:"lifecycle"`
	CurrentRevision        gapdbapi.Revision       `json:"current_revision"`
	DurableThroughRevision gapdbapi.Revision       `json:"durable_through_revision"`
	ReservedRevisionEnd    gapdbapi.Revision       `json:"reserved_revision_end"`
	SnapshotRevision       gapdbapi.Revision       `json:"snapshot_revision"`
	ActiveWALStart         gapdbapi.Revision       `json:"active_wal_start"`
	Limits                 gapdbapi.Limits         `json:"limits"`
}

type provenanceHealthV1 struct {
	Lifecycle         gapdbapi.LifecycleState `json:"lifecycle"`
	Healthy           bool                    `json:"healthy"`
	FailingSubsystems []string                `json:"failing_subsystems"`
	SafeActions       []gapdbapi.SafeAction   `json:"safe_actions"`
}

type provenanceConfigV1 struct {
	Effective       gapdbapi.ConfigValues `json:"effective"`
	Source          string                `json:"source"`
	SafeCeilings    gapdbapi.Limits       `json:"safe_ceilings"`
	RestartRequired bool                  `json:"restart_required"`
}

type wireVersionProofV1 struct {
	SchemaVersion       uint64 `json:"schema_version"`
	ReferenceSHA256     string `json:"reference_sha256"`
	StrictTypedEnvelope bool   `json:"strict_typed_envelope"`
}

type storageAdmissionFactV1 struct {
	StorageVersion  uint64 `json:"storage_version"`
	ReferenceSHA256 string `json:"reference_sha256"`
}

type QualificationProxyEvidenceV1 struct {
	ProductSocketPath string `json:"product_socket_path"`
	DaemonSocketPath  string `json:"daemon_socket_path"`
	SourceCommit      string `json:"source_commit"`
	BinarySHA256      string `json:"binary_sha256"`
}

// OwnershipState is the closed crash-reconciliation result.
type OwnershipState string

const (
	OwnershipOwnedLive     OwnershipState = "owned_live"
	OwnershipOwnedExited   OwnershipState = "owned_exited"
	OwnershipStaleArtifact OwnershipState = "stale_artifact"
	OwnershipForeignLive   OwnershipState = "foreign_live"
	OwnershipIndeterminate OwnershipState = "indeterminate"
)

// OwnershipDiagnostic keeps daemon and proxy identities distinct. ProxyPID is
// populated by the proxy, never inferred from daemon status.
type OwnershipDiagnostic struct {
	State      OwnershipState
	Daemon     ProcessIdentity
	ProxyPID   int
	SocketPath string
	Recovery   []string
}

// Lifecycle owns exactly one process, private runtime directory, and endpoint.
type Lifecycle struct {
	mu         sync.Mutex
	config     LifecycleConfig
	command    *exec.Cmd
	exit       chan error
	pidfd      int
	process    ProcessIdentity
	provenance SidecarProvenance
	runtimeID  fileIdentity
	socketID   fileIdentity
	stdout     *boundedBuffer
	stderr     *boundedBuffer
	stopped    bool
	processes  processAPI
}

// Start verifies and descriptor-executes the exact admitted sidecar, then
// admits its composite Status/Health/DescribeConfig provenance.
func Start(ctx context.Context, config LifecycleConfig) (*Lifecycle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := normalizeLifecycleConfig(&config); err != nil {
		return nil, err
	}
	verified, err := VerifyExecutable(VerifyConfig{
		ExecutablePath: config.ExecutablePath, AdmissionRecordPath: config.AdmissionRecordPath,
	})
	if err != nil {
		return nil, err
	}
	defer verified.Close()
	if err := validatePrivateRoot(config.RuntimeRoot); err != nil {
		return nil, failure(FailureUnsafePath, "start", "runtime root is not private", err)
	}
	if err := preparePrivateDirectory(config.DataDirectory); err != nil {
		return nil, failure(FailureUnsafePath, "start", "data directory is not private", err)
	}
	runtimeDirectory, err := os.MkdirTemp(config.RuntimeRoot, "s-")
	if err != nil {
		return nil, failure(FailureStart, "start", "private runtime directory cannot be created", err)
	}
	if err := os.Chmod(runtimeDirectory, 0o700); err != nil {
		_ = os.Remove(runtimeDirectory)
		return nil, failure(FailureStart, "start", "private runtime mode cannot be set", err)
	}
	runtimeFile, err := os.Open(runtimeDirectory)
	if err != nil {
		_ = os.Remove(runtimeDirectory)
		return nil, failure(FailureStart, "start", "runtime identity cannot be opened", err)
	}
	runtimeID, err := identityOf(runtimeFile)
	_ = runtimeFile.Close()
	if err != nil {
		_ = os.Remove(runtimeDirectory)
		return nil, failure(FailureStart, "start", "runtime identity cannot be read", err)
	}
	socket := filepath.Join(runtimeDirectory, "gapdb.sock")
	if len(socket) > maxSocketPathBytes {
		_ = os.Remove(runtimeDirectory)
		return nil, failure(FailureInvalidConfiguration, "start", "configured socket exceeds Unix bound", nil)
	}

	lifecycle := &Lifecycle{
		config: config, pidfd: -1, runtimeID: runtimeID,
		stdout:    &boundedBuffer{maximum: maxDiagnosticBytes},
		stderr:    &boundedBuffer{maximum: maxDiagnosticBytes},
		processes: config.processes,
	}
	// Record the creation identity before any fallible process operation so
	// attribution-safe rollback can find this exact directory without parsing
	// command arguments or scanning unrelated roots.
	lifecycle.provenance.RuntimeDirectory = runtimeDirectory
	lifecycle.provenance.ConfiguredSocket = socket
	arguments := []string{
		"-db", config.DataDirectory, "-socket", socket, "-json=true",
		"-read-timeout=5s", "-write-timeout=5s", "-idle-timeout=30s",
	}
	command, err := verified.command(arguments, []string{"LANG=C", "TZ=UTC"})
	if err != nil {
		_ = lifecycle.cleanupRuntime(false)
		return nil, err
	}
	command.Stdout, command.Stderr = lifecycle.stdout, lifecycle.stderr
	startedBefore := time.Now().UTC()
	if err := command.Start(); err != nil {
		_ = lifecycle.cleanupRuntime(false)
		return nil, failure(FailureStart, "exec", "descriptor-bound process did not start", err)
	}
	startedAfter := time.Now().UTC()
	lifecycle.command = command
	if config.commandStarted != nil {
		config.commandStarted(command.Process)
	}

	// Command.Start returns only after the child has completed exec; the parent
	// descriptor can now close without weakening the already-executed image.
	_ = verified.Close()
	process, pidfd, err := observeStartedProcess(config.processes, command.Process.Pid, verified.executed)
	if err != nil {
		if abortErr := terminateUnobservedChild(command); abortErr != nil {
			return nil, errors.Join(err, abortErr, lifecycle.cleanupRuntime(false))
		}
		return nil, errors.Join(err, lifecycle.cleanupRuntime(false))
	}
	lifecycle.process, lifecycle.pidfd = process, pidfd
	lifecycle.exit = make(chan error, 1)
	go func() { lifecycle.exit <- command.Wait() }()

	deadline := time.Now().Add(config.StartTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	observation, socketID, err := lifecycle.waitForComposite(ctx, socket, startedBefore, startedAfter, deadline)
	if err != nil {
		if abortErr := lifecycle.abortStart(); abortErr != nil {
			return nil, errors.Join(err, abortErr)
		}
		return nil, err
	}
	lifecycle.socketID = socketID
	lifecycle.provenance = SidecarProvenance{
		SchemaVersion: 1, SupervisorRunID: config.SupervisorRunID,
		AdmissionRecordSHA256: verified.provenance.AdmissionRecordSHA256,
		Executable:            verified.provenance, ExecutedImage: verified.executed, Process: process,
		RuntimeDirectory: runtimeDirectory, RuntimeDevice: runtimeID.device, RuntimeInode: runtimeID.inode,
		DataDirectory:    config.DataDirectory,
		ConfiguredSocket: socket, ConnectedSocket: socket,
		SocketDevice: socketID.device, SocketInode: socketID.inode,
		DatabaseID:             observation.Status.DatabaseID,
		ProductPolicySHA256:    config.ProductPolicySHA256,
		WireVersion:            storegapdb.AdmittedWireVersion,
		WireReferenceSHA256:    "cd08e19252628a1cbfe5bd43018caae86569e041dc01f9d4472dc2eae72389c2",
		StorageVersion:         storegapdb.AdmittedStorageVersion,
		StorageReferenceSHA256: "6d4cca9611e9e272e02d6e566dccc887dacf738c1da8c5d580237f731018295c",
		StdoutSHA256:           lifecycle.stdout.digest(),
		StderrSHA256:           lifecycle.stderr.digest(),
		Observation:            observation,
		Readiness: ReadinessPredicates{
			ByteRecovered: true, Healthy: true, Configured: true, Admitted: true,
			ProductRecovered: false, ReadyForProductRecovery: false,
		},
		AdmittedAt: time.Now().UTC(),
	}
	return lifecycle, nil
}

// Attach re-establishes supervision of a recorded, exact live sidecar after a
// supervisor crash. It never starts, replaces, or signals an ambiguous owner.
func Attach(ctx context.Context, config LifecycleConfig, recorded SidecarProvenance) (*Lifecycle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := normalizeLifecycleConfig(&config); err != nil {
		return nil, err
	}
	verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: config.ExecutablePath, AdmissionRecordPath: config.AdmissionRecordPath})
	if err != nil {
		return nil, err
	}
	verifiedProvenance := verified.Provenance()
	_ = verified.Close()
	if recorded.SchemaVersion != 1 || recorded.Executable != verifiedProvenance ||
		recorded.AdmissionRecordSHA256 != verifiedProvenance.AdmissionRecordSHA256 ||
		recorded.DataDirectory != config.DataDirectory || recorded.ProductPolicySHA256 != config.ProductPolicySHA256 ||
		filepath.Dir(recorded.RuntimeDirectory) != config.RuntimeRoot ||
		recorded.ConfiguredSocket != filepath.Join(recorded.RuntimeDirectory, "gapdb.sock") ||
		recorded.ConnectedSocket != recorded.ConfiguredSocket {
		return nil, failure(FailureAdmissionMismatch, "attach", "recorded lifecycle does not match trusted configuration", nil)
	}
	if err := validatePrivateRoot(config.RuntimeRoot); err != nil {
		return nil, failure(FailureUnsafePath, "attach", "runtime root is not private", err)
	}
	if err := validatePrivateRoot(recorded.RuntimeDirectory); err != nil {
		return nil, failure(FailureUnsafePath, "attach", "recorded runtime is not private", err)
	}
	runtimeInfo, err := os.Lstat(recorded.RuntimeDirectory)
	if err != nil {
		return nil, failure(FailureOwnership, "attach", "recorded runtime is absent", err)
	}
	runtimeID, err := identityFromInfo(runtimeInfo)
	if err != nil || runtimeID.device != recorded.RuntimeDevice || runtimeID.inode != recorded.RuntimeInode {
		return nil, failure(FailureOwnership, "attach", "recorded runtime identity differs", err)
	}
	socketInfo, err := os.Lstat(recorded.ConfiguredSocket)
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 {
		return nil, failure(FailureOwnership, "attach", "recorded socket is absent", err)
	}
	socketID, err := identityFromInfo(socketInfo)
	if err != nil || socketID.device != recorded.SocketDevice || socketID.inode != recorded.SocketInode {
		return nil, failure(FailureOwnership, "attach", "recorded socket identity differs", err)
	}
	if recorded.ExecutedImage.Device != recorded.Process.Device || recorded.ExecutedImage.Inode != recorded.Process.Inode ||
		recorded.ExecutedImage.Size != recorded.Process.Size || recorded.ExecutedImage.SHA256 != recorded.Process.SHA256 {
		return nil, failure(FailureAdmissionMismatch, "attach", "recorded execution image does not match process", nil)
	}
	pidfd, err := acquireRecordedProcess(config.processes, recorded.Process)
	if err != nil {
		return nil, err
	}
	observation, err := observeAttachedComposite(ctx, config, recorded)
	if err != nil {
		if pidfd >= 0 {
			_ = config.processes.closeFD(pidfd)
		}
		return nil, err
	}
	lifecycle := &Lifecycle{
		config: config, pidfd: pidfd, process: recorded.Process,
		runtimeID: runtimeID, socketID: socketID,
		stdout: &boundedBuffer{maximum: maxDiagnosticBytes}, stderr: &boundedBuffer{maximum: maxDiagnosticBytes},
		processes: config.processes,
	}
	lifecycle.exit = monitorRecordedProcess(config.processes, pidfd, recorded.Process)
	lifecycle.provenance = recorded
	lifecycle.provenance.SupervisorRunID = config.SupervisorRunID
	lifecycle.provenance.Observation = observation
	lifecycle.provenance.Readiness = ReadinessPredicates{
		ByteRecovered: true, Healthy: true, Configured: true, Admitted: true,
		ProductRecovered: false, ReadyForProductRecovery: false,
	}
	lifecycle.provenance.AdmittedAt = time.Now().UTC()
	return lifecycle, nil
}

func acquireRecordedProcess(processes processAPI, expected ProcessIdentity) (int, error) {
	if err := validateProcessIdentityWith(processes, expected); err != nil {
		return -1, err
	}
	pidfd, err := processes.pidfdOpen(expected.PID, 0)
	if err != nil {
		if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) {
			return -1, failure(FailureIdentityUnprovable, "attach", "pidfd capability failed", err)
		}
		pidfd = -1
	}
	if err := validateProcessIdentityWith(processes, expected); err != nil {
		if pidfd >= 0 {
			_ = processes.closeFD(pidfd)
		}
		return -1, err
	}
	return pidfd, nil
}

func monitorRecordedProcess(processes processAPI, pidfd int, process ProcessIdentity) chan error {
	exit := make(chan error, 1)
	go func() {
		if pidfd >= 0 {
			poll := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
			_, err := processes.poll(poll, -1)
			exit <- err
			return
		}
		for {
			start, err := processes.startTicks(process.PID)
			if err != nil || start != process.StartTicks {
				exit <- err
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return exit
}

func observeAttachedComposite(ctx context.Context, config LifecycleConfig, recorded SidecarProvenance) (CompositeObservation, error) {
	before, health, configuration, after, databases, err := observeAllSurfaces(
		ctx, config, recorded.ConfiguredSocket, recorded.socketIdentity(), recorded.Process,
	)
	if err != nil {
		return CompositeObservation{}, err
	}
	if err := validateCompositeCore(before, after, health, configuration, recorded.Process); err != nil ||
		after.DatabaseID != recorded.DatabaseID || after.OwnerStartedAt != recorded.Observation.Status.OwnerStartedAt ||
		!allDatabaseIDsEqual(recorded.DatabaseID, databases) {
		return CompositeObservation{}, failure(FailureReadiness, "attach", "recorded public attribution differs", err)
	}
	return CompositeObservation{Status: after, Health: health, DescribeConfig: configuration}, nil
}

func (p SidecarProvenance) socketIdentity() fileIdentity {
	return fileIdentity{device: p.SocketDevice, inode: p.SocketInode, mode: uint32(syscall.S_IFSOCK)}
}

// Provenance returns a value copy with cloned public slices.
func (l *Lifecycle) Provenance() SidecarProvenance {
	l.mu.Lock()
	defer l.mu.Unlock()
	value := l.provenance
	value.Observation.Health.FailingSubsystems = append([]string(nil), value.Observation.Health.FailingSubsystems...)
	value.Observation.Health.SafeActions = append([]gapdbapi.SafeAction(nil), value.Observation.Health.SafeActions...)
	return value
}

// Evidence projects the admitted lifecycle into the exact frozen schema.
func (l *Lifecycle) Evidence() (ProvenanceEvidenceV1, error) {
	provenance := l.Provenance()
	if !provenance.Readiness.Admitted || provenance.DatabaseID == "" || provenance.Process.PID <= 0 {
		return ProvenanceEvidenceV1{}, failure(FailureReadiness, "evidence", "lifecycle has not been admitted", nil)
	}
	status := provenance.Observation.Status
	health := provenance.Observation.Health
	configuration := provenance.Observation.DescribeConfig
	return ProvenanceEvidenceV1{
		SchemaVersion: 1, SupervisorRunID: provenance.SupervisorRunID,
		AdmittedTupleSHA256: provenance.AdmissionRecordSHA256,
		ExecutablePath:      provenance.Executable.ExecutablePath, ExecutableSHA256: provenance.ExecutedImage.SHA256,
		ExecutableSize: provenance.ExecutedImage.Size, ExecutableDevice: provenance.ExecutedImage.Device, ExecutableInode: provenance.ExecutedImage.Inode,
		HostPID: provenance.Process.PID, HostProcessStartTicks: provenance.Process.StartTicks,
		ConfiguredDaemonSocketPath: provenance.ConfiguredSocket, ConnectedDaemonSocketPath: provenance.ConnectedSocket,
		Status: provenanceStatusV1{
			DatabaseID: status.DatabaseID, OwnerPID: status.OwnerPID, OwnerStartedAt: status.OwnerStartedAt,
			Lifecycle: status.Lifecycle, CurrentRevision: status.CurrentRevision, DurableThroughRevision: status.DurableThroughRevision,
			ReservedRevisionEnd: status.ReservedRevisionEnd, SnapshotRevision: status.SnapshotRevision,
			ActiveWALStart: status.ActiveWALStart, Limits: status.Limits,
		},
		Health: provenanceHealthV1{
			Lifecycle: health.Lifecycle, Healthy: health.Healthy,
			FailingSubsystems: append([]string{}, health.FailingSubsystems...),
			SafeActions:       append([]gapdbapi.SafeAction{}, health.SafeActions...),
		},
		DescribeConfig: provenanceConfigV1{
			Effective: configuration.Effective, Source: configuration.Source,
			SafeCeilings: configuration.SafeCeilings, RestartRequired: configuration.RestartRequired,
		},
		WireVersionProof: wireVersionProofV1{
			SchemaVersion: provenance.WireVersion, ReferenceSHA256: provenance.WireReferenceSHA256, StrictTypedEnvelope: true,
		},
		StorageAdmissionFact: storageAdmissionFactV1{
			StorageVersion: provenance.StorageVersion, ReferenceSHA256: provenance.StorageReferenceSHA256,
		},
		ProductPolicySHA256: provenance.ProductPolicySHA256, QualificationProxy: nil,
		AdmittedAt: provenance.AdmittedAt,
	}, nil
}

// MarkProductRecovered is a one-way admission step. A zero or manually
// constructed journal cannot promote byte recovery; it must carry the sealed
// capability produced by controljournal.ValidateJournalV1.
func (l *Lifecycle) MarkProductRecovered(journal controljournal.ValidatedJournalV1) error {
	if _, err := journal.Len(); err != nil {
		return failure(FailureReadiness, "admit_product", "validated journal capability is absent", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped || !l.provenance.Readiness.Admitted || !l.provenance.Readiness.Healthy || !l.provenance.Readiness.Configured {
		return failure(FailureReadiness, "admit_product", "sidecar is not admitted", nil)
	}
	l.provenance.Readiness.ProductRecovered = true
	l.provenance.Readiness.ReadyForProductRecovery = true
	return nil
}

func (l *Lifecycle) waitForComposite(ctx context.Context, socket string, startedBefore, startedAfter, deadline time.Time) (CompositeObservation, fileIdentity, error) {
	var last error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return CompositeObservation{}, fileIdentity{}, failure(FailureReadiness, "probe", "startup context ended", err)
		}
		socketID, identityErr := socketPathIdentity(socket)
		if identityErr != nil {
			last = identityErr
			time.Sleep(10 * time.Millisecond)
			continue
		}
		observation, err := l.observeComposite(ctx, socket, socketID, startedBefore, startedAfter)
		if err == nil {
			return observation, socketID, nil
		}
		last = err
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return CompositeObservation{}, fileIdentity{}, failure(FailureReadiness, "probe", "startup context ended", ctx.Err())
		case <-timer.C:
		}
	}
	return CompositeObservation{}, fileIdentity{}, failure(FailureReadiness, "probe", "configured endpoint did not become attributable", last)
}

func (l *Lifecycle) observeComposite(ctx context.Context, socket string, socketID fileIdentity, startedBefore, startedAfter time.Time) (CompositeObservation, error) {
	statusBefore, health, config, statusAfter, databases, err := observeAllSurfaces(ctx, l.config, socket, socketID, l.process)
	if err != nil {
		return CompositeObservation{}, err
	}
	if err := validateComposite(statusBefore, statusAfter, health, config, l.process, startedBefore, startedAfter); err != nil {
		return CompositeObservation{}, failure(FailureReadiness, "attribute", "public observations do not identify the owned instance", err)
	}
	if !allDatabaseIDsEqual(statusAfter.DatabaseID, databases) {
		return CompositeObservation{}, failure(FailureReadiness, "attribute", "response envelope database identities disagree", nil)
	}
	return CompositeObservation{Status: statusAfter, Health: health, DescribeConfig: config}, nil
}

func observeAllSurfaces(ctx context.Context, config LifecycleConfig, socket string, socketID fileIdentity, process ProcessIdentity) (gapdbapi.StatusResult, gapdbapi.HealthResult, gapdbapi.ConfigResult, gapdbapi.StatusResult, []string, error) {
	var before, after gapdbapi.StatusResult
	var health gapdbapi.HealthResult
	var described gapdbapi.ConfigResult
	values := []struct {
		operation   string
		destination any
	}{
		{"status", &before}, {"health", &health}, {"describe_config", &described}, {"status", &after},
	}
	databases := make([]string, 0, len(values))
	for _, value := range values {
		database, err := observeStrictUnary(ctx, config.processes, socket, socketID, process, value.operation, value.destination, config.StartTimeout)
		if err != nil {
			return before, health, described, after, nil, err
		}
		databases = append(databases, database)
		if config.observationHook != nil {
			config.observationHook(value.operation)
		}
	}
	return before, health, described, after, databases, nil
}

func observeStrictUnary(ctx context.Context, processes processAPI, socket string, socketID fileIdentity, process ProcessIdentity, operation string, destination any, timeout time.Duration) (string, error) {
	if err := verifySocketPathIdentity(socket, socketID); err != nil {
		return "", failure(FailureOwnership, operation, "configured socket identity changed", err)
	}
	connection, err := (&net.Dialer{Timeout: min(timeout, time.Second)}).DialContext(ctx, "unix", socket)
	if err != nil {
		return "", failure(FailureProtocol, operation, "configured endpoint cannot be connected", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(min(timeout, time.Second)))
	peer, err := peerProcessOfConnection(connection)
	if err != nil || peer != process.PID {
		return "", failure(FailureOwnership, operation, "response connection peer differs", err)
	}
	if err := verifyConnectedEndpoint(connection, peer, socket, socketID); err != nil {
		return "", failure(FailureOwnership, operation, "response connection endpoint differs", err)
	}
	if err := validateProcessIdentityWith(processes, process); err != nil {
		return "", err
	}
	if err := verifySocketPathIdentity(socket, socketID); err != nil {
		return "", failure(FailureOwnership, operation, "connected endpoint path changed", err)
	}
	requestID := "sidecar-observe-" + operation
	request, err := json.Marshal(wireRequest{SchemaVersion: storegapdb.AdmittedWireVersion, RequestID: requestID, Operation: operation, Arguments: json.RawMessage(`{}`)})
	if err != nil {
		return "", failure(FailureProtocol, operation, "strict request cannot be encoded", err)
	}
	if err := writeWireFrame(connection, request); err != nil {
		return "", failure(FailureProtocol, operation, "strict request cannot be published", err)
	}
	encoded, err := readWireFrame(connection, storegapdb.MaxWireFrameBytes)
	if err != nil {
		return "", failure(FailureProtocol, operation, "strict response frame is incomplete", err)
	}
	if err := validateNoDuplicateJSON(encoded); err != nil {
		return "", failure(FailureProtocol, operation, "strict response contains duplicate JSON", err)
	}
	var response wireResponse
	if err := decodeStrictJSON(encoded, &response); err != nil || response.SchemaVersion != storegapdb.AdmittedWireVersion ||
		!response.OK || response.RequestID != requestID || response.Operation != operation || response.DatabaseID == "" || len(response.Result) == 0 {
		return "", failure(FailureProtocol, operation, "strict response envelope differs", err)
	}
	if err := validateNoDuplicateJSON(response.Result); err != nil {
		return "", failure(FailureProtocol, operation, "strict result contains duplicate JSON", err)
	}
	if err := requireObservationFields(operation, response.Result); err != nil {
		return "", failure(FailureProtocol, operation, "strict result is incomplete", err)
	}
	if err := decodeStrictJSON(response.Result, destination); err != nil {
		return "", failure(FailureProtocol, operation, "strict public result differs", err)
	}
	if err := validateProcessIdentityWith(processes, process); err != nil {
		return "", err
	}
	if err := verifySocketPathIdentity(socket, socketID); err != nil {
		return "", failure(FailureOwnership, operation, "response endpoint path changed", err)
	}
	return response.DatabaseID, nil
}

func peerPathOfConnection(connection net.Conn) (string, error) {
	_, ok := connection.(*net.UnixConn)
	if !ok {
		return "", errors.New("endpoint is not a Unix connection")
	}
	address, ok := connection.RemoteAddr().(*net.UnixAddr)
	if !ok {
		return "", errors.New("peer address is not Unix")
	}
	return address.Name, nil
}

func verifyConnectedEndpoint(connection net.Conn, peerPID int, configured string, expected fileIdentity) error {
	path, err := peerPathOfConnection(connection)
	if err != nil {
		return err
	}
	observedPath := path
	const descriptorPrefix = "/proc/self/fd/"
	if strings.HasPrefix(path, descriptorPrefix) {
		observedPath = fmt.Sprintf("/proc/%d/fd/%s", peerPID, strings.TrimPrefix(path, descriptorPrefix))
	} else if path != configured {
		return errors.New("connected peer path differs from configured path")
	}
	info, err := os.Stat(observedPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return errors.Join(err, errors.New("connected peer endpoint is not a socket"))
	}
	identity, err := identityFromInfo(info)
	if err != nil || identity.device != expected.device || identity.inode != expected.inode {
		return errors.Join(err, errors.New("connected endpoint inode differs"))
	}
	return nil
}

func requireObservationFields(operation string, encoded []byte) error {
	required := map[string][]string{
		"status": {
			"lifecycle", "database_id", "current_revision", "durable_through_revision", "reserved_revision_end",
			"snapshot_revision", "active_wal_start", "earliest_watch_revision", "record_count", "watch_count",
			"queue_depth", "active_clients", "limits", "owner_pid", "owner_started_at", "snapshot_in_progress", "backup_in_progress",
		},
		"health":          {"lifecycle", "healthy", "failing_subsystems", "safe_actions"},
		"describe_config": {"effective", "source", "safe_ceilings", "restart_required"},
	}[operation]
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return err
	}
	for _, name := range required {
		if value, ok := fields[name]; !ok || bytes.Equal(value, []byte("null")) {
			return fmt.Errorf("result field %q is required", name)
		}
	}
	return nil
}

func socketPathIdentity(socket string) (fileIdentity, error) {
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		return fileIdentity{}, errors.Join(err, errors.New("configured endpoint is not a no-follow socket"))
	}
	return identityFromInfo(info)
}

func verifySocketPathIdentity(socket string, expected fileIdentity) error {
	actual, err := socketPathIdentity(socket)
	if err != nil || actual.device != expected.device || actual.inode != expected.inode {
		return errors.Join(err, errors.New("socket identity differs"))
	}
	return nil
}

func allDatabaseIDsEqual(expected string, values []string) bool {
	if expected == "" {
		return false
	}
	for _, value := range values {
		if value != expected {
			return false
		}
	}
	return true
}

func validateComposite(before, after gapdbapi.StatusResult, health gapdbapi.HealthResult, config gapdbapi.ConfigResult, process ProcessIdentity, startedBefore, startedAfter time.Time) error {
	if err := validateCompositeCore(before, after, health, config, process); err != nil {
		return err
	}
	ownerStarted, err := time.Parse(time.RFC3339Nano, after.OwnerStartedAt)
	if err != nil || ownerStarted.Before(startedBefore.Add(-time.Second)) || ownerStarted.After(startedAfter.Add(time.Second)) {
		return errors.New("reported owner start is outside spawn window")
	}
	return nil
}

func validateCompositeCore(before, after gapdbapi.StatusResult, health gapdbapi.HealthResult, config gapdbapi.ConfigResult, process ProcessIdentity) error {
	if before.DatabaseID == "" || before.DatabaseID != after.DatabaseID || before.OwnerPID != after.OwnerPID ||
		before.OwnerStartedAt != after.OwnerStartedAt || before.Lifecycle != after.Lifecycle ||
		after.OwnerPID != process.PID || after.CurrentRevision < before.CurrentRevision ||
		after.DurableThroughRevision > after.CurrentRevision || after.ReservedRevisionEnd < after.CurrentRevision {
		return errors.New("status identity or revisions disagree")
	}
	if _, err := time.Parse(time.RFC3339Nano, after.OwnerStartedAt); err != nil {
		return errors.New("reported owner start is invalid")
	}
	if after.Lifecycle != gapdbapi.LifecycleReady || health.Lifecycle != after.Lifecycle || !health.Healthy || len(health.FailingSubsystems) != 0 {
		return errors.New("health and lifecycle disagree")
	}
	if config.Source == "" || config.RestartRequired || after.Limits != config.Effective.Limits {
		return errors.New("status and configured limits disagree")
	}
	if err := validateProviderLimits(after.Limits, config.SafeCeilings); err != nil {
		return err
	}
	return nil
}

func validateProviderLimits(reported, safe gapdbapi.Limits) error {
	checks := []struct{ reported, safe, required int }{
		{reported.MaxKeyBytes, safe.MaxKeyBytes, storegapdb.MaxKeyBytes},
		{reported.MaxValueBytes, safe.MaxValueBytes, storegapdb.MaxCheckpointValueBytes},
		{reported.MaxFrameBytes, safe.MaxFrameBytes, storegapdb.MaxWireFrameBytes},
		{reported.MaxBatchBytes, safe.MaxBatchBytes, storegapdb.MaxRawBatchBytes},
		{reported.MaxBatchOperations, safe.MaxBatchOperations, storegapdb.MaxMutationsPerBatch},
		{reported.MaxScanRecords, safe.MaxScanRecords, storegapdb.MaxScanRecords},
		{reported.MaxScanBytes, safe.MaxScanBytes, storegapdb.MaxScanBytes},
		{reported.MaxConcurrentClients, safe.MaxConcurrentClients, 32},
	}
	for _, check := range checks {
		if check.reported < check.required || check.safe < check.reported {
			return errors.New("reported limit is below policy or above safe ceiling")
		}
	}
	return nil
}

func normalizeLifecycleConfig(config *LifecycleConfig) error {
	for _, field := range []struct{ name, value string }{
		{"executable", config.ExecutablePath}, {"admission", config.AdmissionRecordPath},
		{"runtime_root", config.RuntimeRoot}, {"data_directory", config.DataDirectory},
	} {
		if err := validateAbsolutePath(field.value); err != nil {
			return failure(FailureInvalidConfiguration, "configure", field.name+" path is invalid", err)
		}
	}
	if config.SupervisorRunID == "" || len(config.SupervisorRunID) > 128 {
		return failure(FailureInvalidConfiguration, "configure", "supervisor run identity is invalid", nil)
	}
	if len(config.ProductPolicySHA256) != 64 {
		return failure(FailureInvalidConfiguration, "configure", "product policy digest is invalid", nil)
	}
	if _, err := hex.DecodeString(config.ProductPolicySHA256); err != nil {
		return failure(FailureInvalidConfiguration, "configure", "product policy digest is invalid", nil)
	}
	if config.StartTimeout == 0 {
		config.StartTimeout = defaultStartTimeout
	}
	if config.StopTimeout == 0 {
		config.StopTimeout = defaultStopTimeout
	}
	if config.StartTimeout <= 0 || config.StopTimeout <= 0 || config.StartTimeout > time.Minute || config.StopTimeout > time.Minute {
		return failure(FailureInvalidConfiguration, "configure", "lifecycle timeout is outside bound", nil)
	}
	if config.filesystem == nil {
		config.filesystem = statFilesystem
	}
	if config.processes.startTicks == nil {
		config.processes = systemProcessAPI
	}
	if err := validateStorageTopology(*config); err != nil {
		return failure(FailureUnsafePath, "configure", "runtime and data topology is not admitted", err)
	}
	return nil
}

func statFilesystem(path string) (filesystemIdentity, error) {
	fd, err := openDirectoryNoFollow(path)
	if err != nil {
		return filesystemIdentity{}, err
	}
	defer unix.Close(fd)
	var value unix.Statfs_t
	if err := unix.Fstatfs(fd, &value); err != nil {
		return filesystemIdentity{}, err
	}
	return filesystemIdentity{Type: value.Type, FSID: value.Fsid}, nil
}

func validateStorageTopology(config LifecycleConfig) error {
	runtimePath := config.RuntimeRoot
	dataPath := config.DataDirectory
	if _, err := os.Lstat(dataPath); errors.Is(err, os.ErrNotExist) {
		dataPath = filepath.Dir(dataPath)
	} else if err != nil {
		return err
	}
	for _, path := range []string{runtimePath, dataPath} {
		if err := validatePathChain(path, false); err != nil {
			return err
		}
		if err := rejectGitAncestor(path); err != nil {
			return err
		}
	}
	runtimeFS, err := config.filesystem(runtimePath)
	if err != nil || !admittedLocalFilesystem(runtimeFS.Type) {
		return errors.Join(err, errors.New("runtime filesystem is shared or unsupported"))
	}
	dataFS, err := config.filesystem(dataPath)
	if err != nil || !admittedLocalFilesystem(dataFS.Type) {
		return errors.Join(err, errors.New("data filesystem is shared or unsupported"))
	}
	if runtimeFS.Type != dataFS.Type || runtimeFS.FSID != dataFS.FSID {
		return errors.New("runtime and data do not share one local filesystem authority")
	}
	return nil
}

func admittedLocalFilesystem(kind int64) bool {
	switch kind {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.F2FS_SUPER_MAGIC:
		return true
	default:
		return false
	}
}

func rejectGitAncestor(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		marker := filepath.Join(current, ".git")
		if _, err := os.Lstat(marker); err == nil {
			return errors.New("storage path is inside a Git repository or worktree")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func validatePrivateRoot(path string) error {
	if err := validatePathChain(path, false); err != nil {
		return err
	}
	fd, err := openDirectoryNoFollow(path)
	if err != nil {
		return errors.New("root must be an owner-only no-follow directory")
	}
	file := os.NewFile(uintptr(fd), "private-root")
	identity, identityErr := identityOf(file)
	_ = file.Close()
	if identityErr != nil || identity.mode&uint32(syscall.S_IFMT) != uint32(syscall.S_IFDIR) || identity.mode&0o777 != 0o700 {
		return errors.New("root must be an owner-only directory")
	}
	return nil
}

func preparePrivateDirectory(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := validatePrivateRoot(filepath.Dir(path)); err != nil {
			return err
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return validatePrivateRoot(path)
}

func observeStartedProcess(processes processAPI, pid int, executable ExecutedImageProvenance) (ProcessIdentity, int, error) {
	start, err := processes.startTicks(pid)
	if err != nil {
		return ProcessIdentity{}, -1, failure(FailureIdentityUnprovable, "process", "process start identity unavailable", err)
	}
	identity, digest, err := processes.executable(pid)
	if err != nil || identity.device != executable.Device || identity.inode != executable.Inode || identity.size != executable.Size || digest != executable.SHA256 {
		return ProcessIdentity{}, -1, failure(FailureIdentityUnprovable, "process", "running executable differs from admitted descriptor", err)
	}
	pidfd, err := processes.pidfdOpen(pid, 0)
	if err != nil {
		if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) {
			return ProcessIdentity{}, -1, failure(FailureIdentityUnprovable, "process", "pidfd capability failed", err)
		}
		pidfd = -1
	}
	secondStart, startErr := processes.startTicks(pid)
	secondIdentity, secondDigest, executableErr := processes.executable(pid)
	if startErr != nil || executableErr != nil || secondStart != start ||
		secondIdentity.device != identity.device || secondIdentity.inode != identity.inode ||
		secondIdentity.size != identity.size || secondDigest != digest {
		if pidfd >= 0 {
			_ = processes.closeFD(pidfd)
		}
		return ProcessIdentity{}, -1, failure(FailureIdentityUnprovable, "process", "process identity changed around pidfd acquisition", errors.Join(startErr, executableErr))
	}
	return ProcessIdentity{PID: pid, StartTicks: start, Device: identity.device, Inode: identity.inode, Size: identity.size, SHA256: digest}, pidfd, nil
}

func terminateUnobservedChild(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return errors.New("started child handle is absent")
	}
	killErr := command.Process.Kill()
	waitErr := command.Wait()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		waitErr = nil
	}
	return errors.Join(killErr, waitErr)
}

func processStartTicks(pid int) (uint64, error) {
	encoded, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	closeIndex := bytes.LastIndexByte(encoded, ')')
	if closeIndex < 0 || closeIndex+2 >= len(encoded) {
		return 0, errors.New("process stat is malformed")
	}
	fields := strings.Fields(string(encoded[closeIndex+2:]))
	if len(fields) <= 19 {
		return 0, errors.New("process stat lacks start time")
	}
	value, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || value == 0 {
		return 0, errors.New("process start time is invalid")
	}
	return value, nil
}

func processExecutable(pid int) (fileIdentity, string, error) {
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/exe", pid), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fileIdentity{}, "", err
	}
	file := os.NewFile(uintptr(fd), "process-executable")
	defer file.Close()
	identity, err := identityOf(file)
	if err != nil {
		return fileIdentity{}, "", err
	}
	if identity.size <= 0 || identity.size > 64<<20 {
		return fileIdentity{}, "", errors.New("process executable size is outside bound")
	}
	digest, err := digestFile(file, identity.size)
	return identity, digest, err
}

func peerProcess(socket string, timeout time.Duration) (int, error) {
	connection, err := net.DialTimeout("unix", socket, min(timeout, time.Second))
	if err != nil {
		return 0, err
	}
	defer connection.Close()
	return peerProcessOfConnection(connection)
}

func peerProcessOfConnection(connection net.Conn) (int, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return 0, errors.New("endpoint is not a Unix connection")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credential *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if controlErr != nil || credential == nil {
		return 0, controlErr
	}
	return int(credential.Pid), nil
}

// InspectOwnership revalidates every live identity coordinate and never turns
// a stale artifact into authority.
func (l *Lifecycle) InspectOwnership() OwnershipDiagnostic {
	l.mu.Lock()
	defer l.mu.Unlock()
	diagnostic := OwnershipDiagnostic{Daemon: l.process, SocketPath: l.provenance.ConfiguredSocket}
	processes := l.hostProcesses()
	start, err := processes.startTicks(l.process.PID)
	if errors.Is(err, os.ErrNotExist) {
		diagnostic.State = l.exitedArtifactStateLocked()
		return diagnostic
	}
	if err != nil {
		diagnostic.State = OwnershipIndeterminate
		diagnostic.Recovery = operatorRecovery()
		return diagnostic
	}
	if start != l.process.StartTicks {
		diagnostic.State = OwnershipForeignLive
		diagnostic.Recovery = operatorRecovery()
		return diagnostic
	}
	identity, digest, err := processes.executable(l.process.PID)
	if err != nil {
		diagnostic.State = OwnershipIndeterminate
		diagnostic.Recovery = operatorRecovery()
		return diagnostic
	}
	if identity.device != l.process.Device || identity.inode != l.process.Inode || identity.size != l.process.Size || digest != l.process.SHA256 {
		diagnostic.State = OwnershipForeignLive
		diagnostic.Recovery = operatorRecovery()
		return diagnostic
	}
	diagnostic.State = OwnershipOwnedLive
	if l.pidfd < 0 {
		diagnostic.Recovery = operatorRecovery()
	}
	return diagnostic
}

// Stop is idempotent and signals only the revalidated exact process.
func (l *Lifecycle) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return nil
	}
	diagnostic := l.inspectOwnershipLocked()
	if diagnostic.State == OwnershipOwnedExited || diagnostic.State == OwnershipStaleArtifact {
		l.stopped = true
		l.closePidfd()
		l.mu.Unlock()
		return l.cleanupRuntime(true)
	}
	if diagnostic.State != OwnershipOwnedLive {
		l.mu.Unlock()
		return failure(FailureOwnership, "stop", "process ownership is not provable", nil)
	}
	if err := l.signalLocked(unix.SIGTERM); err != nil {
		l.mu.Unlock()
		return failure(FailureOwnership, "stop", "owned process could not be signalled", err)
	}
	exit := l.exit
	timeout := l.config.StopTimeout
	l.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return failure(FailureStart, "stop", "stop context ended", ctx.Err())
	case <-timer.C:
		l.mu.Lock()
		if l.inspectOwnershipLocked().State != OwnershipOwnedLive {
			l.mu.Unlock()
			return failure(FailureOwnership, "stop", "ownership changed during shutdown", nil)
		}
		if err := l.signalLocked(unix.SIGKILL); err != nil {
			l.mu.Unlock()
			return failure(FailureOwnership, "stop", "owned process could not be terminated", err)
		}
		l.mu.Unlock()
		select {
		case <-exit:
		case <-time.After(time.Second):
			return failure(FailureStart, "stop", "owned process did not exit", nil)
		}
	case <-exit:
	}
	l.mu.Lock()
	l.stopped = true
	l.closePidfd()
	l.mu.Unlock()
	return l.cleanupRuntime(true)
}

func (l *Lifecycle) inspectOwnershipLocked() OwnershipDiagnostic {
	diagnostic := OwnershipDiagnostic{Daemon: l.process, SocketPath: l.provenance.ConfiguredSocket}
	processes := l.hostProcesses()
	start, err := processes.startTicks(l.process.PID)
	if errors.Is(err, os.ErrNotExist) {
		diagnostic.State = l.exitedArtifactStateLocked()
		return diagnostic
	}
	if err != nil {
		diagnostic.State = OwnershipIndeterminate
		return diagnostic
	}
	if start != l.process.StartTicks {
		diagnostic.State = OwnershipForeignLive
		return diagnostic
	}
	identity, digest, err := processes.executable(l.process.PID)
	if err != nil {
		diagnostic.State = OwnershipIndeterminate
		return diagnostic
	}
	if identity.device != l.process.Device || identity.inode != l.process.Inode || identity.size != l.process.Size || digest != l.process.SHA256 {
		diagnostic.State = OwnershipForeignLive
		return diagnostic
	}
	diagnostic.State = OwnershipOwnedLive
	if l.pidfd < 0 {
		diagnostic.Recovery = operatorRecovery()
	}
	return diagnostic
}

func (l *Lifecycle) exitedArtifactStateLocked() OwnershipState {
	runtimePath := l.provenance.RuntimeDirectory
	if runtimePath == "" {
		return OwnershipOwnedExited
	}
	info, err := os.Lstat(runtimePath)
	if errors.Is(err, os.ErrNotExist) {
		return OwnershipOwnedExited
	}
	if err != nil {
		return OwnershipIndeterminate
	}
	identity, err := identityFromInfo(info)
	if err != nil || identity.device != l.runtimeID.device || identity.inode != l.runtimeID.inode {
		return OwnershipIndeterminate
	}
	return OwnershipStaleArtifact
}

// CleanupStale reconciles a previously owned, now-exited lifecycle. It refuses
// live, foreign, and indeterminate owners and removes only unchanged artifacts.
func (l *Lifecycle) CleanupStale() error {
	l.mu.Lock()
	state := l.inspectOwnershipLocked().State
	if state != OwnershipStaleArtifact && state != OwnershipOwnedExited {
		l.mu.Unlock()
		return failure(FailureCleanup, "cleanup_stale", "lifecycle is not a proven exited owner", nil)
	}
	l.stopped = true
	l.closePidfd()
	l.mu.Unlock()
	return l.cleanupRuntime(true)
}

func (l *Lifecycle) signalLocked(signal unix.Signal) error {
	processes := l.hostProcesses()
	if l.pidfd >= 0 {
		return processes.pidfdSignal(l.pidfd, signal, nil, 0)
	}
	return errors.New("pidfd signaling is unavailable; process is left untouched")
}

func (l *Lifecycle) closePidfd() {
	if l.pidfd >= 0 {
		_ = l.hostProcesses().closeFD(l.pidfd)
		l.pidfd = -1
	}
}

func (l *Lifecycle) hostProcesses() processAPI {
	if l.processes.startTicks == nil {
		return systemProcessAPI
	}
	return l.processes
}

func (l *Lifecycle) abortStart() error {
	l.mu.Lock()
	if l.command != nil && l.inspectOwnershipLocked().State == OwnershipOwnedLive {
		_ = l.signalLocked(unix.SIGKILL)
	}
	exit := l.exit
	l.mu.Unlock()
	if exit != nil {
		select {
		case <-exit:
		case <-time.After(time.Second):
		}
	}
	l.mu.Lock()
	l.stopped = true
	l.closePidfd()
	l.mu.Unlock()
	return l.cleanupRuntime(false)
}

func (l *Lifecycle) cleanupRuntime(requireSocketIdentity bool) error {
	runtimePath := l.provenance.RuntimeDirectory
	if runtimePath == "" {
		return nil
	}
	parentFD, err := openDirectoryNoFollow(filepath.Dir(runtimePath))
	if err != nil {
		return failure(FailureCleanup, "cleanup", "runtime parent cannot be opened without following links", err)
	}
	defer unix.Close(parentFD)
	name := filepath.Base(runtimePath)
	if err := verifyDirectoryEntry(parentFD, name, l.runtimeID); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return failure(FailureCleanup, "cleanup", "runtime directory entry is no longer attributable", err)
	}
	runtimeFD, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return failure(FailureCleanup, "cleanup", "runtime directory cannot be opened without following links", err)
	}
	runtimeFile := os.NewFile(uintptr(runtimeFD), "owned-runtime")
	defer runtimeFile.Close()
	identity, identityErr := identityOf(runtimeFile)
	if identityErr != nil || identity.device != l.runtimeID.device || identity.inode != l.runtimeID.inode {
		return failure(FailureCleanup, "cleanup", "opened runtime directory is no longer attributable", identityErr)
	}
	var socketStat unix.Stat_t
	statErr := unix.Fstatat(runtimeFD, "gapdb.sock", &socketStat, unix.AT_SYMLINK_NOFOLLOW)
	if statErr == nil {
		current := identityFromStat(socketStat)
		if current.mode&uint32(syscall.S_IFMT) != uint32(syscall.S_IFSOCK) {
			return failure(FailureCleanup, "cleanup", "runtime child is not the owned socket", nil)
		}
		if !requireSocketIdentity && l.socketID.inode == 0 {
			return failure(FailureCleanup, "cleanup", "unattributed socket is preserved for inspection", nil)
		}
		if requireSocketIdentity && (current.device != l.socketID.device || current.inode != l.socketID.inode) {
			return failure(FailureCleanup, "cleanup", "socket is no longer attributable", nil)
		}
		if err := unix.Unlinkat(runtimeFD, "gapdb.sock", 0); err != nil {
			return failure(FailureCleanup, "cleanup", "owned socket cannot be removed", err)
		}
	} else if !errors.Is(statErr, unix.ENOENT) {
		return failure(FailureCleanup, "cleanup", "socket state cannot be inspected", statErr)
	}
	entries, err := runtimeFile.ReadDir(-1)
	if err != nil {
		return failure(FailureCleanup, "cleanup", "runtime contents cannot be inspected", err)
	}
	if len(entries) != 0 {
		return failure(FailureCleanup, "cleanup", "runtime contains foreign artifacts", nil)
	}
	if err := verifyDirectoryEntry(parentFD, name, l.runtimeID); err != nil {
		return failure(FailureCleanup, "cleanup", "runtime entry changed before removal", err)
	}
	if err := unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR); err != nil {
		return failure(FailureCleanup, "cleanup", "owned runtime directory cannot be removed", err)
	}
	return nil
}

func openDirectoryNoFollow(path string) (int, error) {
	return unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS),
	})
}

func verifyDirectoryEntry(parentFD int, name string, expected fileIdentity) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	actual := identityFromStat(stat)
	if actual.mode&uint32(syscall.S_IFMT) != uint32(syscall.S_IFDIR) || actual.device != expected.device || actual.inode != expected.inode {
		return errors.New("directory entry identity differs")
	}
	return nil
}

func identityFromStat(stat unix.Stat_t) fileIdentity {
	return fileIdentity{
		device: uint64(stat.Dev), inode: stat.Ino, size: stat.Size, mode: stat.Mode,
		uid: stat.Uid, gid: stat.Gid, nlink: uint64(stat.Nlink),
		mtimeNsec: stat.Mtim.Sec*1_000_000_000 + stat.Mtim.Nsec,
		ctimeNsec: stat.Ctim.Sec*1_000_000_000 + stat.Ctim.Nsec,
	}
}

func operatorRecovery() []string {
	return []string{"preserve runtime and data directories", "inspect process start and executable identity", "remove artifacts only after proving the owner exited"}
}

type boundedBuffer struct {
	mu      sync.Mutex
	maximum int
	buffer  bytes.Buffer
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.maximum - b.buffer.Len()
	if remaining > 0 {
		_, _ = b.buffer.Write(value[:min(remaining, len(value))])
	}
	return len(value), nil
}

func (b *boundedBuffer) digest() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	digest := sha256.Sum256(b.buffer.Bytes())
	return hex.EncodeToString(digest[:])
}

var _ io.Writer = (*boundedBuffer)(nil)
