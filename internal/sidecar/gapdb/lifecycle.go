package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		stdout: &boundedBuffer{maximum: maxDiagnosticBytes},
		stderr: &boundedBuffer{maximum: maxDiagnosticBytes},
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
	lifecycle.exit = make(chan error, 1)
	go func() { lifecycle.exit <- command.Wait() }()

	// Command.Start returns only after the child has completed exec; the parent
	// descriptor can now close without weakening the already-executed image.
	_ = verified.Close()
	process, pidfd, err := observeStartedProcess(command.Process.Pid, verified.provenance)
	if err != nil {
		if abortErr := lifecycle.abortStart(); abortErr != nil {
			return nil, errors.Join(err, abortErr)
		}
		return nil, err
	}
	lifecycle.process, lifecycle.pidfd = process, pidfd

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
		Executable:            verified.provenance, Process: process,
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
	pidfd, err := acquireRecordedProcess(recorded.Process)
	if err != nil {
		return nil, err
	}
	observation, err := observeAttachedComposite(ctx, recorded, config.StartTimeout)
	if err != nil {
		if pidfd >= 0 {
			_ = unix.Close(pidfd)
		}
		return nil, err
	}
	lifecycle := &Lifecycle{
		config: config, pidfd: pidfd, process: recorded.Process,
		runtimeID: runtimeID, socketID: socketID,
		stdout: &boundedBuffer{maximum: maxDiagnosticBytes}, stderr: &boundedBuffer{maximum: maxDiagnosticBytes},
	}
	lifecycle.exit = monitorRecordedProcess(pidfd, recorded.Process)
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

func acquireRecordedProcess(expected ProcessIdentity) (int, error) {
	if err := validateProcessIdentity(expected); err != nil {
		return -1, err
	}
	pidfd, err := unix.PidfdOpen(expected.PID, 0)
	if err != nil {
		if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) {
			return -1, failure(FailureIdentityUnprovable, "attach", "pidfd capability failed", err)
		}
		pidfd = -1
	}
	if err := validateProcessIdentity(expected); err != nil {
		if pidfd >= 0 {
			_ = unix.Close(pidfd)
		}
		return -1, err
	}
	return pidfd, nil
}

func monitorRecordedProcess(pidfd int, process ProcessIdentity) chan error {
	exit := make(chan error, 1)
	go func() {
		if pidfd >= 0 {
			poll := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
			_, err := unix.Poll(poll, -1)
			exit <- err
			return
		}
		for {
			start, err := processStartTicks(process.PID)
			if err != nil || start != process.StartTicks {
				exit <- err
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return exit
}

func observeAttachedComposite(ctx context.Context, recorded SidecarProvenance, timeout time.Duration) (CompositeObservation, error) {
	peer, err := peerProcess(recorded.ConfiguredSocket, timeout)
	if err != nil || peer != recorded.Process.PID {
		return CompositeObservation{}, failure(FailureOwnership, "attach", "recorded socket peer differs", err)
	}
	client, err := gapdbapi.Dial(recorded.ConfiguredSocket, gapdbapi.ClientOptions{Timeout: min(timeout, time.Second)})
	if err != nil {
		return CompositeObservation{}, failure(FailureProtocol, "attach", "public client cannot bind recorded endpoint", err)
	}
	defer client.Close()
	before, err := client.Status(ctx)
	if err != nil {
		return CompositeObservation{}, failure(FailureProtocol, "attach_status", "strict status response unavailable", err)
	}
	health, err := client.Health(ctx)
	if err != nil {
		return CompositeObservation{}, failure(FailureProtocol, "attach_health", "strict health response unavailable", err)
	}
	configuration, err := client.DescribeConfig(ctx)
	if err != nil {
		return CompositeObservation{}, failure(FailureProtocol, "attach_config", "strict configuration response unavailable", err)
	}
	after, err := client.Status(ctx)
	if err != nil {
		return CompositeObservation{}, failure(FailureProtocol, "attach_status", "closing status response unavailable", err)
	}
	if err := validateCompositeCore(before, after, health, configuration, recorded.Process); err != nil ||
		after.DatabaseID != recorded.DatabaseID || after.OwnerStartedAt != recorded.Observation.Status.OwnerStartedAt {
		return CompositeObservation{}, failure(FailureReadiness, "attach", "recorded public attribution differs", err)
	}
	return CompositeObservation{Status: after, Health: health, DescribeConfig: configuration}, nil
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
		ExecutablePath:      provenance.Executable.ExecutablePath, ExecutableSHA256: provenance.Executable.SHA256,
		ExecutableSize: provenance.Executable.Size, ExecutableDevice: provenance.Executable.Device, ExecutableInode: provenance.Executable.Inode,
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
		observation, socketID, err := l.observeComposite(ctx, socket, startedBefore, startedAfter)
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

func (l *Lifecycle) observeComposite(ctx context.Context, socket string, startedBefore, startedAfter time.Time) (CompositeObservation, fileIdentity, error) {
	peerPID, err := peerProcess(socket, l.config.StartTimeout)
	if err != nil || peerPID != l.process.PID {
		return CompositeObservation{}, fileIdentity{}, failure(FailureOwnership, "probe", "socket peer is not the owned process", err)
	}
	client, err := gapdbapi.Dial(socket, gapdbapi.ClientOptions{Timeout: min(l.config.StartTimeout, time.Second)})
	if err != nil {
		return CompositeObservation{}, fileIdentity{}, failure(FailureProtocol, "probe", "public client cannot bind configured endpoint", err)
	}
	defer client.Close()
	statusBefore, err := client.Status(ctx)
	if err != nil {
		return CompositeObservation{}, fileIdentity{}, failure(FailureProtocol, "status", "strict status response unavailable", err)
	}
	health, err := client.Health(ctx)
	if err != nil {
		return CompositeObservation{}, fileIdentity{}, failure(FailureProtocol, "health", "strict health response unavailable", err)
	}
	config, err := client.DescribeConfig(ctx)
	if err != nil {
		return CompositeObservation{}, fileIdentity{}, failure(FailureProtocol, "describe_config", "strict configuration response unavailable", err)
	}
	statusAfter, err := client.Status(ctx)
	if err != nil {
		return CompositeObservation{}, fileIdentity{}, failure(FailureProtocol, "status", "closing status response unavailable", err)
	}
	if err := validateComposite(statusBefore, statusAfter, health, config, l.process, startedBefore, startedAfter); err != nil {
		return CompositeObservation{}, fileIdentity{}, failure(FailureReadiness, "attribute", "public observations do not identify the owned instance", err)
	}
	socketInfo, err := os.Lstat(socket)
	if err != nil {
		return CompositeObservation{}, fileIdentity{}, failure(FailureOwnership, "attribute", "configured socket identity cannot be opened", err)
	}
	socketID, err := identityFromInfo(socketInfo)
	if err != nil || socketID.mode&uint32(syscall.S_IFMT) != uint32(syscall.S_IFSOCK) {
		return CompositeObservation{}, fileIdentity{}, failure(FailureOwnership, "attribute", "configured endpoint is not a socket", err)
	}
	return CompositeObservation{Status: statusAfter, Health: health, DescribeConfig: config}, socketID, nil
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
	return nil
}

func validatePrivateRoot(path string) error {
	if err := validatePathChain(path, false); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
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

func observeStartedProcess(pid int, executable ExecutableProvenance) (ProcessIdentity, int, error) {
	start, err := processStartTicks(pid)
	if err != nil {
		return ProcessIdentity{}, -1, failure(FailureIdentityUnprovable, "process", "process start identity unavailable", err)
	}
	identity, digest, err := processExecutable(pid)
	if err != nil || identity.device != executable.Device || identity.inode != executable.Inode || digest != executable.SHA256 {
		return ProcessIdentity{}, -1, failure(FailureIdentityUnprovable, "process", "running executable differs from admitted descriptor", err)
	}
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) {
			return ProcessIdentity{}, -1, failure(FailureIdentityUnprovable, "process", "pidfd capability failed", err)
		}
		pidfd = -1
	}
	secondStart, startErr := processStartTicks(pid)
	secondIdentity, secondDigest, executableErr := processExecutable(pid)
	if startErr != nil || executableErr != nil || secondStart != start ||
		secondIdentity.device != identity.device || secondIdentity.inode != identity.inode ||
		secondIdentity.size != identity.size || secondDigest != digest {
		if pidfd >= 0 {
			_ = unix.Close(pidfd)
		}
		return ProcessIdentity{}, -1, failure(FailureIdentityUnprovable, "process", "process identity changed around pidfd acquisition", errors.Join(startErr, executableErr))
	}
	return ProcessIdentity{PID: pid, StartTicks: start, Device: identity.device, Inode: identity.inode, Size: identity.size, SHA256: digest}, pidfd, nil
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
	start, err := processStartTicks(l.process.PID)
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
	identity, digest, err := processExecutable(l.process.PID)
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
	start, err := processStartTicks(l.process.PID)
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
	identity, digest, err := processExecutable(l.process.PID)
	if err != nil {
		diagnostic.State = OwnershipIndeterminate
		return diagnostic
	}
	if identity.device != l.process.Device || identity.inode != l.process.Inode || identity.size != l.process.Size || digest != l.process.SHA256 {
		diagnostic.State = OwnershipForeignLive
		return diagnostic
	}
	diagnostic.State = OwnershipOwnedLive
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
	if l.pidfd >= 0 {
		return unix.PidfdSendSignal(l.pidfd, signal, nil, 0)
	}
	// /proc start/executable identity was rechecked by the caller immediately
	// before this fallback. PID alone is never used.
	return unix.Kill(l.process.PID, signal)
}

func (l *Lifecycle) closePidfd() {
	if l.pidfd >= 0 {
		_ = unix.Close(l.pidfd)
		l.pidfd = -1
	}
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
		// Before provenance publication the configured socket's parent is the
		// only directory with the identity captured at creation.
		if l.command != nil {
			// command arguments are deliberately not parsed; find the unique child
			// by identity under the private runtime root instead.
			entries, _ := os.ReadDir(l.config.RuntimeRoot)
			for _, entry := range entries {
				candidate := filepath.Join(l.config.RuntimeRoot, entry.Name())
				file, err := os.Open(candidate)
				if err != nil {
					continue
				}
				identity, identityErr := identityOf(file)
				_ = file.Close()
				if identityErr == nil && identity.device == l.runtimeID.device && identity.inode == l.runtimeID.inode {
					runtimePath = candidate
					break
				}
			}
		}
	}
	if runtimePath == "" {
		return nil
	}
	file, err := os.Open(runtimePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return failure(FailureCleanup, "cleanup", "runtime identity cannot be opened", err)
	}
	identity, identityErr := identityOf(file)
	_ = file.Close()
	if identityErr != nil || identity.device != l.runtimeID.device || identity.inode != l.runtimeID.inode {
		return failure(FailureCleanup, "cleanup", "runtime directory is no longer attributable", identityErr)
	}
	socket := filepath.Join(runtimePath, "gapdb.sock")
	if info, statErr := os.Lstat(socket); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return failure(FailureCleanup, "cleanup", "runtime child is not the owned socket", nil)
		}
		if !requireSocketIdentity && l.socketID.inode == 0 {
			return failure(FailureCleanup, "cleanup", "unattributed socket is preserved for inspection", nil)
		}
		current, currentErr := identityFromInfo(info)
		if currentErr != nil || (requireSocketIdentity && (current.device != l.socketID.device || current.inode != l.socketID.inode)) {
			return failure(FailureCleanup, "cleanup", "socket is no longer attributable", currentErr)
		}
		if err := os.Remove(socket); err != nil {
			return failure(FailureCleanup, "cleanup", "owned socket cannot be removed", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return failure(FailureCleanup, "cleanup", "socket state cannot be inspected", statErr)
	}
	entries, err := os.ReadDir(runtimePath)
	if err != nil {
		return failure(FailureCleanup, "cleanup", "runtime contents cannot be inspected", err)
	}
	if len(entries) != 0 {
		return failure(FailureCleanup, "cleanup", "runtime contains foreign artifacts", nil)
	}
	if err := os.Remove(runtimePath); err != nil {
		return failure(FailureCleanup, "cleanup", "owned runtime directory cannot be removed", err)
	}
	return nil
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
