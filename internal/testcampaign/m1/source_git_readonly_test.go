package m1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// These fixtures are actual shared Git checkouts; no provider or invented
// source binding participates in the observation.
func sourceGitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	sourceGitRun(t, root, "init", "-q")
	sourceGitRun(t, root, "config", "user.name", "Source Proof")
	sourceGitRun(t, root, "config", "user.email", "proof@example.invalid")
	sourceGitWrite(t, filepath.Join(root, "tracked"), "original\n")
	sourceGitWrite(t, filepath.Join(root, ".gitignore"), "ignored\n")
	sourceGitRun(t, root, "add", ".")
	sourceGitRun(t, root, "commit", "-qm", "original")
	return root
}

func sourceGitRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = sourceGitTestEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func sourceGitTestEnv() []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			env = append(env, value)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
}

func sourceGitWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

type sourceGitSnapshot struct {
	Head    string
	Index   []byte
	Objects map[string][32]byte
}

func sourceGitSnap(t *testing.T, root string) sourceGitSnapshot {
	t.Helper()
	index, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := sourceGitSnapshot{Head: sourceGitRun(t, root, "rev-parse", "HEAD"), Index: index, Objects: make(map[string][32]byte)}
	err = filepath.WalkDir(filepath.Join(root, ".git", "objects"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot.Objects[path] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func sourceGitAccepted(t *testing.T, root string) {
	t.Helper()
	commit, tree, indexTree, digest, err := observeGitSource(t.Context(), root)
	if err != nil {
		t.Fatalf("clean source refused: %v", err)
	}
	if commit != sourceGitRun(t, root, "rev-parse", "HEAD") || tree != sourceGitRun(t, root, "rev-parse", commit+"^{tree}") || indexTree != tree || digest != emptySHA256 {
		t.Fatalf("incorrect verified source: %q %q %q %q", commit, tree, indexTree, digest)
	}
}

func sourceGitRefused(t *testing.T, ctx context.Context, root string) error {
	t.Helper()
	commit, tree, indexTree, digest, err := observeGitSource(ctx, root)
	if err == nil || commit != "" || tree != "" || indexTree != "" || digest != "" {
		t.Fatalf("source refusal leaked a partial proof: %q %q %q %q err=%v", commit, tree, indexTree, digest, err)
	}
	return err
}

func TestSourceGitReadOnlyHeldIndexLock(t *testing.T) {
	root := sourceGitFixture(t)
	before := sourceGitSnap(t, root)
	sourceGitWrite(t, filepath.Join(root, ".git", "index.lock"), "owned by another metadata writer\n")
	sourceGitAccepted(t, root)
	if after := sourceGitSnap(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("observation changed original HEAD, index bytes, or object inventory")
	}
	lock, err := os.ReadFile(filepath.Join(root, ".git", "index.lock"))
	if err != nil || string(lock) != "owned by another metadata writer\n" {
		t.Fatal("observer touched another writer's lock")
	}
}

func TestSourceGitReadOnlyRejectsExecutableModeDriftUnderLocalFalse(t *testing.T) {
	root := sourceGitFixture(t)
	sourceGitRun(t, root, "config", "core.filemode", "false")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.filemode")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	// Exact content and mode remain admissible despite the concealing option.
	sourceGitAccepted(t, root)
	tracked := filepath.Join(root, "tracked")
	if err := os.Chmod(tracked, 0700); err != nil {
		t.Fatal(err)
	}
	before := sourceGitSnap(t, root)
	sourceGitRefused(t, t.Context(), root)
	if after := sourceGitSnap(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("mode-drift refusal changed original HEAD, index bytes, or object inventory")
	}
}

func TestSourceGitReadOnlyConcurrentObservations(t *testing.T) {
	root := sourceGitFixture(t)
	// Force status to inspect a changed stat cache without changing content.
	tracked := filepath.Join(root, "tracked")
	sourceGitWrite(t, tracked, "original\n")
	before := sourceGitSnap(t, root)
	lock := filepath.Join(root, ".git", "index.lock")
	sourceGitWrite(t, lock, "metadata writer")
	stop, ready := make(chan struct{}), make(chan struct{})
	writerResult := make(chan error, 1)
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		close(ready)
		transitions := 0
		for {
			select {
			case <-stop:
				if transitions == 0 {
					writerResult <- fmt.Errorf("transient lock writer never ran")
				} else {
					writerResult <- nil
				}
				return
			default:
			}
			if err := os.Remove(lock); err != nil {
				writerResult <- err
				return
			}
			if err := os.WriteFile(lock, []byte("metadata writer"), 0600); err != nil {
				writerResult <- err
				return
			}
			transitions++
		}
	}()
	<-ready
	const observers = 12
	results := make(chan error, observers)
	var readers sync.WaitGroup
	for range observers {
		readers.Add(1)
		go func() {
			defer readers.Done()
			// Exactly one observation per goroutine: no retry-to-green.
			commit, tree, indexTree, digest, err := observeGitSource(t.Context(), root)
			if err == nil && (commit != before.Head || tree == "" || tree != indexTree || digest != emptySHA256) {
				err = fmt.Errorf("invalid proof")
			}
			results <- err
		}()
	}
	readers.Wait()
	close(stop)
	writer.Wait()
	if err := <-writerResult; err != nil {
		t.Errorf("transient metadata lock writer: %v", err)
	}
	close(results)
	count := 0
	for err := range results {
		count++
		if err != nil {
			t.Errorf("one-shot observation: %v", err)
		}
	}
	if count != observers {
		t.Fatalf("observations=%d want=%d", count, observers)
	}
	if after := sourceGitSnap(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("concurrent readers changed original source metadata")
	}
}

func TestSourceGitReadOnlyRefusals(t *testing.T) {
	for _, kind := range []string{"staged", "unstaged", "untracked", "ignored", "unmerged", "different-index", "skip-worktree", "assume-unchanged", "cancel", "missing-head", "malicious-environment"} {
		t.Run(kind, func(t *testing.T) {
			root := sourceGitFixture(t)
			ctx := t.Context()
			switch kind {
			case "staged", "unstaged", "different-index", "malicious-environment":
				sourceGitWrite(t, filepath.Join(root, "tracked"), "changed\n")
				if kind != "unstaged" {
					sourceGitRun(t, root, "add", "tracked")
				}
				if kind == "different-index" {
					sourceGitWrite(t, filepath.Join(root, "tracked"), "original\n")
				}
				if kind == "malicious-environment" {
					other := sourceGitFixture(t)
					t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
					t.Setenv("GIT_WORK_TREE", other)
					t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
					t.Setenv("GIT_CONFIG_COUNT", "1")
					t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
					t.Setenv("GIT_CONFIG_VALUE_0", other)
				}
			case "untracked", "ignored":
				sourceGitWrite(t, filepath.Join(root, kind), "any bytes\n")
			case "skip-worktree", "assume-unchanged":
				sourceGitRun(t, root, "update-index", "--"+kind, "tracked")
				// Reject even before hidden bytes are introduced.
				sourceGitRefused(t, ctx, root)
				sourceGitWrite(t, filepath.Join(root, "tracked"), "hidden\n")
			case "unmerged":
				blob := sourceGitRun(t, root, "rev-parse", "HEAD:tracked")
				sourceGitRun(t, root, "update-index", "--force-remove", "tracked")
				cmd := exec.Command("git", "-C", root, "update-index", "--index-info")
				cmd.Env = sourceGitTestEnv()
				cmd.Stdin = strings.NewReader(fmt.Sprintf("100644 %s 1\ttracked\n100644 %s 2\ttracked\n", blob, blob))
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("unmerged index: %v %s", err, out)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing-head":
				sourceGitRun(t, root, "update-ref", "-d", "HEAD")
			}
			sourceGitRefused(t, ctx, root)
		})
	}
}

func TestSourceGitReadOnlyIgnoresEnvironmentRetargetingAndReplacements(t *testing.T) {
	root := sourceGitFixture(t)
	original := sourceGitRun(t, root, "rev-parse", "HEAD")
	sourceGitWrite(t, filepath.Join(root, "tracked"), "replacement\n")
	sourceGitRun(t, root, "add", ".")
	sourceGitRun(t, root, "commit", "-qm", "replacement")
	replacement := sourceGitRun(t, root, "rev-parse", "HEAD")
	sourceGitRun(t, root, "checkout", "--detach", "-q", original)
	sourceGitRun(t, root, "replace", original, replacement)
	other := sourceGitFixture(t)
	sourceGitWrite(t, filepath.Join(other, "tracked"), "foreign dirty content\n")
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_OPTIONAL_LOCKS", "1")
	t.Setenv("GIT_NO_REPLACE_OBJECTS", "0")
	sourceGitAccepted(t, root)
	// A replacement cannot make dirty original bytes pass as a clean source.
	sourceGitWrite(t, filepath.Join(root, "tracked"), "replacement\n")
	sourceGitRefused(t, t.Context(), root)
}

// The shim is confined to this test's PATH. It executes real Git, records
// commands, and mutates only after the first full census has been read.
func sourceGitShim(t *testing.T, mode, target string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "commands")
	script := `#!/bin/sh
if [ "${GIT_CONFIG_COUNT-}" != "" ]; then
  if [ "$GIT_CONFIG_COUNT" != 3 ] || [ "$GIT_CONFIG_KEY_2" != core.filemode ] || [ "$GIT_CONFIG_VALUE_2" != true ]; then
    printf 'source mode configuration is not controlled\n' >&2
    exit 97
  fi
fi
printf '%s\n' "$*" >> "$SOURCE_PROOF_LOG"
if [ "$3" = ls-files ] && [ "$4" = --others ] && [ ! -e "$SOURCE_PROOF_ONCE" ]; then
  "$SOURCE_PROOF_REAL_GIT" "$@"
  result=$?
  : > "$SOURCE_PROOF_ONCE"
  if [ "$SOURCE_PROOF_MODE" = move ] || [ "$SOURCE_PROOF_MODE" = move-same-tree ]; then
    "$SOURCE_PROOF_REAL_GIT" -C "$2" checkout --detach -q "$SOURCE_PROOF_TARGET" || exit 99
  elif [ "$SOURCE_PROOF_MODE" = flags ]; then
    "$SOURCE_PROOF_REAL_GIT" -C "$2" update-index --skip-worktree tracked || exit 99
  elif [ "$SOURCE_PROOF_MODE" = dirty ]; then
    printf 'new bytes\n' > "$2/untracked"
  elif [ "$SOURCE_PROOF_MODE" = ignored ]; then
    printf 'new bytes\n' > "$2/ignored"
  elif [ "$SOURCE_PROOF_MODE" = index ]; then
    printf 'new bytes\n' > "$2/tracked"
    "$SOURCE_PROOF_REAL_GIT" -C "$2" add tracked || exit 99
    printf 'original\n' > "$2/tracked"
  fi
  exit "$result"
fi
if [ "$3" = diff-index ] && [ -e "$SOURCE_PROOF_ONCE" ]; then
  if [ "$SOURCE_PROOF_MODE" = error ]; then
    printf 'controlled index inspection failure\n' >&2
    exit 128
  elif [ "$SOURCE_PROOF_MODE" = huge-error ]; then
    i=0
    while [ "$i" -lt 2000 ]; do printf 'bounded diagnostic filler\n' >&2; i=$((i + 1)); done
    exit 128
  fi
fi
exec "$SOURCE_PROOF_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOURCE_PROOF_REAL_GIT", realGit)
	t.Setenv("SOURCE_PROOF_LOG", log)
	t.Setenv("SOURCE_PROOF_ONCE", filepath.Join(dir, "once"))
	t.Setenv("SOURCE_PROOF_MODE", mode)
	t.Setenv("SOURCE_PROOF_TARGET", target)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestSourceGitReadOnlyEndRechecksAndDiagnostics(t *testing.T) {
	for _, mode := range []string{"noop", "move", "move-same-tree", "flags", "dirty", "ignored", "index", "error", "huge-error"} {
		t.Run(mode, func(t *testing.T) {
			root := sourceGitFixture(t)
			original := sourceGitRun(t, root, "rev-parse", "HEAD")
			if mode != "move-same-tree" {
				sourceGitWrite(t, filepath.Join(root, "tracked"), "second commit\n")
				sourceGitRun(t, root, "add", ".")
			}
			sourceGitRun(t, root, "commit", "--allow-empty", "-qm", "second")
			target := sourceGitRun(t, root, "rev-parse", "HEAD")
			sourceGitRun(t, root, "checkout", "--detach", "-q", original)
			log := sourceGitShim(t, mode, target)
			if mode == "noop" {
				sourceGitAccepted(t, root)
			} else {
				err := sourceGitRefused(t, t.Context(), root)
				if mode == "move-same-tree" && !strings.Contains(err.Error(), "source commit changed") {
					t.Fatalf("missing final immutable commit recheck: %v", err)
				}
				if mode == "error" && !strings.Contains(err.Error(), "controlled index inspection failure") {
					t.Fatalf("missing bounded Git stderr: %v", err)
				}
				if mode == "huge-error" && (len(err.Error()) > 4600 || !strings.Contains(err.Error(), "bounded diagnostic filler")) {
					t.Fatalf("unbounded or absent diagnostic: length=%d", len(err.Error()))
				}
			}
			commands, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(commands, []byte("write-tree")) {
				t.Fatal("source observer used write-capable tree acquisition")
			}
			if mode == "noop" && bytes.Count(commands, []byte(" diff-index ")) != 2 {
				t.Fatalf("missing exactly two one-shot index proofs: %s", commands)
			}
		})
	}
}
