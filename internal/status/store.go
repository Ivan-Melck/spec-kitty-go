package status

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// EventsFile is the event log filename.
const EventsFile = "status.events.jsonl"

// SnapshotFile is the materialized snapshot filename.
const SnapshotFile = "status.json"

// StoreError is a corrupted or unreadable event log.
type StoreError struct{ Msg string }

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
	for _, it := range items {
		switch t := it.(type) {
		case *Event:
			rows = append(rows, t.ToObject())
		case *Annotation:
			rows = append(rows, t.ToObject())
		case *jsonx.Object:
			rows = append(rows, t)
		default:
			return fmt.Errorf("status: cannot append %T", it)
		}
	}
	return AppendRows(EventsPath(dir), rows...)
}

// ReadRows reads raw JSON objects from <dir>/status.events.jsonl (missing →
// nil). Blank lines are skipped.
func ReadRows(dir string) ([]*jsonx.Object, error) {
	b, err := os.ReadFile(EventsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*jsonx.Object
	for i, line := range splitLines(string(b)) {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		v, err := jsonx.Decode([]byte(s))
		if err != nil {
			return nil, &StoreError{Msg: fmt.Sprintf("Invalid JSON on line %d: %v", i+1, err)}
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			return nil, &StoreError{Msg: fmt.Sprintf("Invalid event structure on line %d: expected JSON object", i+1)}
		}
		out = append(out, o)
	}
	return out, nil
}

// splitLines mirrors str.splitlines() for \n, \r\n and \r.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
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
	return partition(rows, lineNos, missionID)
}

func readRowsWithLines(dir string) ([]*jsonx.Object, []int, error) {
	b, err := os.ReadFile(EventsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var out []*jsonx.Object
	var nos []int
	for i, line := range splitLines(string(b)) {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		v, err := jsonx.Decode([]byte(s))
		if err != nil {
			return nil, nil, &StoreError{Msg: fmt.Sprintf("Invalid JSON on line %d: %v", i+1, err)}
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			return nil, nil, &StoreError{Msg: fmt.Sprintf("Invalid event structure on line %d: expected JSON object", i+1)}
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
