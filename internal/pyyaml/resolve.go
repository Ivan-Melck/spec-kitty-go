package pyyaml

import "regexp"

// ruamel.yaml's VersionedResolver implicit resolvers (YAML 1.2 by
// default, 1.1 under a %YAML 1.1 directive), keyed by first character in
// registration order.
type implicitResolver struct {
	tag   string
	re    *regexp.Regexp
	first string // "" entry = matches the empty string
	empty bool
}

var (
	reBool12  = regexp.MustCompile(`^(?:true|True|TRUE|false|False|FALSE)$`)
	reBool11  = regexp.MustCompile(`^(?:y|Y|yes|Yes|YES|n|N|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`)
	reFloat12 = regexp.MustCompile(`^(?:[-+]?(?:[0-9][0-9_]*)\.[0-9_]*(?:[eE][-+]?[0-9]+)?|[-+]?(?:[0-9][0-9_]*)(?:[eE][-+]?[0-9]+)|[-+]?\.[0-9_]+(?:[eE][-+][0-9]+)?|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
	reFloat11 = regexp.MustCompile(`^(?:[-+]?(?:[0-9][0-9_]*)\.[0-9_]*(?:[eE][-+]?[0-9]+)?|[-+]?(?:[0-9][0-9_]*)(?:[eE][-+]?[0-9]+)|\.[0-9_]+(?:[eE][-+][0-9]+)?|[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+\.[0-9_]*|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
	reInt12   = regexp.MustCompile(`^(?:[-+]?0b[0-1_]+|[-+]?0o?[0-7_]+|[-+]?[0-9_]+|[-+]?0x[0-9a-fA-F_]+)$`)
	reInt11   = regexp.MustCompile(`^(?:[-+]?0b[0-1_]+|[-+]?0?[0-7_]+|[-+]?(?:0|[1-9][0-9_]*)|[-+]?0x[0-9a-fA-F_]+|[-+]?[1-9][0-9_]*(?::[0-5]?[0-9])+)$`)
	reMerge   = regexp.MustCompile(`^(?:<<)$`)
	reNullR   = regexp.MustCompile(`^(?:~|null|Null|NULL|)$`)
	reTSR     = regexp.MustCompile(`^(?:[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]|[0-9][0-9][0-9][0-9]-[0-9][0-9]?-[0-9][0-9]?(?:[Tt]|[ \t]+)[0-9][0-9]?:[0-9][0-9]:[0-9][0-9](?:\.[0-9]*)?(?:[ \t]*(?:Z|[-+][0-9][0-9]?(?::[0-9][0-9])?))?)$`)
	reValue   = regexp.MustCompile(`^(?:=)$`)
	reYAML    = regexp.MustCompile(`^(?:!|&|\*)$`)
)

func ruamelResolvers(v11 bool) []implicitResolver {
	bool_, float_, int_ := reBool12, reFloat12, reInt12
	boolFirst := "tTfF"
	if v11 {
		bool_, float_, int_ = reBool11, reFloat11, reInt11
		boolFirst = "yYnNtTfFoO"
	}
	return []implicitResolver{
		{TagBool, bool_, boolFirst, false},
		{TagFloat, float_, "-+0123456789.", false},
		{TagInt, int_, "-+0123456789", false},
		{TagMerge, reMerge, "<", false},
		{TagNull, reNullR, "~nN", true},
		{TagTS, reTSR, "0123456789", false},
		{TagValue, reValue, "=", false},
		{tagPrefix + "yaml", reYAML, "!&*", false},
	}
}

var resolvers12, resolvers11 = ruamelResolvers(false), ruamelResolvers(true)

// pyDollar emulates Python's `$` (also matching before a final newline)
// for values that cannot contain one anyway (plain scalars).
func resolveRuamel(v11 bool) func(kind NodeKind, value string, implicit [2]bool) string {
	rs := resolvers12
	if v11 {
		rs = resolvers11
	}
	return func(kind NodeKind, value string, implicit [2]bool) string {
		if kind == ScalarNode && implicit[0] {
			for _, r := range rs {
				if value == "" {
					if !r.empty {
						continue
					}
				} else if !in([]rune(value)[0], r.first) {
					continue
				}
				if r.re.MatchString(value) {
					return r.tag
				}
			}
		}
		switch kind {
		case SequenceNode:
			return TagSeq
		case MappingNode:
			return TagMap
		}
		return TagStr
	}
}
