package setup

// Ports of the 3.2.6+ / 4.0 upgrade migrations (charter activation fold,
// runtime-state cutover, gitignore narrowing, 4.0 retirements, packs).

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func init() {
	migrationImpls["consolidate_charter_bundle_fold"] = mConsolidateCharterBundle()
	migrationImpls["runtime_state_backfill"] = mRuntimeStateBackfill()
	migrationImpls["3_3_1_context_sources_consolidation"] = mContextSourcesConsolidation()
	migrationImpls["4_0_0_retired_hosted_target"] = mRetiredHostedTarget()
	migrationImpls["4_0_0rc5_heal_run_index_paths"] = mHealRunIndexPaths()
	migrationImpls["4.0.0rc5_retire_bundled_dashboard"] = mRetireBundledDashboard()
	migrationImpls["4_0_0rc5_single_branch_code_lanes_restamp"] = mSingleBranchRestamp()
	migrationImpls["4.0.0rc7_pack_level_backfill"] = mPackLevelBackfill()
	migrationImpls["4.0.0rc7_track_charter_pack_yaml"] = mTrackCharterPackYAML()
}

func agentsProtectedEntries() []string { return agents.ProtectedEntries() }

func gitignoreMarker() string { return agents.GitignoreMarker }

// gitPathIgnored is is_gitignore_path_ignored: git check-ignore verdict.
func gitPathIgnored(root, rel string) bool {
	r, err := gitx.Exec(root, nil, nil, "check-ignore", "-q", "--", rel)
	return err == nil && r.Code == 0
}

// --- consolidate_charter_bundle_fold -----------------------------------------------

var legacyBundleFiles = []string{"governance.yaml", "directives.yaml", "metadata.yaml", "references.yaml"}

func charterDir(root string) string { return filepath.Join(root, ".kittify", "charter") }

func legacyBundlePresent(root string) bool {
	for _, n := range legacyBundleFiles {
		if pathExists(filepath.Join(charterDir(root), n)) {
			return true
		}
	}
	return false
}

func configHasActivation(cfg *jsonx.Object) bool {
	if cfg == nil {
		return false
	}
	for _, k := range charter.ActivationYAMLKeys() {
		if cfg.Has(k) {
			return true
		}
	}
	return false
}

func loadYAMLMappingSafe(p string) *jsonx.Object {
	b, err := os.ReadFile(p)
	if err != nil {
		return &jsonx.Object{}
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return &jsonx.Object{}
	}
	return o
}

// mergeDefaults overlays data onto defaults (pydantic model_validate for
// the nested governance models, then model_dump).
func mergeDefaults(defaults, data *jsonx.Object) *jsonx.Object {
	out := defaults.Clone()
	for _, kv := range data.Pairs() {
		if d := out.GetObject(kv.Key); d != nil {
			if o, isObj := kv.Value.(*jsonx.Object); isObj {
				out.Set(kv.Key, mergeDefaults(d, o))
				continue
			}
		}
		if kv.Value != nil {
			out.Set(kv.Key, kv.Value)
		}
	}
	return out
}

func composeCharterYAML(root string, cfg *jsonx.Object) (*jsonx.Object, error) {
	cd := charterDir(root)
	gov := loadYAMLMappingSafe(filepath.Join(cd, "governance.yaml"))
	dirs := loadYAMLMappingSafe(filepath.Join(cd, "directives.yaml"))
	refs := loadYAMLMappingSafe(filepath.Join(cd, "references.yaml"))
	if gov.Has("doctrine") {
		return nil, fmt.Errorf("%s still carries the legacy top-level 'doctrine' key (governance.doctrine); the charter-pack cutover migration rewrites it to 'charter' and runs first. Re-run `spec-kitty upgrade` so it can, then retry.", filepath.Join(cd, "governance.yaml"))
	}
	directives := mergeDefaults(jsonx.NewObject("directives", []any{}), dirs)
	langs := []any{}
	if l, ok := refs.Get("languages"); ok && l != nil {
		if xs, isL := l.([]any); isL {
			for _, x := range xs {
				langs = append(langs, pyStrValue(x))
			}
		}
	}
	refList := []any{}
	if l, ok := refs.Get("references"); ok && l != nil {
		if xs, isL := l.([]any); isL {
			refList = xs
		}
	}
	str := func(k string) string {
		v, _ := refs.Get(k)
		if v == nil {
			return ""
		}
		return pyStrValue(v)
	}
	doc := jsonx.NewObject("schema_version", "2.0.0",
		"governance", mergeDefaults(charter.DefaultGovernance(), gov),
		"directives", directives,
		"catalog", jsonx.NewObject("mission", str("mission"), "template_set", str("template_set"), "languages", langs, "references", refList),
		"overrides", &jsonx.Object{},
		"metadata", jsonx.NewObject("generated_at", clock.Now().UTC().Format("2006-01-02T15:04:05Z"), "bundle_schema_version", 2))
	for _, k := range charter.ActivationYAMLKeys() {
		if v, ok := cfg.Get(k); ok {
			doc.Set(k, v)
		}
	}
	return doc, nil
}

func writeCharterYAMLDoc(p string, doc *jsonx.Object) error {
	var before []byte
	if b, err := os.ReadFile(p); err == nil {
		before = b
	}
	out := charter.RenderYAMLDocument(before, doc, 4096)
	if before != nil && string(before) == out {
		return nil
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(p, []byte(out), mode); err != nil {
		return err
	}
	return os.Chmod(p, mode)
}

func mConsolidateCharterBundle() migrationImpl {
	detect := func(root string) bool {
		return legacyBundlePresent(root) || configHasActivation(loadYAMLMappingSafe(kittify.ConfigPath(root)))
	}
	return migrationImpl{
		noWorktrees: true,
		detect:      detect,
		canApply: func(root string) (bool, string) {
			if detect(root) {
				return ok()
			}
			return false, "charter.yaml already composed; no legacy bundle file or config-embedded activation left to migrate"
		},
		apply: func(root string, dryRun bool) migrationResult {
			cd := charterDir(root)
			cy := filepath.Join(cd, "charter.yaml")
			legacy := legacyBundlePresent(root)
			cfg, _ := loadConfigRoundTrip(root)
			if cfg == nil {
				cfg = &jsonx.Object{}
			}
			hasAct := configHasActivation(cfg)
			if !legacy && !hasAct {
				return migrationResult{Success: true}
			}
			if dryRun {
				var s []string
				if legacy {
					if pathExists(cy) {
						s = append(s, "dry-run: would delete stale legacy files under "+cd)
					} else {
						s = append(s, "dry-run: would compose "+cy+" from legacy bundle files")
					}
					for _, n := range legacyBundleFiles {
						if pathExists(filepath.Join(cd, n)) {
							s = append(s, "dry-run: would delete "+filepath.Join(cd, n))
						}
					}
				}
				if hasAct {
					s = append(s, "dry-run: would relocate activated_* keys from config.yaml onto charter.yaml and add the charter: pointer")
				}
				return migrationResult{Success: true, Changes: s}
			}
			var ch []string
			if pathExists(cy) {
				if hasAct {
					b, _ := os.ReadFile(cy)
					doc, _ := yamlx.ParseObject(b)
					if doc == nil {
						doc = &jsonx.Object{}
					}
					for _, k := range charter.ActivationYAMLKeys() {
						if v, ok := cfg.Get(k); ok {
							doc.Set(k, v)
						}
					}
					writeCharterYAMLDoc(cy, doc)
					ch = append(ch, "Relocated activation keys onto existing "+cy)
				}
			} else {
				doc, err := composeCharterYAML(root, cfg)
				if err != nil {
					return migrationResult{Errors: []string{err.Error()}}
				}
				writeCharterYAMLDoc(cy, doc)
				ch = append(ch, fmt.Sprintf("Composed %s from legacy bundle + config activation", cy))
			}
			for _, n := range legacyBundleFiles {
				p := filepath.Join(cd, n)
				v, err := guardDestructiveRemoval(p, root, defaultCanonicalProver(), false, filepath.Join(root, ".kittify"), false)
				if err != nil || !lexists(p) && v.diagnostic == "Skipped "+displayRel(p, root)+"; path is absent" {
					continue
				}
				if v.backupPath != "" {
					ch = append(ch, fmt.Sprintf("Preserved divergent %s to %s, then retired it", p, v.backupPath))
				} else {
					ch = append(ch, "Deleted "+p)
				}
			}
			if hasAct || !cfg.Has("charter") {
				for _, k := range charter.ActivationYAMLKeys() {
					cfg.Delete(k)
				}
				ptr := cy
				if r, err := filepath.Rel(resolvePath(root), resolvePath(cy)); err == nil && !strings.HasPrefix(r, "..") {
					ptr = filepath.ToSlash(r)
				}
				cfg.Set("charter", ptr)
				os.MkdirAll(filepath.Dir(kittify.ConfigPath(root)), 0o755)
				writeConfigRoundTrip(root, cfg)
				ch = append(ch, "Updated .kittify/config.yaml: removed activated_* keys, added charter: pointer")
			}
			return migrationResult{Success: true, Changes: ch}
		},
	}
}

// --- runtime_state_backfill --------------------------------------------------------

func mRuntimeStateBackfill() migrationImpl {
	detect := func(root string) bool {
		for _, f := range featureDirs(root) {
			if migrate.MissionNeedsCutover(f) {
				return true
			}
		}
		return false
	}
	return migrationImpl{
		noWorktrees: true,
		detect:      detect,
		canApply: func(root string) (bool, string) {
			if detect(root) {
				return ok()
			}
			return false, "no un-migrated mission runtime state found under kitty-specs/"
		},
		apply: func(root string, dryRun bool) migrationResult {
			missions := featureDirs(root)
			if len(missions) == 0 {
				return migrationResult{Success: true}
			}
			var results []migrate.CutoverResult
			for _, f := range missions {
				r := migrate.CutoverMission(f, dryRun)
				results = append(results, r)
				failed := r.Error != nil || (!dryRun && r.Verify != nil && !r.Verify.OK)
				if failed {
					detail := "unknown cutover failure"
					switch {
					case r.Verify != nil && !r.Verify.OK:
						detail = strings.Join(r.Verify.Mismatches, "; ")
						if detail == "" {
							detail = "verify reported a failure with no detail"
						}
					case r.Error != nil:
						detail = *r.Error
					}
					return migrationResult{Errors: []string{fmt.Sprintf("Runtime-state cutover aborted: mission %s failed fail-closed verify (%s); run `spec-kitty migrate backfill-runtime-state --mission %s --dry-run` to inspect",
						pyRepr(r.Slug), detail, r.Slug)}}
				}
			}
			seeded, flipped := 0, 0
			for _, r := range results {
				seeded += r.Seeded
				if r.Flipped {
					flipped++
				}
			}
			if seeded == 0 {
				return migrationResult{Success: true}
			}
			if dryRun {
				return migrationResult{Success: true, Changes: []string{fmt.Sprintf("dry-run: would seed %d event(s) across %d mission(s) scanned", seeded, len(results))}}
			}
			return migrationResult{Success: true, Changes: []string{fmt.Sprintf("Seeded %d event(s) and flipped %d of %d mission(s) scanned", seeded, flipped, len(results))}}
		},
	}
}

// --- 3_3_1 context sources consolidation ----------------------------------------------

const migratedRationale = "Migrated from the retired context-sources surface (mission doctrine-drg-silent-drop-boundary-01M0PE7E)."

var contextMergeMap = [][3]string{{"directives", "directive-references", "code"}, {"tactics", "tactic-references", "id"},
	{"toolguides", "toolguide-references", "id"}, {"styleguides", "styleguide-references", "id"}}

func profilePaths(root string) []string {
	var out []string
	skip := map[string]bool{".git": true, ".venv": true, "node_modules": true, ".worktrees": true}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && skip[info.Name()] && p != root {
			return filepath.SkipDir
		}
		if strings.HasSuffix(info.Name(), ".agent.yaml") {
			out = append(out, p)
		}
		return nil
	})
	return sortedPaths(out)
}

func consolidateContextSources(data *jsonx.Object) (bool, int, []string, error) {
	cs := data.GetObject("context-sources")
	if cs == nil {
		return false, 0, nil, nil
	}
	merged := 0
	for _, m := range contextMergeMap {
		raw, _ := cs.Get(m[0])
		ids, _ := raw.([]any)
		if len(ids) == 0 {
			continue
		}
		refs, _ := func() ([]any, bool) { v, _ := data.Get(m[1]); l, ok := v.([]any); return l, ok }()
		seen := map[string]bool{}
		for _, r := range refs {
			if o, isObj := r.(*jsonx.Object); isObj {
				if v, ok := o.Get(m[2]); ok && v != nil {
					seen[pyStrValue(v)] = true
				}
			}
		}
		n := 0
		for _, x := range ids {
			id := pyStrValue(x)
			if seen[id] {
				continue
			}
			var ref *jsonx.Object
			if m[2] == "code" {
				ref = jsonx.NewObject("code", id, "name", id, "rationale", migratedRationale)
			} else {
				ref = jsonx.NewObject("id", id, "rationale", migratedRationale)
			}
			refs = append(refs, ref)
			seen[id] = true
			n++
		}
		if n > 0 {
			data.Set(m[1], refs)
			merged += n
		}
	}
	var dropped []string
	for _, k := range []string{"doctrine-layers", "additional"} {
		raw, _ := cs.Get(k)
		if xs, isL := raw.([]any); isL {
			for _, x := range xs {
				dropped = append(dropped, k+":"+pyStrValue(x))
			}
		}
	}
	data.Delete("context-sources")
	return true, merged, dropped, nil
}

func mContextSourcesConsolidation() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, p := range profilePaths(root) {
				b, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				if d, err := yamlx.ParseObject(b); err == nil && d != nil && d.GetObject("context-sources") != nil {
					return true
				}
			}
			return false
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch, errs []string
			for _, p := range profilePaths(root) {
				b, err := os.ReadFile(p)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to read %s: %s", p, pyOSError(err)))
					continue
				}
				d, err := yamlx.ParseObject(b)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to read %s: %v", p, err))
					continue
				}
				if d == nil || d.GetObject("context-sources") == nil {
					continue
				}
				changed, merged, dropped, _ := consolidateContextSources(d)
				if !changed {
					continue
				}
				rel, _ := filepath.Rel(root, p)
				summary := fmt.Sprintf("merged %d refs, dropped 0 non-edge entries", merged)
				if len(dropped) > 0 {
					sort.Strings(dropped)
					summary = fmt.Sprintf("merged %d refs, dropped %d non-edge entries (%s)", merged, len(dropped), strings.Join(dropped, ", "))
				}
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would consolidate context-sources in %s (%s)", rel, summary))
					continue
				}
				if err := os.WriteFile(p, []byte(yamlx.Emit(d)), 0o666&^umask()); err != nil {
					errs = append(errs, fmt.Sprintf("Failed to write %s: %s", rel, pyOSError(err)))
					continue
				}
				ch = append(ch, fmt.Sprintf("Consolidated context-sources in %s (%s)", rel, summary))
			}
			if len(ch) == 0 && len(errs) == 0 {
				ch = append(ch, "No consumer profile authored context-sources")
			}
			return newResult(ch, nil, errs)
		},
	}
}

// --- 3.2.6rc3 narrow cursor gitignore ----------------------------------------------------

var (
	narrowCursorEntries = []string{".cursor/rules/spec-kitty.mdc", ".cursor/commands/", ".cursor/skills/"}
	blanketCursorRe     = regexp.MustCompile(`^(/|\*\*/)?\.cursor(/|/\*{1,2})?$`)
)

func knownManagedEntries() map[string]bool {
	out := map[string]bool{".cursor/": true}
	for _, e := range agentsProtectedEntries() {
		out[e] = true
	}
	return out
}

func isBlanketCursor(line string) bool {
	s := strings.TrimSpace(line)
	return s != "" && !strings.HasPrefix(s, "#") && blanketCursorRe.MatchString(s)
}

func classifyBlanketCursor(content string) ([]int, []string) {
	var owned []int
	var unowned []string
	in := false
	lines := pySplitLinesKeep(content)
	known := knownManagedEntries()
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if s == gitignoreMarker() {
			in = true
			continue
		}
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "#") {
			in = false
			continue
		}
		if isBlanketCursor(l) {
			hist := i > 0 && i < len(lines)-1 && s == ".cursor/" && strings.TrimSpace(lines[i-1]) == ".gemini/" && strings.TrimSpace(lines[i+1]) == ".qwen/"
			if in && hist {
				owned = append(owned, i)
			} else {
				unowned = append(unowned, l)
			}
			continue
		}
		if in && !known[s] {
			in = false
		}
	}
	return owned, unowned
}

// readGitignoreNoFollow is gitignore_manager.read_gitignore_text.
func readGitignoreNoFollow(p string) (string, bool, error) {
	if isSymlink(p) {
		return "", false, failClosed("Refusing to read symlinked .gitignore: %s", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, failClosed("Could not safely open .gitignore: %s", pyOSError(err))
	}
	return strings.TrimPrefix(string(b), "\ufeff"), true, nil
}

func missingNarrowEntries(p string) []string {
	c, present, _ := readGitignoreNoFollow(p)
	if !present {
		return append([]string(nil), narrowCursorEntries...)
	}
	have := map[string]bool{}
	for _, l := range pySplitLines(c) {
		have[strings.TrimSpace(l)] = true
	}
	var out []string
	for _, e := range narrowCursorEntries {
		if !have[e] {
			out = append(out, e)
		}
	}
	return out
}

// --- 4.0.0 retired hosted target ----------------------------------------------------------

func runtimeConfigTOML() string { return filepath.Join(project.GlobalSpecKitty(), "config.toml") }

func retiredServerURL() (string, map[string]map[string]string) {
	b, err := os.ReadFile(runtimeConfigTOML())
	if err != nil {
		return "", nil
	}
	tables := parseSimpleTOML(string(b))
	if tables == nil {
		return "", nil
	}
	v, ok := tables["sync"]["server_url"]
	if !ok {
		return "", nil
	}
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || strings.ToLower(u.Hostname()) != "app.spec-kitty.ai" {
		return "", nil
	}
	return v, tables
}

func mRetiredHostedTarget() migrationImpl {
	return migrationImpl{
		noWorktrees: true,
		detect:      func(string) bool { v, _ := retiredServerURL(); return v != "" },
		canApply: func(string) (bool, string) {
			if v, _ := retiredServerURL(); v != "" {
				return ok()
			}
			return false, "no retired first-party server_url in config.toml (already canonical, unset, or custom)"
		},
		apply: func(_ string, dryRun bool) migrationResult {
			v, _ := retiredServerURL()
			if v == "" {
				return migrationResult{Success: true, Changes: []string{"no retired first-party server_url in config.toml (already canonical, unset, or custom)"}}
			}
			if dryRun {
				return migrationResult{Success: true, Changes: []string{"would remove retired config.toml sync.server_url " + v}}
			}
			b, _ := os.ReadFile(runtimeConfigTOML())
			os.WriteFile(runtimeConfigTOML(), []byte(removeTOMLKey(string(b), "sync", "server_url")), 0o644)
			return migrationResult{Success: true, Changes: []string{"removed retired config.toml sync.server_url " + v}}
		},
	}
}

// parseSimpleTOML reads [table] key = "string" pairs (enough to find
// sync.server_url); nil on a syntax it does not understand.
func parseSimpleTOML(s string) map[string]map[string]string {
	out := map[string]map[string]string{"": {}}
	cur := ""
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			cur = strings.TrimSpace(strings.Trim(t, "[]"))
			if out[cur] == nil {
				out[cur] = map[string]string{}
			}
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			return nil
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[cur][strings.TrimSpace(k)] = v
	}
	return out
}

func removeTOMLKey(s, table, key string) string {
	var out []string
	cur := ""
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			cur = strings.TrimSpace(strings.Trim(t, "[]"))
		}
		if cur == table {
			if k, _, ok := strings.Cut(t, "="); ok && strings.TrimSpace(k) == key {
				continue
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// --- 4.0.0rc5 heal run index paths ---------------------------------------------------------

func runIndexPath(root string) string {
	return filepath.Join(root, ".kittify", "runtime", "feature-runs.json")
}

func healableRunEntries(root string) []string {
	b, err := os.ReadFile(runIndexPath(root))
	if err != nil {
		return nil
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil
	}
	var out []string
	for _, kv := range o.Pairs() {
		e, isObj := kv.Value.(*jsonx.Object)
		if !isObj {
			continue
		}
		if rd := e.GetString("run_dir"); rd != "" && (filepath.IsAbs(rd) || isWindowsAbs(rd)) {
			out = append(out, kv.Key)
		}
	}
	return out
}

func isWindowsAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func mHealRunIndexPaths() migrationImpl {
	return migrationImpl{
		noWorktrees: true,
		detect:      func(root string) bool { return len(healableRunEntries(root)) > 0 },
		canApply: func(root string) (bool, string) {
			if len(healableRunEntries(root)) > 0 {
				return ok()
			}
			return false, "no absolute run_dir found in .kittify/runtime/feature-runs.json"
		},
		apply: func(root string, dryRun bool) migrationResult {
			b, err := os.ReadFile(runIndexPath(root))
			if err != nil {
				return migrationResult{Success: true}
			}
			o, err := jsonx.DecodeObject(b)
			if err != nil {
				return migrationResult{Success: true}
			}
			var ch []string
			for _, kv := range o.Pairs() {
				e, isObj := kv.Value.(*jsonx.Object)
				if !isObj {
					continue
				}
				rd := e.GetString("run_dir")
				if rd == "" || !(filepath.IsAbs(rd) || isWindowsAbs(rd)) {
					continue
				}
				nv := rd
				if rel, err := filepath.Rel(root, rd); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
					nv = filepath.ToSlash(rel)
				} else {
					anchor := e.GetString("run_id")
					if anchor == "" {
						anchor = filepath.Base(strings.ReplaceAll(rd, "\\", "/"))
					}
					nv = ".kittify/runtime/runs/" + anchor
				}
				ch = append(ch, fmt.Sprintf("feature-runs.json[%s].run_dir: %s -> %s", kv.Key, rd, nv))
				e.Set("run_dir", nv)
			}
			if !dryRun && len(ch) > 0 {
				os.WriteFile(runIndexPath(root), []byte(jsonx.Dumps(o, jsonx.Options{Indent: 2, SortKeys: true})+"\n"), 0o644)
			}
			return migrationResult{Success: true, Changes: ch}
		},
	}
}

// --- 4.0.0rc5 retire bundled dashboard -------------------------------------------------------

var dashboardCommandFiles = []string{"spec-kitty.dashboard.md", "spec-kitty.dashboard.prompt.md", "spec-kitty.dashboard.toml"}

func retiredDashboardPaths(root string) ([]string, error) {
	dirs, err := migrationAgentDirs(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range dirs {
		ad := filepath.Join(root, d.root, d.subdir)
		if !isDir(ad) {
			continue
		}
		for _, n := range dashboardCommandFiles {
			if f := filepath.Join(ad, n); isFile(f) {
				out = append(out, f)
			}
		}
	}
	for _, rel := range []string{".kittify/.dashboard", ".kittify/preflight-warning.json"} {
		if f := filepath.Join(root, filepath.FromSlash(rel)); isFile(f) {
			out = append(out, f)
		}
	}
	return out, nil
}

func dashboardProver() prover {
	return anyProver(defaultCanonicalProver(), managedPathProver([]string{".kittify/.dashboard", ".kittify/preflight-warning.json"}))
}

func mRetireBundledDashboard() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			paths, err := retiredDashboardPaths(root)
			if err != nil {
				return false, err
			}
			pr := dashboardProver()
			for _, p := range paths {
				if pr(p, root) != nil {
					return true, nil
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			paths, err := retiredDashboardPaths(root)
			if err != nil {
				return migrationResult{}, err
			}
			var ch, warn, errs, preserved []string
			pr := dashboardProver()
			for _, p := range paths {
				rel, _ := filepath.Rel(root, p)
				rel = filepath.ToSlash(rel)
				v, err := guardDestructiveRemoval(p, root, pr, false, "", dryRun)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to remove %s: %s", rel, pyOSError(err)))
					continue
				}
				if v.owned {
					verb := "Removed"
					if dryRun {
						verb = "Would remove"
					}
					ch = append(ch, verb+" retired "+rel)
				} else {
					warn = append(warn, v.diagnostic)
					preserved = append(preserved, p)
				}
			}
			if len(ch) == 0 && len(errs) == 0 && len(preserved) == 0 {
				ch = append(ch, "No retired dashboard files present")
			}
			r := newResult(ch, warn, errs)
			r.Preserved = preserved
			return r, nil
		},
	}
}

// --- 4.0.0rc5 single-branch restamp ----------------------------------------------------------

func mSingleBranchRestamp() migrationImpl {
	detect := func(root string) bool {
		for _, r := range migrate.RestampSingleBranch(root, true, nil) {
			if r.Action == "restamped" {
				return true
			}
		}
		return false
	}
	return migrationImpl{
		noWorktrees: true,
		detect:      detect,
		canApply: func(root string) (bool, string) {
			if detect(root) {
				return ok()
			}
			return false, "no single_branch mission with a code lane found"
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, errs []string
			for _, r := range migrate.RestampSingleBranch(root, dryRun, nil) {
				switch r.Action {
				case "restamped":
					ch = append(ch, r.Slug+": topology single_branch -> lanes")
				case "error":
					errs = append(errs, r.Slug+": "+derefStr(r.Reason))
				}
			}
			return newResult(ch, nil, errs)
		},
	}
}

// --- 4.0.0rc7 pack level / pack.yaml tracking ------------------------------------------------

func projectPackRoot(root string) string { return filepath.Join(root, ".kittify", "charter-packs") }

func packNeedsBackfill(packRoot string) (bool, error) {
	d := filepath.Join(packRoot, "pack.yaml")
	if !isFile(d) {
		return true, nil
	}
	b, err := os.ReadFile(d)
	if err != nil {
		return false, err
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return false, err
	}
	o, isObj := v.(*jsonx.Object)
	if !isObj {
		return false, nil
	}
	return !o.Has("pack_level"), nil
}

func mPackLevelBackfill() migrationImpl {
	roots := func(root string) []string {
		if pr := projectPackRoot(root); isDir(pr) {
			return []string{pr}
		}
		return nil
	}
	return migrationImpl{
		detect: func(root string) bool {
			for _, r := range roots(root) {
				n, err := packNeedsBackfill(r)
				if err != nil || n {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(root) {
				return false, "Project path does not exist: " + root
			}
			for _, r := range roots(root) {
				if _, err := packNeedsBackfill(r); err != nil {
					return false, err.Error()
				}
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch []string
			for _, r := range roots(root) {
				n, err := packNeedsBackfill(r)
				if err != nil {
					return migrationResult{Errors: []string{err.Error()}}
				}
				if !n {
					continue
				}
				d := filepath.Join(r, "pack.yaml")
				disp := displayRel(d, root)
				has := isFile(d)
				if dryRun {
					verb := "write"
					if has {
						verb = "add pack_level: project to"
					}
					ch = append(ch, fmt.Sprintf("Would %s %s", verb, disp))
					continue
				}
				if has {
					b, _ := os.ReadFile(d)
					o, _ := yamlx.ParseObject(b)
					if o == nil {
						o = &jsonx.Object{}
					}
					o.Set("pack_level", "project")
					os.WriteFile(d, []byte(yamlx.Emit(o)), 0o600)
					ch = append(ch, "Added pack_level: project to "+disp)
				} else {
					name := filepath.Base(filepath.Dir(filepath.Dir(r)))
					if name == "" || name == "." || name == "/" {
						name = "charter-packs"
					}
					o := jsonx.NewObject("pack_id", ulid.New(), "name", name, "pack_level", "project")
					os.WriteFile(d, []byte(yamlx.Emit(o)), 0o600)
					ch = append(ch, "Wrote "+disp+" with pack_level: project")
				}
			}
			return migrationResult{Success: true, Changes: ch}
		},
	}
}

const charterPackYAMLAllow = "!.kittify/charter-packs/pack.yaml"

func needsPackYAMLAllowRule(text string) bool {
	rules := map[string]bool{}
	for _, l := range pySplitLines(text) {
		rules[strings.TrimSpace(l)] = true
	}
	blanket := rules[".kittify/charter-packs/**"] || rules["/.kittify/charter-packs/**"]
	allow := rules[charterPackYAMLAllow] || rules["!/.kittify/charter-packs/pack.yaml"]
	return blanket && !allow
}

func ensurePackYAMLAllowRule(lines []string) ([]string, bool) {
	var stripped []string
	for _, l := range lines {
		stripped = append(stripped, strings.TrimSpace(l))
	}
	bi := -1
	for i, r := range stripped {
		if r == ".kittify/charter-packs/**" || r == "/.kittify/charter-packs/**" {
			bi = i
		}
		if r == charterPackYAMLAllow || r == "!/.kittify/charter-packs/pack.yaml" {
			return lines, false
		}
	}
	if bi < 0 {
		return lines, false
	}
	allow := charterPackYAMLAllow
	if stripped[bi] == "/.kittify/charter-packs/**" {
		allow = "!/.kittify/charter-packs/pack.yaml"
	}
	at := bi + 1
	for at < len(stripped) && (strings.HasPrefix(stripped[at], "!.kittify/charter-packs/") || strings.HasPrefix(stripped[at], "!/.kittify/charter-packs/")) {
		at++
	}
	nl := "\n"
	for _, l := range lines {
		if strings.HasSuffix(l, "\r\n") {
			nl = "\r\n"
			break
		}
	}
	out := append([]string(nil), lines...)
	if a := at - 1; a >= 0 && out[a] != "" && !strings.HasSuffix(out[a], "\n") && !strings.HasSuffix(out[a], "\r") {
		out[a] += nl
	}
	out = append(out[:at], append([]string{allow + nl}, out[at:]...)...)
	return out, true
}

func mTrackCharterPackYAML() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			t, present, err := readGitignoreNoFollow(filepath.Join(root, ".gitignore"))
			if err != nil {
				return true
			}
			return present && needsPackYAMLAllowRule(t)
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(root) {
				return false, "Project path does not exist: " + root
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			p := filepath.Join(root, ".gitignore")
			t, present, err := readGitignoreNoFollow(p)
			if err != nil {
				return migrationResult{Success: true, Warnings: []string{fmt.Sprintf(".gitignore could not be read safely (%s); add %s by hand if needed", err, charterPackYAMLAllow)}}
			}
			if !present || !needsPackYAMLAllowRule(t) {
				return migrationResult{Success: true}
			}
			lines, changed := ensurePackYAMLAllowRule(pySplitLinesKeep(t))
			if !changed {
				return migrationResult{Success: true}
			}
			if dryRun {
				return migrationResult{Success: true, Changes: []string{"Would add " + charterPackYAMLAllow + " to .gitignore"}}
			}
			mode := os.FileMode(0o666) &^ umask()
			if st, err := os.Stat(p); err == nil {
				mode = st.Mode().Perm()
			}
			if err := os.WriteFile(p, []byte(strings.Join(lines, "")), mode); err != nil {
				return migrationResult{Errors: []string{fmt.Sprintf(".gitignore could not be written (%s); make it writable, then run `spec-kitty upgrade` again", pyOSError(err))}}
			}
			r := migrationResult{Success: true, Changes: []string{"Added " + charterPackYAMLAllow + " to .gitignore"}}
			if gitPathIgnored(root, ".kittify/charter-packs/pack.yaml") {
				r.Warnings = append(r.Warnings, ".kittify/charter-packs/pack.yaml is still ignored by another .gitignore rule; check your .gitignore so it can be committed")
			}
			return r
		},
	}
}

func derefStr(s *string) string {
	if s == nil {
		return "None"
	}
	return *s
}
