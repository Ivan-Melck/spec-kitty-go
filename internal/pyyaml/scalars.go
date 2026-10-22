package pyyaml

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// ---------------------------------------------------------------- scalars

// asciiCheck is str.encode('ascii')'s UnicodeEncodeError text ("" = ok).
func asciiCheck(s string) string {
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] < 0x80 {
			continue
		}
		j := i
		for j+1 < len(r) && r[j+1] >= 0x80 {
			j++
		}
		if j == i {
			return fmt.Sprintf("'ascii' codec can't encode character %s in position %d: ordinal not in range(128)", pyjson.ReprString(string(r[i])), i)
		}
		return fmt.Sprintf("'ascii' codec can't encode characters in position %d-%d: ordinal not in range(128)", i, j)
	}
	return ""
}

// a2bBase64 is binascii.a2b_base64(strict_mode=False) ("" = ok).
func a2bBase64(s string) ([]byte, string) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := []byte{}
	quad, pads := 0, 0
	var left byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' {
			if quad >= 2 {
				pads++
			}
			continue
		}
		v := strings.IndexByte(alphabet, c)
		if v < 0 {
			continue
		}
		pads = 0
		b := byte(v)
		switch quad {
		case 0:
			quad, left = 1, b
		case 1:
			quad = 2
			out = append(out, left<<2|b>>4)
			left = b & 0x0f
		case 2:
			quad = 3
			out = append(out, left<<4|b>>2)
			left = b & 0x03
		case 3:
			quad = 0
			out = append(out, left<<6|b)
			left = 0
		}
	}
	switch quad {
	case 0:
		return out, ""
	case 1:
		return nil, fmt.Sprintf("Invalid base64-encoded string: number of data characters (%d) cannot be 1 more than a multiple of 4", len(out)/3*4+1)
	}
	if quad+pads >= 4 {
		return out, ""
	}
	return nil, "Incorrect padding"
}

func pyStrip(s string) string { return strings.TrimFunc(s, unicode.IsSpace) }

// pyInt is int(s, base) (s already free of underscores).
func pyInt(s string, base int) (*big.Int, bool) {
	t := pyStrip(s)
	neg := false
	if t != "" && (t[0] == '+' || t[0] == '-') {
		neg = t[0] == '-'
		t = t[1:]
	}
	lt := strings.ToLower(t)
	switch {
	case base == 16 && strings.HasPrefix(lt, "0x"), base == 8 && strings.HasPrefix(lt, "0o"), base == 2 && strings.HasPrefix(lt, "0b"):
		t = t[2:]
	}
	if t == "" {
		return nil, false
	}
	var digits strings.Builder
	for _, r := range t {
		if unicode.IsDigit(r) {
			d := r - '0'
			if r > '9' {
				d = rune(unicodeDigit(r))
			}
			if int(d) >= base {
				return nil, false
			}
			digits.WriteRune('0' + d)
			continue
		}
		if base > 10 {
			lr := unicode.ToLower(r)
			if lr >= 'a' && int(lr-'a')+10 < base {
				digits.WriteRune(lr)
				continue
			}
		}
		return nil, false
	}
	n, ok := new(big.Int).SetString(digits.String(), base)
	if !ok {
		return nil, false
	}
	if neg {
		n.Neg(n)
	}
	return n, true
}

func unicodeDigit(r rune) int {
	// Unicode decimal digits come in contiguous runs of ten starting at 0.
	for z := r; z >= r-9; z-- {
		if !unicode.IsDigit(z) {
			return int(r - z - 1)
		}
	}
	return int(r - (r - 9))
}

func intResult(n *big.Int) any {
	if n.IsInt64() {
		return n.Int64()
	}
	return n
}

// safeInt is SafeConstructor.construct_yaml_int, with int()'s ValueError.
func safeInt(value string) any {
	v := strings.ReplaceAll(value, "_", "")
	if v == "" {
		panic(&PyError{"IndexError", "string index out of range"})
	}
	sign := big.NewInt(1)
	if v[0] == '-' {
		sign = big.NewInt(-1)
	}
	if v[0] == '+' || v[0] == '-' {
		v = v[1:]
	}
	conv := func(s string, base int) *big.Int {
		n, ok := pyInt(s, base)
		if !ok {
			panic(valueError("invalid literal for int() with base %d: %s", base, pyjson.ReprString(s)))
		}
		return n
	}
	var n *big.Int
	switch {
	case v == "0":
		return int64(0)
	case strings.HasPrefix(v, "0b"):
		n = conv(v[2:], 2)
	case strings.HasPrefix(v, "0x"):
		n = conv(v[2:], 16)
	case v != "" && v[0] == '0':
		n = conv(v, 8)
	case strings.Contains(v, ":"):
		parts := strings.Split(v, ":")
		for _, p := range parts { // [int(part) for part in ...] runs in order
			conv(p, 10)
		}
		total := new(big.Int)
		base := big.NewInt(1)
		for i := len(parts) - 1; i >= 0; i-- {
			d := conv(parts[i], 10)
			total.Add(total, new(big.Int).Mul(d, base))
			base.Mul(base, big.NewInt(60))
		}
		n = total
	default:
		if v == "" {
			panic(valueError("invalid literal for int() with base 10: ''"))
		}
		n = conv(v, 10)
	}
	return intResult(n.Mul(n, sign))
}

// pyFloat is float(s).
func pyFloat(s string) (float64, bool) {
	t := pyStrip(s)
	switch strings.ToLower(strings.TrimLeft(t, "+-")) {
	case "inf", "infinity", "nan":
		if strings.Count(t[:len(t)-len(strings.TrimLeft(t, "+-"))], "") > 2 {
			return 0, false
		}
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	if t == "" || strings.ContainsAny(t, "xXpP_") {
		return 0, false
	}
	for _, r := range t {
		if !(('0' <= r && r <= '9') || r == '.' || r == 'e' || r == 'E' || r == '+' || r == '-') {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f, true
		}
		return 0, false
	}
	return f, true
}

// safeFloat is SafeConstructor.construct_yaml_float.
func safeFloat(value string) any {
	v := strings.ToLower(strings.ReplaceAll(value, "_", ""))
	if v == "" {
		panic(&PyError{"IndexError", "string index out of range"})
	}
	sign := 1.0
	if v[0] == '-' {
		sign = -1
	}
	if v[0] == '+' || v[0] == '-' {
		v = v[1:]
	}
	conv := func(s string) float64 {
		f, ok := pyFloat(s)
		if !ok {
			panic(valueError("could not convert string to float: %s", pyjson.ReprString(s)))
		}
		return f
	}
	switch {
	case v == ".inf":
		return sign * math.Inf(1)
	case v == ".nan":
		return math.NaN()
	case strings.Contains(v, ":"):
		parts := strings.Split(v, ":")
		for _, p := range parts {
			conv(p)
		}
		val, base := 0.0, 1.0
		for i := len(parts) - 1; i >= 0; i-- {
			val += conv(parts[i]) * base
			base *= 60
		}
		return sign * val
	}
	return sign * conv(v)
}

// checkTimestamp reports datetime's ValueError for an out-of-range
// implicit timestamp.
func checkTimestamp(v string) error {
	m := reTSParts.FindStringSubmatch(v)
	if m == nil {
		return nil
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
	if y < 1 {
		return valueError("year %d is out of range", y)
	}
	if mo < 1 || mo > 12 {
		return valueError("month must be in 1..12")
	}
	dim := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[mo-1]
	if mo == 2 && (y%4 == 0 && (y%100 != 0 || y%400 == 0)) {
		dim = 29
	}
	if d < 1 || d > dim {
		return valueError("day is out of range for month")
	}
	if m[4] != "" {
		if atoi(m[4]) > 23 {
			return valueError("hour must be in 0..23")
		}
		if atoi(m[5]) > 59 {
			return valueError("minute must be in 0..59")
		}
		if atoi(m[6]) > 59 {
			return valueError("second must be in 0..59")
		}
		if m[10] != "" {
			th, tm := atoi(m[10]), atoi(m[11])
			if th*60+tm >= 24*60 {
				return valueError("offset must be a timedelta strictly between -timedelta(hours=24) and timedelta(hours=24), not %s.", pyTimedeltaRepr(m[9] == "-", th, tm))
			}
		}
	}
	return nil
}

func pyTimedeltaRepr(neg bool, h, m int) string {
	secs := h*3600 + m*60
	if neg {
		secs = -secs
	}
	days := 0
	if secs < 0 {
		days = -1 + (secs+1)/86400
		secs -= days * 86400
	} else {
		days = secs / 86400
		secs %= 86400
	}
	var parts []string
	if days != 0 {
		parts = append(parts, fmt.Sprintf("days=%d", days))
	}
	if secs != 0 {
		parts = append(parts, fmt.Sprintf("seconds=%d", secs))
	}
	if len(parts) == 0 {
		return "datetime.timedelta(0)"
	}
	return "datetime.timedelta(" + strings.Join(parts, ", ") + ")"
}
