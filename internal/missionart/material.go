package missionart

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Port of specify_cli.analysis_inputs: the conservative declarative
// dependency closure a report-only analysis report records (and that its
// freshness check re-collects).

// MaterialInputError is analysis_inputs.MaterialInputError (a ValueError).
type MaterialInputError struct{ Msg string }

func (e *MaterialInputError) Error() string { return e.Msg }

// CharterConfigError is ActiveCharterConfigError (not a ValueError upstream).
type CharterConfigError struct{ Msg string }

func (e *CharterConfigError) Error() string { return e.Msg }

func merr(format string, a ...any) error { return &MaterialInputError{fmt.Sprintf(format, a...)} }

// pyJoin is pathlib's base / rel: "." and empty segments dropped, ".."
// kept (no lexical normalisation); an absolute rel replaces base.
func pyJoin(base, rel string) string {
	if strings.HasPrefix(rel, "/") {
		base = ""
	}
	parts := []string{}
	if base != "" {
		parts = append(parts, strings.TrimRight(base, "/"))
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." {
			continue
		}
		parts = append(parts, seg)
	}
	out := strings.Join(parts, "/")
	if strings.HasPrefix(rel, "/") {
		out = "/" + out
	}
	return out
}

func relParts(root, p string) ([]string, bool) {
	root = strings.TrimRight(root, "/")
	if p == root {
		return nil, true
	}
	if !strings.HasPrefix(p, root+"/") {
		return nil, false
	}
	return strings.Split(strings.TrimPrefix(p, root+"/"), "/"), true
}

func isSymlink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// safePath is analysis_inputs._safe_path.
func safePath(root, p string) error {
	parts, ok := relParts(root, p)
	if !ok {
		return merr("External mutable analysis authority is unsupported")
	}
	cursor := strings.TrimRight(root, "/")
	for _, part := range parts {
		if part == ".." {
			return merr("Analysis authority escapes repository")
		}
		cursor += "/" + part
		if isSymlink(cursor) {
			return merr("Analysis authority contains a symlink")
		}
	}
	return nil
}

// loadMapping is analysis_inputs._mapping.
func loadMapping(p string) (*jsonx.Object, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return jsonx.NewObject(), nil
		}
		return nil, err
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, merr("Malformed material input: %s", filepath.Base(p))
	}
	if v == nil {
		return jsonx.NewObject(), nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok || o == nil {
		return nil, merr("Expected mapping in %s", filepath.Base(p))
	}
	return o, nil
}

// lessParts orders paths like sorted() over pathlib.Path (segment-wise).
func lessParts(a, b string) bool {
	pa, pb := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func sortParts(xs []string) { sort.Slice(xs, func(i, j int) bool { return lessParts(xs[i], xs[j]) }) }

// mutableWPFields is migration.strip_frontmatter.MUTABLE_FIELDS.
var mutableWPFields = map[string]bool{"lane": true, "review_status": true, "reviewed_by": true, "review_feedback": true, "progress": true,
	"shell_pid": true, "assignee": true, "agent": true, "shell_pid_created_at": true, "review_artifact_override_at": true,
	"review_artifact_override_actor": true, "review_artifact_override_wp_id": true, "review_artifact_override_reason": true,
	"reviewer_shell_pid": true, "history": true}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ArtifactHashEntry is analysis_report._artifact_hash_entry (tasks.md is
// hashed checkbox-normalised).
func ArtifactHashEntry(p, root string) *jsonx.Object {
	rel := p
	if r, ok := relParts(root, p); ok {
		rel = strings.Join(r, "/")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return jsonx.NewObject("path", rel, "sha256", nil)
	}
	if filepath.Base(p) == "tasks.md" {
		return jsonx.NewObject("path", rel, "sha256", sha256Hex([]byte(NormalizeTasksMD(string(b)))))
	}
	return jsonx.NewObject("path", rel, "sha256", sha256Hex(b))
}

// materialEntry is analysis_inputs._entry.
func materialEntry(p, root, featureDir string) (*jsonx.Object, error) {
	parts, _ := relParts(root, p)
	rel := strings.Join(parts, "/")
	st, err := os.Stat(p)
	if err == nil && st.IsDir() {
		return jsonx.NewObject("path", rel, "sha256", "directory"), nil
	}
	if err != nil {
		return jsonx.NewObject("path", rel, "sha256", nil), nil
	}
	if filepath.Dir(p) == strings.TrimRight(featureDir, "/")+"/tasks" && strings.HasSuffix(p, ".md") && strings.HasPrefix(filepath.Base(p), "WP") {
		meta, body, ferr := readFrontmatter(p)
		if ferr != nil {
			return nil, merr("Invalid WP definition: %s", filepath.Base(p))
		}
		static := jsonx.NewObject()
		for _, kv := range meta.Pairs() {
			if !mutableWPFields[kv.Key] {
				static.Set(kv.Key, kv.Value)
			}
		}
		text := jsonx.Dumps(static, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true}) + "\n" + body
		return jsonx.NewObject("path", rel, "sha256", sha256Hex([]byte(text))), nil
	}
	return ArtifactHashEntry(p, root), nil
}

// readFrontmatter is FrontmatterManager().read for a WP file.
func readFrontmatter(p string) (*jsonx.Object, string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	content := strings.TrimPrefix(string(b), "\xef\xbb\xbf")
	if !strings.HasPrefix(content, "---") {
		return nil, "", fmt.Errorf("no frontmatter")
	}
	lines := strings.Split(content, "\n")
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r\n\v\f") == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return nil, "", fmt.Errorf("no closing ---")
	}
	v, err := yamlx.Parse([]byte(strings.Join(lines[1:closing], "\n")))
	if err != nil {
		return nil, "", err
	}
	meta := jsonx.NewObject()
	if v != nil {
		o, ok := v.(*jsonx.Object)
		if !ok {
			return nil, "", fmt.Errorf("not a mapping")
		}
		meta = o
	}
	if strings.HasPrefix(filepath.Base(p), "WP") && !meta.Has("dependencies") {
		meta.Set("dependencies", []any{})
	}
	return meta, strings.Join(lines[closing+1:], "\n"), nil
}

var (
	checkboxRe   = regexp.MustCompile(`(?m)^(\s*[-*]\s*)\[[ xX]\]`)
	pipeStatusRe = regexp.MustCompile(`\|(\s*)\[[ xXDP]\](\s*)\|`)
)

// NormalizeTasksMD is analysis_report._normalize_tasks_md (checkbox state
// does not change the tasks.md input hash).
func NormalizeTasksMD(t string) string {
	t = checkboxRe.ReplaceAllString(t, "${1}[ ]")
	for {
		n := pipeStatusRe.ReplaceAllString(t, "|${1}[ ]${2}|")
		if n == t {
			return n
		}
		t = n
	}
}

// HashInputNames is analysis_report._hash_inputs().
var HashInputNames = []string{"spec.md", "plan.md", "tasks.md"}

// defaultAuthorityPaths is DEFAULT_AUTHORITY_PATHS (key order).
var defaultAuthorityPaths = []string{"docs/context/", "docs/adr/3.x/"}

func references(v any, field string) []string {
	var out []string
	switch t := v.(type) {
	case *jsonx.Object:
		for _, kv := range t.Pairs() {
			if s, ok := kv.Value.(string); ok && kv.Key == field && s != "" {
				out = append(out, s)
			} else {
				out = append(out, references(kv.Value, field)...)
			}
		}
	case []any:
		for _, x := range t {
			out = append(out, references(x, field)...)
		}
	}
	return out
}

// wheelOnlyPackFiles are the packs/built-in files the wheel ships that are
// not embedded (Python package markers), with their sha256.
var wheelOnlyPackFiles = map[string]string{
	"missions/built_in_step_contracts/__init__.py": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	"toolguides/system_tools/__init__.py":          "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
}

// packageDigest is the content pin of one bundled tree (_package_inputs).
func packageDigest(sub string) string {
	rows := map[string]string{}
	base := assets.BuiltInPackRoot
	if sub != "" {
		base += "/" + sub
	}
	_ = fs.WalkDir(assets.FS, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, rerr := fs.ReadFile(assets.FS, p)
		if rerr != nil {
			return nil
		}
		rows[strings.TrimPrefix(p, base+"/")] = sha256Hex(b)
		return nil
	})
	for k, v := range wheelOnlyPackFiles {
		if sub == "" {
			rows[k] = v
		} else if strings.HasPrefix(k, sub+"/") {
			rows[strings.TrimPrefix(k, sub+"/")] = v
		}
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sortParts(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + ":" + rows[k]
	}
	return sha256Hex([]byte(strings.Join(lines, "\n")))
}

// templateSelections is _resolved_template_paths: the project-owned
// template paths and the per-kind selection identities. The identity of a
// packaged default records its in-binary asset address where upstream
// records the absolute site-packages path.
func templateSelections(root, featureDir string) ([]string, *jsonx.Object, error) {
	meta, err := loadMapping(filepath.Join(featureDir, "meta.json"))
	if err != nil {
		return nil, nil, err
	}
	mt, ok := meta.Get("mission_type")
	if !ok || mt == nil {
		return nil, jsonx.NewObject(), nil
	}
	missionType := fmt.Sprint(mt)
	sel := jsonx.NewObject()
	var paths []string
	profile := dct.MissionTypeRoster(root)[missionType]
	if profile == nil || profile.TemplateSet == nil {
		return nil, sel, nil
	}
	home, _ := filepath.Abs(project.GlobalKittify())
	for _, kind := range profile.TemplateSet.Keys() {
		name := path.Base(profile.TemplateSet.GetString(kind))
		r, ok := ResolveAssetTier(name, "templates", root, missionType)
		if !ok {
			return nil, nil, merr("Template not found: %s", name)
		}
		var address, selected string
		switch r.Tier {
		case TierGlobal, TierGlobalMission:
			if err := safePath(home, r.Path); err != nil {
				return nil, nil, err
			}
			parts, _ := relParts(home, r.Path)
			address = strings.Join(parts, "/")
			expected := "templates/" + name
			if r.Tier == TierGlobalMission {
				expected = "missions/" + missionType + "/templates/" + name
			}
			pkg, perr := assets.ReadFile("packs/built-in/missions/" + missionType + "/templates/" + name)
			if address != expected || perr != nil || string(pkg) != string(r.Content) {
				return nil, nil, merr("External mutable global template authority is unsupported")
			}
			selected = r.Path
		case TierPackageDefault:
			address = strings.TrimPrefix(r.Path, "packs/built-in/missions/")
			selected = r.Path
		default:
			if err := safePath(root, r.Path); err != nil {
				return nil, nil, err
			}
			parts, _ := relParts(root, r.Path)
			address = strings.Join(parts, "/")
			paths = append(paths, r.Path)
			selected = r.Path
		}
		identity := jsonx.NewObject("mission", missionType, "kind", kind, "tier", r.Tier, "address", address,
			"selected_path", selected, "sha256", sha256Hex(r.Content))
		digest := sha256Hex([]byte(jsonx.Dumps(identity, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true})))
		sel.Set("template-selection:"+kind, jsonx.NewObject("path", nil, "sha256", digest))
	}
	return paths, sel, nil
}

// CollectMaterialInputs is analysis_inputs.collect_material_inputs.
func CollectMaterialInputs(featureDir, root string) (*jsonx.Object, error) {
	root, _ = filepath.Abs(root)
	featureDir, _ = filepath.Abs(featureDir)
	paths := map[string]bool{}
	var include func(p string) error
	include = func(p string) error {
		if err := safePath(root, p); err != nil {
			return err
		}
		st, err := os.Stat(p)
		if err == nil && st.IsDir() {
			paths[p] = true
			entries, _ := os.ReadDir(p)
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			sort.Strings(names)
			for _, n := range names {
				if err := include(p + "/" + n); err != nil {
					return err
				}
			}
			return nil
		}
		if err == nil && !st.Mode().IsRegular() {
			return merr("Non-regular analysis authority is unsupported")
		}
		paths[p] = true
		return nil
	}
	configPath := pyJoin(root, ".kittify/config.yaml")
	if err := include(configPath); err != nil {
		return nil, err
	}
	config, err := loadMapping(configPath)
	if err != nil {
		return nil, err
	}
	charterPath := pyJoin(root, charter.CharterYAML)
	if ptr, ok := config.Get("charter"); ok {
		if s, isS := ptr.(string); isS {
			charterPath = pyJoin(root, s)
		}
	}
	if err := include(charterPath); err != nil {
		return nil, err
	}
	charterDoc, err := loadMapping(charterPath)
	if err != nil {
		return nil, err
	}
	for _, n := range append(append([]string{}, HashInputNames...), "meta.json", "wps.yaml", "tasks") {
		if err := include(pyJoin(featureDir, n)); err != nil {
			return nil, err
		}
	}
	for _, n := range []string{"missions", "overrides", "templates", "command-templates"} {
		if err := include(pyJoin(root, ".kittify/"+n)); err != nil {
			return nil, err
		}
	}
	if err := include(pyJoin(root, dct.ProjectPackRoot)); err != nil {
		return nil, err
	}
	charterDir := filepath.Dir(charterPath)
	for _, n := range []string{"charter.md", "interview/answers.yaml", "_LIBRARY"} {
		if err := include(pyJoin(charterDir, n)); err != nil {
			return nil, err
		}
	}
	declared := append([]string{}, defaultAuthorityPaths...)
	if gv, ok := charterDoc.Get("governance"); ok {
		if g, isO := gv.(*jsonx.Object); isO && g != nil {
			if g.Has("doctrine") {
				return nil, &CharterConfigError{fmt.Sprintf("%s: `governance.doctrine` is the retired selection key and is no longer read. Run `spec-kitty upgrade` to rewrite it as `governance.charter`. See docs/migrations/charter-pack-cutover.md.", charterPath)}
			}
			cfg := jsonx.NewObject()
			if cv, ok := g.Get("charter"); ok {
				co, isO := cv.(*jsonx.Object)
				if !isO || co == nil {
					return nil, merr("governance.charter must be a mapping")
				}
				cfg = co
			}
			for _, key := range []string{"authority_paths", "governance_references"} {
				v, ok := cfg.Get(key)
				if !ok {
					continue
				}
				l, isL := v.([]any)
				if !isL {
					return nil, merr("%s must be a list of paths", key)
				}
				for _, x := range l {
					s, isS := x.(string)
					if !isS {
						return nil, merr("%s must be a list of paths", key)
					}
					declared = append(declared, s)
				}
			}
		}
	}
	for _, v := range declared {
		if err := include(pyJoin(root, v)); err != nil {
			return nil, err
		}
	}
	for _, pk := range charter.LoadPackRegistry(root).Packs {
		r, err := pk.EffectiveRoot(root)
		if err != nil {
			return nil, merr("%s", err.Error())
		}
		if err := include(r); err != nil {
			return nil, err
		}
	}
	for _, v := range references(charterDoc, "local_path") {
		if err := include(pyJoin(charterDir, v)); err != nil {
			return nil, err
		}
	}
	for _, v := range references(charterDoc, "source_path") {
		if strings.HasPrefix(v, "${SPEC_KITTY_PACKS_ROOT}/") || strings.Contains(v, "://") {
			continue
		}
		if strings.Contains(v, "$") {
			return nil, merr("Unresolved external authority source is unsupported")
		}
		if err := include(pyJoin(root, v)); err != nil {
			return nil, err
		}
	}
	tplPaths, selections, err := templateSelections(root, featureDir)
	if err != nil {
		return nil, err
	}
	for _, p := range tplPaths {
		if err := include(p); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sortParts(keys)
	out := jsonx.NewObject()
	for _, p := range keys {
		parts, _ := relParts(root, p)
		e, err := materialEntry(p, root, featureDir)
		if err != nil {
			return nil, err
		}
		out.Set("material:"+strings.Join(parts, "/"), e)
	}
	if os.Getenv("SPEC_KITTY_PACKS_ROOT") != "" || os.Getenv("SPEC_KITTY_TEMPLATE_ROOT") != "" {
		return nil, merr("Environment-selected mutable package authority is unsupported")
	}
	out.Set("package:built-in", jsonx.NewObject("path", nil, "sha256", packageDigest("")))
	out.Set("package:mission-assets", jsonx.NewObject("path", nil, "sha256", packageDigest("missions")))
	for _, kv := range selections.Pairs() {
		out.Set(kv.Key, kv.Value)
	}
	return out, nil
}
