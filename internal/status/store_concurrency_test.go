package status

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const (
	appendChildPath = "SK_TEST_STATUS_APPEND_PATH"
	appendChildID   = "SK_TEST_STATUS_APPEND_ID"
	appendChildGo   = "SK_TEST_STATUS_APPEND_GO"
)

// TestHelperStatusAppendChild is the child half of
// TestAppendRowsConcurrentProcesses: it waits for the start signal, then
// appends one row through AppendRows. It does nothing in a normal run.
func TestHelperStatusAppendChild(t *testing.T) {
	path := os.Getenv(appendChildPath)
	if path == "" {
		t.Skip("child process helper")
	}
	signal := os.Getenv(appendChildGo)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(signal); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("start signal never arrived")
		}
		time.Sleep(time.Millisecond)
	}
	row := jsonx.NewObject("event_id", "child-"+os.Getenv(appendChildID), "kind", AnnotationKind)
	if err := AppendRows(path, row); err != nil {
		t.Fatal(err)
	}
}

// TestAppendRowsConcurrentProcesses is the lost-update regression: forty
// separate processes append one row each to the same status.events.jsonl at
// once, and every row must survive (upstream serialises the appends on the
// Mission status lock).
func TestAppendRowsConcurrentProcesses(t *testing.T) {
	if os.Getenv(appendChildPath) != "" {
		t.Skip("running as a child")
	}
	const writers = 40
	for _, tc := range []struct {
		name string
		git  bool
	}{{"git", true}, {"non-git", false}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.git {
				if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
					t.Fatalf("git init: %v %s", err, out)
				}
			}
			dir := filepath.Join(root, "kitty-specs", "001-demo")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := EventsPath(dir)
			signal := filepath.Join(root, "go")
			cmds := make([]*exec.Cmd, writers)
			for i := range cmds {
				c := exec.Command(os.Args[0], "-test.run=^TestHelperStatusAppendChild$", "-test.count=1")
				c.Env = append(os.Environ(),
					appendChildPath+"="+path, appendChildID+"="+strconv.Itoa(i), appendChildGo+"="+signal)
				var out strings.Builder
				c.Stdout, c.Stderr = &out, &out
				if err := c.Start(); err != nil {
					t.Fatal(err)
				}
				cmds[i] = c
				t.Cleanup(func() {
					if t.Failed() && out.Len() > 0 && !strings.Contains(out.String(), "PASS") {
						t.Logf("child %d output: %s", i, out.String())
					}
				})
			}
			time.Sleep(200 * time.Millisecond)
			if err := os.WriteFile(signal, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			for i, c := range cmds {
				if err := c.Wait(); err != nil {
					t.Errorf("child %d: %v", i, err)
				}
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]int{}
			for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
				v, err := pyjson.Loads(line)
				if err != nil {
					t.Fatalf("torn or interleaved line %q: %v", line, err)
				}
				seen[v.(*jsonx.Object).GetString("event_id")]++
			}
			for i := 0; i < writers; i++ {
				if n := seen[fmt.Sprintf("child-%d", i)]; n != 1 {
					t.Errorf("row of writer %d appears %d times", i, n)
				}
			}
			if len(seen) != writers {
				t.Errorf("%d of %d rows survived", len(seen), writers)
			}
		})
	}
}
