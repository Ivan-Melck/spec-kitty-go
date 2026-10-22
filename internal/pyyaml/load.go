// Package pyyaml reproduces PyYAML's yaml.safe_load (YAML 1.1 implicit
// typing of plain scalars) and yaml.dump (the pure-Python Emitter: block
// style, indent 2, width 80) for the value space used by frontmatter files.
//
// Values: nil, bool, int64, *big.Int, float64, string, Date, Timestamp,
// *jsonx.Object (mappings with string keys, insertion ordered) and []any.
package pyyaml

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Date is a datetime.date loaded from an implicit timestamp scalar.
type Date struct{ Y, M, D int }

// ISO is date.isoformat().
func (d Date) ISO() string { return fmt.Sprintf("%04d-%02d-%02d", d.Y, d.M, d.D) }

// Timestamp is a datetime.datetime; Repr is its isoformat(' ').
type Timestamp struct{ Repr string }

var (
	reBool    = regexp.MustCompile(`^(?:yes|Yes|YES|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`)
	reNull    = regexp.MustCompile(`^(?:~|null|Null|NULL|)$`)
	reInt     = regexp.MustCompile(`^(?:[-+]?0b[0-1_]+|[-+]?0[0-7_]+|[-+]?(?:0|[1-9][0-9_]*)|[-+]?0x[0-9a-fA-F_]+|[-+]?[1-9][0-9_]*(?::[0-5]?[0-9])+)$`)
	reFloat   = regexp.MustCompile(`^(?:[-+]?(?:[0-9][0-9_]*)\.[0-9_]*(?:[eE][-+][0-9]+)?|\.[0-9][0-9_]*(?:[eE][-+][0-9]+)?|[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+\.[0-9_]*|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
	reTS      = regexp.MustCompile(`^(?:[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]|[0-9][0-9][0-9][0-9]-[0-9][0-9]?-[0-9][0-9]?(?:[Tt]|[ \t]+)[0-9][0-9]?:[0-9][0-9]:[0-9][0-9](?:\.[0-9]*)?(?:[ \t]*(?:Z|[-+][0-9][0-9]?(?::[0-9][0-9])?))?)$`)
	reTSParts = regexp.MustCompile(`^([0-9][0-9][0-9][0-9])-([0-9][0-9]?)-([0-9][0-9]?)(?:(?:[Tt]|[ \t]+)([0-9][0-9]?):([0-9][0-9]):([0-9][0-9])(?:\.([0-9]*))?(?:[ \t]*(Z|([-+])([0-9][0-9]?)(?::([0-9][0-9]))?))?)?$`)
)

// ResolvePlain returns the implicit tag PyYAML's resolver gives a plain
// scalar: "null", "bool", "int", "float", "timestamp" or "str".
func ResolvePlain(v string) string {
	switch {
	case reNull.MatchString(v):
		return "null"
	case reBool.MatchString(v):
		return "bool"
	case reInt.MatchString(v):
		return "int"
	case reFloat.MatchString(v):
		return "float"
	case reTS.MatchString(v):
		return "timestamp"
	case v == "<<":
		return "merge"
	case v == "=":
		return "value"
	}
	return "str"
}

func constructInt(v string) any {
	s := strings.ReplaceAll(v, "_", "")
	sign := int64(1)
	if strings.HasPrefix(s, "-") {
		sign = -1
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	var n big.Int
	switch {
	case s == "0":
		return int64(0)
	case strings.HasPrefix(s, "0b"):
		n.SetString(s[2:], 2)
	case strings.HasPrefix(s, "0x"):
		n.SetString(s[2:], 16)
	case strings.HasPrefix(s, "0"):
		n.SetString(s[1:], 8)
	case strings.Contains(s, ":"):
		base := big.NewInt(1)
		parts := strings.Split(s, ":")
		for i := len(parts) - 1; i >= 0; i-- {
			p, _ := new(big.Int).SetString(parts[i], 10)
			n.Add(&n, new(big.Int).Mul(p, base))
			base.Mul(base, big.NewInt(60))
		}
	default:
		n.SetString(s, 10)
	}
	if sign < 0 {
		n.Neg(&n)
	}
	if n.IsInt64() {
		return n.Int64()
	}
	return &n
}

func constructFloat(v string) any {
	s := strings.ToLower(strings.ReplaceAll(v, "_", ""))
	sign := 1.0
	if strings.HasPrefix(s, "-") {
		sign = -1
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	switch {
	case s == ".inf":
		return sign * math.Inf(1)
	case s == ".nan":
		return math.NaN()
	case strings.Contains(s, ":"):
		parts := strings.Split(s, ":")
		val, base := 0.0, 1.0
		for i := len(parts) - 1; i >= 0; i-- {
			f, _ := strconv.ParseFloat(parts[i], 64)
			val += f * base
			base *= 60
		}
		return sign * val
	}
	f, _ := strconv.ParseFloat(s, 64)
	return sign * f
}

func constructTimestamp(v string) any {
	m := reTSParts.FindStringSubmatch(v)
	if m == nil {
		return v
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	if m[4] == "" {
		return Date{y, mo, d}
	}
	h, _ := strconv.Atoi(m[4])
	mi, _ := strconv.Atoi(m[5])
	se, _ := strconv.Atoi(m[6])
	out := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", y, mo, d, h, mi, se)
	if frac := m[7]; frac != "" {
		if len(frac) > 6 {
			frac = frac[:6]
		}
		frac += strings.Repeat("0", 6-len(frac))
		if strings.Trim(frac, "0") != "" {
			out += "." + frac
		}
	}
	if m[8] != "" {
		if m[8] == "Z" {
			out += "+00:00"
		} else {
			th, _ := strconv.Atoi(m[10])
			tm := 0
			if m[11] != "" {
				tm, _ = strconv.Atoi(m[11])
			}
			sign := m[9]
			if th == 0 && tm == 0 {
				sign = "+"
			}
			out += fmt.Sprintf("%s%02d:%02d", sign, th, tm)
		}
	}
	return Timestamp{Repr: out}
}

func plainValue(v string) any {
	switch ResolvePlain(v) {
	case "null":
		return nil
	case "bool":
		switch v[0] {
		case 'y', 'Y', 't', 'T':
			return true
		case 'o', 'O':
			return v == "on" || v == "On" || v == "ON"
		}
		return false
	case "int":
		return constructInt(v)
	case "float":
		return constructFloat(v)
	case "timestamp":
		return constructTimestamp(v)
	}
	return v
}

func keyString(k any) string {
	switch t := k.(type) {
	case string:
		return t
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case Date:
		return t.ISO()
	}
	return fmt.Sprint(k)
}

// SafeLoad parses text like yaml.safe_load (an alias of Load).
func SafeLoad(text string) (any, error) { return Load(text) }
