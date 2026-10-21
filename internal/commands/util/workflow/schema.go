package workflow

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/commands/util/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// pydanticVersion is the major.minor in the errors.pydantic.dev links the
// upstream install (pydantic 2.14) prints.
const pydanticVersion = "2.14"

// fieldError is one pydantic-core line error.
type fieldError struct {
	loc   []any
	msg   string
	typ   string
	input any
}

// ValidationError mirrors str(pydantic.ValidationError) for a model.
type ValidationError struct {
	Title  string
	Errors []fieldError
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	n := len(e.Errors)
	plural := "s"
	if n == 1 {
		plural = ""
	}
	fmt.Fprintf(&b, "%d validation error%s for %s", n, plural, e.Title)
	for _, fe := range e.Errors {
		b.WriteString("\n")
		if len(fe.loc) > 0 {
			parts := make([]string, len(fe.loc))
			for i, l := range fe.loc {
				parts[i] = fmt.Sprint(l)
			}
			b.WriteString(strings.Join(parts, "."))
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  %s [type=%s, input_value=%s, input_type=%s]\n", fe.msg, fe.typ, truncateRepr(pyRepr(fe.input)), pyType(fe.input))
		fmt.Fprintf(&b, "    For further information visit https://errors.pydantic.dev/%s/v/%s", pydanticVersion, fe.typ)
	}
	return b.String()
}

// truncateRepr is pydantic-core's input_value truncation: reprs longer than
// 50 characters keep the first 25 and last 24 around "...".
func truncateRepr(s string) string {
	r := []rune(s)
	if len(r) <= 50 {
		return s
	}
	return string(r[:25]) + "..." + string(r[len(r)-24:])
}

func pyType(v any) string {
	switch v.(type) {
	case pyyaml.Date:
		return "date"
	case pyyaml.Timestamp:
		return "datetime"
	}
	return pyjson.TypeName(v)
}

// pyRepr is repr() over PyYAML-loaded values (dates included).
func pyRepr(v any) string {
	switch t := v.(type) {
	case pyyaml.Date:
		return fmt.Sprintf("datetime.date(%d, %d, %d)", t.Y, t.M, t.D)
	case pyyaml.Timestamp:
		return reprDatetime(t.Repr)
	case *jsonx.Object:
		if t == nil {
			return "None"
		}
		parts := make([]string, 0, t.Len())
		for _, kv := range t.Pairs() {
			parts = append(parts, pyjson.ReprString(kv.Key)+": "+pyRepr(kv.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = pyRepr(x)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return pyjson.Repr(v)
}

// Step is a validated ActionStep.
type Step struct {
	ActionName string
	Next       []string
	Terminal   bool
}

// Sequence is a validated WorkflowSequence.
type Sequence struct {
	WorkflowID string
	Initial    string
	Version    int64
	Actions    []Step
}

type validator struct {
	errs []fieldError
}

func (v *validator) add(loc []any, msg, typ string, input any) {
	v.errs = append(v.errs, fieldError{append([]any(nil), loc...), msg, typ, input})
}

func with(loc []any, more ...any) []any {
	return append(append([]any(nil), loc...), more...)
}

func (v *validator) str(loc []any, in any) (string, bool) {
	if s, ok := in.(string); ok {
		return s, true
	}
	v.add(loc, "Input should be a valid string", "string_type", in)
	return "", false
}

func (v *validator) optStr(loc []any, in any) bool {
	if in == nil {
		return true
	}
	_, ok := v.str(loc, in)
	return ok
}

var reIntStr = regexp.MustCompile(`^[+-]?[0-9](?:_?[0-9])*(?:\.0*)?$`)

func (v *validator) int_(loc []any, in any) (int64, bool) {
	switch t := in.(type) {
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			v.add(loc, "Input should be a finite number", "finite_number", in)
			return 0, false
		}
		if t != math.Trunc(t) {
			v.add(loc, "Input should be a valid integer, got a number with a fractional part", "int_from_float", in)
			return 0, false
		}
		return int64(t), true
	case string:
		s := strings.TrimSpace(t)
		if reIntStr.MatchString(s) {
			s, _, _ = strings.Cut(strings.ReplaceAll(s, "_", ""), ".")
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n, true
			}
		}
		v.add(loc, "Input should be a valid integer, unable to parse string as an integer", "int_parsing", in)
		return 0, false
	}
	v.add(loc, "Input should be a valid integer", "int_type", in)
	return 0, false
}

func (v *validator) bool_(loc []any, in any) (bool, bool) {
	const parsing = "Input should be a valid boolean, unable to interpret input"
	switch t := in.(type) {
	case bool:
		return t, true
	case int64:
		if t == 0 || t == 1 {
			return t == 1, true
		}
		v.add(loc, parsing, "bool_parsing", in)
		return false, false
	case float64:
		if t == 0 || t == 1 {
			return t == 1, true
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "0", "off", "f", "false", "n", "no":
			return false, true
		case "1", "on", "t", "true", "y", "yes":
			return true, true
		}
		v.add(loc, parsing, "bool_parsing", in)
		return false, false
	}
	v.add(loc, "Input should be a valid boolean", "bool_type", in)
	return false, false
}

func (v *validator) list(loc []any, in any) ([]any, bool) {
	if l, ok := in.([]any); ok {
		return l, true
	}
	v.add(loc, "Input should be a valid list", "list_type", in)
	return nil, false
}

func (v *validator) dict(loc []any, in any) (*jsonx.Object, bool) {
	if o, ok := in.(*jsonx.Object); ok && o != nil {
		return o, true
	}
	v.add(loc, "Input should be a valid dictionary", "dict_type", in)
	return nil, false
}

var stepFields = []string{"action_name", "next", "description", "terminal", "agent_profile", "human_in_the_loop", "integration", "outputs"}

func (v *validator) step(loc []any, in any) (Step, bool) {
	var st Step
	// Field-order errors: missing/type errors interleave in declaration
	// order, so validate each field in turn.
	o, isObj := in.(*jsonx.Object)
	if !isObj || o == nil {
		v.add(loc, "Input should be a valid dictionary or instance of ActionStep", "model_type", in)
		return st, false
	}
	before := len(v.errs)
	for _, f := range stepFields {
		val, present := o.Get(f)
		fl := with(loc, f)
		switch f {
		case "action_name", "description":
			if !present {
				v.add(fl, "Field required", "missing", in)
				continue
			}
			s, _ := v.str(fl, val)
			if f == "action_name" {
				st.ActionName = s
			}
		case "next":
			if !present {
				continue
			}
			if l, ok := v.list(fl, val); ok {
				for i, x := range l {
					if s, ok := v.str(with(fl, i), x); ok {
						st.Next = append(st.Next, s)
					}
				}
			}
		case "terminal":
			if present {
				st.Terminal, _ = v.bool_(fl, val)
			}
		case "agent_profile", "human_in_the_loop", "integration":
			if present {
				v.optStr(fl, val)
			}
		case "outputs":
			if !present {
				continue
			}
			if d, ok := v.dict(fl, val); ok {
				for _, kv := range d.Pairs() {
					v.str(with(fl, kv.Key), kv.Value)
				}
			}
		}
	}
	v.extras(loc, o, stepFields)
	if len(v.errs) != before {
		return st, false
	}
	if st.Terminal && len(st.Next) > 0 {
		v.add(loc, fmt.Sprintf("Value error, action %s: terminal=True forbids non-empty `next`", pyjson.ReprString(st.ActionName)), "value_error", in)
		return st, false
	}
	if len(st.Next) > 1 {
		v.add(loc, fmt.Sprintf("Value error, action %s: workflows are linear in v1; `next` may name at most one action", pyjson.ReprString(st.ActionName)), "value_error", in)
		return st, false
	}
	return st, true
}

func (v *validator) extras(loc []any, o *jsonx.Object, fields []string) {
	declared := map[string]bool{}
	for _, f := range fields {
		declared[f] = true
	}
	for _, kv := range o.Pairs() {
		if !declared[kv.Key] {
			v.add(with(loc, kv.Key), "Extra inputs are not permitted", "extra_forbidden", kv.Value)
		}
	}
}

var seqFields = []string{"workflow_id", "description", "actions", "initial", "version", "integrations"}

// Validate is WorkflowSequence.model_validate(raw).
func Validate(raw any) (*Sequence, error) {
	v := &validator{}
	seq := &Sequence{}
	o, isObj := raw.(*jsonx.Object)
	if !isObj || o == nil {
		v.add(nil, "Input should be a valid dictionary or instance of WorkflowSequence", "model_type", raw)
		return nil, &ValidationError{"WorkflowSequence", v.errs}
	}
	for _, f := range seqFields {
		val, present := o.Get(f)
		loc := []any{f}
		if !present {
			if f != "integrations" {
				v.add(loc, "Field required", "missing", raw)
			}
			continue
		}
		switch f {
		case "workflow_id":
			seq.WorkflowID, _ = v.str(loc, val)
		case "description":
			v.str(loc, val)
		case "initial":
			seq.Initial, _ = v.str(loc, val)
		case "version":
			if n, ok := v.int_(loc, val); ok {
				if n < 1 {
					v.add(loc, "Input should be greater than or equal to 1", "greater_than_equal", val)
				}
				seq.Version = n
			}
		case "integrations":
			v.dict(loc, val)
		case "actions":
			if l, ok := v.list(loc, val); ok {
				for i, item := range l {
					if st, ok := v.step([]any{f, i}, item); ok {
						seq.Actions = append(seq.Actions, st)
					}
				}
			}
		}
	}
	v.extras(nil, o, seqFields)
	if len(v.errs) > 0 {
		return nil, &ValidationError{"WorkflowSequence", v.errs}
	}
	if msg := seq.invariants(); msg != "" {
		v.add(nil, "Value error, "+msg, "value_error", raw)
		return nil, &ValidationError{"WorkflowSequence", v.errs}
	}
	return seq, nil
}

func (s *Sequence) invariants() string {
	names := map[string]bool{}
	for _, a := range s.Actions {
		if names[a.ActionName] {
			return "action_name values must be unique within a workflow"
		}
		names[a.ActionName] = true
	}
	if !names[s.Initial] {
		return fmt.Sprintf("initial=%s not in actions", pyjson.ReprString(s.Initial))
	}
	for _, a := range s.Actions {
		for _, n := range a.Next {
			if !names[n] {
				return fmt.Sprintf("action %s references unknown next: %s", pyjson.ReprString(a.ActionName), pyjson.ReprString(n))
			}
		}
	}
	byName := map[string]Step{}
	for _, a := range s.Actions {
		byName[a.ActionName] = a
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var cycle string
	var dfs func(string) bool
	dfs = func(node string) bool {
		if visited[node] {
			return true
		}
		if visiting[node] {
			cycle = fmt.Sprintf("cycle detected at action %s", pyjson.ReprString(node))
			return false
		}
		visiting[node] = true
		for _, n := range byName[node].Next {
			if !dfs(n) {
				return false
			}
		}
		delete(visiting, node)
		visited[node] = true
		return true
	}
	if !dfs(s.Initial) {
		return cycle
	}
	var unreachable []string
	for n := range byName {
		if !visited[n] {
			unreachable = append(unreachable, n)
		}
	}
	if len(unreachable) > 0 {
		sort.Strings(unreachable)
		return "unreachable workflow action(s): " + pyjson.ReprStrings(unreachable)
	}
	return ""
}

// reprDatetime renders repr(datetime) from PyYAML's isoformat(' ') value
// ("YYYY-MM-DD HH:MM:SS[.ffffff][+HH:MM]").
func reprDatetime(iso string) string {
	var y, mo, d, h, mi, se, us int
	rest := iso
	if len(rest) < 19 {
		return "datetime.datetime(" + iso + ")"
	}
	fmt.Sscanf(rest[:19], "%04d-%02d-%02d %02d:%02d:%02d", &y, &mo, &d, &h, &mi, &se)
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
