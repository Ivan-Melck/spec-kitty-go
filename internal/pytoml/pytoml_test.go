package pytoml

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"math/big"
	"os"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

func canon(v any) any {
	switch x := v.(type) {
	case bool:
		return map[string]any{"bool": x}
	case int64:
		return map[string]any{"int": big.NewInt(x).String()}
	case *big.Int:
		return map[string]any{"int": x.String()}
	case float64:
		if math.IsNaN(x) {
			return map[string]any{"float": "nan"}
		}
		return map[string]any{"float": pyjson.ReprFloat(x)}
	case DateTime:
		return map[string]any{"dt": x.ISO()}
	case Date:
		return map[string]any{"date": x.ISO()}
	case Time:
		return map[string]any{"time": x.ISO()}
	case string:
		return x
	case *jsonx.Object:
		pairs := []any{}
		for _, kv := range x.Pairs() {
			pairs = append(pairs, []any{kv.Key, canon(kv.Value)})
		}
		return map[string]any{"map": pairs}
	case []any:
		out := []any{}
		for _, e := range x {
			out = append(out, canon(e))
		}
		return out
	}
	return map[string]any{"other": v}
}

// TestCorpus compares Loads with CPython 3.13 tomllib.loads over the
// corpus testdata/tgen.py generates (values and TOMLDecodeError text).
func TestCorpus(t *testing.T) {
	path := "testdata/corpus.json.gz"
	if env := os.Getenv("PYTOML_CORPUS"); env != "" {
		path = env
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Src string                     `json:"src"`
		Res map[string]json.RawMessage `json:"res"`
	}
	if err := json.NewDecoder(zr).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	fails := 0
	for _, r := range rows {
		if _, ok := r.Res["exc"]; ok {
			continue
		}
		got, err := Loads(r.Src)
		if raw, ok := r.Res["err"]; ok {
			var want string
			json.Unmarshal(raw, &want)
			if err == nil || err.Error() != want {
				t.Errorf("%q:\nwant %q\ngot  %v", r.Src, want, err)
				fails++
			}
		} else {
			var want any
			json.Unmarshal(r.Res["val"], &want)
			wb, _ := json.Marshal(want)
			if err != nil {
				t.Errorf("%q: unexpected error %v", r.Src, err)
				fails++
			} else if gb, _ := json.Marshal(canon(got)); string(gb) != string(wb) {
				t.Errorf("%q:\nwant %s\ngot  %s", r.Src, wb, gb)
				fails++
			}
		}
		if fails > 30 {
			t.Fatal("too many failures")
		}
	}
}
