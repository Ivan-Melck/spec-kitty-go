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

// narrowToWide is the BMP part of rich's cell_table.narrow_to_wide: narrow
// characters a following VS16 (U+FE0F) renders 2 cells wide.
var narrowToWide = [][2]rune{{0x23, 0x23}, {0x2A, 0x2A}, {0x30, 0x39}, {0xA9, 0xA9}, {0xAE, 0xAE}, {0x203C, 0x203C}, {0x2049, 0x2049},
	{0x2122, 0x2122}, {0x2139, 0x2139}, {0x2194, 0x2199}, {0x21A9, 0x21AA}, {0x2328, 0x2328}, {0x23CF, 0x23CF}, {0x23ED, 0x23EF},
	{0x23F1, 0x23F2}, {0x23F8, 0x23FA}, {0x24C2, 0x24C2}, {0x25AA, 0x25AB}, {0x25B6, 0x25B6}, {0x25C0, 0x25C0}, {0x25FB, 0x25FC},
	{0x2600, 0x2604}, {0x260E, 0x260E}, {0x2611, 0x2611}, {0x2618, 0x2618}, {0x261D, 0x261D}, {0x2620, 0x2620}, {0x2622, 0x2623},
	{0x2626, 0x2626}, {0x262A, 0x262A}, {0x262E, 0x262F}, {0x2638, 0x263A}, {0x2640, 0x2640}, {0x2642, 0x2642}, {0x265F, 0x2660},
	{0x2663, 0x2663}, {0x2665, 0x2666}, {0x2668, 0x2668}, {0x267B, 0x267B}, {0x267E, 0x267E}, {0x2692, 0x2692}, {0x2694, 0x2697},
	{0x2699, 0x2699}, {0x269B, 0x269C}, {0x26A0, 0x26A0}, {0x26A7, 0x26A7}, {0x26B0, 0x26B1}, {0x26C8, 0x26C8}, {0x26CF, 0x26CF},
	{0x26D1, 0x26D1}, {0x26D3, 0x26D3}, {0x26E9, 0x26E9}, {0x26F0, 0x26F1}, {0x26F4, 0x26F4}, {0x26F7, 0x26F9}, {0x2702, 0x2702},
	{0x2708, 0x2709}, {0x270C, 0x270D}, {0x270F, 0x270F}, {0x2712, 0x2712}, {0x2714, 0x2714}, {0x2716, 0x2716}, {0x271D, 0x271D},
	{0x2721, 0x2721}, {0x2733, 0x2734}, {0x2744, 0x2744}, {0x2747, 0x2747}, {0x2763, 0x2764}, {0x27A1, 0x27A1}, {0x2934, 0x2935},
	{0x2B05, 0x2B07}, {0x1F170, 0x1F171}, {0x1F17E, 0x1F17F}}

func isNarrowToWide(r rune) bool {
	for _, rg := range narrowToWide {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

// cellWidth approximates rich's cell width (wide emoji count 2; a VS16
// widens the preceding narrow emoji-capable character, as rich 15 does).
func cellWidth(s string) int {
	w := 0
	var last rune = -1
	for _, r := range s {
		switch {
		case r == 0x200D:
		case r == 0xFE0F:
			if last >= 0 && isNarrowToWide(last) {
				w++
			}
			last = -1
			continue
		case r >= 0x1F300 && r <= 0x1FAFF, r == 0x2705, r == 0x231A, r == 0x231B, r == 0x23E9, r == 0x23F3, r == 0x2B50:
			w += 2
		default:
			w++
		}
		last = r
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
