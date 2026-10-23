package writecheckout_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/writecheckout"
)

func TestMissionLockConvergesAcrossSurfaces(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	const meta = `{"mission_slug":"example","mission_id":"01M54Y16550EHN17T75926T0NP","mid8":"01M54Y16","coordination_branch":"kitty/mission-example-01M54Y16"}`
	s.WriteFile("kitty-specs/example/meta.json", meta)
	s.WriteFile("other/kitty-specs/example-01M54Y16/meta.json", meta)
	release, err := writecheckout.AcquireMissionLock(s.Dir, filepath.Join(s.Dir, "kitty-specs/example"), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = writecheckout.AcquireMissionLock(s.Dir, filepath.Join(s.Dir, "other/kitty-specs/example-01M54Y16"), 0)
	var timeout *writecheckout.LockTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("second surface must contend on the same lock: %v", err)
	}
	release()
	release, err = writecheckout.AcquireMissionLock(s.Dir, filepath.Join(s.Dir, "other/kitty-specs/example-01M54Y16"), 0)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
