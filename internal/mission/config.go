package mission

import (
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// ProjectConfig reads .kittify/config.yaml (empty object when absent or
// malformed). Area B owns the full config model; this is a minimal reader.
func ProjectConfig(root string) *jsonx.Object {
	b, err := os.ReadFile(project.Kittify(root, "config.yaml"))
	if err != nil {
		return &jsonx.Object{}
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return &jsonx.Object{}
	}
	return o
}

// AutoCommit returns agents.auto_commit (default true).
func AutoCommit(root string) bool {
	agents := ProjectConfig(root).GetObject("agents")
	if agents == nil {
		return true
	}
	v, ok := agents.Get("auto_commit")
	if !ok || v == nil {
		return true
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return !(s == "false" || s == "0" || s == "no" || s == "off")
	}
	return true
}

// ProjectIdentity returns (uuid, slug) from config.yaml's project block.
func ProjectIdentity(root string) (uuid, slug string) {
	p := ProjectConfig(root).GetObject("project")
	if p == nil {
		return "", ""
	}
	return p.GetString("uuid"), p.GetString("slug")
}
