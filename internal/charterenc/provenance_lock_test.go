package charterenc_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/charterenc"
	"github.com/spec-kitty/go-kitty3/internal/eventlog"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const (
	provChildRoot = "SK_TEST_PROVENANCE_ROOT"
	provChildID   = "SK_TEST_PROVENANCE_ID"
	provChildGo   = "SK_TEST_PROVENANCE_GO"
)

// TestHelperProvenanceChild is the child half of
// TestWriteProvenanceConcurrentProcesses: it waits for the start signal,
// then appends one record to the centralized log of its working directory.
// It does nothing in a normal run.
func TestHelperProvenanceChild(t *testing.T) {
	if os.Getenv(provChildRoot) == "" {
		t.Skip("child process helper")
	}
	if err := os.Chdir(os.Getenv(provChildRoot)); err != nil {
		t.Fatal(err)
	}
	signal := os.Getenv(provChildGo)
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
	failed := ""
	charterenc.Warn = func(m string) { failed = m }
	charterenc.WriteProvenance(&charterenc.Content{SourcePath: "charter-" + os.Getenv(provChildID) + ".md", SourceEncoding: "utf-8", Confidence: 1})
	if failed != "" {
		t.Fatal(failed)
	}
}

// TestWriteProvenanceConcurrentProcesses is the lost-update regression for
// _write_provenance: separate processes append one record each to the same
// .kittify/encoding-provenance/global.jsonl at once (its parents created by
// the first), and every record must survive as a whole line (upstream
// serialises the appends on the interim event-log lock).
func TestWriteProvenanceConcurrentProcesses(t *testing.T) {
	if os.Getenv(provChildRoot) != "" {
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
			signal := filepath.Join(root, "go")
			cmds := make([]*exec.Cmd, writers)
			for i := range cmds {
				c := exec.Command(os.Args[0], "-test.run=^TestHelperProvenanceChild$", "-test.count=1")
				c.Env = append(os.Environ(), provChildRoot+"="+root, provChildID+"="+strconv.Itoa(i), provChildGo+"="+signal)
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
			b, err := os.ReadFile(filepath.Join(root, ".kittify", "encoding-provenance", "global.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]int{}
			for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
				v, err := pyjson.Loads(line)
				if err != nil {
					t.Fatalf("torn or interleaved line %q: %v", line, err)
				}
				seen[v.(*jsonx.Object).GetString("file_path")]++
			}
			for i := 0; i < writers; i++ {
				if n := seen[fmt.Sprintf("charter-%d.md", i)]; n != 1 {
					t.Errorf("record of writer %d appears %d times", i, n)
				}
			}
			if len(seen) != writers {
				t.Errorf("%d of %d records survived", len(seen), writers)
			}
		})
	}
}

// TestWriteProvenanceTakesEventLogLock: a record waits for the writer that
// holds the log's event-log lock, and a torn log is refused (nothing
// written) with upstream's non-fatal warning.
func TestWriteProvenanceTakesEventLogLock(t *testing.T) {
	root := t.TempDir()
	md := filepath.Join(root, "kitty-specs", "001-demo")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(md, ".encoding-provenance.jsonl")
	var warned []string
	old := charterenc.Warn
	charterenc.Warn = func(m string) { warned = append(warned, m) }
	t.Cleanup(func() { charterenc.Warn = old })
	content := &charterenc.Content{SourcePath: filepath.Join(md, "spec.md"), SourceEncoding: "utf-8", Confidence: 1}

	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		if err := eventlog.WithLock(log, time.Second, func() error { close(held); <-release; return nil }); err != nil {
			t.Error(err)
		}
	}()
	<-held
	go func() { charterenc.WriteProvenance(content); close(done) }()
	select {
	case <-done:
		t.Fatal("the provenance append did not wait for the event-log lock")
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("the provenance log was written while another writer held its lock")
	}
	close(release)
	<-done
	b, err := os.ReadFile(log)
	if err != nil || strings.Count(string(b), "\n") != 1 || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("after the lock was released: %q, %v", b, err)
	}

	torn := append(b, `{"torn": `...)
	if err := os.WriteFile(log, torn, 0o644); err != nil {
		t.Fatal(err)
	}
	charterenc.WriteProvenance(content)
	if after, _ := os.ReadFile(log); string(after) != string(torn) {
		t.Errorf("a torn log was appended to: %q", after)
	}
	if want := "Failed to write encoding provenance to " + log; len(warned) != 1 || warned[0] != want {
		t.Errorf("warnings = %q, want [%q]", warned, want)
	}
}
