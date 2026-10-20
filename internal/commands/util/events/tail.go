package events

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// EmptyDigest is sha256(b"").
var EmptyDigest = digest(nil)

// PollInterval is DEFAULT_POLL_INTERVAL_SECONDS.
const PollInterval = 250 * time.Millisecond

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Cursor is TailCursor.
type Cursor struct {
	Offset    int64
	Invariant string
}

// ResumeRefused is the FR-013 refusal (Reason: negative | out_of_range |
// misaligned | content_mismatch).
type ResumeRefused struct{ Reason string }

func (e *ResumeRefused) Error() string { return "resume refused: " + e.Reason }

// PyError is an uncaught Python exception (rendered as a traceback tail).
type PyError struct {
	Type string
	Msg  string
}

func (e *PyError) Error() string { return e.Type + ": " + e.Msg }

// PollResult is the outcome of one poll.
type PollResult struct {
	Cursor      Cursor
	Events      []*jsonx.Object
	FilePresent bool
}

func startOfLastLine(data []byte, offset int64) int64 {
	if offset <= 1 {
		return 0
	}
	idx := bytes.LastIndexByte(data[:offset-1], '\n')
	return int64(idx + 1)
}

func invariantAt(data []byte, offset int64) string {
	start := startOfLastLine(data, offset)
	return digest(data[start:offset])
}

func truncation(at int64, reason string) PollResult {
	ev := jsonx.NewObject("type", "log_truncated", "reason", reason, "detected_at_offset", at, "tail_offset", 0, "tail_invariant", EmptyDigest)
	return PollResult{Cursor: Cursor{0, EmptyDigest}, Events: []*jsonx.Object{ev}, FilePresent: true}
}

// loadsBytes is json.loads(bytes) (UTF-8 / UTF-8-BOM inputs).
func loadsBytes(b []byte) (any, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, &PyError{"UnicodeDecodeError", err.Error()}
	}
	v, err := pyjson.Loads(s)
	if err != nil {
		return nil, &PyError{"JSONDecodeError", err.Error()}
	}
	return v, nil
}

// PollOnce is poll_once(path, cursor).
func PollOnce(path string, c Cursor) (PollResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PollResult{Cursor: c, FilePresent: false}, nil
		}
		return PollResult{}, &PyError{"OSError", err.Error()}
	}
	size := int64(len(data))
	if c.Offset != 0 {
		if size < c.Offset {
			return truncation(c.Offset, "size_shrink"), nil
		}
		if invariantAt(data, c.Offset) != c.Invariant {
			return truncation(c.Offset, "content_mismatch"), nil
		}
	}
	if size <= c.Offset {
		return PollResult{Cursor: c, FilePresent: true}, nil
	}
	chunks := bytes.Split(data[c.Offset:], []byte("\n"))
	complete := chunks[:len(chunks)-1]
	var evs []*jsonx.Object
	var consumed int64
	var last []byte
	for _, chunk := range complete {
		v, err := loadsBytes(chunk)
		if err != nil {
			return PollResult{}, err
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			return PollResult{}, &PyError{"StoreError", "Invalid event structure in tail stream: expected JSON object"}
		}
		consumed += int64(len(chunk)) + 1
		last = append(append([]byte(nil), chunk...), '\n')
		o.Set("tail_offset", c.Offset+consumed)
		o.Set("tail_invariant", digest(last))
		evs = append(evs, o)
	}
	inv := c.Invariant
	if last != nil {
		inv = digest(last)
	}
	return PollResult{Cursor: Cursor{c.Offset + consumed, inv}, Events: evs, FilePresent: true}, nil
}

// ValidateResume is validate_resume_cursor(path, offset, invariant).
func ValidateResume(path string, offset int64, invariant *string) (Cursor, error) {
	if offset < 0 {
		return Cursor{}, &ResumeRefused{"negative"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if offset > 0 {
				return Cursor{}, &ResumeRefused{"out_of_range"}
			}
			if invariant != nil && *invariant != EmptyDigest {
				return Cursor{}, &ResumeRefused{"content_mismatch"}
			}
			return Cursor{0, EmptyDigest}, nil
		}
		return Cursor{}, &PyError{"OSError", err.Error()}
	}
	if offset > int64(len(data)) {
		return Cursor{}, &ResumeRefused{"out_of_range"}
	}
	if offset != 0 && data[offset-1] != '\n' {
		return Cursor{}, &ResumeRefused{"misaligned"}
	}
	actual := invariantAt(data, offset)
	if invariant != nil && actual != *invariant {
		return Cursor{}, &ResumeRefused{"content_mismatch"}
	}
	return Cursor{offset, actual}, nil
}

// Emit writes one event the way print(json.dumps(event)) does.
func Emit(w io.Writer, ev *jsonx.Object) {
	fmt.Fprintln(w, jsonx.Dumps(ev, jsonx.Options{Indent: -1, EnsureASCII: true}))
}

// Tail drives tail_events: emit is called per event; maxEvents < 0 means
// unbounded. sleep is injectable for tests.
func Tail(path string, c Cursor, maxEvents int, sleep func(time.Duration), emit func(*jsonx.Object)) error {
	emitted := 0
	for maxEvents < 0 || emitted < maxEvents {
		r, err := PollOnce(path, c)
		if err != nil {
			return err
		}
		c = r.Cursor
		if len(r.Events) == 0 {
			sleep(PollInterval)
			continue
		}
		for _, ev := range r.Events {
			emit(ev)
			emitted++
			if maxEvents >= 0 && emitted >= maxEvents {
				return nil
			}
		}
	}
	return nil
}

// EventLogPath is mission_event_log_path(feature_dir).
func EventLogPath(featureDir string) string { return filepath.Join(featureDir, "status.events.jsonl") }
