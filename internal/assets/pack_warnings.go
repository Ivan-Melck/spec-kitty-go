package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pywarnings"
)

// PackWarnLog receives get_built_in_pack_root's warnings. The CLI installs
// its logging sink in human mode; library callers may leave it nil.
var PackWarnLog pywarnings.Sink

// PackRootCaller identifies the upstream door into pack-root resolution.
type PackRootCaller int

const (
	OfferingPackRoot PackRootCaller = iota
	MissionAssetRoot
	ProvenancePackRoot
)

// WarnPackRoot reports an invalid override before falling back to embedded
// assets. Emit at resolution doors, rather than for each embedded file read:
// Python attributes this warning to the caller (stacklevel=2).
func WarnPackRoot(caller PackRootCaller) {
	v := os.Getenv("SPEC_KITTY_PACKS_ROOT")
	if v == "" {
		return
	}
	p := packRootChild(v)
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return
	}
	module, line, source := "charter/offering/pack_paths.py", 237, "return get_built_in_pack_root()"
	switch caller {
	case MissionAssetRoot:
		module, line, source = "kernel/paths.py", 435, "pack_root = get_built_in_pack_root()"
	case ProvenancePackRoot:
		module, line, source = "charter/offering/provenance.py", 71, "return get_built_in_pack_root().resolve()"
	}
	msg := fmt.Sprintf("SPEC_KITTY_PACKS_ROOT=%s does not resolve to a directory (expected %s to exist). Ignoring the override and falling back to the installed built-in pack tree -- fix or unset SPEC_KITTY_PACKS_ROOT if this is unexpected.", pyjson.ReprString(v), p)
	pywarnings.Warn(PackWarnLog, module, line, "UserWarning", msg, source)
}

// Path.__str__ collapses dots and repeated separators but preserves parent
// components (which can cross symlinks), unlike filepath.Clean.
func packRootChild(root string) string {
	if filepath.Separator != '/' {
		return filepath.Join(root, "built-in")
	}
	prefix := ""
	if strings.HasPrefix(root, "/") {
		prefix = "/"
		if strings.HasPrefix(root, "//") && !strings.HasPrefix(root, "///") {
			prefix = "//"
		}
	}
	var parts []string
	for _, part := range strings.Split(root, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return prefix + strings.Join(append(parts, "built-in"), "/")
}
