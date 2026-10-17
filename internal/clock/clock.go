// Package clock renders timestamps the way Python's datetime.isoformat does.
package clock

import (
	"fmt"
	"os"
	"time"
)

// Now returns the current time (overridable for tests via SPEC_KITTY_FAKE_NOW,
// an RFC3339 timestamp).
func Now() time.Time {
	if v := os.Getenv("SPEC_KITTY_FAKE_NOW"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
	}
	return time.Now()
}

// ISO renders t like Python's datetime.isoformat(): microsecond precision,
// fraction omitted when zero, "+00:00" for UTC-aware values.
func ISO(t time.Time) string {
	base := t.Format("2006-01-02T15:04:05")
	us := t.Nanosecond() / 1000
	if us != 0 {
		base += fmt.Sprintf(".%06d", us)
	}
	_, off := t.Zone()
	sign := '+'
	if off < 0 {
		sign = '-'
		off = -off
	}
	return base + fmt.Sprintf("%c%02d:%02d", sign, off/3600, (off%3600)/60)
}

// NaiveISO renders t like a naive datetime.isoformat() (no offset).
func NaiveISO(t time.Time) string {
	base := t.Format("2006-01-02T15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		base += fmt.Sprintf(".%06d", us)
	}
	return base
}

// NowUTCISO is datetime.now(UTC).isoformat().
func NowUTCISO() string { return ISO(Now().UTC()) }

// ParseISO parses Python isoformat strings (with or without offset/fraction, or "Z").
func ParseISO(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid isoformat string: %q", s)
}
