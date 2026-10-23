package testkit

import "testing"

func TestNormalizeShortSHAs(t *testing.T) {
	cases := map[string]string{
		"committed 7408322 to main":                       "committed <SHORTSHA> to main",
		"commit a1b2c3d done":                             "commit <SHORTSHA> done",
		"[main 1234567] msg":                              "[main <SHORTSHA>] msg",
		"defaced cafebabe":                                "defaced <SHORTSHA>",
		"restored main bbbbbbb -> ccccccc (was fadedcab)": "restored main <SHORTSHA> -> <SHORTSHA> (was <SHORTSHA>)",
		"decade facade beaded":                            "decade facade beaded",
		"version 1.2345678":                               "version 1.2345678",
		"took 1234567.5 units":                            "took 1234567.5 units",
		"short 123456 stays":                              "short 123456 stays",
		`{"pid": 1234567}`:                                `{"pid": <PID>}`,
		"index 1234567..89abcde0 x":                       "index <SHORTSHA>..<SHORTSHA> x",
		"count 12345678901234 stays":                      "count 12345678901234 stays",
	}
	for in, want := range cases {
		if got := NormalizeValues(in); got != want {
			t.Errorf("NormalizeValues(%q) = %q, want %q", in, got, want)
		}
	}
}
