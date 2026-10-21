package migrate

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// Charter-content encoding migration (cli/commands/migrate/charter_encoding.py
// over charter.encoding_recovery.recover).
//
// Deviation: upstream's single-byte branch runs charset_normalizer; this
// port selects cp1252 whenever the bytes strictly decode as cp1252 (the
// contract's tie-break winner) with a fixed honest confidence, and reports
// every other non-UTF-8, BOM-less file as ambiguous.

// SingleByteConfidence is the confidence reported for a cp1252 selection.
const SingleByteConfidence = 0.9

// Recovered is EncodingRecoveryResult (the non-ambiguous subset).
type Recovered struct {
	Text           string
	SourceEncoding string
	Confidence     float64
	Normalized     bool
}

var cp1252High = [32]rune{
	0x20AC, 0, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017D, 0,
	0, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0, 0x017E, 0x0178,
}

func decodeCP1252(b []byte) (string, bool) {
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c < 0x80 || c >= 0xa0:
			sb.WriteRune(rune(c))
		default:
			r := cp1252High[c-0x80]
			if r == 0 {
				return "", false
			}
			sb.WriteRune(r)
		}
	}
	return sb.String(), true
}

func decodeUTF16(b []byte, big bool) (string, error) {
	if len(b)%2 != 0 {
		return "", fmt.Errorf("'utf-16' codec can't decode byte 0x%02x in position %d: truncated data", b[len(b)-1], len(b)-1)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		if big {
			u[i] = binary.BigEndian.Uint16(b[2*i:])
		} else {
			u[i] = binary.LittleEndian.Uint16(b[2*i:])
		}
	}
	return string(utf16.Decode(u)), nil
}

func decodeUTF32(b []byte, big bool) (string, error) {
	if len(b)%4 != 0 {
		return "", fmt.Errorf("'utf-32' codec can't decode bytes: truncated data")
	}
	var sb strings.Builder
	for i := 0; i < len(b); i += 4 {
		var r uint32
		if big {
			r = binary.BigEndian.Uint32(b[i:])
		} else {
			r = binary.LittleEndian.Uint32(b[i:])
		}
		sb.WriteRune(rune(r))
	}
	return sb.String(), nil
}

// RecoverEncoding is encoding_recovery.recover(data) (nil = ambiguous).
func RecoverEncoding(data []byte) (*Recovered, error) {
	type bom struct {
		prefix []byte
		enc    string
	}
	for _, b := range []bom{{[]byte{0, 0, 0xfe, 0xff}, "utf-32-be"}, {[]byte{0xff, 0xfe, 0, 0}, "utf-32-le"}, {[]byte{0xef, 0xbb, 0xbf}, "utf-8-sig"}, {[]byte{0xff, 0xfe}, "utf-16-le"}, {[]byte{0xfe, 0xff}, "utf-16-be"}} {
		if !bytes.HasPrefix(data, b.prefix) {
			continue
		}
		rest := data[len(b.prefix):]
		var text string
		var err error
		switch b.enc {
		case "utf-32-be", "utf-32-le":
			text, err = decodeUTF32(rest, b.enc == "utf-32-be")
		case "utf-16-be", "utf-16-le":
			text, err = decodeUTF16(rest, b.enc == "utf-16-be")
		default:
			if !utf8.Valid(rest) {
				_, err = pyjson.DecodeUTF8(rest)
			}
			text = string(rest)
		}
		if err != nil {
			return nil, err
		}
		return &Recovered{Text: text, SourceEncoding: b.enc, Confidence: 1.0, Normalized: true}, nil
	}
	if utf8.Valid(data) {
		return &Recovered{Text: string(data), SourceEncoding: "utf-8", Confidence: 1.0}, nil
	}
	if text, ok := decodeCP1252(data); ok {
		return &Recovered{Text: text, SourceEncoding: "cp1252", Confidence: SingleByteConfidence, Normalized: true}, nil
	}
	return nil, nil
}

// AmbiguousBody is _build_ambiguous_body (no charset candidates).
func AmbiguousBody(path string) string {
	return "ERROR: CHARTER_ENCODING_AMBIGUOUS\n  File: " + path + "\n  Detected candidates:\n    (no candidates detected)\n" +
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
		"confidence", rec.Confidence, "normalization_applied", rec.Normalized, "bypass_used", false, "actor", "spec-kitty charter", "mission_id", missionID)
	os.MkdirAll(filepath.Dir(target), 0o755)
	f, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		warnf("Failed to write encoding provenance to %s", target)
		return
	}
	defer f.Close()
	f.WriteString(jsonx.Dumps(record, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true}) + "\n")
}
