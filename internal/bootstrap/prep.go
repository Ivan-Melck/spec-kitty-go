// Package bootstrap implements the user-global asset preparation that the
// upstream CLI runs before (almost) every command: ensure_runtime()
// (~/.kittify/missions), ensure_global_agent_skills() (~/.claude/skills, ...)
// and ensure_global_agent_commands() (slash commands for 13 agents).
//
// Each "owner" keeps a private inventory in ~/.kittify/cache/<owner>-assets.json
// recording the canonical state of every node it wrote, so later runs can
// distinguish owned-and-unchanged files (safe to refresh or retire) from
// user edits (preserved).
package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// FileState is the upstream tool_surface.operations.FileState (mtime_ns is
// always recorded as null).
type FileState struct {
	Kind   string // absent, file, directory, symlink
	Mode   int
	SHA256 string
	Target string
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// observe reads one node without following a final symlink.
func observe(path string) (FileState, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return FileState{Kind: "absent"}, nil
	}
	if err != nil {
		return FileState{}, err
	}
	mode := int(info.Mode().Perm())
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		t, _ := os.Readlink(path)
		return FileState{Kind: "symlink", Mode: mode, Target: t}, nil
	case info.IsDir():
		return FileState{Kind: "directory", Mode: mode}, nil
	case info.Mode().IsRegular():
		b, err := os.ReadFile(path)
		if err != nil {
			return FileState{}, err
		}
		return FileState{Kind: "file", Mode: mode, SHA256: digest(b)}, nil
	}
	return FileState{}, fmt.Errorf("Unsupported asset node: %s", path)
}

type write struct {
	path    string
	before  FileState
	after   FileState
	content []byte
	action  string
	proof   Proof
}

// Proof is an ownership proof (operations.OwnershipProof).
type Proof struct{ Kind, Reference string }

// Disposition records a node left alone.
type Disposition struct {
	Path   string
	State  string // preserve, consent_required, unchanged
	Reason string
}

// prep is one owner's preparation (AssetPreparation).
type prep struct {
	owner     string
	anchor    string
	cache     string
	lockPath  string
	inventory string
	previous  map[string]FileState
	entries   map[string]FileState
	writes    map[string]*write
	order     []string
	selected  map[string]bool
	disp      []Disposition
	observed  map[string]FileState
}

func newPrep(owner, anchor, cache, lockName string) (*prep, error) {
	p := &prep{
		owner: owner, anchor: anchor, cache: cache,
		lockPath:  filepath.Join(cache, lockName),
		inventory: filepath.Join(cache, owner+"-assets.json"),
		previous:  map[string]FileState{}, entries: map[string]FileState{},
		writes: map[string]*write{}, selected: map[string]bool{}, observed: map[string]FileState{},
	}
	st, err := p.observe(p.inventory)
	if err != nil {
		return nil, err
	}
	if st.Kind == "file" {
		b, err := os.ReadFile(p.inventory)
		if err != nil {
			return nil, err
		}
		o, err := jsonx.DecodeObject(b)
		if err != nil {
			return nil, fmt.Errorf("Invalid global asset inventory: %s", p.inventory)
		}
		if v, _ := o.GetInt("schema_version"); v != 1 || o.GetObject("entries") == nil {
			return nil, fmt.Errorf("Invalid global asset inventory: %s", p.inventory)
		}
		for _, kv := range o.GetObject("entries").Pairs() {
			rec, ok := kv.Value.(*jsonx.Object)
			if !ok {
				return nil, fmt.Errorf("Invalid global asset inventory entry")
			}
			rel := kv.Key
			if filepath.IsAbs(rel) || strings.ContainsAny(rel, "\\:") {
				return nil, fmt.Errorf("Unconfined global asset inventory entry")
			}
			for _, part := range strings.Split(rel, "/") {
				if part == "" || part == "." || part == ".." {
					return nil, fmt.Errorf("Unconfined global asset inventory entry")
				}
			}
			fs := FileState{Kind: rec.GetString("kind"), SHA256: rec.GetString("sha256"), Target: rec.GetString("target")}
			fs.Mode, _ = rec.GetInt("mode")
			p.previous[rel] = fs
			p.entries[rel] = fs
		}
	} else if st.Kind != "absent" {
		return nil, fmt.Errorf("Global inventory is not a regular file: %s", p.inventory)
	}
	return p, nil
}

func (p *prep) observe(path string) (FileState, error) {
	if st, ok := p.observed[path]; ok {
		return st, nil
	}
	st, err := observe(path)
	if err != nil {
		return st, err
	}
	p.observed[path] = st
	return st, nil
}

func (p *prep) rel(path string) string {
	r, err := filepath.Rel(p.anchor, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(r)
}

func action(before, after FileState) string {
	switch {
	case before.Kind == "absent":
		if after.Kind != "absent" {
			return "create"
		}
		return ""
	case after.Kind == "absent":
		return "delete"
	case before.Kind != after.Kind:
		return "replace"
	case before.SHA256 != after.SHA256:
		return "update"
	case before.Target != after.Target:
		return "retarget"
	case before.Mode != after.Mode:
		return "chmod"
	}
	return ""
}

func (p *prep) effect(path string, after FileState, content []byte, proof Proof) error {
	before, err := p.observe(path)
	if err != nil {
		return err
	}
	act := action(before, after)
	if act == "" {
		p.disp = append(p.disp, Disposition{p.rel(path), "unchanged", "Asset already matches"})
		return nil
	}
	if prev, ok := p.writes[path]; ok {
		if prev.after != after {
			return fmt.Errorf("Conflicting global asset outputs: %s", path)
		}
		// A later identical plan replaces the earlier one (its proof wins).
		*prev = write{path: path, before: before, after: after, content: content, action: act, proof: proof}
		return nil
	}
	p.writes[path] = &write{path: path, before: before, after: after, content: content, action: act, proof: proof}
	p.order = append(p.order, path)
	return nil
}

func (p *prep) preserve(path, reason string, drift bool) {
	state := "preserve"
	if drift {
		state = "consent_required"
	}
	p.disp = append(p.disp, Disposition{p.rel(path), state, reason})
}

// parents plans creation of missing directories between the anchor and path.
func (p *prep) parents(path string) error {
	var chain []string
	for d := filepath.Dir(path); d != p.anchor && strings.HasPrefix(d, p.anchor+string(filepath.Separator)); d = filepath.Dir(d) {
		chain = append(chain, d)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		st, err := p.observe(chain[i])
		if err != nil {
			return err
		}
		if st.Kind == "absent" {
			if err := p.effect(chain[i], FileState{Kind: "directory", Mode: 0o755}, nil, Proof{"managed_path", p.owner + ":required-parent"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// asset compares canonical output with the destination (AssetPreparation.asset).
func (p *prep) asset(path string, content []byte, mode int, managedTree, predecessor bool) error {
	before, err := p.observe(path)
	if err != nil {
		return err
	}
	p.selected[path] = true
	rel := p.rel(path)
	desired := FileState{Kind: "directory", Mode: mode}
	if content != nil {
		desired = FileState{Kind: "file", Mode: mode, SHA256: digest(content)}
	}
	old, hasOld := p.previous[rel]
	owned := hasOld && before.Kind == old.Kind && before.SHA256 == old.SHA256 && before.Target == old.Target
	equal := before.Kind == desired.Kind && before.SHA256 == desired.SHA256
	if before.Kind != "absent" && !(owned || equal || predecessor || (managedTree && before.Kind != "symlink")) {
		reason := "Unproven existing asset"
		if hasOld {
			reason = "Changed managed asset"
		}
		p.preserve(path, reason, hasOld)
		return nil
	}
	if before.Kind == "directory" && desired.Kind != "directory" {
		p.preserve(path, "Directory replacement requires enumerated ownership", true)
		return nil
	}
	if err := p.parents(path); err != nil {
		return err
	}
	proof := Proof{"managed_path", p.owner + ":" + rel}
	if owned {
		proof = Proof{"manifest", filepath.Base(p.inventory) + ":" + rel}
	}
	if predecessor {
		proof = Proof{"canonical_content", p.owner + ":" + rel + ":canonical-predecessor"}
	}
	if err := p.effect(path, desired, content, proof); err != nil {
		return err
	}
	p.entries[rel] = desired
	return nil
}

// retire removes exact inventory-owned unchanged nodes, retaining drift.
func (p *prep) retire(path string) (bool, error) {
	before, err := p.observe(path)
	if err != nil {
		return false, err
	}
	if before.Kind == "absent" {
		return true, nil
	}
	rel := p.rel(path)
	old, ok := p.previous[rel]
	if !ok || before.Kind != old.Kind || before.SHA256 != old.SHA256 || before.Target != old.Target {
		p.preserve(path, "Unproven or edited retired asset", ok)
		return false, nil
	}
	if before.Kind == "directory" {
		ents, _ := os.ReadDir(path)
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		all := true
		for _, n := range names {
			ok, err := p.retire(filepath.Join(path, n))
			if err != nil {
				return false, err
			}
			all = all && ok
		}
		if !all {
			p.preserve(path, "Retired directory contains preserved content", false)
			return false, nil
		}
	}
	if err := p.effect(path, FileState{Kind: "absent"}, nil, Proof{"manifest", filepath.Base(p.inventory) + ":" + rel}); err != nil {
		return false, err
	}
	delete(p.entries, rel)
	return true, nil
}

// pruneMissing retires recorded descendants of destination not selected now.
func (p *prep) pruneMissing(destination string) error {
	prefix := p.rel(destination) + "/"
	var cands []string
	for rel := range p.previous {
		if strings.HasPrefix(rel, prefix) {
			cands = append(cands, rel)
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		di, dj := strings.Count(cands[i], "/"), strings.Count(cands[j], "/")
		if di != dj {
			return di < dj
		}
		return cands[i] < cands[j]
	})
	var retired []string
	for _, rel := range cands {
		path := filepath.Join(p.anchor, filepath.FromSlash(rel))
		if p.selected[path] {
			continue
		}
		skip := false
		for _, r := range retired {
			if strings.HasPrefix(rel, r+"/") {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if _, err := p.retire(path); err != nil {
			return err
		}
		retired = append(retired, rel)
	}
	return nil
}

// overwriteInternal unconditionally refreshes a cache bookkeeping .lock file.
func (p *prep) overwriteInternal(path string, content []byte) error {
	if err := p.parents(path); err != nil {
		return err
	}
	return p.effect(path, FileState{Kind: "file", Mode: 0o644, SHA256: digest(content)}, content, Proof{"managed_path", p.owner + ":freshness-stamp"})
}

// finish plans the inventory, version stamp and owner lock.
func (p *prep) finish(versionPath, version string) error {
	if !sameEntries(p.entries, p.previous) {
		data := []byte(renderInventory(p.entries))
		if err := p.parents(p.inventory); err != nil {
			return err
		}
		if err := p.effect(p.inventory, FileState{Kind: "file", Mode: 0o644, SHA256: digest(data)}, data, Proof{"managed_path", p.owner + ":inventory"}); err != nil {
			return err
		}
	}
	if versionPath != "" {
		if err := p.parents(versionPath); err != nil {
			return err
		}
		data := []byte(version)
		if err := p.effect(versionPath, FileState{Kind: "file", Mode: 0o644, SHA256: digest(data)}, data, Proof{"managed_path", p.owner + ":version-stamp"}); err != nil {
			return err
		}
	}
	if len(p.writes) > 0 {
		if err := p.parents(p.lockPath); err != nil {
			return err
		}
		st, err := os.Lstat(p.lockPath)
		if os.IsNotExist(err) {
			if err := p.effect(p.lockPath, FileState{Kind: "file", Mode: 0o644, SHA256: digest(nil)}, []byte{}, Proof{"managed_path", p.owner + ":persistent-lock"}); err != nil {
				return err
			}
		} else if err == nil && !st.Mode().IsRegular() {
			return fmt.Errorf("Global owner lock is not a regular file")
		}
	}
	return nil
}

func sameEntries(a, b map[string]FileState) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func renderInventory(entries map[string]FileState) string {
	e := &jsonx.Object{}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fs := entries[k]
		e.Set(k, jsonx.NewObject("kind", fs.Kind, "mode", fs.Mode, "mtime_ns", nil, "sha256", nullable(fs.SHA256), "target", nullable(fs.Target)))
	}
	return jsonx.Dumps(jsonx.NewObject("schema_version", 1, "entries", e), jsonx.SortedPretty) + "\n"
}

// writeOrder mirrors _write_order: dir creates, dot-locks, content, deletes,
// inventory, then .lock stamps last.
func (p *prep) sortedWrites() []*write {
	ws := make([]*write, 0, len(p.order))
	for _, path := range p.order {
		ws = append(ws, p.writes[path])
	}
	stage := func(w *write) int {
		name := filepath.Base(w.path)
		switch {
		case w.after.Kind == "directory" && w.action == "create":
			return 0
		case strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".lock"):
			return 1
		case strings.HasSuffix(name, "-assets.json"):
			return 5
		case strings.HasSuffix(name, ".lock"):
			return 6
		case w.action == "delete":
			return 4
		}
		return 3
	}
	depth := func(w *write) int {
		d := strings.Count(w.path, string(filepath.Separator))
		if w.action == "delete" {
			return -d
		}
		return d
	}
	sort.SliceStable(ws, func(i, j int) bool {
		si, sj := stage(ws[i]), stage(ws[j])
		if si != sj {
			return si < sj
		}
		di, dj := depth(ws[i]), depth(ws[j])
		if di != dj {
			return di < dj
		}
		return ws[i].path < ws[j].path
	})
	return ws
}

func applyWrite(w *write) error {
	switch {
	case w.action == "delete":
		if w.before.Kind == "directory" {
			return os.Remove(w.path)
		}
		return os.Remove(w.path)
	case w.action == "chmod":
		return os.Chmod(w.path, os.FileMode(w.after.Mode))
	case w.after.Kind == "directory":
		if err := os.Mkdir(w.path, os.FileMode(w.after.Mode)); err != nil && !os.IsExist(err) {
			return err
		}
		return os.Chmod(w.path, os.FileMode(w.after.Mode))
	}
	if w.before.Kind == "symlink" {
		os.Remove(w.path)
	}
	return writeFileAtomic(w.path, w.content, os.FileMode(w.after.Mode))
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// apply writes every planned effect under the owner lock.
func (p *prep) apply() error {
	if len(p.writes) == 0 {
		return nil
	}
	unlock, err := p.serialize()
	if err != nil {
		return err
	}
	defer unlock()
	for _, w := range p.sortedWrites() {
		if w.path == p.lockPath && w.action == "create" {
			if err := os.WriteFile(w.path, nil, 0o644); err != nil {
				return err
			}
			os.Chmod(w.path, 0o644)
			continue
		}
		if err := applyWrite(w); err != nil {
			return fmt.Errorf("%s: %v", p.owner, err)
		}
	}
	return nil
}

// serialize takes the cold-install sentinel when the owner lock is absent,
// else the owner lock itself.
func (p *prep) serialize() (func(), error) {
	if _, err := os.Stat(p.lockPath); os.IsNotExist(err) {
		return lockFile(coldInstallSentinel(p.anchor), 0o700)
	}
	return lockFile(p.lockPath, 0)
}

// coldInstallSentinel is ~/.spec-kitty-cold-install/<sha256(anchor)[:16]>.lock.
func coldInstallSentinel(anchor string) string {
	abs := anchor
	if r, err := filepath.EvalSymlinks(anchor); err == nil {
		abs = r
	}
	key := digest([]byte(abs))[:16]
	root := runtimeStateRoot()
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"-cold-install", key+".lock")
}
