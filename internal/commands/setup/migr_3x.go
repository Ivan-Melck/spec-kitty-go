package setup

// Ports of the 3.0 – 3.2.0rc45 upgrade migrations.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func init() {
	migrationImpls["3.0.0_canonical_context"] = mCanonicalContext()
	migrationImpls["3.0.2_restore_prompt_commands"] = mRestorePromptCommands302()
	migrationImpls["3.0.3_globalize_skill_pack"] = mGlobalizeSkillPack()
	migrationImpls["3.1.1_charter_rename"] = mCharterRename()
	migrationImpls["3.1.1_direct_canonical_commands"] = mDirectCanonicalCommands()
	migrationImpls["3.1.1_event_log_merge_driver"] = mergeDriverMigration("Would install status.events.jsonl merge driver and .gitattributes entry",
		[]driverSpec{{"spec-kitty-event-log", "Spec Kitty event log union merge", "spec-kitty merge-driver-event-log %O %A %B", "kitty-specs/**/status.events.jsonl"}})
	migrationImpls["3.1.1_normalize_status_json"] = mNormalizeStatusJSON()
	migrationImpls["3.1.2_globalize_commands"] = mSafeGlobalizeCommands()
	migrationImpls["3.2.0a4_normalize_mission_lifecycle"] = mNormalizeMissionLifecycle()
	migrationImpls["3.2.0a4_safe_globalize_commands"] = mSafeGlobalizeCommands()
	migrationImpls["3.2.0rc28_github_diff_attributes"] = mGitHubDiffAttributes()
	migrationImpls["3.2.0rc30_fix_runtime_next_result_default"] = skillMarkerReplacement("spec-kitty-runtime-next",
		[]string{"Defaults to `success` if omitted."}, "Cannot locate canonical SKILL.md for runtime-next", "All runtime-next skill files already up to date")
	migrationImpls["3.2.0rc35_activate_builtin_mission_types"] = mActivateBuiltinMissionTypes()
	migrationImpls["3.2.0rc35_charter_bundle_v2"] = mCharterBundleV2()
	migrationImpls["3.2.0rc35_charter_manifest_defaults_repair"] = mCharterManifestDefaultsRepair()
	migrationImpls["3.2.0rc35_codex_to_skills"] = mCodexToSkills()
	migrationImpls["3.2.0rc35_default_charter_pack"] = neverNeeded("Superseded by the charter-pack cutover")
	migrationImpls["3.2.0rc35_fix_prompt_file_workaround"] = skillMarkerReplacement("spec-kitty-runtime-next",
		[]string{"Check `prompt_file` is not null, then read and execute", "Workaround #336: treat null prompt as blocked",
			"#336 — `prompt_file` can be `null` on `step` decisions", "Treat a null `prompt_file` as a blocked state"},
		"Cannot locate canonical SKILL.md for runtime-next", "All runtime-next skill files already up to date")
	migrationImpls["3.2.0rc35_kittify_profile_handoff"] = mKittifyProfileHandoff()
	migrationImpls["3.2.0rc35_pi_letta_agent_backfill"] = mPiLettaBackfill()
	migrationImpls["3.2.0rc35_repository_root_checkout_terminology"] = mRepositoryRootCheckoutTerminology()
	migrationImpls["3.2.0rc35_spk_skill_pack"] = mSpkSkillPack()
	migrationImpls["3.2.0rc35_strip_selection_config"] = mStripSelectionConfig()
	migrationImpls["3.2.0rc35_kittify_runtime_git_hygiene"] = mRuntimeGitHygiene()
	migrationImpls["3.2.0rc35_unified_bundle"] = mUnifiedBundle()
	migrationImpls["3.2.0rc35_update_planning_templates"] = mUpdatePlanningTemplates()
	migrationImpls["3_2_0rc39_refresh_orientation_block"] = mRefreshOrientationBlock()
	migrationImpls["3_3_0_session_presence_all_harnesses"] = mSessionPresenceAllHarnesses()
	migrationImpls["3_3_0_session_presence_claude_code"] = mSessionPresenceClaudeCode()
	migrationImpls["3_3_0_op_record_schema_v2"] = mOpRecordSchemaV2()
	migrationImpls["3.2.0rc43_retire_profile_context_command"] = mRetireProfileContextCommand()
	migrationImpls["0_9_3_surface_repair_wiring"] = mSurfaceRepairWiring()
	migrationImpls["0_9_4_roo_deprecation"] = mRooDeprecation()
	migrationImpls["3.2.0rc45_retire_standalone_skill_surface"] = mRetireStandaloneSkillSurface()
	migrationImpls["3.2.3_encoding_provenance_gitignore_backfill"] = mRuntimeGitHygiene()
	migrationImpls["3.2.8_provision_kitty_env"] = mProvisionKittyEnv()
	migrationImpls["migrate_lifecycle_envelope"] = mMigrateLifecycleEnvelope()
}

// --- 3.0.0 canonical context --------------------------------------------------

func discoverFeaturesWithMeta(root string) []string {
	var out []string
	for _, f := range featureDirs(root) {
		if pathExists(filepath.Join(f, "meta.json")) {
			out = append(out, f)
		}
	}
	return out
}

func mCanonicalContext() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			v := kittify.ProjectSchemaVersion(root)
			return v == nil || *v < 3
		},
		canApply: func(root string) (bool, string) {
			k := filepath.Join(root, ".kittify")
			if !isDir(k) {
				return false, "No .kittify directory found — is this a spec-kitty project?"
			}
			if !pathExists(filepath.Join(k, "metadata.yaml")) {
				return false, "No .kittify/metadata.yaml found"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			return runCanonicalContext(root, dryRun)
		},
	}
}

func runCanonicalContext(root string, dryRun bool) migrationResult {
	var warn, errs []string
	if dryRun {
		n := len(discoverFeaturesWithMeta(root))
		return migrationResult{Success: true, Changes: []string{fmt.Sprintf("[DRY RUN] Migrated %d feature(s), 0 WPs backfilled, 0 events generated", n)},
			Warnings: []string{fmt.Sprintf("DRY RUN: would migrate %d feature(s) to schema v3", n)}}
	}
	fail := func(step, msg string) migrationResult {
		errs = append(errs, fmt.Sprintf("[%s] %s", step, msg))
		return migrationResult{Errors: errs, Warnings: warn, Changes: []string{"Migration failed at step: " + step}}
	}
	// Step 2: identity backfill (project_uuid, mission ids, WP ids).
	if err := backfillProjectUUID(root); err != nil {
		if os.IsNotExist(err) {
			return fail("identity_backfill", "metadata.yaml missing: "+err.Error())
		}
		return fail("identity_backfill", "project UUID backfill failed: "+err.Error())
	}
	missionIDs := map[string]string{}
	for _, f := range featureDirs(root) {
		meta, err := mission.ReadMeta(f)
		if err != nil {
			if err == mission.ErrNoMeta {
				continue
			}
			return fail("identity_backfill", "mission ID backfill failed: "+err.Error())
		}
		if v, ok := meta.Get("mission_id"); ok {
			missionIDs[filepath.Base(f)] = fmt.Sprint(v)
			continue
		}
		id := ulid.New()
		meta.Set("mission_id", id)
		mission.WriteMeta(f, meta, false)
		missionIDs[filepath.Base(f)] = id
	}
	wpMaps := map[string]map[string]string{}
	total := 0
	for _, f := range discoverFeaturesWithMeta(root) {
		m := migrate.BackfillWPIDs(f, missionIDs[filepath.Base(f)])
		wpMaps[filepath.Base(f)] = m
		total += len(m)
	}
	// Step 3: ownership backfill.
	for _, f := range discoverFeaturesWithMeta(root) {
		backfillOwnership(f, filepath.Base(f))
	}
	// Step 4: state rebuild.
	events := 0
	for _, f := range discoverFeaturesWithMeta(root) {
		rb := migrate.RebuildEventLog(f, filepath.Base(f), wpMaps[filepath.Base(f)])
		events += rb.Generated
		warn = append(warn, rb.Warnings...)
		for _, e := range rb.Errors {
			warn = append(warn, fmt.Sprintf("State rebuild warning for %s: %s", filepath.Base(f), e))
		}
	}
	// Step 5: strip mutable frontmatter.
	for _, f := range discoverFeaturesWithMeta(root) {
		stripMutableFields(f)
	}
	// Step 6: schema version.
	if err := updateSchemaVersionRuamel(root); err != nil {
		return fail("schema_version_update", "schema version update failed: "+err.Error())
	}
	// Step 7: .gitignore.
	updateCanonicalGitignore(root)
	// Step 8: derived files.
	var moved []string
	for _, f := range featureDirs(root) {
		sj := filepath.Join(f, "status.json")
		if !pathExists(sj) {
			continue
		}
		dd := filepath.Join(root, ".kittify", "derived", filepath.Base(f))
		os.MkdirAll(dd, 0o755)
		dest := filepath.Join(dd, "status.json")
		if copyFile2(sj, dest) == nil {
			moved = append(moved, fmt.Sprintf("Copied %s → %s", sj, dest))
		}
	}
	// Step 9: agent shims.
	if _, err := rewriteAgentShims(root); err != nil {
		warn = append(warn, "Shim rewrite warning (non-fatal): "+err.Error())
	}
	ch := []string{fmt.Sprintf("Migrated %d feature(s), %d WPs backfilled, %d events generated", len(discoverFeaturesWithMeta(root)), total, events)}
	if len(moved) > 0 {
		ch = append(ch, fmt.Sprintf("Moved %d derived file(s)", len(moved)))
	}
	return migrationResult{Success: true, Changes: ch, Warnings: warn}
}

// backfillProjectUUID is backfill_identity.backfill_project_uuid (ruamel
// round-trip dump of metadata.yaml, width 4096).
func backfillProjectUUID(root string) error {
	p := kittify.MetadataPath(root)
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	doc, err := yamlx.ParseObject(b)
	if err != nil {
		return err
	}
	if doc == nil {
		doc = &jsonx.Object{}
	}
	sk := doc.GetObject("spec_kitty")
	if sk == nil {
		sk = &jsonx.Object{}
		doc.Set("spec_kitty", sk)
	}
	if sk.Has("project_uuid") {
		return nil
	}
	sk.Set("project_uuid", ulid.New())
	return os.WriteFile(p, []byte(renderMetadataRuamel(b, doc)), 0o600)
}

// renderMetadataRuamel re-emits metadata.yaml keeping its leading comment
// header (ruamel round-trip keeps comments).
func renderMetadataRuamel(orig []byte, doc *jsonx.Object) string {
	header := ""
	for _, l := range strings.SplitAfter(string(orig), "\n") {
		if strings.HasPrefix(l, "#") || strings.TrimSpace(l) == "" {
			header += l
			continue
		}
		break
	}
	return header + yamlx.EmitWith(doc, yamlx.Options{Width: 4096})
}

func updateSchemaVersionRuamel(root string) error {
	p := kittify.MetadataPath(root)
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("metadata.yaml not found: %s", p)
	}
	doc, err := yamlx.ParseObject(b)
	if err != nil {
		return fmt.Errorf("metadata.yaml is not valid YAML, refusing to rewrite it: %s", p)
	}
	if doc == nil {
		doc = &jsonx.Object{}
	}
	sk, ok := doc.Get("spec_kitty")
	if !ok {
		sk = &jsonx.Object{}
		doc.Set("spec_kitty", sk)
	}
	sko, isObj := sk.(*jsonx.Object)
	if !isObj {
		return fmt.Errorf("metadata.yaml spec_kitty must be a mapping, refusing to rewrite it: %s", p)
	}
	sko.Set("schema_version", kittify.CurrentSchemaVersion)
	caps, _ := sko.Get("schema_capabilities")
	sko.Set("schema_capabilities", kittify.CanonicalSchemaCapabilities(caps))
	sko.Set("last_upgraded_at", clock.NowUTCISO())
	return os.WriteFile(p, []byte(renderMetadataRuamel(b, doc)), 0o600)
}

func updateCanonicalGitignore(root string) {
	p := filepath.Join(root, ".gitignore")
	entries := []string{".kittify/derived/", ".kittify/runtime/", ".kittify/.migration-backup/"}
	b, err := os.ReadFile(p)
	if err != nil {
		os.WriteFile(p, []byte(strings.Join(entries, "\n")+"\n"), 0o666&^umask())
		return
	}
	content := string(b)
	orig := content
	for _, e := range entries {
		if !strings.Contains(content, e) {
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			content += e + "\n"
		}
	}
	if content != orig {
		os.WriteFile(p, []byte(content), 0o666&^umask())
	}
}

// --- ownership backfill / frontmatter strip (migration/backfill_ownership,
// strip_frontmatter) -------------------------------------------------------------

var (
	planningSignals = []*regexp.Regexp{regexp.MustCompile(`\bkitty-specs/`), regexp.MustCompile(`\bspec\.md\b`), regexp.MustCompile(`\bplan\.md\b`),
		regexp.MustCompile(`\btasks\.md\b`), regexp.MustCompile(`\bdata-model\.md\b`), regexp.MustCompile(`\bquickstart\.md\b`)}
	codeSignals = []*regexp.Regexp{regexp.MustCompile(`\bsrc/`), regexp.MustCompile(`\btests?/`), regexp.MustCompile(`\.py\b`),
		regexp.MustCompile(`\.ts\b`), regexp.MustCompile(`\.js\b`)}
	ownedPathRe = regexp.MustCompile(`(?:(?:src|tests?|kitty-specs|docs)/[^\s` + "`" + `'")\]>]+)`)
	wpPrefixRe  = regexp.MustCompile(`^(WP\d+)`)
)

func inferExecutionMode(content string, files []string) string {
	combined := content + "\n" + strings.Join(files, "\n")
	p, c := 0, 0
	for _, r := range planningSignals {
		if r.MatchString(combined) {
			p++
		}
	}
	for _, r := range codeSignals {
		if r.MatchString(combined) {
			c++
		}
	}
	if p > 0 && c == 0 {
		return "planning_artifact"
	}
	return "code_change"
}

func inferOwnedFiles(content, slug string) []string {
	if inferExecutionMode(content, nil) == "planning_artifact" {
		return []string{"kitty-specs/" + slug + "/**"}
	}
	set := map[string]bool{}
	for _, m := range ownedPathRe.FindAllString(content, -1) {
		set[m] = true
	}
	paths := sortedKeys(set)
	var globs []string
	seen := map[string]bool{}
	for _, p := range paths {
		g := p
		if strings.HasSuffix(p, "/") {
			g = p + "**"
		} else if parts := strings.Split(p, "/"); !strings.Contains(parts[len(parts)-1], ".") {
			g = strings.TrimRight(p, "/") + "/**"
		}
		parts := strings.Split(g, "/")
		skip := false
		for i := 1; i < len(parts); i++ {
			if seen[strings.Join(parts[:i], "/")+"/**"] {
				skip = true
				break
			}
		}
		if !skip {
			seen[g] = true
			globs = append(globs, g)
		}
	}
	if len(globs) == 0 {
		globs = []string{"src/**"}
	}
	return globs
}

func inferAuthoritativeSurface(owned []string) string {
	if len(owned) == 0 {
		return ""
	}
	strip := func(p string) string {
		i := strings.LastIndex(p, "/")
		head, tail := "", p
		if i >= 0 {
			head, tail = p[:i], p[i+1:]
		}
		if strings.ContainsAny(tail, "*?[") || strings.Contains(tail, ".") {
			return head
		}
		return strings.TrimRight(p, "/")
	}
	var stripped []string
	for _, p := range owned {
		stripped = append(stripped, strip(p))
	}
	if len(stripped) == 1 {
		c := strings.TrimRight(stripped[0], "/") + "/"
		if strings.HasPrefix(owned[0], c) {
			return c
		}
		return owned[0]
	}
	var split [][]string
	for _, s := range stripped {
		split = append(split, strings.Split(s, "/"))
	}
	var common []string
	for i := 0; ; i++ {
		var seg string
		same := true
		for j, s := range split {
			if i >= len(s) {
				same = false
				break
			}
			if j == 0 {
				seg = s[i]
			} else if s[i] != seg {
				same = false
			}
		}
		if !same {
			break
		}
		common = append(common, seg)
	}
	if len(common) == 0 {
		common = split[0]
	}
	c := strings.TrimRight(strings.Join(common, "/"), "/") + "/"
	for _, p := range owned {
		if strings.HasPrefix(p, c) {
			return c
		}
	}
	return owned[0]
}

func backfillOwnership(feature, slug string) {
	tasks := filepath.Join(feature, "tasks")
	if !isDir(tasks) {
		return
	}
	for _, wp := range globFiles(tasks, "WP*.md") {
		front, body, _, err := frontmatterRead(wp)
		if err != nil {
			continue
		}
		hasMode, hasFiles, hasSurface := front.Has("execution_mode"), front.Has("owned_files"), front.Has("authoritative_surface")
		if hasMode && hasFiles && hasSurface {
			continue
		}
		updates := &jsonx.Object{}
		if !hasMode {
			updates.Set("execution_mode", inferExecutionMode(body, nil))
		}
		if !hasFiles {
			l := []any{}
			for _, g := range inferOwnedFiles(body, slug) {
				l = append(l, g)
			}
			updates.Set("owned_files", l)
		}
		if !hasSurface {
			var cur []string
			v, ok := updates.Get("owned_files")
			if !ok || !pyjsonTruthy(v) {
				v, _ = front.Get("owned_files")
			}
			if l, isL := v.([]any); isL {
				for _, x := range l {
					cur = append(cur, pyStrValue(x))
				}
			}
			updates.Set("authoritative_surface", inferAuthoritativeSurface(cur))
		}
		changed := false
		for _, kv := range updates.Pairs() {
			if kv.Key == "scope" || !front.Has(kv.Key) {
				front.Set(kv.Key, kv.Value)
				changed = true
			}
		}
		if changed {
			os.WriteFile(wp, []byte(renderFrontmatter(front, body)), 0o666&^umask())
		}
	}
}

var mutableFields = []string{"lane", "review_status", "reviewed_by", "review_feedback", "progress", "shell_pid", "assignee", "agent",
	"shell_pid_created_at", "review_artifact_override_at", "review_artifact_override_actor", "review_artifact_override_wp_id",
	"review_artifact_override_reason", "reviewer_shell_pid", "history"}

func stripOne(p string) {
	front, body, _, err := frontmatterRead(p)
	if err != nil {
		return
	}
	n := 0
	for _, f := range mutableFields {
		if front.Has(f) {
			front.Delete(f)
			n++
		}
	}
	if n > 0 {
		os.WriteFile(p, []byte(renderFrontmatter(front, body)), 0o666&^umask())
	}
}

func stripMutableFields(feature string) {
	tasks := filepath.Join(feature, "tasks")
	if !isDir(tasks) {
		return
	}
	for _, wp := range globFiles(tasks, "WP*.md") {
		stripOne(wp)
	}
	if tm := filepath.Join(feature, "tasks.md"); pathExists(tm) {
		stripOne(tm)
	}
}

// --- rewrite_agent_shims (migration/rewrite_shims) ------------------------------

type rewriteResult struct {
	agentsProcessed int
	written         []string
	deleted         []string
	preserved       []string
	warnings        []string
}

func rewriteAgentShims(root string) (*rewriteResult, error) {
	res := &rewriteResult{}
	dirs, err := migrationAgentDirs(root)
	if err != nil {
		res.warnings = append(res.warnings, "generate_all_shims failed: "+err.Error())
		return res, nil
	}
	// generate_all_shims: CLI-driven shims for every agent dir.
	for _, d := range dirs {
		key, ok := agents.AgentDirToKey[d.root]
		if !ok {
			key = strings.TrimLeft(d.root, ".")
		}
		td := filepath.Join(root, d.root, d.subdir)
		os.MkdirAll(td, 0o755)
		ext := "md"
		if cfg, ok := agents.CommandConfigs[key]; ok {
			ext = cfg.Ext
		}
		for _, c := range sortedStrings(agents.CLIDrivenCommands) {
			content, err := agents.ShimContent(c, key, appVersion())
			if err != nil {
				continue
			}
			out := filepath.Join(td, "spec-kitty."+c+"."+ext)
			writePlain(out, []byte(content))
			res.written = append(res.written, out)
		}
	}
	// _generate_prompt_templates.
	var prompts []string
	for _, d := range dirs {
		key, ok := agents.AgentDirToKey[d.root]
		if !ok {
			continue
		}
		if _, ok := agents.CommandConfigs[key]; !ok {
			continue
		}
		td := filepath.Join(root, d.root, d.subdir)
		os.MkdirAll(td, 0o755)
		for _, c := range sortedStrings(agents.PromptDrivenCommands) {
			if !commandTemplateExists(c) {
				continue
			}
			rendered, okr := renderFullPrompt(c, key)
			if !okr {
				continue
			}
			out := filepath.Join(td, commandOutputFilename(c, key))
			writePlain(out, []byte(rendered))
			prompts = append(prompts, out)
		}
	}
	sort.Strings(prompts)
	sort.Strings(res.written)
	res.written = append(res.written, prompts...)
	written := map[string]bool{}
	for _, w := range res.written {
		written[w] = true
	}
	expected := map[string]bool{}
	for _, d := range dirs {
		key, ok := agents.AgentDirToKey[d.root]
		if !ok {
			continue
		}
		for _, c := range agents.PromptDrivenCommands {
			expected[filepath.Join(root, d.root, d.subdir, commandOutputFilename(c, key))] = true
		}
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		cd := filepath.Join(root, d.root, d.subdir)
		if !isDir(cd) || seen[cd] {
			continue
		}
		seen[cd] = true
		res.agentsProcessed++
		for _, f := range globFiles(cd, "spec-kitty.*") {
			if written[f] || expected[f] {
				continue
			}
			v, err := guardDestructiveRemoval(f, root, defaultCanonicalProver(), false, "", false)
			if err != nil {
				res.warnings = append(res.warnings, fmt.Sprintf("Failed to remove stale file %s: %s", f, pyOSError(err)))
				continue
			}
			if v.owned {
				res.deleted = append(res.deleted, f)
			} else {
				res.preserved = append(res.preserved, f)
				res.warnings = append(res.warnings, v.diagnostic)
			}
		}
	}
	return res, nil
}

// writePlain is Path.write_text (keeps an existing file's mode).
func writePlain(p string, data []byte) error {
	if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o200 == 0 {
		return &os.PathError{Op: "open", Path: p, Err: os.ErrPermission}
	}
	return os.WriteFile(p, data, 0o666&^umask())
}

func mRestorePromptCommands302() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				key, ok := agents.AgentDirToKey[d.root]
				if !ok {
					continue
				}
				for _, c := range agents.PromptDrivenCommands {
					if !pathExists(filepath.Join(ad, commandOutputFilename(c, key))) {
						return true, nil
					}
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			if dryRun {
				return newResult([]string{"Would regenerate all agent command files (CLI shims + prompt templates)"}, nil, nil)
			}
			r, _ := rewriteAgentShims(root)
			return migrationResult{Success: true, Changes: []string{fmt.Sprintf("Processed %d agent directories", r.agentsProcessed),
				fmt.Sprintf("Wrote %d command files", len(r.written)), fmt.Sprintf("Deleted %d stale files", len(r.deleted))}, Warnings: r.warnings}
		},
	}
}

func mDirectCanonicalCommands() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				for _, c := range agents.CLIDrivenCommands {
					for _, f := range globFiles(ad, "spec-kitty."+c+".*") {
						if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "spec-kitty agent shim") {
							return true, nil
						}
					}
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			if dryRun {
				return newResult([]string{"Would regenerate all CLI-driven command files to use direct canonical commands"}, nil, nil)
			}
			r, _ := rewriteAgentShims(root)
			var ch []string
			for _, p := range r.written {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					rel = p
				}
				ch = append(ch, "Regenerated: "+rel)
			}
			for _, p := range r.deleted {
				rel, _ := filepath.Rel(root, p)
				ch = append(ch, "Deleted stale: "+rel)
			}
			if len(ch) == 0 {
				ch = append(ch, "No command files needed updating")
			}
			return migrationResult{Success: true, Changes: ch, Warnings: r.warnings}
		},
	}
}

// --- 3.0.3 / spk skill pack -----------------------------------------------------

func mGlobalizeSkillPack() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false
			}
			skills := agents.Skills()
			if len(skills) == 0 {
				return false
			}
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil {
				return false
			}
			inst := installableSkillAgents(ac.Available)
			if len(inst) == 0 {
				return false
			}
			if m := agents.LoadSkillsManifest(root); m != nil {
				for _, e := range m.Entries {
					if strings.HasPrefix(e.SkillName, "spec-kitty-") && e.DeliveryMode != "symlink" {
						return true
					}
				}
			}
			for _, a := range inst {
				r := agents.ProjectSkillRoot(a)
				if r == "" {
					continue
				}
				for _, s := range skills {
					f := filepath.Join(root, filepath.FromSlash(r), s.Name, "SKILL.md")
					if !pathExists(f) || !isSymlink(f) {
						return true
					}
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false, ".kittify/ directory does not exist (not initialized)"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			return reinstallSkillPack(root, dryRun, agents.Skills(), "3.0.3",
				"No canonical skills discovered for relinking", "No skill-installing agents configured; skipping relink",
				"Would relink %d canonical skill(s) for %d agent(s)", "No managed skill files were linked for any configured agent",
				"Relinked %d canonical skill(s) for %d agent(s) (%d managed files)", "Updated .kittify/skills-manifest.json for global canonical skill links", false)
		},
	}
}

func spkSkills() []agents.Skill {
	var out []agents.Skill
	for _, s := range agents.Skills() {
		if strings.HasPrefix(s.Name, "spk-") {
			out = append(out, s)
		}
	}
	return out
}

var retiredCanonicalSkills = map[string]bool{"debugger-debbie": true, "paula-patterns": true, "spk-team-upsun-cli-sync": true, "spk-admin-dashboard": true,
	"spk-team-auth": true, "spk-team-connectors": true, "spk-team-sync": true, "spk-team-tracker": true, "ad-hoc-profile-load": true,
	"spec-kitty-bulk-edit-classification": true, "spec-kitty-charter-doctrine": true, "spec-kitty-constitution-doctrine": true,
	"spec-kitty-glossary-context": true, "spec-kitty-spdd-reasons": true, "spk-doctrine-bulk-edit": true, "spk-doctrine-charter": true,
	"spk-doctrine-glossary": true, "spk-doctrine-profile-load": true, "spk-doctrine-semantic-compression": true, "spk-doctrine-show-me": true,
	"spk-doctrine-spdd-reasons": true, "spec-kitty.advise": true}

// reinstallSkillPack is the shared apply of 3.0.3 / spk_skill_pack:
// install_all_skills, keep preserved entries, save the manifest.
func reinstallSkillPack(root string, dryRun bool, selected []agents.Skill, version, noSkills, noAgents, dryFmt, noEntries, doneFmt, doneLine string, retired bool) migrationResult {
	if len(selected) == 0 {
		return migrationResult{Errors: []string{noSkills}}
	}
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return migrationResult{Errors: []string{err.Error()}}
	}
	inst := installableSkillAgents(ac.Available)
	if len(inst) == 0 {
		return newResult(nil, []string{noAgents}, nil)
	}
	if dryRun {
		return newResult([]string{fmt.Sprintf(dryFmt, len(selected), len(inst))}, nil, nil)
	}
	now := clock.NowUTCISO()
	existing := agents.LoadSkillsManifest(root)
	entries, err := installAllSkills(root, inst, now)
	if err != nil {
		return migrationResult{Errors: []string{err.Error()}}
	}
	m := &agents.SkillsManifest{CreatedAt: now, UpdatedAt: now, SpecKittyVersion: version}
	for _, e := range entries {
		m.AddEntry(e)
	}
	instSet := map[string]bool{}
	for _, a := range inst {
		instSet[a] = true
	}
	canon := map[string]bool{}
	for _, s := range agents.Skills() {
		canon[s.Name] = true
	}
	preserved := 0
	if existing != nil {
		for _, e := range existing.Entries {
			if retired && retiredCanonicalSkills[e.SkillName] {
				continue
			}
			if (!canon[e.SkillName] && e.Origin() != "pack") || !instSet[e.AgentKey] {
				m.Entries = append(m.Entries, e)
				preserved++
			}
		}
	}
	if len(m.Entries) == 0 && preserved == 0 {
		return migrationResult{Errors: []string{noEntries}}
	}
	agents.SaveSkillsManifest(root, m, now)
	return newResult([]string{fmt.Sprintf(doneFmt, len(selected), len(inst), len(m.Entries)), doneLine}, nil, nil)
}

func mSpkSkillPack() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false
			}
			skills := spkSkills()
			if len(skills) == 0 {
				return false
			}
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil {
				return false
			}
			for _, a := range installableSkillAgents(ac.Available) {
				r := agents.ProjectSkillRoot(a)
				if r == "" {
					continue
				}
				for _, s := range skills {
					for _, f := range s.Files {
						if !pathExists(filepath.Join(root, filepath.FromSlash(r), s.Name, filepath.FromSlash(f))) {
							return true
						}
					}
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false, ".kittify/ directory does not exist (not initialized)"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			return reinstallSkillPack(root, dryRun, spkSkills(), "3.2.0rc35",
				"No spk skills discovered for install", "No skill-installing agents configured; skipping spk skill pack",
				"Would install %d spk skill(s) for %d agent(s)", "No managed skill files were installed for any configured agent",
				"Installed %d spk skill(s) for %d agent(s) (%d managed files)", "Updated .kittify/skills-manifest.json with spk skill entries", true)
		},
	}
}

// --- 3.1.1 charter rename ---------------------------------------------------------

var constitutionRe = regexp.MustCompile(`(?i)constitution`)

func replaceConstitution(s string) string {
	return constitutionRe.ReplaceAllStringFunc(s, func(w string) string {
		if w[0] >= 'A' && w[0] <= 'Z' {
			return "Charter"
		}
		return "charter"
	})
}

func mCharterRename() migrationImpl {
	detect := func(root string) (bool, error) {
		k := filepath.Join(root, ".kittify")
		if pathExists(filepath.Join(k, "constitution")) || pathExists(filepath.Join(k, "memory", "constitution.md")) {
			return true, nil
		}
		for _, m := range subdirs(filepath.Join(k, "missions")) {
			if pathExists(filepath.Join(m, "constitution")) {
				return true, nil
			}
		}
		dirs, err := migrationAgentDirs(root)
		if err != nil {
			return false, err
		}
		for _, d := range dirs {
			ad := filepath.Join(root, d.root, d.subdir)
			if pathExists(ad) && pathExists(filepath.Join(ad, "spec-kitty.constitution.md")) {
				return true, nil
			}
		}
		return false, nil
	}
	return migrationImpl{
		detectE: detect,
		canApply: func(root string) (bool, string) {
			if d, _ := detect(root); !d {
				return false, "No constitution-era state found"
			}
			return ok()
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch, errs, warn []string
			k := filepath.Join(root, ".kittify")
			cd := filepath.Join(k, "charter")
			pr := defaultCanonicalProver()
			// _normalize_layouts
			for _, m := range subdirs(filepath.Join(k, "missions")) {
				mc := filepath.Join(m, "constitution")
				if !pathExists(mc) {
					continue
				}
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would remove %s/constitution/", filepath.Base(m)))
					continue
				}
				v, err := guardDestructiveRemoval(mc, root, pr, true, k, false)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to remove %s/constitution/: %s", filepath.Base(m), pyOSError(err)))
				} else if v.owned {
					ch = append(ch, fmt.Sprintf("Removed mission-specific: %s/constitution/", filepath.Base(m)))
				} else {
					warn = append(warn, v.diagnostic)
				}
			}
			memC := filepath.Join(k, "memory", "constitution.md")
			if pathExists(memC) {
				if pathExists(cd) && pathExists(filepath.Join(cd, "charter.md")) {
					if dryRun {
						ch = append(ch, "Would remove stale .kittify/memory/constitution.md")
					} else if v, err := guardDestructiveRemoval(memC, root, pr, false, k, false); err != nil {
						errs = append(errs, "Failed to remove stale memory/constitution.md: "+pyOSError(err))
					} else if v.owned {
						ch = append(ch, "Removed stale .kittify/memory/constitution.md")
					} else {
						warn = append(warn, v.diagnostic)
					}
				} else if dryRun {
					ch = append(ch, "Would move .kittify/memory/constitution.md -> .kittify/charter/charter.md")
				} else {
					os.MkdirAll(cd, 0o755)
					if err := os.Rename(memC, filepath.Join(cd, "charter.md")); err != nil {
						errs = append(errs, "Failed to move memory/constitution.md: "+pyOSError(err))
					} else {
						ch = append(ch, "Moved .kittify/memory/constitution.md -> .kittify/charter/charter.md")
					}
				}
			}
			consD := filepath.Join(k, "constitution")
			if pathExists(consD) {
				if pathExists(cd) {
					if dryRun {
						ch = append(ch, "Would merge .kittify/constitution/ into .kittify/charter/")
					} else {
						for _, item := range listDir(consD) {
							dest := filepath.Join(cd, filepath.Base(item))
							if !pathExists(dest) {
								os.Rename(item, dest)
								ch = append(ch, fmt.Sprintf("Merged %s from constitution/ to charter/", filepath.Base(item)))
							} else {
								v, _ := guardDestructiveRemoval(item, root, pr, isDir(item), k, false)
								warn = append(warn, v.diagnostic)
							}
						}
						if err := os.Remove(consD); err != nil {
							errs = append(errs, "Failed to merge constitution/ into charter/: "+pyOSError(err))
						} else {
							ch = append(ch, "Removed residual .kittify/constitution/ after merge")
						}
					}
				} else if dryRun {
					ch = append(ch, "Would rename .kittify/constitution/ -> .kittify/charter/")
				} else if err := os.Rename(consD, cd); err != nil {
					errs = append(errs, "Failed to rename constitution/ -> charter/: "+pyOSError(err))
				} else {
					ch = append(ch, "Renamed .kittify/constitution/ -> .kittify/charter/")
				}
			}
			if pathExists(cd) {
				oldMD, newMD := filepath.Join(cd, "constitution.md"), filepath.Join(cd, "charter.md")
				if pathExists(oldMD) && !pathExists(newMD) {
					if dryRun {
						ch = append(ch, "Would rename charter/constitution.md -> charter/charter.md")
					} else if err := os.Rename(oldMD, newMD); err != nil {
						errs = append(errs, "Failed to rename constitution.md -> charter.md: "+pyOSError(err))
					} else {
						ch = append(ch, "Renamed charter/constitution.md -> charter/charter.md")
					}
				}
			}
			rewrite := func(p string, readOnly bool) {
				b, err := os.ReadFile(p)
				rel, _ := filepath.Rel(root, p)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to read %s: %s", rel, pyOSError(err)))
					return
				}
				up := replaceConstitution(string(b))
				if up == string(b) {
					return
				}
				if dryRun {
					ch = append(ch, "Would rewrite content in "+rel)
				} else if err := writeGeneratedFile(p, []byte(up), readOnly); err != nil {
					errs = append(errs, fmt.Sprintf("Failed to write %s: %s", rel, err))
				} else {
					ch = append(ch, "Rewrote content in "+rel)
				}
			}
			// _rewrite_content
			if pathExists(cd) {
				for _, f := range rglobFiles(cd) {
					switch filepath.Ext(f) {
					case ".md", ".yaml", ".yml", ".json", ".txt":
						rewrite(f, false)
					}
				}
			}
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !pathExists(ad) {
					continue
				}
				for _, f := range globFiles(ad, "spec-kitty.*.md") {
					if isFile(f) {
						rewrite(f, true)
					}
				}
			}
			// _rename_agent_artifacts
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !pathExists(ad) {
					continue
				}
				oc, nc := filepath.Join(ad, "spec-kitty.constitution.md"), filepath.Join(ad, "spec-kitty.charter.md")
				if pathExists(oc) && !pathExists(nc) {
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would rename %s/%s/spec-kitty.constitution.md", d.root, d.subdir))
					} else if err := os.Rename(oc, nc); err != nil {
						errs = append(errs, fmt.Sprintf("Failed to rename %s/%s/spec-kitty.constitution.md: %s", d.root, d.subdir, pyOSError(err)))
					} else {
						ch = append(ch, fmt.Sprintf("Renamed %s/%s/spec-kitty.constitution.md -> spec-kitty.charter.md", d.root, d.subdir))
						dr := dryRun
						dryRun = false
						rewrite(nc, true)
						dryRun = dr
					}
				}
			}
			// _normalize_metadata
			if md := kittify.LoadMetadata(root); md != nil {
				rewritten := false
				for i, r := range md.Applied {
					if n, ok := kittify.LegacyMigrationID(r.ID); ok {
						if dryRun {
							ch = append(ch, fmt.Sprintf("Would rewrite metadata ID %s -> %s", r.ID, n))
						} else {
							md.Applied[i].ID = n
							rewritten = true
						}
					}
				}
				if rewritten {
					md.Save(root)
					ch = append(ch, "Normalized metadata migration IDs")
				}
			}
			if len(errs) > 0 {
				return migrationResult{Changes: ch, Errors: errs, Warnings: warn}, nil
			}
			return migrationResult{Success: true, Changes: ch, Warnings: warn}, nil
		},
	}
}

// --- 3.1.1 normalize status json ---------------------------------------------------

func statusNeedsNormalisation(feature string) bool {
	sp := filepath.Join(feature, status.SnapshotFile)
	b, err := os.ReadFile(sp)
	if err != nil {
		return false
	}
	actual, err := jsonx.DecodeObject(b)
	if err != nil {
		return false
	}
	snap, err := status.MaterializeSnapshot(feature)
	if err != nil || snap == nil {
		return false
	}
	return actual.GetString("materialized_at") != snap.MaterializedAt || !actual.Has("materialized_at")
}

func mNormalizeStatusJSON() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, f := range featureDirs(root) {
				if statusNeedsNormalisation(f) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if pathExists(filepath.Join(root, "kitty-specs")) || pathExists(filepath.Join(root, ".kittify")) {
				return ok()
			}
			return false, "No kitty-specs/ directory found"
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch []string
			for _, f := range featureDirs(root) {
				sp := filepath.Join(f, status.SnapshotFile)
				if !pathExists(sp) || !statusNeedsNormalisation(f) {
					continue
				}
				if dryRun {
					ch = append(ch, filepath.Base(f)+": would normalise status.json")
					continue
				}
				b, _ := os.ReadFile(sp)
				cur, _ := jsonx.DecodeObject(b)
				snap, _ := status.MaterializeSnapshot(f)
				cur.Set("materialized_at", snap.MaterializedAt)
				os.WriteFile(sp, []byte(jsonx.Dumps(cur, jsonx.Options{Indent: 2, SortKeys: true})+"\n"), 0o666&^umask())
				ch = append(ch, filepath.Base(f)+": normalised status.json")
			}
			return newResult(ch, nil, nil)
		},
	}
}

// --- 3.1.2 / 3.2.0a4 globalize commands -------------------------------------------

func globalizeCommandDirs(root string) ([]agentDir, error) {
	dirs, err := migrationAgentDirs(root)
	if err != nil {
		return nil, err
	}
	if isDir(filepath.Join(root, ".codex", "prompts")) {
		dirs = append(dirs, agentDir{".codex", "prompts"})
	}
	return dirs, nil
}

func isGeneratedCommandFile(p string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	lines := pySplitLines(string(b))
	if len(lines) > 15 {
		lines = lines[:15]
	}
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), agents.VersionMarkerPrefix) {
			return true
		}
	}
	return false
}

func mSafeGlobalizeCommands() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := globalizeCommandDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				for _, c := range listDir(ad) {
					if strings.HasPrefix(filepath.Base(c), "spec-kitty.") {
						return true, nil
					}
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch, warn, preserved []string
			runtimePresent := len(subdirs(filepath.Join(project.GlobalKittify(), "missions"))) > 0
			verbKeep := "Kept"
			if dryRun {
				verbKeep = "Would keep"
			}
			home := os.Getenv("HOME")
			dirs, err := globalizeCommandDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				for _, t := range listDir(ad) {
					name := filepath.Base(t)
					if !strings.HasPrefix(name, "spec-kitty.") {
						continue
					}
					rel, _ := filepath.Rel(root, t)
					rel = filepath.ToSlash(rel)
					switch {
					case !isGeneratedCommandFile(t):
						verb := "Preserved"
						if dryRun {
							verb = "Would preserve"
						}
						ch = append(ch, fmt.Sprintf("%s: %s (no version marker (user-authored or unproven file))", verb, rel))
						preserved = append(preserved, rel)
					case !runtimePresent:
						ch = append(ch, fmt.Sprintf("%s: %s (generated; global runtime (~/.kittify/missions/) not found)", verbKeep, rel))
					case !isFile(filepath.Join(home, d.root, d.subdir, name)):
						ch = append(ch, fmt.Sprintf("%s: %s (generated; no global equivalent at ~/%s/%s/%s)", verbKeep, rel, d.root, d.subdir, name))
					case dryRun:
						ch = append(ch, "Would remove: "+rel)
					default:
						if st, err := os.Stat(t); err == nil {
							os.Chmod(t, st.Mode().Perm()|0o222)
						}
						if err := os.Remove(t); err != nil {
							ch = append(ch, fmt.Sprintf("Kept: %s (generated; unlink failed: %s)", rel, pyOSError(err)))
							warn = append(warn, fmt.Sprintf("Could not remove %s: %s", rel, pyOSError(err)))
						} else {
							ch = append(ch, "Removed: "+rel)
						}
					}
				}
				if !dryRun && isDir(ad) {
					if os.Remove(ad) == nil {
						r, _ := filepath.Rel(root, ad)
						ch = append(ch, "Removed empty directory: "+filepath.ToSlash(r))
					}
				}
			}
			return migrationResult{Success: true, Changes: ch, Warnings: warn, ManualReview: len(preserved) > 0, Preserved: preserved}, nil
		},
	}
}

// --- 3.2.0a4 normalize mission lifecycle -------------------------------------------

func mNormalizeMissionLifecycle() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, r := range migrate.NormalizeLifecycle(root, true, nil) {
				if r.Status != "skipped" {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if pathExists(filepath.Join(root, "kitty-specs")) || pathExists(filepath.Join(root, ".kittify")) {
				return ok()
			}
			return false, "No kitty-specs/ directory found"
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			for _, r := range migrate.NormalizeLifecycle(root, dryRun, nil) {
				if r.Status == "normalized" {
					for _, a := range r.Actions {
						ch = append(ch, r.Slug+": "+a)
					}
				}
				for _, w := range r.Warnings {
					warn = append(warn, r.Slug+": "+w)
				}
				if r.Status == "error" && r.Error != nil && *r.Error != "" {
					errs = append(errs, r.Slug+": "+*r.Error)
				}
			}
			return newResult(ch, warn, errs)
		},
	}
}

// --- 3.2.0rc28 ---------------------------------------------------------------------

var githubDiffEntries = []string{
	"kitty-specs/**/status.json linguist-generated=true", "kitty-specs/**/status.events.jsonl linguist-generated=true",
	"kitty-specs/**/lanes.json linguist-generated=true", "kitty-specs/**/mission-events.jsonl linguist-generated=true",
	"kitty-specs/**/snapshot-latest.json linguist-generated=true", "kitty-specs/**/acceptance-matrix.json linguist-generated=true",
	"kitty-specs/**/occurrence_map.yaml linguist-generated=true", "kitty-specs/**/tasks/** linguist-generated=true",
	"kitty-specs/**/research/evidence-log.csv linguist-generated=true", "kitty-specs/**/research/source-register.csv linguist-generated=true",
	"kitty-specs/**/test-transcripts/** linguist-generated=true", "kitty-specs/**/baseline/** linguist-generated=true",
	"kitty-specs/**/canary-evidence/** linguist-generated=true", ".kittify/workspaces/** linguist-generated=true",
	".kittify/workspaces/** -diff", ".kittify/migrations/** linguist-generated=true", ".kittify/migrations/** -diff",
}

func missingAttributes(root string) []string {
	b, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		return append([]string(nil), githubDiffEntries...)
	}
	have := map[string]bool{}
	for _, l := range pySplitLines(string(b)) {
		have[l] = true
	}
	var out []string
	for _, e := range githubDiffEntries {
		if !have[e] {
			out = append(out, e)
		}
	}
	return out
}

func mGitHubDiffAttributes() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool { return len(missingAttributes(root)) > 0 },
		canApply: func(root string) (bool, string) {
			if !pathExists(root) {
				return false, "Project path does not exist: " + root
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			m := missingAttributes(root)
			if len(m) == 0 {
				return migrationResult{Success: true}
			}
			var ch []string
			verb := "Added"
			if dryRun {
				verb = "Would add"
			}
			for _, e := range m {
				ch = append(ch, fmt.Sprintf("%s .gitattributes entry: %s", verb, e))
			}
			if dryRun {
				return newResult(ch, nil, nil)
			}
			p := filepath.Join(root, ".gitattributes")
			var lines []string
			if b, err := os.ReadFile(p); err == nil {
				lines = pySplitLines(string(b))
			}
			lines = append(lines, m...)
			os.WriteFile(p, []byte(strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n\v\f")+"\n"), 0o666&^umask())
			return newResult(ch, nil, nil)
		},
	}
}

// --- 3.2.0rc35 config migrations ----------------------------------------------------

func loadConfigRoundTrip(root string) (*jsonx.Object, error) {
	b, err := os.ReadFile(kittify.ConfigPath(root))
	if err != nil {
		return nil, err
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return &jsonx.Object{}, nil
	}
	o, isObj := v.(*jsonx.Object)
	if !isObj {
		return nil, nil
	}
	return o, nil
}

var builtinMissionTypes = []string{"documentation", "plan", "research", "software-dev"}

func mActivateBuiltinMissionTypes() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if !pathExists(kittify.ConfigPath(root)) {
				return false
			}
			cfg := loadConfigSafe(root)
			if cfg == nil {
				return false
			}
			v, _ := cfg.Get("mission_type_activations")
			l, isL := v.([]any)
			return !(isL && len(l) > 0)
		},
		canApply: configCanApply,
		apply: func(root string, dryRun bool) migrationResult {
			if !pathExists(kittify.ConfigPath(root)) {
				return newResult([]string{"No .kittify/config.yaml found; nothing to migrate"}, nil, nil)
			}
			cfg, err := loadConfigRoundTrip(root)
			if err != nil {
				return migrationResult{Errors: []string{fmt.Sprintf("Invalid YAML: %v", err)}}
			}
			if cfg == nil {
				return migrationResult{Errors: []string{"config.yaml root must be a mapping"}}
			}
			if v, _ := cfg.Get("mission_type_activations"); func() bool { l, isL := v.([]any); return isL && len(l) > 0 }() {
				return newResult([]string{"mission_type_activations already present; no changes needed"}, nil, nil)
			}
			desc := "Added mission_type_activations: " + pyListRepr(builtinMissionTypes)
			if dryRun {
				return newResult([]string{"Would add: " + desc}, nil, nil)
			}
			l := []any{}
			for _, t := range builtinMissionTypes {
				l = append(l, t)
			}
			cfg.Set("mission_type_activations", l)
			if err := writeConfigRoundTrip(root, cfg); err != nil {
				return migrationResult{Errors: []string{"Failed writing config.yaml: " + pyOSError(err)}}
			}
			return newResult([]string{desc}, nil, nil)
		},
	}
}

func pyListRepr(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = pyRepr(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func mStripSelectionConfig() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if !pathExists(kittify.ConfigPath(root)) {
				return false
			}
			cfg := loadConfigSafe(root)
			if cfg == nil {
				return false
			}
			for _, k := range []string{"agents", "tools"} {
				if s := cfg.GetObject(k); s != nil && s.Has("selection") {
					return true
				}
			}
			return false
		},
		canApply: configCanApply,
		apply: func(root string, dryRun bool) migrationResult {
			if !pathExists(kittify.ConfigPath(root)) {
				return newResult([]string{"No config.yaml found"}, nil, nil)
			}
			cfg, err := loadConfigRoundTrip(root)
			if err != nil {
				return migrationResult{Errors: []string{fmt.Sprintf("Invalid YAML: %v", err)}}
			}
			if cfg == nil {
				return migrationResult{Errors: []string{"config.yaml root must be a mapping"}}
			}
			var ch []string
			for _, k := range []string{"agents", "tools"} {
				if s := cfg.GetObject(k); s != nil && s.Has("selection") {
					if dryRun {
						ch = append(ch, "Would remove: "+k+".selection")
					} else {
						s.Delete("selection")
						ch = append(ch, "Removed: "+k+".selection")
					}
				}
			}
			if len(ch) == 0 {
				return newResult([]string{"No selection block found"}, nil, nil)
			}
			if !dryRun {
				if err := writeConfigRoundTrip(root, cfg); err != nil {
					return migrationResult{Errors: []string{"Failed writing config.yaml: " + pyOSError(err)}}
				}
			}
			return newResult(ch, nil, nil)
		},
	}
}

// --- runtime git hygiene (3.2.0rc35 / 3.2.3) ----------------------------------------

var (
	hygieneEntries      = []string{".kittify/sync-state.json", ".kittify/encoding-provenance/", "kitty-ops/ops-index.jsonl"}
	hygieneTrackedPaths = []string{".kittify/charter/context-state.json", ".kittify/encoding-provenance/global.jsonl", "kitty-ops/ops-index.jsonl"}
)

func gitignoreEntrySet(root string) (map[string]bool, error) {
	c, err := readIgnoreFileText(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, l := range pySplitLines(c) {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(strings.TrimLeft(l, " \t"), "#") {
			out[t] = true
		}
	}
	return out, nil
}

func isInsideWorkTree(root string) bool {
	r, err := gitx.Exec(root, nil, nil, "-C", root, "rev-parse", "--is-inside-work-tree")
	return err == nil && r.Code == 0 && strings.TrimSpace(r.Stdout) == "true"
}

func isTracked(root, rel string) bool {
	if !isInsideWorkTree(root) {
		return false
	}
	r, err := gitx.Exec(root, nil, nil, "ls-files", "--error-unmatch", "--", rel)
	return err == nil && r.Code == 0
}

func mRuntimeGitHygiene() migrationImpl {
	missing := func(root string) ([]string, error) {
		set, err := gitignoreEntrySet(root)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, e := range hygieneEntries {
			if !set[e] {
				out = append(out, e)
			}
		}
		return out, nil
	}
	tracked := func(root string) []string {
		var out []string
		for _, p := range hygieneTrackedPaths {
			if isTracked(root, p) {
				out = append(out, p)
			}
		}
		return out
	}
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			m, err := missing(root)
			if err != nil {
				return false, err
			}
			return len(m) > 0 || len(tracked(root)) > 0, nil
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(root) {
				return false, "Project path does not exist: " + root
			}
			return ok()
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			m, err := missing(root)
			if err != nil {
				return migrationResult{}, err
			}
			if dryRun {
				var ch []string
				for _, e := range m {
					ch = append(ch, "Would add "+e+" to .gitignore")
				}
				for _, p := range tracked(root) {
					ch = append(ch, "Would untrack local runtime file: "+p)
				}
				return newResult(ch, nil, nil), nil
			}
			var ch, errs []string
			modified, err := agents.EnsureGitignoreEntries(root, hygieneEntries, false)
			if err != nil {
				return migrationResult{}, failClosed("%s", err.Error())
			}
			switch {
			case modified && len(m) > 0:
				ch = append(ch, "Added gitignore entries: "+strings.Join(m, ", "))
			case modified:
				ch = append(ch, "Updated .gitignore")
			default:
				ch = append(ch, "gitignore entries already present")
			}
			for _, p := range hygieneTrackedPaths {
				if !isTracked(root, p) {
					continue
				}
				if r, err := gitx.Exec(root, nil, nil, "-C", root, "rm", "--cached", "--", p); err == nil && r.Code == 0 {
					ch = append(ch, "Untracked local runtime file: "+p)
				} else {
					errs = append(errs, "Failed to untrack local runtime file: "+p)
				}
			}
			return newResult(ch, nil, errs), nil
		},
	}
}

// --- 3.2.0rc35 codex to skills ------------------------------------------------------

var legacyCodexFilenames = map[string]bool{"spec-kitty.analyze.md": true, "spec-kitty.charter.md": true, "spec-kitty.implement.md": true,
	"spec-kitty.plan.md": true, "spec-kitty.research.md": true, "spec-kitty.review.md": true, "spec-kitty.specify.md": true,
	"spec-kitty.tasks.md": true, "spec-kitty.tasks-finalize.md": true, "spec-kitty.tasks-outline.md": true, "spec-kitty.tasks-packages.md": true}

func mCodexToSkills() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			pd := filepath.Join(root, ".codex", "prompts")
			if !isDir(pd) {
				return false
			}
			for _, f := range listDir(pd) {
				if isFile(f) && legacyCodexFilenames[filepath.Base(f)] {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false, ".kittify/ directory does not exist (not initialized)"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil {
				return migrationResult{Errors: []string{"Could not load agent config: " + err.Error()}}
			}
			if !containsStr(ac.Available, "codex") {
				return migrationResult{Success: true}
			}
			pd := filepath.Join(root, ".codex", "prompts")
			var owned, third []string
			if pathExists(pd) {
				for _, f := range listDir(pd) {
					if !isFile(f) || filepath.Ext(f) != ".md" {
						continue
					}
					if legacyCodexFilenames[filepath.Base(f)] {
						owned = append(owned, f)
					} else {
						third = append(third, f)
					}
				}
			}
			var ch, warn, errs []string
			if dryRun {
				ch = append(ch, "Would install 11 Codex skill packages under .agents/skills/", fmt.Sprintf("Would delete %d owned .codex/prompts/ file(s)", len(owned)))
				if len(third) > 0 {
					var names []string
					for _, t := range third {
						names = append(names, filepath.Base(t))
					}
					warn = append(warn, fmt.Sprintf("Would preserve %d third-party file(s): %s", len(third), strings.Join(names, ", ")))
				}
				return newResult(ch, warn, nil)
			}
			rep, err := agents.InstallCommandSkills(root, "codex", clock.NowUTCISO())
			if err != nil {
				return migrationResult{Changes: ch, Warnings: warn, Errors: []string{"Skill installation failed: " + err.Error()}}
			}
			ch = append(ch, "Installed 11 Codex skill packages under .agents/skills/")
			for _, p := range rep.Regenerated {
				ch = append(ch, RegeneratedSurfaceLine(filepath.Join(root, filepath.FromSlash(p))))
			}
			if len(owned) > 0 {
				sd := filepath.Join(filepath.Dir(pd), "prompts.superseded")
				var moved []string
				var moveErrs []string
				if err := os.MkdirAll(sd, 0o755); err != nil {
					moveErrs = append(moveErrs, "Could not create .codex/prompts.superseded/: "+pyOSError(err))
				} else {
					for _, p := range owned {
						t := filepath.Join(sd, filepath.Base(p))
						if pathExists(t) {
							if err := os.Remove(p); err != nil {
								moveErrs = append(moveErrs, filepath.Base(p)+": "+pyOSError(err))
							}
							continue
						}
						if err := os.Rename(p, t); err != nil {
							moveErrs = append(moveErrs, filepath.Base(p)+": "+pyOSError(err))
						} else {
							moved = append(moved, t)
						}
					}
				}
				if len(moveErrs) > 0 && len(moved) == 0 {
					return migrationResult{Changes: ch, Warnings: warn, Errors: append(errs, moveErrs...)}
				}
				if len(moved) > 0 {
					ch = append(ch, fmt.Sprintf("Moved %d superseded Codex prompt(s) from .codex/prompts/ to .codex/prompts.superseded/ (new skill packages live in .agents/skills/)", len(moved)))
					lines := []string{"spec-kitty upgrade notice: Codex now reads slash commands from .agents/skills/ (Agent Skills).",
						"Your former Codex prompt files have been moved to ", ".codex/prompts.superseded/ — they are preserved so you can review and ",
						"port any hand edits into the new .agents/skills/spec-kitty.<command>/ ", "packages. Delete .codex/prompts.superseded/ when you are done.", "", "Preserved files:"}
					for _, m := range moved {
						lines = append(lines, "  • "+m)
					}
					fmt.Fprintln(migrationStderr, strings.Join(lines, "\n"))
				}
				if len(moveErrs) > 0 {
					warn = append(warn, "Could not move some .codex/prompts/ files: "+strings.Join(moveErrs, "; "))
				}
			}
			if len(third) > 0 {
				lines := []string{"spec-kitty upgrade notice: the following .codex/prompts/ files were left untouched because they are not Spec Kitty-owned files:"}
				for _, t := range third {
					lines = append(lines, "  • "+t)
				}
				lines = append(lines, "Your Codex integration now reads from .agents/skills/; these files will not be invoked automatically.")
				fmt.Fprintln(migrationStderr, strings.Join(lines, "\n"))
				warn = append(warn, fmt.Sprintf("Preserved %d third-party file(s) in .codex/prompts/", len(third)))
			}
			if pathExists(pd) && len(listDir(pd)) == 0 {
				if err := os.Remove(pd); err != nil {
					warn = append(warn, "Could not remove .codex/prompts/ directory: "+pyOSError(err))
				} else {
					ch = append(ch, "Removed empty .codex/prompts/ directory")
				}
			}
			return migrationResult{Success: true, Changes: ch, Warnings: warn}
		},
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// --- 3.2.0rc35 kittify profile handoff -----------------------------------------------

const (
	handoffSentinel     = "Prepare for Review Hand-off"
	profileLoadSentinel = "Load Agent Profile"
	doThisFirstSentinel = "⚡ Do This First"
)

const implementHandoffBlock = "Before moving this WP to `for_review`, update the `agent_profile` field in the WP\n" +
	"prompt frontmatter to a reviewer profile so the reviewing agent loads the correct\n" +
	"persona automatically.\n\n" +
	"1. Identify the appropriate reviewer profile:\n" +
	"   ```bash\n" +
	"   spec-kitty agent profile list --json | grep reviewer\n" +
	"   ```\n" +
	"   The default reviewer profile is `reviewer-renata`. Use it unless the mission or\n" +
	"   charter specifies a different reviewer.\n\n" +
	"2. Update the WP frontmatter:\n" +
	"   ```yaml\n" +
	"   agent_profile: \"reviewer-renata\"\n" +
	"   role: \"reviewer\"\n" +
	"   ```\n\n" +
	"3. Commit the updated frontmatter together with your implementation changes **before**\n" +
	"   running `spec-kitty agent tasks move-task WPxx --to for_review`.\n\n" +
	"The reviewer will then use `/spk-charter-profile-load` with the reviewer profile and apply\n" +
	"its self-review gates automatically.\n\n"

const reviewProfileLoadBlock = "Before proceeding with the review, load the agent profile from the WP frontmatter\n" +
	"using the `/spk-charter-profile-load` skill (or `spec-kitty agent profile list` to browse\n" +
	"available profiles). Apply the profile's reviewer guidance and self-review gates for\n" +
	"the rest of this review session.\n\n" +
	"The WP frontmatter should already have `agent_profile` set to a reviewer profile\n" +
	"(e.g., `reviewer-renata`) by the implementing agent before it moved the WP to\n" +
	"`for_review`. If `agent_profile` is still set to an implementer profile, load the\n" +
	"implementer profile anyway and note the oversight in your review comments.\n\n"

const taskPromptDoThisFirstBlock = "Use the `/spk-charter-profile-load` skill to load the agent profile specified in the\n" +
	"frontmatter, and behave according to its guidance before parsing the rest of this\n" +
	"prompt.\n\n" +
	"If no profile is specified, run `spec-kitty agent profile list` and select the best\n" +
	"match for this work package's `task_type` and `authoritative_surface`.\n\n" +
	"---\n"

func patchImplement(c string) (string, []string) {
	var ch []string
	if strings.Contains(c, "- `profile`") {
		c = strings.ReplaceAll(c, "- `profile`", "- `agent_profile`")
		ch = append(ch, "fixed stale field: `profile` → `agent_profile`")
	}
	if strings.Contains(c, "- `tool`") {
		c = strings.ReplaceAll(c, "- `tool`", "- `agent`")
		ch = append(ch, "fixed stale field: `tool` → `agent`")
	}
	if strings.Contains(c, "If `profile` is empty") {
		c = strings.ReplaceAll(c, "If `profile` is empty", "If `agent_profile` is empty")
		ch = append(ch, "fixed stale Section-2a fallback sentence")
	}
	if !strings.Contains(c, handoffSentinel) {
		if strings.Contains(c, "## Output") {
			c = strings.Replace(c, "## Output", implementHandoffBlock+"## Output", 1)
		} else {
			c += implementHandoffBlock
		}
		ch = append(ch, "inserted Section 6 'Prepare for Review Hand-off'")
	}
	return c, ch
}

func patchReview(c string) (string, []string) {
	if strings.Contains(c, profileLoadSentinel) {
		return c, nil
	}
	if strings.Contains(c, "### 3.") {
		c = strings.Replace(c, "### 3.", reviewProfileLoadBlock+"### 3.", 1)
	} else {
		c += reviewProfileLoadBlock
	}
	return c, []string{"inserted Section 2a 'Load Agent Profile'"}
}

func patchTaskPromptTemplate(c string) (string, []string) {
	var ch []string
	if strings.HasPrefix(c, "---") {
		if end := strings.Index(c[3:], "---"); end >= 0 {
			end += 3
			front := c[3:end]
			nf := front
			if !strings.Contains(front, "agent_profile:") {
				nf = strings.TrimRight(nf, " \t\r\n\v\f") + "\nagent_profile: \"\"\n"
				ch = append(ch, "added agent_profile field to frontmatter")
			}
			if !strings.Contains(front, "role:") {
				nf = strings.TrimRight(nf, " \t\r\n\v\f") + "\nrole: \"\"\n"
				ch = append(ch, "added role field to frontmatter")
			}
			if nf != front {
				c = "---" + nf + "---" + c[end+3:]
			}
		}
	}
	if !strings.Contains(c, doThisFirstSentinel) {
		lines := strings.Split(c, "\n")
		at := -1
		for i, l := range lines {
			if strings.HasPrefix(l, "# ") && i > 0 {
				at = i + 1
				break
			}
		}
		if at >= 0 {
			lines = append(lines[:at], append([]string{taskPromptDoThisFirstBlock}, lines[at:]...)...)
			c = strings.Join(lines, "\n")
		} else {
			c += taskPromptDoThisFirstBlock
		}
		ch = append(ch, "inserted '⚡ Do This First: Load Agent Profile' section")
	}
	return c, ch
}

func mKittifyProfileHandoff() migrationImpl {
	impl := func(root string) string {
		return filepath.Join(root, ".kittify", "missions", "software-dev", "command-templates", "implement.md")
	}
	skillImpl := func(root string) string {
		return filepath.Join(root, ".agents", "skills", "spec-kitty.implement", "SKILL.md")
	}
	return migrationImpl{
		detect: func(root string) bool {
			for _, p := range []string{impl(root), skillImpl(root)} {
				if b, err := os.ReadFile(p); err == nil && !strings.Contains(string(b), handoffSentinel) {
					return true
				}
			}
			return false
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch []string
			patch := func(p string, fn func(string) (string, []string), readOnly bool) {
				b, err := os.ReadFile(p)
				if err != nil {
					return
				}
				nc, c := fn(string(b))
				if len(c) > 0 && !dryRun {
					writeGeneratedFile(p, []byte(nc), readOnly)
				}
				for _, x := range c {
					ch = append(ch, p+": "+x)
				}
			}
			ct := filepath.Join(root, ".kittify", "missions", "software-dev", "command-templates")
			patch(filepath.Join(ct, "implement.md"), patchImplement, false)
			patch(filepath.Join(ct, "review.md"), patchReview, false)
			patch(filepath.Join(root, ".kittify", "overrides", "missions", "software-dev", "templates", "task-prompt-template.md"), patchTaskPromptTemplate, false)
			patch(skillImpl(root), patchImplement, true)
			patch(filepath.Join(root, ".agents", "skills", "spec-kitty.review", "SKILL.md"), patchReview, true)
			return migrationResult{Success: true, Changes: ch}
		},
	}
}

// --- 3.2.0rc35 pi/letta backfill -----------------------------------------------------

var piLettaGitignore = map[string]string{"pi": ".pi/", "letta": ".letta/"}

func agentCommandSkillsComplete(root, agent string) bool {
	entries := agents.LoadCommandSkillsManifest(root)
	for _, c := range agents.CommandSkillCommands {
		rel := agents.CommandSkillPath(c)
		if !pathExists(filepath.Join(root, filepath.FromSlash(rel))) {
			return false
		}
		e, ok := entries[rel]
		if !ok || !containsStr(e.Agents, agent) {
			return false
		}
	}
	return true
}

func mPiLettaBackfill() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false, nil
			}
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil {
				return false, nil
			}
			var pl []string
			for _, a := range []string{"letta", "pi"} {
				if containsStr(ac.Available, a) {
					pl = append(pl, a)
				}
			}
			if len(pl) == 0 {
				return false, nil
			}
			gi, err := readIgnoreFileText(filepath.Join(root, ".gitignore"))
			if err != nil {
				return false, err
			}
			for _, a := range pl {
				if !strings.Contains(gi, piLettaGitignore[a]) {
					return true, nil
				}
			}
			for _, a := range pl {
				if !agentCommandSkillsComplete(root, a) {
					return true, nil
				}
			}
			return false, nil
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(root) {
				return false, "Project path does not exist: " + root
			}
			return ok()
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil {
				return migrationResult{Errors: []string{"Could not load agent config: " + err.Error()}}, nil
			}
			var ch, warn []string
			for _, a := range []string{"pi", "letta"} {
				if !containsStr(ac.Available, a) {
					continue
				}
				gi, err := readIgnoreFileText(filepath.Join(root, ".gitignore"))
				if err != nil {
					return migrationResult{}, err
				}
				if strings.Contains(gi, piLettaGitignore[a]) {
					continue
				}
				if dryRun {
					ch = append(ch, "Would add "+piLettaGitignore[a]+" to .gitignore")
				} else {
					agents.EnsureGitignoreEntries(root, []string{piLettaGitignore[a]}, false)
					ch = append(ch, "Added "+piLettaGitignore[a]+" to .gitignore")
				}
			}
			for _, a := range []string{"pi", "letta"} {
				if !containsStr(ac.Available, a) || agentCommandSkillsComplete(root, a) {
					continue
				}
				if dryRun {
					ch = append(ch, "Would repair skill pack for "+a)
					continue
				}
				rep, err := agents.InstallCommandSkills(root, a, clock.NowUTCISO())
				if err != nil {
					warn = append(warn, fmt.Sprintf("Skill repair for %s skipped: %s", a, err))
					continue
				}
				ch = append(ch, "Repaired skill pack for "+a)
				for _, p := range rep.Regenerated {
					ch = append(ch, RegeneratedSurfaceLine(filepath.Join(root, filepath.FromSlash(p))))
				}
			}
			return migrationResult{Success: true, Changes: ch, Warnings: warn}, nil
		},
	}
}

// --- 3.2.0rc35 repository-root terminology ---------------------------------------------

var repoRootReplacements = [][2]string{
	{"Planning happens in main for ALL missions.", "Planning happens in the repository root checkout for all missions. The mission target branch comes from branch-context."},
	{"Task generation happens in main for ALL missions.", "Task generation happens in the repository root checkout for all missions and commits to the mission's target branch."},
	{"project root checkout root", "repository root checkout"}, {"Project root checkout root", "Repository root checkout"},
	{"main repository root", "repository root checkout"}, {"Main repository root", "Repository root checkout"},
	{"project root checkout", "repository root checkout"}, {"Project root checkout", "Repository root checkout"},
	{"main repository", "repository root checkout"}, {"Main repository", "Repository root checkout"},
	{"main repo", "repository root checkout"}, {"Main repo", "Repository root checkout"},
}

var repoRootMarkers = []string{"project root checkout", "main repository root", "main repository", "main repo",
	"Planning happens in main for ALL missions.", "Task generation happens in main for ALL missions."}

func mRepositoryRootCheckoutTerminology() migrationImpl {
	needs := func(root string) ([]string, error) {
		return commandFilesMatching(root, func(c string) bool {
			l := strings.ToLower(c)
			for _, m := range repoRootMarkers {
				if strings.Contains(l, strings.ToLower(m)) {
					return true
				}
			}
			return false
		})
	}
	return migrationImpl{
		detectE: func(root string) (bool, error) { h, err := needs(root); return len(h) > 0, err },
		canApply: func(root string) (bool, string) {
			if h, _ := needs(root); len(h) > 0 {
				return ok()
			}
			return false, "No generated command files need repository-root terminology updates"
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			hits, err := needs(root)
			if err != nil {
				return migrationResult{}, err
			}
			if len(hits) == 0 {
				return newResult([]string{"No files needed updating"}, nil, nil), nil
			}
			var changed, preserved, warn []string
			for _, f := range hits {
				b, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				c := string(b)
				for _, r := range repoRootReplacements {
					c = strings.ReplaceAll(c, r[0], r[1])
				}
				if c == string(b) {
					continue
				}
				rel, _ := filepath.Rel(root, f)
				v := guardDestructiveOverwrite(f, root, true, false, defaultCanonicalProver(), "")
				if !v.proceed {
					preserved = append(preserved, rel)
					warn = append(warn, v.diagnostic)
					continue
				}
				if !dryRun {
					writeGeneratedFile(f, []byte(c), true)
				}
				changed = append(changed, rel)
			}
			if len(changed) == 0 {
				changed = []string{"Updated generated command files to use 'repository root checkout' terminology"}
			}
			return migrationResult{Success: true, Changes: changed, Warnings: warn, Preserved: preserved, ManualReview: len(preserved) > 0}, nil
		},
	}
}

// --- 3.2.0rc35 update planning templates ----------------------------------------------

func mUpdatePlanningTemplates() migrationImpl {
	cmds := []string{"tasks-outline", "tasks-packages"}
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				for _, c := range cmds {
					for _, f := range globFiles(ad, "spec-kitty."+c+".*") {
						if fileContainsAny(f, []string{"Create `tasks.md`"}) {
							return true, nil
						}
					}
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var warn []string
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			tdir := filepath.Join(assetsDisplayRoot(), filepath.FromSlash(agents.CommandTemplatesDir))
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				if _, ok := agents.AgentDirToKey[d.root]; !ok {
					continue
				}
				// The packaged templates live at <command>/prompt.md; the
				// migration looks for <command>.md and skips every command.
				for _, c := range cmds {
					warn = append(warn, fmt.Sprintf("Template not found for command '%s' in %s — skipping", c, tdir))
				}
			}
			return migrationResult{Success: true, Warnings: warn}, nil
		},
	}
}

// assetsDisplayRoot is where upstream's installed package data lives; the Go
// binary embeds it, so a stable placeholder root names it in messages.
func assetsDisplayRoot() string { return "<spec-kitty>" }

// --- session presence (rc39 / 3_3_0) ---------------------------------------------------

func migrationPresence(root string) agents.Presence {
	c := presenceContent(root)
	if PlanCompat(Invocation{CommandPath: []string{"session-start"}, RawArgs: []string{"session-start"}}, root).Decision == DecisionBlockMigration {
		c.Health = "migration-required"
	}
	return c
}

func extractOrientation(root, rules string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rules)))
	if err != nil {
		return "", false
	}
	t := string(b)
	s := strings.Index(t, agents.SectionOpen)
	if s < 0 {
		return "", false
	}
	e := strings.Index(t[s:], agents.SectionClose)
	if e < 0 {
		return "", false
	}
	e = s + e + len(agents.SectionClose)
	if e < len(t) && t[e] == '\n' {
		e++
	}
	return t[s:e], true
}

func staleOrientationKeys(root, rendered string) []string {
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return nil
	}
	var stale []string
	for _, k := range ac.Available {
		w := agents.WriterFor(k)
		if !w.HasPresence(root) || w.RulesPath == "" || w.Claude {
			continue
		}
		if ex, _ := extractOrientation(root, w.RulesPath); ex != rendered {
			stale = append(stale, k)
		}
	}
	return stale
}

func mRefreshOrientationBlock() migrationImpl {
	return migrationImpl{
		noWorktrees: true,
		detect: func(root string) bool {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false
			}
			return len(staleOrientationKeys(root, migrationPresence(root).Render())) > 0
		},
		canApply: kittifyRequired,
		apply: func(root string, dryRun bool) migrationResult {
			c := migrationPresence(root)
			stale := staleOrientationKeys(root, c.Render())
			var ch []string
			if dryRun {
				for _, k := range stale {
					ch = append(ch, "Would refresh stale orientation block for harness: "+k)
				}
				return migrationResult{Success: true, Changes: ch}
			}
			for _, k := range stale {
				agents.WriterFor(k).WriteOrientation(root, c)
				ch = append(ch, "Refreshed orientation block for harness: "+k)
			}
			return migrationResult{Success: true, Changes: ch}
		},
	}
}

func kittifyRequired(root string) (bool, string) {
	if !isDir(filepath.Join(root, ".kittify")) {
		return false, ".kittify/ directory does not exist (not initialized)"
	}
	return ok()
}

func pendingPresence(root string) []string {
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, k := range ac.Available {
		if k == "claude" {
			continue
		}
		w := agents.WriterFor(k)
		if w.CanWrite(root) && !w.HasPresence(root) {
			out = append(out, k)
		}
	}
	return out
}

func mSessionPresenceAllHarnesses() migrationImpl {
	return migrationImpl{
		noWorktrees: true,
		detect: func(root string) bool {
			return isDir(filepath.Join(root, ".kittify")) && len(pendingPresence(root)) > 0
		},
		canApply: kittifyRequired,
		apply: func(root string, dryRun bool) migrationResult {
			pending := pendingPresence(root)
			var ch []string
			if dryRun {
				for _, k := range pending {
					ch = append(ch, "Would write session presence orientation for harness: "+k)
				}
				return migrationResult{Success: true, Changes: ch}
			}
			c := migrationPresence(root)
			for _, k := range pending {
				w := agents.WriterFor(k)
				if w.HasPresence(root) {
					continue
				}
				w.WriteOrientation(root, c)
				ch = append(ch, "Wrote session presence orientation for harness: "+k)
			}
			return migrationResult{Success: true, Changes: ch}
		},
	}
}

func mSessionPresenceClaudeCode() migrationImpl {
	return migrationImpl{
		noWorktrees: true,
		detect: func(root string) bool {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false
			}
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil || !containsStr(ac.Available, "claude") {
				return false
			}
			return !agents.WriterFor("claude").HasPresence(root)
		},
		canApply: kittifyRequired,
		apply: func(root string, dryRun bool) migrationResult {
			if dryRun {
				return migrationResult{Success: true, Changes: []string{"Would write orientation to .claude/CLAUDE.md and register SessionStart + Stop hooks"}}
			}
			agents.WriterFor("claude").WriteOrientation(root, migrationPresence(root))
			return migrationResult{Success: true, Changes: []string{"Wrote spec-kitty orientation to .claude/CLAUDE.md",
				"Registered spec-kitty session-start SessionStart hook", "Registered spec-kitty session-stop Stop hook"}}
		},
	}
}

// --- 3_3_0 op record schema v2 -----------------------------------------------------------

type opPlan struct {
	action   string // skip | rewrite | delete
	lines    []string
	warnings []string
	reason   string
}

func strOrEmpty(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func planOpFile(p string) opPlan {
	b, err := os.ReadFile(p)
	if err != nil {
		return opPlan{action: "delete", reason: "unreadable or empty file"}
	}
	var lines []string
	for _, l := range pySplitLines(string(b)) {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return opPlan{action: "delete", reason: "unreadable or empty file"}
	}
	var parsed []*jsonx.Object
	for _, l := range lines {
		v, err := jsonx.Decode([]byte(l))
		if err != nil {
			return opPlan{action: "delete", reason: "unparseable JSON line"}
		}
		o, isObj := v.(*jsonx.Object)
		if !isObj {
			return opPlan{action: "delete", reason: "non-object JSONL line"}
		}
		parsed = append(parsed, o)
	}
	si := -1
	for i, d := range parsed {
		if strOrEmpty(d, "event") == "started" {
			si = i
			break
		}
	}
	if si < 0 {
		return opPlan{action: "delete", reason: "missing started event"}
	}
	started := parsed[si]
	invID, profID := strOrEmpty(started, "invocation_id"), strOrEmpty(started, "profile_id")
	if invID == "" || profID == "" {
		return opPlan{action: "delete", reason: "started event lacks invocation_id/profile_id"}
	}
	isV2C := func(d *jsonx.Object) bool { _, err := invocation.ParseCompleted(d); return err == nil }
	allV2 := true
	for _, d := range parsed {
		if strOrEmpty(d, "event") == "completed" && !isV2C(d) {
			allV2 = false
		}
	}
	if _, err := invocation.ParseStarted(started); err == nil && allV2 {
		return opPlan{action: "skip"}
	}
	var warn []string
	startedAt := strOrEmpty(started, "started_at")
	or := func(s, d string) string {
		if s == "" {
			return d
		}
		return s
	}
	gca := true
	if v, ok := started.Get("governance_context_available"); ok {
		gca = pyjsonTruthy(v)
	}
	payload := jsonx.NewObject("event", "started", "invocation_id", invID, "profile_id", profID,
		"action", or(strOrEmpty(started, "action"), "unrecorded"), "request_text", strOrEmpty(started, "request_text"),
		"actor", or(strOrEmpty(started, "actor"), "unrecorded"), "mode_of_work", or(strOrEmpty(started, "mode_of_work"), "task_execution"),
		"governance_context_hash", strOrEmpty(started, "governance_context_hash"), "governance_context_available", gca)
	for _, k := range []string{"router_confidence"} {
		if v, ok := started.Get(k); ok {
			payload.Set(k, v)
		}
	}
	payload.Set("started_at", startedAt)
	for _, k := range []string{"mission_id", "wp_id"} {
		if v, ok := started.Get(k); ok {
			payload.Set(k, v)
		}
	}
	se, err := invocation.ParseStarted(payload)
	if err != nil {
		return opPlan{action: "delete", reason: "started event not representable as v2"}
	}
	var out []string
	for i, d := range parsed {
		switch ev := strOrEmpty(d, "event"); {
		case ev == "started" && i == si:
			out = append(out, se.Line())
		case ev == "completed":
			if isV2C(d) {
				out = append(out, lines[i])
				continue
			}
			ca := strOrEmpty(d, "completed_at")
			if ca == "" {
				ca = startedAt
				warn = append(warn, filepath.Base(p)+": completed_at missing; fell back to started_at")
			}
			oc := strOrEmpty(d, "outcome")
			if oc != "done" && oc != "failed" && oc != "abandoned" {
				oc = "abandoned"
			}
			cp := jsonx.NewObject("event", "completed", "invocation_id", or(strOrEmpty(d, "invocation_id"), invID), "completed_at", ca,
				"outcome", oc, "closed_by", "agent")
			if v, ok := d.Get("evidence_ref"); ok {
				cp.Set("evidence_ref", v)
			} else {
				cp.Set("evidence_ref", nil)
			}
			ce, err := invocation.ParseCompleted(cp)
			if err != nil {
				return opPlan{action: "delete", reason: "completed event not representable as v2"}
			}
			out = append(out, ce.Line())
		default:
			out = append(out, lines[i])
		}
	}
	return opPlan{action: "rewrite", lines: out, warnings: warn}
}

var opExcluded = map[string]bool{"ops-index.jsonl": true, "lifecycle.jsonl": true, "propagation-errors.jsonl": true}

func opFiles(root string) []string {
	var out []string
	for _, f := range globFiles(filepath.Join(root, "kitty-ops"), "*.jsonl") {
		if !opExcluded[filepath.Base(f)] {
			out = append(out, f)
		}
	}
	return out
}

func mOpRecordSchemaV2() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, f := range opFiles(root) {
				if planOpFile(f).action != "skip" {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !isDir(filepath.Join(root, "kitty-ops")) {
				return false, "kitty-ops/ directory does not exist"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, deleted []string
			for _, f := range opFiles(root) {
				pl := planOpFile(f)
				name := filepath.Base(f)
				switch pl.action {
				case "skip":
					continue
				case "delete":
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would delete %s (%s)", name, pl.reason))
					} else {
						os.Remove(f)
						deleted = append(deleted, name)
						ch = append(ch, fmt.Sprintf("Deleted unsalvageable %s (%s)", name, pl.reason))
					}
					continue
				}
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would rewrite %s to v2 schema", name))
				} else {
					var b strings.Builder
					for _, l := range pl.lines {
						b.WriteString(l + "\n")
					}
					tmp := f + ".tmp"
					os.WriteFile(tmp, []byte(b.String()), 0o666&^umask())
					os.Rename(tmp, f)
					ch = append(ch, fmt.Sprintf("Rewrote %s to v2 schema", name))
				}
				warn = append(warn, pl.warnings...)
			}
			if len(deleted) > 0 {
				warn = append(warn, fmt.Sprintf("Deleted %d unsalvageable Op record file(s): %s", len(deleted), strings.Join(deleted, ", ")))
			}
			return migrationResult{Success: true, Changes: ch, Warnings: warn}
		},
	}
}

// --- rc43 retire profile-context ------------------------------------------------------------

func mRetireProfileContextCommand() migrationImpl {
	base := mProfileContextDeployment()
	return base
}

// --- 0_9_3 surface repair wiring / 0_9_4 roo -----------------------------------------------

func mSurfaceRepairWiring() migrationImpl {
	marker := func(root string) string { return filepath.Join(root, ".kittify", "surface_repair_wired") }
	return migrationImpl{
		noWorktrees: true,
		detect: func(root string) bool {
			return pathExists(filepath.Join(root, ".kittify")) && !pathExists(marker(root))
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(filepath.Join(root, ".kittify")) {
				return false, "No .kittify directory found; project is not initialized"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			if !dryRun {
				if err := os.WriteFile(marker(root), []byte("wired\n"), 0o666&^umask()); err != nil {
					return migrationResult{Errors: []string{"Could not write wiring marker: " + pyOSError(err)}}
				}
			}
			n := 0
			if ac, err := kittify.LoadAgentConfig(root); err == nil && len(ac.Available) > 0 {
				n = len(CollectToolSurfaces(root, ac.Available, true, nil))
			}
			return migrationResult{Success: true, Changes: []string{fmt.Sprintf("surface_repair_wiring: probed %d tool surface(s)", n)}}
		},
	}
}

func rooConfigured(root string) bool {
	cfg, err := loadConfigRoundTrip(root)
	if err != nil || cfg == nil {
		return false
	}
	var sect *jsonx.Object
	if v, _ := cfg.Get("agents"); pyjsonTruthy(v) {
		sect, _ = v.(*jsonx.Object)
	} else if v, _ := cfg.Get("tools"); pyjsonTruthy(v) {
		sect, _ = v.(*jsonx.Object)
	}
	if sect == nil {
		return false
	}
	l, _ := sect.Get("available")
	if xs, ok := l.([]any); ok {
		for _, x := range xs {
			if pyStrValue(x) == "roo" {
				return true
			}
		}
	}
	return false
}

func mRooDeprecation() migrationImpl {
	return migrationImpl{
		noWorktrees: true,
		detect:      func(root string) bool { return pathExists(filepath.Join(root, ".roo")) || rooConfigured(root) },
		canApply:    func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn []string
			if pathExists(filepath.Join(root, ".roo")) {
				printRooNotice()
				ch = append(ch, fmt.Sprintf("Roo Code deprecation notice emitted (.roo/ preserved at %s)", filepath.Join(root, ".roo")))
			}
			if rooConfigured(root) {
				if dryRun {
					ch = append(ch, "dry-run: would remove 'roo' from .kittify/config.yaml")
				} else {
					cfg, _ := loadConfigRoundTrip(root)
					sect := cfg.GetObject("agents")
					if sect == nil || sect.Len() == 0 {
						sect = cfg.GetObject("tools")
					}
					if l, ok := sect.Get("available"); ok {
						var keep []any
						for _, x := range l.([]any) {
							if pyStrValue(x) != "roo" {
								keep = append(keep, x)
							}
						}
						if keep == nil {
							keep = []any{}
						}
						sect.Set("available", keep)
					}
					writeConfigRoundTrip(root, cfg)
					ch = append(ch, "Removed 'roo' from .kittify/config.yaml (agent no longer supported)")
				}
			}
			return migrationResult{Success: true, Changes: ch, Warnings: warn}
		},
	}
}

func printRooNotice() {
	panelStderr("[yellow]Roo Code has been discontinued as of 2026-05-15.[/yellow]\n"+
		"The [bold].roo/[/bold] directory has been preserved but Roo Code\n"+
		"will no longer be maintained or generated by spec-kitty.\n"+
		"You may safely delete [bold].roo/[/bold] if you are not using Roo Code.\n\n"+
		"Remove the agent from your project config:\n"+
		"  [cyan]spec-kitty agent config remove roo[/cyan]", "Roo Code Deprecation Notice")
}

// --- rc45 retire standalone skill surface ---------------------------------------------------

func retiredSkillPaths(root string) []string {
	roots := map[string]bool{}
	for _, cfg := range agents.SkillConfigs {
		if cfg.Class == agents.SkillClassWrapper {
			continue
		}
		for _, r := range cfg.Roots {
			roots[filepath.Join(root, strings.Trim(r, "/"))] = true
		}
	}
	var out []string
	for _, r := range sortedKeys(roots) {
		d := filepath.Join(r, "spec-kitty.advise")
		if lexists(d) {
			out = append(out, d)
		}
	}
	return out
}

func pathHasRetiredSkill(p string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == "spec-kitty.advise" {
			return true
		}
	}
	return false
}

func mRetireStandaloneSkillSurface() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if len(retiredSkillPaths(root)) > 0 {
				return true
			}
			if m := agents.LoadSkillsManifest(root); m != nil {
				for _, e := range m.Entries {
					if e.SkillName == "spec-kitty.advise" || pathHasRetiredSkill(e.InstalledPath) {
						return true
					}
				}
			}
			for p := range agents.LoadCommandSkillsManifest(root) {
				if pathHasRetiredSkill(p) {
					return true
				}
			}
			return false
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			pr := manifestProver(true, true)
			for _, d := range retiredSkillPaths(root) {
				rel, _ := filepath.Rel(root, d)
				if dryRun {
					ch = append(ch, "Would remove retired skill surface "+rel)
					continue
				}
				v, err := guardDestructiveRemoval(d, root, pr, isDir(d) && !isSymlink(d), "", false)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to remove %s: %s", rel, pyOSError(err)))
				} else if v.owned {
					ch = append(ch, "Removed retired skill surface "+rel)
				} else {
					warn = append(warn, v.diagnostic)
				}
			}
			prunable := func(name, p string) bool {
				return (name == "spec-kitty.advise" || pathHasRetiredSkill(p)) && !pathExists(filepath.Join(root, filepath.FromSlash(p)))
			}
			if m := agents.LoadSkillsManifest(root); m != nil {
				var removed []string
				var keep []agents.SkillEntry
				for _, e := range m.Entries {
					if prunable(e.SkillName, e.InstalledPath) {
						removed = append(removed, e.InstalledPath)
					} else {
						keep = append(keep, e)
					}
				}
				sort.Strings(removed)
				if len(removed) > 0 {
					verb := "Pruned"
					if dryRun {
						verb = "Would prune"
					} else {
						m.Entries = keep
						agents.SaveSkillsManifest(root, m, clock.NowUTCISO())
					}
					for _, p := range removed {
						ch = append(ch, verb+" retired skill manifest entry "+p)
					}
				}
			}
			if entries := agents.LoadCommandSkillsManifest(root); pathExists(filepath.Join(root, ".kittify", agents.CommandSkillsManifestFile)) {
				var removed []string
				for p := range entries {
					if pathHasRetiredSkill(p) && !pathExists(filepath.Join(root, filepath.FromSlash(p))) {
						removed = append(removed, p)
					}
				}
				sort.Strings(removed)
				if len(removed) > 0 {
					verb := "Pruned"
					if dryRun {
						verb = "Would prune"
					} else {
						for _, p := range removed {
							delete(entries, p)
						}
						agents.SaveCommandSkillsManifest(root, entries)
					}
					for _, p := range removed {
						ch = append(ch, verb+" retired command skills manifest entry "+p)
					}
				}
			}
			if len(ch) == 0 && len(warn) == 0 && len(errs) == 0 {
				ch = append(ch, "Retired standalone governance skill surfaces absent")
			}
			return newResult(ch, warn, errs)
		},
	}
}

// --- 3.2.8 provision .kitty.env ---------------------------------------------------------------

var governedOperatorVars = []string{"SPEC_KITTY_HOME", "SPEC_KITTY_NON_INTERACTIVE", "SPEC_KITTY_FORCE_INTERACTIVE", "SPEC_KITTY_SKIP_PRE_REVIEW_GATE",
	"SPEC_KITTY_NO_BANNER", "SPEC_KITTY_NO_NAG", "SPEC_KITTY_NO_UPGRADE_CHECK"}

func kittyEnvContent() string {
	lines := []string{"# Spec Kitty per-repo operator environment file.", "# Provisioned by migration 3.2.8_provision_kitty_env -- see",
		"# contracts/kitty-env-loader.md and contracts/provenance-and-channel.md.", "#",
		"# SPEC_KITTY_PACKS_ROOT is intentionally never written here: an", "# always-set value flips the TEMPLATE_ROOT gate (kernel/paths.py).", ""}
	seeded := false
	for _, v := range governedOperatorVars {
		if val, ok := os.LookupEnv(v); ok {
			lines = append(lines, v+"="+val)
			seeded = true
		}
	}
	if seeded {
		lines = append(lines, "")
	}
	lines = append(lines, "# Secret-shaped vars are never auto-seeded by value -- fill in by hand:", "# SPEC_KITTY_ORG_TOKEN=", "# SPEC_KITTY_ORG_AUTH_HEADER=", "")
	return strings.Join(lines, "\n")
}

func ignoreEntriesNoFollow(p string) (map[string]bool, error) {
	out := map[string]bool{}
	if isSymlink(p) {
		return nil, fmt.Errorf("Refusing to open %s: [Errno 40] Too many levels of symbolic links: %s", p, pyRepr(p))
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, l := range pySplitLines(strings.TrimPrefix(string(b), "\ufeff")) {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(strings.TrimLeft(l, " \t"), "#") {
			out[t] = true
		}
	}
	return out, nil
}

func claudeignoreSymlinkError(p string) error {
	if !isSymlink(p) {
		return nil
	}
	t, err := os.Readlink(p)
	if err != nil {
		return failClosed(".claudeignore is a symlink; refusing to read or write through it: %s (could not resolve target: %s)", p, pyOSError(err))
	}
	return failClosed(".claudeignore is a symlink to %s; refusing to read or write through it: %s", pyRepr(t), p)
}

func kittyEnvState(root string) (envMissing, ptrMissing, giMissing, ciMissing bool, err error) {
	envMissing = !pathExists(filepath.Join(root, ".kittify", ".kitty.env"))
	ptrMissing = true
	if pathExists(kittify.ConfigPath(root)) {
		if cfg, cerr := loadConfigRoundTrip(root); cerr == nil && cfg != nil {
			v, _ := cfg.Get("env_file")
			ptrMissing = !pyjsonTruthy(v)
		}
	}
	gi, gerr := ignoreEntriesNoFollow(filepath.Join(root, ".gitignore"))
	if gerr != nil {
		return false, false, false, false, gerr
	}
	giMissing = !gi[".kittify/.kitty.env"]
	cp := filepath.Join(root, ".claudeignore")
	if e := claudeignoreSymlinkError(cp); e != nil {
		return false, false, false, false, e
	}
	ci, cerr := ignoreEntriesNoFollow(cp)
	if cerr != nil {
		return false, false, false, false, cerr
	}
	ciMissing = !ci[".kittify/.kitty.env"]
	return
}

func mProvisionKittyEnv() migrationImpl {
	detect := func(root string) (bool, error) {
		a, b, c, d, err := kittyEnvState(root)
		return a || b || c || d, err
	}
	return migrationImpl{
		noWorktrees: true,
		detectE:     detect,
		canApply: func(root string) (bool, string) {
			if d, _ := detect(root); d {
				return ok()
			}
			return false, ".kitty.env already provisioned (file, config pointer, ignore coverage all present)"
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch []string
			env, ptr, gi, ci, err := kittyEnvState(root)
			if err != nil {
				return migrationResult{}, err
			}
			if env {
				if !dryRun {
					p := filepath.Join(root, ".kittify", ".kitty.env")
					os.MkdirAll(filepath.Dir(p), 0o755)
					os.WriteFile(p, []byte(kittyEnvContent()), 0o666&^umask())
				}
				ch = append(ch, "Created .kittify/.kitty.env")
			}
			if ptr {
				if !dryRun {
					cfg, _ := loadConfigRoundTrip(root)
					if cfg == nil {
						cfg = &jsonx.Object{}
					}
					cfg.Set("env_file", "${SPEC_KITTY_HOME}/.kitty.env")
					writeConfigRoundTrip(root, cfg)
				}
				ch = append(ch, "Registered env_file: ${SPEC_KITTY_HOME}/.kitty.env in config.yaml")
			}
			if _, _, gi2, _, _ := kittyEnvState(root); gi && (dryRun || gi2) {
				if !dryRun {
					agents.EnsureGitignoreEntries(root, []string{".kittify/.kitty.env"}, false)
				}
				ch = append(ch, "Added .kittify/.kitty.env to .gitignore")
			}
			if ci {
				if !dryRun {
					p := filepath.Join(root, ".claudeignore")
					var lines []string
					if b, err := os.ReadFile(p); err == nil {
						lines = pySplitLines(strings.TrimPrefix(string(b), "\ufeff"))
					}
					if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
						lines = append(lines, "")
					}
					lines = append(lines, ".kittify/.kitty.env", "")
					mode := os.FileMode(0o666) &^ umask()
					if st, err := os.Stat(p); err == nil {
						mode = st.Mode().Perm()
					}
					tmp := filepath.Join(root, fmt.Sprintf(".claudeignore.%d.tmp", os.Getpid()))
					os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), mode)
					os.Chmod(tmp, mode)
					os.Rename(tmp, p)
				}
				ch = append(ch, "Added .kittify/.kitty.env to .claudeignore")
			}
			if len(ch) == 0 {
				ch = []string{"already provisioned"}
			}
			return migrationResult{Success: true, Changes: ch}, nil
		},
	}
}

// --- migrate_lifecycle_envelope -----------------------------------------------------------------

var lifecycleEventTypes = map[string]bool{"ProjectInitialized": true, "MissionCreated": true, "SpecifyStarted": true, "SpecifyCompleted": true,
	"PlanStarted": true, "PlanCompleted": true, "TasksStarted": true, "TasksCompleted": true, "WPCreated": true, "ReviewerSelfApproval": true,
	"MissionReopened": true, "FollowUpRecorded": true}

var synthesizedEnvelopeKeys = []string{"causation_id", "correlation_id", "data_tier", "node_id", "lamport_clock", "build_id"}

type envelopeManifest struct {
	migrated int
	refused  string
	lines    []string
}

func computeEnvelope(logPath string) (envelopeManifest, error) {
	var m envelopeManifest
	b, err := os.ReadFile(logPath)
	if err != nil {
		return m, err
	}
	for n, raw := range pySplitLines(string(b)) {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		v, err := jsonx.Decode([]byte(raw))
		if err != nil {
			return m, fmt.Errorf("Invalid JSON on line %d: %v", n+1, err)
		}
		row, isObj := v.(*jsonx.Object)
		if !isObj || !lifecycleEventTypes[strOrEmpty(row, "event_type")] {
			m.lines = append(m.lines, raw)
			continue
		}
		if strOrEmpty(row, "event_type") == "ReviewerSelfApproval" {
			m.lines = append(m.lines, raw)
			continue
		}
		strict := strOrEmpty(row, "schema_version") == "3.0.0" && !row.Has("aggregate_type")
		for _, k := range synthesizedEnvelopeKeys {
			strict = strict && row.Has(k)
		}
		if strict {
			m.lines = append(m.lines, raw)
			continue
		}
		pu, ok := row.Get("project_uuid")
		if !ok || pu == nil {
			m.lines = append(m.lines, raw)
			continue
		}
		out := &jsonx.Object{}
		for _, kv := range row.Pairs() {
			if kv.Key != "aggregate_type" {
				out.Set(kv.Key, kv.Value)
			}
		}
		out.Set("schema_version", "3.0.0")
		node := kittify.GenerateNodeID()
		out.Set("causation_id", nil)
		eid, _ := row.Get("event_id")
		out.Set("correlation_id", eid)
		out.Set("data_tier", 0)
		out.Set("node_id", node)
		out.Set("lamport_clock", 0)
		out.Set("build_id", kittify.DeriveBuildID(pyStrValue(pu), node))
		m.lines = append(m.lines, jsonx.Dumps(out, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true}))
		m.migrated++
	}
	return m, nil
}

func lifecycleLogPaths(root string) []string {
	var out []string
	if p := kittify.EventsPath(root); pathExists(p) {
		out = append(out, p)
	}
	for _, f := range featureDirs(root) {
		if p := filepath.Join(f, status.EventsFile); pathExists(p) {
			out = append(out, p)
		}
	}
	return out
}

func mMigrateLifecycleEnvelope() migrationImpl {
	detect := func(root string) bool {
		for _, p := range lifecycleLogPaths(root) {
			if m, err := computeEnvelope(p); err == nil && m.migrated > 0 {
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
			return false, "no legacy-shaped lifecycle envelope rows found in any event log"
		},
		apply: func(root string, dryRun bool) migrationResult {
			paths := lifecycleLogPaths(root)
			if len(paths) == 0 {
				return migrationResult{Success: true}
			}
			total := 0
			var refusals []string
			for _, p := range paths {
				m, err := computeEnvelope(p)
				if err != nil {
					refusals = append(refusals, p+": "+err.Error())
					continue
				}
				if !dryRun && m.migrated > 0 {
					bak := p + ".pre-migration.bak"
					if pathExists(bak) {
						refusals = append(refusals, fmt.Sprintf("%s: refusing to migrate: a prior snapshot already exists at %s (migration is one-shot per file; remove the snapshot manually once satisfied with the prior run before re-migrating)", p, bak))
						continue
					}
					orig, _ := os.ReadFile(p)
					os.WriteFile(bak, orig, 0o600)
					var b bytes.Buffer
					for _, l := range m.lines {
						b.WriteString(l + "\n")
					}
					os.WriteFile(p, b.Bytes(), 0o600)
				}
				total += m.migrated
			}
			if total == 0 {
				return migrationResult{Success: len(refusals) == 0, Errors: refusals}
			}
			msg := fmt.Sprintf("Rewrote %d legacy-shaped lifecycle envelope row(s) to F1's strict shape across %d event log(s) scanned", total, len(paths))
			if dryRun {
				msg = fmt.Sprintf("dry-run: would rewrite %d legacy-shaped lifecycle envelope row(s) to F1's strict shape across %d event log(s) scanned", total, len(paths))
			}
			return migrationResult{Success: len(refusals) == 0, Changes: []string{msg}, Errors: refusals}
		},
	}
}

// --- unified bundle (3.2.0rc35) ---------------------------------------------------------------

func mUnifiedBundle() migrationImpl {
	return migrationImpl{
		noWorktrees: true,
		detect: func(root string) bool {
			st, err := os.Stat(filepath.Join(root, ".git"))
			return !(err == nil && st.Mode().IsRegular())
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			start := time.Now()
			report := func(present, applied, refreshed bool, validation *jsonx.Object, errs []string) migrationResult {
				dur := int(time.Since(start).Milliseconds())
				el := []any{}
				for _, e := range errs {
					el = append(el, e)
				}
				r := jsonx.NewObject("applied", applied, "bundle_validation", validation, "charter_present", present,
					"chokepoint_refreshed", refreshed, "duration_ms", dur, "errors", el, "migration_id", "3.2.0rc35_unified_bundle",
					"target_version", "3.2.0rc35")
				return migrationResult{Success: len(errs) == 0, Changes: []string{jsonx.Dumps(r, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true})}, Errors: errs}
			}
			def := jsonx.NewObject("missing_derived", []any{}, "missing_tracked", []any{}, "passed", true, "unexpected", []any{})
			canonical := root
			cd := filepath.Join(canonical, ".kittify", "charter")
			if !pathExists(filepath.Join(cd, "charter.md")) {
				return report(false, false, false, def, nil)
			}
			missing := func(files []string) []any {
				out := []any{}
				for _, f := range files {
					if !pathExists(filepath.Join(canonical, filepath.FromSlash(f))) {
						out = append(out, f)
					}
				}
				return out
			}
			tracked := []string{".kittify/charter/charter.md"}
			derived := []string{".kittify/charter/governance.yaml", ".kittify/charter/directives.yaml", ".kittify/charter/metadata.yaml"}
			declared := map[string]bool{}
			for _, f := range append(append([]string{}, tracked...), derived...) {
				declared[f] = true
			}
			var unexpected []string
			for _, f := range rglobFiles(cd) {
				rel, _ := filepath.Rel(canonical, f)
				if !declared[filepath.ToSlash(rel)] {
					unexpected = append(unexpected, filepath.ToSlash(rel))
				}
			}
			sort.Strings(unexpected)
			ua := []any{}
			for _, u := range unexpected {
				ua = append(ua, u)
			}
			mt, md := missing(tracked), missing(derived)
			v := jsonx.NewObject("missing_derived", md, "missing_tracked", mt, "passed", len(mt) == 0 && len(md) == 0, "unexpected", ua)
			return report(true, false, false, v, nil)
		},
	}
}

// --- charter bundle versioning (charter.offering.versioning) ------------------------------------

func bundleSchemaVersion(cd string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(cd, "charter.yaml"))
	if err != nil {
		return 0, false
	}
	doc, err := yamlx.ParseObject(b)
	if err != nil || doc == nil {
		return 0, false
	}
	md := doc.GetObject("metadata")
	if md == nil {
		return 0, false
	}
	v, ok := md.Get("bundle_schema_version")
	n, isInt := v.(int)
	if !ok || !isInt {
		return 0, false
	}
	return n, true
}

// bundleCompat returns (incompatible, message) for check_bundle_compatibility.
func bundleCompat(v int, has bool) (bool, string) {
	switch {
	case !has:
		return false, "Bundle schema version not found; treating as v1. Run `spec-kitty upgrade`."
	case v == 2:
		return false, fmt.Sprintf("Bundle schema version %d is supported.", v)
	case v == 1:
		return false, fmt.Sprintf("Bundle schema version %d requires migration. Run `spec-kitty upgrade`.", v)
	case v < 1:
		return true, fmt.Sprintf("Bundle schema version %d predates the earliest supported version (1). Contact support.", v)
	}
	return true, fmt.Sprintf("Bundle schema version %d is newer than this CLI supports (2). Upgrade your CLI.", v)
}

func canonicalYAMLBytes(o *jsonx.Object) []byte {
	return []byte(yamlx.Emit(sortKeysDeep(o)))
}

func sortKeysDeep(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		keys := t.Keys()
		sort.Strings(keys)
		out := &jsonx.Object{}
		for _, k := range keys {
			x, _ := t.Get(k)
			out.Set(k, sortKeysDeep(x))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = sortKeysDeep(x)
		}
		return out
	}
	return v
}

func sha256HexBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// repairV2ManifestDefaults is repair_v2_synthesis_manifest_defaults.
func repairV2ManifestDefaults(cd string, dryRun bool) (changes, errs []string) {
	mp := filepath.Join(cd, "synthesis-manifest.yaml")
	b, err := os.ReadFile(mp)
	if err != nil {
		return nil, nil
	}
	m, err := yamlx.ParseObject(b)
	if err != nil {
		return nil, []string{fmt.Sprintf("Failed to load synthesis-manifest.yaml: %v", err)}
	}
	if m == nil || strOrEmpty(m, "schema_version") != "2" || m.Has("built_in_only") {
		return nil, nil
	}
	stored, isStr := func() (string, bool) { v, _ := m.Get("manifest_hash"); s, ok := v.(string); return s, ok }()
	if !isStr {
		return nil, []string{"Cannot repair synthesis-manifest.yaml: manifest_hash is missing or invalid."}
	}
	legacy := &jsonx.Object{}
	for _, kv := range m.Pairs() {
		if kv.Key != "manifest_hash" {
			legacy.Set(kv.Key, kv.Value)
		}
	}
	if sha256HexBytes(canonicalYAMLBytes(legacy)) != stored {
		return nil, []string{"Cannot repair synthesis-manifest.yaml: existing manifest_hash does not match the pre-built_in_only v2 payload."}
	}
	m.Set("built_in_only", false)
	fields := &jsonx.Object{}
	for _, kv := range m.Pairs() {
		if kv.Key != "manifest_hash" {
			fields.Set(kv.Key, kv.Value)
		}
	}
	fields.Set("schema_version", "2")
	if !fields.Has("mission_id") {
		fields.Set("mission_id", nil)
	}
	m.Set("manifest_hash", sha256HexBytes(canonicalYAMLBytes(fields)))
	if !dryRun {
		if err := os.WriteFile(mp, []byte(yamlx.Emit(m)), 0o666&^umask()); err != nil {
			return nil, []string{"Failed to write synthesis-manifest.yaml: " + pyOSError(err)}
		}
	}
	return []string{mp}, nil
}

func mCharterBundleV2() migrationImpl {
	cd := func(root string) string { return filepath.Join(root, ".kittify", "charter") }
	return migrationImpl{
		detect: func(root string) bool {
			if !pathExists(cd(root)) {
				return false
			}
			v, has := bundleSchemaVersion(cd(root))
			if !has || v < 2 {
				return true
			}
			if v == 2 {
				c, e := repairV2ManifestDefaults(cd(root), true)
				return len(c) > 0 || len(e) > 0
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(cd(root)) {
				return false, "No charter directory found at .kittify/charter"
			}
			if bad, msg := bundleCompat(bundleSchemaVersion(cd(root))); bad {
				return false, msg
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			v, has := bundleSchemaVersion(cd(root))
			if bad, msg := bundleCompat(v, has); bad {
				return migrationResult{Errors: []string{msg}}
			}
			if !has {
				v = 1
			}
			if v >= 2 {
				c, e := repairV2ManifestDefaults(cd(root), dryRun)
				return newResult(c, nil, e)
			}
			c, e := migrateBundleV1toV2(cd(root), dryRun)
			if len(e) == 0 {
				c2, e2 := repairV2ManifestDefaults(cd(root), dryRun)
				c, e = append(c, c2...), append(e, e2...)
			}
			return newResult(c, nil, e)
		},
	}
}

// migrateBundleV1toV2 is migrate_v1_to_v2 (provenance sidecars, synthesis
// manifest, charter.yaml stamp).
func migrateBundleV1toV2(cd string, dryRun bool) (changes, errs []string) {
	for _, sp := range globFiles(filepath.Join(cd, "provenance"), "*.yaml") {
		b, _ := os.ReadFile(sp)
		d, err := yamlx.ParseObject(b)
		if err != nil {
			errs = append(errs, fmt.Sprintf("Failed to load sidecar %s: %v", filepath.Base(sp), err))
			continue
		}
		if d == nil {
			errs = append(errs, fmt.Sprintf("Sidecar %s is not a YAML mapping; skipping.", filepath.Base(sp)))
			continue
		}
		if strOrEmpty(d, "schema_version") == "2" {
			continue
		}
		setDefault := func(k string, v any) {
			if !d.Has(k) {
				d.Set(k, v)
			}
		}
		setDefault("synthesizer_version", "(pre-phase7-migration)")
		setDefault("synthesis_run_id", "(pre-phase7-migration)")
		if !d.Has("produced_at") {
			if st, err := os.Stat(sp); err == nil {
				d.Set("produced_at", clock.ISO(st.ModTime().UTC()))
			} else {
				d.Set("produced_at", "(pre-phase7-migration)")
			}
		}
		if !d.Has("source_input_ids") {
			src, _ := d.Get("source_urns")
			if src == nil {
				src = []any{}
			}
			d.Set("source_input_ids", src)
		}
		if v, _ := d.Get("corpus_snapshot_id"); v == nil {
			d.Set("corpus_snapshot_id", "(none)")
		}
		d.Set("schema_version", "2")
		changes = append(changes, sp)
		if !dryRun {
			os.WriteFile(sp, []byte(yamlx.Emit(d)), 0o666&^umask())
		}
	}
	mp := filepath.Join(cd, "synthesis-manifest.yaml")
	if b, err := os.ReadFile(mp); err == nil {
		m, perr := yamlx.ParseObject(b)
		if perr != nil {
			errs = append(errs, fmt.Sprintf("Failed to load synthesis-manifest.yaml: %v", perr))
		} else if m != nil && strOrEmpty(m, "schema_version") != "2" {
			for _, kv := range [][2]any{{"synthesizer_version", "(pre-phase7-migration)"}, {"mission_id", nil}, {"built_in_only", false}} {
				if !m.Has(kv[0].(string)) {
					m.Set(kv[0].(string), kv[1])
				}
			}
			m.Set("schema_version", "2")
			fields := &jsonx.Object{}
			for _, kv := range m.Pairs() {
				if kv.Key != "manifest_hash" {
					fields.Set(kv.Key, kv.Value)
				}
			}
			m.Set("manifest_hash", sha256HexBytes(canonicalYAMLBytes(fields)))
			changes = append(changes, mp)
			if !dryRun {
				os.WriteFile(mp, []byte(yamlx.Emit(m)), 0o666&^umask())
			}
		}
	}
	cy := filepath.Join(cd, "charter.yaml")
	if b, err := os.ReadFile(cy); err == nil {
		d, perr := yamlx.ParseObject(b)
		if perr == nil && d != nil {
			md := d.GetObject("metadata")
			if md == nil {
				md = &jsonx.Object{}
				d.Set("metadata", md)
			}
			if v, _ := md.Get("bundle_schema_version"); v != 2 {
				md.Set("bundle_schema_version", 2)
				changes = append(changes, cy)
				if !dryRun {
					os.WriteFile(cy, []byte(yamlx.Emit(d)), 0o666&^umask())
				}
			}
		}
	}
	return changes, errs
}

func mCharterManifestDefaultsRepair() migrationImpl {
	cd := func(root string) string { return filepath.Join(root, ".kittify", "charter") }
	return migrationImpl{
		detect: func(root string) bool {
			if !pathExists(cd(root)) {
				return false
			}
			if v, has := bundleSchemaVersion(cd(root)); !has || v != 2 {
				return false
			}
			c, e := repairV2ManifestDefaults(cd(root), true)
			return len(c) > 0 || len(e) > 0
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(cd(root)) {
				return false, "No charter directory found at .kittify/charter"
			}
			v, has := bundleSchemaVersion(cd(root))
			if bad, msg := bundleCompat(v, has); bad {
				return false, msg
			}
			if !has || v != 2 {
				return false, "Charter bundle is not at current v2 schema."
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			c, e := repairV2ManifestDefaults(cd(root), dryRun)
			return newResult(c, nil, e)
		},
	}
}

var _ = pyyaml.Dump
