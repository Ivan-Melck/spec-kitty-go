package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charsetnorm"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// Charter-content encoding migration (cli/commands/migrate/charter_encoding.py
// over charter.encoding_recovery.recover, ported in internal/charsetnorm with
// charset_normalizer's detection).

// Recovered is EncodingRecoveryResult (the non-ambiguous subset).
type Recovered struct {
	Text           string
	SourceEncoding string
	Confidence     float64
	Normalized     bool
	BypassUsed     bool
}

// RecoverEncoding is encoding_recovery.recover(data) (unsafe=False). A nil
// result with a nil error is the ambiguous verdict; candidates carries the
// charset_normalizer candidates for the diagnostic body.
func RecoverEncoding(data []byte) (*Recovered, []charsetnorm.Candidate, error) {
	res, err := charsetnorm.Recover(data, false)
	if err != nil {
		return nil, nil, err
	}
	if res.Ambiguous {
		return nil, res.Candidates, nil
	}
	return &Recovered{Text: res.Text, SourceEncoding: res.SourceEncoding, Confidence: res.Confidence,
		Normalized: res.NormalizationApplied, BypassUsed: res.BypassUsed}, res.Candidates, nil
}

// AmbiguousBody is charter.activation._io._build_ambiguous_body.
func AmbiguousBody(path string, candidates []charsetnorm.Candidate) string {
	var lines []string
	for _, c := range candidates {
		lines = append(lines, fmt.Sprintf("    - %s (confidence %.2f)", c.Encoding, c.Confidence))
	}
	if len(lines) == 0 {
		lines = append(lines, "    (no candidates detected)")
	}
	return "ERROR: CHARTER_ENCODING_AMBIGUOUS\n  File: " + path + "\n  Detected candidates:\n" + strings.Join(lines, "\n") + "\n" +
		"  Mixed-content signal: the byte sequence cannot be decoded as strict UTF-8\n  and no encoding could be selected via the cp1252 tie-break contract.\n\n" +
		"  Remediation options:\n    1. Open the file in a UTF-8-aware editor and re-save.\n" +
		"    2. iconv -f <detected-encoding> -t utf-8 <file> > <file>.utf8 && mv <file>.utf8 <file>.\n" +
		"    3. Re-run with --unsafe (logs bypass_used=true to provenance)."
}

// CharterFiles is _collect_charter_files.
func CharterFiles(root string) []string {
	var files []string
	exts := []string{".yaml", ".md", ".txt"}
	globDir := func(dir string) {
		for _, ext := range exts {
			m, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
			sort.Strings(m)
			files = append(files, m...)
		}
	}
	specs := filepath.Join(root, "kitty-specs")
	if isDirFollow(specs) {
		entries, _ := os.ReadDir(specs)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names {
			md := filepath.Join(specs, n)
			if !isDirFollow(md) {
				continue
			}
			if cd := filepath.Join(md, "charter"); isDirFollow(cd) {
				globDir(cd)
			}
		}
	}
	if gc := filepath.Join(root, ".kittify", "charter"); isDirFollow(gc) {
		globDir(gc)
	}
	return files
}

// WriteEncodingProvenance is charter.activation._io._write_provenance for a
// file-sourced decode (paths routed like _route_provenance_path; the
// centralized log path is relative to the working directory, as upstream).
func WriteEncodingProvenance(path string, rec *Recovered) {
	parts := strings.Split(path, string(filepath.Separator))
	target := filepath.Join(".kittify", "encoding-provenance", "global.jsonl")
	var missionID any
	for i, p := range parts {
		if p == "kitty-specs" && i+1 < len(parts) {
			md := strings.Join(parts[:i+2], string(filepath.Separator))
			if md == "" {
				md = "/"
			}
			target = filepath.Join(md, ".encoding-provenance.jsonl")
			if b, err := os.ReadFile(filepath.Join(md, "meta.json")); err == nil {
				if v, err := pyjson.Loads(string(b)); err == nil {
					if o, ok := v.(*jsonx.Object); ok {
						if id, ok := o.Get("mission_id"); ok {
							missionID = pyjson.Str(id)
						}
					}
				}
			}
			break
		}
	}
	record := jsonx.NewObject("event_id", ulid.New(), "at", clock.NowUTCISO(), "file_path", path, "source_encoding", rec.SourceEncoding,
		"confidence", rec.Confidence, "normalization_applied", rec.Normalized, "bypass_used", rec.BypassUsed, "actor", "spec-kitty charter", "mission_id", missionID)
	os.MkdirAll(filepath.Dir(target), 0o755)
	f, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		warnf("Failed to write encoding provenance to %s", target)
		return
	}
	defer f.Close()
	f.WriteString(jsonx.Dumps(record, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true}) + "\n")
}
