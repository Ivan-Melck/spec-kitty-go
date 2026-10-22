package pydantic

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Standard pydantic-core error types and fixed messages.
const (
	TypeMissing        = "missing"
	MsgMissing         = "Field required"
	TypeExtraForbidden = "extra_forbidden"
	MsgExtraForbidden  = "Extra inputs are not permitted"
	TypeStringType     = "string_type"
	MsgStringType      = "Input should be a valid string"
	TypeIntType        = "int_type"
	MsgIntType         = "Input should be a valid integer"
	TypeIntParsing     = "int_parsing"
	MsgIntParsing      = "Input should be a valid integer, unable to parse string as an integer"
	TypeIntFromFloat   = "int_from_float"
	MsgIntFromFloat    = "Input should be a valid integer, got a number with a fractional part"
	TypeFloatType      = "float_type"
	MsgFloatType       = "Input should be a valid number"
	TypeBoolType       = "bool_type"
	MsgBoolType        = "Input should be a valid boolean"
	TypeBoolParsing    = "bool_parsing"
	MsgBoolParsing     = "Input should be a valid boolean, unable to interpret input"
	TypeListType       = "list_type"
	MsgListType        = "Input should be a valid list"
	TypeDictType       = "dict_type"
	MsgDictType        = "Input should be a valid dictionary"
	TypeModelType      = "model_type"
	TypeValueError     = "value_error"
	TypeAssertionError = "assertion_error"
	TypeLiteralError   = "literal_error"
	TypeEnum           = "enum"
	TypeStringTooShort = "string_too_short"
	TypeStringTooLong  = "string_too_long"
	TypeTooShort       = "too_short"
	TypeTooLong        = "too_long"
	TypePattern        = "string_pattern_mismatch"
	TypeGreaterEqual   = "greater_than_equal"
	TypeLessEqual      = "less_than_equal"
	TypeGreaterThan    = "greater_than"
	TypeDatetimeType   = "datetime_type"
	TypeDatetimeParse  = "datetime_from_date_parsing"
	TypeURLParsing     = "url_parsing"
)

// ModelTypeMsg is the model_type message for a model class name.
func ModelTypeMsg(class string) string {
	return "Input should be a valid dictionary or instance of " + class
}

// ValueErrorMsg is the message of a validator raising ValueError(msg).
func ValueErrorMsg(msg string) string { return "Value error, " + msg }

// AssertionErrorMsg is the message of a validator raising AssertionError.
func AssertionErrorMsg(msg string) string { return "Assertion failed, " + msg }

// ExpectedOr renders pydantic's "'a', 'b' or 'c'" expected list for
// literal_error / enum messages (each value repr'd).
func ExpectedOr(vals []any) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = Repr(v)
	}
	switch len(q) {
	case 0:
		return ""
	case 1:
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}

// ExpectedStrings is ExpectedOr over strings.
func ExpectedStrings(vals []string) string {
	a := make([]any, len(vals))
	for i, v := range vals {
		a[i] = v
	}
	return ExpectedOr(a)
}

// LiteralMsg is the literal_error / enum message.
func LiteralMsg(vals []string) string { return "Input should be " + ExpectedStrings(vals) }

// LiteralCtx is the ctx dict of literal_error / enum.
func LiteralCtx(vals []string) *jsonx.Object {
	return jsonx.NewObject("expected", ExpectedStrings(vals))
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// StringTooShortMsg is "String should have at least N character(s)".
func StringTooShortMsg(min int) string {
	return "String should have at least " + plural(min, "character")
}

// StringTooLongMsg is "String should have at most N character(s)".
func StringTooLongMsg(max int) string {
	return "String should have at most " + plural(max, "character")
}

// TooShortMsg is the too_short message for a collection kind ("List").
func TooShortMsg(kind string, min, actual int) string {
	return fmt.Sprintf("%s should have at least %s after validation, not %d", kind, plural(min, "item"), actual)
}

// TooLongMsg is the too_long message.
func TooLongMsg(kind string, max, actual int) string {
	return fmt.Sprintf("%s should have at most %s after validation, not %d", kind, plural(max, "item"), actual)
}

// PatternMsg is the string_pattern_mismatch message.
func PatternMsg(pattern string) string {
	return "String should match pattern " + pyjson.ReprString(pattern)
}

// GreaterEqualMsg is the greater_than_equal message.
func GreaterEqualMsg(ge any) string {
	return fmt.Sprintf("Input should be greater than or equal to %v", ge)
}

// LessEqualMsg is the less_than_equal message.
func LessEqualMsg(le any) string { return fmt.Sprintf("Input should be less than or equal to %v", le) }

// GreaterThanMsg is the greater_than message.
func GreaterThanMsg(gt any) string { return fmt.Sprintf("Input should be greater than %v", gt) }
