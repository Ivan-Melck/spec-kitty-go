package pyyaml

// The lazy stream reader of yaml.reader.Reader / ruamel.yaml.reader.Reader:
// a file stream is read in 4096-unit chunks only when the scanner needs
// more characters (peek / prefix / forward), and each chunk is decoded and
// checked for non-printable characters as it arrives. A bad byte or a
// special character past the part read so far therefore surfaces after any
// earlier scanner error, exactly like upstream. A text-mode stream
// (open(path, encoding="utf-8")) is additionally modelled as CPython's
// TextIOWrapper: 8192-byte chunks decoded incrementally (a decoding error is
// the read's UnicodeDecodeError, positioned within that chunk) with
// universal newlines.

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const readerChunk = 4096 // Reader.update_raw(size=4096)

// lazyReader is the Reader state for a stream input.
type lazyReader struct {
	binary bool
	// binary stream
	data     []byte
	rawPos   int    // stream_pointer (bytes handed out by read())
	raw      []byte // raw_buffer (undecoded bytes)
	encoding string // utf-8, utf-16-le, utf-16-be
	// text stream
	tw      *textWrapper
	rawText []rune

	eof  bool
	done bool // raw_buffer is None (EOF sentinel appended)
}

func (r *lazyReader) rawLen() int {
	if r.binary {
		return len(r.raw)
	}
	return len(r.rawText)
}

// updateRaw is Reader.update_raw: one read(4096) of the stream.
func (r *lazyReader) updateRaw() {
	if r.binary {
		n := min(readerChunk, len(r.data)-r.rawPos)
		r.raw = append(r.raw, r.data[r.rawPos:r.rawPos+n]...)
		r.rawPos += n
		if n == 0 {
			r.eof = true
		}
		return
	}
	chunk := r.tw.read(readerChunk)
	r.rawText = append(r.rawText, chunk...)
	if len(chunk) == 0 {
		r.eof = true
	}
}

// decode is raw_decode(raw_buffer, 'strict', eof) (or the raw text of a
// text stream): the newly decoded characters, consuming them from the raw
// buffer. An undecodable byte panics with the ReaderError upstream raises.
func (s *scanner) decodeChunk() []rune {
	r := s.lazy
	if !r.binary {
		data := r.rawText
		r.rawText = nil
		return data
	}
	switch r.encoding {
	case "utf-16-le", "utf-16-be":
		n := len(r.raw) &^ 1
		units := r.raw[:n]
		out := decodeUTF16(units, r.encoding == "utf-16-be")
		// keep a trailing high surrogate for the next chunk
		if !r.eof && len(out) > 0 && out[len(out)-1] >= 0xD800 && out[len(out)-1] < 0xDC00 {
			out = out[:len(out)-1]
			n -= 2
		}
		r.raw = append([]byte(nil), r.raw[n:]...)
		return out
	}
	text, err := pyjson.DecodeUTF8(r.raw)
	if err == nil {
		r.raw = nil
		return []rune(text)
	}
	ue := err.(*pyjson.UnicodeError)
	if !r.eof && ue.Reason == "unexpected end of data" {
		valid, _ := pyjson.DecodeUTF8(r.raw[:ue.Start])
		r.raw = append([]byte(nil), r.raw[ue.Start:]...)
		return []rune(valid)
	}
	// raw_buffer[exc.start] is an int, so the "unacceptable character" form is used
	pos := r.rawPos - len(r.raw) + ue.Start
	panic(&ReaderError{Name: s.name, Position: pos, Character: rune(r.raw[ue.Start]), Byte: -1, Encoding: "utf-8", Reason: ue.Reason})
}

// determineEncoding is Reader.determine_encoding followed by update(1).
func (s *scanner) determineEncoding() {
	r := s.lazy
	for !r.eof && (r.rawLen() < 2) {
		r.updateRaw()
	}
	if r.binary {
		r.encoding = "utf-8"
		switch {
		case len(r.raw) >= 2 && r.raw[0] == 0xff && r.raw[1] == 0xfe:
			r.encoding = "utf-16-le"
		case len(r.raw) >= 2 && r.raw[0] == 0xfe && r.raw[1] == 0xff:
			r.encoding = "utf-16-be"
		}
	}
	s.update(1)
}

// update is Reader.update: read and decode chunks until length characters
// are available past the pointer.
func (s *scanner) update(length int) {
	r := s.lazy
	if r == nil || r.done {
		return
	}
	for len(s.buffer)-s.pointer < length {
		if !r.eof {
			r.updateRaw()
		}
		data := s.decodeChunk()
		for i, c := range data {
			if !isPrintable(c) {
				panic(&ReaderError{Name: s.name, Position: len(s.buffer) + i, Character: c, Byte: -1, Encoding: "unicode", Reason: "special characters are not allowed"})
			}
		}
		s.buffer = append(s.buffer, data...)
		if r.eof {
			s.buffer = append(s.buffer, 0)
			r.done = true
			return
		}
	}
}

// textWrapper models io.TextIOWrapper(encoding="utf-8", newline=None) over a
// regular file: read(n) decodes 8192-byte chunks with the incremental UTF-8
// decoder and the universal-newline decoder.
type textWrapper struct {
	data      []byte
	pos       int
	pending   []byte
	pendingCR bool
	decoded   []rune
	eof       bool
}

const textChunk = 8192 // TextIOWrapper._CHUNK_SIZE

func (t *textWrapper) read(n int) []rune {
	for len(t.decoded) < n && !t.eof {
		t.readChunk()
	}
	take := min(n, len(t.decoded))
	out := t.decoded[:take]
	t.decoded = t.decoded[take:]
	return out
}

func (t *textWrapper) readChunk() {
	end := min(t.pos+textChunk, len(t.data))
	chunk := t.data[t.pos:end]
	t.pos = end
	final := len(chunk) == 0
	input := append(append([]byte(nil), t.pending...), chunk...)
	t.pending = nil
	text, err := pyjson.DecodeUTF8(input)
	if err != nil {
		ue := err.(*pyjson.UnicodeError)
		if final || ue.Reason != "unexpected end of data" {
			panic(ue)
		}
		text, _ = pyjson.DecodeUTF8(input[:ue.Start])
		t.pending = append([]byte(nil), input[ue.Start:]...)
	}
	if t.pendingCR {
		text = "\r" + text
		t.pendingCR = false
	}
	if !final && strings.HasSuffix(text, "\r") {
		text = text[:len(text)-1]
		t.pendingCR = true
	}
	if strings.ContainsRune(text, '\r') {
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	}
	t.decoded = append(t.decoded, []rune(text)...)
	if final {
		t.eof = true
	}
}
