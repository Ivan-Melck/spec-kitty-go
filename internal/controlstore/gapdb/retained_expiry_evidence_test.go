package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

// This fixture qualifies ONLY an isolated test image and adapter infrastructure.
// It does not mint original-lifecycle/Host authority or admit a provider image.
// The ordinary production constructor/admission capabilities remain unchanged.
func TestRetainedExpiryEvidenceLive(t *testing.T) {
	manifest := os.Getenv("SPEC_KITTY_EXPIRY_EVIDENCE_BUILD")
	if manifest == "" {
		t.Skip("set separate expiry-evidence build.json; never an admitted image")
	}
	var build struct {
		Kind                  string `json:"kind"`
		Version               string `json:"version"`
		Adopted               bool   `json:"adopted"`
		ReleaseReady          bool   `json:"release_ready"`
		QualificationComplete bool   `json:"qualification_complete"`
		Executables           []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"executables"`
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &build); err != nil {
		t.Fatal(err)
	}
	if build.Kind != "expiry-test-artifact-not-production-admission" || build.Version != "v0.0.0-20260907114832-006e9d469d40" || build.Adopted || build.ReleaseReady || build.QualificationComplete || len(build.Executables) != 2 {
		t.Fatal("not a separate technical test build")
	}
	for _, image := range build.Executables {
		b, err := os.ReadFile(image.Path)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		if !filepath.IsAbs(image.Path) || int64(len(b)) != image.Bytes || hex.EncodeToString(h[:]) != image.SHA256 {
			t.Fatal("test image differs from build receipt")
		}
	}
	for _, mode := range []string{"hidden_expired_extra", "EOF_releases", "default_build_ignores_gate"} {
		t.Run(mode, func(t *testing.T) {
			image := build.Executables[0]
			if mode == "default_build_ignores_gate" {
				image = build.Executables[1]
			}
			root, err := os.MkdirTemp("", "spk-expiry-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			socket := filepath.Join(root, "s")
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			parent := os.NewFile(uintptr(fds[0]), "expiry-parent")
			child := os.NewFile(uintptr(fds[1]), "expiry-child")
			control, err := net.FileConn(parent)
			_ = parent.Close()
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = control.Close() })
			if err := control.SetDeadline(time.Now().Add(12 * time.Second)); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(image.Path, "--db", filepath.Join(root, "db"), "--socket", socket)
			cmd.Env = []string{"LANG=C", "TZ=UTC", "SPEC_KITTY_EXPIRY_GATE_FD=3"}
			cmd.ExtraFiles = []*os.File{child}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			// Stderr goes to a file: no concurrent bytes.Buffer reads/writes.
			diagnostic, err := os.Create(filepath.Join(root, "daemon.stderr"))
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			defer diagnostic.Close()
			cmd.Stderr = diagnostic
			if err := cmd.Start(); err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			_ = child.Close()
			t.Cleanup(func() {
				_ = control.Close() // release a held writer before requesting shutdown
				if err := cmd.Process.Signal(unix.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
					t.Error("test daemon shutdown signal", err)
				}
				waited := make(chan error, 1)
				go func() { waited <- cmd.Wait() }()
				select {
				case err := <-waited:
					if err != nil {
						t.Error("test daemon shutdown", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("test daemon graceful shutdown timed out")
					if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
						t.Error("test daemon kill", err)
					}
					select {
					case <-waited:
					case <-time.After(3 * time.Second):
						t.Error("test daemon wait remained blocked after kill")
					}
				}
			})
			ready := make(chan error, 1)
			go func() {
				var r struct {
					Ready      bool   `json:"ready"`
					SocketPath string `json:"socket_path"`
				}
				err := json.NewDecoder(io.LimitReader(stdout, 4096)).Decode(&r)
				if err == nil && (!r.Ready || r.SocketPath != socket) {
					err = errors.New("inexact readiness")
				}
				ready <- err
			}()
			select {
			case err := <-ready:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("test daemon readiness timed out")
			}
			client, err := gapdbapi.Dial(socket, gapdbapi.ClientOptions{Timeout: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			status, err := client.Status(t.Context())
			if err != nil || status.OwnerPID != cmd.Process.Pid {
				t.Fatal("test owner status", err)
			}
			lifetime := expiryEvidenceLifetime(t, cmd.Process.Pid, root, socket, image.SHA256)
			transport := &retainedTransport{lifetime: lifetime, endpoint: socket, database: status.DatabaseID, ownerPID: status.OwnerPID, ownerStarted: status.OwnerStartedAt, timeout: 2 * time.Second}
			t.Cleanup(func() { _ = transport.Close() })
			a, err := newAdapter(&ownedProvider{provider: &publicProvider{client: client}, transport: transport}, socket, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			a.lifetime = lifetime
			coordinator := retainedCoordinatorV2(t, a)
			ack := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "expiry-live", nil, 0))
			acks := []journalv2.AcknowledgedPublicationV2{ack}
			status, err = transport.status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			known := len(retainedCopiedOracleV2(t, acks))
			if status.RecordCount != known {
				t.Fatal("unexpected initial retained footprint")
			}
			expiryEvidenceMatched(t, a, status, acks)
			expiry := time.Now().Add(300 * time.Millisecond)
			extra := "spk/expiry-evidence/extra"
			insert, err := client.Put(t.Context(), extra, []byte("retained but not live"), &expiry, gapdbapi.AckDurable)
			if err != nil || insert.Revision == 0 || insert.DurableThroughRevision != insert.Revision {
				t.Fatal("extra insert not durable", err)
			}
			var marker [1]byte
			if mode == "default_build_ignores_gate" {
				if err := control.SetReadDeadline(time.Now().Add(700 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				_, err := io.ReadFull(control, marker[:])
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatal("default build unexpectedly used evidence gate", err)
				}
			} else {
				if _, err := io.ReadFull(control, marker[:]); err != nil || marker[0] != 'H' {
					t.Fatal("due writer never acquired gate", err)
				}
			}
			if mode == "hidden_expired_extra" {
				// Real attributed Get and live scan omit the extra, but the engine's
				// actual retained count includes it. No fake clock/scan/count is used.
				if _, err := transport.get(t.Context(), extra); err != nil {
					var apiError *gapdbapi.Error
					if !errors.As(err, &apiError) || apiError.Code != gapdbapi.CodeNotFound {
						t.Fatal("expired extra read failed for another reason", err)
					}
				} else {
					t.Fatal("expired extra still live")
				}
				page, err := client.ScanPrefix(t.Context(), "spk/", 1000, "")
				if err != nil || len(page.Records) != known || page.Cursor != "" || page.Truncated {
					t.Fatal("live scan did not hide exactly the extra", err)
				}
				s0, err := transport.status(t.Context())
				if err != nil || s0.RecordCount != known+1 || s0.CurrentRevision != insert.Revision || s0.DurableThroughRevision != insert.Revision {
					t.Fatal("retained extra not stable/durable", err)
				}
				capture, err := journalv2.NewStoreRevisionV2(journalv2.DatabaseID(s0.DatabaseID), uint64(s0.CurrentRevision), uint64(s0.DurableThroughRevision))
				if err != nil {
					t.Fatal(err)
				}
				session, err := a.PrepareRetainedObservationV2(t.Context(), capture, acks)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				if err := session.ReadPublications(); err != nil {
					t.Fatal("expected publication read failed", err)
				}
				if _, err := session.PrivateContext(); err != nil {
					t.Fatal(err)
				}
				attempts := session.state.budget.attempts
				if count, err := session.Finish(0); count != 0 || !errors.Is(err, errRetainedObservation) {
					t.Fatal("hidden extra earned membership", count, err)
				}
				if !session.state.countMismatch || session.state.budget.attempts != attempts+1 {
					t.Fatal("refusal did not occur at single S1 count check")
				}
				s2, err := transport.status(t.Context())
				if err != nil || !expiryEvidenceSameCoordinate(s0, s2) || s2.RecordCount != known+1 {
					t.Fatal("refusal was not in stable held interval", err)
				}
				if _, err := control.Write([]byte{'R'}); err != nil {
					t.Fatal(err)
				}
				if _, err := io.ReadFull(control, marker[:]); err != nil || marker[0] != 'A' {
					t.Fatal("release not acknowledged", err)
				}
			} else {
				_ = control.Close()
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				status, err = transport.status(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if status.RecordCount == known && status.CurrentRevision > insert.Revision && status.CurrentRevision == status.DurableThroughRevision {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("writer did not resume normal expiry")
				}
				time.Sleep(5 * time.Millisecond) // wait for post-release cleanup, not the witness
			}
			expiryEvidenceMatched(t, a, status, acks)
			t.Logf("test-image=%s mode=%s: actual ACK exact-read/count control passed; NOT Host or production admission", image.SHA256, mode)
		})
	}
}

func expiryEvidenceSameCoordinate(a, b gapdbapi.StatusResult) bool {
	return a.DatabaseID == b.DatabaseID && a.OwnerPID == b.OwnerPID && a.OwnerStartedAt == b.OwnerStartedAt && a.Lifecycle == gapdbapi.LifecycleReady && b.Lifecycle == gapdbapi.LifecycleReady && a.CurrentRevision == b.CurrentRevision && a.DurableThroughRevision == b.DurableThroughRevision
}

func expiryEvidenceMatched(t *testing.T, a *Adapter, s gapdbapi.StatusResult, acks []journalv2.AcknowledgedPublicationV2) {
	t.Helper()
	capture, err := journalv2.NewStoreRevisionV2(journalv2.DatabaseID(s.DatabaseID), uint64(s.CurrentRevision), uint64(s.DurableThroughRevision))
	if err != nil {
		t.Fatal(err)
	}
	session, err := a.PrepareRetainedObservationV2(t.Context(), capture, acks)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.ReadPublications(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.PrivateContext(); err != nil {
		t.Fatal(err)
	}
	if count, err := session.Finish(0); err != nil || count != uint64(s.RecordCount) {
		t.Fatal("exact footprint not matched", count, err)
	}
	if err := session.CheckLifetime(); err != nil {
		t.Fatal(err)
	}
}

func expiryEvidenceLifetime(t *testing.T, pid int, root, socket, imageHash string) *retainedkernel.Lifetime {
	t.Helper()
	proc, err := os.ReadFile(fmtProc(pid, "stat"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(proc[bytes.LastIndexByte(proc, ')')+2:]))
	if len(fields) <= 19 {
		t.Fatal("short process stat")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var image, sock, dir unix.Stat_t
	if err := unix.Stat(fmtProc(pid, "exe"), &image); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lstat(socket, &sock); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lstat(root, &dir); err != nil {
		t.Fatal(err)
	}
	lifetime, err := retainedkernel.New(context.Background(), retainedkernel.Anchor{PID: pid, StartTicks: ticks, ImageDevice: uint64(image.Dev), ImageInode: image.Ino, ImageSize: image.Size, ImageSHA256: imageHash, SocketPath: socket, SocketDevice: uint64(sock.Dev), SocketInode: sock.Ino, DirectoryPath: root, DirectoryDevice: uint64(dir.Dev), DirectoryInode: dir.Ino})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lifetime.Close() })
	return lifetime
}
func fmtProc(pid int, name string) string { return "/proc/" + strconv.Itoa(pid) + "/" + name }
