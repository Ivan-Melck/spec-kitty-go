package mission

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// MetaFile is the per-mission metadata filename.
const MetaFile = "meta.json"

// MetaOptions is json.dumps(meta, indent=2, ensure_ascii=False, sort_keys=True).
var MetaOptions = jsonx.Options{Indent: 2, SortKeys: true}

// RequiredMetaKeys are validated (present and truthy) on write.
var RequiredMetaKeys = []string{"slug", "mission_slug", "friendly_name", "mission_type", "target_branch", "created_at"}

// ErrNoMeta is returned by ReadMeta when meta.json does not exist.
var ErrNoMeta = errors.New("meta.json not found")

// ReadMeta loads <dir>/meta.json preserving key order. A UTF-8 BOM is
// tolerated. Missing file → ErrNoMeta; malformed → error.
func ReadMeta(dir string) (*jsonx.Object, error) {
	b, err := os.ReadFile(filepath.Join(dir, MetaFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoMeta
		}
		return nil, err
	}
	b = []byte(strings.TrimPrefix(string(b), "\ufeff"))
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, fmt.Errorf("Invalid meta.json in %s: %v", dir, err)
	}
	return o, nil
}

// ReadMetaOrEmpty never fails: missing or malformed → empty object.
func ReadMetaOrEmpty(dir string) *jsonx.Object {
	o, err := ReadMeta(dir)
	if err != nil || o == nil {
		return &jsonx.Object{}
	}
	return o
}

// RenderMeta serialises meta exactly like upstream write_meta.
func RenderMeta(meta *jsonx.Object) []byte {
	return []byte(jsonx.Dumps(meta, MetaOptions) + "\n")
}

// ValidateMeta returns validation errors for required keys.
func ValidateMeta(meta *jsonx.Object) []string {
	var errs []string
	for _, k := range RequiredMetaKeys {
		v, ok := meta.Get(k)
		if !ok || !truthy(v) {
			errs = append(errs, "Missing required field: "+k)
		}
	}
	return errs
}

// WriteMeta atomically writes <dir>/meta.json (sorted keys, unicode kept,
// trailing newline, mode 0600 like mkstemp).
func WriteMeta(dir string, meta *jsonx.Object, validate bool) error {
	if validate {
		if errs := ValidateMeta(meta); len(errs) > 0 {
			return fmt.Errorf("Invalid meta.json for %s: %s", filepath.Base(dir), strings.Join(errs, "; "))
		}
	}
	return atomicfile.Write(filepath.Join(dir, MetaFile), RenderMeta(meta))
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case int:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.Object:
		return t.Len() > 0
	}
	return true
}

// MissionNumber coerces meta.mission_number to (int, ok): null/absent → !ok;
// "042" → 42.
func MissionNumber(meta *jsonx.Object) (int, bool) {
	v, _ := meta.Get("mission_number")
	switch t := v.(type) {
	case int:
		return t, true
	case float64:
		return int(t), true
	case string:
		s := strings.TrimSpace(t)
		if n, err := strconv.Atoi(s); err == nil {
			return n, true
		}
	}
	return 0, false
}

// MissionNumberValue returns meta.mission_number as a JSON value (int or nil).
func MissionNumberValue(meta *jsonx.Object) any {
	if n, ok := MissionNumber(meta); ok {
		return n
	}
	return nil
}

// MissionNumberString renders mission_identity_fields' mission_number: int →
// decimal, null → slug prefix number or "".
func MissionNumberString(meta *jsonx.Object, slug string) string {
	if n, ok := MissionNumber(meta); ok {
		return strconv.Itoa(n)
	}
	if n, ok := NumberFromSlug(slug); ok {
		return strconv.Itoa(n)
	}
	return ""
}

// MissionType returns meta.mission_type ("" when absent).
func MissionType(meta *jsonx.Object) string {
	if meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.GetString("mission_type"))
}
