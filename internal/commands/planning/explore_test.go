package planning_test

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestExplore is a developer aid (skipped unless SK_EXPLORE_SCRIPT is set):
// it initialises a sandbox project with the oracle, then runs each line of
// the script with bash inside it ($SK = oracle, $GOSK = Go binary, $TD =
// missionfx testdata) and writes a transcript to SK_EXPLORE_OUT.
func TestExplore(t *testing.T) {
	script := os.Getenv("SK_EXPLORE_SCRIPT")
	if script == "" {
		t.Skip("SK_EXPLORE_SCRIPT not set")
	}
	oracle := testkit.Oracle(t)
	s := testkit.NewGitSandbox(t)
	missionfx.SeedRepo(s)
	if os.Getenv("SK_EXPLORE_NOINIT") == "" {
		missionfx.OracleInit(t, s)
	}
	s.Env = append(s.Env, "SK="+oracle, "GOSK="+testkit.Binary(t), "TD="+missionfx.Path(""))
	f, err := os.Open(script)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		r := s.Exec("", "bash", "-c", line)
		fmt.Fprintf(&out, "$ %s\n[rc=%d]\n%s", line, r.Code, r.Stdout)
		if r.Stderr != "" {
			fmt.Fprintf(&out, "--- stderr:\n%s", r.Stderr)
		}
		out.WriteString("\n")
	}
	dest := os.Getenv("SK_EXPLORE_OUT")
	if dest == "" {
		t.Log(out.String())
		return
	}
	text := strings.NewReplacer(s.Dir, "<PROJ>", s.Home, "<HOME>", s.Base, "<BASE>").Replace(out.String())
	if err := os.WriteFile(dest, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
