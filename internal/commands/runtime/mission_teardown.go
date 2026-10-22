package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/retrospective"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// recordedMid8 is mission_metadata.recorded_mid8: meta mid8, else the first
// eight characters of mission_id ("" when neither is recorded).
func recordedMid8(dir string) string {
	meta := mission.ReadMetaOrEmpty(dir)
	if m := strings.TrimSpace(meta.GetString("mid8")); m != "" {
		return m
	}
	if id := strings.TrimSpace(meta.GetString("mission_id")); len(id) >= 8 {
		return id[:8]
	}
	return ""
}

// teardownCoordination is mission close's _teardown_coordination_worktree:
// persist the retrospective (capture when absent, then commit the record
// and its event-log append), then remove the coordination worktree and
// report it (human mode). provenance "" is runtime_post_completion.
func teardownCoordination(ctx *cli.Context, root, slug, dir, provenance string, jsonMode bool) {
	mid8 := recordedMid8(dir)
	if mid8 == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, retrospective.FileName)); err != nil {
		retrospective.AuthorRuntime(root, slug, provenance)
	}
	var dirty []string
	for _, p := range []string{filepath.Join(dir, retrospective.FileName), status.EventsPath(dir)} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		if out, err := gitx.Run(root, "status", "--porcelain", "--", filepath.ToSlash(rel)); err == nil && strings.TrimSpace(out) != "" {
			dirty = append(dirty, p)
		}
	}
	if len(dirty) > 0 {
		coord.CommitPaths(root, fmt.Sprintf("chore(%s): capture mission retrospective", slug), dirty...)
	}
	wt := lanes.CoordWorktreePath(root, slug, mid8)
	if _, err := os.Stat(wt); err == nil {
		gitx.Run(root, "worktree", "remove", "--force", wt)
	}
	gitx.Run(root, "worktree", "prune")
	if jsonMode {
		return
	}
	if _, err := os.Stat(wt); err == nil {
		rich.Print(ctx.Stdout, "Warning: coordination worktree still present after teardown; manual cleanup may be required.")
	} else {
		rich.Print(ctx.Stdout, "✓ Coordination worktree torn down for "+mission.CoordMissionDirName(slug, mid8))
	}
}
