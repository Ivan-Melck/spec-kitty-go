package projection

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteAtomicCreatesAndReplacesWithExactMode(t *testing.T) {
	root := t.TempDir()
	if err := WriteAtomic(root, "tasks.md", []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(root, "tasks.md"), []byte("first\n"), 0o600)
	if err := WriteAtomic(root, "tasks.md", []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(root, "tasks.md"), []byte("second\n"), 0o644)
	assertNoTemporaryFiles(t, root)
}

func TestWriteAtomicRejectsUntrustedPathsBeforeMutation(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "safe"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("safe", filepath.Join(root, "inside-link")); err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(outsideTarget, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideTarget, filepath.Join(root, "destination-link")); err != nil {
		t.Fatal(err)
	}
	for name := range map[string]struct{}{"": {}, ".": {}, "../escape.md": {}, "a/../../escape.md": {}, "/absolute.md": {}, "safe\\windows.md": {}, "bad\x00name": {}, "missing/tasks.md": {}, "outside-link/tasks.md": {}, "inside-link/tasks.md": {}, "destination-link": {}, "safe": {}} {
		t.Run(fmt.Sprintf("%x", name), func(t *testing.T) {
			err := WriteAtomic(root, name, []byte("new"), 0o600)
			assertKind(t, err, KindUnsafeOutputPath)
		})
	}
	if actual, err := os.ReadFile(filepath.Join(root, "tasks.md")); err != nil || string(actual) != "old" {
		t.Fatalf("trusted target changed: %q %v", actual, err)
	}
	if actual, err := os.ReadFile(outsideTarget); err != nil || string(actual) != "outside" {
		t.Fatalf("outside target changed: %q %v", actual, err)
	}
	rootLink := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}
	assertKind(t, WriteAtomic(rootLink, "tasks.md", []byte("new"), 0o600), KindUnsafeOutputPath)
	for _, mode := range []os.FileMode{0, 0o666, 0o755, os.ModeSetuid | 0o600} {
		assertKind(t, WriteAtomic(root, "mode.md", []byte("new"), mode), KindUnsafeOutputPath)
	}
}

func TestWriteAtomicFaultsPreserveCompleteOldOrNewAndCleanup(t *testing.T) {
	stages := []fileStage{stageCreate, stageChmod, stageWrite, stageFileSync, stageClose, stageRename, stageDirectorySync}
	for _, failedStage := range stages {
		t.Run(string(failedStage), func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "tasks.md")
			old := []byte(strings.Repeat("old", 4096))
			replacement := []byte(strings.Repeat("new", 8192))
			if err := os.WriteFile(target, old, 0o600); err != nil {
				t.Fatal(err)
			}
			secret := "credential-DO-NOT-LEAK"
			err := writeAtomic(root, "tasks.md", replacement, 0o600, func(stage fileStage) error {
				if stage == failedStage {
					return errors.New(secret)
				}
				return nil
			})
			if err == nil {
				t.Fatal("fault did not fail")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), string(replacement[:32])) {
				t.Fatalf("diagnostic leaked supplied data: %v", err)
			}
			actual, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := old
			if failedStage == stageDirectorySync {
				want = replacement
			}
			if !bytes.Equal(actual, want) {
				t.Fatalf("stage %s exposed partial/mixed data", failedStage)
			}
			assertNoTemporaryFiles(t, root)
		})
	}
}

func TestWriteAtomicConcurrentWritersAndReadersSeeCompleteValues(t *testing.T) {
	root := t.TempDir()
	old := append([]byte("old:"), bytes.Repeat([]byte("o"), 128<<10)...)
	if err := os.WriteFile(filepath.Join(root, "tasks.md"), old, 0o600); err != nil {
		t.Fatal(err)
	}
	values := [][]byte{old}
	for i := 0; i < 16; i++ {
		values = append(values, append([]byte(fmt.Sprintf("value-%02d:", i)), bytes.Repeat([]byte{byte('a' + i)}, 128<<10)...))
	}
	valid := func(value []byte) bool {
		for _, candidate := range values {
			if bytes.Equal(value, candidate) {
				return true
			}
		}
		return false
	}
	start := make(chan struct{})
	readerDone := make(chan struct{})
	readerErrors := make(chan error, 1)
	go func() {
		defer close(readerDone)
		<-start
		for i := 0; i < 500; i++ {
			value, err := os.ReadFile(filepath.Join(root, "tasks.md"))
			if err != nil {
				select {
				case readerErrors <- err:
				default:
				}
				return
			}
			if !valid(value) {
				select {
				case readerErrors <- errors.New("reader observed partial or mixed bytes"):
				default:
				}
				return
			}
		}
	}()
	var writers sync.WaitGroup
	for i := 1; i < len(values); i++ {
		value := values[i]
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			if err := WriteAtomic(root, "tasks.md", value, 0o600); err != nil {
				select {
				case readerErrors <- err:
				default:
				}
			}
		}()
	}
	close(start)
	writers.Wait()
	<-readerDone
	select {
	case err := <-readerErrors:
		t.Fatal(err)
	default:
	}
	final, err := os.ReadFile(filepath.Join(root, "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !valid(final) || bytes.Equal(final, old) {
		t.Fatal("final value is not one complete replacement")
	}
	assertNoTemporaryFiles(t, root)
}

func TestWriteAtomicCreatesNestedExistingDestinationOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "kitty-specs", "mission"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(root, "kitty-specs/mission/tasks.md", []byte("projection\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(root, "kitty-specs", "mission", "tasks.md"), []byte("projection\n"), 0o600)
}

func assertFile(t *testing.T, name string, want []byte, mode os.FileMode) {
	t.Helper()
	actual, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, want) {
		t.Fatalf("got %q want %q", actual, want)
	}
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("mode=%o want=%o", info.Mode().Perm(), mode)
	}
}
func assertNoTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	var matches []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".spec-kitty-projection-") && strings.HasSuffix(entry.Name(), ".tmp") {
			matches = append(matches, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) > 0 {
		t.Fatalf("temporary files leaked: %v", matches)
	}
}
