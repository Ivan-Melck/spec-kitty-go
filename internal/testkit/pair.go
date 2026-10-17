package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pair runs identical command sequences against the Go binary and the
// oracle in two sandboxes prepared the same way, for differential tests.
type Pair struct {
	T      testing.TB
	Go     *Sandbox
	Oracle *Sandbox
}

// NewPair creates two git sandboxes (skips when no oracle is configured).
func NewPair(t testing.TB) *Pair {
	Oracle(t)
	return &Pair{T: t, Go: NewGitSandbox(t), Oracle: NewGitSandbox(t)}
}

// Both applies fn to both sandboxes (e.g. to write the same fixture files).
func (p *Pair) Both(fn func(s *Sandbox)) {
	fn(p.Go)
	fn(p.Oracle)
}

// Run executes args in both and returns (go, oracle) results.
func (p *Pair) Run(args ...string) (Result, Result) {
	p.T.Helper()
	return p.Go.Run(args...), p.Oracle.RunOracle(args...)
}

// RunIn executes args in dir (project-relative) in both.
func (p *Pair) RunIn(dir string, args ...string) (Result, Result) {
	p.T.Helper()
	return p.Go.Exec(dir, Binary(p.T), args...), p.Oracle.Exec(dir, Oracle(p.T), args...)
}

// Compare runs args in both and reports differences in exit code and
// normalized stdout (stderr compared only when compareStderr).
func (p *Pair) Compare(compareStderr bool, args ...string) (Result, Result) {
	p.T.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		p.T.Errorf("%s: exit code go=%d oracle=%d\n--- go stderr:\n%s\n--- oracle stderr:\n%s", strings.Join(args, " "), g.Code, o.Code, g.Stderr, o.Stderr)
	}
	gs, os_ := p.Go.Normalize(g.Stdout), p.Oracle.Normalize(o.Stdout)
	if gs != os_ {
		p.T.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), Diff(os_, gs))
	}
	if compareStderr {
		ge, oe := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr)
		if ge != oe {
			p.T.Errorf("%s: stderr differs\n%s", strings.Join(args, " "), Diff(oe, ge))
		}
	}
	return g, o
}

// CompareTree compares the file lists (and optionally normalized contents)
// under rel in both projects. ignore holds path prefixes to skip.
func (p *Pair) CompareTree(rel string, contents bool, ignore ...string) {
	p.T.Helper()
	gr, or := filepath.Join(p.Go.Dir, rel), filepath.Join(p.Oracle.Dir, rel)
	gf, of := filter(Files(gr), ignore), filter(Files(or), ignore)
	if strings.Join(gf, "\n") != strings.Join(of, "\n") {
		p.T.Errorf("tree %s differs (oracle vs go):\n%s", rel, Diff(strings.Join(of, "\n"), strings.Join(gf, "\n")))
		return
	}
	if !contents {
		return
	}
	for _, f := range gf {
		gb, _ := os.ReadFile(filepath.Join(gr, f))
		ob, _ := os.ReadFile(filepath.Join(or, f))
		gn, on := p.Go.Normalize(string(gb)), p.Oracle.Normalize(string(ob))
		if gn != on {
			p.T.Errorf("file %s differs:\n%s", filepath.Join(rel, f), Diff(on, gn))
		}
	}
}

func filter(files, ignore []string) []string {
	var out []string
outer:
	for _, f := range files {
		for _, ig := range ignore {
			if strings.HasPrefix(f, ig) {
				continue outer
			}
		}
		out = append(out, f)
	}
	return out
}

// Diff renders a small line diff (want=oracle, got=go).
func Diff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	n := len(wl)
	if len(gl) > n {
		n = len(gl)
	}
	shown := 0
	for i := 0; i < n && shown < 40; i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			fmt.Fprintf(&b, "line %d:\n  oracle: %q\n  go:     %q\n", i+1, w, g)
			shown++
		}
	}
	if shown == 0 {
		return "(no line differences)"
	}
	return b.String()
}
