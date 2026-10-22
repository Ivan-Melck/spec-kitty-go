package charsetnorm

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Port of upstream charter/encoding_recovery.py (recover) and
// kernel/text_decode.py (detect_bom): the single chokepoint upstream uses
// for whole-file encoding recovery of charter and mission artifacts.

// CP1252Codec is encoding_recovery.CP1252_CODEC.
const CP1252Codec = "cp1252"

const (
	tieEpsilon          = 0.02
	maxHonestConfidence = 1.0 - tieEpsilon
)

// Candidate is one (encoding, 1 - chaos) pair reported by from_bytes.
type Candidate struct {
	Encoding   string
	Confidence float64
}

// RecoveryResult is EncodingRecoveryResult.
type RecoveryResult struct {
	Text                 string
	SourceEncoding       string
	Confidence           float64
	Ambiguous            bool
	Candidates           []Candidate
	NormalizationApplied bool
	BypassUsed           bool
}

// DecodeError is a CPython UnicodeDecodeError; Error() is str(exc).
type DecodeError struct {
	Codec      string
	Object     []byte
	Start, End int
	Reason     string
}

func (e *DecodeError) Error() string {
	if e.End-e.Start == 1 {
		return fmt.Sprintf("'%s' codec can't decode byte 0x%02x in position %d: %s", e.Codec, e.Object[e.Start], e.Start, e.Reason)
	}
	return fmt.Sprintf("'%s' codec can't decode bytes in position %d-%d: %s", e.Codec, e.Start, e.End-1, e.Reason)
}

// NotedError is an exception carrying PEP 678 notes. Error() renders the
// message followed by one "[NOTE] ..." line per note, as rich's traceback
// prints them under the exception line.
type NotedError struct {
	Err   error
	Notes []string
}

func (e *NotedError) Error() string {
	s := e.Err.Error()
	for _, n := range e.Notes {
		s += "\n[NOTE] " + n
	}
	return s
}

func (e *NotedError) Unwrap() error { return e.Err }

var bomTable =[]struct {
	prefix         []byte
	codec, srcName string
}{
	{[]byte{0, 0, 0xfe, 0xff}, "utf-32", "utf-32-be"},
	{[]byte{0xff, 0xfe, 0, 0}, "utf-32", "utf-32-le"},
	{[]byte{0xef, 0xbb, 0xbf}, "utf-8-sig", "utf-8-sig"},
	{[]byte{0xff, 0xfe}, "utf-16", "utf-16-le"},
	{[]byte{0xfe, 0xff}, "utf-16", "utf-16-be"},
}

// DetectBOM is kernel.text_decode.detect_bom: (codec, source_encoding).
func DetectBOM(data []byte) (codec, source string, ok bool) {
	for _, b := range bomTable {
		if bytes.HasPrefix(data, b.prefix) {
			return b.codec, b.srcName, true
		}
	}
	return "", "", false
}

// DecodeBOMCodec is data.decode(codec) for the BOM-sniffing codecs
// "utf-8-sig", "utf-16" and "utf-32", with CPython's error reporting.
func DecodeBOMCodec(data []byte, codec string) (string, error) {
	switch codec {
	case "utf-8-sig":
		rest := data
		if bytes.HasPrefix(rest, []byte{0xef, 0xbb, 0xbf}) {
			rest = rest[3:]
		}
		if !utf8.Valid(rest) {
			_, err := pyjson.DecodeUTF8(rest)
			// utf_8_sig is a Python-level codec, so CPython attaches a
			// PEP 678 note to the escaping exception.
			return "", &NotedError{Err: err, Notes: []string{"decoding with 'utf-8-sig' codec failed"}}
		}
		return string(rest), nil
	case "utf-16":
		return decodeUTF16Strict(data)
	case "utf-32":
		return decodeUTF32Strict(data)
	}
	return "", fmt.Errorf("unknown encoding: %s", codec)
}

// decodeUTF16Strict is bytes.decode("utf-16") (BOM sniffing, native=LE).
func decodeUTF16Strict(b []byte) (string, error) {
	q := 0
	big := false
	if len(b) >= 2 {
		if b[0] == 0xff && b[1] == 0xfe {
			q = 2
		} else if b[0] == 0xfe && b[1] == 0xff {
			q, big = 2, true
		}
	}
	name := "utf-16-le"
	if big {
		name = "utf-16-be"
	}
	unit := func(i int) rune {
		if big {
			return rune(b[i])<<8 | rune(b[i+1])
		}
		return rune(b[i+1])<<8 | rune(b[i])
	}
	fail := func(start, end int, reason string) error {
		return &DecodeError{Codec: name, Object: b, Start: start, End: end, Reason: reason}
	}
	out := make([]rune, 0, (len(b)-q)/2)
	for q < len(b) {
		if len(b)-q < 2 {
			return "", fail(q, len(b), "truncated data")
		}
		ch := unit(q)
		q += 2
		if ch < 0xd800 || ch > 0xdfff {
			out = append(out, ch)
			continue
		}
		if ch >= 0xdc00 {
			return "", fail(q-2, q, "illegal encoding")
		}
		if len(b)-q < 2 {
			return "", fail(q-2, len(b), "unexpected end of data")
		}
		ch2 := unit(q)
		if ch2 < 0xdc00 || ch2 > 0xdfff {
			return "", fail(q-2, q, "illegal UTF-16 surrogate")
		}
		q += 2
		out = append(out, 0x10000+((ch-0xd800)<<10)+(ch2-0xdc00))
	}
	return string(out), nil
}

// decodeUTF32Strict is bytes.decode("utf-32") (BOM sniffing, native=LE).
func decodeUTF32Strict(b []byte) (string, error) {
	q := 0
	big := false
	if len(b) >= 4 {
		if b[0] == 0xff && b[1] == 0xfe && b[2] == 0 && b[3] == 0 {
			q = 4
		} else if b[0] == 0 && b[1] == 0 && b[2] == 0xfe && b[3] == 0xff {
			q, big = 4, true
		}
	}
	name := "utf-32-le"
	if big {
		name = "utf-32-be"
	}
	out := make([]rune, 0, (len(b)-q)/4)
	for q < len(b) {
		if len(b)-q < 4 {
			return "", &DecodeError{Codec: name, Object: b, Start: q, End: len(b), Reason: "truncated data"}
		}
		var ch uint32
		if big {
			ch = uint32(b[q])<<24 | uint32(b[q+1])<<16 | uint32(b[q+2])<<8 | uint32(b[q+3])
		} else {
			ch = uint32(b[q+3])<<24 | uint32(b[q+2])<<16 | uint32(b[q+1])<<8 | uint32(b[q])
		}
		switch {
		case ch >= 0xd800 && ch <= 0xdfff:
			return "", &DecodeError{Codec: name, Object: b, Start: q, End: q + 4, Reason: "code point in surrogate code point range(0xd800, 0xe000)"}
		case ch >= 0x110000:
			return "", &DecodeError{Codec: name, Object: b, Start: q, End: q + 4, Reason: "code point not in range(0x110000)"}
		}
		out = append(out, rune(ch))
		q += 4
	}
	return string(out), nil
}

// Recover is charter.encoding_recovery.recover(data, unsafe=unsafe). A
// non-nil error is the UnicodeDecodeError upstream lets escape when bytes
// after a recognised BOM do not decode.
func Recover(data []byte, unsafe bool) (*RecoveryResult, error) {
	if codec, src, ok := DetectBOM(data); ok {
		text, err := DecodeBOMCodec(data, codec)
		if err != nil {
			return nil, err
		}
		return &RecoveryResult{Text: text, SourceEncoding: src, Confidence: 1.0, Candidates: []Candidate{}, NormalizationApplied: true}, nil
	}
	if utf8.Valid(data) {
		return &RecoveryResult{Text: string(data), SourceEncoding: "utf-8", Confidence: 1.0, Candidates: []Candidate{}}, nil
	}
	return recoverSingleByte(data, unsafe), nil
}

func isCP1252Family(e string) bool { return e == CP1252Codec || e == "windows-1252" }

func recoverSingleByte(data []byte, unsafe bool) *RecoveryResult {
	candidates := []Candidate{}
	for _, m := range FromBytes(data).All() {
		candidates = append(candidates, Candidate{m.Encoding(), 1.0 - m.Chaos()})
	}
	tiedNames, tiedChaos := tiedCandidates(candidates)
	_ = tiedChaos
	if enc, size, ok := selectFromTied(tiedNames, data); ok {
		chaos := chaosFor(candidates, enc)
		conf := honestConfidence(chaos, size)
		text, _ := decodeWith(pyCodecName(enc), data)
		return &RecoveryResult{Text: string(text), SourceEncoding: enc, Confidence: conf, Candidates: candidates, NormalizationApplied: true}
	}
	if unsafe {
		return &RecoveryResult{Text: decodeCP1252Lossless(data), SourceEncoding: CP1252Codec, Confidence: 0.0, Candidates: candidates, NormalizationApplied: true, BypassUsed: true}
	}
	return &RecoveryResult{Ambiguous: true, Candidates: candidates}
}

// tiedCandidates is _tied_candidates: the dict {encoding: chaos} (insertion
// order kept in names) within _TIE_EPSILON of the minimum chaos.
func tiedCandidates(cands []Candidate) ([]string, map[string]float64) {
	if len(cands) == 0 {
		return nil, nil
	}
	var order []string
	chaoses := map[string]float64{}
	for _, c := range cands {
		if _, ok := chaoses[c.Encoding]; !ok {
			order = append(order, c.Encoding)
		}
		chaoses[c.Encoding] = 1.0 - c.Confidence
	}
	minChaos := chaoses[order[0]]
	for _, e := range order {
		if chaoses[e] < minChaos {
			minChaos = chaoses[e]
		}
	}
	var tied []string
	out := map[string]float64{}
	for _, e := range order {
		if chaoses[e] <= minChaos+tieEpsilon {
			tied = append(tied, e)
			out[e] = chaoses[e]
		}
	}
	return tied, out
}

func selectFromTied(tied []string, data []byte) (string, int, bool) {
	cp1252Tied := false
	for _, e := range tied {
		if isCP1252Family(e) {
			cp1252Tied = true
			break
		}
	}
	if cp1252Tied {
		if _, ok := decodeWith(CP1252Codec, data); ok {
			return CP1252Codec, len(tied), true
		}
	}
	if len(tied) == 1 {
		if isCP1252Family(tied[0]) {
			return "", 0, false
		}
		return tied[0], 1, true
	}
	return "", 0, false
}

func chaosFor(cands []Candidate, enc string) float64 {
	if isCP1252Family(enc) {
		for _, c := range cands {
			if isCP1252Family(c.Encoding) {
				return 1.0 - c.Confidence
			}
		}
		return 0.0
	}
	for _, c := range cands {
		if c.Encoding == enc {
			return 1.0 - c.Confidence
		}
	}
	return 0.0
}

func honestConfidence(chaos float64, tieSize int) float64 {
	penalty := tieEpsilon * float64(tieSize)
	raw := (1.0 - chaos) - penalty
	return math64Max(0.0, math64Min(raw, maxHonestConfidence))
}

func math64Max(a, b float64) float64 {
	if a >= b {
		return a
	}
	return b
}

func math64Min(a, b float64) float64 {
	if a <= b {
		return a
	}
	return b
}

// pyCodecName maps a codec label to the codec table name ("windows-1252"
// is an alias of cp1252).
func pyCodecName(enc string) string { return IANAName(enc, false) }

// decodeCP1252Lossless is data.decode("cp1252", errors=<lossless>): bytes
// cp1252 leaves undefined become the code point of the same ordinal.
func decodeCP1252Lossless(data []byte) string {
	sbcsOnce.Do(loadSBCS)
	t := sbcsTables[CP1252Codec]
	out := make([]rune, 0, len(data))
	for _, b := range data {
		if r := t[b]; r >= 0 {
			out = append(out, r)
		} else {
			out = append(out, rune(b))
		}
	}
	return string(out)
}
