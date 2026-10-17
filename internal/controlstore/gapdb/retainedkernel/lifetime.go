// Package retainedkernel checks retained Linux identities. It has no provider,
// protocol, dialing, signaling, or descriptor-export capability. An Anchor is
// only an identity constraint: it is not executable admission or owner authority.
package retainedkernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

const maxImageBytes = 64 << 20
const maxProcBytes = 64 << 10

// A signal storm cannot spin forever even with a deadline-free caller context.
// This cap bounds only zero-timeout kernel inspections, never response retries.
const maxPollAttempts = 64

var ErrRevoked = errors.New("retained lifetime revoked")
var ErrIdentity = errors.New("retained identity differs")
var ErrUnsupported = errors.New("retained pidfd unavailable")

// Anchor uses the EXECUTED image inode, which may be a sealed memfd rather than
// the configured source inode. All fields are copied; callers cannot retarget a
// Lifetime. The source-backed one-listener/no-reload contract is still required.
type Anchor struct {
	PID                             int
	StartTicks                      uint64
	ImageDevice, ImageInode         uint64
	ImageSize                       int64
	ImageSHA256                     string
	SocketPath                      string
	SocketDevice, SocketInode       uint64
	DirectoryPath                   string
	DirectoryDevice, DirectoryInode uint64
}

// Lifetime owns its own pidfd, never the supervisor's signaling descriptor.
// mu excludes close/descriptor-reuse races. Revocation is atomic and does not
// wait for an in-progress hash; every chunk and every successful check rechecks it.
type Lifetime struct {
	anchor  Anchor
	mu      sync.Mutex
	pidfd   int
	revoked atomic.Bool
	closed  bool // terminal shutdown entered; not a certificate of release
	closeAttempted bool // an actual FIRST kernel close, distinct from no owned fd
	closeErr error // immutable after closed; a later call cannot repair failure
}

// New safely acquires a private pidfd bracketed by full identity checks. Unlike
// startup supervision, observation membership has no start-tick polling fallback.
func New(ctx context.Context, anchor Anchor) (*Lifetime, error) {
	return newLifetime(ctx, anchor, unix.PidfdOpen)
}

func newLifetime(ctx context.Context, anchor Anchor, open func(int, int) (int, error)) (*Lifetime, error) {
	return newLifetimeWithClose(ctx, anchor, open, unix.Close)
}

// Private call-only component seam; production passes the literal syscall.
func newLifetimeWithClose(ctx context.Context, anchor Anchor, open func(int, int) (int, error), closeFD func(int) error) (*Lifetime, error) {
	digest, err := hex.DecodeString(anchor.ImageSHA256)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != anchor.ImageSHA256 ||
		anchor.PID <= 0 || anchor.StartTicks == 0 || anchor.ImageInode == 0 || anchor.ImageSize <= 0 || anchor.ImageSize > maxImageBytes ||
		!configuredPath(anchor.SocketPath) || anchor.SocketInode == 0 || anchor.DirectoryInode == 0 ||
		anchor.DirectoryPath != anchor.SocketPath[:strings.LastIndexByte(anchor.SocketPath, '/')] {
		return nil, ErrIdentity
	}
	l := &Lifetime{anchor: anchor, pidfd: -1}
	if err := l.checkLocked(ctx, false); err != nil {
		return nil, err
	}
	fd, err := open(anchor.PID, 0)
	if err != nil {
		return nil, errors.Join(ErrUnsupported, err)
	}
	if fd < 0 {
		return nil, ErrUnsupported
	}
	l.pidfd = fd
	if err := l.checkLocked(ctx, true); err != nil {
		return nil, joinRetainedCustody(err, l.closeWith(closeFD))
	}
	return l, nil
}

// Revoke is terminal, immediate, and safe during checking or shutdown failure.
func (l *Lifetime) Revoke() {
	if l != nil {
		l.revoked.Store(true)
	}
}

func (l *Lifetime) Close() error { return l.closeWith(unix.Close) }

// No closer is stored. FIRST close/result publication is under the same mu.
func (l *Lifetime) closeWith(closeFD func(int) error) error {
	if l == nil {
		return nil
	}
	l.Revoke()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.closeErr
	}
	l.closed = true
	if l.pidfd >= 0 {
		l.closeAttempted = true
		fd := l.pidfd
		l.pidfd = -1 // relinquish the number even if the FIRST close fails
		l.closeErr = retainedCloseResult(nil, "pidfd", closeFD(fd))
	}
	return l.closeErr
}

// ImageSize is the immutable verified executed-image size. It grants no
// descriptor, address, admission, or liveness; checks still hash actual bytes.
func (l *Lifetime) ImageSize() uint64 {
	if l == nil || l.anchor.ImageSize <= 0 {
		return 0
	}
	return uint64(l.anchor.ImageSize)
}

func (l *Lifetime) Check(ctx context.Context) error {
	if l == nil {
		return ErrRevoked
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.checkLocked(ctx, true)
}

// CheckEndpoint also refuses proxy/alternate paths, even if they reach the same
// process. No endpoint discovery or redirection occurs here.
func (l *Lifetime) CheckEndpoint(ctx context.Context, path string) error {
	if l == nil || path != l.anchor.SocketPath {
		return ErrIdentity
	}
	return l.Check(ctx)
}

func (l *Lifetime) live(ctx context.Context, requireFD bool) error {
	return l.liveWithPoll(ctx, requireFD, unix.Poll)
}

// The private poll seam permits deterministic interruption/bound tests without
// exposing descriptors or changing the kernel inspection used in production.
func (l *Lifetime) liveWithPoll(ctx context.Context, requireFD bool, inspect func([]unix.PollFd, int) (int, error)) error {
	if ctx == nil {
		return errors.New("retained check requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.revoked.Load() || l.closed {
		return ErrRevoked
	}
	if requireFD {
		if l.pidfd < 0 {
			return ErrUnsupported
		}
		poll := []unix.PollFd{{Fd: int32(l.pidfd), Events: unix.POLLIN}}
		for attempt := 0; attempt < maxPollAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if l.revoked.Load() {
				return ErrRevoked
			}
			n, err := inspect(poll, 0)
			if errors.Is(err, unix.EINTR) {
				// Go preemption/signals may interrupt even a zero-timeout poll.
				// This is a kernel inspection retry, never a response/RPC retry.
				if err := ctx.Err(); err != nil {
					return err
				}
				if l.revoked.Load() {
					return ErrRevoked
				}
				if attempt == maxPollAttempts-1 {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if n != 0 || poll[0].Revents != 0 {
				return ErrIdentity
			}
			break
		}
	}
	return nil
}

func (l *Lifetime) checkLocked(ctx context.Context, requireFD bool) error {
	return l.checkLockedWithOpen(ctx, requireFD, openRetainedFileOnce)
}

// DRAFT private deterministic seam, pending design review. The sole production
// caller above supplies the exact existing one-attempt kernel opener. This is
// not stored on Lifetime, a global variable, a provider, or a factory selector.
func (l *Lifetime) checkLockedWithOpen(ctx context.Context, requireFD bool, openFile func(string, func() error) (*os.File, error)) error {
	return l.checkLockedWithCalls(ctx, requireFD, openFile, (*os.File).Close)
}

func (l *Lifetime) checkLockedWithCalls(ctx context.Context, requireFD bool, openFile func(string, func() error) (*os.File, error), closeFile func(*os.File) error) (resultErr error) {
	if openFile == nil {
		return ErrIdentity
	}
	check := func() error { return l.live(ctx, requireFD) }
	if err := check(); err != nil {
		return err
	}
	before, err := startTicks(l.anchor.PID, check)
	if err != nil || before != l.anchor.StartTicks {
		return errors.Join(ErrIdentity, err)
	}
	file, err := openFile(fmt.Sprintf("/proc/%d/exe", l.anchor.PID), check)
	if err != nil {
		return errors.Join(ErrIdentity, err)
	}
	defer func() { resultErr = retainedCloseResult(resultErr, "proc-exe", closeFile(file)) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	// fstat the opened executable, not the configured source pathname.
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if !info.Mode().IsRegular() || uint64(stat.Dev) != l.anchor.ImageDevice || stat.Ino != l.anchor.ImageInode || stat.Size != l.anchor.ImageSize {
		return ErrIdentity
	}
	hash := sha256.New()
	if err := readChunks(file, l.anchor.ImageSize, hash, check); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != l.anchor.ImageSHA256 {
		return ErrIdentity
	}
	after, err := startTicks(l.anchor.PID, check)
	if err != nil || after != before {
		return errors.Join(ErrIdentity, err)
	}
	if err := configuredSocketIdentity(l.anchor, check); err != nil {
		return err
	}
	return check()
}

// VerifyConnection inspects the actual connection using fresh kernel queries;
// it performs no wire I/O and never consults cached RemoteAddr. The connected
// sockfs FD inode is deliberately not compared to the filesystem socket inode.
func (l *Lifetime) VerifyConnection(ctx context.Context, conn *net.UnixConn) error {
	if l == nil || conn == nil {
		return ErrIdentity
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkLocked(ctx, true); err != nil {
		return err
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var peer *unix.Ucred
	var address unix.Sockaddr
	var inspectErr error
	err = raw.Control(func(fd uintptr) {
		peer, inspectErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if inspectErr == nil {
			address, inspectErr = unix.Getpeername(int(fd))
		}
	})
	if err != nil || inspectErr != nil {
		return errors.Join(err, inspectErr)
	}
	path, ok := address.(*unix.SockaddrUnix)
	if !ok || peer == nil || int(peer.Pid) != l.anchor.PID {
		return ErrIdentity
	}
	resolved := path.Name
	const prefix = "/proc/self/fd/"
	if strings.HasPrefix(resolved, prefix) {
		suffix := strings.TrimPrefix(resolved, prefix)
		parts := strings.SplitN(suffix, "/", 2)
		if len(parts) != 2 || parts[1] != l.anchor.SocketPath[strings.LastIndexByte(l.anchor.SocketPath, '/')+1:] {
			return ErrIdentity
		}
		if _, err := strconv.ParseUint(parts[0], 10, 32); err != nil {
			return ErrIdentity
		}
		resolved = fmt.Sprintf("/proc/%d/fd/%s", peer.Pid, suffix)
	} else if resolved != l.anchor.SocketPath {
		return ErrIdentity
	}
	// Only the kernel peer's intentional proc directory-FD alias follows links.
	if err := socketIdentity(resolved, l.anchor); err != nil {
		return err
	}
	if err := configuredSocketIdentity(l.anchor, func() error { return l.live(ctx, true) }); err != nil {
		return err
	}
	return l.live(ctx, true)
}

// Reject syntax before any cleaning: even a /symlink/../ alias is forbidden.
// Unix endpoints bound both kernel path work and component count (<= 53).
func configuredPath(path string) bool {
	if len(path) < 3 || len(path) > 107 || path[0] != '/' || strings.IndexByte(path, 0) >= 0 {
		return false
	}
	for _, part := range strings.Split(path[1:], "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func configuredSocketIdentity(anchor Anchor, check func() error) error {
	return configuredSocketIdentityWithOpen(anchor, check, unix.Openat2)
}

// DRAFT private seam pending design review; production always passes Openat2.
// The callback is neither retained nor selectable through any public surface.
func configuredSocketIdentityWithOpen(anchor Anchor, check func() error, openDirectory func(int, string, *unix.OpenHow) (int, error)) error {
	return configuredSocketIdentityWithCalls(anchor, check, openDirectory, unix.Close)
}

func configuredSocketIdentityWithCalls(anchor Anchor, check func() error, openDirectory func(int, string, *unix.OpenHow) (int, error), closeFD func(int) error) (resultErr error) {
	if check == nil || openDirectory == nil {
		return ErrIdentity
	}
	if err := check(); err != nil {
		return err
	}
	// One bounded path resolution refuses ALL symlink/magic-link components.
	fd, err := openDirectory(unix.AT_FDCWD, anchor.DirectoryPath, &unix.OpenHow{
		Flags:   uint64(unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS),
	})
	if err != nil {
		return errors.Join(ErrIdentity, err)
	}
	if fd < 0 {
		return ErrIdentity
	}
	defer func() { resultErr = retainedCloseResult(resultErr, "socket-directory", closeFD(fd)) }()
	if err := check(); err != nil {
		return err
	}
	var directory, socket unix.Stat_t
	if err := unix.Fstat(fd, &directory); err != nil {
		return err
	}
	if directory.Mode&unix.S_IFMT != unix.S_IFDIR || uint64(directory.Dev) != anchor.DirectoryDevice || directory.Ino != anchor.DirectoryInode {
		return ErrIdentity
	}
	if err := check(); err != nil {
		return err
	}
	name := anchor.SocketPath[strings.LastIndexByte(anchor.SocketPath, '/')+1:]
	if err := unix.Fstatat(fd, name, &socket, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return errors.Join(ErrIdentity, err)
	}
	if socket.Mode&unix.S_IFMT != unix.S_IFSOCK || uint64(socket.Dev) != anchor.SocketDevice || socket.Ino != anchor.SocketInode {
		return ErrIdentity
	}
	return check()
}

func socketIdentity(path string, anchor Anchor) error {
	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFSOCK || uint64(stat.Dev) != anchor.SocketDevice || stat.Ino != anchor.SocketInode {
		return errors.Join(ErrIdentity, err)
	}
	return nil
}

func startTicks(pid int, check func() error) (uint64, error) {
	return startTicksWithOpen(pid, check, openRetainedFileOnce)
}

// DRAFT private seam pending design review, with the same fixed production
// opener and no exported callback/descriptor capability as checkLockedWithOpen.
func startTicksWithOpen(pid int, check func() error, openFile func(string, func() error) (*os.File, error)) (uint64, error) {
	return startTicksWithCalls(pid, check, openFile, (*os.File).Close)
}

func startTicksWithCalls(pid int, check func() error, openFile func(string, func() error) (*os.File, error), closeFile func(*os.File) error) (ticks uint64, resultErr error) {
	if check == nil || openFile == nil {
		return 0, ErrIdentity
	}
	file, err := openFile(fmt.Sprintf("/proc/%d/stat", pid), check)
	if err != nil {
		return 0, err
	}
	defer func() {
		resultErr = retainedCloseResult(resultErr, "proc-stat", closeFile(file))
		if resultErr != nil {
			ticks = 0 // no identity is published after a failed actual close
		}
	}()
	var encoded bytes.Buffer
	if err := readChunks(file, maxProcBytes, &encoded, check); err != nil {
		return 0, err
	}
	raw := encoded.Bytes()
	closeIndex := bytes.LastIndexByte(raw, ')')
	if closeIndex < 0 || closeIndex+2 >= len(raw) {
		return 0, ErrIdentity
	}
	fields := strings.Fields(string(raw[closeIndex+2:]))
	if len(fields) <= 19 {
		return 0, ErrIdentity
	}
	value, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || value == 0 {
		return 0, ErrIdentity
	}
	return value, nil
}

// readChunks bounds allocation and polls cancellation/liveness between local
// regular-file/proc reads. These kernel files do not use network I/O deadlines.
func readChunks(reader io.Reader, maximum int64, destination io.Writer, check func() error) error {
	var buffer [32 << 10]byte
	var total int64
	for {
		if err := check(); err != nil {
			return err
		}
		n, err := reader.Read(buffer[:min(int64(len(buffer)), maximum-total+1)])
		total += int64(n)
		if total > maximum {
			return ErrIdentity
		}
		if n != 0 {
			if _, writeErr := destination.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
		}
		if checkErr := check(); checkErr != nil {
			return checkErr
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}
