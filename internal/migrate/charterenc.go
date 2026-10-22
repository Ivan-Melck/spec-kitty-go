package migrate

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/charsetnorm"
	"github.com/spec-kitty/go-kitty3/internal/charterenc"
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
	return charterenc.AmbiguousBody(candidates, path)
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
// file-sourced decode (internal/charterenc).
func WriteEncodingProvenance(path string, rec *Recovered) {
	charterenc.Warn = func(m string) { warnf("%s", m) }
	charterenc.WriteProvenance(&charterenc.Content{Text: rec.Text, SourceEncoding: rec.SourceEncoding, Confidence: rec.Confidence,
		SourcePath: path, NormalizationApplied: rec.Normalized, BypassUsed: rec.BypassUsed})
}
