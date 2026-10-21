package tracker_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestExplore runs an exploration script (TRACKER_EXPLORE=<file>) line by
// line in a fresh oracle-initialised sandbox and writes the transcript next
// to the script (<file>.out). Development aid only; skipped otherwise.
func TestExplore(t *testing.T) {
	script := os.Getenv("TRACKER_EXPLORE")
	if script == "" {
		t.Skip("TRACKER_EXPLORE not set")
	}
	oracle := testkit.Oracle(t)
	s := testkit.NewGitSandbox(t)
	if os.Getenv("TRACKER_EXPLORE_INIT") != "" {
		missionfx.OracleInit(t, s)
	}
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out.WriteString("$ " + line + "\n")
		cmd := exec.Command("sh", "-c", line)
		cmd.Dir = s.Dir
		cmd.Env = append(s.Environ(), "O="+oracle, "G="+testkit.Binary(t), "H="+s.Home, "P="+s.Dir, "BIN="+filepath.Dir(script))
		b, err := cmd.CombinedOutput()
		out.Write(b)
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		out.WriteString("[rc=" + itoa(code) + "]\n")
	}
	if err := os.WriteFile(script+".out", []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
