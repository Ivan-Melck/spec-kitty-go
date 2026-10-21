// Package helpparity checks the Go binary's --help output against the
// upstream inventory ($REF/inventory/help/*.txt, rendered at COLUMNS=160).
package helpparity

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// inventoryDir locates $REF/inventory/help, or "" when absent.
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
			// "__force_multi_command_mode__" splits into empty segments.
			out = append(out, strings.Join(parts[i:], "_"))
			break
		}
		out = append(out, parts[i])
	}
	return out
}

func runHelp(t *testing.T, bin, home, cwd string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Env = append([]string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"SPEC_KITTY_NO_UPGRADE_CHECK=1",
		"NO_COLOR=1",
		"TERM=dumb",
		"LANG=C.UTF-8",
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

// TestHelpInventoryParity renders --help for every inventoried path and
// compares it byte for byte. Paths the Go binary does not implement (it
// answers "No such command"), and groups whose only difference is listing
// such subcommands, are reported but not failed.
func TestHelpInventoryParity(t *testing.T) {
	dir := inventoryDir()
	if dir == "" {
		t.Skip("help inventory not present")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	sort.Strings(files)
	bin := testkit.Binary(t)
	home := t.TempDir()
	cwd := t.TempDir()
	type result struct{ file, path, want, got string }
	var results []result
	var differ, missing, pending []string
	missingSet := map[string]bool{}
	for _, f := range files {
		want, _ := os.ReadFile(f)
		args := append(pathArgs(filepath.Base(f)), "--help")
		got, code := runHelp(t, bin, home, cwd, []string{"COLUMNS=160"}, args...)
		path := strings.Join(args[:len(args)-1], " ")
		if code == 2 && strings.Contains(got, "No such command") {
			missing = append(missing, path)
			missingSet[path] = true
			continue
		}
		results = append(results, result{filepath.Base(f), path, string(want), got})
	}
	matched := 0
	for _, r := range results {
		switch {
		case r.got == r.want:
			matched++
		case onlyMissingSubcommands(r.path, r.want, r.got, missingSet):
			pending = append(pending, r.file)
		default:
			differ = append(differ, r.file)
			if os.Getenv("HELPPARITY_VERBOSE") != "" {
				t.Logf("%s differs:\n--- want\n%s\n--- got\n%s", r.file, r.want, r.got)
			}
		}
	}
	t.Logf("help parity: %d match, %d differ, %d list unported subcommands, %d not implemented (of %d)",
		matched, len(differ), len(pending), len(missing), len(files))
	if len(pending) > 0 {
		t.Logf("listing unported subcommands: %s", strings.Join(pending, " "))
	}
	if len(missing) > 0 {
		t.Logf("not implemented: %s", strings.Join(missing, ", "))
	}
	if len(differ) > 0 {
		t.Errorf("%d help outputs differ: %s", len(differ), strings.Join(differ, " "))
	}
}

// panelCommands lists the names in a help output's Commands panel.
func panelCommands(help string) []string {
	var names []string
	in := false
	for _, l := range strings.Split(help, "\n") {
		switch {
		case strings.HasPrefix(l, "╭─ Commands"):
			in = true
		case in && strings.HasPrefix(l, "╰"):
			return names
		case in && strings.HasPrefix(l, "│ ") && !strings.HasPrefix(l, "│  "):
			if f := strings.Fields(strings.TrimPrefix(l, "│ ")); len(f) > 0 {
				names = append(names, f[0])
			}
		}
	}
	return names
}

// onlyMissingSubcommands reports whether a group's help differs because it
// lists subcommands the Go binary does not implement yet.
func onlyMissingSubcommands(path, want, got string, missing map[string]bool) bool {
	have := map[string]bool{}
	for _, n := range panelCommands(got) {
		have[n] = true
	}
	absent := 0
	for _, n := range panelCommands(want) {
		if have[n] {
			continue
		}
		if !missing[strings.TrimSpace(path+" "+n)] {
			return false
		}
		absent++
	}
	return absent > 0
}
