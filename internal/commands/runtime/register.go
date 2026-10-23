// Package runtime registers the runtime-loop commands (area F):
// `agent action implement|review`, `implement`, `next`, the
// `mission`/`mission-type` run/close/follow-up/reopen leaves, `context *`,
// `agent tests stale-check` and `reconcile`.
package runtime

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
)

const agentHelp = "Commands for AI agents to execute spec-kitty mission actions programmatically"

// exitFrom maps a workflow error to a cli exit.
func exitFrom(err error) error {
	if err == nil {
		return nil
	}
	var we *workflow.Exit
	if errors.As(err, &we) {
		return cli.Exit(we.Code)
	}
	var ce *cli.ExitError
	if errors.As(err, &ce) {
		return err
	}
	return err
}

// locateRoot returns the main repository root (or "" when none).
func locateRoot() string {
	cwd, _ := os.Getwd()
	return project.Locate(cwd)
}

// resolveMission resolves --mission like resolve_mission_handle, printing
// upstream's errors (JSON envelope exit 1, human stderr exit 2).
func resolveMission(stdout, stderr io.Writer, root, handle string, jsonMode bool) (string, error) {
	slug, err := mission.ResolveHandle(root, handle)
	if err == nil {
		return slug, nil
	}
	return "", missionResolveError(stdout, stderr, err, jsonMode)
}

// resolveMissionIdentity is selector_resolution.resolve_mission_handle: the
// identity ladder only (no literal-dir short-circuit, no bare-modern fold),
// as the top-level implement uses it.
func resolveMissionIdentity(stdout, stderr io.Writer, root, handle string, jsonMode bool) (string, error) {
	r, err := mission.ResolveIdentity(root, handle)
	if err == nil {
		return r.Slug, nil
	}
	return "", missionResolveError(stdout, stderr, err, jsonMode)
}

// missionResolveError renders a resolver failure (human: exit 2, JSON: exit 1).
func missionResolveError(stdout, stderr io.Writer, err error, jsonMode bool) error {
	switch e := err.(type) {
	case *mission.SelectorAmbiguousError:
		if jsonMode {
			fmt.Fprintln(stdout, jsonx.Dumps(jsonx.NewObject("success", false, "error_code", e.Code(), "error", e.Error(),
				"handle", e.Handle, "candidates", e.Candidates), compact))
			return cli.Exit(1)
		}
		rich.Print(stdout, "[red]Error:[/red] "+e.Error())
		return cli.Exit(2)
	case *mission.AmbiguousError:
		if jsonMode {
			fmt.Fprintln(stdout, jsonx.Dumps(jsonx.NewObject("success", false, "error_code", "MISSION_AMBIGUOUS_SELECTOR", "error", e.Error(),
				"handle", e.Handle, "candidates", e.CandidatesJSON()), compact))
			return cli.Exit(1)
		}
		rich.Print(stderr, e.Error())
		return cli.Exit(2)
	case *mission.NotFoundError:
		if jsonMode {
			fmt.Fprintln(stdout, jsonx.Dumps(jsonx.NewObject("success", false, "error_code", "MISSION_NOT_FOUND", "error", e.Error(), "handle", e.Handle), compact))
			return cli.Exit(1)
		}
		rich.Print(stderr, "[red]Error:[/red] "+e.HumanMessage())
		return cli.Exit(2)
	}
	return err
}

// compact is json.dumps(x) default separators.
var compact = jsonx.Options{Indent: -1, EnsureASCII: true}

// captureOut is an Out whose stdout is buffered (json mode helpers).
func captureOut() (*workflow.Out, *bytes.Buffer) {
	var b bytes.Buffer
	return workflow.NewOut(&b, os.Stderr), &b
}
