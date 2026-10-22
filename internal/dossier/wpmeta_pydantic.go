package dossier

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"gopkg.in/yaml.v3"
)

// str(pydantic.ValidationError) for WPMetadata.model_validate over a WP
// frontmatter mapping loaded by ruamel's round-trip loader
// (FrontmatterManager.read). Values keep their ruamel types in the
// input_type column (CommentedMap, CommentedSeq, ScalarFloat, TimeStamp,
// LiteralScalarString, ...).

// wpFieldOrder is WPMetadata's field declaration order.
var wpFieldOrder = strings.Fields(`work_package_id title dependencies base_branch base_commit created_at
	planning_base_branch merge_target_branch branch_strategy requirement_refs tracker_refs priority execution_mode
	owned_files create_intent authoritative_surface scope task_type subtasks phase phases assignee agent model
	agent_profile role shell_pid shell_pid_created_at history lane feature_slug activity_log review_status
	reviewed_by approved_by reviewer reviewer_agent reviewer_shell_pid review_feedback review_feedback_file
	subtitle description estimated_duration tags mission_id mission_number mission_slug status wp_code
	work_package_title`)

const wpLaneChoices = "planned, claimed, in_progress, for_review, in_review, approved, done, blocked, canceled, doing"

// rtValue is a frontmatter value with the ruamel type it was loaded as.
type rtValue struct {
	v    any
	node *yaml.Node // nil for values built by the model's before-validator
}

type wpLineError struct {
	loc      string
	msg, typ string
	input    rtValue
}

var (
	reRTDate = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
	reRTTime = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})(?:[Tt]|[ \t]+)(\d{1,2}):(\d{2}):(\d{2})(?:\.(\d*))?(?:[ \t]*(Z|[-+]\d{1,2}(?::?\d{2})?))?$`)
)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// rtTimestamp renders a YAML timestamp like ruamel's constructed value:
// (repr, type name).
func rtTimestamp(src string) (string, string) {
	if m := reRTDate.FindStringSubmatch(src); m != nil {
		return fmt.Sprintf("datetime.date(%d, %d, %d)", atoi(m[1]), atoi(m[2]), atoi(m[3])), "date"
	}
	m := reRTTime.FindStringSubmatch(src)
	if m == nil {
		return pyjson.ReprString(src), "str"
	}
	parts := []string{m[1], m[2], m[3], m[4], m[5]}
	nums := make([]string, 0, 7)
	for _, p := range parts {
		nums = append(nums, strconv.Itoa(atoi(p)))
	}
	sec, us := atoi(m[6]), 0
	if m[7] != "" {
		frac := (m[7] + "000000")[:6]
		us = atoi(frac)
	}
	if sec != 0 || us != 0 {
		nums = append(nums, strconv.Itoa(sec))
	}
	if us != 0 {
		nums = append(nums, strconv.Itoa(us))
	}
	if m[8] == "" {
		return "datetime.datetime(" + strings.Join(nums, ", ") + ")", "datetime"
	}
	tz := m[8]
	offset := 0
	if tz != "Z" {
		sign := 1
		if tz[0] == '-' {
			sign = -1
		}
		rest := strings.ReplaceAll(tz[1:], ":", "")
		h, mm := 0, 0
		if len(rest) <= 2 {
			h = atoi(rest)
		} else {
			h, mm = atoi(rest[:len(rest)-2]), atoi(rest[len(rest)-2:])
		}
		offset = sign * (h*3600 + mm*60)
	}
	var td string
	switch {
	case offset == 0:
		td = "datetime.timedelta(0)"
	case offset > 0:
		td = fmt.Sprintf("datetime.timedelta(seconds=%d)", offset)
	default:
		days := offset / 86400
		secs := offset - days*86400
		if secs < 0 {
			days--
			secs += 86400
		}
		td = fmt.Sprintf("datetime.timedelta(days=%d, seconds=%d)", days, secs)
	}
	return fmt.Sprintf("TimeStamp(%s, tzinfo=datetime.timezone(%s, %s))", strings.Join(nums, ", "), td, pyjson.ReprString(tz)), "TimeStamp"
}

// rtRepr is repr() of a ruamel round-trip value.
func rtRepr(x rtValue) string {
	n := x.node
	if n != nil && n.Kind == yaml.ScalarNode && n.ShortTag() == "!!timestamp" {
		r, _ := rtTimestamp(n.Value)
		return r
	}
	switch t := x.v.(type) {
	case *jsonx.Object:
		var b strings.Builder
		b.WriteByte('{')
		for i, kv := range t.Pairs() {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(pyjson.ReprString(kv.Key) + ": " + rtRepr(rtValue{kv.Value, childNode(n, kv.Key)}))
		}
		b.WriteByte('}')
		return b.String()
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = rtRepr(rtValue{e, seqNode(n, i)})
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return pyjson.Repr(x.v)
}

// rtType is type(value).__name__ for a ruamel round-trip value.
func rtType(x rtValue) string {
	n := x.node
	switch t := x.v.(type) {
	case *jsonx.Object:
		if n == nil {
			return "dict"
		}
		return "CommentedMap"
	case []any:
		if n == nil {
			return "list"
		}
		return "CommentedSeq"
	case float64:
		if n != nil && (strings.Contains(strings.ToLower(n.Value), "inf") || strings.Contains(strings.ToLower(n.Value), "nan")) {
			return "float"
		}
		return "ScalarFloat"
	case int, int64:
		if n != nil {
			v := strings.ToLower(n.Value)
			switch {
			case strings.HasPrefix(v, "0x"):
				return "HexInt"
			case strings.HasPrefix(v, "0o"):
				return "OctalInt"
			case strings.HasPrefix(v, "0b"):
				return "BinaryInt"
			case strings.Contains(v, "_"):
				return "ScalarInt"
			}
		}
		return "int"
	case string:
		if n != nil {
			if n.ShortTag() == "!!timestamp" {
				_, typ := rtTimestamp(n.Value)
				return typ
			}
			switch n.Style {
			case yaml.LiteralStyle:
				return "LiteralScalarString"
			case yaml.FoldedStyle:
				return "FoldedScalarString"
			}
		}
		_ = t
		return "str"
	}
	return pyjson.TypeName(x.v)
}

func childNode(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func seqNode(n *yaml.Node, i int) *yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode || i >= len(n.Content) {
		return nil
	}
	return n.Content[i]
}

// isTimestamp reports a scalar ruamel constructs as a date/datetime.
func isTimestamp(x rtValue) bool {
	return x.node != nil && x.node.Kind == yaml.ScalarNode && x.node.ShortTag() == "!!timestamp" && rtTypeIsTime(x.node.Value)
}

func rtTypeIsTime(src string) bool {
	_, typ := rtTimestamp(src)
	return typ != "str"
}

// rootNode parses the frontmatter source into its top-level mapping node.
func rootNode(src string) *yaml.Node {
	var n yaml.Node
	if yaml.Unmarshal([]byte(src), &n) != nil || len(n.Content) == 0 {
		return nil
	}
	if n.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return n.Content[0]
}

// pyIntLiteral is int(str) (base 10, surrounding whitespace and single
// underscores between digits allowed).
func pyIntLiteral(s string) bool {
	t := strings.TrimSpace(s)
	if t != "" && (t[0] == '+' || t[0] == '-') {
		t = t[1:]
	}
	if t == "" || strings.HasPrefix(t, "_") || strings.HasSuffix(t, "_") || strings.Contains(t, "__") {
		return false
	}
	for _, r := range strings.ReplaceAll(t, "_", "") {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// WPMetadataTypeError is the TypeError WPMetadata's shell_pid
// before-validator raises (int() of a mapping, sequence or date); it
// escapes pydantic and aborts validation. "" when not raised.
func WPMetadataTypeError(src string, meta *jsonx.Object) string {
	v, ok := meta.Get("shell_pid")
	if !ok || v == nil || v == "" {
		return ""
	}
	x := rtValue{v, childNode(rootNode(src), "shell_pid")}
	switch v.(type) {
	case string:
		if !isTimestamp(x) {
			return ""
		}
	case int, int64, float64, bool:
		return ""
	}
	typ := rtType(x)
	if typ == "date" || typ == "datetime" { // C types report their qualified tp_name
		typ = "datetime." + typ
	}
	return fmt.Sprintf("int() argument must be a string, a bytes-like object or a real number, not %s", pyjson.ReprString(typ))
}

// WPMetadataValidationError returns str(pydantic.ValidationError) for the
// WP frontmatter (src is the YAML text, meta its parsed mapping as
// readFrontmatter returns it), or "" when WPMetadata accepts it.
func WPMetadataValidationError(src string, meta *jsonx.Object) string {
	root := rootNode(src)
	data := jsonx.NewObject()
	nodes := map[string]*yaml.Node{}
	for _, kv := range meta.Pairs() {
		data.Set(kv.Key, kv.Value)
		nodes[kv.Key] = childNode(root, kv.Key)
	}
	// _normalize_legacy_fields (mode="before"), mutating the mapping.
	if !data.Has("title") && data.Has("work_package_title") {
		t, _ := data.Get("work_package_title")
		data.Set("title", t)
		nodes["title"] = nodes["work_package_title"]
	}
	if d, ok := data.Get("dependencies"); ok {
		if s, isStr := d.(string); isStr {
			l := []any{}
			if st := strings.TrimSpace(s); st != "[]" {
				for _, p := range strings.Split(st, ",") {
					if p = strings.TrimSpace(p); p != "" {
						l = append(l, p)
					}
				}
			}
			data.Set("dependencies", l)
			nodes["dependencies"] = nil
		}
	}
	if r, ok := data.Get("requirement_refs"); ok {
		if s, isStr := r.(string); isStr {
			l := []any{}
			for _, tok := range reRefSplit.Split(s, -1) {
				if strings.TrimSpace(tok) != "" {
					l = append(l, tok)
				}
			}
			data.Set("requirement_refs", l)
			nodes["requirement_refs"] = nil
		}
	}
	if r, ok := data.Get("tracker_refs"); ok {
		if s, isStr := r.(string); isStr {
			l := []any{}
			for _, p := range strings.Split(s, ",") {
				if p = strings.TrimSpace(p); p != "" {
					l = append(l, p)
				}
			}
			data.Set("tracker_refs", l)
			nodes["tracker_refs"] = nil
		}
	}
	whole := rtValue{data, root}
	var errs []wpLineError
	add := func(loc, msg, typ string, in rtValue) {
		errs = append(errs, wpLineError{loc: loc, msg: msg, typ: typ, input: in})
	}
	strField := func(f string, x rtValue) (string, bool) {
		s, ok := x.v.(string)
		if !ok || isTimestamp(x) {
			add(f, "Input should be a valid string", "string_type", x)
			return "", false
		}
		return s, true
	}
	for _, f := range wpFieldOrder {
		v, present := data.Get(f)
		if !present {
			if f == "work_package_id" {
				add(f, "Field required", "missing", whole)
			}
			continue
		}
		x := rtValue{v, nodes[f]}
		k := wpFields[f]
		switch {
		case f == "phase":
			// coerce_phase: str(v) before validation; always a valid str.
		case f == "shell_pid":
			if v == nil || v == "" {
				continue
			}
			switch t := v.(type) {
			case string:
				if !isTimestamp(x) && !pyIntLiteral(t) {
					add(f, "Value error, invalid literal for int() with base 10: "+pyjson.ReprString(t), "value_error", x)
				}
			case float64, int, int64, bool:
			default:
				add(f, "Input should be a valid integer", "int_type", x)
			}
		case f == "lane":
			if v == nil || v == "" {
				continue
			}
			s := pyStr(v)
			if s == "doing" {
				s = "in_progress"
			}
			if !validLanes[s] {
				add(f, fmt.Sprintf("Value error, Invalid lane value: %s. Must be one of: %s", rtRepr(x), wpLaneChoices), "value_error", x)
			}
		case k == kAny:
		case k == kStr:
			if v == nil && f != "work_package_id" {
				continue
			}
			s, ok := strField(f, x)
			if !ok {
				continue
			}
			switch f {
			case "work_package_id":
				if !reWPID.MatchString(s) {
					add(f, fmt.Sprintf("Value error, Invalid work_package_id: %s (must match WP##)", pyjson.ReprString(s)), "value_error", x)
				}
			case "title":
				if strings.TrimSpace(s) == "" {
					add(f, "Value error, title must not be empty", "value_error", x)
				}
			case "base_commit":
				if s != "unknown" && !reBaseCommit.MatchString(s) {
					add(f, fmt.Sprintf("Value error, Invalid base_commit: %s (must be hex SHA)", pyjson.ReprString(s)), "value_error", x)
				}
			}
		case k == kStrList || k == kList:
			l, ok := v.([]any)
			if !ok {
				add(f, "Input should be a valid list", "list_type", x)
				continue
			}
			if k == kStrList {
				for i, e := range l {
					ex := rtValue{e, seqNode(x.node, i)}
					if _, isStr := e.(string); !isStr || isTimestamp(ex) {
						add(fmt.Sprintf("%s.%d", f, i), "Input should be a valid string", "string_type", ex)
					}
				}
			}
		}
	}
	for _, kv := range data.Pairs() {
		if _, ok := wpFields[kv.Key]; !ok {
			add(kv.Key, "Extra inputs are not permitted", "extra_forbidden", rtValue{kv.Value, nodes[kv.Key]})
		}
	}
	if len(errs) == 0 {
		return ""
	}
	var b strings.Builder
	plural := "s"
	if len(errs) == 1 {
		plural = ""
	}
	fmt.Fprintf(&b, "%d validation error%s for WPMetadata", len(errs), plural)
	for _, e := range errs {
		r := []rune(rtRepr(e.input))
		repr := string(r)
		if len(r) > 50 {
			repr = string(r[:25]) + "..." + string(r[len(r)-24:])
		}
		fmt.Fprintf(&b, "\n%s\n  %s [type=%s, input_value=%s, input_type=%s]\n    For further information visit https://errors.pydantic.dev/2.14/v/%s",
			e.loc, e.msg, e.typ, repr, rtType(e.input), e.typ)
	}
	return b.String()
}
