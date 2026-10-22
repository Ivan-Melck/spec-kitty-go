// Package pydantic reproduces the observable error text of pydantic v2
// (pydantic-core, as installed upstream: pydantic 2.14): str(ValidationError),
// repr(err.errors()) and the standard per-type messages. It is a formatter,
// not a validator: callers collect ErrorDetail values while checking a
// document against the upstream model and render them here.
package pydantic

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// Version is the major.minor in the errors.pydantic.dev links.
const Version = "2.14"

// URL is the documentation link pydantic appends for an error type.
func URL(typ string) string { return "https://errors.pydantic.dev/" + Version + "/v/" + typ }

// ErrorDetail is one pydantic ErrorDetails entry.
type ErrorDetail struct {
	// Loc is the location tuple: string keys and int indexes.
	Loc []any
	// Msg is the human message ("Field required").
	Msg string
	// Type is the error type ("missing").
	Type string
	// Input is the offending input value.
	Input any
	// Ctx is the optional ctx dict (repr'd in DictRepr).
	Ctx *jsonx.Object
	// HideInput omits input_value/input_type (hide_input_in_errors).
	HideInput bool
	// InputType overrides type(input).__name__ (e.g. ruamel's CommentedMap).
	InputType string
}

// Detail builds an ErrorDetail from a dotted location ("a.0.b"): numeric
// parts become int indexes.
func Detail(dotted, typ, msg string, input any) ErrorDetail {
	return ErrorDetail{Loc: SplitLoc(dotted), Msg: msg, Type: typ, Input: input}
}

// SplitLoc parses a dotted location; numeric parts become ints.
func SplitLoc(dotted string) []any {
	if dotted == "" {
		return nil
	}
	parts := strings.Split(dotted, ".")
	out := make([]any, len(parts))
	for i, p := range parts {
		if n, err := strconv.Atoi(p); err == nil && p == strconv.Itoa(n) {
			out[i] = n
		} else {
			out[i] = p
		}
	}
	return out
}

// FormatLoc is the location line of str(ValidationError) ("a.0.b").
func FormatLoc(loc []any) string {
	parts := make([]string, len(loc))
	for i, l := range loc {
		parts[i] = fmt.Sprint(l)
	}
	return strings.Join(parts, ".")
}

// ValidationError mirrors pydantic_core.ValidationError.
type ValidationError struct {
	// Title is the model (or TypeAdapter type) name.
	Title  string
	Errors []ErrorDetail
}

// New returns a ValidationError for title with errs.
func New(title string, errs ...ErrorDetail) *ValidationError {
	return &ValidationError{Title: title, Errors: errs}
}

// Add appends an error at a dotted location.
func (e *ValidationError) Add(dotted, typ, msg string, input any) {
	e.Errors = append(e.Errors, Detail(dotted, typ, msg, input))
}

// Len is the number of errors.
func (e *ValidationError) Len() int { return len(e.Errors) }

// Error is str(ValidationError).
func (e *ValidationError) Error() string { return Format(e.Title, e.Errors) }

// Format renders str(ValidationError) for title and errs.
func Format(title string, errs []ErrorDetail) string {
	var b strings.Builder
	n := len(errs)
	if n == 1 {
		fmt.Fprintf(&b, "1 validation error for %s", title)
	} else {
		fmt.Fprintf(&b, "%d validation errors for %s", n, title)
	}
	for _, d := range errs {
		b.WriteString("\n")
		if len(d.Loc) > 0 {
			b.WriteString(FormatLoc(d.Loc))
			b.WriteString("\n")
		}
		if d.HideInput {
			fmt.Fprintf(&b, "  %s [type=%s]\n", d.Msg, d.Type)
		} else {
			it := d.InputType
			if it == "" {
				it = TypeName(d.Input)
			}
			fmt.Fprintf(&b, "  %s [type=%s, input_value=%s, input_type=%s]\n", d.Msg, d.Type, InputRepr(d.Input), it)
		}
		fmt.Fprintf(&b, "    For further information visit %s", URL(d.Type))
	}
	return b.String()
}

// ErrorsRepr is repr(err.errors()).
func (e *ValidationError) ErrorsRepr() string {
	parts := make([]string, len(e.Errors))
	for i, d := range e.Errors {
		parts[i] = d.DictRepr()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// LocRepr is repr(tuple(loc)).
func LocRepr(loc []any) string {
	parts := make([]string, len(loc))
	for i, l := range loc {
		parts[i] = Repr(l)
	}
	s := "(" + strings.Join(parts, ", ")
	if len(parts) == 1 {
		s += ","
	}
	return s + ")"
}

// DictRepr is repr() of the ErrorDetails dict.
func (d ErrorDetail) DictRepr() string {
	out := "{'type': " + pyjson.ReprString(d.Type) + ", 'loc': " + LocRepr(d.Loc) + ", 'msg': " + pyjson.ReprString(d.Msg)
	if !d.HideInput {
		out += ", 'input': " + Repr(d.Input)
	}
	if d.Ctx != nil && d.Ctx.Len() > 0 {
		out += ", 'ctx': " + Repr(d.Ctx)
	}
	return out + ", 'url': " + pyjson.ReprString(URL(d.Type)) + "}"
}

// Truncate is pydantic-core's input_value truncation: reprs longer than 50
// characters keep the first 25 and the last 24 around "...".
func Truncate(s string) string {
	r := []rune(s)
	if len(r) <= 50 {
		return s
	}
	return string(r[:25]) + "..." + string(r[len(r)-24:])
}

// InputRepr is the truncated input_value repr.
func InputRepr(v any) string { return Truncate(Repr(v)) }

// PyValue lets callers supply repr()/type().__name__ for custom values.
type PyValue interface {
	PyRepr() string
	PyTypeName() string
}

// Repr is Python repr() of a parsed (JSON/YAML) value, including PyYAML
// dates and timestamps.
func Repr(v any) string {
	switch t := v.(type) {
	case PyValue:
		return t.PyRepr()
	case pyyaml.Date:
		return fmt.Sprintf("datetime.date(%d, %d, %d)", t.Y, t.M, t.D)
	case pyyaml.Timestamp:
		return ReprDatetime(t.Repr)
	case *jsonx.Object:
		if t == nil {
			return "None"
		}
		parts := make([]string, 0, t.Len())
		for _, kv := range t.Pairs() {
			parts = append(parts, pyjson.ReprString(kv.Key)+": "+Repr(kv.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = Repr(x)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		return pyjson.ReprStrings(t)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	return pyjson.Repr(v)
}

// TypeName is type(v).__name__ for parsed values.
func TypeName(v any) string {
	switch t := v.(type) {
	case PyValue:
		return t.PyTypeName()
	case pyyaml.Date:
		return "date"
	case pyyaml.Timestamp:
		return "datetime"
	case []string:
		return "list"
	}
	return pyjson.TypeName(v)
}

// ReprDatetime renders repr(datetime) from an isoformat string (' ' or 'T'
// separator, optional microseconds and UTC offset).
func ReprDatetime(iso string) string {
	var y, mo, d, h, mi, se, us int
	rest := iso
	if len(rest) < 19 {
		return "datetime.datetime(" + iso + ")"
	}
	fmt.Sscanf(strings.Replace(rest[:19], "T", " ", 1), "%04d-%02d-%02d %02d:%02d:%02d", &y, &mo, &d, &h, &mi, &se)
	rest = rest[19:]
	if strings.HasPrefix(rest, ".") && len(rest) >= 7 {
		us, _ = strconv.Atoi(rest[1:7])
		rest = rest[7:]
	}
	parts := []string{strconv.Itoa(y), strconv.Itoa(mo), strconv.Itoa(d), strconv.Itoa(h), strconv.Itoa(mi)}
	if se != 0 || us != 0 {
		parts = append(parts, strconv.Itoa(se))
	}
	if us != 0 {
		parts = append(parts, strconv.Itoa(us))
	}
	if rest == "Z" {
		rest = "+00:00"
	}
	if len(rest) > 1 {
		var th, tm int
		fmt.Sscanf(rest[1:], "%02d:%02d", &th, &tm)
		secs := th*3600 + tm*60
		if rest[0] == '-' {
			secs = -secs
		}
		switch {
		case secs == 0:
			parts = append(parts, "tzinfo=datetime.timezone.utc")
		case secs > 0:
			parts = append(parts, fmt.Sprintf("tzinfo=datetime.timezone(datetime.timedelta(seconds=%d))", secs))
		default:
			parts = append(parts, fmt.Sprintf("tzinfo=datetime.timezone(datetime.timedelta(days=-1, seconds=%d))", 86400+secs))
		}
	}
	return "datetime.datetime(" + strings.Join(parts, ", ") + ")"
}

// RuamelTypeName is type(v).__name__ for a value ruamel.yaml's round-trip
// loader produced (CommentedMap / CommentedSeq containers, ScalarFloat).
func RuamelTypeName(v any) string {
	switch v.(type) {
	case *jsonx.Object:
		return "CommentedMap"
	case []any:
		return "CommentedSeq"
	case float64:
		return "ScalarFloat"
	}
	return TypeName(v)
}
