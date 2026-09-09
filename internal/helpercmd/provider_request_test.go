package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestProviderRequestStrictRawAndInnerMetadata(t *testing.T) {
	for _, data := range []string{
		`{"schema":"go-kitty.tracker-request/1","schema":"go-kitty.tracker-request/1","action":"inventory","data":{}}`,
		`{"Schema":"go-kitty.tracker-request/1","action":"inventory","data":{}}`,
		`{"schema":"go-kitty.tracker-request/1","action":"inventory","data":{"project_id":"p","project_\u0069d":"p"}}`,
		`{"schema":"go-kitty.tracker-request/1","action":"inventory","data":{}} {}`,
		`{"schema":"go-kitty.tracker-request/1","action":"inventory","data":{"project_id":"\ud800"}}`,
	} {
		if _, err := decodeProviderRequest(strings.NewReader(data), "inventory", "project"); err == nil {
			t.Fatalf("accepted invalid request: %s", data)
		}
	}
	for _, value := range []string{"", `null`, `[]`, `{"k":1,"k":2}`, `{"nested":{"a":1,"a":2}}`, `{"x":"\ud800"}`} {
		raw, _ := json.Marshal(map[string]any{"schema": "go-kitty.tracker-request/1", "action": "create", "data": map[string]any{"work_id": "work", "operation_id": "op", "title": "Title", "provider_metadata_json": value}})
		if _, err := decodeProviderRequest(strings.NewReader(string(raw)), "create", "project"); err == nil {
			t.Fatalf("accepted inner metadata %q", value)
		}
	}
}
func TestProviderRequestMetadataPreservesFullDepthAndPrecision(t *testing.T) {
	opaque := `{"number":9007199254740993,"huge":1e400,"negative_zero":-0,"text":"雪","deep":` + strings.Repeat("[", 9999) + "0" + strings.Repeat("]", 9999) + "}"
	raw, _ := json.Marshal(map[string]any{"schema": "go-kitty.tracker-request/1", "action": "create", "data": map[string]any{"work_id": "work", "operation_id": "op", "title": "Title", "provider_metadata_json": opaque}})
	request, err := decodeProviderRequest(strings.NewReader(string(raw)), "create", "project")
	if err != nil {
		t.Fatal(err)
	}
	for _, lexeme := range []string{"9007199254740993", "1e400", "-0"} {
		if !strings.Contains(string(request.create.ProviderMetadata.Bytes()), lexeme) {
			t.Fatalf("lost numeric lexeme %s", lexeme)
		}
	}
}
func TestProviderAssignmentModesRejectMutationFieldsOnObserve(t *testing.T) {
	raw := `{"schema":"go-kitty.tracker-request/1","action":"assign","data":{"mode":"observe","operation_id":"op"}}`
	if _, err := decodeProviderRequest(strings.NewReader(raw), "assign", "project"); err == nil {
		t.Fatal("assignment observe accepted mutation fields")
	}
}

func TestProviderRequestBoundariesAndExactStructuredKeys(t *testing.T) {
	valid := `{"schema":"go-kitty.tracker-request/1","action":"create","data":{"work_id":"work","operation_id":"op","title":"雪😀�","criteria":[{"id":"c","text":"T","verifier":"test","required":true}],"provider_metadata_json":"{\"pair\":\"\\ud83d\\ude00\",\"replacement\":\"�\"}"}}`
	if _, err := decodeProviderRequest(strings.NewReader(valid), "create", "project"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"required"`, `"Required"`, 1),
		strings.Replace(valid, `"id":"c"`, `"id":"c","\u0069d":"d"`, 1),
		strings.Replace(valid, `"title":"雪😀�"`, `"title":null`, 1),
		strings.Replace(valid, `"title":"雪😀�"`, `"title":"`+string([]byte{0xff})+`"`, 1),
		strings.Replace(valid, `"work_id":"work"`, `"project_id":"other","work_id":"work"`, 1),
		strings.Replace(valid, `"provider_metadata_json":"{`, `"provider_metadata_json":null,"unused":"{`, 1),
		strings.Repeat(" ", int(MaxRequestBytes)) + valid,
	} {
		if _, err := decodeProviderRequest(strings.NewReader(raw), "create", "project"); err == nil {
			t.Fatal("accepted invalid structured or bounded input")
		}
	}
}

type trackedRequestPipe struct {
	*io.PipeReader
	entered chan struct{}
	once    sync.Once
	closes  atomic.Int32
}

func (p *trackedRequestPipe) Read(data []byte) (int, error) {
	p.once.Do(func() { close(p.entered) })
	return p.PipeReader.Read(data)
}
func (p *trackedRequestPipe) Close() error { p.closes.Add(1); return p.PipeReader.Close() }
func TestProviderRequestCancellationOwnsOnlyActiveStream(t *testing.T) {
	for _, mode := range []string{"before", "during", "completed"} {
		t.Run(mode, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			tracked := &trackedRequestPipe{PipeReader: reader, entered: make(chan struct{})}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "before" {
				cancel()
			}
			result := make(chan error, 1)
			go func() { _, err := decodeProviderRequestContext(ctx, tracked, "inventory", "project"); result <- err }()
			if mode != "before" {
				<-tracked.entered
			}
			switch mode {
			case "during":
				cancel()
			case "completed":
				go func() {
					_, _ = io.WriteString(writer, `{"schema":"go-kitty.tracker-request/1","action":"inventory","data":{}}`)
					_ = writer.Close()
				}()
			}
			var err error
			select {
			case err = <-result:
			case <-time.After(time.Second):
				t.Fatal("cancellation left an active blocked read")
			}
			if mode == "completed" {
				if err != nil {
					t.Fatal(err)
				}
				cancel()
				time.Sleep(10 * time.Millisecond)
				if tracked.closes.Load() != 0 {
					t.Fatal("watcher closed stream after return")
				}
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
				want := int32(0)
				if mode == "during" {
					want = 1
				}
				if tracked.closes.Load() != want {
					t.Fatalf("stream lifetime mismatch: %d", tracked.closes.Load())
				}
			}
		})
	}
}

func requestFileFlags(t *testing.T, file *os.File) uintptr {
	t.Helper()
	raw, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags uintptr
	var callErr syscall.Errno
	if err = raw.Control(func(fd uintptr) { flags, _, callErr = syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFL, 0) }); err != nil || callErr != 0 {
		t.Fatalf("descriptor flags: %v %v", err, callErr)
	}
	return flags
}
func TestProviderRequestBorrowedOSPipeSurvivesCancellation(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	before := requestFileFlags(t, reader)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := decodeProviderRequestContext(ctx, reader, "inventory", "project"); done <- err }()
	if _, err = writer.Write([]byte(`{"schema":`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("borrowed OS pipe did not cancel")
	}
	if !errors.Is(err, context.Canceled) || requestFileFlags(t, reader) != before {
		t.Fatalf("cancellation/flags lost: %v", err)
	}
	if _, err = writer.Write([]byte("usable")); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err = reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(reader)
	if err = readErr; err != nil || !strings.HasSuffix(string(data), "usable") {
		t.Fatalf("original pipe unusable: %s %v", data, err)
	}
}
func TestProviderRequestBorrowedFilePreservesOffsetAndFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	content := `ignored{"schema":"go-kitty.tracker-request/1","action":"inventory","data":{}}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.Seek(int64(len("ignored")), io.SeekStart); err != nil {
		t.Fatal(err)
	}
	before := requestFileFlags(t, file)
	ctx, cancel := context.WithCancel(t.Context())
	_, err = decodeProviderRequestContext(ctx, file, "inventory", "project")
	cancel()
	if err != nil || requestFileFlags(t, file) != before {
		t.Fatalf("file flags/offset lost: %v", err)
	}
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil || offset != int64(len(content)) {
		t.Fatalf("original file unusable or wrong offset: %d %v", offset, err)
	}
}

func countRequestDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
func TestProviderRequestDescriptorModesAndRepeatedCleanup(t *testing.T) {
	// Initialize the runtime poller before taking the stable descriptor census.
	warmR, warmW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	warmR.Close()
	warmW.Close()
	baseline := countRequestDescriptors(t)
	for _, nonblock := range []bool{false, true} {
		for _, cancelRead := range []bool{false, true} {
			for repeat := 0; repeat < 4; repeat++ {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				raw, err := reader.SyscallConn()
				if err != nil {
					t.Fatal(err)
				}
				var setErr error
				if err = raw.Control(func(fd uintptr) { setErr = syscall.SetNonblock(int(fd), nonblock) }); err != nil || setErr != nil {
					t.Fatalf("set fixture flags: %v %v", err, setErr)
				}
				before := requestFileFlags(t, reader)
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { _, err := decodeProviderRequestContext(ctx, reader, "inventory", "project"); done <- err }()
				if cancelRead {
					// Wait for the owned duplicate rather than assuming scheduler progress.
					deadline := time.Now().Add(time.Second)
					for countRequestDescriptors(t) <= baseline+2 && time.Now().Before(deadline) {
						time.Sleep(time.Millisecond)
					}
					if countRequestDescriptors(t) <= baseline+2 {
						cancel()
						reader.Close()
						writer.Close()
						t.Fatal("reader never acquired its owned duplicate")
					}
					cancel()
				} else {
					go func() {
						_, _ = io.WriteString(writer, `{"schema":"go-kitty.tracker-request/1","action":"inventory","data":{}}`)
						_ = writer.Close()
					}()
				}
				select {
				case err = <-done:
				case <-time.After(time.Second):
					cancel()
					reader.Close()
					writer.Close()
					t.Fatal("descriptor operation did not finish")
				}
				if cancelRead && !errors.Is(err, context.Canceled) || !cancelRead && err != nil {
					t.Fatalf("mode result: nonblock=%v cancel=%v err=%v", nonblock, cancelRead, err)
				}
				cancel()
				if requestFileFlags(t, reader) != before {
					t.Fatal("original descriptor status flags changed")
				}
				if cancelRead {
					if _, err = writer.Write([]byte("still usable")); err != nil {
						t.Fatal(err)
					}
					writer.Close()
				}
				remainder, err := io.ReadAll(reader)
				if err != nil || cancelRead && string(remainder) != "still usable" {
					t.Fatalf("borrowed file unusable: %s %v", remainder, err)
				}
				reader.Close()
				writer.Close()
				if got := countRequestDescriptors(t); got != baseline {
					t.Fatalf("descriptor leak: before=%d after=%d", baseline, got)
				}
			}
		}
	}
}
