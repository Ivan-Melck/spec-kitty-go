package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// StatusRollbackRefused is mission_write.STATUS_ROLLBACK_REFUSED.
const StatusRollbackRefused = "STATUS_ROLLBACK_REFUSED"

// RollbackRefusal is mission_write.RollbackRefusal: why a rollback left the
// Mission's status files untouched (the value is the operator text).
type RollbackRefusal string

// The RollbackRefusal values (upstream's enum values verbatim).
const (
	RefusalLogVanished          RollbackRefusal = "the event log existed at capture and is gone"
	RefusalLogShrank            RollbackRefusal = "the event log is shorter than at capture"
	RefusalTailUnparseable      RollbackRefusal = "the rows after the capture point are not whole JSON event rows"
	RefusalTailNotOwned         RollbackRefusal = "the rows after the capture point are not the rows this operation appended"
	RefusalTailAlreadyCommitted RollbackRefusal = "the rows after the capture point are already committed at HEAD"
	RefusalHeadUnreadable       RollbackRefusal = "HEAD could not be read to prove the rows are uncommitted"
	RefusalIOError              RollbackRefusal = "the rollback hit an I/O error before it could finish"
)

// RollbackPoint is mission_write.RollbackPoint: the pre-operation state of a
// Mission's status files (event-log size and status.json bytes).
type RollbackPoint struct {
	EventsPath, StatusPath string
	PreEventSize           int64
	PreStatus              []byte // nil when status.json did not exist
	EventsExisted          bool
}

// CaptureRollbackPoint is capture_rollback_point for the status directory
// dir (the coordination worktree's Mission directory on a coord Mission).
func CaptureRollbackPoint(dir string) *RollbackPoint {
	p := &RollbackPoint{EventsPath: filepath.Join(dir, status.EventsFile), StatusPath: filepath.Join(dir, status.SnapshotFile)}
	if st, err := os.Stat(p.EventsPath); err == nil {
		p.EventsExisted = true
		p.PreEventSize = st.Size()
	}
	if b, err := os.ReadFile(p.StatusPath); err == nil {
		p.PreStatus = b
	}
	return p
}

// AppendedEventIDs is appended_event_ids: the event ids of the rows appended
// after the point; ok is false when the tail is not whole JSON rows.
func (p *RollbackPoint) AppendedEventIDs() (ids []string, ok bool) {
	f, err := os.Open(p.EventsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, true
		}
		return nil, false
	}
	defer f.Close()
	if _, err := f.Seek(p.PreEventSize, io.SeekStart); err != nil {
		return nil, false
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	return parseTailEventIDs(tail)
}

// RollbackOutcome is mission_write.RollbackOutcome.
type RollbackOutcome struct {
	RolledBack bool
	Refusal    RollbackRefusal // "" when rolled back
	EventsPath string
}

// Message is RollbackOutcome.message().
func (r RollbackOutcome) Message() string {
	if r.Refusal == "" {
		return ""
	}
	state := "left unchanged"
	if r.Refusal == RefusalIOError {
		state = "may be partly rewritten"
	}
	return fmt.Sprintf("%s: %s; %s %s. Inspect with: git diff HEAD -- %s", StatusRollbackRefused, string(r.Refusal), r.EventsPath, state, r.EventsPath)
}

// refuse is _refuse: the outcome, logged as a WARNING record on errw.
func (p *RollbackPoint) refuse(errw io.Writer, refusal RollbackRefusal) RollbackOutcome {
	out := RollbackOutcome{Refusal: refusal, EventsPath: p.EventsPath}
	if errw != nil {
		rich.LogWarning(errw, out.Message())
	}
	return out
}

// Restore is rollback_status_artifacts (through workflow._restore_status_artifacts):
// cut the rows appended after the point when they are exactly
// expected (when hasExpected) and not committed at HEAD, then restore
// status.json; otherwise refuse and leave both files byte-identical. A
// filesystem failure is reported as the IO_ERROR refusal.
func (p *RollbackPoint) Restore(errw io.Writer, expected []string, hasExpected bool) RollbackOutcome {
	f, err := os.OpenFile(p.EventsPath, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if p.EventsExisted {
				return p.refuse(errw, RefusalLogVanished)
			}
			return p.restoreStatus(errw)
		}
		return p.ioFailure(errw, err)
	}
	refusal, err := p.verifyTail(f, expected, hasExpected)
	if err != nil {
		f.Close()
		return p.ioFailure(errw, err)
	}
	if refusal != "" {
		f.Close()
		return p.refuse(errw, refusal)
	}
	if !p.EventsExisted {
		f.Close()
		if err := os.Remove(p.EventsPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return p.ioFailure(errw, err)
		}
		return p.restoreStatus(errw)
	}
	if st, err := f.Stat(); err == nil && st.Size() > p.PreEventSize {
		if err := f.Truncate(p.PreEventSize); err != nil {
			f.Close()
			return p.ioFailure(errw, err)
		}
		f.Sync()
	}
	f.Close()
	return p.restoreStatus(errw)
}

func (p *RollbackPoint) restoreStatus(errw io.Writer) RollbackOutcome {
	if p.PreStatus == nil {
		if err := os.Remove(p.StatusPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return p.ioFailure(errw, err)
		}
	} else {
		os.MkdirAll(filepath.Dir(p.StatusPath), 0o755)
		tmp := p.StatusPath + ".tmp-rollback"
		if err := os.WriteFile(tmp, p.PreStatus, 0o644); err != nil {
			return p.ioFailure(errw, err)
		}
		if err := os.Rename(tmp, p.StatusPath); err != nil {
			os.Remove(tmp)
			return p.ioFailure(errw, err)
		}
	}
	return RollbackOutcome{RolledBack: true, EventsPath: p.EventsPath}
}

// ioFailure is rollback_io_failure.
func (p *RollbackPoint) ioFailure(errw io.Writer, err error) RollbackOutcome {
	if errw != nil {
		rich.LogWarning(errw, fmt.Sprintf("rollback of %s failed: %s", p.EventsPath, pyOSError(err)))
	}
	return p.refuse(errw, RefusalIOError)
}

// pyOSError renders err like str(OSError) ("[Errno N] text: 'path'") where
// the error carries a path; other errors print as is.
func pyOSError(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Sprintf("%v: '%s'", pe.Err, pe.Path)
	}
	return err.Error()
}

// verifyTail is _verify_tail.
func (p *RollbackPoint) verifyTail(f *os.File, expected []string, hasExpected bool) (RollbackRefusal, error) {
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.Size() < p.PreEventSize {
		return RefusalLogShrank, nil
	}
	if _, err := f.Seek(p.PreEventSize, io.SeekStart); err != nil {
		return "", err
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	ids, ok := parseTailEventIDs(tail)
	if !ok {
		return RefusalTailUnparseable, nil
	}
	if hasExpected && !sameMultiset(ids, expected) {
		return RefusalTailNotOwned, nil
	}
	if len(ids) == 0 {
		return "", nil
	}
	committed, ok := committedEventIDs(p.EventsPath)
	if !ok {
		return RefusalHeadUnreadable, nil
	}
	for _, id := range ids {
		if committed[id] {
			return RefusalTailAlreadyCommitted, nil
		}
	}
	return "", nil
}

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	n := map[string]int{}
	for _, s := range a {
		n[s]++
	}
	for _, s := range b {
		n[s]--
		if n[s] < 0 {
			return false
		}
	}
	return true
}

// parseTailEventIDs is _parse_tail_event_ids: blank lines are legal, a last
// row without its newline is torn.
func parseTailEventIDs(tail []byte) ([]string, bool) {
	if len(tail) > 0 && !bytes.HasSuffix(tail, []byte("\n")) {
		return nil, false
	}
	ids := []string{}
	for _, raw := range bytes.Split(tail, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var row any
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, false
		}
		m, isMap := row.(map[string]any)
		if !isMap {
			return nil, false
		}
		id, isStr := m["event_id"].(string)
		if !isStr {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}

// committedEventIDs is _committed_event_ids: the event ids of the log
// committed at HEAD of the checkout holding eventsPath (empty outside a
// repository, on an unborn HEAD or when HEAD does not track the log); ok is
// false when the committed ids are unknown.
func committedEventIDs(eventsPath string) (map[string]bool, bool) {
	out := map[string]bool{}
	dir := filepath.Dir(eventsPath)
	top := git(dir, "rev-parse", "--show-toplevel") // gitx pins LC_ALL=C
	if top.Code != 0 {
		if strings.Contains(top.Stderr, "not a git repository") {
			return out, true
		}
		return nil, false
	}
	root := strings.TrimSpace(top.Stdout)
	if headIsUnborn(root) {
		return out, true
	}
	rr, err1 := filepath.EvalSymlinks(root)
	ep, err2 := filepath.EvalSymlinks(eventsPath)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	rel, err := filepath.Rel(rr, ep)
	if err != nil {
		return nil, false
	}
	rel = filepath.ToSlash(rel)
	// blob_at: a path HEAD does not track reads as None.
	if git(root, "cat-file", "-e", "HEAD:"+rel).Code != 0 {
		if git(root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}").Code != 0 {
			return nil, false
		}
		return out, true
	}
	blob := git(root, "cat-file", "blob", "HEAD:"+rel)
	if blob.Code != 0 {
		return nil, false
	}
	for _, raw := range strings.Split(blob.Stdout, "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var row any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return nil, false
		}
		if m, isMap := row.(map[string]any); isMap {
			if id, isStr := m["event_id"].(string); isStr {
				out[id] = true
			}
		}
	}
	return out, true
}

// headIsUnborn is _head_is_unborn.
func headIsUnborn(root string) bool {
	ref := git(root, "symbolic-ref", "--quiet", "HEAD")
	if ref.Code != 0 {
		return false
	}
	return git(root, "show-ref", "--verify", "--quiet", strings.TrimSpace(ref.Stdout)).Code == 1
}
