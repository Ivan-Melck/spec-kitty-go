package helpparity

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestHelpWidthsOracle compares --help with the oracle at other console
// widths (and with the plain renderer forced on/off) for every inventoried
// path the Go binary implements.
func TestHelpWidthsOracle(t *testing.T) {
	oracle := testkit.Oracle(t)
	dir := inventoryDir()
	if dir == "" {
		t.Skip("help inventory not present")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	sort.Strings(files)
	bin := testkit.Binary(t)
	variants := []struct {
		name string
		env  []string
	}{
		{"cols80", []string{"COLUMNS=80"}},
		{"cols100", []string{"COLUMNS=100"}},
		{"cols60", []string{"COLUMNS=60"}},
		{"simple160", []string{"COLUMNS=160", "SPEC_KITTY_SIMPLE_HELP=1"}},
		{"rich60", []string{"COLUMNS=60", "SPEC_KITTY_SIMPLE_HELP=0"}},
	}
	for _, v := range variants {
		v := v
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			goHome, oHome, cwd := t.TempDir(), t.TempDir(), t.TempDir()
			paths := make(chan []string)
			var mu sync.Mutex
			var bad []string
			var wg sync.WaitGroup
			for w := 0; w < 6; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for args := range paths {
						argv := append(append([]string(nil), args...), "--help")
						got, code := runHelp(t, bin, goHome, cwd, v.env, argv...)
						if code == 2 && strings.Contains(got, "No such command") {
							continue
						}
						want, _ := runHelp(t, oracle, oHome, cwd, v.env, argv...)
						if got != want && !onlyMissingSubcommands(strings.Join(args, " "), want, got, goMissing(t, bin, goHome, cwd, want, args)) {
							mu.Lock()
							bad = append(bad, strings.Join(args, " "))
							mu.Unlock()
							if os.Getenv("HELPPARITY_VERBOSE") != "" {
								t.Logf("%v differs:\n--- want\n%s\n--- got\n%s", args, want, got)
							}
						}
					}
				}()
			}
			for _, f := range files {
				paths <- pathArgs(filepath.Base(f))
			}
			close(paths)
			wg.Wait()
			sort.Strings(bad)
			if len(bad) > 0 {
				t.Errorf("%d paths differ: %s", len(bad), strings.Join(bad, ", "))
			}
		})
	}
}

// goMissing returns the subcommands listed in want that the Go binary
// rejects as unknown commands.
func goMissing(t *testing.T, bin, home, cwd, want string, args []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range panelCommands(want) {
		argv := append(append([]string(nil), args...), n, "--help")
		got, code := runHelp(t, bin, home, cwd, []string{"COLUMNS=80"}, argv...)
		if code == 2 && strings.Contains(got, "No such command") {
			out[strings.TrimSpace(fmt.Sprint(strings.Join(args, " "), " ", n))] = true
		}
	}
	return out
}
