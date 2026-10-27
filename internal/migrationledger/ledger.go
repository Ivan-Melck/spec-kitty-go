// Package migrationledger persists the tracked and clone-local migration
// histories. Successful records, rather than a version stamp, settle work.
package migrationledger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const (
	TrackedRelative    = ".kittify/migration-ledger.jsonl"
	CloneLocalRelative = ".kittify/migrations/clone-local.jsonl"
)

// CanonicalID normalizes the historical constitution-era migration aliases.
func CanonicalID(id string) (string, bool) {
	switch id {
	case "0.10.12_constitution_cleanup":
		return "0.10.12_charter_cleanup", true
	case "0.13.0_update_constitution_templates":
		return "0.13.0_update_charter_templates", true
	case "2.0.0_constitution_directory":
		return "2.0.0_charter_directory", true
	case "2.0.2_constitution_context_bootstrap":
		return "2.0.2_charter_context_bootstrap", true
	case "2.1.2_fix_constitution_doctrine_skill":
		return "2.1.2_fix_charter_doctrine_skill", true
	}
	return id, false
}

// Record is one ledger line. SupersededBy is serialized only when set: the
// runner marks a superseded substitute's own run with it, so its "skipped"
// reads apart from a "skipped" the shipped migration recorded (#6629).
type Record struct {
	ID, AppliedAt, Result, Identity, ContentHash, CLIVersion string
	CloneLocal                                               bool
	SupersededBy                                             string
}

func (r Record) JSON() *jsonx.Object {
	o := jsonx.NewObject("id", r.ID, "applied_at", r.AppliedAt, "result", r.Result,
		"identity", r.Identity, "content_hash", r.ContentHash, "cli_version", r.CLIVersion)
	if r.CloneLocal {
		o.Set("clone_local", true)
	}
	if r.SupersededBy != "" {
		o.Set("superseded_by", r.SupersededBy)
	}
	return o
}

type Ledger struct{ Root string }

func (l Ledger) Path(cloneLocal bool) string {
	rel := TrackedRelative
	if cloneLocal {
		rel = CloneLocalRelative
	}
	return filepath.Join(l.Root, filepath.FromSlash(rel))
}

func (l Ledger) lines(cloneLocal bool) ([]string, error) {
	b, err := os.ReadFile(l.Path(cloneLocal))
	if err != nil {
		return nil, nil
	} // Python's raw reader tolerates OSError.
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// Read skips malformed JSON, missing required fields and invalid dates. The
// last record for an id wins, retaining its first-seen position.
func (l Ledger) Read(cloneLocal bool) ([]Record, error) {
	lines, err := l.lines(cloneLocal)
	if err != nil {
		return nil, err
	}
	var records []Record
	index := map[string]int{}
	for _, line := range lines {
		value, err := pyjson.Loads(line)
		if err != nil {
			continue
		}
		o, ok := value.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("migration ledger record must be a mapping, got %s", pyjson.TypeName(value))
		}
		if !o.Has("id") || !o.Has("applied_at") || !o.Has("result") {
			continue
		}
		at := o.GetString("applied_at")
		if _, err := clock.ParseISO(at); err != nil {
			continue
		}
		r := Record{ID: o.GetString("id"), AppliedAt: at, Result: o.GetString("result"), Identity: o.GetString("identity"), ContentHash: o.GetString("content_hash"), CLIVersion: o.GetString("cli_version"), CloneLocal: o.GetBool("clone_local"), SupersededBy: supersededBy(o)}
		if !o.Has("identity") {
			r.Identity = r.ID
		}
		if !o.Has("cli_version") {
			r.CLIVersion = "unknown"
		}
		if id, ok := CanonicalID(r.ID); ok {
			r.ID = id
		}
		if i, ok := index[r.ID]; ok {
			records[i] = r
		} else {
			index[r.ID] = len(records)
			records = append(records, r)
		}
	}
	return records, nil
}

// AppendMany atomically replaces each partition with its old records plus
// the whole new batch. A crash cannot leave a partial JSONL append behind.
func (l Ledger) AppendMany(records []Record) error {
	for _, cloneLocal := range []bool{false, true} {
		var batch []Record
		for _, record := range records {
			if record.CloneLocal == cloneLocal {
				batch = append(batch, record)
			}
		}
		if len(batch) == 0 {
			continue
		}
		lines, err := l.lines(cloneLocal)
		if err != nil {
			return err
		}
		for _, record := range batch {
			lines = append(lines, jsonx.Dumps(record.JSON(), jsonx.Options{Indent: -1}))
		}
		if err := atomicfile.Write(l.Path(cloneLocal), []byte(strings.Join(lines, "\n")+"\n")); err != nil {
			return err
		}
	}
	return nil
}

func (l Ledger) Append(record Record) error { return l.AppendMany([]Record{record}) }

// supersededBy is str(data.get("superseded_by", "")).
func supersededBy(o *jsonx.Object) string {
	v, ok := o.Get("superseded_by")
	if !ok {
		return ""
	}
	return pyjson.Str(v)
}
