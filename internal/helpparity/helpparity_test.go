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
	// "__force_multi_command_mode__" splits into empty segments.
	var out []string
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			// re-join the hidden path verbatim
			rest := strings.Join(parts[i:], "_")
			out = append(out, rest)
			break
		}
		out = append(out, parts[i])
	}
	return out
}

func runHelp(t *testing.T, bin, home, cwd string, env []string, args ...string) (string, int) {
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
// answers "No such command") are reported but not failed.
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
	var differ, missing []string
	matched := 0
	for _, f := range files {
		want, _ := os.ReadFile(f)
		args := append(pathArgs(filepath.Base(f)), "--help")
		got, code := runHelp(t, bin, home, cwd, []string{"COLUMNS=160"}, args...)
		if code == 2 && strings.Contains(got, "No such command") {
			missing = append(missing, strings.Join(args[:len(args)-1], " "))
			continue
		}
		if got != string(want) {
			differ = append(differ, filepath.Base(f))
			if os.Getenv("HELPPARITY_VERBOSE") != "" {
				t.Logf("%s differs:\n--- want\n%s\n--- got\n%s", filepath.Base(f), want, got)
			}
			continue
		}
		matched++
	}
	t.Logf("help parity: %d match, %d differ, %d not implemented (of %d)", matched, len(differ), len(missing), len(files))
	if len(missing) > 0 {
		t.Logf("not implemented: %s", strings.Join(missing, ", "))
	}
	if len(differ) > 0 {
		t.Errorf("%d help outputs differ: %s", len(differ), strings.Join(differ, " "))
	}
}
