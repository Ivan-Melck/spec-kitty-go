package workflow

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/missionwrite"
)

const tRow = `{"event_id": "01BBBBBBBBBBBBBBBBBBBBBBBB", "wp_id": "WP01"}` + "\n"

func TestHandleCommitFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	rb := missionwrite.Capture(dir)
	os.WriteFile(filepath.Join(dir, "status.events.jsonl"), []byte(tRow), 0o644)
	var out, errw bytes.Buffer
	rc := &Receipts{}
	err := handleCommitFailure(NewOut(&out, &errw), rc, dir, errors.New("boom"), "main", "chore: m", "WP01", "op", rb,
		"Error: Failed to commit workflow status update for WP01", true)
	if e, ok := err.(*Exit); !ok || e.Code != 1 {
		t.Fatalf("err = %v", err)
	}
	if got := out.String(); got != "Error: Failed to commit workflow status update for WP01: boom. Event log rolled back to pre-emit state.\n" {
		t.Fatalf("stdout %q", got)
	}
	if rc.list[0].Outcome != "refused" {
		t.Fatalf("receipt %+v", rc.list[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "status.events.jsonl")); err == nil {
		t.Fatal("log survived the rollback")
	}
}

func TestHandleCommitFailureCommittedClaim(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	rb := missionwrite.Capture(dir)
	os.WriteFile(filepath.Join(dir, "status.events.jsonl"), []byte(tRow), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@e", "commit", "-q", "-m", "c"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, o)
		}
	}
	var out, errw bytes.Buffer
	rc := &Receipts{}
	handleCommitFailure(NewOut(&out, &errw), rc, dir, errors.New("boom"), "main", "chore: m", "WP01", "for_review -> in_review for WP01", rb, "Error: x", false)
	if got := out.String(); got != "WP01 claim was committed; the follow-up for_review -> in_review for WP01 commit failed: boom\n" {
		t.Fatalf("stdout %q", got)
	}
	if !strings.HasPrefix(errw.String(), "WARNING  STATUS_ROLLBACK_REFUSED: the rows after the capture point") {
		t.Fatalf("stderr %q", errw.String())
	}
	if r := rc.list[0]; r.Outcome != "committed" || len(r.SHA) != 40 {
		t.Fatalf("receipt %+v", r)
	}
}
