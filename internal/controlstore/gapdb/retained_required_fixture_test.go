//go:build linux && amd64

package gapdb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

// TEST ONLY, both ordinary and E profiles: simulated Unix + self-pidfd, never
// Host/provider authority. Each owned close is attempted once; later cleanup
// observes its cached FIRST result, never a potentially repairing second Close.
// The private frame-guard test also checks its own explicit transport.Close
// directly. A subsequent fixture close cannot undo that test's failure; these
// real transports currently return nil, not an injected close-failure campaign.
type retainedRequiredClose struct {
	once  sync.Once
	close func() error
	err   error
	calls atomic.Int32
}

func (c *retainedRequiredClose) Close() error {
	c.once.Do(func() {
		c.calls.Add(1)
		c.err = c.close()
	})
	return c.err
}

func retainedRequiredCheckClose(t *testing.T, name string, c *retainedRequiredClose) {
	t.Helper()
	if c != nil {
		if err := c.Close(); err != nil {
			t.Errorf("FIRST %s close failed (cached, not retried); ownership residual/unqualified cleanup: %v", name, err)
		}
	}
}

func retainedRequiredIdentity(path string, want unix.Stat_t, kind uint32) error {
	var got unix.Stat_t
	if err := unix.Lstat(path, &got); err != nil {
		return err
	}
	if got.Dev != want.Dev || got.Ino != want.Ino || got.Mode&unix.S_IFMT != kind {
		return fmt.Errorf("identity differs at %s", path)
	}
	return nil
}

// No auto-unlink: closing a listener must NEVER remove a replacement pathname.
// Only an independently verified, known socket inode is eligible for unlink.
type retainedRequiredSocket struct {
	listener *net.UnixListener
	path     string
	dir      unix.Stat_t
	identity unix.Stat_t
	known    bool
	close    *retainedRequiredClose
	cleanup  sync.Once
}

func retainedRequiredListen(path string, directory unix.Stat_t) (*retainedRequiredSocket, error) {
	if err := retainedRequiredIdentity(filepath.Dir(path), directory, unix.S_IFDIR); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	s := &retainedRequiredSocket{listener: listener, path: path, dir: directory, close: &retainedRequiredClose{close: listener.Close}}
	if err := unix.Lstat(path, &s.identity); err != nil {
		return s, err // caller still owns the listener, but NOT an unlink identity
	}
	if s.identity.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return s, errors.New("created listener pathname is no longer a socket")
	}
	s.known = true
	return s, nil
}

func (s *retainedRequiredSocket) unlink(t *testing.T) {
	t.Helper()
	if !s.known {
		t.Errorf("residual: no verified socket identity; preserving %s", s.path)
		return
	}
	if err := retainedRequiredIdentity(filepath.Dir(s.path), s.dir, unix.S_IFDIR); err != nil {
		t.Errorf("residual: preserving socket in changed/unknown directory %s: %v", s.path, err)
		return
	}
	if err := retainedRequiredIdentity(s.path, s.identity, unix.S_IFSOCK); err != nil {
		t.Errorf("residual: preserving missing/foreign socket pathname %s: %v", s.path, err)
		return
	}
	if err := unix.Unlink(s.path); err != nil {
		t.Errorf("residual: verified socket unlink failed %s: %v", s.path, err)
	}
}

func (s *retainedRequiredSocket) remove(t *testing.T) {
	t.Helper()
	if s != nil {
		s.cleanup.Do(func() {
			retainedRequiredCheckClose(t, "listener "+s.path, s.close)
			s.unlink(t)
		})
	}
}

// Park/restore ONLY the known original socket. NOREPLACE makes foreign targets
// residual failures, never overwritten repairs. A replacement is closed once
// and identity-unlinked BEFORE restoring the original. Qualification flags and
// cached close outcomes are not reset by restoring this filesystem fixture.
func (s *retainedRequiredSocket) replace(t *testing.T) error {
	t.Helper()
	if !s.known {
		return errors.New("cannot park an unverified socket")
	}
	if err := retainedRequiredIdentity(filepath.Dir(s.path), s.dir, unix.S_IFDIR); err != nil {
		return err
	}
	if err := retainedRequiredIdentity(s.path, s.identity, unix.S_IFSOCK); err != nil {
		return err
	}
	parked := s.path + "-old"
	if err := unix.Renameat2(unix.AT_FDCWD, s.path, unix.AT_FDCWD, parked, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	var replacement *retainedRequiredSocket
	t.Cleanup(func() {
		replacement.remove(t)
		if err := retainedRequiredIdentity(filepath.Dir(s.path), s.dir, unix.S_IFDIR); err != nil {
			t.Errorf("residual: cannot restore original socket in changed directory: %v", err)
			return
		}
		if err := retainedRequiredIdentity(parked, s.identity, unix.S_IFSOCK); err != nil {
			t.Errorf("residual: parked original socket identity lost: %v", err)
			return
		}
		if err := unix.Renameat2(unix.AT_FDCWD, parked, unix.AT_FDCWD, s.path, unix.RENAME_NOREPLACE); err != nil {
			t.Errorf("residual: original socket restore refused without overwrite: %v", err)
		}
	})
	var err error
	replacement, err = retainedRequiredListen(s.path, s.dir)
	return err
}

type retainedRequiredFixtureCleanup struct {
	socket         *retainedRequiredSocket
	transportClose *retainedRequiredClose
}

func retainedRequiredFixture(t *testing.T, handler func(*net.UnixConn, retainedRequest) []byte) (*retainedTransport, *retainedkernel.Lifetime, *atomic.Int32) {
	t.Helper()
	transport, lifetime, accepted, _ := retainedRequiredFixtureOwned(t, handler)
	return transport, lifetime, accepted
}

func retainedRequiredFixtureOwned(t *testing.T, handler func(*net.UnixConn, retainedRequest) []byte) (*retainedTransport, *retainedkernel.Lifetime, *atomic.Int32, *retainedRequiredFixtureCleanup) {
	t.Helper()
	root, err := os.MkdirTemp("", "rt-required-")
	if err != nil {
		t.Fatal(err)
	}
	owner := &retainedRequiredFixtureCleanup{}
	var directory unix.Stat_t
	var directoryKnown, started bool
	var procClose, imageClose, lifetimeClose *retainedRequiredClose
	var workers sync.WaitGroup
	var stopping atomic.Bool
	done := make(chan struct{})
	// Registered first, therefore fault-listener cleanup/restoration registered
	// by users runs first. Listener stop precedes joins; ALL server goroutines
	// finish (including their checked first conn.Close) before root removal.
	t.Cleanup(func() {
		retainedRequiredCheckClose(t, "transport", owner.transportClose)
		stopping.Store(true)
		if owner.socket != nil {
			retainedRequiredCheckClose(t, "listener "+owner.socket.path, owner.socket.close)
		}
		if started {
			<-done
			workers.Wait()
		}
		retainedRequiredCheckClose(t, "lifetime", lifetimeClose)
		retainedRequiredCheckClose(t, "proc stat file", procClose)
		retainedRequiredCheckClose(t, "executed image file", imageClose)
		owner.socket.remove(t)
		if !directoryKnown {
			t.Errorf("residual: preserving unverified temporary root %s", root)
			return
		}
		if err := retainedRequiredIdentity(root, directory, unix.S_IFDIR); err != nil {
			t.Errorf("residual: preserving changed/unknown temporary root %s: %v", root, err)
			return
		}
		if err := os.Remove(root); err != nil { // empty only; NEVER RemoveAll
			t.Errorf("residual: root is nonempty or cannot be removed %s: %v", root, err)
		}
	})
	if err := unix.Lstat(root, &directory); err != nil {
		t.Fatal(err)
	}
	directoryKnown = directory.Mode&unix.S_IFMT == unix.S_IFDIR
	if !directoryKnown {
		t.Fatal("new temporary root is not a directory")
	}
	owner.socket, err = retainedRequiredListen(filepath.Join(root, "s"), directory)
	if err != nil {
		t.Fatal(err)
	}
	proc, err := os.Open("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	procClose = &retainedRequiredClose{close: proc.Close}
	procBytes, readErr := io.ReadAll(io.LimitReader(proc, 64<<10+1))
	retainedRequiredCheckClose(t, "proc stat file", procClose)
	if readErr != nil || len(procBytes) > 64<<10 {
		t.Fatal("proc stat read", readErr, len(procBytes))
	}
	end := bytes.LastIndexByte(procBytes, ')')
	if end < 0 || end+2 >= len(procBytes) {
		t.Fatal("malformed proc stat")
	}
	fields := strings.Fields(string(procBytes[end+2:]))
	if len(fields) <= 19 {
		t.Fatal("short proc stat")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	imageClose = &retainedRequiredClose{close: image.Close}
	var exe unix.Stat_t
	if err := unix.Fstat(int(image.Fd()), &exe); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, image); err != nil {
		t.Fatal(err)
	}
	retainedRequiredCheckClose(t, "executed image file", imageClose)
	socket := owner.socket.identity
	anchor := retainedkernel.Anchor{DirectoryPath: root, DirectoryDevice: uint64(directory.Dev), DirectoryInode: directory.Ino, PID: os.Getpid(), StartTicks: ticks, ImageDevice: uint64(exe.Dev), ImageInode: exe.Ino, ImageSize: exe.Size, ImageSHA256: hex.EncodeToString(hash.Sum(nil)), SocketPath: owner.socket.path, SocketDevice: uint64(socket.Dev), SocketInode: socket.Ino}
	lifetime, err := retainedkernel.New(t.Context(), anchor)
	if err != nil {
		t.Fatal(err)
	}
	lifetimeClose = &retainedRequiredClose{close: lifetime.Close}
	status := retainedTestStatus()
	transport := &retainedTransport{lifetime: lifetime, endpoint: owner.socket.path, database: status.DatabaseID, ownerPID: status.OwnerPID, ownerStarted: status.OwnerStartedAt, timeout: time.Second}
	owner.transportClose = &retainedRequiredClose{close: transport.Close}
	var accepted atomic.Int32
	started = true
	go func() {
		defer close(done)
		for {
			conn, err := owner.socket.listener.AcceptUnix()
			if err != nil {
				if !stopping.Load() || !errors.Is(err, net.ErrClosed) {
					t.Errorf("unexpected server accept failure: %v", err)
				}
				return
			}
			accepted.Add(1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				serverClose := &retainedRequiredClose{close: conn.Close}
				var source retainedFixtureSourceOutcome
				defer func() {
					source.closeAttempted = true
					source.closeErr = serverClose.Close() // cached FIRST actual full close
					source.report(t) // cannot erase a prior write/shutdown failure
				}()
				if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Errorf("server deadline failure: %v", err)
					return
				}
				raw, err := retainedReadFrame(conn)
				if err != nil {
					t.Logf("observed server request read failure (fault/precheck/cancel may close peer): %v", err)
					return
				}
				var request retainedRequest
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Errorf("unexpected invalid fixture request JSON: %v", err)
					return
				}
				response := handler(conn, request)
				if response != nil {
					source.writeAttempted = true
					source.writeErr = retainedWriteFrame(conn, response)
					if source.writeErr == nil {
						writeEnd := &retainedRequiredClose{close: conn.CloseWrite}
						source.shutdownAttempted = true
						source.shutdownErr = writeEnd.Close() // FIRST, never retried
					}
				}
			}()
		}
	}()
	return transport, lifetime, &accepted, owner
}

func TestRetainedRequiredFixtureCloseCachesFirstResult(t *testing.T) {
	for _, first := range []error{nil, errors.New("first close sentinel")} {
		c := &retainedRequiredClose{close: func() error { return first }}
		if got := c.Close(); got != first {
			t.Fatal("first outcome not retained", got)
		}
		c.close = func() error { t.Error("later close attempted a repair"); return nil }
		if later := c.Close(); later != first || c.calls.Load() != 1 {
			t.Fatal("later close repaired/lost first outcome", later, c.calls.Load())
		}
	}
}
