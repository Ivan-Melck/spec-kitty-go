package invocation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/spec-kitty/go-kitty3/internal/clock"
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

func appendNoFollow(path, line string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

// WriteStarted creates kitty-ops/<id>.jsonl exclusively and appends the
// index entry.
func WriteStarted(root string, s *Started) (string, error) {
	dir := filepath.Join(root, EventsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", &WriteError{"Failed to write invocation record: " + err.Error()}
	}
	p, err := RecordPath(root, s.InvocationID)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", &WriteError{fmt.Sprintf("ULID collision on %s — retry with a new invocation_id", p)}
		}
		return "", &WriteError{"Failed to write invocation record: " + err.Error()}
	}
	_, err = f.WriteString(s.Line() + "\n")
	f.Close()
	if err != nil {
		return "", &WriteError{"Failed to write invocation record: " + err.Error()}
	}
	idx := jsonx.NewObject("invocation_id", s.InvocationID, "profile_id", s.ProfileID, "started_at", s.StartedAt)
	_ = appendNoFollow(filepath.Join(dir, IndexFile), jsonx.Dumps(idx, lineOpts)+"\n")
	return p, nil
}

// readRows reads a trail file and validates its started-event identity.
func readRows(p, id string) ([]*jsonx.Object, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &Error{"Invocation record not found: " + id}
		}
		return nil, &WriteError{"Failed to open invocation record safely: " + err.Error()}
	}
	var rows []*jsonx.Object
	for _, l := range strings.Split(string(b), "\n") {
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

// WriteCompleted appends the completed event (AlreadyClosedError when one
// exists).
func WriteCompleted(root string, c *Completed) error {
	p, err := RecordPath(root, c.InvocationID)
	if err != nil {
		return err
	}
	rows, err := readRows(p, c.InvocationID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.GetString("event") == "completed" {
			return &AlreadyClosedError{c.InvocationID}
		}
	}
	if err := appendNoFollow(p, c.Line()+"\n"); err != nil {
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
	if _, err := readRows(p, id); err != nil {
		return err
	}
	if err := appendNoFollow(p, jsonx.Dumps(entry, lineOpts)+"\n"); err != nil {
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
	if _, err := readRows(p, id); err != nil {
		return
	}
	entry := jsonx.NewObject("event", "glossary_checked", "invocation_id", id)
	for _, kv := range obs.ToObject().Pairs() {
		entry.Set(kv.Key, kv.Value)
	}
	_ = appendNoFollow(p, jsonx.Dumps(entry, lineOpts)+"\n")
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
