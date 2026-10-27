package invocation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/eventlog"
	"github.com/spec-kitty/go-kitty3/internal/glossary"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// On-disk layout of the Op trail.
const (
	EventsDir    = "kitty-ops"
	IndexFile    = "ops-index.jsonl"
	ClosuresFile = "op-closures.jsonl"
)

// Error is InvocationError (base).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

// WriteError is InvocationWriteError.
type WriteError struct{ Msg string }

func (e *WriteError) Error() string { return e.Msg }

// AlreadyClosedError is raised on a second close.
type AlreadyClosedError struct{ ID string }

func (e *AlreadyClosedError) Error() string {
	return fmt.Sprintf("Invocation %s is already closed.", e.ID)
}

// RecordPath is InvocationWriter.invocation_path (validated).
func RecordPath(root, id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	p := filepath.Join(root, EventsDir, id+".jsonl")
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("Invocation record must not be a symbolic link: %s", p)
	}
	return p, nil
}

// appendNoFollow is InvocationWriter._append_line_no_follow: one locked,
// owner-only append (line has no terminator) that never follows a final
// symlink and refuses a torn last line.
func appendNoFollow(path, line string) error {
	return eventlog.Append(path, []string{line}, eventlog.Options{NoFollow: true, Mode: recordMode})
}

// WriteStarted creates kitty-ops/<id>.jsonl exclusively (owner-only, under
// the kitty-ops family lock) and appends the index entry.
func WriteStarted(root string, s *Started) (string, error) {
	dir := filepath.Join(root, EventsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", &WriteError{"Failed to write invocation record: " + err.Error()}
	}
	p, err := RecordPath(root, s.InvocationID)
	if err != nil {
		return "", err
	}
	err = eventLogLock(dir, func() error {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, recordMode)
		if err != nil {
			return err
		}
		_, err = f.WriteString(s.Line() + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	})
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", &WriteError{fmt.Sprintf("ULID collision on %s — retry with a new invocation_id", p)}
		}
		return "", &WriteError{"Failed to write invocation record: " + err.Error()}
	}
	// The index is a performance aid: a refused append (a torn index tail)
	// degrades `invocations list` and never fails the Op (upstream only logs).
	idx := jsonx.NewObject("invocation_id", s.InvocationID, "profile_id", s.ProfileID, "started_at", s.StartedAt)
	_ = appendNoFollow(filepath.Join(dir, IndexFile), jsonx.Dumps(idx, lineOpts))
	return p, nil
}

// appendValidated is InvocationWriter._append_validated: under the kitty-ops
// family lock, verify the trail's started-event identity (and, for a close,
// that it is not completed yet), then append line. Nothing is written when a
// check refuses.
func appendValidated(root, p, id, line string, refuseIfCompleted bool) error {
	check := func(existing []byte) error {
		rows, err := parseRows(existing, id)
		if err != nil {
			return err
		}
		if refuseIfCompleted {
			for _, r := range rows {
				if r.GetString("event") == "completed" {
					return &AlreadyClosedError{id}
				}
			}
		}
		return nil
	}
	err := eventlog.Append(p, []string{line}, eventlog.Options{LockAnchor: filepath.Join(root, EventsDir), NoCreate: true,
		NoFollow: true, Mode: recordMode, Precondition: check})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrNotExist):
		return &Error{"Invocation record not found: " + id}
	case eventlog.IsCode(err, eventlog.CodeSymlink):
		return &WriteError{"Failed to open invocation record safely: " + err.Error()}
	case eventlog.IsCode(err, eventlog.CodeTornTail):
		return &Error{fmt.Sprintf("Invocation record is unreadable: %s (%v)", id, err)}
	}
	return err
}

// parseRows validates a trail's started-event identity.
func parseRows(b []byte, id string) ([]*jsonx.Object, error) {
	if !utf8.Valid(b) {
		return nil, &Error{"Invocation record is unreadable: " + id}
	}
	var rows []*jsonx.Object
	for _, l := range strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' || r == '\r' }) {
		if strings.TrimSpace(l) == "" {
			continue
		}
		o, err := decodeLine(l)
		if err != nil || o == nil {
			return nil, &Error{"Invocation record is unreadable: " + id}
		}
		rows = append(rows, o)
	}
	if len(rows) == 0 || rows[0].GetString("event") != "started" {
		return nil, &Error{"Invocation record has no started event: " + id}
	}
	if emb, _ := rows[0].Get("invocation_id"); emb != id {
		return nil, &Error{fmt.Sprintf("Invocation record identity mismatch: requested=%s, embedded=%s", pyRepr(id), reprAny(emb))}
	}
	return rows, nil
}

func reprAny(v any) string {
	if s, ok := v.(string); ok {
		return pyRepr(s)
	}
	if v == nil {
		return "None"
	}
	return fmt.Sprint(v)
}

// passThrough reports the invocation errors the writer re-raises as is.
func passThrough(err error) bool {
	var ac *AlreadyClosedError
	var ie *Error
	var we *WriteError
	return errors.As(err, &ac) || errors.As(err, &ie) || errors.As(err, &we)
}

// WriteCompleted appends the completed event (AlreadyClosedError when one
// exists).
func WriteCompleted(root string, c *Completed) error {
	p, err := RecordPath(root, c.InvocationID)
	if err != nil {
		return err
	}
	if err := appendValidated(root, p, c.InvocationID, c.Line(), true); err != nil {
		if passThrough(err) {
			return err
		}
		return &WriteError{"Failed to append completed event: " + err.Error()}
	}
	return nil
}

// AppendLink appends an artifact_link (ref != "") or commit_link event.
func AppendLink(root, id, kind, ref, sha string) error {
	p, err := RecordPath(root, id)
	if err != nil {
		return err
	}
	entry := jsonx.NewObject("event", "artifact_link", "invocation_id", id, "at", clock.NowUTCISO())
	if ref != "" {
		entry.Set("kind", kind)
		entry.Set("ref", ref)
	} else {
		entry.Set("event", "commit_link")
		entry.Set("sha", sha)
	}
	if err := appendValidated(root, p, id, jsonx.Dumps(entry, lineOpts), false); err != nil {
		if passThrough(err) {
			return err
		}
		return &WriteError{"Failed to append correlation event: " + err.Error()}
	}
	return nil
}

// WriteGlossaryObservation appends glossary_checked (only when conflicts or
// an error were observed). Best effort.
func WriteGlossaryObservation(root, id string, obs *glossary.Observation) {
	if obs == nil || (len(obs.AllConflicts) == 0 && obs.ErrorMsg == nil) {
		return
	}
	p, err := RecordPath(root, id)
	if err != nil {
		return
	}
	entry := jsonx.NewObject("event", "glossary_checked", "invocation_id", id)
	for _, kv := range obs.ToObject().Pairs() {
		entry.Set(kv.Key, kv.Value)
	}
	_ = appendValidated(root, p, id, jsonx.Dumps(entry, lineOpts), false)
}

// ClosedIDs is closed_invocation_ids (the op-closures spine).
func ClosedIDs(root string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(root, EventsDir, ClosuresFile))
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		o, err := decodeLine(l)
		if err != nil || o == nil {
			continue
		}
		if c, err := ParseCompleted(o); err == nil {
			out[c.InvocationID] = true
		}
	}
	return out
}

var urlSchemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]+://`)

// NormaliseRef is normalise_ref: repo-relative when under root, absolute
// otherwise; URLs verbatim.
func NormaliseRef(ref, root string) string {
	if urlSchemeRe.MatchString(ref) {
		return ref
	}
	cand := ref
	if !filepath.IsAbs(cand) {
		cand = filepath.Join(root, ref)
	}
	resolved := resolvePath(cand)
	rr := resolvePath(root)
	if rel, err := filepath.Rel(rr, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		if rel == "." {
			return "."
		}
		return rel
	}
	return resolved
}

// resolvePath is Path.resolve() (non-strict): symlinks of the existing
// prefix are followed, the rest is lexically cleaned.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir, base := filepath.Split(p)
	if dir == "" || dir == p {
		return p
	}
	return filepath.Join(resolvePath(strings.TrimSuffix(dir, string(filepath.Separator))), base)
}
