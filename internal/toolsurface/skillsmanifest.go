package toolsurface

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

var skillEntryFields = []string{"skill_name", "source_file", "installed_path", "installation_class", "agent_key", "content_hash", "installed_at",
	"delivery_mode", "origin", "source_ref", "source_hash"}

var skillEntryRequired = []string{"skill_name", "source_file", "installed_path", "installation_class", "agent_key", "content_hash", "installed_at"}

func pyList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "'" + n + "'"
	}
	switch len(q) {
	case 1:
		return q[0]
	case 2:
		return q[0] + " and " + q[1]
	}
	return strings.Join(q[:len(q)-1], ", ") + ", and " + q[len(q)-1]
}

// parseSkillsManifest is skills.manifest._parse_manifest with its error texts.
func parseSkillsManifest(raw string) (*agents.SkillsManifest, error) {
	v, err := pyjson.Loads(raw)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, errors.New("Invalid skills manifest shape")
	}
	entriesV, has := o.Get("entries")
	if has {
		if _, isList := entriesV.([]any); !isList {
			return nil, errors.New("Invalid skills manifest shape")
		}
	}
	for _, k := range o.Keys() {
		switch k {
		case "version", "created_at", "updated_at", "spec_kitty_version", "entries":
		default:
			return nil, errors.New("Unknown skills manifest fields")
		}
	}
	m := &agents.SkillsManifest{}
	known := map[string]bool{}
	for _, f := range skillEntryFields {
		known[f] = true
	}
	seen := map[string]bool{}
	for _, x := range o.GetList("entries") {
		eo, ok := x.(*jsonx.Object)
		if !ok {
			return nil, errors.New("Invalid skills manifest entry")
		}
		for _, kv := range eo.Pairs() {
			if _, isStr := kv.Value.(string); !isStr {
				return nil, errors.New("Invalid skills manifest entry")
			}
		}
		for _, kv := range eo.Pairs() {
			if !known[kv.Key] {
				return nil, fmt.Errorf("Invalid skills manifest: ManagedFileEntry.__init__() got an unexpected keyword argument '%s'", kv.Key)
			}
		}
		var missing []string
		for _, f := range skillEntryRequired {
			if !eo.Has(f) {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			word := "argument"
			if len(missing) > 1 {
				word = "arguments"
			}
			return nil, fmt.Errorf("Invalid skills manifest: ManagedFileEntry.__init__() missing %d required positional %s: %s", len(missing), word, pyList(missing))
		}
		e := agents.SkillEntry{SkillName: eo.GetString("skill_name"), SourceFile: strings.ReplaceAll(eo.GetString("source_file"), `\`, "/"),
			InstalledPath: strings.ReplaceAll(eo.GetString("installed_path"), `\`, "/"), InstallationClass: eo.GetString("installation_class"),
			AgentKey: eo.GetString("agent_key"), ContentHash: eo.GetString("content_hash"), InstalledAt: eo.GetString("installed_at"), DeliveryMode: "copy"}
		if eo.Has("delivery_mode") {
			e.DeliveryMode = eo.GetString("delivery_mode")
		}
		origin := "builtin"
		if eo.Has("origin") {
			origin = eo.GetString("origin")
		}
		e = e.WithProvenance(origin, eo.GetString("source_ref"), eo.GetString("source_hash"))
		if e.DeliveryMode != "copy" && e.DeliveryMode != "symlink" {
			return nil, errors.New("Invalid skill delivery mode")
		}
		k := e.InstalledPath + "\x00" + e.AgentKey
		if seen[k] {
			return nil, errors.New("Duplicate skills manifest owner")
		}
		seen[k] = true
		m.Entries = append(m.Entries, e)
	}
	if vv, ok := o.Get("version"); ok {
		if n, isInt := vv.(int); !isInt || n != 1 {
			return nil, errors.New("Unsupported skills manifest version")
		}
	}
	for _, k := range []string{"created_at", "updated_at", "spec_kitty_version"} {
		if vv, ok := o.Get(k); ok {
			if _, isStr := vv.(string); !isStr {
				return nil, fmt.Errorf("Invalid skills manifest %s", k)
			}
		}
	}
	m.CreatedAt, m.UpdatedAt, m.SpecKittyVersion = o.GetString("created_at"), o.GetString("updated_at"), o.GetString("spec_kitty_version")
	return m, nil
}

// loadSkillsManifestStrict is load_manifest(strict=True): nil when absent.
func loadSkillsManifestStrict(root string) (*agents.SkillsManifest, error) {
	target := agents.SkillsManifestPath(root)
	st, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Invalid skills manifest: %v", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("Invalid skills manifest: Skills manifest is not a regular file: %s", target)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("Invalid skills manifest: %v", err)
	}
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, fmt.Errorf("Invalid skills manifest: %v", err)
	}
	m, err := parseSkillsManifest(s)
	if err != nil {
		if strings.HasPrefix(err.Error(), "Invalid skills manifest:") {
			return nil, err
		}
		return nil, fmt.Errorf("Invalid skills manifest: %v", err)
	}
	return m, nil
}

// preparedManifest is skills.manifest.PreparedSkillManifest.
type preparedManifest struct {
	content []byte
	changed bool
	mode    int
	before  FileState
}

// prepareSkillsManifest is skills.manifest.prepare_manifest.
func prepareSkillsManifest(m *agents.SkillsManifest, root, now string) (preparedManifest, error) {
	target := agents.SkillsManifestPath(root)
	before, err := observeNode(target)
	if err != nil {
		return preparedManifest{}, err
	}
	if before.Kind != "file" && before.Kind != "absent" {
		return preparedManifest{}, fmt.Errorf("Skills manifest is not a regular file: %s", target)
	}
	mode := 0o600
	if before.Mode >= 0 {
		mode = before.Mode
	}
	var raw []byte
	var previous *agents.SkillsManifest
	if before.Kind == "file" {
		raw, err = os.ReadFile(target)
		if err != nil {
			return preparedManifest{}, err
		}
		if previous, err = parseSkillsManifest(string(raw)); err != nil {
			return preparedManifest{}, err
		}
	}
	desired := &agents.SkillsManifest{CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, SpecKittyVersion: m.SpecKittyVersion, Entries: append([]agents.SkillEntry(nil), m.Entries...)}
	if previous != nil {
		desired.CreatedAt = previous.CreatedAt
		desired.Entries = retainEntryTimes(desired.Entries, previous)
		cmp := *desired
		cmp.UpdatedAt = previous.UpdatedAt
		if agents.RenderSkillsManifest(&cmp) == agents.RenderSkillsManifest(previous) {
			return preparedManifest{content: raw, changed: false, mode: mode, before: before}, nil
		}
	}
	prepared := now
	if prepared == "" {
		prepared = desired.UpdatedAt
	}
	if desired.CreatedAt == "" {
		desired.CreatedAt = prepared
	}
	desired.UpdatedAt = prepared
	content := []byte(agents.RenderSkillsManifest(desired))
	return preparedManifest{content: content, changed: string(content) != string(raw) || raw == nil, mode: mode, before: before}, nil
}

func retainEntryTimes(entries []agents.SkillEntry, previous *agents.SkillsManifest) []agents.SkillEntry {
	key := func(e agents.SkillEntry) string { return e.InstalledPath + "\x00" + e.AgentKey }
	old := map[string]agents.SkillEntry{}
	order := map[string]int{}
	for _, e := range previous.Entries {
		k := key(e)
		old[k] = e
		if _, ok := order[k]; !ok {
			order[k] = len(order)
		}
	}
	out := make([]agents.SkillEntry, len(entries))
	for i, e := range entries {
		if p, ok := old[key(e)]; ok && p.SkillName == e.SkillName && p.SourceFile == e.SourceFile && p.InstallationClass == e.InstallationClass {
			e.InstalledAt = p.InstalledAt
		}
		out[i] = e
	}
	sort.SliceStable(out, func(i, j int) bool {
		oi, iok := order[key(out[i])]
		oj, jok := order[key(out[j])]
		if !iok {
			oi = len(order)
		}
		if !jok {
			oj = len(order)
		}
		if oi != oj {
			return oi < oj
		}
		if out[i].InstalledPath != out[j].InstalledPath {
			return out[i].InstalledPath < out[j].InstalledPath
		}
		return out[i].AgentKey < out[j].AgentKey
	})
	return out
}

// observeNode reads one node without following a final symlink.
func observeNode(p string) (FileState, error) {
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return Absent(), nil
	}
	if err != nil {
		return FileState{}, err
	}
	mode := int(info.Mode().Perm())
	if info.Mode()&os.ModeSetuid != 0 {
		mode |= 0o4000
	}
	if info.Mode()&os.ModeSetgid != 0 {
		mode |= 0o2000
	}
	if info.Mode()&os.ModeSticky != 0 {
		mode |= 0o1000
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		t, _ := os.Readlink(p)
		return FileState{Kind: "symlink", Target: t, Mode: mode}, nil
	case info.IsDir():
		return DirOf(mode), nil
	case info.Mode().IsRegular():
		b, err := os.ReadFile(p)
		if err != nil {
			return FileState{}, err
		}
		return FileOf(Digest(b), mode), nil
	}
	return FileState{}, fmt.Errorf("Unsupported managed-skill node: %s", p)
}

// observeConfined is skill_path_observations: every lexical parent between
// root and rel must be a directory (or absent); returns the final state.
func observeConfined(root, rel string) (FileState, error) {
	for _, part := range strings.Split(rel, "/") {
		if part == ".." || part == "." {
			return FileState{}, fmt.Errorf("Unsafe managed-skill path: %s", filepath.Join(root, filepath.FromSlash(rel)))
		}
	}
	for _, parent := range parentsOf(rel) {
		st, err := observeNode(filepath.Join(root, filepath.FromSlash(parent)))
		if err != nil {
			return FileState{}, err
		}
		if st.Kind != "absent" && st.Kind != "directory" {
			return FileState{}, fmt.Errorf("Managed-skill parent is not a directory: %s", filepath.Join(root, filepath.FromSlash(parent)))
		}
	}
	return observeNode(filepath.Join(root, filepath.FromSlash(rel)))
}
