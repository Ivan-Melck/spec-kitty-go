package hiddencmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/commands/setup"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() { cli.RootHelpBanner = rootHelpBanner }

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

// bannerWanted is _should_render_banner_for_invocation: only when the first
// non-option token is "init", outside agent harnesses.
func bannerWanted(argv []string) bool {
	if truthy(os.Getenv("SPEC_KITTY_NO_BANNER")) {
		return false
	}
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE", "CODEX", "OPENCODE", "CURSOR_TRACE_ID"} {
		if _, ok := os.LookupEnv(k); ok {
			return false
		}
	}
	for _, t := range argv {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || strings.HasPrefix(t, "-") {
			continue
		}
		return t == "init"
	}
	return false
}

// rootHelpBanner is show_banner() as called by the root group's help.
func rootHelpBanner(w io.Writer, argv []string) {
	if !bannerWanted(argv) {
		return
	}
	width := cli.Width()
	centered := func(s string) {
		n := rich.CellLen(s)
		if n >= width {
			fmt.Fprintln(w, s)
			return
		}
		left := (width - n) / 2
		fmt.Fprintln(w, strings.Repeat(" ", left)+s+strings.Repeat(" ", width-n-left))
	}
	lines := strings.Split(strings.TrimSpace(setup.Banner), "\n")
	maxw := 0
	for _, l := range lines {
		maxw = max(maxw, rich.CellLen(l))
	}
	for _, l := range lines {
		centered(rich.SetCellSize(l, maxw))
	}
	centered(strings.Repeat(" ", maxw))
	centered(setup.Tagline)
	centered("v" + app.Version)
	fmt.Fprintln(w)
}
