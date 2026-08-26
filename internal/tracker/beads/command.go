package beads

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultReadTimeout     = 10 * time.Second
	defaultMutationTimeout = 30 * time.Second
	defaultStdoutLimit     = int64(4 << 20)
	defaultStderrLimit     = int64(1 << 20)
	defaultKillGrace       = 250 * time.Millisecond
)

// CommandKind is the closed vocabulary supported by the v1.2.2 automation
// seam. It is evidence metadata, never an arbitrary executable operation.
type CommandKind string

const (
	CommandVersion        CommandKind = "version"
	CommandWhere          CommandKind = "where"
	CommandInfo           CommandKind = "info"
	CommandDoctor         CommandKind = "doctor"
	CommandShow           CommandKind = "show"
	CommandList           CommandKind = "list"
	CommandReady          CommandKind = "ready"
	CommandCreate         CommandKind = "create"
	CommandUpdate         CommandKind = "update"
	CommandAtomicClaim    CommandKind = "atomic_claim"
	CommandDependencyAdd  CommandKind = "dependency_add"
	CommandDependencyList CommandKind = "dependency_list"
	CommandCommentAppend  CommandKind = "comment_append"
	CommandCommentList    CommandKind = "comment_list"
	CommandExport         CommandKind = "export"
	CommandBackupInit     CommandKind = "backup_init"
	CommandBackupSync     CommandKind = "backup_sync"
	CommandBackupStatus   CommandKind = "backup_status"
	CommandBackupRestore  CommandKind = "backup_restore"
)

type expectedShape uint8

const (
	expectedObject expectedShape = iota + 1
	expectedArray
	expectedJSONL
	expectedEmpty
)

type commandBinding struct {
	executable  string
	workspace   string
	workingDir  string
	timeout     time.Duration
	stdoutLimit int64
	stderrLimit int64
	credentials map[string]string
}

func defaultBinding(executable, workspace, workingDir string) commandBinding {
	return commandBinding{
		executable: executable, workspace: workspace, workingDir: workingDir,
		stdoutLimit: defaultStdoutLimit, stderrLimit: defaultStderrLimit,
	}
}

type command struct {
	kind             CommandKind
	binding          commandBinding
	argv             []string
	shape            expectedShape
	mutating         bool
	explicitDeadline bool
}

func newVersionCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandVersion, binding, []string{"version", "--json"}, expectedObject, false, false)
}

func newWhereCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandWhere, binding, []string{"where", "--json"}, expectedObject, false, false)
}

func newInfoCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandInfo, binding, []string{"info", "--schema", "--json"}, expectedObject, false, false)
}

func newDoctorCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandDoctor, binding, []string{"doctor", "--agent", "--json"}, expectedObject, false, true)
}

func newShowCommand(binding commandBinding, id string) (command, error) {
	if err := requiredValue("work id", id); err != nil {
		return command{}, err
	}
	return buildCommand(CommandShow, binding, []string{"show", id, "--json"}, expectedObject, false, false)
}

func newListCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandList, binding, []string{"list", "--json"}, expectedArray, false, false)
}

func newReadyCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandReady, binding, []string{"ready", "--json"}, expectedArray, false, false)
}

type createValues struct{ title, description, acceptance, metadata string }

func newCreateCommand(binding commandBinding, values createValues) (command, error) {
	if err := requiredValue("title", values.title); err != nil {
		return command{}, err
	}
	argv := []string{"create", "--title", values.title}
	if values.description != "" {
		argv = append(argv, "--description", values.description)
	}
	if values.acceptance != "" {
		argv = append(argv, "--acceptance", values.acceptance)
	}
	if values.metadata != "" {
		argv = append(argv, "--metadata", values.metadata)
	}
	argv = append(argv, "--json")
	return buildCommand(CommandCreate, binding, argv, expectedObject, true, false)
}

type updateValues struct{ id, title, description, acceptance, metadata, assignee string }

func newUpdateCommand(binding commandBinding, values updateValues) (command, error) {
	if err := requiredValue("work id", values.id); err != nil {
		return command{}, err
	}
	argv := []string{"update", values.id}
	if values.title != "" {
		argv = append(argv, "--title", values.title)
	}
	if values.description != "" {
		argv = append(argv, "--description", values.description)
	}
	if values.acceptance != "" {
		argv = append(argv, "--acceptance", values.acceptance)
	}
	if values.metadata != "" {
		argv = append(argv, "--metadata", values.metadata)
	}
	if values.assignee != "" {
		argv = append(argv, "--assignee", values.assignee)
	}
	if len(argv) == 2 {
		return command{}, validationError("update requires at least one changed field")
	}
	argv = append(argv, "--json")
	return buildCommand(CommandUpdate, binding, argv, expectedObject, true, false)
}

func newAtomicClaimCommand(binding commandBinding, id string) (command, error) {
	if err := requiredValue("work id", id); err != nil {
		return command{}, err
	}
	return buildCommand(CommandAtomicClaim, binding, []string{"update", id, "--claim", "--json"}, expectedObject, true, false)
}

func newDependencyAddCommand(binding commandBinding, blocked, blocker string) (command, error) {
	if err := requiredValue("blocked work id", blocked); err != nil {
		return command{}, err
	}
	if err := requiredValue("blocking work id", blocker); err != nil {
		return command{}, err
	}
	if blocked == blocker {
		return command{}, validationError("dependency cannot reference the same work twice")
	}
	return buildCommand(CommandDependencyAdd, binding, []string{"dep", "add", blocked, blocker, "--type", "blocks", "--json"}, expectedObject, true, false)
}

func newDependencyListCommand(binding commandBinding, id string) (command, error) {
	if err := requiredValue("work id", id); err != nil {
		return command{}, err
	}
	return buildCommand(CommandDependencyList, binding, []string{"dep", "list", id, "--json"}, expectedArray, false, false)
}

func newCommentAppendCommand(binding commandBinding, id, body string) (command, error) {
	if err := requiredValue("work id", id); err != nil {
		return command{}, err
	}
	if err := requiredValue("comment body", body); err != nil {
		return command{}, err
	}
	return buildCommand(CommandCommentAppend, binding, []string{"comments", "add", id, body, "--json"}, expectedObject, true, false)
}

func newCommentListCommand(binding commandBinding, id string) (command, error) {
	if err := requiredValue("work id", id); err != nil {
		return command{}, err
	}
	return buildCommand(CommandCommentList, binding, []string{"comments", id, "--json"}, expectedArray, false, false)
}

func newExportCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandExport, binding, []string{"export", "--json"}, expectedJSONL, false, true)
}

func newBackupInitCommand(binding commandBinding, destination string) (command, error) {
	if !filepath.IsAbs(destination) {
		return command{}, validationError("backup destination must be absolute")
	}
	return buildCommand(CommandBackupInit, binding, []string{"backup", "init", filepath.Clean(destination)}, expectedEmpty, true, true)
}

func newBackupSyncCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandBackupSync, binding, []string{"backup", "sync"}, expectedEmpty, true, true)
}

func newBackupStatusCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandBackupStatus, binding, []string{"backup", "status"}, expectedEmpty, false, true)
}

func newBackupRestoreCommand(binding commandBinding, source string) (command, error) {
	if !filepath.IsAbs(source) {
		return command{}, validationError("backup source must be absolute")
	}
	return buildCommand(CommandBackupRestore, binding, []string{"backup", "restore", filepath.Clean(source)}, expectedEmpty, true, true)
}

func buildCommand(kind CommandKind, binding commandBinding, argv []string, shape expectedShape, mutating, explicitDeadline bool) (command, error) {
	cmd := command{kind: kind, binding: binding, argv: append([]string(nil), argv...), shape: shape, mutating: mutating, explicitDeadline: explicitDeadline}
	if err := cmd.validate(); err != nil {
		return command{}, err
	}
	return cmd, nil
}

func requiredValue(name, value string) error {
	if strings.TrimSpace(value) == "" || strings.IndexByte(value, 0) >= 0 {
		return validationError(name + " is required and must not contain NUL")
	}
	return nil
}

func validationError(message string) error {
	return &commandFailure{class: failureValidation, message: message}
}

func (cmd *command) validate() error {
	if cmd == nil {
		return validationError("command is missing")
	}
	if !filepath.IsAbs(cmd.binding.executable) || filepath.Clean(cmd.binding.executable) != cmd.binding.executable {
		return validationError("executable path must be absolute and clean")
	}
	if !filepath.IsAbs(cmd.binding.workspace) || filepath.Clean(cmd.binding.workspace) != cmd.binding.workspace {
		return validationError("workspace path must be absolute and clean")
	}
	if !filepath.IsAbs(cmd.binding.workingDir) || filepath.Clean(cmd.binding.workingDir) != cmd.binding.workingDir {
		return validationError("working directory must be absolute and clean")
	}
	if cmd.binding.stdoutLimit == 0 {
		cmd.binding.stdoutLimit = defaultStdoutLimit
	}
	if cmd.binding.stderrLimit == 0 {
		cmd.binding.stderrLimit = defaultStderrLimit
	}
	if cmd.binding.stdoutLimit < 0 || cmd.binding.stderrLimit < 0 {
		return validationError("output limits must be positive")
	}
	if cmd.binding.timeout == 0 {
		if cmd.explicitDeadline {
			return validationError("command requires an explicit bounded deadline")
		}
		if cmd.mutating {
			cmd.binding.timeout = defaultMutationTimeout
		} else {
			cmd.binding.timeout = defaultReadTimeout
		}
	}
	if cmd.binding.timeout < 0 || cmd.binding.timeout > defaultMutationTimeout {
		return validationError("timeout must be positive and no greater than 30 seconds")
	}
	if len(cmd.argv) == 0 {
		return validationError("command arguments are missing")
	}
	for _, arg := range cmd.argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return validationError("command argument contains NUL")
		}
	}
	if cmd.shape < expectedObject || cmd.shape > expectedEmpty {
		return validationError("unsupported output shape")
	}
	contract, ok := commandContractFor(cmd.kind)
	if !ok {
		return validationError("unsupported command kind")
	}
	if cmd.shape != contract.shape || cmd.mutating != contract.mutating || cmd.explicitDeadline != contract.explicitDeadline {
		return validationError("command metadata is inconsistent with its kind")
	}
	hasJSON := false
	for _, arg := range cmd.argv {
		if arg == "--json" {
			hasJSON = true
		}
	}
	if hasJSON != contract.json {
		return validationError("command JSON flag is inconsistent with its documented protocol")
	}
	for key, value := range cmd.binding.credentials {
		if !allowedCredential(key) || strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return validationError("credential environment is invalid")
		}
	}
	return nil
}

type commandContract struct {
	shape            expectedShape
	mutating         bool
	explicitDeadline bool
	json             bool
}

func commandContractFor(kind CommandKind) (commandContract, bool) {
	switch kind {
	case CommandVersion, CommandWhere, CommandInfo, CommandShow:
		return commandContract{shape: expectedObject, json: true}, true
	case CommandDoctor:
		return commandContract{shape: expectedObject, explicitDeadline: true, json: true}, true
	case CommandList, CommandReady, CommandDependencyList, CommandCommentList:
		return commandContract{shape: expectedArray, json: true}, true
	case CommandCreate, CommandUpdate, CommandAtomicClaim, CommandDependencyAdd, CommandCommentAppend:
		return commandContract{shape: expectedObject, mutating: true, json: true}, true
	case CommandExport:
		return commandContract{shape: expectedJSONL, explicitDeadline: true, json: true}, true
	case CommandBackupInit, CommandBackupSync, CommandBackupRestore:
		return commandContract{shape: expectedEmpty, mutating: true, explicitDeadline: true}, true
	case CommandBackupStatus:
		return commandContract{shape: expectedEmpty, explicitDeadline: true}, true
	default:
		return commandContract{}, false
	}
}

func (cmd command) renderedArgv() []string { return append([]string(nil), cmd.argv...) }

type mutationOutcome string

const (
	mutationNotStarted mutationOutcome = "not_started"
	mutationNotApplied mutationOutcome = "not_applied"
	mutationApplied    mutationOutcome = "applied"
	mutationAmbiguous  mutationOutcome = "ambiguous"
	mutationUnknown    mutationOutcome = "unknown"
)

type commandResult struct {
	kind            CommandKind
	exitCode        *int
	duration        time.Duration
	started         bool
	signaled        bool
	stdout          []byte
	stderr          []byte
	stdoutTruncated bool
	stderrTruncated bool
	cancelCause     error
	mutationOutcome mutationOutcome
}

type commandRunner interface {
	run(context.Context, command) commandResult
}

type processRunner struct{ killGrace time.Duration }

func newProcessRunner() *processRunner { return &processRunner{killGrace: defaultKillGrace} }

func (runner *processRunner) run(parent context.Context, request command) commandResult {
	startedAt := time.Now()
	result := commandResult{kind: request.kind, mutationOutcome: mutationUnknown}
	if err := request.validate(); err != nil {
		result.duration = time.Since(startedAt)
		result.cancelCause = err
		if request.mutating {
			result.mutationOutcome = mutationNotStarted
		}
		return result
	}
	if err := parent.Err(); err != nil {
		result.duration = time.Since(startedAt)
		result.cancelCause = err
		if request.mutating {
			result.mutationOutcome = mutationNotStarted
		}
		return result
	}
	ctx, cancel := context.WithTimeout(parent, request.binding.timeout)
	defer cancel()

	stdout := newBoundedWriter(request.binding.stdoutLimit)
	stderr := newBoundedWriter(request.binding.stderrLimit)
	cmd := exec.Command(request.binding.executable, request.argv...)
	cmd.Dir = request.binding.workingDir
	cmd.Env = childEnvironment(request.binding)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		result.duration = time.Since(startedAt)
		result.cancelCause = &commandFailure{class: failureSpawn, message: "provider process did not start", cause: err}
		if request.mutating {
			result.mutationOutcome = mutationNotStarted
		}
		return result
	}
	result.started = true
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-waited:
	case <-ctx.Done():
		result.cancelCause = ctx.Err()
		terminateProcessGroup(cmd.Process.Pid, runner.grace())
		waitErr = <-waited
	}
	result.duration = time.Since(startedAt)
	result.stdout, result.stdoutTruncated = stdout.snapshot()
	result.stderr, result.stderrTruncated = stderr.snapshot()
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		result.exitCode = &code
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			result.signaled = status.Signaled()
		}
	}
	if result.cancelCause == nil && waitErr != nil {
		result.cancelCause = &commandFailure{class: failureExit, message: "provider process exited unsuccessfully", cause: waitErr}
	}
	if result.stdoutTruncated || result.stderrTruncated {
		if result.cancelCause == nil {
			result.cancelCause = &commandFailure{class: failureOverflow, message: "provider output exceeded its bound"}
		}
	}
	if request.mutating && (result.cancelCause != nil || result.stdoutTruncated || result.stderrTruncated) {
		result.mutationOutcome = mutationAmbiguous
	}
	return result
}

func (runner *processRunner) grace() time.Duration {
	if runner != nil && runner.killGrace > 0 {
		return runner.killGrace
	}
	return defaultKillGrace
}

func terminateProcessGroup(pid int, grace time.Duration) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	<-timer.C
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

var baseEnvironmentNames = []string{"LANG", "LC_ALL", "SSL_CERT_DIR", "SSL_CERT_FILE", "TMPDIR", "TZ"}

var credentialEnvironmentNames = map[string]struct{}{
	"DOLT_CREDENTIALS_PATH": {}, "DOLT_REMOTE_PASSWORD": {}, "DOLT_REMOTE_USER": {},
	"DOLT_SERVER_HOST": {}, "DOLT_SERVER_PORT": {},
}

func allowedCredential(name string) bool { _, ok := credentialEnvironmentNames[name]; return ok }

func childEnvironment(binding commandBinding) []string {
	values := make(map[string]string, len(baseEnvironmentNames)+len(binding.credentials)+3)
	for _, name := range baseEnvironmentNames {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}
	for name, value := range binding.credentials {
		values[name] = value
	}
	values["BEADS_DIR"] = binding.workspace
	values["BD_JSON_ENVELOPE"] = "1"
	values["BD_NON_INTERACTIVE"] = "1"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}

type boundedWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int64
	overflow bool
}

func newBoundedWriter(limit int64) *boundedWriter { return &boundedWriter{limit: limit} }

func (writer *boundedWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	remaining := writer.limit - int64(writer.buf.Len())
	if remaining > 0 {
		keep := int64(len(payload))
		if keep > remaining {
			keep = remaining
		}
		_, _ = writer.buf.Write(payload[:int(keep)])
	}
	if int64(len(payload)) > remaining {
		writer.overflow = true
	}
	return len(payload), nil
}

func (writer *boundedWriter) snapshot() ([]byte, bool) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return append([]byte(nil), writer.buf.Bytes()...), writer.overflow
}

func isContextFailure(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
