// Package colorparity compares the Go binary's terminal output with the
// oracle's when colour is on (FORCE_COLOR with each of rich's colour
// systems, and a real PTY) and when it is off (NO_COLOR, TERM=dumb, not a
// terminal): help for every inventoried path, usage errors, tables,
// panels, trees, log lines and tracebacks, byte for byte.
package colorparity

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// colourSystems are the environments that select each rich colour system
// (plus the variants that must stay plain or attribute-only).
var colourSystems = []struct {
	name string
	env  []string
}{
	{"standard", []string{"FORCE_COLOR=1", "TERM=xterm"}},
	{"256", []string{"FORCE_COLOR=1", "TERM=xterm-256color"}},
	{"truecolor", []string{"FORCE_COLOR=1", "TERM=xterm", "COLORTERM=truecolor"}},
}

// plainVariants must produce the same bytes as the oracle too.
var plainVariants = []struct {
	name string
	env  []string
}{
	{"no_color", []string{"FORCE_COLOR=1", "TERM=xterm", "NO_COLOR=1"}},
	{"dumb", []string{"FORCE_COLOR=1", "TERM=dumb"}},
	{"notty", []string{"TERM=xterm"}},
}

func inventoryDir() string {
	if d := os.Getenv("SPEC_KITTY_HELP_INVENTORY"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	d := filepath.Join(home, ".cache", "spec-kitty-parity", "inventory", "help")
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d
	}
	return ""
}

// pathArgs turns "spec-kitty_agent_tasks_move-task.txt" into its argv.
func pathArgs(file string) []string {
	parts := strings.Split(strings.TrimSuffix(file, ".txt"), "_")
	var out []string
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			out = append(out, strings.Join(parts[i:], "_"))
			break
		}
		out = append(out, parts[i])
	}
	return out
}

func run(t *testing.T, bin, home, cwd string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Env = append([]string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"SPEC_KITTY_NO_UPGRADE_CHECK=1",
		"LANG=C.UTF-8",
		"COLUMNS=100",
	}, env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return out.String(), code
}

// visible shows escape sequences for failure messages.
func visible(s string) string { return strings.ReplaceAll(s, "\x1b", "␛") }

// firstDiff reports the first differing line.
func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(wl), len(gl)); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d:\nwant %q\ngot  %q", i+1, visible(w), visible(g))
		}
	}
	return ""
}

type probe struct {
	name string
	args []string
}

// compareAll runs every probe under every environment on both binaries in
// parallel and reports mismatches.
func compareAll(t *testing.T, probes []probe, envs []struct {
	name string
	env  []string
}) {
	oracle := testkit.Oracle(t)
	bin := testkit.Binary(t)
	for _, v := range envs {
		v := v
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			goHome, oHome, cwd := t.TempDir(), t.TempDir(), t.TempDir()
			ch := make(chan probe)
			var mu sync.Mutex
			var bad []string
			var wg sync.WaitGroup
			for w := 0; w < 6; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for p := range ch {
						got, gc := run(t, bin, goHome, cwd, v.env, p.args...)
						want, wc := run(t, oracle, oHome, cwd, v.env, p.args...)
						if got != want || gc != wc {
							mu.Lock()
							bad = append(bad, p.name)
							if len(bad) <= 3 || os.Getenv("COLORPARITY_VERBOSE") != "" {
								t.Logf("%s (exit %d vs %d): %s", p.name, wc, gc, firstDiff(want, got))
							}
							mu.Unlock()
						}
					}
				}()
			}
			for _, p := range probes {
				ch <- p
			}
			close(ch)
			wg.Wait()
			sort.Strings(bad)
			if len(bad) > 0 {
				t.Errorf("%d of %d differ: %s", len(bad), len(probes), strings.Join(bad, ", "))
			}
		})
	}
}

func helpProbes(t *testing.T) []probe {
	dir := inventoryDir()
	if dir == "" {
		t.Skip("help inventory not present")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	sort.Strings(files)
	var out []probe
	for _, f := range files {
		args := append(pathArgs(filepath.Base(f)), "--help")
		out = append(out, probe{strings.Join(args, " "), args})
	}
	return out
}

// TestHelpColour: --help of every inventoried path, coloured exactly like
// typer/rich for each colour system.
func TestHelpColour(t *testing.T) { compareAll(t, helpProbes(t), colourSystems) }

// TestHelpPlainVariants: NO_COLOR keeps attributes only, TERM=dumb and a
// non-terminal stay plain.
func TestHelpPlainVariants(t *testing.T) { compareAll(t, helpProbes(t), plainVariants) }

// usageProbes: an unknown option on every inventoried path (usage line,
// "Try ... --help" suggestion and Error panel with option suggestions),
// plus unknown subcommands and missing arguments.
func usageProbes(t *testing.T) []probe {
	var out []probe
	for _, p := range helpProbes(t) {
		args := append(append([]string(nil), p.args[:len(p.args)-1]...), "--hel")
		out = append(out, probe{strings.Join(args, " "), args})
	}
	for _, args := range [][]string{
		{"nonexistent-command"},
		{"agent", "taks"},
		{"charter", "bogus", "x"},
		{"agent", "tasks", "move-task"},
		{"agent", "tasks", "move-task", "WP01"},
		{"archive", "create"},
		{"implement", "--mission"},
		{"next", "--agent"},
		{"agent", "config", "set"},
		{"init", "--ai", "nope"},
		{"agent", "tasks", "status", "--json", "extra", "args"},
		{"tracker", "sync", "pull", "--limit", "x"},
	} {
		out = append(out, probe{strings.Join(args, " "), args})
	}
	return out
}

// TestUsageErrorColour: typer's rich_format_error on stderr.
func TestUsageErrorColour(t *testing.T) {
	compareAll(t, usageProbes(t), append(colourSystems[:len(colourSystems):len(colourSystems)], plainVariants...))
}
