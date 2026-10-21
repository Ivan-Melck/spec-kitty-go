package agentcore

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// missionCtx is a resolved mission for the tasks/status command family.
type missionCtx struct {
	Root    string // main repository root
	Slug    string // mission directory name
	Surface *coord.Surface
}

func (m *missionCtx) PrimaryDir() string { return m.Surface.PrimaryDir }

// ReadDir is the coordination-aware STATUS read dir.
func (m *missionCtx) ReadDir() string { return m.Surface.StatusDir() }

// TasksDir is the PRIMARY tasks/ dir.
func (m *missionCtx) TasksDir() string { return m.Surface.PrimaryDir + "/tasks" }

func newMissionCtx(root, slug string) *missionCtx {
	return &missionCtx{Root: root, Slug: slug, Surface: coord.Resolve(root, slug)}
}

// targetBranch is read_target_branch_from_meta with the primary-branch
// fallback used by resolve_target_branch.
func (m *missionCtx) targetBranch() string {
	if t := strings.TrimSpace(m.Surface.Meta.GetString("target_branch")); t != "" {
		return t
	}
	return resolvePrimaryBranch(m.Root, "", true)
}

// banner is _ensure_target_branch_checked_out: prints the branch banner in
// human mode and returns the current branch.
func (m *missionCtx) banner(ctx *cli.Context, jsonMode bool) (string, error) {
	current := gitx.CurrentBranch(m.Root)
	if current == "" || current == "HEAD" {
		return "", fmt.Errorf("Detached HEAD — checkout a branch before continuing")
	}
	if !jsonMode {
		target := m.targetBranch()
		if current == target {
			consolePrint(ctx.Stdout, fmt.Sprintf("Branch: %s (target for this mission)", current))
		} else {
			consolePrint(ctx.Stdout, fmt.Sprintf("Branch: on '%s', mission targets '%s'", current, target))
		}
	}
	return current, nil
}

// outputError is tasks_shared._output_error.
func outputError(ctx *cli.Context, jsonMode bool, msg string) {
	if jsonMode {
		printJSON(ctx.Stdout, jsonx.NewObject("error", msg))
	} else {
		consolePrint(ctx.Stdout, "Error: "+msg)
	}
}

// identityPayload is _mission_identity_payload(feature_dir).
func identityPayload(dir string) *jsonx.Object {
	meta := mission.ReadMetaOrEmpty(dir)
	slug := meta.GetString("mission_slug")
	if slug == "" {
		slug = meta.GetString("slug")
	}
	if slug == "" || !mission.SafePathSegment(slug) {
		slug = lastSeg(dir)
	}
	return jsonx.NewObject("mission_slug", slug, "mission_number", mission.MissionNumberValue(meta),
		"mission_type", mission.MissionType(meta))
}

func lastSeg(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// pyRound rounds like Python round(x, n) for display.
func pyRound(x float64, n int) float64 {
	s := strconv.FormatFloat(x, 'f', n, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// pyFloat renders a float like Python str()/repr().
func pyFloat(x float64) string { return jsonx.FormatFloat(x) }

// cellWidth approximates rich's cell width (wide emoji count 2).
func cellWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r == 0xFE0F || r == 0x200D:
		case r >= 0x1F300 && r <= 0x1FAFF, r == 0x2705, r == 0x231A, r == 0x231B, r == 0x23E9, r == 0x23F3, r == 0x2B50:
			w += 2
		default:
			w++
		}
	}
	return w
}

func padRight(s string, w int) string {
	if n := cellWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func center(s string, w int) string {
	n := cellWidth(s)
	if n >= w {
		return s
	}
	left := (w - n) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-n-left)
}

// truncateRunes keeps the first n runes.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func floorInt(x float64) int { return int(math.Floor(x)) }

// resolveRequiredMission resolves --mission for tasks/status commands with
// the shared "--mission <slug> is required" error.
func resolveRequiredMission(ctx *cli.Context, jsonMode bool) (*missionCtx, error) {
	root := project.Locate("")
	if root == "" {
		outputError(ctx, jsonMode, "Could not locate project root")
		return nil, cli.Exit(1)
	}
	slug, err := resolveTasksMission(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return nil, err
	}
	return newMissionCtx(root, slug), nil
}
