package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

// migrationInfo is one upstream BaseMigration's registry identity.
type migrationInfo struct {
	ID, TargetVersion, Description                            string
	RunsFirst                                                 bool
	CloneLocal, RequiresGit, LedgerAdoption, MainCheckoutOnly bool
	Identity, ContentHash                                     string
}

// migrationResult is upgrade.migrations.base.MigrationResult.
type migrationResult struct {
	Success       bool
	Changes       []string
	Warnings      []string
	Errors        []string
	ManualReview  bool
	Preserved     []string
	PartialWrites []migrationPartialWrite
}

type migrationPartialWrite struct{ Mission, Path string }

// migrationImpl ports one migration's detect/can_apply/apply. Migrations
// without a port detect as "not needed" (they target state the Go port
// never produces: pre-3.x layouts, the retired doctrine tree, Python-era
// artifacts) and are recorded as skipped, exactly like an upstream run on a
// project that is already in the target state.
type migrationImpl struct {
	detect   func(root string) bool
	canApply func(root string) (bool, string)
	apply    func(root string, dryRun bool) migrationResult
	// detectE / applyE may fail closed (GitignorePathError,
	// MigrationStateUnreadableError): the runner records a failure.
	detectE func(root string) (bool, error)
	applyE  func(root string, dryRun bool) (migrationResult, error)
	// noWorktrees is runs_on_worktrees = False.
	noWorktrees bool
	// structural is a runs_first migration's structural_detect.
	structural func(root string) bool
}

var migrationImpls = map[string]migrationImpl{}

func (m migrationInfo) impl() migrationImpl { return migrationImpls[m.ID] }

func (m migrationInfo) detect(root string) bool {
	ok, _ := m.detectErr(root)
	return ok
}

// detectFailClosed is registry._detect_fail_closed: a fail-closed detection
// error selects the migration.
func (m migrationInfo) detectFailClosed(root string) bool {
	ok, err := m.detectErr(root)
	return ok || err != nil
}

// detectErr is detect() with its fail-closed exception.
func (m migrationInfo) detectErr(root string) (bool, error) {
	var needed bool
	err := dct.LegacyActivationScope([]string{root}, func() error {
		i := m.impl()
		if i.detectE != nil {
			var err error
			needed, err = i.detectE(root)
			return err
		}
		if i.detect != nil {
			needed = i.detect(root)
		}
		return nil
	})
	return needed, err
}

// reselectWhenRecorded is BaseMigration.reselect_when_recorded.
func (m migrationInfo) reselectWhenRecorded(root string) bool {
	needed := false
	dct.LegacyActivationScope([]string{root}, func() error {
		i := m.impl()
		needed = m.RunsFirst && i.structural != nil && i.structural(root)
		return nil
	})
	return needed
}

func (m migrationInfo) runsOnWorktrees() bool {
	return !m.impl().noWorktrees && !m.MainCheckoutOnly
}

func (m migrationInfo) canApply(root string) (bool, string) {
	allowed, reason := true, ""
	dct.LegacyActivationScope([]string{root}, func() error {
		if i := m.impl(); i.canApply != nil {
			allowed, reason = i.canApply(root)
		} else if _, err := os.Stat(root); err != nil {
			allowed, reason = false, "Project path does not exist: "+root
		}
		return nil
	})
	return allowed, reason
}

func (m migrationInfo) apply(root string, dryRun bool) migrationResult {
	r, _ := m.applyErr(root, dryRun)
	return r
}

func (m migrationInfo) applyErr(root string, dryRun bool) (migrationResult, error) {
	result := migrationResult{Success: true}
	err := dct.LegacyActivationScope([]string{root}, func() error {
		i := m.impl()
		if i.applyE != nil {
			var err error
			result, err = i.applyE(root, dryRun)
			return err
		}
		if i.apply != nil {
			result = i.apply(root, dryRun)
		}
		return nil
	})
	return result, err
}

// targetSchema is int(Version(target_version).major) (MigrationStep).
func (m migrationInfo) targetSchema() int {
	v, ok := parsePEP440(m.TargetVersion)
	if !ok {
		return 0
	}
	return v.Major()
}

// applicableMigrations is MigrationRegistry.get_applicable.
func applicableMigrations(from, to, root string) ([]migrationInfo, error) {
	_ = from // the version window is no longer the applied authority
	var declared, selected []migrationInfo
	for _, m := range migrationCatalog {
		cmp, valid := versionCmp(m.TargetVersion, to)
		if m.LedgerAdoption || !valid || cmp <= 0 {
			declared = append(declared, m)
		}
	}
	if root == "" {
		return declared, nil
	}
	for _, cloneLocal := range []bool{false, true} {
		if _, err := readMigrationRecords(root, cloneLocal); err != nil {
			return nil, err
		}
	}
	err := dct.LegacyActivationScope([]string{root}, func() error {
		adopting := map[string]bool{}
		for _, m := range declared {
			if m.LedgerAdoption && !migrationSettled(root, m) {
				plan, err := migrationAdoptionPlan(root)
				if err != nil {
					return err
				}
				for _, record := range plan {
					if record.ID != m.ID && record.Result == "success" {
						adopting[record.ID] = true
					}
				}
			}
		}
		for _, m := range declared {
			if (!migrationSettled(root, m) && !adopting[m.ID]) || m.reselectWhenRecorded(root) {
				selected = append(selected, m)
			}
		}
		return nil
	})
	return selected, err
}

// detectProjectVersion is VersionDetector.detect_version.
func detectProjectVersion(root string) string {
	if _, err := os.Stat(filepath.Join(root, ".kittify")); err != nil {
		return "unknown"
	}
	if m := kittify.LoadMetadata(root); m != nil {
		return m.Version
	}
	return "unknown"
}

// pendingMigrationSteps is the compat contract's pending_migrations list.
func pendingMigrationSteps(root string) ([]any, error) {
	return migrationSteps(root, "")
}

func migrationSteps(root, target string) ([]any, error) {
	if target == "" {
		target = appVersion()
	}
	from := detectProjectVersion(root)
	if from == "unknown" {
		from = "0.0.0"
	}
	out := []any{}
	migrations, err := applicableMigrations(from, target, root)
	if err != nil {
		return nil, err
	}
	for _, m := range migrations {
		out = append(out, stepObject(m))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Ported migrations
// ---------------------------------------------------------------------------

func init() {
	migrationImpls["4.0.0rc7_exclude_spec_kitty"] = excludeMigration(".spec-kitty/", "Exclude .spec-kitty/ from git index")
	migrationImpls["4_0_0rc5_install_lane_tip_recorder"] = migrationImpl{
		detect: func(root string) bool { return len(pendingLaneTipHooks(root)) > 0 },
		canApply: func(root string) (bool, string) {
			if len(pendingLaneTipHooks(root)) > 0 {
				return true, ""
			}
			return false, "lane-tip recorder already installed in every hook slot (or no git repository present)"
		},
		apply: func(root string, dryRun bool) migrationResult {
			pending := pendingLaneTipHooks(root)
			var ch []string
			if dryRun {
				for _, n := range pending {
					ch = append(ch, "would install lane-tip recorder hook: "+n)
				}
				return migrationResult{Success: true, Changes: ch}
			}
			for _, n := range InstallLaneTipRecorder(root) {
				ch = append(ch, "installed lane-tip recorder hook: "+n)
			}
			return migrationResult{Success: true, Changes: ch}
		},
	}
	for id, d := range mergeDriverMigrations {
		migrationImpls[id] = mergeDriverMigration(d.summary, d.drivers)
	}
}

func gitignoreEntries(root string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return out
	}
	for _, l := range strings.Split(strings.TrimPrefix(string(b), "\xef\xbb\xbf"), "\n") {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "#") {
			out[t] = true
		}
	}
	return out
}

func gitignoreBackfill(equiv [][]string) migrationImpl {
	missing := func(root string) []string {
		present := gitignoreEntries(root)
		var out []string
		for _, forms := range equiv {
			found := false
			for _, f := range forms {
				found = found || present[f]
			}
			if !found {
				out = append(out, forms[0])
			}
		}
		return out
	}
	return migrationImpl{
		detect: func(root string) bool { return len(missing(root)) > 0 },
		apply: func(root string, dryRun bool) migrationResult {
			m := missing(root)
			if dryRun {
				var ch []string
				for _, e := range m {
					ch = append(ch, "Would add "+e+" to .gitignore")
				}
				return migrationResult{Success: true, Changes: ch}
			}
			if len(m) == 0 {
				return migrationResult{Success: true, Changes: []string{"gitignore entries already present"}}
			}
			if _, err := agents.EnsureGitignoreEntries(root, m, false); err != nil {
				return migrationResult{Errors: []string{err.Error()}}
			}
			return migrationResult{Success: true, Changes: []string{"Added gitignore entries: " + strings.Join(m, ", ")}}
		},
	}
}

func gitCommonDir(root string) string {
	r, err := gitx.Exec(root, nil, nil, "rev-parse", "--git-common-dir")
	if err != nil || r.Code != 0 {
		return ""
	}
	d := strings.TrimSpace(r.Stdout)
	if !filepath.IsAbs(d) {
		d = filepath.Join(root, d)
	}
	return d
}

func excludeMigration(entry, _ string) migrationImpl {
	file := func(root string) string {
		d := gitCommonDir(root)
		if d == "" {
			return ""
		}
		p := filepath.Join(d, "info", "exclude")
		if _, err := os.Stat(p); err != nil {
			return ""
		}
		return p
	}
	return migrationImpl{
		detect: func(root string) bool {
			p := file(root)
			if p == "" {
				return false
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return false
			}
			for _, l := range strings.Split(string(b), "\n") {
				if strings.TrimSpace(l) == entry {
					return false
				}
			}
			return true
		},
		canApply: func(root string) (bool, string) {
			if gitCommonDir(root) == "" {
				return false, "Not a git repository"
			}
			if file(root) == "" {
				return false, ".git/info/exclude file not found"
			}
			return true, ""
		},
		apply: func(root string, dryRun bool) migrationResult {
			if gitCommonDir(root) == "" {
				return migrationResult{Success: true, Changes: []string{"Skipped: not a git repository"}}
			}
			if dryRun {
				return migrationResult{Success: true, Changes: []string{"Would add " + entry + " to .git/info/exclude"}}
			}
			ExcludeFromGitIndex(root, []string{entry})
			return migrationResult{Success: true, Changes: []string{"Added " + entry + " to .git/info/exclude"}}
		},
	}
}

// ExcludeFromGitIndex is core.git_ops.exclude_from_git_index: append missing
// patterns to the common-dir info/exclude under a spec-kitty marker.
func ExcludeFromGitIndex(root string, patterns []string) {
	d := gitCommonDir(root)
	if d == "" {
		return
	}
	p := filepath.Join(d, "info", "exclude")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	existing := map[string]bool{}
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		existing[l] = true
	}
	var add []string
	for _, x := range patterns {
		if !existing[x] {
			add = append(add, x)
		}
	}
	if len(add) == 0 {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer f.Close()
	const marker = "# Added by spec-kitty (local exclusions)"
	if !existing[marker] {
		fmt.Fprintf(f, "\n%s\n", marker)
	}
	for _, x := range add {
		fmt.Fprintf(f, "%s\n", x)
	}
}

// ---------------------------------------------------------------------------
// Lane-tip recorder hooks (policy.lane_tip_recorder)
// ---------------------------------------------------------------------------

const laneTipHookSignature = "# Generated by spec-kitty (lane-tip recorder)."

// LaneTipHookBody is the post-commit/post-rewrite recorder script.
const LaneTipHookBody = "#!/bin/sh\n" + laneTipHookSignature + " Do not edit by hand.\n" +
	"b=$(git symbolic-ref -q HEAD) || exit 0\n" +
	"case \"$b\" in refs/heads/kitty/mission-*-lane-*) ;; *) exit 0 ;; esac\n" +
	"id=${b##*-lane-}\n" +
	"case \"$id\" in ''|*[!a-z]*) exit 0 ;; esac\n" +
	"git update-ref \"refs/spec-kitty/lane-tip/${b#refs/heads/}\" HEAD >/dev/null 2>&1\n" +
	"exit 0\n"

var laneTipHookNames = []string{"post-commit", "post-rewrite"}

func laneTipHooksDir(root string) string {
	r, err := gitx.Exec(root, nil, nil, "rev-parse", "--git-path", "hooks")
	if err != nil || r.Code != 0 {
		return ""
	}
	d := strings.TrimSpace(r.Stdout)
	if !filepath.IsAbs(d) {
		d = filepath.Join(root, d)
	}
	if s, err := gitx.Exec(root, nil, nil, "config", "--show-scope", "--get", "core.hooksPath"); err == nil && s.Code != 1 {
		scope, _, _ := strings.Cut(s.Stdout, "\t")
		if s.Code != 0 || (strings.TrimSpace(scope) != "local" && strings.TrimSpace(scope) != "worktree") {
			return ""
		}
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return ""
		}
	}
	return d
}

func pendingLaneTipHooks(root string) []string {
	d := laneTipHooksDir(root)
	if d == "" {
		return nil
	}
	var out []string
	for _, n := range laneTipHookNames {
		if _, err := os.Lstat(filepath.Join(d, n)); err != nil {
			out = append(out, n)
		}
	}
	return out
}

// InstallLaneTipRecorder installs the recorder into empty (or own) hook
// slots; foreign hooks are left untouched. Returns the installed names.
func InstallLaneTipRecorder(root string) []string {
	d := laneTipHooksDir(root)
	if d == "" {
		return nil
	}
	os.MkdirAll(d, 0o755)
	var out []string
	for _, n := range laneTipHookNames {
		p := filepath.Join(d, n)
		if b, err := os.ReadFile(p); err == nil && !strings.Contains(string(b), laneTipHookSignature) {
			continue
		} else if err != nil {
			if _, lerr := os.Lstat(p); lerr == nil {
				continue
			}
		}
		tmp := p + ".tmp"
		if os.WriteFile(tmp, []byte(LaneTipHookBody), 0o755) != nil {
			continue
		}
		os.Chmod(tmp, 0o755)
		if os.Rename(tmp, p) == nil {
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Merge-driver seeding migrations (_merge_driver_seeding)
// ---------------------------------------------------------------------------

type driverSpec struct{ key, name, command, pattern string }

var mergeDriverMigrations = map[string]struct {
	summary string
	drivers []driverSpec
}{
	"3.2.6_decisions_event_log_merge_driver": {"Would install the decisions.events.jsonl merge driver and .gitattributes entry", []driverSpec{
		{"spec-kitty-event-log", "Spec Kitty event log union merge", "spec-kitty merge-driver-event-log %O %A %B", "kitty-specs/**/decisions.events.jsonl"}}},
	"3.2.6_gate_artifact_merge_drivers": {"Would install gate-artifact merge drivers and .gitattributes entries", []driverSpec{
		{"spec-kitty-acceptance-matrix", "Spec Kitty acceptance matrix filled-side merge", "spec-kitty merge-driver-acceptance-matrix %O %A %B", "kitty-specs/**/acceptance-matrix.json"},
		{"spec-kitty-issue-matrix", "Spec Kitty issue matrix filled-side merge", "spec-kitty merge-driver-issue-matrix %O %A %B", "kitty-specs/**/issue-matrix.md"}}},
	"3.2.6_issue_matrix_driver_repoint": {"Would repoint the issue-matrix merge driver to issue-matrix.json", []driverSpec{
		{"spec-kitty-issue-matrix", "Spec Kitty issue matrix row-aware merge", "spec-kitty merge-driver-issue-matrix %O %A %B", "kitty-specs/**/issue-matrix.json"}}},
	"3.2.6_meta_traces_merge_drivers": {"Would install meta.json + traces/*.md merge drivers and .gitattributes entries", []driverSpec{
		{"spec-kitty-meta", "Spec Kitty mission meta field merge", "spec-kitty merge-driver-meta %O %A %B", "kitty-specs/**/meta.json"},
		{"spec-kitty-traces", "Spec Kitty mission traces union merge", "spec-kitty merge-driver-traces %O %A %B", "kitty-specs/**/traces/*.md"}}},
	"3.2.7_review_cycle_merge_driver": {"Would install the review-cycle-*.md merge driver and .gitattributes entry", []driverSpec{
		{"spec-kitty-review-cycle", "Spec Kitty review-cycle verdict collision refusal", "spec-kitty merge-driver-review-cycle %O %A %B", "kitty-specs/**/tasks/*/review-cycle-*.md"}}},
	"4.0.0rc5_decision_index_merge_driver": {"Would install the decisions/index.json merge driver and .gitattributes entry", []driverSpec{
		{"spec-kitty-decision-index", "Spec Kitty decision-index entry union merge", "spec-kitty merge-driver-decision-index %O %A %B", "kitty-specs/**/decisions/index.json"}}},
}

func gitConfigLocal(root string, args ...string) (string, bool) {
	r, err := gitx.Exec(root, nil, nil, append([]string{"config", "--local"}, args...)...)
	if err != nil || r.Code != 0 {
		return "", false
	}
	return strings.TrimSpace(r.Stdout), true
}

func mergeDriverMigration(summary string, drivers []driverSpec) migrationImpl {
	isGit := func(root string) bool { _, err := os.Stat(filepath.Join(root, ".git")); return err == nil }
	detect := func(root string) bool {
		b, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
		if err != nil {
			return true
		}
		for _, d := range drivers {
			if !strings.Contains(string(b), d.pattern+" merge="+d.key) {
				return true
			}
		}
		if !isGit(root) {
			return false
		}
		for _, d := range drivers {
			n, _ := gitConfigLocal(root, "--get", "merge."+d.key+".name")
			c, _ := gitConfigLocal(root, "--get", "merge."+d.key+".driver")
			if n != d.name || c != d.command {
				return true
			}
		}
		return false
	}
	return migrationImpl{
		detect: detect,
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn []string
			if dryRun {
				if detect(root) {
					ch = append(ch, summary)
				}
				return migrationResult{Success: true, Changes: ch}
			}
			attrs := filepath.Join(root, ".gitattributes")
			git := isGit(root)
			for _, d := range drivers {
				line := d.pattern + " merge=" + d.key
				if ensureLine(attrs, line) {
					ch = append(ch, "Added .gitattributes entry: "+line)
				}
				if !git {
					continue
				}
				if n, _ := gitConfigLocal(root, "--get", "merge."+d.key+".name"); n != d.name {
					gitx.Exec(root, nil, nil, "config", "--local", "merge."+d.key+".name", d.name)
					ch = append(ch, "Configured git merge."+d.key+".name")
				}
				if c, _ := gitConfigLocal(root, "--get", "merge."+d.key+".driver"); c != d.command {
					gitx.Exec(root, nil, nil, "config", "--local", "merge."+d.key+".driver", d.command)
					ch = append(ch, "Configured git merge."+d.key+".driver")
				}
			}
			if !git {
				warn = append(warn, "Skipped local git merge-driver config because this project is not a git repository")
			}
			return migrationResult{Success: true, Changes: ch, Warnings: warn}
		},
	}
}

// ensureLine is _merge_driver_seeding.ensure_line.
func ensureLine(path, line string) bool {
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for _, l := range lines {
			if l == line {
				return false
			}
		}
	}
	lines = append(lines, line)
	os.WriteFile(path, []byte(strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n")+"\n"), 0o644)
	return true
}
