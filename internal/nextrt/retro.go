package nextrt

// The run-cursor lock (runtime.next.run_lock) and the terminal
// retrospective around MissionRunCompleted
// (runtime_bridge_engine.TerminalRetrospective).

import (
	"path/filepath"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
	"github.com/spec-kitty/go-kitty3/internal/retrospective"
)

// runCursorLockTimeout is run_lock._LOCK_TIMEOUT_S.
var runCursorLockTimeout = 10 * time.Second

// runCursorLock is run_cursor_lock: the machine file lock on the dedicated
// <run_dir>/state.json.lock sidecar serialising every state.json
// read-modify-write of a run.
func runCursorLock(runDir string) (*filelock.Lock, error) {
	return filelock.Acquire(filepath.Join(runDir, "state.json.lock"), runCursorLockTimeout)
}

// retrospectiveGateRefused is RetrospectiveGateRefused: the strict
// pre-completion capture failed, so the terminal advance was aborted.
type retrospectiveGateRefused struct{ cause error }

func (e *retrospectiveGateRefused) Error() string { return e.cause.Error() }

// terminalRetrospective resolves the policy lazily, on the first terminal
// call. before is the engine's abort-only guard (the blocking capture under
// timing=before_completion + failure_policy=block); after runs the default
// best-effort capture once the advance landed, only when the guard was
// reached, so a re-poll of a terminal run captures nothing.
type terminalRetrospective struct {
	root, slug, featureDir string
	resolved, reached      bool
	enabled, blocking      bool
	missionID              string
}

func (t *terminalRetrospective) resolve() {
	if t.resolved {
		return
	}
	t.resolved = true
	// _resolve_retrospective_policy_for_runtime: a policy error falls back
	// to the default (enabled, non-blocking) policy; the capture itself
	// re-resolves and records the failure.
	policy, _, err := retrospective.ResolvePolicy(t.root)
	if err != nil {
		policy = retrospective.DefaultPolicy()
	}
	t.enabled, t.blocking = policy.Enabled(), policy.BlocksCompletion()
	t.missionID = retrospective.TerminusMissionID(t.featureDir)
}

func (t *terminalRetrospective) before() error {
	t.reached = true
	t.resolve()
	if t.enabled && t.blocking {
		if err := retrospective.RuntimeCapture(t.root, t.slug, t.missionID, "runtime_strict_gate"); err != nil {
			return &retrospectiveGateRefused{err}
		}
	}
	return nil
}

func (t *terminalRetrospective) after() {
	if !t.reached {
		return
	}
	t.resolve()
	if t.enabled && !t.blocking {
		_ = retrospective.RuntimeCapture(t.root, t.slug, t.missionID, "runtime_post_completion")
	}
}
