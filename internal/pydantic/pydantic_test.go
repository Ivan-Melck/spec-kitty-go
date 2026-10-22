package pydantic

import (
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// TestFormatMatchesPydantic compares against testdata/pyd1.golden, captured
// from pydantic 2.14 (see the model in the golden's generator, work-DOC/pyd1.py).
func TestFormatMatchesPydantic(t *testing.T) {
	golden, err := os.ReadFile("testdata/pyd1.golden")
	if err != nil {
		t.Fatal(err)
	}
	e := New("M")
	e.Errors = append(e.Errors,
		ErrorDetail{Loc: []any{"name"}, Type: TypeStringTooShort, Msg: StringTooShortMsg(2), Input: "x", Ctx: jsonx.NewObject("min_length", 2)},
		ErrorDetail{Loc: []any{"kind"}, Type: TypeLiteralError, Msg: LiteralMsg([]string{"a", "b", "c"}), Input: "z", Ctx: LiteralCtx([]string{"a", "b", "c"})},
		Detail("items.0.n", TypeIntParsing, MsgIntParsing, "q"),
		ErrorDetail{Loc: []any{"items", 1}, Type: TypeModelType, Msg: ModelTypeMsg("Inner"), Input: 5, Ctx: jsonx.NewObject("class_name", "Inner")},
		Detail("when", TypeStringType, MsgStringType, pyyaml.Date{Y: 2024, M: 1, D: 2}),
		Detail("extra", TypeExtraForbidden, MsgExtraForbidden, jsonx.NewObject("k", strings.Repeat("a", 60))),
	)
	got := e.Error() + "\n---\n" + e.ErrorsRepr() + "\n"
	if got != string(golden) {
		t.Fatalf("mismatch:\n--- got\n%s\n--- want\n%s", got, golden)
	}
}

func TestReprDatetime(t *testing.T) {
	cases := map[string]string{
		"2024-01-02 03:04:05":       "datetime.datetime(2024, 1, 2, 3, 4, 5)",
		"2024-01-02T03:04:00+00:00": "datetime.datetime(2024, 1, 2, 3, 4, tzinfo=datetime.timezone.utc)",
		"2024-01-02T03:04:00Z":      "datetime.datetime(2024, 1, 2, 3, 4, tzinfo=datetime.timezone.utc)",
		"2024-01-02 03:04:05-01:00": "datetime.datetime(2024, 1, 2, 3, 4, 5, tzinfo=datetime.timezone(datetime.timedelta(days=-1, seconds=82800)))",
	}
	for in, want := range cases {
		if got := ReprDatetime(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}
