package rich

import (
	"strings"
	"testing"
)

// Expected output captured from rich 15 (Traceback over a Trace whose frame
// files do not exist, width=None, word_wrap=True) at console width 50.
func TestRenderTracebackChain(t *testing.T) {
	frames := []Frame{{"merge_driver_meta", 111}, {"_union_acceptance_history", 341}}
	exc := &Exception{
		Type:   "TypeError",
		Value:  "a very long message that will need to wrap across the console width for sure yes",
		Frames: frames,
		Cause:  &Exception{Type: "KeyError", Value: "'x'", Frames: frames[:1]},
	}
	want := `╭────── Traceback (most recent call last) ───────╮
│ in merge_driver_meta:111                       │
╰────────────────────────────────────────────────╯
KeyError: 'x'

The above exception was the direct cause of the` + " " + `
following exception:

╭────── Traceback (most recent call last) ───────╮
│ in merge_driver_meta:111                       │
│                                                │
│ in _union_acceptance_history:341               │
╰────────────────────────────────────────────────╯
TypeError: a very long message that will need to` + " " + `
wrap across the console width for sure yes`
	if got := strings.Join(RenderTraceback(exc, 50), "\n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderTracebackNoFrames(t *testing.T) {
	got := RenderTraceback(&Exception{Type: "sqlite3.IntegrityError", Value: "UNIQUE"}, 80)
	if len(got) != 1 || got[0] != "IntegrityError: UNIQUE" {
		t.Errorf("got %q", got)
	}
	if e := ParseException("json.decoder.JSONDecodeError: Expecting value"); e.Error() != "JSONDecodeError: Expecting value" {
		t.Errorf("got %q", e.Error())
	}
}
