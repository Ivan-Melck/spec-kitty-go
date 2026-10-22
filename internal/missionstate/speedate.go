package missionstate

import "strconv"

// pydantic-core's lax datetime validation of a str (speedate): a string is
// accepted when it parses as an RFC 3339-ish datetime, a unix timestamp or a
// bare date; otherwise the error text is the date parser's
// ("Input should be a valid datetime or date, <reason>").

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// speedateDate is Date::parse_bytes_partial: (offset, error).
func speedateDate(b string) (int, string) {
	if len(b) < 10 {
		return 0, "input is too short"
	}
	for i := 0; i < 4; i++ {
		if !isDigit(b[i]) {
			return 0, "invalid character in year"
		}
	}
	year, _ := strconv.Atoi(b[:4])
	if b[4] != '-' {
		return 0, "invalid date separator, expected `-`"
	}
	if !isDigit(b[5]) || !isDigit(b[6]) {
		return 0, "invalid character in month"
	}
	month, _ := strconv.Atoi(b[5:7])
	if b[7] != '-' {
		return 0, "invalid date separator, expected `-`"
	}
	if !isDigit(b[8]) || !isDigit(b[9]) {
		return 0, "invalid character in day"
	}
	day, _ := strconv.Atoi(b[8:10])
	var maxDays int
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		maxDays = 31
	case 4, 6, 9, 11:
		maxDays = 30
	case 2:
		maxDays = 28
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			maxDays = 29
		}
	default:
		return 0, "month value is outside expected range of 1-12"
	}
	if day < 1 || day > maxDays {
		return 0, "day value is outside expected range"
	}
	return 10, ""
}

// speedateDateError is Date::parse_bytes_rfc3339's error ("" when valid).
func speedateDateError(s string) string {
	off, err := speedateDate(s)
	if err != "" {
		return err
	}
	if off != len(s) {
		return "unexpected extra characters at the end of the input"
	}
	return ""
}

// speedateTime is Time::parse_bytes_partial (ok=false on any error).
func speedateTime(b string) (int, bool) {
	if len(b) < 5 || !isDigit(b[0]) || !isDigit(b[1]) || b[2] != ':' || !isDigit(b[3]) || !isDigit(b[4]) {
		return 0, false
	}
	hour, _ := strconv.Atoi(b[:2])
	minute, _ := strconv.Atoi(b[3:5])
	n := 5
	second := 0
	if n < len(b) && b[n] == ':' {
		if len(b) < 8 || !isDigit(b[6]) || !isDigit(b[7]) {
			return 0, false
		}
		second, _ = strconv.Atoi(b[6:8])
		n = 8
		if n < len(b) && (b[n] == '.' || b[n] == ',') {
			n++
			i := 0
			for n+i < len(b) && isDigit(b[n+i]) {
				i++
			}
			if i == 0 {
				return 0, false
			}
			n += i
		}
	}
	if hour > 23 || minute > 59 || second > 59 {
		return 0, false
	}
	return n, true
}

// speedateTZ parses an optional offset at b[0:] (Z | ±HH[[:]MM]).
func speedateTZ(b string) (int, bool) {
	if b == "" {
		return 0, true
	}
	switch b[0] {
	case 'Z', 'z':
		return 1, true
	case '+', '-':
		if len(b) < 3 || !isDigit(b[1]) || !isDigit(b[2]) {
			return 0, false
		}
		h, _ := strconv.Atoi(b[1:3])
		n := 3
		if n < len(b) && b[n] == ':' {
			n++
		}
		if len(b) < n+2 || !isDigit(b[n]) || !isDigit(b[n+1]) {
			return 0, false
		}
		m, _ := strconv.Atoi(b[n : n+2])
		if m > 59 {
			return 0, false
		}
		n += 2
		if h > 23 {
			return 0, false
		}
		return n, true
	}
	return 0, true
}

// speedateNumber reports a unix-timestamp literal (int or float).
func speedateNumber(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i = 1
	}
	digits, dot := 0, false
	for ; i < len(s); i++ {
		switch {
		case isDigit(s[i]):
			digits++
		case s[i] == '.' && !dot:
			dot = true
		default:
			return false
		}
	}
	return digits > 0
}

// speedateDateTimeOK is DateTime::parse_bytes_with_config succeeding.
func speedateDateTimeOK(s string) bool {
	if off, err := speedateDate(s); err == "" {
		if off < len(s) && (s[off] == 'T' || s[off] == 't' || s[off] == ' ' || s[off] == '_') {
			if n, ok := speedateTime(s[off+1:]); ok {
				rest := s[off+1+n:]
				if m, ok := speedateTZ(rest); ok && m == len(rest) {
					return true
				}
			}
		}
	}
	return speedateNumber(s)
}

// pydanticDatetimeError is the lax datetime validator's message for a str
// input ("" when accepted).
func pydanticDatetimeError(s string) string {
	if speedateDateTimeOK(s) {
		return ""
	}
	if err := speedateDateError(s); err != "" {
		return "Input should be a valid datetime or date, " + err
	}
	return ""
}
