package kittify

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// RenderConfigUpdate renders config.yaml after fn edits the loaded mapping,
// without writing: it returns the current bytes (nil when absent) and the
// bytes SaveConfig/UpdateConfig would write (ruamel round trip: comments
// and styles of untouched entries kept).
func RenderConfigUpdate(root string, fn func(cfg *jsonx.Object)) (before, desired []byte, err error) {
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, nil, err
	}
	p := ConfigPath(root)
	if b, rerr := os.ReadFile(p); rerr == nil {
		before = b
	}
	fn(cfg)
	return before, []byte(renderOver(p, cfg, nil)), nil
}
