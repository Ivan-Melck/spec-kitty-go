package charter

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pep440"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

type PackPin struct {
	Repository, Commit, Subtree, Algorithm, Digest, Level string
	RequiresCore, Snapshot                                *string
}

var pinSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var windowsDrive = regexp.MustCompile(`^[A-Za-z]:`)

func ValidatePackSubtree(subtree string) error {
	if subtree == "" || strings.Contains(subtree, `\`) {
		return fmt.Errorf("subtree must be a non-empty POSIX path without backslashes; got %s", pyjson.Repr(subtree))
	}
	if strings.HasPrefix(subtree, "-") {
		return fmt.Errorf("subtree must not start with '-'; got %s", pyjson.Repr(subtree))
	}
	if strings.HasPrefix(subtree, "/") || windowsDrive.MatchString(subtree) || contains(strings.Split(subtree, "/"), "..") {
		return fmt.Errorf("subtree must be relative and must not contain '..'; got %s", pyjson.Repr(subtree))
	}
	return nil
}
func pinReadError(p string, err error) error {
	detail := err.Error()
	if os.IsNotExist(err) {
		detail = "[Errno 2] No such file or directory: " + pyjson.Repr(p)
	}
	return fmt.Errorf("cannot read pin lockfile %s: %s", p, detail)
}
func LoadPackPin(p string) (*PackPin, error) {
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, pinReadError(p, err)
	}
	value, err := yamlx.Parse(content)
	if err != nil {
		return nil, pinReadError(p, err)
	}
	data, ok := value.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("%s: expected a mapping at the top level", p)
	}
	digest := data.GetObject("tree_digest")
	if digest == nil {
		return nil, fmt.Errorf("%s: 'tree_digest' must be a mapping", p)
	}
	optional := func(object *jsonx.Object, key string) (*string, error) {
		raw, ok := object.Get(key)
		if !ok || raw == nil {
			return nil, nil
		}
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("field %s must be a string or null", pyjson.Repr(key))
		}
		return &text, nil
	}
	required := func(object *jsonx.Object, key, where string) (string, error) {
		raw, _ := object.Get(key)
		text, ok := raw.(string)
		if !ok || text == "" {
			return "", fmt.Errorf("%s: missing or non-string field %s", where, pyjson.Repr(key))
		}
		return text, nil
	}
	subtree, err := optional(data, "subtree")
	if err != nil {
		return nil, err
	}
	if subtree == nil {
		return nil, fmt.Errorf("%s: missing field 'subtree' (the pack's path inside the open-packs repository)", p)
	}
	pin := &PackPin{Subtree: *subtree, Level: "built-in"}
	if pin.Repository, err = required(data, "repository", p); err != nil {
		return nil, err
	}
	if pin.Commit, err = required(data, "commit", p); err != nil {
		return nil, err
	}
	if pin.Algorithm, err = required(digest, "algorithm", "tree_digest"); err != nil {
		return nil, err
	}
	if pin.Digest, err = required(digest, "value", "tree_digest"); err != nil {
		return nil, err
	}
	level, err := optional(digest, "level")
	if err != nil {
		return nil, err
	}
	if level != nil && *level != "" {
		pin.Level = *level
	}
	if !contains([]string{"built-in", "org", "project"}, pin.Level) {
		return nil, fmt.Errorf("tree_digest.level must be one of ('built-in', 'org', 'project'); got %s", pyjson.Repr(pin.Level))
	}
	if pin.RequiresCore, err = optional(data, "requires_core"); err != nil {
		return nil, err
	}
	if pin.Snapshot, err = optional(data, "snapshot_sha256"); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(pin.Repository, "https://") && !strings.HasPrefix(pin.Repository, "file://") {
		return nil, fmt.Errorf("repository must be an https:// or file:// URL; got %s", pyjson.Repr(pin.Repository))
	}
	if !pinSHA.MatchString(pin.Commit) {
		return nil, fmt.Errorf("commit must be a 40-hex lowercase SHA (never a tag); got %s", pyjson.Repr(pin.Commit))
	}
	if err := ValidatePackSubtree(pin.Subtree); err != nil {
		return nil, err
	}
	return pin, nil
}

const pinHeader = "# Pin lockfile: which spec-kitty-open-packs commit this pack was taken from.\n# Direction: open-packs -> core. (open-packs' own built-in.source.yaml records the\n# OPPOSITE anchor, core -> open-packs.) The tree is fetched from the pinned commit,\n# not vendored; tree_digest = clean_tree_digest(<subtree>, level=<tree_digest.level>).\n"

func WritePackPin(pin *PackPin, p string) error {
	data := jsonx.NewObject("repository", pin.Repository, "commit", pin.Commit, "subtree", pin.Subtree, "tree_digest", jsonx.NewObject("algorithm", pin.Algorithm, "value", pin.Digest, "level", pin.Level))
	if pin.RequiresCore != nil {
		data.Set("requires_core", *pin.RequiresCore)
	}
	if pin.Snapshot != nil {
		data.Set("snapshot_sha256", *pin.Snapshot)
	}
	return os.WriteFile(p, []byte(pinHeader+yamlx.EmitWith(data, yamlx.Options{Width: 4096})), 0644)
}
func PackCleanTreeDigest(root, level string) (string, error) {
	descriptor, err := readRenameMapping(filepath.Join(root, "pack.yaml"))
	if err != nil {
		return "", err
	}
	if !descriptor.Has("pack_level") {
		descriptor.Set("pack_level", level)
	}
	if message := ValidatePackDescriptor(descriptor); message != "" {
		return "", fmt.Errorf("%s", message)
	}
	files := map[string]bool{"pack.yaml": true}
	plain := func(p string) bool { info, err := os.Lstat(p); return err == nil && info.Mode().IsRegular() }
	var layout []string
	switch level {
	case "built-in":
		paths, _ := filepath.Glob(filepath.Join(root, "*.graph.yaml"))
		for _, p := range paths {
			layout = append(layout, filepath.Base(p))
		}
	case "org":
		layout = []string{"org-charter.yaml", "drg/fragment.yaml"}
	default:
		layout = []string{"graph.yaml"}
	}
	for _, rel := range layout {
		if plain(filepath.Join(root, filepath.FromSlash(rel))) {
			files[rel] = true
		}
	}
	for name := range recognisedArtifactDirs {
		dir := filepath.Join(root, name)
		_ = filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() && entry.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			if entry.IsDir() || strings.HasSuffix(entry.Name(), ".pyc") || strings.HasSuffix(entry.Name(), ".pyo") || !plain(p) {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			files[filepath.ToSlash(rel)] = true
			return nil
		})
	}
	return packDigestFiles(root, dct.SortedKeys(files), true)
}
func PackFullTreeDigest(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if entry.Name() == "__pycache__" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".pyc") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Snapshot contains unsupported symlink: %s", rel)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Snapshot contains unsupported file type: %s", rel)
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	return packDigestFiles(root, files, false)
}
func packDigestFiles(root string, files []string, normalize bool) (string, error) {
	hash := sha256.New()
	var size [8]byte
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		if normalize {
			content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
			content = bytes.ReplaceAll(content, []byte("\r"), []byte("\n"))
		}
		binary.BigEndian.PutUint64(size[:], uint64(len([]byte(rel))))
		hash.Write(size[:])
		hash.Write([]byte(rel))
		binary.BigEndian.PutUint64(size[:], uint64(len(content)))
		hash.Write(size[:])
		hash.Write(content)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func CheckPackPin(pin *PackPin, root string) (bool, error) {
	if pin.Algorithm != "clean-tree-sha256" {
		return false, fmt.Errorf("PACK_PIN_INVALID: pin for %s uses tree_digest.algorithm %s; only 'clean-tree-sha256' is supported", pin.Repository, pyjson.Repr(pin.Algorithm))
	}
	if pin.Snapshot == nil {
		return false, fmt.Errorf("PACK_PIN_INVALID: pin for %s has no 'snapshot_sha256'; run `spec-kitty charter pack vendor --pack <pack> --ref %s` to write it", pin.Repository, pin.Commit)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, nil
	}
	// Every symlink, including bytecode and directory links, invalidates a placed tree.
	symlink := false
	_ = filepath.WalkDir(root, func(_ string, e fs.DirEntry, err error) error {
		if err == nil && e.Type()&os.ModeSymlink != 0 {
			symlink = true
		}
		return nil
	})
	if symlink {
		return false, nil
	}
	clean, err := PackCleanTreeDigest(root, pin.Level)
	if err != nil || clean != pin.Digest {
		return false, nil
	}
	full, err := PackFullTreeDigest(root)
	return err == nil && full == *pin.Snapshot, nil
}
func assertPinnedCoreCompatible(pin *PackPin, root string) error {
	if pin.RequiresCore != nil {
		valid, err := pep440.Satisfies(app.Version, *pin.RequiresCore)
		if err != nil {
			return fmt.Errorf("the pin's requires_core %s is not a valid specifier: %s", pyjson.Repr(*pin.RequiresCore), err)
		}
		if !valid {
			return fmt.Errorf("incompatible requires_core: the pin requires %s but core is %s", pyjson.Repr(*pin.RequiresCore), app.Version)
		}
	}
	descriptor, err := readRenameMapping(filepath.Join(root, "pack.yaml"))
	if err == nil {
		if message := ValidatePackDescriptor(descriptor); message != "" {
			err = fmt.Errorf("%s", message)
		}
	}
	if err != nil {
		return fmt.Errorf("the fetched pack.yaml is unreadable: %s", err)
	}
	if raw, present := descriptor.Get("requires_core"); present && raw != nil {
		required, _ := raw.(string)
		valid, _ := pep440.Satisfies(app.Version, required)
		if !valid {
			return fmt.Errorf("incompatible requires_core: the fetched pack requires %s but core is %s", pyjson.Repr(required), app.Version)
		}
	}
	return nil
}
