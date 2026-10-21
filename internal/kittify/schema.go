package kittify

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// ProjectState is compat.planner.ProjectState.
type ProjectState string

const (
	StateNoProject     ProjectState = "no_project"
	StateUninitialized ProjectState = "uninitialized"
	StateLegacy        ProjectState = "legacy"
	StateStale         ProjectState = "stale"
	StateCompatible    ProjectState = "compatible"
	StateTooNew        ProjectState = "too_new"
	StateCorrupt       ProjectState = "corrupt"
)

// ProjectStatus is compat.planner.ProjectStatus.
type ProjectStatus struct {
	State         ProjectState
	Root          string
	SchemaVersion *int
	MetadataError string
}

const maxMetadataBytes = 262_144

// ProjectSchemaVersion is get_project_schema_version: spec_kitty.schema_version
// from metadata.yaml as an int, nil when absent or unusable. The second
// result reports a value that is present but not an int-convertible scalar.
func ProjectSchemaVersion(root string) *int {
	v, _ := projectSchemaVersion(root)
	return v
}

func projectSchemaVersion(root string) (*int, any) {
	b, err := os.ReadFile(MetadataPath(root))
	if err != nil {
		return nil, nil
	}
	b = []byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf"))
	doc, err := yamlx.Parse(b)
	if err != nil {
		return nil, nil
	}
	o, ok := doc.(*jsonx.Object)
	if !ok {
		return nil, nil
	}
	sk, ok := o.Get("spec_kitty")
	if !ok {
		return nil, nil
	}
	so, ok := sk.(*jsonx.Object)
	if !ok {
		return nil, nil
	}
	raw, ok := so.Get("schema_version")
	if !ok || raw == nil {
		return nil, nil
	}
	switch x := raw.(type) {
	case int:
		return &x, nil
	case int64:
		n := int(x)
		return &n, nil
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return nil, raw
		}
		n := int(x)
		return &n, nil
	case bool:
		n := 0
		if x {
			n = 1
		}
		return &n, nil
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return &n, nil
		}
	}
	return nil, raw
}

// ScanProjectStatus is compat.planner._scan_project for an already resolved
// project root ("" = no project).
func ScanProjectStatus(root string) ProjectStatus {
	if root == "" {
		return ProjectStatus{State: StateNoProject}
	}
	st := ProjectStatus{Root: root}
	fi, err := os.Stat(MetadataPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			st.State = StateUninitialized
			return st
		}
		st.State, st.MetadataError = StateCorrupt, fmt.Sprintf("stat failed: %v", err)
		return st
	}
	if fi.Size() > maxMetadataBytes {
		st.State, st.MetadataError = StateCorrupt, "oversized"
		return st
	}
	v, _ := projectSchemaVersion(root)
	if v == nil {
		st.State = StateLegacy
		return st
	}
	if *v < 0 || *v > 1000 {
		st.State, st.MetadataError = StateCorrupt, fmt.Sprintf("schema_version out of range: %d", *v)
		return st
	}
	st.SchemaVersion = v
	switch {
	case *v < MinSupportedSchema:
		st.State = StateStale
	case *v > MaxSupportedSchema:
		st.State = StateTooNew
	default:
		st.State = StateCompatible
	}
	return st
}

// NeedsMigration reports a stale or legacy project (BLOCK_PROJECT_MIGRATION
// for unsafe commands).
func (s ProjectStatus) NeedsMigration() bool {
	return s.State == StateStale || s.State == StateLegacy
}
