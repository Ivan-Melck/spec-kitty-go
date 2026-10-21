package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// ledgerName is the per-bundle ownership manifest.
const ledgerName = ".spec-kitty-bundle.json"

const owner = "plugin_bundle"

// BuildError is _builder.BuildError (uncaught upstream: a traceback, exit 1).
type BuildError struct{ Msg string }

func (e *BuildError) Error() string { return e.Msg }

// StagedFile is one bundle output; Path is relative to the staging root.
type StagedFile struct {
	Path     string
	Content  []byte
	Mode     os.FileMode
	Manifest bool
	Managed  bool
}

func staged(p string, content []byte) StagedFile {
	return StagedFile{Path: p, Content: content, Mode: 0o644, Managed: true}
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// jsonLegacy is json.dumps(payload, indent=2, ensure_ascii=False) (no
// trailing newline).
func jsonLegacy(v any) []byte { return []byte(jsonx.Dumps(v, jsonx.PrettyUTF8)) }

// stagingRoot is staging_root + confined_output: the nearest existing
// ancestor of dir's parent (resolved) and dir re-rooted beneath it.
func stagingRoot(dir string) (root, confined string, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	cand := filepath.Dir(abs)
	for {
		if _, err := os.Stat(cand); err == nil {
			break
		}
		cand = filepath.Dir(cand)
	}
	if st, err := os.Lstat(cand); err == nil && st.Mode()&fs.ModeSymlink != 0 {
		return "", "", &pyError{"ValueError", "Staging ancestor is a symlink: " + cand}
	}
	resolved, err := filepath.EvalSymlinks(cand)
	if err != nil {
		return "", "", err
	}
	rel, _ := filepath.Rel(cand, abs)
	return resolved, filepath.Join(resolved, rel), nil
}

// pyError is an uncaught Python exception.
type pyError struct{ Type, Msg string }

func (e *pyError) Error() string { return e.Type + ": " + e.Msg }

// node is an lstat observation of one staging path.
type node struct {
	kind string // absent | file | directory | symlink | other
	sum  string
	mode os.FileMode
}

func observe(p string) (node, error) {
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return node{kind: "absent"}, nil
	}
	if err != nil {
		return node{}, err
	}
	mode := st.Mode().Perm()
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		return node{kind: "symlink", mode: mode}, nil
	case st.Mode().IsRegular():
		b, err := os.ReadFile(p)
		if err != nil {
			return node{}, err
		}
		return node{kind: "file", sum: digest(b), mode: mode}, nil
	case st.IsDir():
		return node{kind: "directory", mode: mode}, nil
	}
	return node{}, &BuildError{"Unsupported staging node: " + p}
}

// observeConfined is observe_confined: every parent of root/rel must be a
// directory or absent.
func observeConfined(root, rel string) (node, error) {
	parts := strings.Split(rel, "/")
	cur := root
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		n, err := observe(cur)
		if err != nil {
			return n, err
		}
		if i < len(parts)-1 && n.kind != "directory" && n.kind != "absent" {
			return n, &BuildError{"Unsafe bundle parent: " + cur}
		}
		if i == len(parts)-1 {
			return n, nil
		}
	}
	return node{kind: "absent"}, nil
}

type ledgerEntry struct {
	sum  string
	mode int64
}

// readLedger is _ledger_entries over an existing ledger file.
func readLedger(p string) (map[string]ledgerEntry, error) {
	out := map[string]ledgerEntry{}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := pyjson.LoadsBytes(b)
	if err != nil {
		return nil, &BuildError{err.Error()}
	}
	o, ok := v.(*jsonx.Object)
	invalid := &BuildError{"Invalid required bundle ownership manifest"}
	if !ok {
		return nil, invalid
	}
	if ow, _ := o.Get("owner"); ow != owner {
		return nil, invalid
	}
	sv, _ := o.Get("schema_version")
	if n, isInt := asInt(sv); !isInt || n != 1 {
		return nil, invalid
	}
	membersV, _ := o.Get("members")
	members, ok := membersV.(*jsonx.Object)
	if !ok {
		return nil, &BuildError{"Invalid required bundle ownership members"}
	}
	for _, kv := range members.Pairs() {
		e, ok := kv.Value.(*jsonx.Object)
		if !ok {
			return nil, &BuildError{"Invalid bundle ownership entry"}
		}
		if kv.Key == "" || strings.HasPrefix(kv.Key, "/") || containsDotDot(kv.Key) {
			return nil, &BuildError{"Unconfined bundle ownership entry"}
		}
		sum, _ := e.Get("sha256")
		mode, _ := e.Get("mode")
		s, sok := sum.(string)
		m, mok := asInt(mode)
		if !sok || !mok {
			return nil, &pyError{"AssertionError", ""}
		}
		out[kv.Key] = ledgerEntry{s, m}
	}
	return out, nil
}

func asInt(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	}
	return 0, false
}

func containsDotDot(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// stage is prepare_staging + apply_staging + the preserved-content check of
// finish_build for one batch whose ledgers live in bundleDirs (paths
// relative to root).
func stage(root string, files []StagedFile, bundleDirs []string) error {
	known := map[string]ledgerEntry{}
	type ledger struct {
		path    string
		dir     string
		entries map[string]ledgerEntry
	}
	var ledgers []ledger
	for _, d := range bundleDirs {
		lp := path.Join(d, ledgerName)
		n, err := observeConfined(root, lp)
		if err != nil {
			return err
		}
		if n.kind != "file" && n.kind != "absent" {
			return &BuildError{"Unsafe bundle ownership manifest: " + filepath.Join(root, lp)}
		}
		entries, err := readLedger(filepath.Join(root, lp))
		if err != nil {
			return err
		}
		for rel, e := range entries {
			known[path.Join(d, rel)] = e
		}
		ledgers = append(ledgers, ledger{lp, d, entries})
	}

	// Unique members, first occurrence order.
	var order []string
	unique := map[string]StagedFile{}
	for _, f := range files {
		if prev, ok := unique[f.Path]; ok {
			if string(prev.Content) != string(f.Content) || prev.Mode != f.Mode {
				return &BuildError{"Conflicting staged output: " + f.Path}
			}
			continue
		}
		unique[f.Path] = f
		order = append(order, f.Path)
	}

	type effect struct {
		file  StagedFile
		write bool // content (create/update/replace) vs chmod only
	}
	var effects []effect
	preserved := map[string]bool{}
	for _, p := range order {
		m := unique[p]
		before, err := observeConfined(root, p)
		if err != nil {
			return err
		}
		sum := digest(m.Content)
		if before.kind == "symlink" {
			preserved[p] = true
			continue
		}
		if before.kind != "absent" && before.sum != sum {
			preserved[p] = true
			continue
		}
		switch {
		case before.kind == "absent":
			effects = append(effects, effect{m, true})
		case before.mode != m.Mode:
			effects = append(effects, effect{m, false})
		}
	}
	if len(preserved) > 0 {
		kept := effects[:0]
		for _, e := range effects {
			if !e.file.Manifest {
				kept = append(kept, e)
			} else {
				preserved[e.file.Path] = true
			}
		}
		effects = kept
		for _, p := range order {
			if unique[p].Manifest {
				preserved[p] = true
			}
		}
	}
	var ledgerEffects []effect
	for _, l := range ledgers {
		members := map[string]ledgerEntry{}
		for k, v := range l.entries {
			members[k] = v
		}
		for _, p := range order {
			m := unique[p]
			if !m.Managed || preserved[p] {
				continue
			}
			if rel, ok := relUnder(p, l.dir); ok {
				members[rel] = ledgerEntry{digest(m.Content), int64(m.Mode)}
			}
		}
		keys := make([]string, 0, len(members))
		for k := range members {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mo := &jsonx.Object{}
		for _, k := range keys {
			mo.Set(k, jsonx.NewObject("mode", members[k].mode, "sha256", members[k].sum))
		}
		content := []byte(jsonx.Dumps(jsonx.NewObject("members", mo, "owner", owner, "schema_version", 1), jsonx.SortedPretty) + "\n")
		before, err := observeConfined(root, l.path)
		if err != nil {
			return err
		}
		f := StagedFile{Path: l.path, Content: content, Mode: 0o644, Manifest: true}
		switch {
		case before.kind != "file" || before.sum != digest(content):
			ledgerEffects = append(ledgerEffects, effect{f, true})
		case before.mode != f.Mode:
			ledgerEffects = append(ledgerEffects, effect{f, false})
		}
	}
	// Apply: members, then manifests, then ledgers (parents created 0755).
	sort.SliceStable(effects, func(i, j int) bool { return !effects[i].file.Manifest && effects[j].file.Manifest })
	for _, e := range append(effects, ledgerEffects...) {
		dest := filepath.Join(root, filepath.FromSlash(e.file.Path))
		if err := mkdirs(root, path.Dir(e.file.Path)); err != nil {
			return err
		}
		if !e.write {
			if err := os.Chmod(dest, e.file.Mode); err != nil {
				return err
			}
			continue
		}
		if err := writeStaged(dest, e.file.Content, e.file.Mode); err != nil {
			return err
		}
	}
	if len(preserved) > 0 {
		return &BuildError{"Staged build preserved custom or consent-required content"}
	}
	return nil
}

func relUnder(p, dir string) (string, bool) {
	if dir == "." || dir == "" {
		return p, true
	}
	if strings.HasPrefix(p, dir+"/") {
		return p[len(dir)+1:], true
	}
	return "", false
}

// mkdirs creates the missing directories of rel under root with mode 0755
// (exact, not umask-reduced).
func mkdirs(root, rel string) error {
	if rel == "." || rel == "" {
		return nil
	}
	cur := root
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		if _, err := os.Lstat(cur); err == nil {
			continue
		}
		if err := os.Mkdir(cur, 0o755); err != nil {
			return err
		}
		if err := os.Chmod(cur, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// writeStaged is write_staged_file: temp file, exact mode, atomic replace.
func writeStaged(dest string, content []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dest)
}

func relTo(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

func errorf(format string, a ...any) error { return &BuildError{fmt.Sprintf(format, a...)} }
