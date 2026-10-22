package planning

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// tasksOutputError is tasks_shared._output_error (compact JSON / console).
func tasksOutputError(ctx *cli.Context, jsonMode bool, msg string) {
	if jsonMode {
		printCompact(ctx.Stdout, jsonx.NewObject("error", msg))
	} else {
		say(ctx.Stdout, "Error: "+rich.Strip(msg))
	}
}

// resolveTasksMission is tasks_shared._find_mission_slug.
func resolveTasksMission(ctx *cli.Context, root, handle string, jsonMode bool) (string, error) {
	h := strings.TrimSpace(handle)
	if h == "" {
		tasksOutputError(ctx, jsonMode, "--mission <slug> is required")
		return "", cli.Exit(1)
	}
	slug, err := mission.ResolveHandle(root, h)
	if err == nil {
		return slug, nil
	}
	return "", renderResolveError(ctx, err, jsonMode)
}

// renderResolveError renders a mission.ResolveHandle failure the way the
// agent tasks family does.
func renderResolveError(ctx *cli.Context, err error, jsonMode bool) error {
	switch e := err.(type) {
	case *mission.SelectorAmbiguousError:
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("success", false, "error_code", e.Code(), "error", e.Error(), "handle", e.Handle, "candidates", strList(e.Candidates)))
			return cli.Exit(1)
		}
		say(ctx.Stdout, "[red]Error:[/red] "+e.Error())
		return cli.Exit(2)
	case *mission.AmbiguousError:
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("success", false, "error_code", "MISSION_AMBIGUOUS_SELECTOR", "error", e.Error(), "handle", e.Handle, "candidates", e.CandidatesJSON()))
			return cli.Exit(1)
		}
		say(ctx.Stderr, e.Error())
		return cli.Exit(2)
	case *mission.NotFoundError:
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("success", false, "error_code", "MISSION_NOT_FOUND", "error", e.Error(), "handle", e.Handle))
			return cli.Exit(1)
		}
		say(ctx.Stderr, "[red]Error:[/red] "+e.HumanMessage())
		return cli.Exit(2)
	}
	return err
}

// targetBanner is _ensure_target_branch_checked_out: banner in human mode,
// returns the current branch.
func targetBanner(ctx *cli.Context, root, slug string, jsonMode bool) (string, error) {
	current := gitx.CurrentBranch(root)
	if current == "" || current == "HEAD" {
		return "", fmt.Errorf("Detached HEAD — checkout a branch before continuing")
	}
	if !jsonMode {
		target := strings.TrimSpace(mission.ReadMetaOrEmpty(project.Specs(root, slug)).GetString("target_branch"))
		if target == "" {
			target = safecommit.PrimaryBranch(root)
		}
		if current == target {
			say(ctx.Stdout, fmt.Sprintf("Branch: %s (target for this mission)", current))
		} else {
			say(ctx.Stdout, fmt.Sprintf("Branch: on '%s', mission targets '%s'", current, target))
		}
	}
	return current, nil
}

// protectedStatusCommitError is _protected_branch_status_commit_error.
func protectedStatusCommitError(branch, root, command, slug string) string {
	if !coord.IsProtected(root, slug, branch) {
		return ""
	}
	return fmt.Sprintf("Refusing to run `%s` with auto-commit on protected branch '%s' before mutating status files. Run status commit operations from an allowed coordination/lane branch, or unblock with: %s.", command, branch, coord.ProtectedRemedy)
}

// identityPayload is _mission_identity_payload(feature_dir).
func identityPayload(dir string) *jsonx.Object {
	meta := mission.ReadMetaOrEmpty(dir)
	slug := meta.GetString("mission_slug")
	if slug == "" {
		slug = meta.GetString("slug")
	}
	if slug == "" || !mission.SafePathSegment(slug) {
		parts := strings.Split(strings.TrimRight(dir, "/"), "/")
		slug = parts[len(parts)-1]
	}
	return jsonx.NewObject("mission_slug", slug, "mission_number", mission.MissionNumberValue(meta), "mission_type", mission.MissionType(meta))
}
