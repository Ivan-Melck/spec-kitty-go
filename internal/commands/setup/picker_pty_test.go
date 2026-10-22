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
	s := testkit.NewSandbox(t)
	m, sl := openPTY(t)
	defer m.Close()
	cmd := exec.Command(bin, "init", "proj")
	cmd.Dir = s.Dir
	cmd.Env = append(s.Environ(), "TERM=xterm", "COLUMNS=80", "SPEC_KITTY_GO_SKIP_BOOTSTRAP=1")
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
	for deadline := time.Now().Add(60 * time.Second); frames() == 0 && time.Now().Before(deadline); {
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
	if !strings.Contains(out, "╭──────────────────────── Choose your AI assistant(s): ────────────────────────╮") ||
		!strings.Contains(out, "│  ▶    ☑ copilot (GitHub Copilot)") ||
		!strings.Contains(out, "Use ↑/↓ to move, Space to toggle, Enter to confirm, Esc to cancel") {
		t.Errorf("panel not drawn:\n%s", out)
	}
	if !strings.Contains(out, "Selected AI assistant(s): Claude Code") {
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
