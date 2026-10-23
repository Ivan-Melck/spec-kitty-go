package bootstrap

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// Observations belong to a preparation, not the ownership inventory. File
// timestamps and directory identities detect a replacement even if its payload
// happens to match. Source roles are sticky across subsequent destination reads.
type assetObservation struct {
	state    FileState
	info     os.FileInfo
	children []string
	source   bool
}

type tornReadError struct {
	prep   *prep
	path   string
	source bool
}

func (e *tornReadError) Error() string { return "Asset changed during preparation: " + e.path }

// A destination changed while the owner was still preparing its batch. Wait
// for that owner once, then rebuild against the settled tree. Source changes
// are terminal and never gain destination tolerance through a retry.
func buildSerialized(build func() (*prep, error)) (*prep, error) {
	p, err := build()
	var torn *tornReadError
	if !errors.As(err, &torn) || torn.source {
		return p, err
	}
	unlock, err := torn.prep.serialize()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return build()
}

var assetEnvironmentNames = []string{"HOME", "USERPROFILE", "SPEC_KITTY_HOME", "SPEC_KITTY_TEMPLATE_ROOT", "SPEC_KITTY_PACKS_ROOT", "XDG_CONFIG_HOME", "OPENCODE_CONFIG_DIR", "LOCALAPPDATA"}

func (p *prep) observeSourceTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		_, err = p.observeAsset(path, entry.IsDir(), true)
		return err
	})
}

func assetEnvironment() map[string]*string {
	env := map[string]*string{}
	for _, name := range assetEnvironmentNames {
		if value, exists := os.LookupEnv(name); exists {
			env[name] = &value
		} else {
			env[name] = nil
		}
	}
	return env
}

func directoryMembers(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

func readAssetObservation(path string, source bool) (assetObservation, error) {
	state, err := observe(path)
	if err != nil {
		return assetObservation{}, err
	}
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return assetObservation{}, err
	}
	return assetObservation{state: state, info: info, source: source}, nil
}

func sameObservation(a, b assetObservation) bool {
	if a.state != b.state {
		return false
	}
	if a.info == nil || b.info == nil {
		return a.info == nil && b.info == nil
	}
	if a.state.Kind == "directory" {
		return os.SameFile(a.info, b.info)
	}
	return a.info.ModTime() == b.info.ModTime()
}

func (p *prep) observeAsset(path string, members, source bool) (FileState, error) {
	var parents []string
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		parents = append(parents, dir)
	}
	for i := len(parents) - 1; i >= 0; i-- {
		dir := parents[i]
		parent, err := readAssetObservation(dir, source)
		if err != nil {
			return FileState{}, err
		}
		alias := runtime.GOOS == "darwin" && (dir == "/var" || dir == "/tmp") && parent.state.Kind == "symlink" && parent.state.Target == "private/"+filepath.Base(dir)
		if parent.state.Kind != "absent" && parent.state.Kind != "directory" && !alias {
			return FileState{}, fmt.Errorf("Asset parent is not a directory: %s", dir)
		}
		if previous, ok := p.checks[dir]; ok {
			previous.source = previous.source || source
			p.checks[dir] = previous
		} else {
			p.checks[dir] = parent
		}
	}
	current, err := readAssetObservation(path, source)
	if err != nil {
		return FileState{}, err
	}
	if previous, ok := p.checks[path]; ok {
		if !sameObservation(previous, current) {
			return FileState{}, &tornReadError{p, path, source || previous.source}
		}
		current.source = current.source || previous.source
		current.children = previous.children
	}
	if members && current.state.Kind == "directory" {
		current.children, err = directoryMembers(path)
		if err != nil {
			return FileState{}, err
		}
	}
	p.checks[path] = current
	p.observed[path] = current.state
	return current.state, nil
}

func canonicalContent(current, desired FileState) bool {
	return current.Kind == desired.Kind && current.SHA256 == desired.SHA256
}

func bookkeepingWrite(w *write) bool {
	if w.proof.Kind != "managed_path" {
		return false
	}
	suffix := w.proof.Reference[strings.LastIndex(w.proof.Reference, ":")+1:]
	return suffix == "inventory" || suffix == "version-stamp" || suffix == "persistent-lock"
}

func (p *prep) contentConfirmed() bool {
	for path, w := range p.writes {
		if w.after.Kind == "file" && !bookkeepingWrite(w) {
			state, err := observe(path)
			if err != nil || !canonicalContent(state, w.after) {
				return false
			}
		}
	}
	return true
}

func (p *prep) membershipTolerated(path string, old assetObservation, current []string) bool {
	if old.source || !p.contentConfirmed() {
		return false
	}
	for _, name := range old.children {
		if !slices.Contains(current, name) {
			return false
		}
	}
	for _, name := range current {
		if slices.Contains(old.children, name) {
			continue
		}
		child := filepath.Join(path, name)
		w := p.writes[child]
		if w == nil {
			return false
		}
		state, err := observe(child)
		if err != nil || !canonicalContent(state, w.after) {
			return false
		}
	}
	return true
}

// checkAssets runs while holding the owner lock, before any batch writes.
// A peer's canonical destination payload may converge; source changes and
// incomplete completion markers must still refuse the whole batch.
func (p *prep) checkAssets() error {
	for _, name := range assetEnvironmentNames {
		value, exists := os.LookupEnv(name)
		before := p.environ[name]
		if (before == nil) != !exists || (exists && before != nil && *before != value) {
			return fmt.Errorf("Global asset environment changed: %s", name)
		}
	}
	paths := make([]string, 0, len(p.checks))
	for path := range p.checks {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		a, b := strings.Count(paths[i], string(filepath.Separator)), strings.Count(paths[j], string(filepath.Separator))
		if a != b {
			return a < b
		}
		return paths[i] < paths[j]
	})
	sentinel := coldInstallSentinel(p.anchor)
	for _, path := range paths {
		if path == p.lockPath {
			continue
		}
		old := p.checks[path]
		current, err := readAssetObservation(path, old.source)
		if err != nil {
			return fmt.Errorf("Could not read global asset %s: %w", path, err)
		}
		sentinelParent := old.state.Kind == "absent" && current.state.Kind == "directory" && strings.HasPrefix(sentinel, path+string(filepath.Separator))
		if !sameObservation(old, current) && !sentinelParent {
			w := p.writes[path]
			tolerated := !old.source && w != nil && canonicalContent(current.state, w.after)
			if tolerated && (w.after.Kind != "file" || bookkeepingWrite(w)) {
				tolerated = p.contentConfirmed()
			}
			if !tolerated {
				return fmt.Errorf("Global asset input changed: %s", path)
			}
		}
		if old.children != nil {
			children, err := directoryMembers(path)
			if err != nil {
				return err
			}
			if !slices.Equal(old.children, children) && !p.membershipTolerated(path, old, children) {
				return fmt.Errorf("Global asset inventory changed: %s", path)
			}
		}
	}
	return nil
}
