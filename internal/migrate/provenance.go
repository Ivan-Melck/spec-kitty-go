package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// ProvenanceRecord is MatrixMigrationRecord.
type ProvenanceRecord struct {
	Path    string
	Stamped int
}

// ProvenanceError is MatrixMigrationError.
type ProvenanceError struct{ Path, Message string }

// ProvenanceSummary is BackfillSummary.
type ProvenanceSummary struct {
	FilesInspected int
	DryRun         bool
	Migrated       []ProvenanceRecord
	Unchanged      []string
	Errors         []ProvenanceError
}

// StampedTotal sums the stamped invariants.
func (s *ProvenanceSummary) StampedTotal() int {
	n := 0
	for _, r := range s.Migrated {
		n += r.Stamped
	}
	return n
}

// Result is "errors_present" or "success".
func (s *ProvenanceSummary) Result() string {
	if len(s.Errors) > 0 {
		return "errors_present"
	}
	return "success"
}

// UncaughtError is an exception upstream does not catch (rendered as a
// traceback by the CLI).
type UncaughtError struct{ Type, Msg string }

func (e *UncaughtError) Error() string { return e.Msg }

func matrixPaths(root string) []string {
	var out []string
	for _, name := range MissionDirs(root) {
		dir := coord.Resolve(root, name).StatusDir()
		p := filepath.Join(dir, "acceptance-matrix.json")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	return out
}

func computeProvenance(raw string) (string, int, bool, error) {
	v, err := pyjson.Loads(raw)
	if err != nil {
		return "", 0, false, err
	}
	data, ok := v.(*jsonx.Object)
	if !ok {
		return "", 0, false, &UncaughtError{"AttributeError", fmt.Sprintf("'%s' object has no attribute 'get'", pyjson.TypeName(v))}
	}
	invV, _ := data.Get("negative_invariants")
	var invs []any
	if pyjson.Truthy(invV) {
		l, ok := invV.([]any)
		if !ok {
			return "", 0, false, &UncaughtError{"TypeError", "negative_invariants is not a list"}
		}
		invs = l
	}
	stamped := 0
	var out []any
	for _, iv := range invs {
		inv, ok := iv.(*jsonx.Object)
		if !ok {
			return "", 0, false, &UncaughtError{"TypeError", fmt.Sprintf("NegativeInvariant.from_dict() argument after ** must be a mapping, not %s", pyjson.TypeName(iv))}
		}
		var missing []string
		for _, k := range []string{"invariant_id", "description", "verification_method"} {
			if !inv.Has(k) {
				missing = append(missing, "'"+k+"'")
			}
		}
		if len(missing) > 0 {
			return "", 0, false, &UncaughtError{"TypeError", fmt.Sprintf("NegativeInvariant.__init__() missing %d required positional argument(s): %v", len(missing), missing)}
		}
		res := "pending"
		if r, ok := inv.Get("result"); ok {
			res = pyjson.Str(r)
		}
		if res == "pending" || inv.Has("provenance_origin") {
			out = append(out, inv)
			continue
		}
		c := inv.Clone()
		c.Set("provenance_origin", "legacy_unrecorded")
		out = append(out, c)
		stamped++
	}
	if stamped == 0 {
		return "", 0, false, nil
	}
	data.Set("negative_invariants", out)
	return jsonx.Dumps(data, jsonx.Pretty) + "\n", stamped, true, nil
}

// BackfillProvenance is run_backfill_provenance_migration.
func BackfillProvenance(root string, dryRun bool) (*ProvenanceSummary, error) {
	var paths []string
	if st, err := os.Stat(project.Specs(root)); err == nil && st.IsDir() {
		paths = matrixPaths(root)
	}
	sum := &ProvenanceSummary{FilesInspected: len(paths), DryRun: dryRun}
	type planned struct {
		path, text string
		n          int
	}
	var plan []planned
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			sum.Errors = append(sum.Errors, ProvenanceError{p, err.Error()})
			continue
		}
		text, err := pyjson.DecodeUTF8(b)
		if err != nil {
			return nil, &UncaughtError{"UnicodeDecodeError", err.Error()}
		}
		newText, n, changed, err := computeProvenance(text)
		if err != nil {
			if _, ok := err.(*pyjson.DecodeError); ok {
				sum.Errors = append(sum.Errors, ProvenanceError{p, err.Error()})
				continue
			}
			return nil, err
		}
		if !changed {
			sum.Unchanged = append(sum.Unchanged, p)
			continue
		}
		plan = append(plan, planned{p, newText, n})
	}
	if !dryRun {
		orig := map[string][]byte{}
		var written []string
		for _, pl := range plan {
			b, _ := os.ReadFile(pl.path)
			orig[pl.path] = b
			if err := os.WriteFile(pl.path+".tmp-migrate", []byte(pl.text), 0o644); err == nil {
				err = os.Rename(pl.path+".tmp-migrate", pl.path)
			}
			if err := checkWritten(pl.path, pl.text); err != nil {
				for _, w := range written {
					os.WriteFile(w, orig[w], 0o644)
				}
				return nil, err
			}
			written = append(written, pl.path)
		}
	}
	for _, pl := range plan {
		sum.Migrated = append(sum.Migrated, ProvenanceRecord{pl.path, pl.n})
	}
	return sum, nil
}

func checkWritten(path, want string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(b) != want {
		return fmt.Errorf("failed to write %s", path)
	}
	return nil
}
