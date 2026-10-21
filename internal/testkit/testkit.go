// Package testkit supports end-to-end and differential (oracle) tests.
//
// A Sandbox is an isolated HOME plus a git project directory. Run executes the
// Go spec-kitty binary (built once per test binary); RunOracle executes the
// upstream Python CLI named by $SPEC_KITTY_ORACLE (tests skip when unset).
// Normalize masks run-specific values (ULIDs, timestamps, SHAs, temp paths)
// so outputs and files can be compared between the two implementations.
package testkit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// ModuleRoot returns the repository root (directory holding go.mod).
func ModuleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// Binary builds cmd/spec-kitty once and returns its path.
func Binary(t testing.TB) string {
	t.Helper()
	if p := os.Getenv("SPEC_KITTY_GO_BIN"); p != "" {
		return p
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sk-go-bin-")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "spec-kitty")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/spec-kitty")
		cmd.Dir = ModuleRoot()
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = &buildError{string(out)}
		}
	})
	if buildErr != nil {
		t.Fatalf("building spec-kitty: %v", buildErr)
	}
	return binPath
}

type buildError struct{ out string }

func (b *buildError) Error() string { return b.out }

// Oracle returns the upstream CLI path or skips the test.
func Oracle(t testing.TB) string {
	t.Helper()
	p := os.Getenv("SPEC_KITTY_ORACLE")
	if p == "" {
		t.Skip("SPEC_KITTY_ORACLE not set")
	}
	return p
}

// Sandbox is an isolated environment.
type Sandbox struct {
	T    testing.TB
	Base string // temp root
	Home string // isolated HOME
	Dir  string // project directory (cwd for commands)
	Env  []string
}

// NewSandbox creates HOME and an empty project dir (not yet a git repo).
func NewSandbox(t testing.TB) *Sandbox {
	t.Helper()
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	s := &Sandbox{T: t, Base: base, Home: filepath.Join(base, "home"), Dir: filepath.Join(base, "proj")}
	must(t, os.MkdirAll(s.Home, 0o755))
	must(t, os.MkdirAll(s.Dir, 0o755))
	return s
}

// NewGitSandbox creates a sandbox whose project is a git repo on main with
// one initial commit.
func NewGitSandbox(t testing.TB) *Sandbox {
	s := NewSandbox(t)
	s.Git("init", "-q", "-b", "main")
	s.Git("config", "user.name", "Test User")
	s.Git("config", "user.email", "test@example.com")
	s.Git("config", "commit.gpgsign", "false")
	s.WriteFile("README.md", "# demo\n")
	s.Git("add", "README.md")
	s.Git("commit", "-q", "-m", "initial commit")
	return s
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Environ returns the isolated environment for commands.
func (s *Sandbox) Environ() []string {
	env := []string{
		"HOME=" + s.Home,
		"PATH=" + os.Getenv("PATH"),
		"SPEC_KITTY_NO_UPGRADE_CHECK=1",
		"NO_COLOR=1",
		"TERM=dumb",
		"COLUMNS=200",
		"LANG=C.UTF-8",
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_NOSYSTEM=1",
		"XDG_CONFIG_HOME=" + filepath.Join(s.Home, ".config"),
		"TMPDIR=" + filepath.Join(s.Base, "tmp"),
	}
	os.MkdirAll(filepath.Join(s.Base, "tmp"), 0o755)
	return append(env, s.Env...)
}

// Result of a command.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// Exec runs an arbitrary executable inside the sandbox.
func (s *Sandbox) Exec(dir, exe string, args ...string) Result {
	s.T.Helper()
	if dir == "" {
		dir = s.Dir
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.Dir, dir)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = s.Environ()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			s.T.Fatalf("exec %s: %v", exe, err)
		}
	}
	return Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
}

// Run executes the Go binary in the project dir.
func (s *Sandbox) Run(args ...string) Result {
	s.T.Helper()
	return s.Exec("", Binary(s.T), args...)
}

// RunIn executes the Go binary in dir (relative to the project).
func (s *Sandbox) RunIn(dir string, args ...string) Result {
	s.T.Helper()
	return s.Exec(dir, Binary(s.T), args...)
}

// RunOracle executes the upstream CLI in the project dir.
func (s *Sandbox) RunOracle(args ...string) Result {
	s.T.Helper()
	return s.Exec("", Oracle(s.T), args...)
}

// MustRun runs the Go binary and fails on non-zero exit.
func (s *Sandbox) MustRun(args ...string) Result {
	s.T.Helper()
	r := s.Run(args...)
	if r.Code != 0 {
		s.T.Fatalf("spec-kitty %s: exit %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), r.Code, r.Stdout, r.Stderr)
	}
	return r
}

// Git runs git in the project dir and returns trimmed stdout.
func (s *Sandbox) Git(args ...string) string {
	s.T.Helper()
	r := s.Exec("", "git", args...)
	if r.Code != 0 {
		s.T.Fatalf("git %s: %s%s", strings.Join(args, " "), r.Stdout, r.Stderr)
	}
	return strings.TrimSpace(r.Stdout)
}

// WriteFile writes a project-relative file.
func (s *Sandbox) WriteFile(rel, content string) {
	s.T.Helper()
	p := filepath.Join(s.Dir, rel)
	must(s.T, os.MkdirAll(filepath.Dir(p), 0o755))
	must(s.T, os.WriteFile(p, []byte(content), 0o644))
}

// ReadFile reads a project-relative file ("" if missing).
func (s *Sandbox) ReadFile(rel string) string {
	b, err := os.ReadFile(filepath.Join(s.Dir, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

// Exists reports whether a project-relative path exists.
func (s *Sandbox) Exists(rel string) bool {
	_, err := os.Stat(filepath.Join(s.Dir, rel))
	return err == nil
}

// Files lists every file under root (relative, sorted), skipping .git.
func Files(root string) []string {
	var out []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if info.IsDir() && (info.Name() == ".git") {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

var (
	reULID = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)
	reMid8 = regexp.MustCompile(`-[0-9A-HJKMNP-TV-Z]{8}\b`)
	// reBareMid8 matches a standalone mid8 (ULIDs minted this century start "0").
	reBareMid8 = regexp.MustCompile(`\b0[0-9A-HJKMNP-TV-Z]{7}\b`)
	reTS       = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})?`)
	reSHA      = regexp.MustCompile(`\b[0-9a-f]{40}\b|\b[0-9a-f]{64}\b`)
	reShort    = regexp.MustCompile(`\b[0-9a-f]{7,12}\b`)
	reUUID     = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reDur      = regexp.MustCompile(`\b\d+\.\d+s\b`)
	rePid      = regexp.MustCompile(`"(shell_pid|pid)": \d+`)
)

// Normalize masks volatile values in s. Sandbox paths become <BASE>/<HOME>/<PROJ>.
func (s *Sandbox) Normalize(text string) string {
	text = strings.ReplaceAll(text, s.Dir, "<PROJ>")
	text = strings.ReplaceAll(text, s.Home, "<HOME>")
	text = strings.ReplaceAll(text, s.Base, "<BASE>")
	return NormalizeValues(text)
}

// NormalizeValues masks ULIDs, timestamps, hashes and UUIDs.
func NormalizeValues(text string) string {
	text = reUUID.ReplaceAllString(text, "<UUID>")
	text = reULID.ReplaceAllString(text, "<ULID>")
	text = reMid8.ReplaceAllString(text, "-<MID8>")
	// bare mid8 values ("mid8": "01M5DA9K", "(mid8 01M5DA9K)"): ULID-prefixed
	// 8-char tokens that contain a letter.
	text = reBareMid8.ReplaceAllStringFunc(text, func(m string) string {
		if strings.IndexFunc(m, func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0 {
			return "<MID8>"
		}
		return m
	})
	text = reTS.ReplaceAllString(text, "<TS>")
	text = reSHA.ReplaceAllString(text, "<SHA>")
	text = reShort.ReplaceAllStringFunc(text, func(m string) string {
		// only mask tokens that contain both digits and letters (git short shas)
		if strings.ContainsAny(m, "abcdef") && strings.ContainsAny(m, "0123456789") {
			return "<SHORTSHA>"
		}
		return m
	})
	text = reDur.ReplaceAllString(text, "<DUR>")
	text = rePid.ReplaceAllString(text, `"$1": <PID>`)
	return text
}
