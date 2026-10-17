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
	"sync/atomic"
	"syscall"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalcheckpoint "github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

const (
	defaultStartTimeout = 5 * time.Second
	defaultStopTimeout  = 5 * time.Second
	maxSocketPathBytes  = 103
	maxSchemaTokenBytes = 128
)

// LifecycleConfig contains explicit trusted roots. RuntimeRoot and
// DataDirectory must be outside Git worktrees; Lifecycle never discovers or
// substitutes defaults for them.
type LifecycleConfig struct {
	ExecutablePath      string
	AdmissionRecordPath string
	Admission           ExecutableAdmission
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
	startTicks         func(int) (uint64, error)
	executableIdentity func(int) (fileIdentity, error)
	executable         func(int) (fileIdentity, string, error)
	pidfdOpen          func(int, int) (int, error)
	pidfdSignal        func(int, unix.Signal, *unix.Siginfo, int) error
	closeFD            func(int) error
	poll               func([]unix.PollFd, int) (int, error)
}

var systemProcessAPI = processAPI{
	startTicks: processStartTicks, executableIdentity: processExecutableIdentity, executable: processExecutable,
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

// RestartMode names the two owner-loss paths qualification and the supervisor
// can distinguish without guessing from process timing.
type RestartMode string

const (
	RestartClean   RestartMode = "clean"
	RestartUnclean RestartMode = "unclean"
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
	expiry          lifecycleExpiryState
	mu              sync.Mutex
	config          LifecycleConfig
	command         *exec.Cmd
	exit            chan error
	pidfd           int
	process         ProcessIdentity
	processImage    fileIdentity
	provenance      SidecarProvenance
	runtimeID       fileIdentity
	socketID        fileIdentity
	stdout          *boundedBuffer
	stderr          *boundedBuffer
	stopped         bool
	processes       processAPI
	productRestores map[string]ProductRestoreProofV1
	retained        atomic.Pointer[retainedkernel.Lifetime]
	ownedRevoked    atomic.Bool
	ownedEndpoint   string
	ownedBootstrap  gapdbapi.StatusResult
	startupAbort startupAbortOutcome // published by shared custody Once, immutable thereafter
	pidfdCloseAttempted bool // mu; FIRST attempt is not a success certificate
	pidfdCloseErr error // mu; immutable FIRST close outcome
	teardownCustody atomic.Pointer[teardownCustodyClaim]
	teardownCustodyErr error // entire first teardown, published before claim.done
	shutdownWait actualWaitObservation // sole FIRST owner records actual state/error
	shutdownWaitChecked bool // includes missing owner, distinct from actual attempt
	attached bool // ordinary external monitor, never child reap authority
	attachExitObserved bool
	attachExitErr error
	runtimeCleanupOnce sync.Once
	runtimeCleanupErr error // includes directory then parent close results
	pendingCustody startupPendingCustody // startup owner; copied into FIRST abort
	childWaitOnce sync.Once
	childWait *startupChildWait // actual Cmd.Wait publication, not poll completion
	startupPublished atomic.Pointer[startupCustodyPublication] // immutable startup-only snapshots for lock-refusal collection
}

// Start verifies and descriptor-executes the exact admitted sidecar, then
// admits its composite Status/Health/DescribeConfig provenance.
func Start(ctx context.Context, config LifecycleConfig) (result *Lifecycle, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := normalizeLifecycleConfig(&config); err != nil {
		return nil, err
	}
	verified, err := VerifyExecutable(VerifyConfig{
		ExecutablePath: config.ExecutablePath, AdmissionRecordPath: config.AdmissionRecordPath, Admission: config.Admission,
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		closeErr := verified.Close()
		if closeErr != nil && result != nil {
			closeErr = withStartupCustody(closeErr, result.Stop(ctx))
			result = nil
		}
		resultErr = withStartupCustody(resultErr, closeErr)
	}()
	return startVerified(ctx, config, verified)
}

// startVerified shares the entire post-verification launch with the fixed tagged
// root. Conditional hooks are inert for ordinary verified executables.
func startVerified(ctx context.Context, config LifecycleConfig, verified *VerifiedExecutable) (_ *Lifecycle, resultErr error) {
	defer func() {
		if resultErr != nil {
			verified.bindExpiryFailedCleanup(ctx)
			verified.expiryStartFailed()
		}
	}()
	if err := validatePrivateRootWithReport(config.RuntimeRoot, verified.expiry.cleanupReport()); err != nil {
		return nil, failure(FailureUnsafePath, "start", "runtime root is not private", err)
	}
	if err := preparePrivateDirectoryWithReport(config.DataDirectory, verified.expiry.cleanupReport()); err != nil {
		return nil, failure(FailureUnsafePath, "start", "data directory is not private", err)
	}
	runtimeDirectory, err := os.MkdirTemp(config.RuntimeRoot, "s-")
	if err != nil {
		return nil, failure(FailureStart, "start", "private runtime directory cannot be created", err)
	}
	if err := os.Chmod(runtimeDirectory, 0o700); err != nil {
		return nil, withStartupCustody(failure(FailureStart, "start", "private runtime mode cannot be set", err), preservedCreatedRuntime(runtimeDirectory, fileIdentity{}))
	}
	runtimeFile, err := os.Open(runtimeDirectory)
	if err != nil {
		return nil, withStartupCustody(failure(FailureStart, "start", "runtime identity cannot be opened", err), preservedCreatedRuntime(runtimeDirectory, fileIdentity{}))
	}
	verified.expiry.cleanupReport().localOpened()
	runtimeID, err := identityOf(runtimeFile)
	runtimeCloseErr := verified.expiry.cleanupReport().closeLocalFile(runtimeFile)
	socket := filepath.Join(runtimeDirectory, "gapdb.sock")
	if primary := runtimeIdentityResult(socket, err, runtimeCloseErr); primary != nil {
		if err != nil || runtimeCloseErr != nil {
			return nil, withStartupCustody(primary, preservedCreatedRuntime(runtimeDirectory, runtimeID))
		}
		// Pure socket-bound refusal still owns a known directory. Use exact
		// attribution/empty-content checks, never a blind pathname Remove.
		return nil, withStartupCustody(primary, cleanupCreatedRuntime(ctx, config, runtimeDirectory, runtimeID, verified.expiry.cleanupReport()))
	}

	lifecycle := &Lifecycle{
		config: config, pidfd: -1, runtimeID: runtimeID,
		stdout:    &boundedBuffer{maximum: maxDiagnosticBytes},
		stderr:    &boundedBuffer{maximum: maxDiagnosticBytes},
		processes: config.processes,
	}
	lifecycle.initExpiry(verified)
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
		cleanup, cancel := lifecycle.startupCleanupContext(ctx)
		defer cancel()
		verified.expiryStartFailed()
		return nil, withStartupCustody(err, lifecycle.cleanupRuntimeBefore(cleanup, false))
	}
	command.Stdout, command.Stderr = lifecycle.stdout, lifecycle.stderr
	startedBefore := time.Now().UTC()
	if err := command.Start(); err != nil {
		verified.expiryCommandStarted()
		cleanup, cancel := lifecycle.startupCleanupContext(ctx)
		defer cancel()
		verified.expiryStartFailed()
		return nil, withStartupCustody(failure(FailureStart, "exec", "descriptor-bound process did not start", err), lifecycle.cleanupRuntimeBefore(cleanup, false))
	}
	verified.expiryCommandStarted()
	startedAfter := time.Now().UTC()
	lifecycle.command = command
	lifecycle.startChildWait(command) // one actual Wait owner, including pre-attribution failures
	if config.commandStarted != nil {
		config.commandStarted(command.Process)
	}

	// The kernel has consumed the descriptor, but Cmd.Start's exec-error-pipe
	// handshake need not wait for /proc/PID/exe to expose the new mm. A freshly
	// created endpoint is a post-exec ordering trigger, not ownership evidence.
	// Establish the exact image and pidfd once after that trigger, before any
	// protocol observation. Never retry an image mismatch or trust the socket.
	if closeErr := verified.Close(); closeErr != nil {
		return nil, withStartupCustody(closeErr, lifecycle.abortStartBefore(ctx, lifecycle.originalNonEscalatingCleanupCutoff(ctx)))
	}

	// ORIGINAL readiness startpoint: after private setup, launch and descriptor
	// consumption. Cleanup has a separate original envelope, not this deadline.
	deadline := intersectCleanupDeadline(ctx, time.Now().Add(config.StartTimeout))
	observation, socketID, err := lifecycle.waitForComposite(ctx, socket, startedBefore, startedAfter, deadline, verified.executed)
	if err != nil {
		return nil, withStartupCustody(err, lifecycle.abortStartBefore(ctx, lifecycle.originalNonEscalatingCleanupCutoff(ctx)))
	}
	process := lifecycle.process
	lifecycle.socketID = socketID
	lifecycle.ownedEndpoint, lifecycle.ownedBootstrap = socket, observation.Status
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
	if err := lifecycle.expiryStarted(verified); err != nil {
		return nil, withStartupCustody(err, lifecycle.abortStartBefore(ctx, lifecycle.originalNonEscalatingCleanupCutoff(ctx)))
	}
	return lifecycle, nil
}

// Attach re-establishes supervision of a recorded, exact live sidecar after a
// supervisor crash. It never starts, replaces, or signals an ambiguous owner.
func Attach(ctx context.Context, config LifecycleConfig, recorded SidecarProvenance) (result *Lifecycle, resultErr error) {
	if !expiryAttachAllowed(recorded) {
		return nil, failure(FailureAdmissionMismatch, "attach", "expiry qualification cannot be attached", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := normalizeLifecycleConfig(&config); err != nil {
		return nil, err
	}
	verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: config.ExecutablePath, AdmissionRecordPath: config.AdmissionRecordPath, Admission: config.Admission})
	if err != nil {
		return nil, err
	}
	verifiedProvenance := verified.Provenance()
	verifiedCloseErr := verified.Close()
	defer func() { resultErr = withStartupCustody(resultErr, verifiedCloseErr) }()
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
	if verifiedCloseErr != nil { return nil, nil } // defer refuses close-only before pidfd/RPC
	pidfd, err := acquireRecordedProcess(config.processes, recorded.Process)
	if err != nil {
		return nil, err
	}
	observation, err := observeAttachedComposite(ctx, config, recorded)
	if err != nil {
		var closeErr error
		if pidfd >= 0 { closeErr = config.processes.closeFD(pidfd) }
		return nil, withStartupCustody(err, closeErr)
	}
	lifecycle := &Lifecycle{
		config: config, pidfd: pidfd, process: recorded.Process, attached: true,
		runtimeID: runtimeID, socketID: socketID,
		stdout: &boundedBuffer{maximum: maxDiagnosticBytes}, stderr: &boundedBuffer{maximum: maxDiagnosticBytes},
		processes: config.processes,
	}
	lifecycle.processImage, err = config.processes.executableIdentity(recorded.Process.PID)
	if err != nil || !sameProcessImage(lifecycle.processImage, recorded.Process) {
		var closeErr error
		if pidfd >= 0 { closeErr = config.processes.closeFD(pidfd) }
		return nil, withStartupCustody(failure(FailureOwnership, "attach", "recorded executable metadata differs", err), closeErr)
	}
	lifecycle.exit = monitorRecordedProcess(config.processes, pidfd, recorded.Process)
	lifecycle.ownedEndpoint, lifecycle.ownedBootstrap = recorded.ConfiguredSocket, observation.Status
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
		var closeErr error
		if pidfd >= 0 { closeErr = processes.closeFD(pidfd) }
		return -1, withStartupCustody(err, closeErr)
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
	if !l.legacyEvidenceAllowed() {
		return ProvenanceEvidenceV1{}, failure(FailureAdmissionMismatch, "evidence", "expiry qualification has no legacy admission evidence", nil)
	}
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

// ProductRestoreBindingV1 returns the copy-only database/owner identity that a
// read-only RestoreV1 must bind. It does not itself promote product readiness.
func (l *Lifecycle) ProductRestoreBindingV1() (journalcheckpoint.RestoreBindingV1, error) {
	work, err := l.beginExpiryWork()
	if err != nil {
		return journalcheckpoint.RestoreBindingV1{}, err
	}
	defer l.finishExpiryWork(work)
	return l.productRestoreBindingV1WithWork(work)
}

func (l *Lifecycle) productRestoreBindingV1WithWork(work *expiryLifecycleToken) (journalcheckpoint.RestoreBindingV1, error) {
	if l == nil {
		return journalcheckpoint.RestoreBindingV1{}, failure(FailureReadiness, "restore_binding", "lifecycle is absent", nil)
	}
	l.mu.Lock()
	if l.stopped || !l.provenance.Readiness.Admitted || !l.provenance.Readiness.Healthy || !l.provenance.Readiness.Configured {
		l.mu.Unlock()
		return journalcheckpoint.RestoreBindingV1{}, failure(FailureReadiness, "restore_binding", "sidecar is not admitted", nil)
	}
	provenance := l.provenance
	l.mu.Unlock()
	supervisor, err := currentExecutableIdentityWithReport(work.cleanupReport())
	if err != nil {
		return journalcheckpoint.RestoreBindingV1{}, err
	}
	return journalcheckpoint.RestoreBindingV1{
		DatabaseID:     provenance.DatabaseID,
		HostInstanceID: hostInstanceIDV1(supervisor, provenance.Process, provenance.DatabaseID),
	}, nil
}

// MarkProductRecovered is a one-way admission step. Only RestoreV1's sealed,
// mission-specific admission can promote readiness; generic journals and
// caller-built result structs have no compatible capability.
func (l *Lifecycle) MarkProductRecovered(admission journalcheckpoint.RestoreAdmissionV1) (ProductRestoreProofV1, error) {
	facts, err := admission.Facts()
	if err != nil {
		return ProductRestoreProofV1{}, failure(FailureReadiness, "admit_product", "restore admission capability is absent", err)
	}
	work, err := l.beginExpiryWork()
	if err != nil {
		return ProductRestoreProofV1{}, err
	}
	defer l.finishExpiryWork(work)
	binding, err := l.productRestoreBindingV1WithWork(work)
	if err != nil {
		return ProductRestoreProofV1{}, err
	}
	if facts.Binding != binding || facts.ObservedStoreRevision == 0 && !facts.Pristine ||
		facts.TerminalWatermark.ThroughRecordRevision > facts.ObservedStoreRevision ||
		facts.CanonicalProjectionDigest == [32]byte{} {
		return ProductRestoreProofV1{}, failure(FailureReadiness, "admit_product", "restore admission does not match the owned store", nil)
	}
	proof := newProductRestoreProofV1(facts)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped || !l.provenance.Readiness.Admitted || l.provenance.DatabaseID != binding.DatabaseID {
		return ProductRestoreProofV1{}, failure(FailureReadiness, "admit_product", "sidecar ownership changed during restore admission", nil)
	}
	l.provenance.Readiness.ProductRecovered = true
	l.provenance.Readiness.ReadyForProductRecovery = true
	if l.productRestores == nil {
		l.productRestores = make(map[string]ProductRestoreProofV1)
	}
	l.productRestores[string(facts.Source.ProjectID)+"\x00"+string(facts.Source.MissionID)] = proof
	return proof, nil
}

func (l *Lifecycle) waitForComposite(ctx context.Context, socket string, startedBefore, startedAfter, deadline time.Time, expected ExecutedImageProvenance) (CompositeObservation, fileIdentity, error) {
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
		if l.process.PID == 0 {
			if err := l.establishStartedProcess(expected); err != nil {
				return CompositeObservation{}, fileIdentity{}, err
			}
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

// establishStartedProcess is one ownership transition. The earlier sole actual
// Wait owner remains owned even when attribution fails; pending close custody
// reaches the shared FIRST abort. No partially attributed process is published.
func (l *Lifecycle) establishStartedProcess(expected ExecutedImageProvenance) error {
	// Only the sequential startup owner writes pendingCustody. Publish its
	// frozen rollback outcome even when attribution returns an error.
	defer func() { l.publishStartupPendingCustody(l.pendingCustody) }()
	process, pidfd, err := observeStartedProcessWithCustody(l.processes, l.command.Process.Pid, expected, l.expiryCleanupReport(), &l.pendingCustody)
	if err != nil {
		return err
	}
	image, err := l.processes.executableIdentity(l.command.Process.Pid)
	if err != nil || !sameProcessImage(image, process) {
		if pidfd >= 0 {
			closeErr := l.processes.closeFD(pidfd)
			l.expiryCleanupReport().supervisorClosed(closeErr)
			l.pendingCustody.record(closeErr)
		}
		return withStartupCustody(failure(FailureIdentityUnprovable, "process", "running executable metadata is unavailable", err), l.pendingCustody.supervisorCloseErr)
	}
	l.process, l.pidfd, l.processImage = process, pidfd, image
	return nil
}

func (l *Lifecycle) observeComposite(ctx context.Context, socket string, socketID fileIdentity, startedBefore, startedAfter time.Time) (CompositeObservation, error) {
	statusBefore, health, config, statusAfter, databases, err := observeAllSurfacesWithReport(ctx, l.config, socket, socketID, l.process, l.expiryCleanupReport())
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
	return observeAllSurfacesWithReport(ctx, config, socket, socketID, process, nil)
}

func observeAllSurfacesWithReport(ctx context.Context, config LifecycleConfig, socket string, socketID fileIdentity, process ProcessIdentity, report *expiryCleanupReport) (gapdbapi.StatusResult, gapdbapi.HealthResult, gapdbapi.ConfigResult, gapdbapi.StatusResult, []string, error) {
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
		database, err := observeStrictUnaryWithReport(ctx, config.processes, socket, socketID, process, value.operation, value.destination, config.StartTimeout, report)
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
	return observeStrictUnaryWithReport(ctx, processes, socket, socketID, process, operation, destination, timeout, nil)
}

func observeStrictUnaryWithReport(ctx context.Context, processes processAPI, socket string, socketID fileIdentity, process ProcessIdentity, operation string, destination any, timeout time.Duration, report *expiryCleanupReport) (database string, resultErr error) {
	return observeStrictUnaryWithClose(ctx, processes, socket, socketID, process, operation, destination, timeout, report, (*expiryCleanupReport).closeLocalConnection)
}
// Call-only fixed-close seam, never a factory/provider/configuration selector.
func observeStrictUnaryWithClose(ctx context.Context, processes processAPI, socket string, socketID fileIdentity, process ProcessIdentity, operation string, destination any, timeout time.Duration, report *expiryCleanupReport, closeCall func(*expiryCleanupReport, net.Conn) error) (database string, resultErr error) {
	if err := verifySocketPathIdentity(socket, socketID); err != nil {
		return "", failure(FailureOwnership, operation, "configured socket identity changed", err)
	}
	connection, err := (&net.Dialer{Timeout: min(timeout, time.Second)}).DialContext(ctx, "unix", socket)
	if err != nil {
		return "", failure(FailureProtocol, operation, "configured endpoint cannot be connected", err)
	}
	report.localOpened()
	var closeOnce sync.Once
	var closeErr error
	closeConnection := func() {
		closeOnce.Do(func() { closeErr = closeCall(report, connection) })
	}
	defer func() {
		closeConnection() // joins any running cancellation callback; never closes twice
		resultErr = withStartupCustody(resultErr, startupCleanupError("strict connection first close failed", closeErr))
		if resultErr != nil { database = "" }
	}()
	deadline := time.Now().Add(min(timeout, time.Second))
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return "", failure(FailureProtocol, operation, "strict connection deadline cannot be set", err)
	}
	stopCancel := context.AfterFunc(ctx, closeConnection)
	defer stopCancel()
	if err := ctx.Err(); err != nil {
		return "", failure(FailureProtocol, operation, "strict observation context ended", err)
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return "", failure(FailureProtocol, operation, "strict connection is not Unix", nil)
	}
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
	if err := unixConnection.CloseWrite(); err != nil {
		return "", failure(FailureProtocol, operation, "strict request stream cannot be ended", err)
	}
	encoded, err := readWireFrame(connection, storegapdb.MaxWireFrameBytes)
	if err != nil {
		return "", failure(FailureProtocol, operation, "strict response frame is incomplete", strictUnaryReadCause(ctx, err))
	}
	if err := strictRequireUnaryEOF(connection); err != nil {
		return "", failure(FailureProtocol, operation, "strict response stream did not end after one frame", strictUnaryReadCause(ctx, err))
	}
	if err := ctx.Err(); err != nil {
		return "", failure(FailureProtocol, operation, "strict observation context ended", err)
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
	if err := ctx.Err(); err != nil {
		return "", failure(FailureProtocol, operation, "strict observation context ended", err)
	}
	return response.DatabaseID, nil
}

// Preserve the actual interrupted read as primary. The cancellation callback can
// close the connection before the socket deadline reports a timeout; retain the
// independently observed context cause too, without inventing a timeout or EOF.
// Only the private Cause concrete type changes; primary text/identity/Is/As remain.
// Added wrapper storage/work is OWNED_UNQUALIFIED.
type strictUnaryContextReadError struct { primary, context error }
func (e *strictUnaryContextReadError) Error() string { return e.primary.Error() }
func (e *strictUnaryContextReadError) Unwrap() []error { return []error{e.primary, e.context} }
func strictUnaryReadCause(ctx context.Context, err error) error {
	if errors.Is(err, net.ErrClosed) {
		if contextErr := ctx.Err(); contextErr != nil {
			return &strictUnaryContextReadError{primary: err, context: contextErr}
		}
	}
	return err
}

// EOF ends only this response stream, not the peer process or its lifetime.
// A bounded blocking read shares the RPC deadline; silence is never admission.
func strictRequireUnaryEOF(reader io.Reader) error {
	var extra [1]byte
	n, err := reader.Read(extra[:])
	if n != 0 {
		return errors.Join(errors.New("extra byte after strict unary response"), err)
	}
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return io.ErrNoProgress
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
	return normalizeLifecycleConfigWithReport(config, nil)
}

func normalizeLifecycleConfigWithReport(config *LifecycleConfig, report *expiryCleanupReport) error {
	for _, field := range []struct{ name, value string }{
		{"executable", config.ExecutablePath}, {"admission", config.AdmissionRecordPath},
		{"runtime_root", config.RuntimeRoot}, {"data_directory", config.DataDirectory},
	} {
		if err := validateAbsolutePath(field.value); err != nil {
			return failure(FailureInvalidConfiguration, "configure", field.name+" path is invalid", err)
		}
	}
	if !isSchemaToken(config.SupervisorRunID, maxSchemaTokenBytes) {
		return failure(FailureInvalidConfiguration, "configure", "supervisor run identity is invalid", nil)
	}
	if !isLowerSHA256(config.ProductPolicySHA256) {
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
		if report != nil {
			config.filesystem = func(path string) (filesystemIdentity, error) {
				return statFilesystemWithReport(path, report)
			}
		}
	}
	if config.processes.startTicks == nil {
		config.processes = systemProcessAPI
		if report != nil {
			config.processes.startTicks = func(pid int) (uint64, error) {
				return processStartTicksWithReport(pid, report)
			}
			config.processes.executable = func(pid int) (fileIdentity, string, error) {
				return processExecutableWithReport(pid, report)
			}
			config.processes.executableIdentity = func(pid int) (fileIdentity, error) {
				return processExecutableIdentityWithReport(pid, report)
			}
		}
	} else if config.processes.executableIdentity == nil {
		config.processes.executableIdentity = systemProcessAPI.executableIdentity
		if report != nil {
			config.processes.executableIdentity = func(pid int) (fileIdentity, error) {
				return processExecutableIdentityWithReport(pid, report)
			}
		}
	}
	if err := validateStorageTopology(*config); err != nil {
		return failure(FailureUnsafePath, "configure", "runtime and data topology is not admitted", err)
	}
	return nil
}

func isSchemaToken(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum || !isASCIIAlphanumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !isASCIIAlphanumeric(character) && character != '.' && character != '_' && character != ':' && character != '-' {
			return false
		}
	}
	return true
}

func isASCIIAlphanumeric(character byte) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
}

func isLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func statFilesystem(path string) (filesystemIdentity, error) {
	return statFilesystemWithReport(path, nil)
}

func statFilesystemWithReport(path string, report *expiryCleanupReport) (result filesystemIdentity, resultErr error) {
	fd, err := openDirectoryNoFollow(path)
	if err != nil {
		return filesystemIdentity{}, err
	}
	report.localOpened()
	defer func() {
		resultErr = withStartupCustody(resultErr, report.closeLocalFD(fd))
		if resultErr != nil { result = filesystemIdentity{} }
	}()
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
	return validatePrivateRootWithReport(path, nil)
}

func validatePrivateRootWithReport(path string, report *expiryCleanupReport) (resultErr error) {
	if err := validatePathChain(path, false); err != nil {
		return err
	}
	fd, err := openDirectoryNoFollow(path)
	if err != nil {
		return errors.New("root must be an owner-only no-follow directory")
	}
	file := os.NewFile(uintptr(fd), "private-root")
	report.localOpened()
	defer func() { resultErr = withStartupCustody(resultErr, report.closeLocalFile(file)) }()
	identity, identityErr := identityOf(file)
	if identityErr != nil || identity.mode&uint32(syscall.S_IFMT) != uint32(syscall.S_IFDIR) || identity.mode&0o777 != 0o700 {
		return errors.New("root must be an owner-only directory")
	}
	return nil
}

func preparePrivateDirectory(path string) error {
	return preparePrivateDirectoryWithReport(path, nil)
}

func preparePrivateDirectoryWithReport(path string, report *expiryCleanupReport) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := validatePrivateRootWithReport(filepath.Dir(path), report); err != nil {
			return err
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return validatePrivateRootWithReport(path, report)
}

func observeStartedProcess(processes processAPI, pid int, executable ExecutedImageProvenance) (ProcessIdentity, int, error) {
	return observeStartedProcessWithReport(processes, pid, executable, nil)
}

func observeStartedProcessWithReport(processes processAPI, pid int, executable ExecutedImageProvenance, report *expiryCleanupReport) (ProcessIdentity, int, error) {
	return observeStartedProcessWithCustody(processes, pid, executable, report, nil)
}

func observeStartedProcessWithCustody(processes processAPI, pid int, executable ExecutedImageProvenance, report *expiryCleanupReport, pending *startupPendingCustody) (ProcessIdentity, int, error) {
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
	if pidfd >= 0 {
		report.supervisorOpened()
	}
	secondStart, startErr := processes.startTicks(pid)
	secondIdentity, secondDigest, executableErr := processes.executable(pid)
	if startErr != nil || executableErr != nil || secondStart != start ||
		secondIdentity.device != identity.device || secondIdentity.inode != identity.inode ||
		secondIdentity.size != identity.size || secondDigest != digest {
		var closeErr error
		if pidfd >= 0 {
			closeErr = processes.closeFD(pidfd)
			report.supervisorClosed(closeErr)
			pending.record(closeErr)
		}
		primary := failure(FailureIdentityUnprovable, "process", "process identity changed around pidfd acquisition", errors.Join(startErr, executableErr))
		return ProcessIdentity{}, -1, withStartupCustody(primary, startupCleanupError("attribution rollback supervisor close failed", closeErr))
	}
	return ProcessIdentity{PID: pid, StartTicks: start, Device: identity.device, Inode: identity.inode, Size: identity.size, SHA256: digest}, pidfd, nil
}

func processStartTicks(pid int) (uint64, error) {
	return processStartTicksWithReport(pid, nil)
}

func processStartTicksWithReport(pid int, report *expiryCleanupReport) (uint64, error) {
	file, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	return processStartTicksFileWithReport(file, report)
}

// Consumes the actual owned File, not a descriptor number. No closer is stored
// or selected by configuration; production supplies only this literal method.
func processStartTicksFileWithReport(file *os.File, report *expiryCleanupReport) (uint64, error) {
	return processStartTicksFileWithClose(file, report, (*expiryCleanupReport).closeLocalFile)
}

// Immediate call-only component seam. io.ReadAll replaces ReadFile's stat-sized
// buffering; allocation/read behavior is a new OWNED_UNQUALIFIED cost.
func processStartTicksFileWithClose(file *os.File, report *expiryCleanupReport, closeCall func(*expiryCleanupReport, *os.File) error) (ticks uint64, resultErr error) {
	report.localOpened()
	defer func() {
		resultErr = withStartupCustody(resultErr, closeCall(report, file))
		if resultErr != nil { ticks = 0 }
	}()
	encoded, err := io.ReadAll(file)
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
	return processExecutableWithReport(pid, nil)
}

func processExecutableWithReport(pid int, report *expiryCleanupReport) (result fileIdentity, digestResult string, resultErr error) {
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/exe", pid), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fileIdentity{}, "", err
	}
	file := os.NewFile(uintptr(fd), "process-executable")
	return processExecutableFileWithReport(file, report)
}
// Consumes an owned file; tests may supply a closed File object, never reclose
// a descriptor integer. Production supplies only its actual /proc open result.
func processExecutableFileWithReport(file *os.File, report *expiryCleanupReport) (result fileIdentity, digestResult string, resultErr error) {
	report.localOpened()
	defer func() {
		resultErr = withStartupCustody(resultErr, report.closeLocalFile(file))
		if resultErr != nil { result, digestResult = fileIdentity{}, "" }
	}()
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

func processExecutableIdentity(pid int) (fileIdentity, error) {
	return processExecutableIdentityWithReport(pid, nil)
}

func processExecutableIdentityWithReport(pid int, report *expiryCleanupReport) (result fileIdentity, resultErr error) {
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/exe", pid), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fileIdentity{}, err
	}
	file := os.NewFile(uintptr(fd), "process-executable-identity")
	report.localOpened()
	defer func() {
		resultErr = withStartupCustody(resultErr, report.closeLocalFile(file))
		if resultErr != nil { result = fileIdentity{} }
	}()
	identity, err := identityOf(file)
	if err != nil {
		return fileIdentity{}, err
	}
	if identity.size <= 0 || identity.size > 64<<20 {
		return fileIdentity{}, errors.New("process executable identity is outside bound")
	}
	return identity, nil
}

func sameProcessImage(identity fileIdentity, expected ProcessIdentity) bool {
	return identity.device == expected.Device && identity.inode == expected.Inode &&
		identity.size == expected.Size
}

func peerProcess(socket string, timeout time.Duration) (pid int, resultErr error) {
	connection, err := net.DialTimeout("unix", socket, min(timeout, time.Second))
	if err != nil {
		return 0, err
	}
	defer func() {
		resultErr = withStartupCustody(resultErr, connection.Close())
		if resultErr != nil { pid = 0 }
	}()
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
	work, err := l.beginExpiryWork()
	if err != nil {
		return OwnershipDiagnostic{State: OwnershipIndeterminate, Recovery: operatorRecovery()}
	}
	defer l.finishExpiryWork(work)
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
	identity, err := processes.executableIdentity(l.process.PID)
	if err != nil {
		diagnostic.State = OwnershipIndeterminate
		diagnostic.Recovery = operatorRecovery()
		return diagnostic
	}
	if !sameProcessImage(identity, l.process) {
		diagnostic.State = OwnershipForeignLive
		diagnostic.Recovery = operatorRecovery()
		return diagnostic
	}
	if l.processImage.nlink > 0 && identity.nlink == 0 {
		diagnostic.State = OwnershipForeignLive
		diagnostic.Recovery = operatorRecovery()
		return diagnostic
	}
	if identity != l.processImage {
		verifiedIdentity, digest, verifyErr := processes.executable(l.process.PID)
		if verifyErr != nil {
			diagnostic.State = OwnershipIndeterminate
			diagnostic.Recovery = operatorRecovery()
			return diagnostic
		}
		if verifiedIdentity != identity || !sameProcessImage(verifiedIdentity, l.process) || digest != l.process.SHA256 {
			diagnostic.State = OwnershipForeignLive
			diagnostic.Recovery = operatorRecovery()
			return diagnostic
		}
		l.processImage = identity
	}
	diagnostic.State = OwnershipOwnedLive
	if l.pidfd < 0 {
		diagnostic.Recovery = operatorRecovery()
	}
	return diagnostic
}

// RestartOwned replaces only an exactly attributed owned sidecar. Both modes
// preserve the configured durable data directory and admitted binary while
// Start creates a fresh private runtime directory, socket, and process
// provenance. Invalid requests are fully validated before the predecessor is
// signalled.
func (l *Lifecycle) RestartOwned(ctx context.Context, mode RestartMode, supervisorRunID string) (*Lifecycle, error) {
	if l == nil {
		return nil, failure(FailureOwnership, "restart", "lifecycle is absent", nil)
	}
	if !l.ordinaryRestartAllowed() {
		return nil, failure(FailureAdmissionMismatch, "restart", "expiry qualification requires its private generation transition", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	config := l.config
	predecessor := l.provenance
	l.mu.Unlock()
	if mode != RestartClean && mode != RestartUnclean {
		return nil, failure(FailureInvalidConfiguration, "restart", "restart mode is invalid", nil)
	}
	if supervisorRunID == predecessor.SupervisorRunID {
		return nil, failure(FailureInvalidConfiguration, "restart", "replacement supervisor run identity must be fresh", nil)
	}
	config.SupervisorRunID = supervisorRunID
	if err := normalizeLifecycleConfig(&config); err != nil {
		return nil, err
	}
	var err error
	if mode == RestartClean {
		err = l.Stop(ctx)
	} else {
		err = l.stopOwnedUnclean(ctx)
	}
	if err != nil {
		return nil, err
	}
	next, err := Start(ctx, config)
	if err != nil {
		return nil, err
	}
	fresh := next.Provenance()
	if fresh.DatabaseID != predecessor.DatabaseID || fresh.DataDirectory != predecessor.DataDirectory ||
		fresh.RuntimeDirectory == predecessor.RuntimeDirectory || fresh.ConfiguredSocket == predecessor.ConfiguredSocket ||
		fresh.Process == predecessor.Process || fresh.SupervisorRunID == predecessor.SupervisorRunID {
		closeErr := next.Stop(context.Background())
		return nil, withStartupCustody(failure(FailureOwnership, "restart", "replacement did not preserve data with a fresh owner lifetime", nil), closeErr)
	}
	return next, nil
}

func (l *Lifecycle) stopOwnedUnclean(ctx context.Context) (resultErr error) {
	result, won := l.firstCustodyBefore(ctx, l.originalNonEscalatingCleanupCutoff(ctx), true, l.stopOwnedUncleanFirst)
	if !won { return withStartupCustody(failure(FailureOwnership, "restart_unclean", "predecessor lifecycle is already stopped", nil), result) }
	return result
}

func (l *Lifecycle) stopOwnedUncleanFirst(ctx context.Context) (resultErr error) {
	defer func() { resultErr = l.shutdownTailCustody(ctx, resultErr) }()
	if err := l.lockCleanup(ctx); err != nil { return err }
	if l.stopped {
		l.mu.Unlock()
		return withStartupCustody(failure(FailureOwnership, "restart_unclean", "predecessor lifecycle is already stopped", nil), l.finishExitedShutdown(ctx, "restart_unclean"))
	}
	state := l.inspectOwnershipLocked().State
	if state != OwnershipOwnedLive && state != OwnershipOwnedExited && state != OwnershipStaleArtifact {
		l.mu.Unlock()
		return failure(FailureOwnership, "restart_unclean", "process ownership is not provable", nil)
	}
	if state == OwnershipOwnedLive {
		if err := l.signalLocked(unix.SIGKILL); err != nil {
			l.mu.Unlock()
			return failure(FailureOwnership, "restart_unclean", "owned process could not be terminated", err)
		}
	}
	l.mu.Unlock()
	// Includes already-exited entry; a poll/exit notification is never reap.
	return l.finishExitedShutdown(ctx, "restart_unclean")
}

// Stop is idempotent and signals only the revalidated exact process.
func (l *Lifecycle) Stop(ctx context.Context) (resultErr error) {
	result, _ := l.firstCustodyBefore(ctx, l.originalCleanupCutoff(ctx), true, l.stopFirst)
	return result
}

func (l *Lifecycle) stopFirst(ctx context.Context) (resultErr error) {
	defer func() { resultErr = l.shutdownTailCustody(ctx, resultErr) }()
	if err := l.lockCleanup(ctx); err != nil { return err }
	if l.stopped { l.mu.Unlock(); return l.finishExitedShutdown(ctx, "stop") }
	state := l.inspectOwnershipLocked().State
	if state == OwnershipOwnedExited || state == OwnershipStaleArtifact {
		l.mu.Unlock()
		return l.finishExitedShutdown(ctx, "stop")
	}
	if state != OwnershipOwnedLive {
		l.mu.Unlock()
		return failure(FailureOwnership, "stop", "process ownership is not provable", nil)
	}
	if err := l.signalLocked(unix.SIGTERM); err != nil {
		l.mu.Unlock()
		return failure(FailureOwnership, "stop", "owned process could not be signalled", err)
	}
	timeout := l.config.StopTimeout
	if timeout <= 0 { timeout = defaultStopTimeout }
	l.mu.Unlock()
	term, cancel := context.WithDeadline(ctx, intersectCleanupDeadline(ctx, time.Now().Add(timeout)))
	waitErr := l.observeShutdownCompletion(term)
	termExpired := term.Err() != nil
	cancel()
	completionObserved := l.shutdownWait.observed || (l.attached && l.attachExitObserved)
	if waitErr != nil && termExpired && ctx.Err() == nil && !completionObserved {
		if err := l.lockCleanup(ctx); err != nil { return withStartupCustody(waitErr, err) }
		state := l.inspectOwnershipLocked().State
		if state == OwnershipOwnedLive {
			if err := l.signalLocked(unix.SIGKILL); err != nil {
				l.mu.Unlock()
				return failure(FailureOwnership, "stop", "owned process could not be terminated", err)
			}
		} else if state != OwnershipOwnedExited && state != OwnershipStaleArtifact {
			l.mu.Unlock()
			return failure(FailureOwnership, "stop", "ownership changed during shutdown", nil)
		}
		l.mu.Unlock()
		// Original escalation cap intersects the SAME applicable cleanup cutoff.
		escalation, done := context.WithDeadline(ctx, intersectCleanupDeadline(ctx, time.Now().Add(time.Second)))
		waitErr = l.observeShutdownCompletion(escalation)
		done()
	}
	return l.finishShutdownAfterObservation(ctx, "stop", waitErr)
}
func (l *Lifecycle) finishExitedShutdown(ctx context.Context, operation string) error {
	return l.finishShutdownAfterObservation(ctx, operation, l.observeShutdownCompletion(ctx))
}
func (l *Lifecycle) finishShutdownAfterObservation(ctx context.Context, operation string, waitErr error) error {
	if err := l.lockCleanup(ctx); err != nil { return withStartupCustody(waitErr, err) }
	state := l.inspectOwnershipLocked().State
	var identityErr error
	if state != OwnershipOwnedExited && state != OwnershipStaleArtifact {
		identityErr = failure(FailureOwnership, operation, "ownership changed or exit unproven; runtime preserved", nil)
	}
	l.stopped = true
	closeErr := l.closePidfd() // safe owned descriptor release even after Wait error
	l.mu.Unlock()
	custody := withStartupCustody(waitErr, withStartupCustody(identityErr, closeErr))
	if custody != nil { return custody } // no unknown artifact deletion
	if err := ctx.Err(); err != nil { return startupCleanupError("cleanup cutoff elapsed; runtime preserved", err) }
	return l.cleanupRuntimeBefore(ctx, true)
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
	ctx := context.Background()
	result, won := l.firstCustodyBefore(ctx, l.originalNonEscalatingCleanupCutoff(ctx), true, l.cleanupStaleBefore)
	if !won { return l.expiryStaleCustodyResult(result) }
	return result
}

func (l *Lifecycle) cleanupStaleFirst() error {
	ctx, cancel := context.WithDeadline(context.Background(), l.originalNonEscalatingCleanupCutoff(context.Background()))
	defer cancel()
	return l.cleanupStaleBefore(ctx)
}
func (l *Lifecycle) cleanupStaleBefore(ctx context.Context) (resultErr error) {
	defer func() { resultErr = l.shutdownTailCustody(ctx, resultErr) }()
	if err := l.lockCleanup(ctx); err != nil { return err }
	state := l.inspectOwnershipLocked().State
	l.mu.Unlock()
	if state != OwnershipStaleArtifact && state != OwnershipOwnedExited {
		return failure(FailureCleanup, "cleanup_stale", "lifecycle is not a proven exited owner", nil)
	}
	return l.finishExitedShutdown(ctx, "cleanup_stale")
}

func (l *Lifecycle) signalLocked(signal unix.Signal) error {
	processes := l.hostProcesses()
	if l.pidfd >= 0 {
		return processes.pidfdSignal(l.pidfd, signal, nil, 0)
	}
	return errors.New("pidfd signaling is unavailable; process is left untouched")
}

func (l *Lifecycle) closePidfd() error {
	if l.pidfdCloseAttempted {
		return l.pidfdCloseErr
	}
	if l.pidfd >= 0 {
		l.pidfdCloseAttempted = true
		fd := l.pidfd
		l.pidfd = -1 // never retry a descriptor number after FIRST failure
		err := l.hostProcesses().closeFD(fd)
		l.expiryCleanupReport().supervisorClosed(err)
		l.pidfdCloseErr = startupCleanupError("first supervisor pidfd close failed", err)
	}
	return l.pidfdCloseErr
}

func (l *Lifecycle) hostProcesses() processAPI {
	if l.processes.startTicks == nil {
		return systemProcessAPI
	}
	processes := l.processes
	if processes.executableIdentity == nil && processes.executable != nil {
		processes.executableIdentity = func(pid int) (fileIdentity, error) {
			identity, _, err := processes.executable(pid)
			return identity, err
		}
	}
	return processes
}

func (l *Lifecycle) abortStart() error {
	ctx := context.Background()
	return l.abortStartBefore(ctx, l.originalNonEscalatingCleanupCutoff(ctx))
}

func (l *Lifecycle) abortStartBefore(ctx context.Context, deadline time.Time) error {
	return l.abortStartBeforeWithKill(ctx, deadline, (*os.Process).Kill)
}

// Private call-only component seam. No child handle or callback is installed
// through configuration; production supplies only the literal method above.
func (l *Lifecycle) abortStartBeforeWithKill(ctx context.Context, deadline time.Time, killChild func(*os.Process) error) error {
	result, _ := l.firstCustodyBefore(ctx, deadline, false, func(cleanup context.Context) error {
		l.startupAbort = l.abortStartFirst(cleanup, deadline, killChild)
		return l.startupAbort.result
	})
	return result
}

func (l *Lifecycle) cleanupRuntimeFirst(requireSocketIdentity bool) (resultErr error) {
	return l.cleanupRuntimeWithCloses(requireSocketIdentity, (*expiryCleanupReport).closeLocalFD, (*expiryCleanupReport).closeLocalFile)
}
func (l *Lifecycle) cleanupRuntimeBefore(ctx context.Context, required bool) error {
	return l.cleanupRuntimeWithCall(required, func(required bool) error {
		return l.cleanupRuntimeWithClosesBefore(ctx, required, (*expiryCleanupReport).closeLocalFD, (*expiryCleanupReport).closeLocalFile)
	})
}

// Call-only close seams; production passes fixed report methods, never a field.
func (l *Lifecycle) cleanupRuntimeWithCloses(requireSocketIdentity bool, closeFD func(*expiryCleanupReport, int) error, closeFile func(*expiryCleanupReport, *os.File) error) (resultErr error) {
	return l.cleanupRuntimeWithClosesBefore(context.Background(), requireSocketIdentity, closeFD, closeFile)
}
func (l *Lifecycle) cleanupRuntimeWithClosesBefore(ctx context.Context, requireSocketIdentity bool, closeFD func(*expiryCleanupReport, int) error, closeFile func(*expiryCleanupReport, *os.File) error) (resultErr error) {
	if err := ctx.Err(); err != nil { return startupCleanupError("runtime cleanup cutoff elapsed; artifacts preserved", err) }
	runtimePath := l.provenance.RuntimeDirectory
	if runtimePath == "" {
		return nil
	}
	parentFD, err := openDirectoryNoFollow(filepath.Dir(runtimePath))
	if err != nil {
		return failure(FailureCleanup, "cleanup", "runtime parent cannot be opened without following links", err)
	}
	l.expiryCleanupReport().localOpened()
	defer func() {
		resultErr = withStartupCustody(resultErr, startupCleanupError("runtime parent first close failed", closeFD(l.expiryCleanupReport(), parentFD)))
	}()
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
	l.expiryCleanupReport().localOpened()
	defer func() {
		resultErr = withStartupCustody(resultErr, startupCleanupError("runtime directory first close failed", closeFile(l.expiryCleanupReport(), runtimeFile)))
	}()
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
		if current.device != l.socketID.device || current.inode != l.socketID.inode {
			return failure(FailureCleanup, "cleanup", "socket is no longer attributable", nil)
		}
		if err := ctx.Err(); err != nil { return startupCleanupError("socket cleanup cutoff elapsed; artifacts preserved", err) }
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
	if err := ctx.Err(); err != nil { return startupCleanupError("directory cleanup cutoff elapsed; artifacts preserved", err) }
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
