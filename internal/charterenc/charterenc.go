// Package charterenc is charter/activation/_io.py: the single ingestion
// chokepoint for charter content. It decodes a file through the canonical
// encoding detector (charter.encoding_recovery.recover, ported in
// internal/charsetnorm), appends an encoding-provenance record, and fails
// closed with CHARTER_ENCODING_AMBIGUOUS when no code page can be selected.
package charterenc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charsetnorm"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// Diagnostic codes (CharterEncodingDiagnostic).
const (
	CodeAmbiguous     = "CHARTER_ENCODING_AMBIGUOUS"
	CodeNotNormalized = "CHARTER_ENCODING_NOT_NORMALIZED"
)

// Content is CharterContent.
type Content struct {
	Text                 string
	SourceEncoding       string
	Confidence           float64
	SourcePath           string // "" for inline bytes
	NormalizationApplied bool
	BypassUsed           bool
}

// Error is CharterEncodingError (a KittyInternalConsistencyError).
type Error struct {
	Code string
	Body string
}

func (e *Error) Error() string { return e.Body }

// Warn receives logger.warning messages (provenance write failures);
// defaults to discarding them like an unconfigured logger.
var Warn = func(string) {}

// LoadFile is load_charter_file(path, unsafe=unsafe). The error is an
// *Error (ambiguous), the UnicodeDecodeError recover lets escape for bytes
// after a BOM, or the read error.
func LoadFile(path string, unsafe bool) (*Content, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return load(data, path, unsafe)
}

// LoadBytes is load_charter_bytes (no provenance for inline bytes beyond
// the centralized record upstream writes with an empty file_path).
func LoadBytes(data []byte, unsafe bool) (*Content, error) { return load(data, "", unsafe) }

func load(data []byte, source string, unsafe bool) (*Content, error) {
	res, err := charsetnorm.Recover(data, unsafe)
	if err != nil {
		return nil, err
	}
	if res.Ambiguous {
		return nil, &Error{Code: CodeAmbiguous, Body: AmbiguousBody(res.Candidates, source)}
	}
	c := &Content{Text: res.Text, SourceEncoding: res.SourceEncoding, Confidence: res.Confidence, SourcePath: source,
		NormalizationApplied: res.NormalizationApplied, BypassUsed: res.BypassUsed}
	WriteProvenance(c)
	return c, nil
}

// AmbiguousBody is _build_ambiguous_body ("" source = inline bytes).
func AmbiguousBody(candidates []charsetnorm.Candidate, source string) string {
	label := source
	if label == "" {
		label = "<inline bytes>"
	}
	var lines []string
	for _, c := range candidates {
		lines = append(lines, fmt.Sprintf("    - %s (confidence %.2f)", c.Encoding, c.Confidence))
	}
	if len(lines) == 0 {
		lines = append(lines, "    (no candidates detected)")
	}
	return "ERROR: " + CodeAmbiguous + "\n  File: " + label + "\n  Detected candidates:\n" + strings.Join(lines, "\n") + "\n" +
		"  Mixed-content signal: the byte sequence cannot be decoded as strict UTF-8\n  and no encoding could be selected via the cp1252 tie-break contract.\n\n" +
		"  Remediation options:\n    1. Open the file in a UTF-8-aware editor and re-save.\n" +
		"    2. iconv -f <detected-encoding> -t utf-8 <file> > <file>.utf8 && mv <file>.utf8 <file>.\n" +
		"    3. Re-run with --unsafe (logs bypass_used=true to provenance)."
}

// missionDir is the kitty-specs/<mission> prefix of path ("" when none):
// the first "kitty-specs" component followed by one more component.
func missionDir(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, string(filepath.Separator))
	for i, p := range parts {
		if p == "kitty-specs" {
			if i+1 < len(parts) {
				md := strings.Join(parts[:i+2], string(filepath.Separator))
				if md == "" {
					md = "/"
				}
				return md
			}
			return ""
		}
	}
	return ""
}

// ProvenancePath is _route_provenance_path: the mission's
// .encoding-provenance.jsonl, else the centralized log (relative to the
// working directory, as upstream).
func ProvenancePath(source string) string {
	if md := missionDir(source); md != "" {
		return filepath.Join(md, ".encoding-provenance.jsonl")
	}
	return filepath.Join(".kittify", "encoding-provenance", "global.jsonl")
}

func resolveMissionID(source string) any {
	md := missionDir(source)
	if md == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(md, "meta.json"))
	if err != nil {
		return nil
	}
	v, err := pyjson.Loads(string(b))
	if err != nil {
		return nil
	}
	if o, ok := v.(*jsonx.Object); ok {
		if id, ok := o.Get("mission_id"); ok {
			return pyjson.Str(id)
		}
	}
	return nil
}

// WriteProvenance is _write_provenance: append one EncodingProvenanceRecord
// (json.dumps(sort_keys=True)) to the routed JSONL file.
func WriteProvenance(c *Content) {
	record := jsonx.NewObject("event_id", ulid.New(), "at", clock.NowUTCISO(), "file_path", c.SourcePath,
		"source_encoding", c.SourceEncoding, "confidence", c.Confidence, "normalization_applied", c.NormalizationApplied,
		"bypass_used", c.BypassUsed, "actor", "spec-kitty charter", "mission_id", resolveMissionID(c.SourcePath))
	target := ProvenancePath(c.SourcePath)
	os.MkdirAll(filepath.Dir(target), 0o755)
	f, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		Warn(fmt.Sprintf("Failed to write encoding provenance to %s", target))
		return
	}
	defer f.Close()
	f.WriteString(jsonx.Dumps(record, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true}) + "\n")
}
