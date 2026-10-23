// Package colorparity compares the Go binary's terminal output with the
// oracle's when colour is on (FORCE_COLOR with each of rich's colour
// systems, and a real PTY) and when it is off (NO_COLOR, TERM=dumb, not a
// terminal): help for every inventoried path, usage errors, tables,
// panels, trees, log lines and tracebacks, byte for byte.
package colorparity

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

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

var reCorr = regexp.MustCompile(`corr-[0-9a-f]{32}`)

// normalize masks volatile values (timestamps, ids).
func normalize(s string) string {
	return testkit.NormalizeValues(reCorr.ReplaceAllString(s, "corr-<ID>"))
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
						got, want = normalize(got), normalize(want)
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

// leafProbes: every inventoried command path (leaves and groups) run bare,
// except commands that start long-running or interactive work.
func leafProbes(t *testing.T) []probe {
	dir := inventoryDir()
	if dir == "" {
		t.Skip("help inventory not present")
	}
	skip := map[string]bool{"init": true, "upgrade": true, "dashboard": true, "merge-driver": true,
		"session-start": true, "session-stop": true, "plugin build": true, "lint": true, "events tail": true,
		"commit-guard-hook": true}
	// The built-in pack root is the installed package directory upstream
	// (site-packages/packs/built-in) and the extracted cache under
	// ~/.kittify/cache/packs in Go; these print it.
	for _, k := range []string{"charter pack asset list", "charter pack regenerate-graph"} {
		skip[k] = true
	}
	// Bare groups are probed too (help, "Missing command." or a default
	// action); `agent config` belongs to the setup package (area TSE).
	skip["agent config"] = true
	files, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	var paths [][]string
	for _, f := range files {
		paths = append(paths, pathArgs(filepath.Base(f)))
	}
	var out []probe
	for _, a := range paths {
		name := strings.Join(a, " ")
		if len(a) == 0 || skip[name] || strings.HasPrefix(name, "merge-driver") || strings.HasPrefix(name, "__") {
			continue
		}
		out = append(out, probe{name, a})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// TestNoProjectColour runs every command path bare in an empty directory
// (not a project, not a git repository) and compares the coloured output:
// the "not in a project" / missing-argument paths of every command family.
func TestNoProjectColour(t *testing.T) {
	oracle := testkit.Oracle(t)
	bin := testkit.Binary(t)
	probes := leafProbes(t)
	for _, v := range colourSystems[:1] {
		v := v
		t.Run(v.name, func(t *testing.T) {
			ch := make(chan probe)
			var mu sync.Mutex
			var bad []string
			var wg sync.WaitGroup
			for w := 0; w < 6; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for p := range ch {
						tmp := t.TempDir()
						res := map[string]string{}
						for _, side := range []string{"o", "g"} {
							// Same basenames on both sides: rich highlights a
							// path's last component separately, so only the
							// shared prefix can be masked.
							base := filepath.Join(tmp, side)
							home, cwd := filepath.Join(base, "home"), filepath.Join(base, "cwd")
							os.MkdirAll(home, 0o755)
							os.MkdirAll(cwd, 0o755)
							exe := bin
							if side == "o" {
								exe = oracle
							}
							out, code := runTimeout(exe, home, cwd, v.env, p.args...)
							out = strings.ReplaceAll(strings.ReplaceAll(out, cwd, "<CWD>"), home, "<HOME>")
							out = strings.ReplaceAll(out, base+"/", "<BASE>/")
							res[side] = fmt.Sprintf("exit %d\n%s", code, normalize(testkit.MaskTracebackFrames(out)))
						}
						if res["o"] != res["g"] {
							mu.Lock()
							bad = append(bad, p.name)
							if os.Getenv("COLORPARITY_VERBOSE") != "" {
								t.Logf("%s: %s", p.name, firstDiff(res["o"], res["g"]))
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

// runTimeout is run with stdin closed and a 90 s limit.
func runTimeout(bin, home, cwd string, env []string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cwd
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "SPEC_KITTY_NO_UPGRADE_CHECK=1",
		"LANG=C.UTF-8", "COLUMNS=100", "GIT_CONFIG_NOSYSTEM=1"}, env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}
	return out.String(), code
}

// TestPTYSpotCheck runs a few commands under a real pseudo-terminal
// (util-linux script(1)), no FORCE_COLOR: colour detection from isatty.
func TestPTYSpotCheck(t *testing.T) {
	oracle := testkit.Oracle(t)
	bin := testkit.Binary(t)
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not available")
	}
	for _, args := range [][]string{
		{"archive", "--help"},
		{"agent", "tasks", "move-task", "--help"},
		{"archive", "--bogus"},
		{"nonexistent"},
		{"context", "list"},
		{"mission", "switch"},
	} {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			pty := func(exe string) string {
				home, cwd := t.TempDir(), t.TempDir()
				cmd := exec.Command("script", "-qec", exe+" "+strings.Join(args, " "), "/dev/null")
				cmd.Dir = cwd
				cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "SPEC_KITTY_NO_UPGRADE_CHECK=1",
					"LANG=C.UTF-8", "COLUMNS=100", "LINES=40", "TERM=xterm-256color"}
				out, _ := cmd.CombinedOutput()
				return string(out)
			}
			want, got := pty(oracle), pty(bin)
			if want != got {
				t.Errorf("%v under a PTY: %s", args, firstDiff(want, got))
			}
			if !strings.Contains(want, "\x1b[") {
				t.Errorf("oracle output under a PTY has no colour: %q", want)
			}
		})
	}
}

// TestUsageErrorColour: typer's rich_format_error on stderr.
func TestUsageErrorColour(t *testing.T) {
	compareAll(t, usageProbes(t), append(colourSystems[:len(colourSystems):len(colourSystems)], plainVariants...))
}
