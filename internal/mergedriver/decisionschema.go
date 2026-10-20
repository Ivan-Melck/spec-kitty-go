package mergedriver

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// This file reproduces the observable behaviour of pydantic (v2.14) validating
// decisions.models.DecisionIndex in lax mode and dumping it with
// model_dump(mode="json"): accepted inputs, coercions, datetime parsing
// (speedate) and the str(ValidationError) text.

type pydErr struct {
	loc   string
	msg   string
	typ   string
	input any
}

type pydValidator struct{ errs []pydErr }

func (v *pydValidator) add(loc, typ, msg string, input any) {
	v.errs = append(v.errs, pydErr{loc, msg, typ, input})
}

// pydInputRepr is pydantic-core's truncated input_value repr.
func pydInputRepr(x any) string {
	r := []rune(pyjson.Repr(x))
	if len(r) > 50 {
		return string(r[:25]) + "..." + string(r[len(r)-24:])
	}
	return string(r)
}

func (v *pydValidator) String(model string) string {
	var b strings.Builder
	n := len(v.errs)
	if n == 1 {
		fmt.Fprintf(&b, "1 validation error for %s", model)
	} else {
		fmt.Fprintf(&b, "%d validation errors for %s", n, model)
	}
	for _, e := range v.errs {
		fmt.Fprintf(&b, "\n%s\n  %s [type=%s, input_value=%s, input_type=%s]\n    For further information visit https://errors.pydantic.dev/2.14/v/%s",
			e.loc, e.msg, e.typ, pydInputRepr(e.input), pyjson.TypeName(e.input), e.typ)
	}
	return b.String()
}

func joinLoc(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func (v *pydValidator) str(loc string, x any, minLen int) (string, bool) {
	s, ok := x.(string)
	if !ok {
		v.add(loc, "string_type", "Input should be a valid string", x)
		return "", false
	}
	if len([]rune(s)) < minLen {
		v.add(loc, "string_too_short", fmt.Sprintf("String should have at least %d character%s", minLen, plural(minLen)), x)
		return "", false
	}
	return s, true
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (v *pydValidator) optStr(loc string, x any) (any, bool) {
	if x == nil {
		return nil, true
	}
	s, ok := v.str(loc, x, 0)
	return s, ok
}

func (v *pydValidator) enum(loc string, x any, values []string) (string, bool) {
	if s, ok := x.(string); ok {
		for _, val := range values {
			if s == val {
				return s, true
			}
		}
	}
	q := make([]string, len(values))
	for i, val := range values {
		q[i] = "'" + val + "'"
	}
	msg := "Input should be " + strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
	v.add(loc, "enum", msg, x)
	return "", false
}

func (v *pydValidator) boolean(loc string, x any) (bool, bool) {
	switch t := x.(type) {
	case bool:
		return t, true
	case int:
		if t == 0 || t == 1 {
			return t == 1, true
		}
	case float64:
		if t == 0 || t == 1 {
			return t == 1, true
		}
	case string:
		switch strings.ToLower(t) {
		case "0", "off", "f", "false", "n", "no":
			return false, true
		case "1", "on", "t", "true", "y", "yes":
			return true, true
		}
	case json.Number, int64:
	default:
		v.add(loc, "bool_type", "Input should be a valid boolean", x)
		return false, false
	}
	v.add(loc, "bool_parsing", "Input should be a valid boolean, unable to interpret input", x)
	return false, false
}

// pydDatetime is a parsed datetime (speedate) with its two renderings.
type pydDatetime struct {
	json string // pydantic JSON form (Z for UTC)
	iso  string // Python datetime.isoformat()
}

func (v *pydValidator) datetime(loc string, x any, optional bool) (any, *pydDatetime, bool) {
	if x == nil && optional {
		return nil, nil, true
	}
	var dt *pydDatetime
	var perr string
	switch t := x.(type) {
	case string:
		dt, perr = parseSpeedateDatetime(t)
	case int:
		dt = timestampDatetime(float64(t))
	case int64:
		dt = timestampDatetime(float64(t))
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			perr = "invalid"
		} else {
			dt = timestampDatetime(t)
		}
	default:
		v.add(loc, "datetime_type", "Input should be a valid datetime", x)
		return nil, nil, false
	}
	if dt == nil {
		v.add(loc, "datetime_from_date_parsing", "Input should be a valid datetime or date, "+perr, x)
		return nil, nil, false
	}
	return dt.json, dt, true
}

var decisionEntryFields = []string{"decision_id", "origin_flow", "step_id", "slot_key", "input_key", "question", "options",
	"status", "final_answer", "rationale", "other_answer", "summary_json", "created_at", "resolved_at", "resolved_by",
	"opened_by", "mission_id", "mission_slug"}

type validatedEntry struct {
	obj        *jsonx.Object
	createdISO string
	id         string
}

func (v *pydValidator) indexEntry(loc string, x any) (*validatedEntry, bool) {
	in, ok := x.(*jsonx.Object)
	if !ok {
		v.add(loc, "model_type", "Input should be a valid dictionary or instance of IndexEntry", x)
		return nil, false
	}
	before := len(v.errs)
	out := &jsonx.Object{}
	ve := &validatedEntry{obj: out}
	required := func(name string) (any, bool) {
		val, ok := in.Get(name)
		if !ok {
			v.add(joinLoc(loc, name), "missing", "Field required", in)
		}
		return val, ok
	}
	optional := func(name string, def any) (any, bool) {
		val, ok := in.Get(name)
		if !ok {
			return def, false
		}
		return val, true
	}
	for _, f := range decisionEntryFields {
		l := joinLoc(loc, f)
		switch f {
		case "decision_id", "mission_id", "mission_slug", "input_key", "question":
			val, ok := required(f)
			if !ok {
				continue
			}
			min := 0
			if f == "input_key" || f == "question" {
				min = 1
			}
			if s, ok := v.str(l, val, min); ok {
				out.Set(f, s)
				if f == "decision_id" {
					ve.id = s
				}
			}
		case "origin_flow":
			if val, ok := required(f); ok {
				if s, ok := v.enum(l, val, []string{"charter", "specify", "plan"}); ok {
					out.Set(f, s)
				}
			}
		case "status":
			if val, ok := required(f); ok {
				if s, ok := v.enum(l, val, decisionStatuses); ok {
					out.Set(f, s)
				}
			}
		case "step_id", "slot_key", "final_answer", "rationale", "resolved_by", "opened_by":
			val, _ := optional(f, nil)
			if s, ok := v.optStr(l, val); ok {
				out.Set(f, s)
			}
		case "options":
			val, present := optional(f, nil)
			if !present {
				out.Set(f, []any{})
				continue
			}
			list, ok := val.([]any)
			if !ok {
				v.add(l, "tuple_type", "Input should be a valid tuple", val)
				continue
			}
			res := []any{}
			for i, item := range list {
				if s, ok := v.str(joinLoc(l, strconv.Itoa(i)), item, 0); ok {
					res = append(res, s)
				}
			}
			out.Set(f, res)
		case "other_answer":
			val, present := optional(f, false)
			if !present {
				out.Set(f, false)
				continue
			}
			if b, ok := v.boolean(l, val); ok {
				out.Set(f, b)
			}
		case "summary_json":
			val, _ := optional(f, nil)
			if val == nil {
				out.Set(f, nil)
				continue
			}
			d, ok := val.(*jsonx.Object)
			if !ok {
				v.add(l, "dict_type", "Input should be a valid dictionary", val)
				continue
			}
			res := &jsonx.Object{}
			for _, kv := range d.Pairs() {
				if s, ok := v.str(joinLoc(l, kv.Key), kv.Value, 0); ok {
					res.Set(kv.Key, s)
				}
			}
			out.Set(f, res)
		case "created_at":
			if val, ok := required(f); ok {
				if j, dt, ok := v.datetime(l, val, false); ok {
					out.Set(f, j)
					ve.createdISO = dt.iso
				}
			}
		case "resolved_at":
			val, _ := optional(f, nil)
			if j, _, ok := v.datetime(l, val, true); ok {
				out.Set(f, j)
			}
		}
	}
	known := map[string]bool{}
	for _, f := range decisionEntryFields {
		known[f] = true
	}
	for _, kv := range in.Pairs() {
		if !known[kv.Key] {
			v.add(joinLoc(loc, kv.Key), "extra_forbidden", "Extra inputs are not permitted", kv.Value)
		}
	}
	if len(v.errs) > before {
		return nil, false
	}
	step, _ := out.Get("step_id")
	slot, _ := out.Get("slot_key")
	if !pyjson.Truthy(step) && !pyjson.Truthy(slot) {
		v.add(loc, "value_error", "Value error, step_id or slot_key required", in)
		return nil, false
	}
	return ve, true
}

// ValidateDecisionIndex validates doc as a DecisionIndex and returns its
// model_dump(mode="json") form with entries sorted by
// (created_at.isoformat(), decision_id). On failure it returns the
// str(ValidationError) text.
func ValidateDecisionIndex(doc *jsonx.Object) (*jsonx.Object, string) {
	v := &pydValidator{}
	out := &jsonx.Object{}
	if ver, ok := doc.Get("version"); ok {
		if !pyjson.Equal(ver, 1) || !isNumber(ver) {
			v.add("version", "literal_error", "Input should be 1", ver)
		}
	}
	out.Set("version", 1)
	if mid, ok := doc.Get("mission_id"); !ok {
		v.add("mission_id", "missing", "Field required", doc)
	} else if s, ok := v.str("mission_id", mid, 0); ok {
		out.Set("mission_id", s)
	}
	var entries []*validatedEntry
	if raw, ok := doc.Get("entries"); ok {
		list, isList := raw.([]any)
		if !isList {
			v.add("entries", "tuple_type", "Input should be a valid tuple", raw)
		}
		for i, e := range list {
			if ve, ok := v.indexEntry(joinLoc("entries", strconv.Itoa(i)), e); ok {
				entries = append(entries, ve)
			}
		}
	}
	for _, k := range doc.Keys() {
		if k != "version" && k != "mission_id" && k != "entries" {
			val, _ := doc.Get(k)
			v.add(k, "extra_forbidden", "Extra inputs are not permitted", val)
		}
	}
	if len(v.errs) > 0 {
		return nil, v.String("DecisionIndex")
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].createdISO != entries[j].createdISO {
			return entries[i].createdISO < entries[j].createdISO
		}
		return entries[i].id < entries[j].id
	})
	list := make([]any, len(entries))
	for i, e := range entries {
		list[i] = e.obj
	}
	out.Set("entries", list)
	return out, ""
}

func isNumber(x any) bool {
	switch x.(type) {
	case bool, int, int64, float64, json.Number:
		return true
	}
	return false
}

// --- speedate datetime parsing ---------------------------------------------

func digits(s string, i, n int) (int, bool) {
	if i+n > len(s) {
		return 0, false
	}
	v := 0
	for k := 0; k < n; k++ {
		c := s[i+k]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	return v, true
}

func daysIn(year, month int) int {
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// parseSpeedateDate parses YYYY-MM-DD at the start of s; err is the
// speedate error text.
func parseSpeedateDate(s string) (y, m, d int, err string) {
	if len(s) < 10 {
		return 0, 0, 0, "input is too short"
	}
	var ok bool
	if y, ok = digits(s, 0, 4); !ok {
		return 0, 0, 0, "invalid character in year"
	}
	if s[4] != '-' {
		return 0, 0, 0, "invalid date separator, expected `-`"
	}
	if m, ok = digits(s, 5, 2); !ok {
		return 0, 0, 0, "invalid character in month"
	}
	if s[7] != '-' {
		return 0, 0, 0, "invalid date separator, expected `-`"
	}
	if d, ok = digits(s, 8, 2); !ok {
		return 0, 0, 0, "invalid character in day"
	}
	if m < 1 || m > 12 {
		return 0, 0, 0, "month value is outside expected range of 1-12"
	}
	if d < 1 || d > daysIn(y, m) {
		return 0, 0, 0, "day value is outside expected range"
	}
	return y, m, d, ""
}

func isNumericString(s string) bool {
	t := strings.TrimPrefix(s, "-")
	if t == "" {
		return false
	}
	dot := false
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && !dot && i > 0:
			dot = true
		default:
			return false
		}
	}
	return true
}

func parseSpeedateDatetime(s string) (*pydDatetime, string) {
	if isNumericString(s) {
		f, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return timestampDatetime(f), ""
		}
	}
	if dt := parseFullDatetime(s); dt != nil {
		return dt, ""
	}
	y, m, d, err := parseSpeedateDate(s)
	if err != "" {
		return nil, err
	}
	if len(s) > 10 {
		return nil, "unexpected extra characters at the end of the input"
	}
	return formatDatetime(y, m, d, 0, 0, 0, 0, nil), ""
}

func parseFullDatetime(s string) *pydDatetime {
	y, m, d, err := parseSpeedateDate(s)
	if err != "" || len(s) < 16 {
		return nil
	}
	switch s[10] {
	case 'T', 't', '_', ' ':
	default:
		return nil
	}
	i := 11
	hh, ok1 := digits(s, i, 2)
	if !ok1 || i+2 >= len(s) || s[i+2] != ':' {
		return nil
	}
	mm, ok2 := digits(s, i+3, 2)
	if !ok2 || hh > 23 || mm > 59 {
		return nil
	}
	i += 5
	ss, micro := 0, 0
	if i < len(s) && s[i] == ':' {
		var ok bool
		if ss, ok = digits(s, i+1, 2); !ok || ss > 59 {
			return nil
		}
		i += 3
		if i < len(s) && (s[i] == '.' || s[i] == ',') {
			i++
			n := 0
			start := i
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				if n < 6 {
					micro = micro*10 + int(s[i]-'0')
					n++
				}
				i++
			}
			if i == start {
				return nil
			}
			for ; n < 6; n++ {
				micro *= 10
			}
		}
	}
	var offset *int
	if i < len(s) {
		switch s[i] {
		case 'Z', 'z':
			z := 0
			offset = &z
			i++
		case '+', '-':
			sign := 1
			if s[i] == '-' {
				sign = -1
			}
			oh, ok := digits(s, i+1, 2)
			if !ok {
				return nil
			}
			j := i + 3
			if j < len(s) && s[j] == ':' {
				j++
			}
			om, ok := digits(s, j, 2)
			if !ok || oh > 23 || om > 59 {
				return nil
			}
			o := sign * (oh*3600 + om*60)
			offset = &o
			i = j + 2
		default:
			return nil
		}
	}
	if i != len(s) {
		return nil
	}
	return formatDatetime(y, m, d, hh, mm, ss, micro, offset)
}

func timestampDatetime(f float64) *pydDatetime {
	if math.Abs(f) > 20_000_000_000 {
		f /= 1000
	}
	sec := math.Floor(f)
	micro := int(math.Round((f - sec) * 1e6))
	if micro >= 1_000_000 {
		sec++
		micro -= 1_000_000
	}
	total := int64(sec)
	days := total / 86400
	rem := total % 86400
	if rem < 0 {
		rem += 86400
		days--
	}
	y, m, d := civilFromDays(days)
	z := 0
	return formatDatetime(y, m, d, int(rem/3600), int(rem%3600/60), int(rem%60), micro, &z)
}

// civilFromDays converts days since 1970-01-01 to a proleptic Gregorian date.
func civilFromDays(z int64) (int, int, int) {
	z += 719468
	era := z / 146097
	if z < 0 && z%146097 != 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return int(y), int(m), int(d)
}

func formatDatetime(y, m, d, hh, mm, ss, micro int, offset *int) *pydDatetime {
	base := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d", y, m, d, hh, mm, ss)
	if micro != 0 {
		base += fmt.Sprintf(".%06d", micro)
	}
	if offset == nil {
		return &pydDatetime{json: base, iso: base}
	}
	o := *offset
	sign := "+"
	if o < 0 {
		sign = "-"
		o = -o
	}
	tz := fmt.Sprintf("%s%02d:%02d", sign, o/3600, o%3600/60)
	if *offset == 0 {
		return &pydDatetime{json: base + "Z", iso: base + tz}
	}
	return &pydDatetime{json: base + tz, iso: base + tz}
}
