package hubnot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	maxStdoutBytes      = 8 << 20
	maxStderrBytes      = 64 << 10
	maxObservationBytes = 64 << 20
	maxObservationItems = 10000
	maxMutationBytes    = 256 << 10
	childTimeout        = 30 * time.Second
	observationTimeout  = 120 * time.Second
)

var errOutputLimit = errors.New("provider output limit exceeded")
var errObservationLimit = errors.New("logical observation limit exceeded")

type observationBudget struct {
	ctx                    context.Context
	cancel                 context.CancelFunc
	mu                     sync.Mutex
	bytes, items, restarts int
}

// The same budget must survive stale-snapshot restarts and failed attempts.
func newObservationBudget() *observationBudget {
	ctx, cancel := context.WithTimeout(context.Background(), observationTimeout)
	return &observationBudget{ctx: ctx, cancel: cancel}
}
func (b *observationBudget) close() { b.cancel() }
func (b *observationBudget) charge(bytes, items int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if bytes < 0 || items < 0 {
		return errObservationLimit
	}
	b.bytes += bytes
	b.items += items
	if b.bytes > maxObservationBytes || b.items > maxObservationItems {
		return errObservationLimit
	}
	return b.ctx.Err()
}
func (b *observationBudget) restart() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.restarts++
	if b.restarts > 2 || b.bytes > maxObservationBytes || b.items > maxObservationItems {
		return errObservationLimit
	}
	return b.ctx.Err()
}

type processResult struct {
	started        bool
	start          time.Time
	exitCode       int
	stdout, stderr []byte
	outputBytes    int
	err            error
}
type limitCapture struct {
	buffer       bytes.Buffer
	limit, total int
	cancel       context.CancelFunc
	overflow     bool
}

func (w *limitCapture) Write(p []byte) (int, error) {
	w.total += len(p)
	remaining := w.limit - w.buffer.Len()
	if len(p) > remaining {
		_, _ = w.buffer.Write(p[:remaining])
		w.overflow = true
		w.cancel()
		return len(p), errOutputLimit
	}
	return w.buffer.Write(p)
}

// Fixed ambient policy: no caller-supplied Git alternate repository, credentials,
// provider secrets, global Git configuration or shell evaluation reaches hn.
func processEnvironment() []string {
	return []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C", "LANG=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
}
func runProcess(parent context.Context, executable, workspace string, args []string, input []byte) processResult {
	result := processResult{exitCode: -1, start: time.Now()}
	if parent == nil {
		result.err = errors.New("context is missing")
		return result
	}
	ctx, cancel := context.WithTimeout(parent, childTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workspace
	cmd.Env = processEnvironment()
	cmd.Stdin = bytes.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = kill
	cmd.WaitDelay = 250 * time.Millisecond
	stdout := &limitCapture{limit: maxStdoutBytes, cancel: cancel}
	stderr := &limitCapture{limit: maxStderrBytes, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		result.err = err
		return result
	}
	result.started = true
	err := cmd.Wait()
	_ = kill() // Also terminate descendants which inherited pipes.
	result.stdout, result.stderr = stdout.buffer.Bytes(), stderr.buffer.Bytes()
	result.outputBytes = stdout.total + stderr.total
	if cmd.ProcessState != nil {
		result.exitCode = cmd.ProcessState.ExitCode()
	}
	switch {
	case stdout.overflow || stderr.overflow:
		result.err = errOutputLimit
	case ctx.Err() != nil:
		result.err = ctx.Err()
	case result.exitCode < 0:
		result.err = err
	case errors.Is(err, exec.ErrWaitDelay):
		result.err = err
	default:
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			result.err = err
		}
	}
	return result
}

var _ io.Writer = (*limitCapture)(nil)
