package glossary

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestLedgerValidationMatchesOracle(t *testing.T) {
	f, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	python := strings.TrimSpace(strings.TrimPrefix(first, "#!"))
	parse := func(raw string) *jsonx.Object {
		o, err := jsonx.DecodeObject([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	actor := `"actor":{"actor_id":"user:cli","actor_type":"human","display_name":"CLI User"},"provenance":{"source":"cli_resolve","timestamp":"2026-10-24T01:00:00+00:00","actor_id":"user:cli"}`
	bases := []*jsonx.Object{
		parse(`{"event_type":"TermCandidateObserved","mission_id":"m","run_id":"r","term":"workspace","source_step":"plan","actor_id":"a","confidence":1.0,"extraction_method":"request_text","context":"text"}`),
		parse(`{"event_type":"GlossaryClarificationResolved","conflict_id":"c","term_surface":"workspace","selected_sense":{"surface":"workspace","scope":"team_domain","definition":"Checkout","confidence":0.9},"resolution_mode":"async",` + actor + `}`),
		parse(`{"event_type":"GlossarySenseUpdated","term_surface":"workspace","scope":"team_domain","new_sense":{"surface":"workspace","scope":"team_domain","definition":"Checkout","confidence":1.0,"status":"active"},"update_type":"create",` + actor + `}`),
	}
	clone := func(o *jsonx.Object) *jsonx.Object { return parse(jsonx.Dumps(o, jsonx.Line)) }
	var cases []any
	for _, base := range bases {
		cases = append(cases, clone(base))
		var paths [][]string
		for _, pair := range base.Pairs() {
			paths = append(paths, []string{pair.Key})
			if object, ok := pair.Value.(*jsonx.Object); ok {
				for _, key := range object.Keys() {
					paths = append(paths, []string{pair.Key, key})
				}
			}
		}
		for _, path := range paths {
			for _, bad := range []any{nil, "", 0, false, []any{}, &jsonx.Object{}, "bogus", 2.0} {
				record := clone(base)
				object := record
				for _, key := range path[:len(path)-1] {
					object = object.GetObject(key)
				}
				object.Set(path[len(path)-1], bad)
				cases = append(cases, record)
			}
			record := clone(base)
			object := record
			for _, key := range path[:len(path)-1] {
				object = object.GetObject(key)
			}
			object.Delete(path[len(path)-1])
			cases = append(cases, record)
		}
		record := clone(base)
		record.Set("extra", true)
		cases = append(cases, record)
		record = clone(base)
		record.Set("timestamp", 17)
		cases = append(cases, record)
		record = clone(base)
		record.Set("confidence", math.NaN())
		cases = append(cases, record)
	}
	s := testkit.NewSandbox(t)
	s.WriteFile("cases.json", jsonx.Dumps(cases, jsonx.Line))
	oracle := s.Exec("", python, "-c", `import json
from pathlib import Path
from spec_kitty_events.persistence import build_record
out=[]
for event in json.loads(Path("cases.json").read_text()):
 try:
  build_record(event)
  out.append("")
 except Exception as exc:
  out.append(str(exc))
print(json.dumps(out))`)
	if oracle.Code != 0 {
		t.Fatalf("oracle: %s%s", oracle.Stdout, oracle.Stderr)
	}
	raw, err := jsonx.Decode([]byte(oracle.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	expected := raw.([]any)
	if len(expected) != len(cases) {
		t.Fatalf("oracle returned %d of %d cases", len(expected), len(cases))
	}
	for i, record := range cases {
		actual := ""
		if err := ValidateEvent(record.(*jsonx.Object)); err != nil {
			actual = err.Error()
		}
		if actual != expected[i] {
			t.Errorf("case %d %s\nwant %s\ngot  %s", i, jsonx.Dumps(record, jsonx.Line), expected[i], actual)
		}
	}
	fmt.Fprintf(os.Stdout, "Compared %d glossary ledger validation cases\n", len(cases))
}
