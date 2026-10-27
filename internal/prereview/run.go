package prereview

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Head-run states (HeadRunState).
const (
	RunCompleted        = "completed"
	RunTimedOut         = "timed_out"
	RunCancelled        = "cancelled"
	RunLaunchFailed     = "launch_failed"
	RunIncompleteOutput = "incomplete_output"
	RunNotStarted       = "not_started"
)

const (
	heartbeatInterval = 30 * time.Second
	terminateGrace    = 5 * time.Second
	stderrTailChars   = 500
	lockAcquire       = 5 * time.Second
)

func itoa(i int) string { return strconv.Itoa(i) }

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[len(r)-n:])
	}
	return s
}

// RuntimeRoot is get_runtime_root().base: $SPEC_KITTY_HOME, else ~/.spec-kitty.
func RuntimeRoot() string {
	if h := os.Getenv("SPEC_KITTY_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".spec-kitty")
}

// withRunLock serialises head runs machine-wide on
// <runtime root>/gate-locks/pre-review-gate-run.lock (advisory: a lock that
// cannot be taken within 5s is skipped, like upstream).
func withRunLock(fn func()) {
	path := filepath.Join(RuntimeRoot(), "gate-locks", "pre-review-gate-run.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fn()
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		fn()
		return
	}
	defer f.Close()
	deadline := time.Now().Add(lockAcquire)
	locked := false
	for {
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			locked = true
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if locked {
		defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
	fn()
}

// RunOptions configures one command run.
type RunOptions struct {
	Dir      string
	Timeout  time.Duration
	Progress func(elapsed float64) // heartbeat every 30s while running
	Elapsed  func(elapsed float64) // observed elapsed on timeout
	Serial   bool                  // take the machine-wide run lock
}

func gateEnv() []string {
	env := os.Environ()
	return append(env, "PWHEADLESS=1")
}

// RunRaw is pre_review_gate._run_raw_command: launch in its own session,
// observe with heartbeats until the timeout (then SIGTERM/SIGKILL the
// process group). Returns (state, raw or nil, error text).
func RunRaw(command []string, o RunOptions) (string, *Raw, string) {
	var state string
	var raw *Raw
	var errText string
	run := func() {
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Dir = o.Dir
		cmd.Env = gateEnv()
		ownProcessGroup(cmd)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Start(); err != nil {
			state, errText = RunLaunchFailed, "scoped test run failed to launch: "+pyOSError(err, command[0])
			return
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		start := time.Now()
		deadline := start.Add(o.Timeout)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				terminate(cmd, done)
				if o.Elapsed != nil {
					o.Elapsed(time.Since(start).Seconds())
				}
				state = RunTimedOut
				errText = fmt.Sprintf("scoped test run timed out after %ds; stderr tail: %s", int(o.Timeout/time.Second), tail(errb.String(), stderrTailChars))
				return
			}
			wait := heartbeatInterval
			if remaining < wait {
				wait = remaining
			}
			select {
			case <-done:
				state = RunCompleted
				code := 0
				if cmd.ProcessState != nil {
					code = cmd.ProcessState.ExitCode()
					if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
						code = -int(ws.Signal())
					}
				}
				raw = &Raw{ReturnCode: code, Stdout: out.String(), Stderr: errb.String()}
				return
			case <-time.After(wait):
				if time.Now().Before(deadline) && o.Progress != nil {
					o.Progress(time.Since(start).Seconds())
				}
			}
		}
	}
	if o.Serial {
		withRunLock(run)
	} else {
		run()
	}
	if raw != nil {
		if p := junitArg(command); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(o.Dir, p)
			}
			raw.ArtifactPath = p
		}
	}
	return state, raw, errText
}

func terminate(cmd *exec.Cmd, done chan error) {
	signalProcessTree(cmd, false)
	select {
	case <-done:
		return
	case <-time.After(terminateGrace):
	}
	signalProcessTree(cmd, true)
	select {
	case <-done:
	case <-time.After(terminateGrace):
	}
}

// junitArg is _extract_junit_output_path.
func junitArg(command []string) string {
	for _, a := range command {
		if strings.HasPrefix(a, "--junitxml=") {
			return strings.SplitN(a, "=", 2)[1]
		}
	}
	return ""
}

// pyOSError renders a launch failure like Python's FileNotFoundError str().
func pyOSError(err error, exe string) string {
	if e, ok := err.(*exec.Error); ok && e.Err == exec.ErrNotFound {
		return fmt.Sprintf("[Errno 2] No such file or directory: '%s'", exe)
	}
	if pe, ok := err.(*os.PathError); ok {
		if errno, ok := pe.Err.(syscall.Errno); ok {
			return fmt.Sprintf("[Errno %d] %s: '%s'", int(errno), pyStrerror(errno), exe)
		}
	}
	return err.Error()
}

func pyStrerror(e syscall.Errno) string {
	s := e.Error()
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
