package status

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// EventsFile is the event log filename.
const EventsFile = "status.events.jsonl"

// SnapshotFile is the materialized snapshot filename.
const SnapshotFile = "status.json"

// StoreError is a corrupted or unreadable event log.
type StoreError struct {
	Msg   string
	Cause error // the chained exception (raise ... from exc), e.g. a *pyjson.DecodeError
}

func (e *StoreError) Error() string { return e.Msg }

// EventsPath returns <dir>/status.events.jsonl.
func EventsPath(dir string) string { return filepath.Join(dir, EventsFile) }

var piiFields = map[string]bool{
	"machine_name": true, "hostname": true, "workspace_path": true,
	"developer_name": true, "developer_email": true,
}

// Sanitize returns a copy of row with PII keys stripped at every depth and
// session timestamps replaced (sanitize_event_for_log).
func Sanitize(row *jsonx.Object) *jsonx.Object {
	out := stripPII(row).(*jsonx.Object)
	started, hasS := out.Get("session_started_at")
	ended, hasE := out.Get("session_ended_at")
	if hasS || hasE {
		out.Delete("session_started_at")
		out.Delete("session_ended_at")
		ss, _ := started.(string)
		es, _ := ended.(string)
		if hasS && hasE {
			t1, e1 := clock.ParseISO(strings.Replace(ss, "Z", "+00:00", 1))
			t2, e2 := clock.ParseISO(strings.Replace(es, "Z", "+00:00", 1))
			if e1 == nil && e2 == nil {
				out.Set("session_duration_s", int(t2.Sub(t1).Seconds()))
			}
		}
	}
	return out
}

func stripPII(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		o := &jsonx.Object{}
		for _, kv := range t.Pairs() {
			if piiFields[kv.Key] {
				continue
			}
			o.Set(kv.Key, stripPII(kv.Value))
		}
		return o
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = stripPII(x)
		}
		return l
	}
	return v
}

// SerializeLine is serialize_event_line: sanitized, sort_keys=True, default
// separators, ensure_ascii=True; no trailing newline.
func SerializeLine(row *jsonx.Object) string {
	return jsonx.Dumps(Sanitize(row), jsonx.Line)
}

// AppendRows atomically appends rows to the JSONL file at path (read whole
// file, append, temp + rename; resulting mode 0600). Symlinked logs are
// refused.
func AppendRows(path string, rows ...*jsonx.Object) error {
	if len(rows) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return &StoreError{Msg: "Refusing to read symbolic link event log: " + path}
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var buf bytes.Buffer
	buf.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		buf.WriteByte('\n')
	}
	for _, r := range rows {
		buf.WriteString(SerializeLine(r))
		buf.WriteByte('\n')
	}
	return writeTemp0600(path, buf.Bytes())
}

func writeTemp0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Sync()
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// AppendEvents appends transitions and annotations as one atomic batch.
func AppendEvents(dir string, items ...any) error {
	var rows []*jsonx.Object
	transitions := false
	for _, it := range items {
		switch t := it.(type) {
		case *Event:
			rows = append(rows, t.ToObject())
			transitions = true
		case *Annotation:
			rows = append(rows, t.ToObject())
		case *jsonx.Object:
			rows = append(rows, t)
		default:
			return fmt.Errorf("status: cannot append %T", it)
		}
	}
	// append_event_stream_atomic_verified reads the stream before the
	// append; a transition batch is then read back (verify_event_readback).
	ExtraReadPasses(dir, 1)
	if err := AppendRows(EventsPath(dir), rows...); err != nil {
		return err
	}
	if transitions {
		ExtraReadPasses(dir, 1)
	}
	return nil
}

// ReadRows reads raw JSON objects from <dir>/status.events.jsonl (missing →
// nil). Blank lines are skipped.
func ReadRows(dir string) ([]*jsonx.Object, error) {
	text, ok, err := readLogText(EventsPath(dir))
	if err != nil || !ok {
		return nil, err
	}
	var out []*jsonx.Object
	// read_events_raw iterates the text file (`for line in fh`).
	for i, line := range pyjson.FileLines(text) {
		s := pyjson.Strip(line)
		if s == "" {
			continue
		}
		o, err := decodeRow(s, i+1)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// readLogText is Path.exists() + Path.read_text(encoding="utf-8") of the
// event log: (text, exists, error). A missing log (ENOENT, ENOTDIR, ELOOP)
// is ("", false, nil); any other OS failure is a *pyjson.OSError and invalid
// UTF-8 a *pyjson.UnicodeError, worded like CPython's exceptions. Line
// endings are left untranslated.
func readLogText(path string) (string, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) ||
			errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.ENAMETOOLONG) {
			return "", false, nil
		}
		return "", false, pyOSError(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", true, pyOSError(err)
	}
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return "", true, err
	}
	return s, true, nil
}

func pyOSError(err error) error {
	if oe, ok := pyjson.AsOSError(err); ok {
		return oe
	}
	return err
}

// decodeRow is json.loads of one stripped line plus the object check, with
// StoreError wording ("Invalid JSON on line N: <JSONDecodeError>").
func decodeRow(s string, lineNo int) (*jsonx.Object, error) {
	v, err := pyjson.Loads(s)
	if err != nil {
		return nil, &StoreError{Msg: fmt.Sprintf("Invalid JSON on line %d: %v", lineNo, err), Cause: err}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &StoreError{Msg: fmt.Sprintf("Invalid event structure on line %d: expected JSON object", lineNo)}
	}
	return o, nil
}

var retrospectiveTypes = map[string]bool{
	"RetrospectiveCaptured": true, "RetrospectiveCaptureFailed": true, "RetrospectiveSkipped": true,
}

// IsNonLaneRow reports rows that share the log but are not lane transitions.
func IsNonLaneRow(o *jsonx.Object) bool {
	if o.GetString("kind") == AnnotationKind {
		return false
	}
	if n, ok := o.Get("event_name"); ok {
		if s, ok := n.(string); ok && strings.HasPrefix(s, "retrospective.") {
			return true
		}
	}
	if t, ok := o.Get("type"); ok {
		if s, ok := t.(string); ok && retrospectiveTypes[s] {
			return true
		}
	}
	return o.Has("event_type")
}

// ReadStream reads and partitions the event log in dir. missionID back-fills
// transitions lacking mission_id (legacy rows); pass "" to skip.
func ReadStream(dir string, missionID string) (*Stream, error) {
	rows, lineNos, err := readRowsWithLines(dir)
	if err != nil {
		return nil, err
	}
	logSlugWarnings(dir, rows)
	return partition(rows, lineNos, missionID)
}

func readRowsWithLines(dir string) ([]*jsonx.Object, []int, error) {
	text, ok, err := readLogText(EventsPath(dir))
	if err != nil || !ok {
		return nil, nil, err
	}
	var out []*jsonx.Object
	var nos []int
	// read_text translates newlines, then str.splitlines() splits.
	for i, line := range pyjson.Splitlines(pyjson.TranslateNewlines(text)) {
		s := pyjson.Strip(line)
		if s == "" {
			continue
		}
		o, err := decodeRow(s, i+1)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, o)
		nos = append(nos, i+1)
	}
	return out, nos, nil
}

func partition(rows []*jsonx.Object, lineNos []int, missionID string) (*Stream, error) {
	st := &Stream{}
	for i, o := range rows {
		ln := i + 1
		if lineNos != nil {
			ln = lineNos[i]
		}
		if k, ok := o.Get("kind"); ok && k != nil {
			if k == AnnotationKind {
				a, err := AnnotationFromObject(o)
				if err != nil {
					return nil, &StoreError{Msg: fmt.Sprintf("Invalid event structure on line %d: %v", ln, err)}
				}
				st.Annotations = append(st.Annotations, a)
				continue
			}
			return nil, &StoreError{Msg: fmt.Sprintf("Unknown event kind %s on line %d", reprAny(k), ln)}
		}
		if IsNonLaneRow(o) {
			continue
		}
		if missionID != "" && !o.Has("mission_id") {
			o = o.Clone()
			o.Set("mission_id", missionID)
		}
		e, err := EventFromObject(o)
		if err != nil {
			return nil, &StoreError{Msg: fmt.Sprintf("Invalid event structure on line %d: %v", ln, err)}
		}
		st.Transitions = append(st.Transitions, e)
	}
	return st, nil
}

// ParseRows partitions already-decoded rows.
func ParseRows(rows []*jsonx.Object) (*Stream, error) { return partition(rows, nil, "") }

// DiaryError is spec_kitty_events.diary.DiaryError.
type DiaryError struct{ Msg string }

func (e *DiaryError) Error() string { return e.Msg }

// ParseDiary partitions raw rows with the shared diary package's rules
// (parse_diary): the decoder used by status materialization.
func ParseDiary(rows []*jsonx.Object) (*Stream, error) {
	st := &Stream{}
	for _, o := range rows {
		if k, ok := o.Get("kind"); ok && k != nil {
			if k == AnnotationKind {
				a, err := DiaryAnnotationFromObject(o)
				if err != nil {
					return nil, &DiaryError{Msg: "Invalid annotation row: " + err.Error()}
				}
				st.Annotations = append(st.Annotations, a)
				continue
			}
			return nil, &DiaryError{Msg: "Unknown event kind " + reprAny(k)}
		}
		if IsNonLaneRow(o) {
			continue
		}
		e, err := EventFromObject(o)
		if err != nil {
			return nil, &DiaryError{Msg: "Invalid lane-transition row: " + err.Error()}
		}
		st.Transitions = append(st.Transitions, e)
	}
	return st, nil
}
