package beads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
	CommandVersion          CommandKind = "version"
	CommandWhere            CommandKind = "where"
	CommandDoltShow         CommandKind = "dolt_show"
	CommandDoltStatus       CommandKind = "dolt_status"
	CommandShow             CommandKind = "show"
	CommandList             CommandKind = "list"
	CommandReady            CommandKind = "ready"
	CommandCreate           CommandKind = "create"
	CommandUpdate           CommandKind = "update"
	CommandAtomicClaim      CommandKind = "atomic_claim"
	CommandDependencyAdd    CommandKind = "dependency_add"
	CommandDependencyRemove CommandKind = "dependency_remove"
	CommandDependencyList   CommandKind = "dependency_list"
	CommandCommentAppend    CommandKind = "comment_append"
	CommandCommentList      CommandKind = "comment_list"
	CommandExport           CommandKind = "export"
	CommandBackupInit       CommandKind = "backup_init"
	CommandBackupSync       CommandKind = "backup_sync"
	CommandBackupStatus     CommandKind = "backup_status"
	CommandBackupRestore    CommandKind = "backup_restore"
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
	argvSeal         [sha256.Size]byte
}

func newVersionCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandVersion, binding, []string{"version", "--json"}, expectedObject, false, false)
}

func newWhereCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandWhere, binding, []string{"where", "--json"}, expectedObject, false, false)
}

func newDoltShowCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandDoltShow, binding, []string{"dolt", "show", "--json"}, expectedObject, false, false)
}

func newDoltStatusCommand(binding commandBinding) (command, error) {
	return buildCommand(CommandDoltStatus, binding, []string{"dolt", "status", "--json"}, expectedObject, false, false)
}

func newShowCommand(binding commandBinding, id string) (command, error) {
	if err := requiredExternalID("work id", id); err != nil {
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

type updateValue struct {
	value string
	set   bool
}

func presentUpdateValue(value string) updateValue { return updateValue{value: value, set: true} }

type updateValues struct {
	id                                       string
	title, description, acceptance, metadata updateValue
	assignee                                 updateValue
}

func newUpdateCommand(binding commandBinding, values updateValues) (command, error) {
	if err := requiredExternalID("work id", values.id); err != nil {
		return command{}, err
	}
	argv := []string{"update", values.id}
	if values.title.set {
		argv = append(argv, "--title", values.title.value)
	}
	if values.description.set {
		argv = append(argv, "--description", values.description.value)
	}
	if values.acceptance.set {
		argv = append(argv, "--acceptance", values.acceptance.value)
	}
	if values.metadata.set {
		argv = append(argv, "--metadata", values.metadata.value)
	}
	if values.assignee.set {
		argv = append(argv, "--assignee", values.assignee.value)
	}
	if len(argv) == 2 {
		return command{}, validationError("update requires at least one changed field")
	}
	argv = append(argv, "--json")
	return buildCommand(CommandUpdate, binding, argv, expectedObject, true, false)
}

func newAtomicClaimCommand(binding commandBinding, id string) (command, error) {
	if err := requiredExternalID("work id", id); err != nil {
		return command{}, err
	}
	return buildCommand(CommandAtomicClaim, binding, []string{"update", id, "--claim", "--json"}, expectedObject, true, false)
}

func newDependencyAddCommand(binding commandBinding, blocked, blocker string) (command, error) {
	if err := requiredExternalID("blocked work id", blocked); err != nil {
		return command{}, err
	}
	if err := requiredExternalID("blocking work id", blocker); err != nil {
		return command{}, err
	}
	if blocked == blocker {
		return command{}, validationError("dependency cannot reference the same work twice")
	}
	return buildCommand(CommandDependencyAdd, binding, []string{"dep", "add", blocked, blocker, "--type", "blocks", "--json"}, expectedObject, true, false)
}

func newDependencyListCommand(binding commandBinding, id string) (command, error) {
	if err := requiredExternalID("work id", id); err != nil {
		return command{}, err
	}
	return buildCommand(CommandDependencyList, binding, []string{"dep", "list", id, "--json"}, expectedArray, false, false)
}

func newDependencyRemoveCommand(binding commandBinding, blocked, blocker string) (command, error) {
	if err := requiredExternalID("blocked work id", blocked); err != nil {
		return command{}, err
	}
	if err := requiredExternalID("blocking work id", blocker); err != nil {
		return command{}, err
	}
	if blocked == blocker {
		return command{}, validationError("dependency cannot reference the same work twice")
	}
	return buildCommand(CommandDependencyRemove, binding, []string{"dep", "remove", blocked, blocker, "--json"}, expectedObject, true, false)
}

func newCommentAppendCommand(binding commandBinding, id, body string) (command, error) {
	if err := requiredExternalID("work id", id); err != nil {
		return command{}, err
	}
	if err := requiredValue("comment body", body); err != nil {
		return command{}, err
	}
	return buildCommand(CommandCommentAppend, binding, []string{"comments", "add", id, body, "--json"}, expectedObject, true, false)
}

func newCommentListCommand(binding commandBinding, id string) (command, error) {
	if err := requiredExternalID("work id", id); err != nil {
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
	if err := cmd.validateStructure(); err != nil {
		return command{}, err
	}
	cmd.argvSeal = sealArgv(cmd)
	return cmd, nil
}

func requiredValue(name, value string) error {
	if strings.TrimSpace(value) == "" || strings.IndexByte(value, 0) >= 0 || strings.HasPrefix(value, "-") {
		return validationError(name + " is required and must not contain NUL or begin with an option prefix")
	}
	return nil
}

func requiredExternalID(name, value string) error {
	if len(value) == 0 || len(value) > 256 || !isASCIIAlphanumeric(value[0]) {
		return validationError(name + " must use the bounded provider identifier grammar")
	}
	for index := 1; index < len(value); index++ {
		current := value[index]
		if !isASCIIAlphanumeric(current) && current != '-' && current != '_' && current != '.' {
			return validationError(name + " must use the bounded provider identifier grammar")
		}
	}
	return nil
}

func isASCIIAlphanumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func validationError(message string) error {
	return &commandFailure{class: failureValidation, message: message}
}

func (cmd *command) validate() error {
	if err := cmd.validateStructure(); err != nil {
		return err
	}
	if cmd.argvSeal != sealArgv(*cmd) {
		return validationError("command arguments changed after typed construction")
	}
	return nil
}

func (cmd *command) validateStructure() error {
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
	if err := validateArgvShape(cmd.kind, cmd.argv); err != nil {
		return err
	}
	for key, value := range cmd.binding.credentials {
		if !allowedCredential(key) || strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return validationError("credential environment is invalid")
		}
	}
	return nil
}

func sealArgv(cmd command) [sha256.Size]byte {
	parts := make([]string, 0, len(cmd.argv)+4)
	parts = append(parts, string(cmd.kind), strconv.Itoa(int(cmd.shape)), strconv.FormatBool(cmd.mutating), strconv.FormatBool(cmd.explicitDeadline))
	parts = append(parts, cmd.argv...)
	return sha256.Sum256([]byte(strings.Join(parts, "\x00")))
}

func validateArgvShape(kind CommandKind, argv []string) error {
	fixed := func(want ...string) error {
		if len(argv) != len(want) {
			return validationError("command arguments do not match the typed command shape")
		}
		for index := range want {
			if argv[index] != want[index] {
				return validationError("command arguments do not match the typed command shape")
			}
		}
		return nil
	}
	switch kind {
	case CommandVersion:
		return fixed("version", "--json")
	case CommandWhere:
		return fixed("where", "--json")
	case CommandDoltShow:
		return fixed("dolt", "show", "--json")
	case CommandDoltStatus:
		return fixed("dolt", "status", "--json")
	case CommandList:
		return fixed("list", "--json")
	case CommandReady:
		return fixed("ready", "--json")
	case CommandShow:
		if len(argv) != 3 || argv[0] != "show" || argv[2] != "--json" {
			return validationError("show arguments do not match the typed command shape")
		}
		return requiredExternalID("work id", argv[1])
	case CommandCreate:
		return validateOptionCommand(argv, "create", 1, []string{"--title", "--description", "--acceptance", "--metadata"})
	case CommandUpdate:
		if len(argv) < 5 || argv[0] != "update" {
			return validationError("update arguments do not match the typed command shape")
		}
		if err := requiredExternalID("work id", argv[1]); err != nil {
			return err
		}
		return validateOptionCommand(argv, "update", 2, []string{"--title", "--description", "--acceptance", "--metadata", "--assignee"})
	case CommandAtomicClaim:
		if len(argv) != 4 || argv[0] != "update" || argv[2] != "--claim" || argv[3] != "--json" {
			return validationError("claim arguments do not match the typed command shape")
		}
		return requiredExternalID("work id", argv[1])
	case CommandDependencyAdd:
		if len(argv) != 7 || argv[0] != "dep" || argv[1] != "add" || argv[4] != "--type" || argv[5] != "blocks" || argv[6] != "--json" {
			return validationError("dependency arguments do not match the typed command shape")
		}
	case CommandDependencyRemove:
		if len(argv) != 5 || argv[0] != "dep" || argv[1] != "remove" || argv[4] != "--json" {
			return validationError("dependency removal arguments do not match the typed command shape")
		}
	case CommandDependencyList:
		if len(argv) != 4 || argv[0] != "dep" || argv[1] != "list" || argv[3] != "--json" {
			return validationError("dependency list arguments do not match the typed command shape")
		}
		return requiredExternalID("work id", argv[2])
	case CommandCommentAppend:
		if len(argv) != 5 || argv[0] != "comments" || argv[1] != "add" || argv[4] != "--json" {
			return validationError("comment arguments do not match the typed command shape")
		}
		if err := requiredExternalID("work id", argv[2]); err != nil {
			return err
		}
		return requiredValue("comment body", argv[3])
	case CommandCommentList:
		if len(argv) != 3 || argv[0] != "comments" || argv[2] != "--json" {
			return validationError("comment list arguments do not match the typed command shape")
		}
		return requiredExternalID("work id", argv[1])
	case CommandExport:
		return fixed("export", "--json")
	case CommandBackupInit:
		return validateBackupPath(argv, "init")
	case CommandBackupSync:
		return fixed("backup", "sync")
	case CommandBackupStatus:
		return fixed("backup", "status")
	case CommandBackupRestore:
		return validateBackupPath(argv, "restore")
	default:
		return validationError("unsupported command kind")
	}
	if err := requiredExternalID("blocked work id", argv[2]); err != nil {
		return err
	}
	return requiredExternalID("blocking work id", argv[3])
}

func validateOptionCommand(argv []string, verb string, start int, orderedFlags []string) error {
	if len(argv) <= start+2 || argv[0] != verb || argv[len(argv)-1] != "--json" {
		return validationError(verb + " arguments do not match the typed command shape")
	}
	position := start
	seen := 0
	for _, flag := range orderedFlags {
		if position >= len(argv)-1 || argv[position] != flag {
			continue
		}
		if position+1 >= len(argv)-1 {
			return validationError(verb + " option is missing its typed value")
		}
		value := argv[position+1]
		explicitEmptyUpdate := verb == "update" && (flag == "--description" || flag == "--acceptance")
		if !explicitEmptyUpdate {
			if err := requiredValue(strings.TrimPrefix(flag, "--"), value); err != nil {
				return err
			}
		}
		position += 2
		seen++
	}
	if position != len(argv)-1 || seen == 0 || verb == "create" && argv[start] != "--title" {
		return validationError(verb + " arguments do not match the typed command shape")
	}
	return nil
}

func validateBackupPath(argv []string, operation string) error {
	if len(argv) != 3 || argv[0] != "backup" || argv[1] != operation || !filepath.IsAbs(argv[2]) || filepath.Clean(argv[2]) != argv[2] {
		return validationError("backup arguments do not match the typed command shape")
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
	case CommandVersion, CommandWhere, CommandDoltShow, CommandDoltStatus, CommandShow:
		return commandContract{shape: expectedObject, json: true}, true
	case CommandList, CommandReady, CommandDependencyList, CommandCommentList:
		return commandContract{shape: expectedArray, json: true}, true
	case CommandCreate, CommandUpdate, CommandAtomicClaim, CommandDependencyAdd, CommandDependencyRemove, CommandCommentAppend:
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
	values := make(map[string]string, len(baseEnvironmentNames)+len(binding.credentials)+5)
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
	// Beads otherwise writes metrics and deferred event state during nominally
	// read-only commands. These values are fixed process policy, not caller-
	// supplied credentials, so inspection cannot modify provider or user state.
	values["BD_DISABLE_METRICS"] = "1"
	values["BD_DISABLE_EVENT_FLUSH"] = "1"
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
