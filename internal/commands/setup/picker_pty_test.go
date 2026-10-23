//go:build linux

package setup_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// openPTY opens a pseudo-terminal pair with plain syscalls.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Skipf("ptsname: %v", e)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	ws := struct{ row, col, x, y uint16 }{40, 80, 0, 0}
	syscall.Syscall(syscall.SYS_IOCTL, s.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	return m, s
}

func runPicker(t *testing.T, keys ...string) (string, *testkit.Sandbox, int) {
	return runPickerWith(t, testkit.Binary(t), keys...)
}

func runPickerWith(t *testing.T, bin string, keys ...string) (string, *testkit.Sandbox, int) {
	return runPickerEnv(t, bin, false, keys...)
}

// runPickerEnv drives init's picker in a PTY; colour=true drops NO_COLOR so
// rich renders colours as on a real terminal.
func runPickerEnv(t *testing.T, bin string, colour bool, keys ...string) (string, *testkit.Sandbox, int) {
	return runPTY(t, bin, colour, true, []string{"init", "proj"}, keys...)
}

// runPTY runs bin args in a PTY (NO_COLOR dropped when colour); picker
// waits for the agent picker before sending keys.
func runPTY(t *testing.T, bin string, colour, picker bool, args []string, keys ...string) (string, *testkit.Sandbox, int) {
	return runPTYIn(t, testkit.NewSandbox(t), bin, colour, picker, args, keys...)
}

func runPTYIn(t *testing.T, s *testkit.Sandbox, bin string, colour, picker bool, args []string, keys ...string) (string, *testkit.Sandbox, int) {
	m, sl := openPTY(t)
	defer m.Close()
	cmd := exec.Command(bin, args...)
	cmd.Dir = s.Dir
	env := s.Environ()
	if colour {
		var kept []string
		for _, e := range env {
			if !strings.HasPrefix(e, "NO_COLOR=") {
				kept = append(kept, e)
			}
		}
		env = kept
	}
	env = append(env, "TERM=xterm", "COLUMNS=80")
	if picker {
		env = append(env, "SPEC_KITTY_GO_SKIP_BOOTSTRAP=1")
	}
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = sl, sl, sl
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sl.Close()
	outc := make(chan []byte, 1)
	var mu sync.Mutex
	var live []byte
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := m.Read(b)
			mu.Lock()
			live = append(live, b[:n]...)
			mu.Unlock()
			if err != nil {
				break
			}
		}
		mu.Lock()
		outc <- live
		mu.Unlock()
	}()
	// Keys go out only once the panel is drawn (the oracle starts slowly).
	frames := func() int {
		mu.Lock()
		defer mu.Unlock()
		return strings.Count(string(live), "Use ↑/↓ to move")
	}
	for deadline := time.Now().Add(60 * time.Second); picker && frames() == 0 && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
	}
	codes := map[string]string{"up": "\x1b[A", "down": "\x1b[B", "space": " ", "enter": "\r", "esc": "\x1b\x1b", "ctrlc": "\x03"}
	for i, k := range keys {
		m.Write([]byte(codes[k]))
		for deadline := time.Now().Add(5 * time.Second); frames() < i+2 && k != "enter" && k != "esc" && time.Now().Before(deadline); {
			time.Sleep(50 * time.Millisecond)
		}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		cmd.Process.Kill()
		t.Fatal("init did not finish")
	}
	var out []byte
	select {
	case out = <-outc:
	case <-time.After(5 * time.Second):
	}
	return string(out), s, cmd.ProcessState.ExitCode()
}

// MAINT-08: the arrow-key agent picker (TTY), driven through a real PTY.
func TestInitArrowKeyPicker(t *testing.T) {
	out, s, code := runPicker(t, "down", "space", "up", "space", "enter")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	plainOut := reCSI.ReplaceAllString(out, "")
	if !strings.Contains(plainOut, "╭──────────────────────── Choose your AI assistant(s): ────────────────────────╮") ||
		!strings.Contains(plainOut, "│  ▶    ☑ copilot (GitHub Copilot)") ||
		!strings.Contains(plainOut, "Use ↑/↓ to move, Space to toggle, Enter to confirm, Esc to cancel") {
		t.Errorf("panel not drawn:\n%s", out)
	}
	// On a terminal rich highlights "assistant(s)" (bold call and parens),
	// so match the text with the SGR codes stripped.
	if !strings.Contains(reCSI.ReplaceAllString(out, ""), "Selected AI assistant(s): Claude Code") {
		t.Errorf("selection (copilot toggled off, claude on) not reported:\n%s", out)
	}
	cfg, _ := os.ReadFile(filepath.Join(s.Dir, "proj", ".kittify", "config.yaml"))
	if !strings.Contains(string(cfg), "  - claude\n") || strings.Contains(string(cfg), "copilot") {
		t.Errorf("config agents:\n%s", cfg)
	}
}

func TestInitArrowKeyPickerCancel(t *testing.T) {
	out, s, code := runPicker(t, "esc")
	if code != 1 || !strings.Contains(out, "Selection cancelled") {
		t.Errorf("esc: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "proj", ".kittify", "config.yaml")); err == nil {
		t.Errorf("cancelled init wrote config")
	}
}

var reCSI = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// pickerFrames returns the panel frames drawn (SGR/cursor controls removed).
func pickerFrames(out string) []string {
	clean := strings.ReplaceAll(reCSI.ReplaceAllString(out, ""), "\r", "")
	var frames []string
	for {
		i := strings.Index(clean, "╭──────────────────────── Choose your AI")
		if i < 0 {
			return frames
		}
		j := strings.Index(clean[i:], "╯")
		if j < 0 {
			return frames
		}
		frames = append(frames, clean[i:i+j+len("╯")])
		clean = clean[i+j:]
	}
}

func TestInitArrowKeyPickerFramesMatchOracle(t *testing.T) {
	oracle := testkit.Oracle(t)
	keys := []string{"down", "space", "down", "down", "space", "up", "enter"}
	gout, _, gcode := runPicker(t, keys...)
	oout, _, ocode := runPickerWith(t, oracle, keys...)
	if gcode != ocode {
		t.Errorf("exit go=%d oracle=%d", gcode, ocode)
	}
	gf, of := pickerFrames(gout), pickerFrames(oout)
	if strings.Join(gf, "\n=====\n") != strings.Join(of, "\n=====\n") {
		t.Errorf("frames differ:\n%s", testkit.Diff(strings.Join(of, "\n=====\n"), strings.Join(gf, "\n=====\n")))
	}
	if len(gf) != len(keys)+1 {
		t.Errorf("go drew %d frames, want %d", len(gf), len(keys)+1)
	}
}

var reNonSGR = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-ln-z]")

// colourFrames returns the panel frames with their SGR styling kept
// (cursor controls removed).
func colourFrames(out string) []string {
	clean := strings.ReplaceAll(reNonSGR.ReplaceAllString(out, ""), "\r", "")
	var frames []string
	for {
		i := strings.Index(clean, "Choose your AI")
		if i < 0 {
			return frames
		}
		start := strings.LastIndex(clean[:i], "╭")
		if p := strings.LastIndex(clean[:start], "\x1b["); p >= 0 && !strings.ContainsAny(clean[p:start], "\n╯") {
			start = p
		}
		j := strings.Index(clean[i:], "╯")
		if j < 0 {
			return frames
		}
		end := i + j + len("╯")
		if strings.HasPrefix(clean[end:], "\x1b[0m") {
			end += len("\x1b[0m")
		}
		frames = append(frames, clean[start:end])
		clean = clean[end:]
	}
}

// PYF-14 follow-up: on a colour terminal the picker panel carries rich's
// segment styles (cyan border, bold title, cyan/bright_black rows, dim help).
func TestInitArrowKeyPickerColourFramesMatchOracle(t *testing.T) {
	oracle := testkit.Oracle(t)
	keys := []string{"down", "space", "up", "enter"}
	gout, _, gcode := runPickerEnv(t, testkit.Binary(t), true, keys...)
	oout, _, ocode := runPickerEnv(t, oracle, true, keys...)
	if gcode != ocode {
		t.Errorf("exit go=%d oracle=%d", gcode, ocode)
	}
	gf, of := colourFrames(gout), colourFrames(oout)
	if len(of) == 0 || strings.Join(gf, "\n=====\n") != strings.Join(of, "\n=====\n") {
		t.Errorf("colour frames differ:\n%s", testkit.Diff(strings.Join(of, "\n=====\n"), strings.Join(gf, "\n=====\n")))
	}
}

// nagTranscript runs `doctor skills` in a PTY with a fresh nag cache that
// reports a newer release, returning the nag text (the output up to the
// end of its last line).
func nagTranscript(t *testing.T, bin string) string {
	s := testkit.NewSandbox(t)
	s.Env = append(s.Env, "HTTPS_PROXY=http://127.0.0.1:9", "HTTP_PROXY=http://127.0.0.1:9")
	dir := filepath.Join(s.Home, ".cache", "spec-kitty")
	os.MkdirAll(dir, 0o700)
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00")
	rec := `{"always_upgrade": false, "cli_version_key": "4.0.0rc8", "fetched_at": "` + now + `", "last_shown_at": null, ` +
		`"latest_source": "pypi", "latest_version": "99.1.0", "never_ask": false, "remote_version_seen": null, "snooze_step": null, "snoozed_until": null}`
	if err := os.WriteFile(filepath.Join(dir, "upgrade-nag.json"), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runPTYIn(t, s, bin, true, false, []string{"doctor", "skills"})
	out = strings.ReplaceAll(out, "\r", "")
	end := strings.Index(out, "for guidance.")
	if end < 0 {
		return out
	}
	return out[:end+len("for guidance.")]
}

// The upgrade nag is CliConsole(stderr).print(message): highlighted and
// wrapped on a colour terminal; doctor subcommands are classified by
// safety_modes (read-only doctor skills is safe, so it nags).
func TestNagColourMatchesOracle(t *testing.T) {
	oracle := testkit.Oracle(t)
	g, o := nagTranscript(t, testkit.Binary(t)), nagTranscript(t, oracle)
	if !strings.Contains(o, "\x1b[1;36m99.1\x1b[0m") {
		t.Fatalf("oracle did not nag:\n%q", o)
	}
	if g != o {
		t.Errorf("nag differs:\n%s", testkit.Diff(o, g))
	}
}

// Doctor tables on an 80-column colour terminal: rich's column collapse,
// ellipsis and crop, bold header, cyan first column, markup cells, and the
// highlighted markup=False issue lines.
func TestDoctorColourTablesMatchOracle(t *testing.T) {
	oracle := testkit.Oracle(t)
	s := testkit.NewSandbox(t)
	if r := s.Run("init", ".", "--ai", "claude,codex", "--non-interactive"); r.Code != 0 {
		t.Fatalf("init: %s", r.Stderr)
	}
	os.RemoveAll(filepath.Join(s.Dir, ".agents/skills/spec-kitty.plan"))
	for _, args := range [][]string{{"doctor", "state-roots"}, {"doctor", "skills"}, {"doctor", "command-files"}} {
		g, _, gc := runPTYIn(t, s, testkit.Binary(t), true, false, args)
		o, _, oc := runPTYIn(t, s, oracle, true, false, args)
		if gc != oc {
			t.Errorf("%v: exit go=%d oracle=%d", args, gc, oc)
		}
		g, o = strings.ReplaceAll(g, "\r", ""), strings.ReplaceAll(o, "\r", "")
		if !strings.Contains(o, "\x1b[") {
			t.Fatalf("%v: oracle not coloured:\n%q", args, o)
		}
		if g != o {
			t.Errorf("%v differs:\n%s", args, testkit.Diff(o, g))
		}
	}
}

var reSpaces = regexp.MustCompile(" {2,}")

// initColourTranscript canonicalizes a coloured `init --ai` PTY
// transcript: CRs dropped (the tty maps \n to \r\n), init's Live region
// collapsed (collapseLive: start and final frames and every printed line
// kept, timing-dependent refresh frames folded), sandbox paths masked.
// Cursor and SGR codes are compared as written.
func initColourTranscript(out string, s *testkit.Sandbox) string {
	clean := collapseLive(strings.ReplaceAll(out, "\r", ""))
	var lines []string
	for _, l := range strings.Split(clean, "\n") {
		if strings.Contains(l, s.Base) {
			// A path wrapped in a panel may split anywhere after Base.
			l = strings.ReplaceAll(strings.ReplaceAll(l, s.Dir, "<D>"), s.Base, "<B>")
			l = reSpaces.ReplaceAllString(l, "  ")
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// PYF-14 follow-up (FIX3): on a colour terminal init's banner gradient,
// setup panel, live step tree (start frame, a frame after every print, the
// final frame, erase codes), final static tree and closing panels carry
// rich's styles, byte for byte.
func TestInitColourOutputMatchesOracle(t *testing.T) {
	oracle := testkit.Oracle(t)
	for _, sel := range []string{"claude,vibe", "codex", "copilot,gemini,cursor"} {
		sel := sel
		t.Run(strings.ReplaceAll(sel, ",", "_"), func(t *testing.T) {
			t.Parallel()
			args := []string{"init", "proj", "--ai", sel}
			gout, gs, gcode := runPTY(t, testkit.Binary(t), true, false, args)
			oout, osb, ocode := runPTY(t, oracle, true, false, args)
			if gcode != ocode {
				t.Errorf("exit go=%d oracle=%d", gcode, ocode)
			}
			g, o := initColourTranscript(gout, gs), initColourTranscript(oout, osb)
			if !strings.Contains(o, "\x1b[3;93mSpec Kitty") || !strings.Contains(o, "\x1b[?25l") {
				t.Fatalf("oracle transcript not coloured / no live region:\n%q", oout)
			}
			if g != o {
				t.Errorf("transcript differs:\n%s", testkit.Diff(o, g))
			}
		})
	}
}
