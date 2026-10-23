package setup

// charter_pack_cutover (upgrade/migrations/m_4_0_0rc6_charter_pack_cutover.py
// and its _charter_pack_cutover_* helpers): move .kittify/doctrine to
// .kittify/charter-packs, rename the retired `doctrine` keys, reset released
// default activation lists and remove retired skill copies. runs_first.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/rtyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const (
	legacyProjectRoot = ".kittify/doctrine"
	packRootRel       = ".kittify/charter-packs"
)

func init() {
	migrationImpls["charter_pack_cutover"] = migrationImpl{
		noWorktrees: true,
		detect: func(root string) bool {
			if len(detectLegacyCharterLayout(root)) > 0 {
				return true
			}
			r, err := runCutover(root, true, nil)
			return err != nil || r.actionable()
		},
		structural: func(root string) bool {
			f := detectLegacyCharterLayout(root)
			for _, x := range f {
				if !configKeyFindings[x] {
					return true
				}
			}
			steps := []cutoverStep{stepRewriteActivationPackIDs}
			if len(f) > 0 {
				steps = []cutoverStep{stepRewriteConfigKeys, stepRewriteActivationPackIDs}
			}
			r, err := runCutover(root, true, steps)
			return err != nil || r.actionable()
		},
		canApply: func(string) (bool, string) { return ok() },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			r, err := runCutover(root, dryRun, nil)
			if err != nil {
				return migrationResult{}, err
			}
			return r.result(dryRun), nil
		},
	}
}

var configKeyFindings = map[string]bool{"legacy_org_packs_key": true, "legacy_organisation_packs_key": true,
	"legacy_governance_selection_key": true, "legacy_tracker_ownership_key": true}

// --- legacy layout detection (migration/legacy_charter_layout.py) -----------------

func governanceFilePath(root string) string {
	return filepath.Join(root, ".kittify", "charter", "governance.yaml")
}

func loadPrefiltered(p string) (bool, any) {
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) || isNotDirErr(err) {
			return true, nil
		}
		return false, nil
	}
	if !strings.Contains(string(b), "doctrine") && !strings.Contains(string(b), "organisation_packs") {
		return true, nil
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return false, nil
	}
	return true, v
}

func isNotDirErr(err error) bool { return strings.Contains(err.Error(), "not a directory") }

func legacyOrgBlock(cfg *jsonx.Object) *jsonx.Object {
	sec := cfg.GetObject("doctrine")
	if sec == nil {
		return nil
	}
	org := sec.GetObject("org")
	if org != nil && (org.Has("packs") || org.Has("local_path")) {
		return org
	}
	return nil
}

func convertibleOrgPack(e any) bool {
	o, isObj := e.(*jsonx.Object)
	if !isObj {
		return false
	}
	src := "local_path"
	if v, ok := o.Get("source"); ok {
		s, _ := v.(string)
		src = s
		if _, isS := v.(string); !isS {
			return false
		}
	}
	_, nameOK := func() (string, bool) { v, _ := o.Get("name"); s, ok := v.(string); return s, ok }()
	_, pathOK := func() (string, bool) { v, _ := o.Get("path"); s, ok := v.(string); return s, ok }()
	return nameOK && pathOK && src == "local_path"
}

func detectLegacyCharterLayout(root string) []string {
	var f []string
	lr := filepath.Join(root, filepath.FromSlash(legacyProjectRoot))
	if st, err := os.Lstat(lr); err == nil {
		if st.Mode()&os.ModeSymlink != 0 || st.IsDir() {
			f = append(f, "legacy_project_root")
		}
	} else if !os.IsNotExist(err) && !isNotDirErr(err) {
		f = append(f, "unreadable_project_root")
	}
	readable, gov := loadPrefiltered(governanceFilePath(root))
	if !readable {
		f = append(f, "unreadable_governance_file")
	} else if g, ok := gov.(*jsonx.Object); ok && g.Has("doctrine") {
		f = append(f, "legacy_governance_file")
	}
	readable, cfg := loadPrefiltered(kittify.ConfigPath(root))
	if !readable {
		f = append(f, "unreadable_config")
	} else if c, ok := cfg.(*jsonx.Object); ok {
		if legacyOrgBlock(c) != nil {
			f = append(f, "legacy_org_packs_key")
		}
		if l, isL := func() ([]any, bool) { v, _ := c.Get("organisation_packs"); l, ok := v.([]any); return l, ok }(); isL {
			for _, e := range l {
				if convertibleOrgPack(e) {
					f = append(f, "legacy_organisation_packs_key")
					break
				}
			}
		}
		if g := c.GetObject("governance"); g != nil && g.Has("doctrine") {
			f = append(f, "legacy_governance_selection_key")
		}
		if t := c.GetObject("tracker"); t != nil && t.Has("doctrine") {
			f = append(f, "legacy_tracker_ownership_key")
		}
	}
	return f
}

// --- report ------------------------------------------------------------------------

type cutoverReport struct {
	moved, rewritten, reset, keptForReview, matchesMinimal, skillsRemoved, skillsKept, errors []string
	restoreHints, preserved                                                                   []string
}

func (r *cutoverReport) actionable() bool {
	return len(r.moved)+len(r.rewritten)+len(r.reset)+len(r.skillsRemoved)+len(r.errors) > 0
}

func (r *cutoverReport) merge(o *cutoverReport) {
	r.moved = append(r.moved, o.moved...)
	r.rewritten = append(r.rewritten, o.rewritten...)
	r.reset = append(r.reset, o.reset...)
	r.keptForReview = append(r.keptForReview, o.keptForReview...)
	r.matchesMinimal = append(r.matchesMinimal, o.matchesMinimal...)
	r.skillsRemoved = append(r.skillsRemoved, o.skillsRemoved...)
	r.skillsKept = append(r.skillsKept, o.skillsKept...)
	r.errors = append(r.errors, o.errors...)
	r.restoreHints = append(r.restoreHints, o.restoreHints...)
	r.preserved = append(r.preserved, o.preserved...)
}

func strsAny(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

func (r *cutoverReport) result(dryRun bool) migrationResult {
	d := jsonx.NewObject("errors", strsAny(r.errors), "kept_for_review", strsAny(r.keptForReview), "matches_minimal", strsAny(r.matchesMinimal),
		"moved", strsAny(r.moved), "reset", strsAny(r.reset), "rewritten", strsAny(r.rewritten), "skills_kept", strsAny(r.skillsKept),
		"skills_removed", strsAny(r.skillsRemoved))
	ch := []string{jsonx.Dumps(d, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true})}
	for _, v := range []struct {
		l          []string
		verb, dryV string
	}{{r.moved, "Moved", "Would move"}, {r.rewritten, "Rewrote", "Would rewrite"}, {r.reset, "Reset", "Would reset"}, {r.skillsRemoved, "Removed skill", "Would remove skill"}} {
		p := v.verb
		if dryRun {
			p = v.dryV
		}
		for _, l := range v.l {
			ch = append(ch, p+" "+l)
		}
	}
	var warn []string
	for _, v := range []struct {
		l         []string
		lab, dryL string
	}{{r.keptForReview, "Kept for review", "Would keep for review"}, {r.matchesMinimal, "Matches preset minimal, kept", "Would keep (matches preset minimal)"},
		{r.skillsKept, "Kept edited skill copy", "Would keep edited skill copy"}} {
		p := v.lab
		if dryRun {
			p = v.dryL
		}
		for _, l := range v.l {
			warn = append(warn, p+": "+l)
		}
	}
	warn = append(warn, r.restoreHints...)
	return migrationResult{Success: len(r.errors) == 0, Changes: ch, Errors: append([]string(nil), r.errors...), Warnings: warn,
		ManualReview: len(warn) > 0, Preserved: append([]string(nil), r.preserved...)}
}

// --- run ---------------------------------------------------------------------------

type cutoverRun struct {
	root   string
	dryRun bool
	rep    *cutoverReport
}

type cutoverStep func(r *cutoverRun) error

func (r *cutoverRun) legacy() string {
	return filepath.Join(r.root, filepath.FromSlash(legacyProjectRoot))
}
func (r *cutoverRun) pack() string { return filepath.Join(r.root, filepath.FromSlash(packRootRel)) }
func (r *cutoverRun) rel(p string) string {
	if x, ok := relPosix(p, r.root); ok {
		return x
	}
	return filepath.ToSlash(p)
}
func (r *cutoverRun) write(p, text string) {
	if !r.dryRun {
		os.WriteFile(p, []byte(text), filePermOr(p, 0o600))
	}
}

func unreadable(root, p string, err error) error {
	rel, ok := relPosix(p, root)
	if !ok {
		rel = filepath.ToSlash(p)
	}
	return failClosed("%s could not be read (%s); fix the file, then run `spec-kitty upgrade` again", rel, err)
}

func runCutover(root string, dryRun bool, steps []cutoverStep) (*cutoverReport, error) {
	f := detectLegacyCharterLayout(root)
	for _, x := range f {
		switch x {
		case "unreadable_project_root":
			return nil, failClosed(".kittify/doctrine could not be inspected (permission or I/O error); check its permissions, then run `spec-kitty upgrade` again")
		case "unreadable_config":
			return nil, failClosed(".kittify/config.yaml is not readable YAML; fix the file, then run `spec-kitty upgrade` again")
		case "unreadable_governance_file":
			return nil, failClosed(".kittify/charter/governance.yaml is not readable YAML; fix the file, then run `spec-kitty upgrade` again")
		}
	}
	if steps == nil {
		steps = []cutoverStep{stepPreflight, stepMoveProjectRoot, stepRewritePathReferences, stepRewriteConfigKeys, stepRewriteActivationPackIDs,
			stepResetActivationLists, stepRemoveRetiredSkillCopies}
	}
	r := &cutoverRun{root: root, dryRun: dryRun, rep: &cutoverReport{}}
	for _, s := range steps {
		if err := s(r); err != nil {
			return nil, err
		}
		if len(r.rep.errors) > 0 {
			break
		}
	}
	return r.rep, nil
}

func legacyEntries(lr string) []string {
	var out []string
	filepath.Walk(lr, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == lr {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().IsRegular() {
			rel, _ := filepath.Rel(lr, p)
			out = append(out, rel)
		}
		return nil
	})
	return sortedPaths(out)
}

func sameEntry(src, cp string) bool {
	if isSymlink(src) {
		if !isSymlink(cp) {
			return false
		}
		a, _ := os.Readlink(src)
		b, _ := os.Readlink(cp)
		return a == b
	}
	if isSymlink(cp) || !isFile(cp) || !isFile(src) {
		return false
	}
	a, _ := os.ReadFile(src)
	b, _ := os.ReadFile(cp)
	return string(a) == string(b)
}

func stepPreflight(r *cutoverRun) error {
	lr := r.legacy()
	if isSymlink(lr) {
		r.rep.errors = append(r.rep.errors, r.rel(lr)+" is a symlink; replace it with the directory it points to, then run `spec-kitty upgrade` again")
		return nil
	}
	if !isDir(lr) {
		return nil
	}
	for _, rel := range legacyEntries(lr) {
		t := filepath.Join(r.pack(), rel)
		if lexists(t) && !sameEntry(filepath.Join(lr, rel), t) {
			r.rep.errors = append(r.rep.errors, fmt.Sprintf("%s collides with %s (different content); reconcile the two, then run `spec-kitty upgrade` again",
				r.rel(filepath.Join(lr, rel)), r.rel(t)))
		}
	}
	return nil
}

func stepMoveProjectRoot(r *cutoverRun) error {
	lr := r.legacy()
	if !isDir(lr) || isSymlink(lr) {
		return nil
	}
	for _, rel := range legacyEntries(lr) {
		src, t := filepath.Join(lr, rel), filepath.Join(r.pack(), rel)
		line := r.rel(src) + " -> " + r.rel(t)
		if !r.dryRun {
			if !lexists(t) {
				os.MkdirAll(filepath.Dir(t), 0o755)
				var err error
				if isSymlink(src) {
					l, _ := os.Readlink(src)
					err = os.Symlink(l, t)
				} else {
					err = copyFile2(src, t)
				}
				if err != nil {
					r.rep.errors = append(r.rep.errors, fmt.Sprintf("%s could not be moved (%s); close any program holding it, then run `spec-kitty upgrade` again", r.rel(src), pyOSError(err)))
					return nil
				}
			}
			if !sameEntry(src, t) {
				r.rep.errors = append(r.rep.errors, fmt.Sprintf("%s could not be moved (the copy at %s does not match (not package-owned)); close any program holding it, then run `spec-kitty upgrade` again", r.rel(src), r.rel(t)))
				return nil
			}
			os.Remove(src)
		}
		r.rep.moved = append(r.rep.moved, line)
	}
	if r.dryRun {
		return nil
	}
	empty := true
	filepath.Walk(lr, func(p string, info os.FileInfo, err error) error {
		if err == nil && p != lr && (info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			empty = false
		}
		return nil
	})
	if !empty {
		r.rep.errors = append(r.rep.errors, fmt.Sprintf("%s still holds files; move them to %s, then run `spec-kitty upgrade` again", r.rel(lr), r.rel(r.pack())))
		return nil
	}
	if err := os.RemoveAll(lr); err != nil {
		r.rep.errors = append(r.rep.errors, fmt.Sprintf("%s could not be removed (%s); run `spec-kitty upgrade` again", r.rel(lr), pyOSError(err)))
	}
	return nil
}

func rewriteLegacyPath(v any) (string, bool) {
	s, isS := v.(string)
	if !isS {
		return "", false
	}
	n := s
	if strings.HasPrefix(s, `.kittify\doctrine\`) {
		n = strings.ReplaceAll(s, `\`, "/")
	}
	if strings.HasPrefix(n, legacyProjectRoot+"/") {
		return packRootRel + "/" + strings.TrimPrefix(n, legacyProjectRoot+"/"), true
	}
	return "", false
}

func mentionsLegacy(t string) bool {
	return strings.Contains(t, legacyProjectRoot+"/") || strings.Contains(t, `.kittify\doctrine\`)
}

func rewriteValues(node any, rel, trail string, lines *[]string) {
	switch t := node.(type) {
	case *jsonx.Object:
		for _, kv := range t.Pairs() {
			ct := kv.Key
			if trail != "" {
				ct = trail + "." + kv.Key
			}
			if m, ok := rewriteLegacyPath(kv.Value); ok {
				t.Set(kv.Key, m)
				*lines = append(*lines, fmt.Sprintf("%s: %s %s -> %s", rel, ct, kv.Value, m))
			} else {
				rewriteValues(kv.Value, rel, ct, lines)
			}
		}
	case []any:
		for i, v := range t {
			ct := fmt.Sprintf("%s[%d]", trail, i)
			if m, ok := rewriteLegacyPath(v); ok {
				t[i] = m
				*lines = append(*lines, fmt.Sprintf("%s: %s %s -> %s", rel, ct, v, m))
			} else {
				rewriteValues(v, rel, ct, lines)
			}
		}
	}
}

func stepRewritePathReferences(r *cutoverRun) error {
	// synthesis manifest (artifacts[].path)
	sm := filepath.Join(r.root, ".kittify", "charter", "synthesis-manifest.yaml")
	if b, err := os.ReadFile(sm); err == nil && mentionsLegacy(string(b)) {
		m, err := yamlx.ParseObject(b)
		if err != nil {
			return unreadable(r.root, sm, err)
		}
		changed := false
		arts, _ := m.Get("artifacts")
		for _, a := range func() []any { l, _ := arts.([]any); return l }() {
			ao, isObj := a.(*jsonx.Object)
			if !isObj {
				continue
			}
			if mv, ok := rewriteLegacyPath(ao.GetString("path")); ok {
				r.rep.rewritten = append(r.rep.rewritten, fmt.Sprintf("%s: artifacts[].path %s -> %s", r.rel(sm), ao.GetString("path"), mv))
				ao.Set("path", mv)
				changed = true
			}
		}
		if changed {
			fields := &jsonx.Object{}
			for _, kv := range m.Pairs() {
				if kv.Key != "manifest_hash" {
					fields.Set(kv.Key, kv.Value)
				}
			}
			m.Set("manifest_hash", sha256HexBytes(canonicalYAMLBytes(fields)))
			r.write(sm, string(canonicalYAMLBytes(m)))
		}
	}
	// provenance sidecars
	var sidecars []string
	sidecars = append(sidecars, globFiles(filepath.Join(r.root, ".kittify", "charter", "provenance"), "*.yaml")...)
	for _, rt := range []string{r.legacy(), r.pack()} {
		if isDir(rt) && !isSymlink(rt) {
			var found []string
			filepath.Walk(rt, func(p string, info os.FileInfo, err error) error {
				if err == nil && strings.HasSuffix(p, ".yaml") && filepath.Base(filepath.Dir(p)) == ".provenance" {
					found = append(found, p)
				}
				return nil
			})
			sidecars = append(sidecars, sortedPaths(found)...)
		}
	}
	for _, p := range sidecars {
		b, err := os.ReadFile(p)
		if err != nil || !mentionsLegacy(string(b)) {
			continue
		}
		d, err := yamlx.Parse(b)
		if err != nil {
			return unreadable(r.root, p, err)
		}
		var lines []string
		rewriteValues(d, r.rel(p), "", &lines)
		if len(lines) > 0 {
			r.rep.rewritten = append(r.rep.rewritten, lines...)
			r.write(p, yamlx.EmitWith(d, yamlx.Options{Width: 4096}))
		}
	}
	// skills manifest source_ref
	smp := agents.SkillsManifestPath(r.root)
	if b, err := os.ReadFile(smp); err == nil && mentionsLegacy(string(b)) {
		d, err := jsonx.DecodeObject(b)
		if err != nil {
			return unreadable(r.root, smp, err)
		}
		changed := false
		for _, e := range d.GetList("entries") {
			eo, isObj := e.(*jsonx.Object)
			if !isObj {
				continue
			}
			if mv, ok := rewriteLegacyPath(eo.GetString("source_ref")); ok {
				r.rep.rewritten = append(r.rep.rewritten, fmt.Sprintf("%s: entries[].source_ref %s -> %s", r.rel(smp), eo.GetString("source_ref"), mv))
				eo.Set("source_ref", mv)
				changed = true
			}
		}
		if changed {
			r.write(smp, jsonx.Dumps(d, jsonx.Pretty)+"\n")
		}
	}
	// .gitignore
	gp := filepath.Join(r.root, ".gitignore")
	text, present, err := readGitignoreNoFollow(gp)
	if err != nil {
		r.rep.keptForReview = append(r.rep.keptForReview, fmt.Sprintf(".gitignore could not be read safely (%s); check it for .kittify/doctrine rules by hand", err))
		return nil
	}
	if !present || !strings.Contains(text, legacyProjectRoot) {
		return nil
	}
	ruleRe := regexp.MustCompile(regexp.QuoteMeta(legacyProjectRoot) + `(/|$|\s)`)
	lines := pySplitLinesKeep(text)
	existing := map[string]bool{}
	for _, l := range lines {
		existing[strings.TrimSpace(l)] = true
	}
	var out, changes []string
	for _, l := range lines {
		rule := strings.TrimSpace(l)
		if rule == "" || strings.HasPrefix(rule, "#") || !ruleRe.MatchString(rule) {
			out = append(out, l)
			continue
		}
		nr := ruleRe.ReplaceAllString(rule, packRootRel+"$1")
		if existing[nr] {
			changes = append(changes, fmt.Sprintf("dropped rule %s (%s is already present)", rule, nr))
			continue
		}
		out = append(out, strings.Replace(l, rule, nr, 1))
		existing[nr] = true
		changes = append(changes, fmt.Sprintf("rule %s -> %s", rule, nr))
	}
	out, added := ensurePackYAMLAllowRule(out)
	if added {
		changes = append(changes, "added "+charterPackYAMLAllow+" (keeps the project pack.yaml tracked under the charter-pack ignore rule)")
	}
	if len(changes) == 0 {
		return nil
	}
	if !r.dryRun {
		if err := os.WriteFile(gp, []byte(strings.Join(out, "")), filePermOr(gp, 0o644)); err != nil {
			r.rep.errors = append(r.rep.errors, fmt.Sprintf(".gitignore could not be written (%s); make it writable, then run `spec-kitty upgrade` again", pyOSError(err)))
			return nil
		}
	}
	for _, c := range changes {
		r.rep.rewritten = append(r.rep.rewritten, ".gitignore: "+c)
	}
	return nil
}

// --- key renames ---------------------------------------------------------------------

type keyEdit struct {
	mapping  *jsonx.Object
	old, new string
	label    string
	path     []string // parent path (for the text rename)
	listItem bool
}

func (e keyEdit) drops() bool { return e.mapping.Has(e.new) }

func compactJSONValue(v any) string {
	t := jsonx.Dumps(v, jsonx.Options{Indent: -1, Compact: true, EnsureASCII: true})
	if len(t) > 160 {
		t = t[:157] + "..."
	}
	return t
}

func (e keyEdit) describe(rel string) string {
	nl := e.new
	if i := strings.LastIndex(e.label, "."); i >= 0 {
		nl = e.label[:i] + "." + e.new
	}
	if e.drops() {
		v, _ := e.mapping.Get(e.old)
		return fmt.Sprintf("%s: dropped %s (%s is already present; the canonical value wins; dropped value: %s)", rel, e.label, nl, compactJSONValue(v))
	}
	return fmt.Sprintf("%s: %s -> %s", rel, e.label, nl)
}

func renameInMapping(e keyEdit) {
	if e.drops() {
		e.mapping.Delete(e.old)
		return
	}
	out := &jsonx.Object{}
	for _, kv := range e.mapping.Pairs() {
		if kv.Key == e.old {
			out.Set(e.new, kv.Value)
		} else {
			out.Set(kv.Key, kv.Value)
		}
	}
	*e.mapping = *out
}

// renameInText renames keys in place in the source text (ruamel's line/col
// key positions): a key line under its parent block, quoted or bare.
func renameInText(text string, edits []keyEdit) (string, bool) {
	lines := pySplitLinesKeep(text)
	for _, e := range edits {
		done := false
		depthIndent := -1
		parentIdx := 0
		for i, l := range lines {
			trim := strings.TrimLeft(l, " ")
			ind := len(l) - len(trim)
			if parentIdx < len(e.path) {
				if (depthIndent < 0 && ind == 0 || ind > depthIndent) && keyLineMatches(trim, e.path[parentIdx]) {
					depthIndent = ind
					parentIdx++
				}
				continue
			}
			body := trim
			if e.listItem && strings.HasPrefix(body, "- ") {
				body = strings.TrimLeft(body[2:], " ")
				ind = len(l) - len(body)
			}
			if (len(e.path) == 0 && ind == 0 || len(e.path) > 0 && ind > depthIndent) && keyLineMatches(body, e.old) {
				for _, q := range []string{"", `"`, "'"} {
					tok := q + e.old + q
					if strings.HasPrefix(body, tok) {
						col := len(l) - len(body)
						lines[i] = l[:col] + q + e.new + q + l[col+len(tok):]
						done = true
						break
					}
				}
				if done && !e.listItem {
					break
				}
				if done && e.listItem {
					break
				}
			}
		}
		if !done {
			return "", false
		}
	}
	return strings.Join(lines, ""), true
}

func keyLineMatches(body, key string) bool {
	for _, q := range []string{"", `"`, "'"} {
		if strings.HasPrefix(body, q+key+q) {
			rest := strings.TrimLeft(body[len(q+key+q):], " \t")
			if strings.HasPrefix(rest, ":") {
				return true
			}
		}
	}
	return false
}

func (r *cutoverRun) applyKeyEdits(p, text string, data *jsonx.Object, edits []keyEdit) {
	if len(edits) == 0 {
		return
	}
	rel := r.rel(p)
	var desc []string
	for _, e := range edits {
		desc = append(desc, e.describe(rel))
	}
	drops := false
	for _, e := range edits {
		drops = drops || e.drops()
	}
	edited, ok := "", false
	if !drops {
		edited, ok = renameInText(text, edits)
	}
	if !ok {
		for _, e := range edits {
			renameInMapping(e)
		}
		edited = yamlx.EmitWith(data, yamlx.Options{Width: 4096})
	}
	r.rep.rewritten = append(r.rep.rewritten, desc...)
	r.write(p, edited)
}

func mappingEdit(data *jsonx.Object, section, old, nw, label string) []keyEdit {
	m := data
	var path []string
	if section != "" {
		m = data.GetObject(section)
		path = []string{section}
	}
	if m != nil && m.Has(old) {
		return []keyEdit{{mapping: m, old: old, new: nw, label: label, path: path}}
	}
	return nil
}

// roundTripOver is the migration's _dump of the edited round-trip map
// (YAML() with preserve_quotes, width 4096): the edits in cfg are applied
// over the file text as loaded, so comments, quote styles and untouched
// entries are kept.
func roundTripOver(text string, cfg *jsonx.Object, snap *rtyaml.Snapshot) string {
	if doc, err := rtyaml.LoadString(text); err == nil {
		doc.Apply(cfg, snap)
		if out, err := doc.DumpWidth(4096); err == nil {
			return out
		}
	}
	return yamlx.EmitWith(cfg, yamlx.Options{Width: 4096})
}

func readYAMLText(root, p string) (string, *jsonx.Object, bool, error) {
	if !isFile(p) {
		return "", nil, false, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", nil, false, unreadable(root, p, err)
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return "", nil, false, unreadable(root, p, err)
	}
	o, _ := v.(*jsonx.Object)
	return string(b), o, true, nil
}

var (
	legacyKeyLineRe         = regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]+)?['"]?(?:doctrine|doctrine_pack_id)['"]?[ \t]*:`)
	topLevelLegacyKeyLineRe = regexp.MustCompile(`(?m)^['"]?doctrine['"]?[ \t]*:`)
)

func stepRewriteConfigKeys(r *cutoverRun) error {
	p := kittify.ConfigPath(r.root)
	text, cfg, present, err := readYAMLText(r.root, p)
	if err != nil {
		return err
	}
	if present && cfg != nil && (strings.Contains(text, "doctrine") || strings.Contains(text, "organisation_packs")) {
		rel := r.rel(p)
		snap := rtyaml.Snap(cfg) // the CommentedMap as loaded, before the in-place edits
		changed := r.rewriteLegacyOrg(cfg, rel)
		changed = r.rewriteOrganisationPacks(cfg, rel) || changed
		edits := mappingEdit(cfg, "governance", "doctrine", "charter", "governance.doctrine")
		edits = append(edits, mappingEdit(cfg, "tracker", "doctrine", "ownership", "tracker.doctrine")...)
		if !changed {
			r.applyKeyEdits(p, text, cfg, edits)
		} else {
			for _, e := range edits {
				r.rep.rewritten = append(r.rep.rewritten, e.describe(rel))
				renameInMapping(e)
			}
			r.write(p, roundTripOver(text, cfg, snap))
		}
	}
	// governance selection in charter.yaml and governance.yaml
	cy := r.charterYAML()
	if text, d, present, err := readYAMLText(r.root, cy); err != nil {
		return err
	} else if present && d != nil && legacyKeyLineRe.MatchString(text) {
		r.applyKeyEdits(cy, text, d, mappingEdit(d, "governance", "doctrine", "charter", "governance.doctrine"))
	}
	gp := governanceFilePath(r.root)
	if text, d, present, err := readYAMLText(r.root, gp); err != nil {
		return err
	} else if present && d != nil && topLevelLegacyKeyLineRe.MatchString(text) {
		r.applyKeyEdits(gp, text, d, mappingEdit(d, "", "doctrine", "charter", "doctrine"))
	}
	ap := answersPath(r.root)
	if text, d, present, err := readYAMLText(r.root, ap); err != nil {
		return err
	} else if present && d != nil && topLevelLegacyKeyLineRe.MatchString(text) {
		r.applyKeyEdits(ap, text, d, mappingEdit(d, "", "doctrine", "charter", "doctrine"))
	}
	return nil
}

func (r *cutoverRun) charterYAML() string {
	cfg := loadYAMLMappingSafe(kittify.ConfigPath(r.root))
	if v, ok := cfg.Get("charter"); ok {
		if s, isS := v.(string); isS && strings.TrimSpace(s) != "" {
			if filepath.IsAbs(s) {
				return s
			}
			return filepath.Join(r.root, s)
		}
	}
	return charterYAMLPath(r.root)
}

func singlePackName(local any) string {
	base := ""
	if s, ok := local.(string); ok && s != "" {
		base = filepath.Base(strings.ReplaceAll(s, `\`, "/"))
	}
	name := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(base), "-"), "-")
	if name == "" || name == "default" {
		name = "org"
	}
	return name
}

func placeCanonicalOrg(cfg, org *jsonx.Object, anchor string) bool {
	sec, ok := cfg.Get("charter_packs")
	if !ok {
		out := &jsonx.Object{}
		placed := false
		for _, kv := range cfg.Pairs() {
			if kv.Key == anchor && !placed {
				out.Set("charter_packs", jsonx.NewObject("org", org))
				placed = true
			}
			out.Set(kv.Key, kv.Value)
		}
		if !placed {
			out.Set("charter_packs", jsonx.NewObject("org", org))
		}
		*cfg = *out
		return true
	}
	so, isObj := sec.(*jsonx.Object)
	if !isObj {
		return false
	}
	so.Set("org", org)
	return true
}

const notAMapping = "charter_packs is not a mapping; make it one by hand, then run `spec-kitty upgrade` again"

func (r *cutoverRun) rewriteLegacyOrg(cfg *jsonx.Object, rel string) bool {
	org := legacyOrgBlock(cfg)
	if org == nil {
		return false
	}
	label := "doctrine.org (single-pack form)"
	if org.Has("packs") {
		label = "doctrine.org.packs"
	}
	sec := cfg.GetObject("doctrine")
	canonical := func() bool { s := cfg.GetObject("charter_packs"); return s != nil && s.GetObject("org") != nil }()
	switch {
	case canonical:
		v, _ := sec.Get("org")
		sec.Delete("org")
		r.rep.rewritten = append(r.rep.rewritten, fmt.Sprintf("%s: dropped %s (charter_packs.org is already present; the canonical value wins; dropped value: %s)", rel, label, compactJSONValue(v)))
	case org.Has("packs"):
		if !placeCanonicalOrg(cfg, org, "doctrine") {
			r.rep.keptForReview = append(r.rep.keptForReview, fmt.Sprintf("%s: %s kept (%s)", rel, label, notAMapping))
			return false
		}
		sec.Delete("org")
		r.rep.rewritten = append(r.rep.rewritten, fmt.Sprintf("%s: %s -> charter_packs.org.packs", rel, label))
	default:
		entry := jsonx.NewObject("name", singlePackName(func() any { v, _ := org.Get("local_path"); return v }()))
		for _, k := range []string{"local_path", "subdir", "source_type", "url", "ref"} {
			if v, ok := org.Get(k); ok {
				entry.Set(k, v)
			}
		}
		if !placeCanonicalOrg(cfg, jsonx.NewObject("packs", []any{entry}), "doctrine") {
			r.rep.keptForReview = append(r.rep.keptForReview, fmt.Sprintf("%s: %s kept (%s)", rel, label, notAMapping))
			return false
		}
		for _, k := range []string{"local_path", "subdir", "source_type", "url", "ref"} {
			org.Delete(k)
		}
		if org.Len() == 0 {
			sec.Delete("org")
		}
		r.rep.rewritten = append(r.rep.rewritten, fmt.Sprintf("%s: %s -> charter_packs.org.packs entry named %s", rel, label, pyRepr(entry.GetString("name"))))
	}
	if s := cfg.GetObject("doctrine"); s != nil {
		if s.Len() == 0 {
			cfg.Delete("doctrine")
		} else {
			r.rep.keptForReview = append(r.rep.keptForReview, fmt.Sprintf("%s: doctrine: keeps %s (not a charter-pack key)", rel, strings.Join(s.Keys(), ", ")))
		}
	}
	return true
}

func (r *cutoverRun) rewriteOrganisationPacks(cfg *jsonx.Object, rel string) bool {
	raw, _ := cfg.Get("organisation_packs")
	flat, isL := raw.([]any)
	conv := false
	for _, e := range flat {
		conv = conv || convertibleOrgPack(e)
	}
	if !isL || !conv {
		if cfg.Has("organisation_packs") {
			r.rep.keptForReview = append(r.rep.keptForReview, rel+": organisation_packs kept (no entry converts to a local_path pack)")
		}
		return false
	}
	if s := cfg.GetObject("charter_packs"); s != nil && s.GetObject("org") != nil {
		cfg.Delete("organisation_packs")
		r.rep.rewritten = append(r.rep.rewritten, fmt.Sprintf("%s: dropped organisation_packs (charter_packs.org is already present; the canonical value wins; dropped value: %s)", rel, compactJSONValue(flat)))
		return true
	}
	var converted, kept []any
	var names []string
	for _, e := range flat {
		if convertibleOrgPack(e) {
			o := e.(*jsonx.Object)
			converted = append(converted, jsonx.NewObject("name", o.GetString("name"), "local_path", o.GetString("path")))
			names = append(names, o.GetString("name"))
		} else {
			kept = append(kept, e)
		}
	}
	if !placeCanonicalOrg(cfg, jsonx.NewObject("packs", converted), "organisation_packs") {
		r.rep.keptForReview = append(r.rep.keptForReview, fmt.Sprintf("%s: organisation_packs kept (%s)", rel, notAMapping))
		return false
	}
	r.rep.rewritten = append(r.rep.rewritten, fmt.Sprintf("%s: organisation_packs -> charter_packs.org.packs (%s)", rel, strings.Join(names, ", ")))
	if len(kept) > 0 {
		cfg.Set("organisation_packs", kept)
		c := fmt.Sprintf("%d entries", len(kept))
		if len(kept) == 1 {
			c = "1 entry"
		}
		r.rep.keptForReview = append(r.rep.keptForReview, fmt.Sprintf("%s: organisation_packs keeps %s with a source other than local_path", rel, c))
	} else {
		cfg.Delete("organisation_packs")
	}
	return true
}

func activationEntries(data *jsonx.Object) []*jsonx.Object {
	var out []*jsonx.Object
	for _, holder := range []*jsonx.Object{data, data.GetObject("governance")} {
		if holder == nil {
			continue
		}
		v, _ := holder.Get("activations")
		if l, isL := v.([]any); isL {
			for _, e := range l {
				if o, isObj := e.(*jsonx.Object); isObj {
					out = append(out, o)
				}
			}
		}
	}
	return out
}

func stepRewriteActivationPackIDs(r *cutoverRun) error {
	for _, p := range []string{r.charterYAML(), governanceFilePath(r.root)} {
		text, d, present, err := readYAMLText(r.root, p)
		if err != nil {
			return err
		}
		if !present || d == nil || !legacyKeyLineRe.MatchString(text) {
			continue
		}
		var edits []keyEdit
		for _, e := range activationEntries(d) {
			if e.Has("doctrine_pack_id") {
				edits = append(edits, keyEdit{mapping: e, old: "doctrine_pack_id", new: "charter_pack_id", label: "activations[].doctrine_pack_id", listItem: true})
			}
		}
		if len(edits) > 0 {
			// The text rename cannot locate list items reliably; re-dump.
			r.applyKeyEditsDump(p, d, edits)
		}
	}
	return nil
}

func (r *cutoverRun) applyKeyEditsDump(p string, data *jsonx.Object, edits []keyEdit) {
	rel := r.rel(p)
	for _, e := range edits {
		r.rep.rewritten = append(r.rep.rewritten, e.describe(rel))
	}
	b, _ := os.ReadFile(p)
	text := string(b)
	re := regexp.MustCompile(`(?m)^([ \t]*(?:-[ \t]+)?)(['"]?)doctrine_pack_id(['"]?)([ \t]*:)`)
	edited := re.ReplaceAllString(text, "${1}${2}charter_pack_id${3}${4}")
	drops := false
	for _, e := range edits {
		drops = drops || e.drops()
	}
	if drops {
		for _, e := range edits {
			renameInMapping(e)
		}
		edited = yamlx.EmitWith(data, yamlx.Options{Width: 4096})
	}
	r.write(p, edited)
}

// --- activation list resets (_charter_pack_cutover_resets) ---------------------------

func normaliseID(kindKey, raw string) string {
	if kindKey != "activated_directives" {
		return raw
	}
	if s, ok := directiveIDToStem[raw]; ok {
		return s
	}
	if regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*$`).MatchString(raw) && !regexp.MustCompile(`^DIRECTIVE_\d+$`).MatchString(raw) {
		return strings.ReplaceAll(strings.ToLower(raw), "_", "-")
	}
	return raw
}

func resetActivationKeys() []string {
	var out []string
	for _, k := range charter.ActivationYAMLKeys() {
		if k != "mission_type_activations" {
			out = append(out, k)
		}
	}
	return out
}

type resetAction struct {
	path, key, outcome, line, hint string
}

func setOf(xs []string) string {
	s := append([]string(nil), xs...)
	sort.Strings(s)
	return strings.Join(s, "\x00")
}

func absentMeaning(key string) string {
	plural := strings.TrimPrefix(key, "activated_")
	noun := strings.ReplaceAll(plural, "_", " ")
	if plural == "skills" {
		return "only the " + noun + " your org packs require are in force"
	}
	return "all " + noun + " available"
}

func planResets(root string) ([]resetAction, error) {
	cp := kittify.ConfigPath(root)
	cfg := &jsonx.Object{}
	if isFile(cp) {
		b, err := os.ReadFile(cp)
		if err != nil {
			return nil, unreadable(root, cp, err)
		}
		v, err := yamlx.Parse(b)
		if err != nil {
			return nil, unreadable(root, cp, err)
		}
		if o, ok := v.(*jsonx.Object); ok {
			cfg = o
		}
	}
	paths := []string{cp}
	if p := dct.CharterPointer(root, cfg); p != "" {
		paths = []string{p, cp}
	}
	keys := resetActivationKeys()
	var acts []resetAction
	for _, p := range paths {
		data := cfg
		if p != cp {
			data = nil
			if isFile(p) {
				b, err := os.ReadFile(p)
				if err != nil {
					return nil, unreadable(root, p, err)
				}
				v, err := yamlx.Parse(b)
				if err != nil {
					return nil, unreadable(root, p, err)
				}
				data, _ = v.(*jsonx.Object)
			}
		}
		if data == nil {
			continue
		}
		has := false
		for _, k := range keys {
			has = has || data.Has(k)
		}
		if !has {
			continue
		}
		rel := displayRel(p, root)
		for _, k := range keys {
			v, ok := data.Get(k)
			if !ok {
				continue
			}
			acts = append(acts, classifyReset(p, rel, k, v))
		}
	}
	return acts, nil
}

func classifyReset(p, rel, key string, v any) resetAction {
	l, isL := v.([]any)
	if !isL {
		return resetAction{p, key, "kept_for_review", fmt.Sprintf("%s: %s is not a list; not changed", rel, key), ""}
	}
	var ids []string
	for _, x := range l {
		s, isS := x.(string)
		if !isS {
			return resetAction{p, key, "kept_for_review", fmt.Sprintf("%s: %s holds a non-string id; not changed", rel, key), ""}
		}
		ids = append(ids, normaliseID(key, s))
	}
	uniq := map[string]bool{}
	for _, s := range ids {
		uniq[s] = true
	}
	ids = sortedKeys(uniq)
	pin := func(what string) string {
		return fmt.Sprintf("%s: %s matched %s and is now absent. To keep this exact list pinned, set %s: [%s] in %s.", rel, key, what, key, strings.Join(ids, ", "), rel)
	}
	if key == "activated_kinds" {
		switch setOf(ids) {
		case setOf(cutoverDefaultKindGate):
			return resetAction{p, key, "reset", fmt.Sprintf("%s: activated_kinds (matched the released default kind gate; now absent)", rel), pin("the released default kind gate")}
		case setOf(cutoverMinimalKindGate):
			return resetAction{p, key, "reset", fmt.Sprintf("%s: activated_kinds [directives, tactics] (the released minimal kind gate; now absent)", rel),
				fmt.Sprintf("%s: activated_kinds was [directives, tactics], the kind gate of the released `minimal` preset; it switched every other kind off, which was a defect, so it is now absent (every kind available). To gate kinds again, set activated_kinds in %s.", rel, rel)}
		}
		return resetAction{p, key, "kept_for_review", fmt.Sprintf("%s: activated_kinds customised; not changed", rel), ""}
	}
	if len(l) == 0 {
		m := absentMeaning(key)
		return resetAction{p, key, "reset", fmt.Sprintf("%s: %s was [] (nothing active); now absent (%s)", rel, key, m),
			fmt.Sprintf("%s: %s was [] (nothing active); it is now absent (%s). To switch the kind off again, set %s: [] in %s.", rel, key, m, key, rel)}
	}
	for _, snap := range cutoverDefaultSnapshots[key] {
		if setOf(snap) == setOf(ids) {
			return resetAction{p, key, "reset", fmt.Sprintf("%s: %s (matched a released default list; now absent, every built-in available)", rel, key), pin("a released default list")}
		}
	}
	for _, snap := range cutoverMinimalSnapshots[key] {
		if setOf(snap) == setOf(ids) {
			return resetAction{p, key, "matches_minimal", fmt.Sprintf("%s: %s matches preset minimal; not changed", rel, key), ""}
		}
	}
	return resetAction{p, key, "kept_for_review", fmt.Sprintf("%s: %s customised; not changed", rel, key), ""}
}

func stepResetActivationLists(r *cutoverRun) error {
	md := kittify.LoadMetadata(r.root)
	if md != nil && md.HasMigration("charter_pack_cutover") {
		return nil
	}
	acts, err := planResets(r.root)
	if err != nil {
		return err
	}
	byFile := map[string][]string{}
	var order []string
	for _, a := range acts {
		switch a.outcome {
		case "reset":
			r.rep.reset = append(r.rep.reset, a.line)
			if _, seen := byFile[a.path]; !seen {
				order = append(order, a.path)
			}
			byFile[a.path] = append(byFile[a.path], a.key)
		case "matches_minimal":
			r.rep.matchesMinimal = append(r.rep.matchesMinimal, a.line)
		default:
			r.rep.keptForReview = append(r.rep.keptForReview, a.line)
		}
		if a.hint != "" {
			r.rep.restoreHints = append(r.rep.restoreHints, a.hint)
		}
	}
	if r.dryRun {
		return nil
	}
	for _, p := range order {
		b, err := os.ReadFile(p)
		if err != nil {
			r.rep.errors = append(r.rep.errors, fmt.Sprintf("%s could not be written (%s); fix the file, then run `spec-kitty upgrade` again", displayRel(p, r.root), pyOSError(err)))
			continue
		}
		d, _ := yamlx.ParseObject(b)
		if d == nil {
			d = &jsonx.Object{}
		}
		onlyKeys := true
		rm := map[string]bool{}
		for _, k := range byFile[p] {
			rm[k] = true
		}
		for _, k := range d.Keys() {
			onlyKeys = onlyKeys && rm[k]
		}
		var out string
		if onlyKeys {
			out = "{}\n"
		} else {
			for k := range rm {
				d.Delete(k)
			}
			out = charter.RenderYAMLDocument(b, d, 4096)
		}
		os.WriteFile(p, []byte(out), filePermOr(p, 0o644))
	}
	return nil
}

// --- retired skill copies (_charter_pack_cutover_skills) -------------------------------

func stepRemoveRetiredSkillCopies(r *cutoverRun) error {
	shipped := map[string]bool{}
	for _, s := range agents.Skills() {
		shipped[s.Name] = true
	}
	var names []string
	for _, n := range removedSkillNames {
		if !shipped[n] {
			names = append(names, n)
		}
	}
	ac, err := kittify.LoadAgentConfig(r.root)
	if err != nil {
		return failClosed("%s; fix the file, then run `spec-kitty upgrade` again", err)
	}
	var roots []string
	seen := map[string]bool{}
	for _, a := range ac.Available {
		for _, rt := range agents.SkillConfigs[a].Roots {
			n := strings.TrimRight(rt, "/")
			if !seen[n] {
				seen[n] = true
				roots = append(roots, n)
			}
		}
	}
	var settled []string
	for _, rt := range roots {
		base := filepath.Join(r.root, filepath.FromSlash(rt))
		if len(names) == 0 || !isDir(base) {
			continue
		}
		for _, n := range names {
			p := filepath.Join(base, n)
			if !lexists(p) {
				continue
			}
			rel := r.rel(p)
			pr := anyProver(manifestProver(true, true), frozenTreeHashProver(shippedSkillHashes[n]))
			v, err := guardDestructiveRemoval(p, r.root, pr, true, "", r.dryRun)
			if err != nil {
				r.rep.errors = append(r.rep.errors, fmt.Sprintf("%s could not be removed (%s); close any program holding it, then run `spec-kitty upgrade` again", rel, pyOSError(err)))
				continue
			}
			if v.owned {
				reason := "package-owned (canonical_content)"
				if proof := pr(p, r.root); proof != nil {
					reason = "package-owned (" + proof.kind + ")"
				}
				r.rep.skillsRemoved = append(r.rep.skillsRemoved, fmt.Sprintf("%s (%s; %s)", rel, n, reason))
			} else {
				r.rep.skillsKept = append(r.rep.skillsKept, fmt.Sprintf("%s (%s; edited or not a shipped copy, left in place and no longer managed by spec-kitty; remove it by hand once you no longer need it)", rel, n))
				r.rep.preserved = append(r.rep.preserved, rel)
			}
			settled = append(settled, rel)
		}
	}
	if len(settled) > 0 && !r.dryRun {
		if m := agents.LoadSkillsManifest(r.root); m != nil {
			var kept []agents.SkillEntry
			for _, e := range m.Entries {
				drop := false
				for _, s := range settled {
					drop = drop || strings.HasPrefix(e.InstalledPath, s+"/")
				}
				if !drop {
					kept = append(kept, e)
				}
			}
			if len(kept) != len(m.Entries) {
				m.Entries = kept
				agents.SaveSkillsManifest(r.root, m, clock.NowUTCISO())
			}
		}
	}
	return nil
}

func frozenTreeHashProver(hashes map[string]string) prover {
	return func(p, root string) *ownershipProof {
		if len(hashes) == 0 || isSymlink(p) || !isDir(p) {
			return nil
		}
		found := map[string]string{}
		bad := false
		filepath.Walk(p, func(q string, info os.FileInfo, err error) error {
			if err != nil || q == p {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				bad = true
				return nil
			}
			if info.Mode().IsRegular() {
				b, _ := os.ReadFile(q)
				rel, _ := filepath.Rel(p, q)
				found[filepath.ToSlash(rel)] = "sha256:" + sha256Hex(b)
			}
			return nil
		})
		if bad || len(found) != len(hashes) {
			return nil
		}
		for k, h := range hashes {
			if found[k] != "sha256:"+h {
				return nil
			}
		}
		rel, _ := relPosix(p, root)
		return &ownershipProof{"canonical_content", "shipped-skill-hash:" + rel}
	}
}
