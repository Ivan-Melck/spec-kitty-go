package healthcheck

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// Built-in pack provenance (charter/offering/provenance.py and
// m_4_0_0rc5_heal_template_set_provenance.py).

// BuiltInPackRoot is kernel.paths.get_built_in_pack_root for this build:
// $SPEC_KITTY_PACKS_ROOT/built-in when it names an existing directory. The
// Go binary embeds its packs, so it has no installed on-disk pack tree to
// fall back to (upstream: <site-packages>/packs/built-in); "" then.
func BuiltInPackRoot() string {
	v := os.Getenv("SPEC_KITTY_PACKS_ROOT")
	if v == "" {
		return ""
	}
	p := filepath.Join(v, "built-in")
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return ""
	}
	return pyResolve(p)
}

// pyResolve is Path.resolve() (non-strict): absolute, symlinks resolved for
// the longest existing prefix.
func pyResolve(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if r, err := filepath.EvalSymlinks(a); err == nil {
		return r
	}
	dir, base := filepath.Split(a)
	dir = filepath.Clean(dir)
	if dir == a {
		return a
	}
	return filepath.Join(pyResolve(dir), base)
}

// IsBuiltInPackPath is is_built_in_pack_path: path resolves under the
// built-in pack root.
func IsBuiltInPackPath(p string) bool {
	if p == "" {
		return false
	}
	root := BuiltInPackRoot()
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, pyResolve(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

var winAbsRe = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|[\\/]{2}[^\\/]+[\\/][^\\/]+)`)

// IsAbsolutePathAnyOS is Path(v).is_absolute() or PureWindowsPath(v).is_absolute().
func IsAbsolutePathAnyOS(v string) bool { return filepath.IsAbs(v) || winAbsRe.MatchString(v) }

var builtInRootParts = []string{"packs", "built-in"}

// posixParts is PurePosixPath(p.replace("\\", "/")).parts.
func posixParts(p string) []string {
	p = strings.ReplaceAll(p, `\`, "/")
	var out []string
	if strings.HasPrefix(p, "/") {
		out = append(out, "/")
	}
	for _, s := range strings.Split(p, "/") {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

func endsWithParts(src, suffix []string) bool {
	if len(src) < len(suffix) {
		return false
	}
	for i := range suffix {
		if src[len(src)-len(suffix)+i] != suffix[i] {
			return false
		}
	}
	return true
}

// MissionSuffix is _expected_mission_suffix ("" when mission is unusable).
func MissionSuffix(mission string) string {
	if mission == "" || strings.ContainsAny(mission, `/\`) || mission == "." || mission == ".." {
		return ""
	}
	return "built-in/missions/" + mission + "/mission.yaml"
}

// matchesCanonicalShape is _matches_canonical_source_shape.
func matchesCanonicalShape(sourcePath, suffix string) bool {
	src, exp := posixParts(sourcePath), posixParts(suffix)
	if !endsWithParts(src, exp) {
		return false
	}
	builtIn := len(src) - len(exp)
	start := builtIn - len(builtInRootParts) + 1
	if start < 0 {
		return false
	}
	return src[start] == "packs" && src[builtIn] == "built-in"
}

// SuffixMatches is the ambiguity test: the path ends with the expected
// mission suffix.
func SuffixMatches(sourcePath, suffix string) bool {
	return endsWithParts(posixParts(sourcePath), posixParts(suffix))
}

// MatchesMissionSource is _matches_mission_source: an absolute, canonically
// shaped built-in mission path under the built-in root, or proven to come
// from a former Spec Kitty checkout.
func MatchesMissionSource(sourcePath, suffix string) bool {
	if !IsAbsolutePathAnyOS(sourcePath) || !filepath.IsAbs(sourcePath) {
		return false
	}
	if suffix == "" || !matchesCanonicalShape(sourcePath, suffix) {
		return false
	}
	return IsBuiltInPackPath(sourcePath) || hasFormerCheckoutProof(sourcePath, suffix)
}

type snapEntry struct {
	dev, ino uint64
	typ      uint32
}

// sourcePathSnapshot is _source_path_snapshot (ok=false is None).
func sourcePathSnapshot(checkoutRoot string, rel []string) ([]snapEntry, bool) {
	if !filepath.IsAbs(checkoutRoot) {
		return nil, false
	}
	comps := append(posixParts(checkoutRoot)[1:], rel...)
	for _, c := range comps {
		if c == "" || c == "." || c == ".." {
			return nil, false
		}
	}
	current := "/"
	var snap []snapEntry
	for i, c := range comps {
		current = filepath.Join(current, c)
		st, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return snap, true
		}
		if err != nil {
			return nil, false
		}
		mode := st.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, false
		}
		final := i == len(comps)-1
		if final && !mode.IsRegular() || !final && !mode.IsDir() {
			return nil, false
		}
		e := snapEntry{typ: uint32(mode.Type())}
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			e.dev, e.ino = uint64(sys.Dev), uint64(sys.Ino)
		}
		snap = append(snap, e)
	}
	return snap, true
}

func sameSnap(a, b []snapEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const specKittyRepository = "github.com/spec-kitty/spec-kitty"

// isSpecKittyRepository is _is_spec_kitty_repository.
func isSpecKittyRepository(v string) bool {
	n := strings.ToLower(strings.TrimSpace(v))
	if strings.HasPrefix(n, "git@github.com:") {
		n = "ssh://" + strings.Replace(n, ":", "/", 1)
	}
	u, err := url.Parse(n)
	if err != nil {
		return false
	}
	repo := u.Hostname() + strings.TrimSuffix(strings.TrimRight(u.Path, "/"), ".git")
	return repo == specKittyRepository && (u.Scheme == "https" || u.Scheme == "ssh")
}

// hasFormerCheckoutProof is _has_former_checkout_proof.
func hasFormerCheckoutProof(sourcePath, suffix string) bool {
	rel := append(append([]string{}, builtInRootParts...), posixParts(suffix)[1:]...)
	root := sourcePath
	for range rel {
		root = filepath.Dir(root)
	}
	initial, ok := sourcePathSnapshot(root, rel)
	if !ok {
		return false
	}
	expected := filepath.Join(append([]string{pyResolve(root)}, rel...)...)
	if pyResolve(sourcePath) != pyResolve(expected) {
		return false
	}
	return checkoutTracksMission(root, rel, initial)
}

// checkoutTracksMission is _checkout_tracks_mission.
func checkoutTracksMission(root string, rel []string, initial []snapEntry) bool {
	b, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil {
		return false
	}
	meta, err := tracker.ParseTOML(string(b))
	if err != nil {
		return false
	}
	proj := meta.GetObject("project")
	if proj == nil || proj.GetString("name") != "spec-kitty-cli" {
		return false
	}
	var repository string
	found := false
	if urls := proj.GetObject("urls"); urls != nil {
		for _, kv := range urls.Pairs() {
			if s, isStr := kv.Value.(string); isStr && strings.ToLower(kv.Key) == "repository" {
				repository, found = s, true
				break
			}
		}
	}
	if !found || !isSpecKittyRepository(repository) {
		return false
	}
	top, err := gitx.Run(root, "rev-parse", "--show-toplevel")
	if err != nil || pyResolve(strings.TrimSpace(top)) != pyResolve(root) {
		return false
	}
	origin, err := gitx.Run(root, "remote", "get-url", "origin")
	if err != nil || !isSpecKittyRepository(strings.TrimSpace(origin)) {
		return false
	}
	before, ok := sourcePathSnapshot(root, rel)
	if !ok || !sameSnap(before, initial) {
		return false
	}
	relPath := strings.Join(rel, "/")
	out, err := gitx.Run(root, "ls-files", "--stage", "-z", "--", ":(literal)"+relPath)
	if err != nil {
		return false
	}
	after, ok := sourcePathSnapshot(root, rel)
	if !ok || !sameSnap(after, before) {
		return false
	}
	var recs []string
	for _, r := range strings.Split(out, "\x00") {
		if r != "" {
			recs = append(recs, r)
		}
	}
	if len(recs) != 1 {
		return false
	}
	head, path, _ := strings.Cut(recs[0], "\t")
	f := strings.Fields(head)
	return len(f) == 3 && path == relPath && (f[0] == "100644" || f[0] == "100755") && f[2] == "0"
}

