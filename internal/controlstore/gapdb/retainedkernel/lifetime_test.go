package retainedkernel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Real test process, not an admitted provider. Bind exactly as the pinned daemon
// does, through its retained directory fd, to exercise kernel peername translation.
func TestRetainedKernelChild(t *testing.T) {
	if os.Getenv("RETAINED_KERNEL_CHILD") != "1" {
		return
	}
	dir, err := os.Open(os.Getenv("RETAINED_KERNEL_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: "/proc/self/fd/" + itoa(int(dir.Fd())) + "/s", Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, err := os.Stdout.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
func itoa(value int) string { // avoid coupling fixtures to kernel parsing
	return strconv.Itoa(value)
}

func testAnchor(t *testing.T, pid int, path string) Anchor {
	t.Helper()
	ticks, err := startTicks(pid, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open("/proc/" + itoa(pid) + "/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var image unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &image); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	var socket unix.Stat_t
	if err := unix.Lstat(path, &socket); err != nil {
		t.Fatal(err)
	}
	var directory unix.Stat_t
	if err := unix.Lstat(filepath.Dir(path), &directory); err != nil {
		t.Fatal(err)
	}
	return Anchor{DirectoryPath: filepath.Dir(path), DirectoryDevice: uint64(directory.Dev), DirectoryInode: directory.Ino, PID: pid, StartTicks: ticks, ImageDevice: uint64(image.Dev), ImageInode: image.Ino, ImageSize: image.Size, ImageSHA256: hex.EncodeToString(hash.Sum(nil)), SocketPath: path, SocketDevice: uint64(socket.Dev), SocketInode: socket.Ino}
}
func childFixture(t *testing.T, memfd bool) (Anchor, *exec.Cmd, io.WriteCloser) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	command := exec.Command(os.Args[0], "-test.run=^TestRetainedKernelChild$")
	if memfd {
		fd, err := unix.MemfdCreate("retained-kernel-test", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
		if err != nil {
			t.Fatal(err)
		}
		image := os.NewFile(uintptr(fd), "test-image")
		defer image.Close()
		source, err := os.Open(os.Args[0])
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		if _, err := io.Copy(image, source); err != nil {
			t.Fatal(err)
		}
		if _, err := unix.FcntlInt(image.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
			t.Fatal(err)
		}
		command = exec.Command("/proc/self/fd/3", "-test.run=^TestRetainedKernelChild$")
		command.ExtraFiles = []*os.File{image}
	}
	command.Env = append(os.Environ(), "RETAINED_KERNEL_CHILD=1", "RETAINED_KERNEL_DIR="+dir)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = command.Process.Kill(); _ = command.Wait() })
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && line != "ready\n" {
			err = ErrIdentity
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test process did not become ready")
	}
	return testAnchor(t, command.Process.Pid, filepath.Join(dir, "s")), command, stdin
}
func connectFixture(t *testing.T, path string) *net.UnixConn {
	t.Helper()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestRetainedLifetimeActualPeerExecutedMemfdAndSourceRefusal(t *testing.T) {
	anchor, _, _ := childFixture(t, true)
	lifetime, err := New(context.Background(), anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	conn := connectFixture(t, anchor.SocketPath)
	if err := lifetime.VerifyConnection(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	// A copied Anchor is immutable; source executable has equal bytes but a
	// different filesystem inode from the executed sealed memfd.
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	wrong := anchor
	wrong.ImageDevice, wrong.ImageInode = uint64(stat.Dev), stat.Ino
	if wrong.ImageDevice == anchor.ImageDevice && wrong.ImageInode == anchor.ImageInode {
		t.Fatal("fixture lacks source/executed distinction")
	}
	if fake, err := New(context.Background(), wrong); err == nil {
		_ = fake.Close()
		t.Fatal("source inode accepted")
	}
	anchor.ImageSHA256 = strings.Repeat("0", 64)
	if err := lifetime.Check(context.Background()); err != nil {
		t.Fatal("caller mutation retargeted lifetime", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lifetime.VerifyConnection(context.Background(), conn); err == nil {
		t.Fatal("closed connection accepted")
	}
}
func TestRetainedLifetimeEndpointPeerAndPostcheck(t *testing.T) {
	anchor, _, _ := childFixture(t, false)
	lifetime, err := New(context.Background(), anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	conn := connectFixture(t, anchor.SocketPath)
	if err := lifetime.VerifyConnection(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	proxyPath := filepath.Join(filepath.Dir(anchor.SocketPath), "proxy")
	proxy, err := net.ListenUnix("unix", &net.UnixAddr{Name: proxyPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	if err := lifetime.CheckEndpoint(context.Background(), proxyPath); err == nil {
		t.Fatal("proxy endpoint accepted")
	}
	if err := lifetime.VerifyConnection(context.Background(), connectFixture(t, proxyPath)); err == nil {
		t.Fatal("foreign peer accepted")
	}
	if err := os.Rename(anchor.SocketPath, anchor.SocketPath+"-old"); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: anchor.SocketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := lifetime.VerifyConnection(context.Background(), conn); err == nil {
		t.Fatal("post-response endpoint substitution accepted")
	}
}
func TestRetainedLifetimeDeadRevokedCancelledUnsupportedAndCleanup(t *testing.T) {
	anchor, command, stdin := childFixture(t, false)
	fds := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := fds()
	if _, err := newLifetime(context.Background(), anchor, func(int, int) (int, error) { return -1, unix.ENOSYS }); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	wrong := anchor
	wrong.StartTicks++
	if _, err := New(context.Background(), wrong); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
	wrong = anchor
	wrong.ImageSHA256 = strings.Repeat("0", 64)
	if _, err := New(context.Background(), wrong); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
	if _, err := newLifetime(context.Background(), anchor, func(pid, flags int) (int, error) {
		fd, err := unix.PidfdOpen(pid, flags)
		_ = os.Rename(anchor.SocketPath, anchor.SocketPath+"-temp")
		return fd, err
	}); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
	if err := os.Rename(anchor.SocketPath+"-temp", anchor.SocketPath); err != nil {
		t.Fatal(err)
	}
	if after := fds(); after != before {
		t.Fatalf("failed constructors leaked descriptors: %d -> %d", before, after)
	}
	lifetime, err := New(context.Background(), anchor)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lifetime.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); _ = lifetime.Check(context.Background()) }()
	}
	lifetime.Revoke()
	if err := lifetime.Check(context.Background()); !errors.Is(err, ErrRevoked) {
		t.Fatal(err)
	}
	if err := lifetime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Close(); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	if after := fds(); after != before {
		t.Fatalf("Close leaked descriptors: %d -> %d", before, after)
	}
	live, err := New(context.Background(), anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	_ = stdin.Close()
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := live.Check(context.Background()); err == nil {
		t.Fatal("dead process accepted")
	}
}

// Persistent ancestor substitution preserves the genuine peer/socket inode but
// must refuse configured-path topology. This is not foreign-provider evidence.
func TestRetainedKernelParentDirectoryAliasRefusal(t *testing.T) {
	anchor, _, _ := childFixture(t, false)
	lifetime, err := New(t.Context(), anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	root := anchor.DirectoryPath
	moved := root + "-moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(root); _ = os.Rename(moved, root) })
	if err := os.Symlink(moved, root); err != nil {
		t.Fatal(err)
	}
	conn := connectFixture(t, anchor.SocketPath)
	if err := lifetime.VerifyConnection(t.Context(), conn); !errors.Is(err, ErrIdentity) {
		t.Fatal("configured parent alias accepted", err)
	}
	if fresh, err := New(t.Context(), anchor); err == nil {
		_ = fresh.Close()
		t.Fatal("fresh lifetime recaptured alias")
	}
}

func TestRetainedKernelDirectoryIdentityAndSyntax(t *testing.T) {
	anchor, _, _ := childFixture(t, false)
	wrong := anchor
	wrong.DirectoryInode++
	if lifetime, err := New(t.Context(), wrong); err == nil {
		_ = lifetime.Close()
		t.Fatal("directory identity ignored")
	}
	for _, path := range []string{anchor.DirectoryPath + "/./s", anchor.DirectoryPath + "/../" + filepath.Base(anchor.DirectoryPath) + "/s", anchor.DirectoryPath + "//s"} {
		wrong = anchor
		wrong.SocketPath = path
		if lifetime, err := New(t.Context(), wrong); err == nil {
			_ = lifetime.Close()
			t.Fatal("path syntax cleaned", path)
		}
	}
}

func TestRetainedKernelPollInterruptionBound(t *testing.T) {
	t.Run("transient", func(t *testing.T) {
		l := &Lifetime{pidfd: 7}
		calls := 0
		err := l.liveWithPoll(t.Context(), true, func(fds []unix.PollFd, timeout int) (int, error) {
			calls++
			if len(fds) != 1 || fds[0].Fd != 7 || timeout != 0 {
				t.Fatal("not a nonblocking retained inspection")
			}
			if calls < 3 {
				return 0, unix.EINTR
			}
			return 0, nil
		})
		if err != nil || calls != 3 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("persistent background context", func(t *testing.T) {
		l := &Lifetime{pidfd: 7}
		calls := 0
		err := l.liveWithPoll(context.Background(), true, func([]unix.PollFd, int) (int, error) { calls++; return 0, unix.EINTR })
		if !errors.Is(err, unix.EINTR) || calls != maxPollAttempts {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("cancel during interruption", func(t *testing.T) {
		l := &Lifetime{pidfd: 7}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		err := l.liveWithPoll(ctx, true, func([]unix.PollFd, int) (int, error) { calls++; cancel(); return 0, unix.EINTR })
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("deadline during interruption", func(t *testing.T) {
		l := &Lifetime{pidfd: 7}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		calls := 0
		err := l.liveWithPoll(ctx, true, func([]unix.PollFd, int) (int, error) { calls++; <-ctx.Done(); return 0, unix.EINTR })
		if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("revoke during interruption", func(t *testing.T) {
		l := &Lifetime{pidfd: 7}
		calls := 0
		err := l.liveWithPoll(t.Context(), true, func([]unix.PollFd, int) (int, error) { calls++; l.Revoke(); return 0, unix.EINTR })
		if !errors.Is(err, ErrRevoked) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
}

func TestRetainedLifetimeImageSizeImmutableScalar(t *testing.T) {
	anchor, _, _ := childFixture(t, false)
	lifetime, err := New(t.Context(), anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	want := uint64(anchor.ImageSize)
	anchor.ImageSize++
	if lifetime.ImageSize() != want {
		t.Fatal("caller can retarget size")
	}
	lifetime.Revoke()
	if lifetime.ImageSize() != want {
		t.Fatal("scalar size incorrectly grants/requires liveness")
	}
	var absent *Lifetime
	if absent.ImageSize() != 0 {
		t.Fatal("nil size")
	}
}

func TestRetainedBoundedHashChunksInterruptAndLimits(t *testing.T) {
	var output bytes.Buffer
	count := 0
	err := readChunks(bytes.NewReader(make([]byte, 1<<20)), 1<<20, &output, func() error {
		count++
		if count == 4 {
			return context.Canceled
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || output.Len() >= 1<<20 {
		t.Fatal("hash did not interrupt", err, output.Len())
	}
	output.Reset()
	if err := readChunks(bytes.NewReader(make([]byte, 32)), 31, &output, func() error { return nil }); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
}
