package m2

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAttemptReservationCaptureAndTerminalAreImmutable(t *testing.T) {
	root := t.TempDir()
	source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40), GoVersion: "go1.26.7", GoModSHA256: stringOf('c', 64), GoSumSHA256: stringOf('d', 64)}
	attempt, err := ReserveAttempt(root, "attempt-one", source)
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.Capture("raw/receipt.json", []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if err := attempt.Capture("raw/receipt.json", []byte("one\n")); err != nil {
		t.Fatal("byte-identical capture must be idempotent:", err)
	}
	if err := attempt.Capture("raw/receipt.json", []byte("two\n")); err == nil {
		t.Fatal("attempt receipt was overwritten")
	}
	if err := attempt.SealNonPass("fail", errors.New("injected boundary failure")); err != nil {
		t.Fatal(err)
	}
	terminal, err := VerifyNonPassAttempt(filepath.Join(root, "attempt-one", "evidence"))
	if err != nil || terminal.Verdict != "fail" {
		t.Fatalf("terminal=%+v err=%v", terminal, err)
	}
	if err := attempt.Record("after-seal", "pass", ""); err == nil {
		t.Fatal("post-seal event mutation succeeded")
	}
	if err := attempt.Capture("after-seal", []byte("x")); err == nil {
		t.Fatal("post-seal artifact mutation succeeded")
	}
}

func TestAttemptRunIDReservationIsExclusive(t *testing.T) {
	root := t.TempDir()
	source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40), GoVersion: "go1.26.7", GoModSHA256: stringOf('c', 64), GoSumSHA256: stringOf('d', 64)}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := ReserveAttempt(root, "one-run", source)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful reservations=%d want=1", successes)
	}
	if _, err := os.Stat(filepath.Join(root, ".run-one-run.lock")); err != nil {
		t.Fatal(err)
	}
}
