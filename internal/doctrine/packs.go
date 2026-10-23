package doctrine

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Layer names (artifact provenance).
const (
	LayerBuiltIn = "built-in"
	LayerOrg     = "org"
	LayerProject = "project"
)

// ProjectPackRoot is the project pack root relative to the repo root.
const ProjectPackRoot = ".kittify/charter-packs"

// PackFS is a pack root that can be read as files. Built-in packs are
// served from the embedded assets; org/project packs from disk.
type PackFS struct {
	// Disk is the on-disk directory of the pack (always set; for the
	// built-in pack it is the materialised copy, see BuiltInRoot).
	Disk string
	// fsys reads pack-relative slash paths.
	fsys fs.FS
}

// ReadFile reads a pack-relative slash path.
func (p *PackFS) ReadFile(rel string) ([]byte, error) { return fs.ReadFile(p.fsys, path.Clean(rel)) }

// Exists reports whether a pack-relative path exists.
func (p *PackFS) Exists(rel string) bool {
	_, err := fs.Stat(p.fsys, path.Clean(rel))
	return err == nil
}

// Path returns the absolute on-disk path of a pack-relative path.
func (p *PackFS) Path(rel string) string { return filepath.Join(p.Disk, filepath.FromSlash(rel)) }

// Walk lists files under dir (pack-relative, sorted) whose names end with suffix.
// recursive=false lists only direct children.
func (p *PackFS) Walk(dir, suffix string, recursive bool) []string {
	var out []string
	dir = path.Clean(dir)
	if _, err := fs.Stat(p.fsys, dir); err != nil {
		return nil
	}
	fs.WalkDir(p.fsys, dir, func(fp string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if fp != dir && !recursive {
				return fs.SkipDir
			}
			return nil
		}
		if suffix == "" || strings.HasSuffix(d.Name(), suffix) {
			out = append(out, fp)
		}
		return nil
	})
	sortPaths(out)
	return out
}

// sortPaths orders slash paths like Python's sorted(Path.rglob(...)): by
// path components (so "a/b" < "a-b").
func sortPaths(ps []string) {
	less := func(a, b string) bool {
		ap, bp := strings.Split(a, "/"), strings.Split(b, "/")
		for i := 0; i < len(ap) && i < len(bp); i++ {
			if ap[i] != bp[i] {
				return ap[i] < bp[i]
			}
		}
		return len(ap) < len(bp)
	}
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && less(ps[j], ps[j-1]); j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

// DiskPack returns a PackFS over an on-disk directory.
func DiskPack(dir string) *PackFS { return &PackFS{Disk: dir, fsys: os.DirFS(dir)} }

var (
	builtInOnce sync.Once
	builtInPack *PackFS
)

// BuiltIn returns the built-in pack. Reads come from the embedded assets
// (or SPEC_KITTY_PACKS_ROOT/built-in when set); Disk is a real directory
// holding the same files so that paths shown to agents can be opened.
func BuiltIn() *PackFS {
	assets.WarnPackRoot(assets.OfferingPackRoot)
	builtInOnce.Do(func() {
		if v := os.Getenv("SPEC_KITTY_PACKS_ROOT"); v != "" {
			d := filepath.Join(v, "built-in")
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				builtInPack = DiskPack(d)
				return
			}
		}
		builtInPack = &PackFS{Disk: materializedBuiltIn(), fsys: assets.Dir(assets.BuiltInPackRoot)}
	})
	return builtInPack
}

// BuiltInRoot is the on-disk directory of the built-in pack.
func BuiltInRoot() string { return BuiltIn().Disk }

// materializedBuiltIn extracts the embedded built-in pack into the user
// cache (~/.kittify/cache/packs/<revision>/built-in) once per revision and
// returns that directory. On failure it returns the would-be path.
func materializedBuiltIn() string {
	rev := assets.Revision()
	if rev == "" {
		rev = "dev"
	}
	dir := filepath.Join(project.GlobalKittify(), "cache", "packs", rev, "built-in")
	marker := filepath.Join(dir, ".complete")
	if _, err := os.Stat(marker); err == nil {
		return dir
	}
	tmp := dir + ".tmp-" + strings.ReplaceAll(filepath.Base(os.Args[0]), "/", "") + "-" + itoa(os.Getpid())
	os.RemoveAll(tmp)
	if _, err := assets.CopyTree(assets.BuiltInPackRoot, tmp, 0o644); err != nil {
		os.RemoveAll(tmp)
		return dir
	}
	os.WriteFile(filepath.Join(tmp, ".complete"), []byte(rev+"\n"), 0o644)
	os.RemoveAll(dir)
	os.MkdirAll(filepath.Dir(dir), 0o755)
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
	}
	return dir
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ProjectPack returns the project pack (.kittify/charter-packs) of repoRoot.
func ProjectPack(repoRoot string) *PackFS {
	return DiskPack(filepath.Join(repoRoot, filepath.FromSlash(ProjectPackRoot)))
}

// OrgPack is one configured org charter pack.
type OrgPack struct {
	Name      string
	PackID    string
	LocalPath string
	Subdir    string
	Root      string // effective absolute root
}

// OrgPacks reads charter_packs.org.packs from .kittify/config.yaml in
// declaration order (lenient: invalid entries yield no packs).
func OrgPacks(repoRoot string) []OrgPack {
	cfg, err := LoadConfig(repoRoot)
	if err != nil || cfg == nil {
		return nil
	}
	cp := cfg.GetObject("charter_packs")
	if cp == nil {
		return nil
	}
	org := cp.GetObject("org")
	if org == nil {
		return nil
	}
	var out []OrgPack
	for _, raw := range org.GetList("packs") {
		o, ok := raw.(*jsonx.Object)
		if !ok {
			return nil
		}
		name, lp := o.GetString("name"), o.GetString("local_path")
		if strings.TrimSpace(name) == "" || lp == "" {
			return nil
		}
		exp := os.ExpandEnv(lp)
		if strings.HasPrefix(exp, "~") {
			exp = filepath.Join(project.UserHome(), strings.TrimPrefix(exp, "~"))
		}
		if !filepath.IsAbs(exp) {
			exp = filepath.Join(repoRoot, exp)
		}
		root := filepath.Clean(exp)
		if sd := strings.TrimSpace(o.GetString("subdir")); sd != "" && sd != "." {
			root = filepath.Join(root, sd)
		}
		out = append(out, OrgPack{Name: name, PackID: o.GetString("pack_id"), LocalPath: lp, Subdir: o.GetString("subdir"), Root: root})
	}
	return out
}

// OrgRoots returns the existing org pack roots in declaration order
// (upstream resolve_pack_chain(strict=False)).
func OrgRoots(repoRoot string) []string {
	var out []string
	for _, p := range OrgPacks(repoRoot) {
		if st, err := os.Stat(p.Root); err == nil && st.IsDir() {
			out = append(out, p.Root)
		}
	}
	return out
}
