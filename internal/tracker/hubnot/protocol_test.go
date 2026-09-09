package hubnot

import (
	"strings"
	"testing"
)

func TestProtocolStrictBoundary(t *testing.T) {
	good := `{"schema":"hn.issue/1","ok":true,"data":{"items":[],"total":0,"conflict":false,"cycle_count":0},"snapshot":"sha256:` + strings.Repeat("a", 64) + `"}`
	for _, bad := range []string{good + `{}`, strings.Replace(good, `"ok":true`, `"ok":true,"error":null`, 1), strings.Replace(good, `"ok":true`, `"ok":true,"ok":true`, 1), strings.Replace(good, `"total":0`, `"total":0,"total":0`, 1), strings.Replace(good, `"total":0`, `"value":"\ud800","total":0`, 1), strings.Replace(good, `"total":0`, `"value":"`+string([]byte{0xff})+`","total":0`, 1), strings.Replace(good, `"ok":true`, `"ok":false`, 1), strings.Replace(good, `"hn.issue/1"`, `"hn.issue/2"`, 1), strings.Replace(good, `"ok":true`, `"ok":true,"error":{"code":"x","message":"x"}`, 1)} {
		if _, err := decodeEnvelope([]byte(bad), issueSchema, 0); err == nil {
			t.Fatal("accepted malformed response")
		}
	}
	if _, err := decodeEnvelope([]byte(good), issueSchema, 0); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{`{"a":"\ud83d\ude00"}`, `{"a":"�"}`, `{"a":123456789012345678901234567890}`} {
		var v any
		if err := decodeStrict([]byte(text), &v); err != nil {
			t.Fatal(err)
		}
	}
}
func TestObservationBudgetDoesNotReset(t *testing.T) {
	b := newObservationBudget()
	defer b.close()
	if err := b.charge(maxObservationBytes-1, 9999); err != nil {
		t.Fatal(err)
	}
	if err := b.restart(); err != nil {
		t.Fatal(err)
	}
	if err := b.charge(2, 2); err == nil {
		t.Fatal("restart reset budget")
	}
	c := newObservationBudget()
	defer c.close()
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.restart(); err != nil {
			t.Fatal(err)
		}
	}
	if c.restart() == nil {
		t.Fatal("unbounded restarts")
	}
}
