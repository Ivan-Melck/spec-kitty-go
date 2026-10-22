package dossier

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// SnapshotPath is <feature dir>/.kittify/dossiers/<slug>/snapshot-latest.json.
func SnapshotPath(featureDir, slug string) string {
	return filepath.Join(featureDir, ".kittify", "dossiers", slug, "snapshot-latest.json")
}

// LoadSnapshot is dossier.snapshot.load_snapshot: nil when no snapshot was
// recorded; otherwise the decoded object validated like the
// MissionDossierSnapshot pydantic model. Errors carry upstream's exception
// text (json decode, UnicodeDecodeError, ** mapping TypeError, pydantic
// ValidationError).
func LoadSnapshot(featureDir, slug string) (*jsonx.Object, error) {
	if reason := mission.UnsafeSegmentReason(slug); reason != "" {
		return nil, fmt.Errorf("%s", reason)
	}
	b, err := os.ReadFile(SnapshotPath(featureDir, slug))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	v, err := pyjson.LoadsBytes(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("specify_cli.dossier.models.MissionDossierSnapshot() argument after ** must be a mapping, not %s", pyjson.TypeName(v))
	}
	if errs := validateSnapshot(o); len(errs) > 0 {
		return nil, fmt.Errorf("%s", pydanticText("MissionDossierSnapshot", errs))
	}
	return o, nil
}

type pydErr struct {
	loc, typ, msg string
	input         any
}

func truncRepr(v any) string {
	r := pyjson.Repr(v)
	if rs := []rune(r); len(rs) > 50 {
		return string(rs[:25]) + "..." + string(rs[len(rs)-24:])
	}
	return r
}

func pydanticText(model string, errs []pydErr) string {
	s := "s"
	if len(errs) == 1 {
		s = ""
	}
	lines := []string{fmt.Sprintf("%d validation error%s for %s", len(errs), s, model)}
	for _, e := range errs {
		lines = append(lines, e.loc, fmt.Sprintf("  %s [type=%s, input_value=%s, input_type=%s]", e.msg, e.typ, truncRepr(e.input), pyjson.TypeName(e.input)),
			fmt.Sprintf("    For further information visit https://errors.pydantic.dev/2.14/v/%s", e.typ))
	}
	return strings.Join(lines, "\n")
}

// laxInt is pydantic's lax int validation; ok=false appends the error.
func laxInt(v any, loc string, errs *[]pydErr) (int64, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case int:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		if x != math.Trunc(x) || math.IsInf(x, 0) || math.IsNaN(x) {
			*errs = append(*errs, pydErr{loc, "int_from_float", "Input should be a valid integer, got a number with a fractional part", v})
			return 0, false
		}
		return int64(x), true
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return n, true
		}
		*errs = append(*errs, pydErr{loc, "int_parsing", "Input should be a valid integer, unable to parse string as an integer", v})
		return 0, false
	}
	if n, ok := v.(interface{ Int64() (int64, error) }); ok {
		if i, err := n.Int64(); err == nil {
			return i, true
		}
	}
	*errs = append(*errs, pydErr{loc, "int_type", "Input should be a valid integer", v})
	return 0, false
}

// speedateError approximates pydantic-core's datetime parse errors.
func speedateError(s string) string {
	if len(s) < 10 {
		return "input is too short"
	}
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return "invalid character in year"
		}
	}
	if s[4] != '-' {
		return "invalid date separator, expected `-`"
	}
	return "invalid datetime separator, expected `T`, `t`, `_` or space"
}

func validDatetime(v any, loc string, errs *[]pydErr) {
	switch x := v.(type) {
	case string:
		if _, err := time.Parse(time.RFC3339Nano, strings.Replace(x, " ", "T", 1)); err == nil {
			return
		}
		if _, err := time.Parse("2006-01-02T15:04:05.999999", x); err == nil {
			return
		}
		if _, err := time.Parse("2006-01-02", x); err == nil {
			return
		}
		*errs = append(*errs, pydErr{loc, "datetime_from_date_parsing", "Input should be a valid datetime or date, " + speedateError(x), v})
	case int, int64, float64:
		return
	case bool:
		*errs = append(*errs, pydErr{loc, "datetime_type", "Input should be a valid datetime", v})
	default:
		if _, ok := v.(interface{ Int64() (int64, error) }); ok {
			return
		}
		*errs = append(*errs, pydErr{loc, "datetime_type", "Input should be a valid datetime", v})
	}
}

// validateSnapshot checks the MissionDossierSnapshot fields in declaration
// order (lax mode; extra keys ignored).
func validateSnapshot(o *jsonx.Object) []pydErr {
	var errs []pydErr
	str := func(k string, required bool) (string, bool) {
		v, ok := o.Get(k)
		if !ok {
			if required {
				errs = append(errs, pydErr{k, "missing", "Field required", o})
			}
			return "", false
		}
		s, isStr := v.(string)
		if !isStr {
			errs = append(errs, pydErr{k, "string_type", "Input should be a valid string", v})
			return "", false
		}
		return s, true
	}
	str("mission_slug", true)
	str("snapshot_id", false)
	for _, k := range []string{"total_artifacts", "required_artifacts", "required_present", "required_missing", "optional_artifacts", "optional_present"} {
		v, ok := o.Get(k)
		if !ok {
			continue
		}
		if n, ok := laxInt(v, k, &errs); ok && n < 0 {
			errs = append(errs, pydErr{k, "greater_than_equal", "Input should be greater than or equal to 0", v})
		}
	}
	if s, ok := str("completeness_status", false); ok {
		if s != "complete" && s != "incomplete" && s != "unknown" {
			errs = append(errs, pydErr{"completeness_status", "value_error",
				fmt.Sprintf("Value error, completeness_status must be one of {'incomplete', 'unknown', 'complete'}; got '%s'", s), s})
		}
	}
	if s, ok := str("parity_hash_sha256", true); ok && s != "" {
		digest := strings.TrimPrefix(s, "sha256:")
		if len([]rune(digest)) != 64 {
			errs = append(errs, pydErr{"parity_hash_sha256", "value_error",
				fmt.Sprintf("Value error, parity_hash_sha256 must be a 64-char SHA256 digest; got %d characters", len([]rune(digest))), s})
		} else if !isHex(digest) {
			errs = append(errs, pydErr{"parity_hash_sha256", "value_error",
				fmt.Sprintf("Value error, parity_hash_sha256 must be valid hexadecimal; got '%s'", s), s})
		}
	}
	if v, ok := o.Get("parity_hash_components"); ok {
		if l, isList := v.([]any); !isList {
			errs = append(errs, pydErr{"parity_hash_components", "list_type", "Input should be a valid list", v})
		} else {
			for i, x := range l {
				if _, isStr := x.(string); !isStr {
					errs = append(errs, pydErr{fmt.Sprintf("parity_hash_components.%d", i), "string_type", "Input should be a valid string", x})
				}
			}
		}
	}
	if v, ok := o.Get("artifact_summaries"); ok {
		if l, isList := v.([]any); !isList {
			errs = append(errs, pydErr{"artifact_summaries", "list_type", "Input should be a valid list", v})
		} else {
			for i, x := range l {
				if _, isObj := x.(*jsonx.Object); !isObj {
					errs = append(errs, pydErr{fmt.Sprintf("artifact_summaries.%d", i), "dict_type", "Input should be a valid dictionary", x})
				}
			}
		}
	}
	if v, ok := o.Get("computed_at"); ok {
		validDatetime(v, "computed_at", &errs)
	}
	return errs
}

// isHex is int(digest, 16) succeeding (underscores between digits allowed,
// an optional sign / 0x prefix).
func isHex(s string) bool {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(strings.TrimPrefix(t, "+"), "-")
	t = strings.TrimPrefix(strings.TrimPrefix(t, "0x"), "0X")
	if t == "" || strings.HasPrefix(t, "_") || strings.HasSuffix(t, "_") || strings.Contains(t, "__") {
		return false
	}
	for _, c := range t {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '_') {
			return false
		}
	}
	return true
}
