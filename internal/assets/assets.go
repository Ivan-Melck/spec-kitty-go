// Package assets embeds the data files shipped in the upstream spec-kitty
// wheel (doctrine packs, mission definitions, step prompts, skills,
// templates, schemas). Paths mirror the in-wheel layout, e.g.
// "packs/built-in/missions/software-dev/mission.yaml" or
// "charter/offering/skills/<name>/SKILL.md".
package assets

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed all:data
var raw embed.FS

// FS is the asset tree rooted at the wheel layout.
var FS fs.FS = mustSub(raw, "data")

func mustSub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}

// Revision is the upstream commit the assets were synced from.
func Revision() string {
	b, _ := fs.ReadFile(FS, "UPSTREAM_REVISION")
	return strings.TrimSpace(string(b))
}

// BuiltInPackRoot is the asset path of the built-in doctrine pack.
const BuiltInPackRoot = "packs/built-in"

// packsOverride honours SPEC_KITTY_PACKS_ROOT (<value>/built-in on disk).
func packsOverride() string {
	if v := os.Getenv("SPEC_KITTY_PACKS_ROOT"); v != "" {
		if st, err := os.Stat(filepath.Join(v, "built-in")); err == nil && st.IsDir() {
			return v
		}
	}
	return ""
}

// DiskSource returns the mutable source behind a pack override. Embedded
// assets have no filesystem observation to retain for an install batch.
func DiskSource(name string) string {
	name = path.Clean(name)
	if root := packsOverride(); root != "" && strings.HasPrefix(name, "packs/") {
		return filepath.Join(root, strings.TrimPrefix(name, "packs/"))
	}
	return ""
}

// ReadFile reads an asset. Paths under packs/ honour SPEC_KITTY_PACKS_ROOT.
func ReadFile(name string) ([]byte, error) {
	name = path.Clean(name)
	if o := packsOverride(); o != "" && strings.HasPrefix(name, "packs/") {
		return os.ReadFile(filepath.Join(o, strings.TrimPrefix(name, "packs/")))
	}
	return fs.ReadFile(FS, name)
}

// MustRead reads an asset or panics (for assets that are always shipped).
func MustRead(name string) []byte {
	b, err := ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}

// Exists reports whether an asset exists.
func Exists(name string) bool {
	_, err := Stat(name)
	return err == nil
}

// Stat stats an asset.
func Stat(name string) (fs.FileInfo, error) {
	name = path.Clean(name)
	if o := packsOverride(); o != "" && strings.HasPrefix(name, "packs/") {
		return os.Stat(filepath.Join(o, strings.TrimPrefix(name, "packs/")))
	}
	return fs.Stat(FS, name)
}

// Dir returns an fs.FS rooted at dir.
func Dir(dir string) fs.FS {
	dir = path.Clean(dir)
	if o := packsOverride(); o != "" && strings.HasPrefix(dir, "packs/") {
		return os.DirFS(filepath.Join(o, strings.TrimPrefix(dir, "packs/")))
	}
	return mustSub(FS, dir)
}

// Walk lists every regular file below dir (sorted, slash paths relative to
// the asset root).
func Walk(dir string) ([]string, error) {
	var out []string
	err := fs.WalkDir(Dir(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, path.Join(dir, p))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// List returns the names of the direct entries of dir (sorted).
func List(dir string) ([]fs.DirEntry, error) {
	return fs.ReadDir(Dir(dir), ".")
}

// CopyTree copies the asset subtree src into the filesystem directory dst.
func CopyTree(src, dst string, perm os.FileMode) ([]string, error) {
	files, err := Walk(src)
	if err != nil {
		return nil, err
	}
	var written []string
	for _, f := range files {
		rel := strings.TrimPrefix(f, path.Clean(src)+"/")
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return written, err
		}
		b, err := ReadFile(f)
		if err != nil {
			return written, err
		}
		if err := os.WriteFile(target, b, perm); err != nil {
			return written, err
		}
		written = append(written, target)
	}
	return written, nil
}
