package autorebase

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mergedriver"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Options name the lane whose worktree merges the mission branch.
type Options struct {
	Root, LaneID, Branch, MissionBranch, Worktree string
	// Env is _make_merge_env() (extra environment for every git call).
	Env []string
	// EnsureDrivers is _ensure_merge_driver_git_config (optional).
	EnsureDrivers func()
	// Disposable is the lane worktree's residue context (_abort_context):
	// paths a guarded merge abort may discard beyond the merge's own. nil:
	// nothing else is disposable.
	Disposable func(string) bool
}

// Report is AutoRebaseReport.
type Report struct {
	LaneID          string
	Attempted       bool
	Succeeded       bool
	Classifications []Classification
	HaltReason      string
}

// targetOwnedPaths is target_owned_paths().
var targetOwnedPaths = map[string]bool{".kittify/metadata.yaml": true}

// IsTargetOwnedPath is is_target_owned_path.
func IsTargetOwnedPath(p string) bool {
	n := strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(n, "/") || (len(n) > 1 && n[1] == ':') {
		return false
	}
	for strings.HasPrefix(n, "./") {
		n = n[2:]
	}
	return targetOwnedPaths[n]
}

type attempt struct {
	o    Options
	wt   string
	cls  []Classification
	lane string
}

func (a *attempt) git(args ...string) gitx.Result {
	r, _ := gitx.Exec(a.wt, a.o.Env, nil, args...)
	return r
}

func output(r gitx.Result) string {
	if s := strings.TrimSpace(r.Stderr); s != "" {
		return s
	}
	return strings.TrimSpace(r.Stdout)
}

func pathParts(rel string) []string { return strings.Split(path.Clean(rel), "/") }

func isStatusEventsPath(rel string) bool {
	p := pathParts(rel)
	return len(p) >= 3 && p[0] == "kitty-specs" && p[len(p)-1] == status.EventsFile
}

func isStatusJSONPath(rel string) bool {
	p := pathParts(rel)
	return len(p) >= 3 && p[0] == "kitty-specs" && p[len(p)-1] == statusJSONFilename
}

// isCoordinationOwnedArtifact is _is_coordination_owned_artifact.
func isCoordinationOwnedArtifact(rel string) bool {
	if mission.IsCoordResidue(rel) {
		return true
	}
	switch mission.ArtifactKindForPath(rel) {
	case mission.KindLaneState, mission.KindWorkPackageTask, mission.KindAnalysisReport:
		return true
	}
	return false
}

func (a *attempt) showStage(rel string, stage int) (string, bool) {
	r := a.git("show", fmt.Sprintf(":%d:%s", stage, rel))
	if r.Code != 0 {
		return "", false
	}
	return r.Stdout, true
}

func (a *attempt) sparseEnabled() bool {
	r := a.git("config", "--bool", "core.sparseCheckout")
	return r.Code == 0 && strings.ToLower(strings.TrimSpace(r.Stdout)) == "true"
}

func rejectedSparse(r gitx.Result) bool {
	c := r.Stderr + "\n" + r.Stdout
	return strings.Contains(c, "unknown option") && strings.Contains(c, "sparse")
}

func (a *attempt) stage(rel string) (bool, string) {
	if a.sparseEnabled() {
		r := a.git("add", "--sparse", rel)
		if r.Code == 0 {
			return true, ""
		}
		if !rejectedSparse(r) {
			return false, output(r)
		}
	}
	if r := a.git("add", rel); r.Code != 0 {
		return false, output(r)
	}
	return true, ""
}

func (a *attempt) remove(rel string) (bool, string) {
	target := filepath.Join(a.wt, rel)
	if _, err := os.Lstat(target); err == nil {
		if err := os.Remove(target); err != nil {
			return false, fmt.Sprintf("could not remove %s: %v", rel, err)
		}
	}
	if a.sparseEnabled() {
		r := a.git("rm", "-f", "--ignore-unmatch", "--sparse", rel)
		if r.Code == 0 {
			return true, ""
		}
		if !rejectedSparse(r) {
			return false, output(r)
		}
	}
	if r := a.git("rm", "-f", "--ignore-unmatch", rel); r.Code != 0 {
		return false, output(r)
	}
	return true, ""
}

func (a *attempt) reapplySparse() string {
	if !a.sparseEnabled() {
		return ""
	}
	if r := a.git("sparse-checkout", "reapply"); r.Code != 0 {
		return output(r)
	}
	return ""
}

func (a *attempt) managed(rel, rule string) {
	a.cls = append(a.cls, Classification{FilePath: filepath.Join(a.wt, rel), Res: auto("", rule)})
}

func (a *attempt) resolveStatusEvents(rel string) string {
	var texts []string
	have := map[int]bool{}
	for _, st := range []int{1, 2, 3} {
		if t, ok := a.showStage(rel, st); ok {
			texts = append(texts, t)
			have[st] = true
		}
	}
	if !have[2] || !have[3] {
		return fmt.Sprintf("%s: refusing status.events.jsonl deletion conflict for %s", RuleStatusEvents, rel)
	}
	merged, err := mergedriver.MergeEventLogTexts(texts...)
	if err != nil {
		return fmt.Sprintf("%s: %s", RuleStatusEvents, err)
	}
	p := filepath.Join(a.wt, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(merged), 0o644); err != nil {
		return fmt.Sprintf("%s: could not write %s: %v", RuleStatusEvents, rel, err)
	}
	if ok, msg := a.stage(rel); !ok {
		return fmt.Sprintf("%s: git add %s failed: %s", RuleStatusEvents, rel, msg)
	}
	a.managed(rel, RuleStatusEvents)
	return ""
}

// unmergedStages is unmerged_stages: the stage numbers of rel's unmerged
// index entries.
func (a *attempt) unmergedStages(rel string) map[int]bool {
	r := a.git("ls-files", "-u", "-z", "--", rel)
	out := map[int]bool{}
	if r.Code != 0 {
		return out
	}
	for _, rec := range strings.Split(r.Stdout, "\x00") {
		meta, _, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) == 3 && len(f[2]) == 1 {
			out[int(f[2][0]-'0')] = true
		}
	}
	return out
}

func (a *attempt) resolveTakeTheirs(rel, rule string) string {
	stages := a.unmergedStages(rel)
	if len(stages) == 0 {
		return fmt.Sprintf("%s: could not read the unmerged index entries of %s", rule, rel)
	}
	if !stages[3] {
		if ok, msg := a.remove(rel); !ok {
			return fmt.Sprintf("%s: git rm %s failed: %s", rule, rel, msg)
		}
		a.managed(rel, rule)
		return ""
	}
	theirs, ok := a.showStage(rel, 3)
	if !ok {
		return fmt.Sprintf("%s: could not read stage 3 of %s", rule, rel)
	}
	p := filepath.Join(a.wt, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(theirs), 0o644); err != nil {
		return fmt.Sprintf("%s: could not write %s: %v", rule, rel, err)
	}
	if ok, msg := a.stage(rel); !ok {
		return fmt.Sprintf("%s: git add %s failed: %s", rule, rel, msg)
	}
	a.managed(rel, rule)
	return ""
}

func (a *attempt) hydrateStatusEvents(dirRel string) string {
	events := filepath.Join(a.wt, dirRel, status.EventsFile)
	if _, err := os.Stat(events); err == nil {
		return ""
	}
	rel := path.Join(dirRel, status.EventsFile)
	text, ok := a.showStage(rel, 0)
	if !ok {
		return fmt.Sprintf("%s: missing authoritative %s", RuleStatusJSON, rel)
	}
	os.MkdirAll(filepath.Dir(events), 0o755)
	f, err := os.OpenFile(events, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ""
		}
		return fmt.Sprintf("%s: could not hydrate %s: %v", RuleStatusJSON, rel, err)
	}
	f.WriteString(text)
	f.Close()
	if ok, msg := a.stage(rel); !ok {
		return fmt.Sprintf("%s: git add %s failed: %s", RuleStatusJSON, rel, msg)
	}
	return ""
}

func (a *attempt) resolveStatusJSON(dirRel string) string {
	rel := path.Join(dirRel, statusJSONFilename)
	if h := a.hydrateStatusEvents(dirRel); h != "" {
		return h
	}
	if _, err := status.Materialize(filepath.Join(a.wt, dirRel)); err != nil {
		return fmt.Sprintf("%s: could not materialize %s: %v", RuleStatusJSON, rel, err)
	}
	if ok, msg := a.stage(rel); !ok {
		return fmt.Sprintf("%s: git add %s failed: %s", RuleStatusJSON, rel, msg)
	}
	a.managed(rel, RuleStatusJSON)
	return ""
}

// resolveManaged is _resolve_managed_artifact_conflicts.
func (a *attempt) resolveManaged(conflicted []string) ([]string, string) {
	var remaining []string
	refresh := map[string]bool{}
	for _, rel := range conflicted {
		var halt string
		switch {
		case isStatusEventsPath(rel):
			halt = a.resolveStatusEvents(rel)
			refresh[path.Dir(rel)] = true
		case isStatusJSONPath(rel):
			refresh[path.Dir(rel)] = true
			continue
		case IsTargetOwnedPath(rel):
			halt = a.resolveTakeTheirs(rel, RuleTargetOwned)
		case isCoordinationOwnedArtifact(rel):
			halt = a.resolveTakeTheirs(rel, RuleCoordArtifact)
		default:
			remaining = append(remaining, rel)
			continue
		}
		if halt != "" {
			return remaining, halt
		}
	}
	for _, d := range sortedKeys(refresh) {
		if h := a.resolveStatusJSON(d); h != "" {
			return remaining, h
		}
	}
	return remaining, ""
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *attempt) statusPathsAt(ref string) (map[string]bool, string) {
	r := a.git("ls-tree", "-r", "--name-only", "-z", ref, "--", "kitty-specs")
	if r.Code != 0 {
		return nil, output(r)
	}
	out := map[string]bool{}
	for _, p := range strings.Split(r.Stdout, "\x00") {
		if p != "" && (isStatusEventsPath(p) || isStatusJSONPath(p)) {
			out[p] = true
		}
	}
	return out, ""
}

// refusePreexistingLaneStatusDeletions is _refuse_preexisting_lane_status_deletions.
func (a *attempt) refusePreexistingLaneStatusDeletions() string {
	mb := a.git("merge-base", "HEAD", a.o.MissionBranch)
	if mb.Code != 0 {
		d := strings.TrimSpace(mb.Stderr)
		if d == "" {
			d = strings.TrimSpace(mb.Stdout)
		}
		return fmt.Sprintf("%s: could not find merge base with %s: %s", RuleStatusEvents, a.o.MissionBranch, d)
	}
	basePaths, err := a.statusPathsAt(strings.TrimSpace(mb.Stdout))
	if err != "" {
		return fmt.Sprintf("%s: could not inspect base status artifacts: %s", RuleStatusEvents, err)
	}
	missionPaths, err := a.statusPathsAt(a.o.MissionBranch)
	if err != "" {
		return fmt.Sprintf("%s: could not inspect coordination status artifacts: %s", RuleStatusEvents, err)
	}
	both := map[string]bool{}
	for p := range basePaths {
		if missionPaths[p] {
			both[p] = true
		}
	}
	for _, p := range sortedKeys(both) {
		if a.git("cat-file", "-e", "HEAD:"+p).Code != 0 {
			return fmt.Sprintf("%s: refusing pre-existing lane-side deletion of coordination-owned status artifact %s", RuleStatusEvents, p)
		}
	}
	return ""
}

// refreshStagedStatus is _refresh_status_json_for_staged_artifacts.
func (a *attempt) refreshStagedStatus() string {
	r := a.git("diff", "--cached", "--name-status", "-M", "-z")
	if r.Code != 0 {
		return fmt.Sprintf("%s: could not inspect staged paths: %s", RuleStatusJSON, output(r))
	}
	dirs := map[string]bool{}
	f := strings.Split(r.Stdout, "\x00")
	for i := 0; i < len(f); {
		st := f[i]
		i++
		if st == "" {
			continue
		}
		n := 1
		if strings.HasPrefix(st, "R") || strings.HasPrefix(st, "C") {
			n = 2
		}
		if i+n > len(f) {
			break
		}
		paths := f[i : i+n]
		i += n
		for _, p := range paths {
			if isStatusEventsPath(p) {
				if strings.HasPrefix(st, "D") || strings.HasPrefix(st, "R") {
					return fmt.Sprintf("%s: refusing staged deletion of %s", RuleStatusEvents, p)
				}
				dirs[path.Dir(p)] = true
			} else if isStatusJSONPath(p) {
				dirs[path.Dir(p)] = true
			}
		}
	}
	for _, d := range sortedKeys(dirs) {
		abs := filepath.Join(a.wt, d, statusJSONFilename)
		done := false
		for _, c := range a.cls {
			if c.FilePath == abs && c.Res.Auto && c.Res.RuleID == RuleStatusJSON {
				done = true
			}
		}
		if done {
			continue
		}
		if h := a.resolveStatusJSON(d); h != "" {
			return h
		}
	}
	return ""
}

func (a *attempt) ensureUser() {
	for _, kv := range [][2]string{{"user.email", "auto-rebase@spec-kitty"}, {"user.name", "spec-kitty auto-rebase"}} {
		r := a.git("config", "--get", kv[0])
		if r.Code != 0 || strings.TrimSpace(r.Stdout) == "" {
			a.git("config", kv[0], kv[1])
		}
	}
}

// fail is _abort_with_failure: the merge is aborted through the destructive
// guard; a refusal leaves it in progress and says so in the halt reason.
func (a *attempt) fail(reason string) Report {
	if err := a.guardedAbort(a.o.Disposable); err != nil {
		if refused, ok := err.(*AbortRefused); ok {
			reason += "; the merge was left in progress because aborting it would discard local changes: " + refused.Remediation
		}
	}
	if e := a.reapplySparse(); e != "" {
		reason += "; sparse checkout cleanup failed: " + e
	}
	return Report{LaneID: a.lane, Attempted: true, Succeeded: false, Classifications: a.cls, HaltReason: reason}
}

func (a *attempt) mergeInProgress() bool {
	return a.git("rev-parse", "-q", "--verify", "MERGE_HEAD").Code == 0
}

// processFile is _process_conflicted_file.
func (a *attempt) processFile(rel string) (bool, string) {
	abs := filepath.Join(a.wt, rel)
	b, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Sprintf("could not read conflicted file %s: %v", abs, err)
	}
	rebuilt, cls, halt := ResolveText(abs, string(b))
	a.cls = append(a.cls, cls...)
	if halt != "" {
		return false, halt
	}
	if err := os.WriteFile(abs, []byte(rebuilt), 0o644); err != nil {
		return false, fmt.Sprintf("could not write merged file %s: %v", abs, err)
	}
	if r := a.git("add", rel); r.Code != 0 {
		return false, fmt.Sprintf("git add %s failed: %s", abs, strings.TrimSpace(r.Stderr))
	}
	return filepath.Base(rel) == "__init__.py", ""
}

func (a *attempt) regenerateUVLock() (bool, string) {
	lockDir := filepath.Join(a.o.Root, ".kittify")
	os.MkdirAll(lockDir, 0o755)
	if f, err := os.OpenFile(filepath.Join(lockDir, "auto-rebase-uv-lock.lock"), os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	}
	if _, err := exec.LookPath("uv"); err != nil {
		return false, "uv binary not found on PATH"
	}
	cmd := exec.Command("uv", "lock", "--no-upgrade")
	cmd.Dir = a.wt
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		s := se.String()
		if s == "" {
			s = so.String()
		}
		return false, strings.TrimSpace(s)
	}
	return true, ""
}

func (a *attempt) resolveUVLock() (bool, string) {
	lock := filepath.Join(a.wt, uvLockFilename)
	if _, err := os.Lstat(lock); err == nil {
		if err := os.Remove(lock); err != nil {
			return false, fmt.Sprintf("could not remove conflicted uv.lock: %v", err)
		}
	}
	if ok, msg := a.regenerateUVLock(); !ok {
		return false, msg
	}
	if r := a.git("add", uvLockFilename); r.Code != 0 {
		return false, "git add uv.lock failed: " + strings.TrimSpace(r.Stderr)
	}
	return true, ""
}

func (a *attempt) ruffFix(rel string) (bool, string) {
	if _, err := exec.LookPath("ruff"); err != nil {
		return true, ""
	}
	cmd := exec.Command("ruff", "check", "--fix", "--select", "I001", filepath.Join(a.wt, rel))
	cmd.Dir = a.wt
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return true, ""
	}
	if code != 0 && code != 1 {
		s := se.String()
		if s == "" {
			s = so.String()
		}
		return false, strings.TrimSpace(s)
	}
	return true, ""
}

// Attempt is attempt_auto_rebase: merge the mission branch into the lane
// worktree, resolving conflicts by the classifier rules.
func Attempt(o Options) Report {
	if o.Disposable == nil {
		if p := lanes.ParseBranch(o.Branch); p != nil {
			o.Disposable = LaneResidue(o.Root, p.MissionSlug)
		}
	}
	a := &attempt{o: o, wt: o.Worktree, lane: o.LaneID}
	a.ensureUser()
	if h := a.refusePreexistingLaneStatusDeletions(); h != "" {
		return Report{LaneID: o.LaneID, Attempted: true, HaltReason: h}
	}
	if o.EnsureDrivers != nil {
		o.EnsureDrivers()
	}
	m := a.git("merge", "--no-edit", "--no-ff", "--no-commit", o.MissionBranch)
	if m.Code == 0 {
		if h := a.refreshStagedStatus(); h != "" {
			return a.fail(h)
		}
		if e := a.reapplySparse(); e != "" {
			return a.fail("sparse checkout cleanup failed: " + e)
		}
		if a.mergeInProgress() {
			if c := a.git("-c", "commit.gpgsign=false", "commit", "--no-edit"); c.Code != 0 {
				return a.fail("merge commit failed: " + firstNonEmpty(c.Stderr, c.Stdout))
			}
		}
		lanes.RecordTip(o.Root, o.Branch)
		return Report{LaneID: o.LaneID, Attempted: true, Succeeded: true, Classifications: a.cls}
	}
	var conflicted []string
	if r := a.git("diff", "--name-only", "--diff-filter=U", "-z"); r.Code == 0 {
		for _, p := range strings.Split(r.Stdout, "\x00") {
			if p != "" {
				conflicted = append(conflicted, p)
			}
		}
	}
	if len(conflicted) == 0 {
		return a.fail(fmt.Sprintf("git merge failed without conflicts on %s: %s", o.Branch, firstNonEmpty(m.Stderr, m.Stdout)))
	}
	remaining, halt := a.resolveManaged(conflicted)
	if halt != "" {
		return a.fail(halt)
	}
	var inits []string
	uvSeen := false
	for _, rel := range remaining {
		if filepath.Base(rel) == uvLockFilename {
			a.cls = append(a.cls, Classification{FilePath: filepath.Join(a.wt, rel), Res: auto("", RuleUVLock)})
			uvSeen = true
			continue
		}
		isInit, halt := a.processFile(rel)
		if halt != "" {
			return a.fail(halt)
		}
		if isInit {
			inits = append(inits, rel)
		}
	}
	if uvSeen {
		if ok, msg := a.resolveUVLock(); !ok {
			return a.fail(RuleUVLock + ": " + msg)
		}
	}
	for _, rel := range inits {
		if ok, msg := a.ruffFix(rel); !ok {
			return a.fail(RuleInitImports + ": ruff failed: " + msg)
		}
		a.git("add", rel)
	}
	if h := a.refreshStagedStatus(); h != "" {
		return a.fail(h)
	}
	if e := a.reapplySparse(); e != "" {
		return a.fail("sparse checkout cleanup failed: " + e)
	}
	ruleSet := map[string]bool{}
	for _, c := range a.cls {
		if c.Res.Auto {
			ruleSet[c.Res.RuleID] = true
		}
	}
	msg := fmt.Sprintf("auto-rebase(lane=%s): %d conflicts resolved by classifier rules [%s]", o.LaneID, len(a.cls), strings.Join(sortedKeys(ruleSet), ", "))
	if !a.mergeInProgress() {
		return a.fail("merge commit failed: no merge, revert, cherry-pick or squash is in progress")
	}
	if c := a.git("-c", "commit.gpgsign=false", "commit", "-m", msg); c.Code != 0 {
		return a.fail("merge commit failed: " + firstNonEmpty(c.Stderr, c.Stdout))
	}
	lanes.RecordTip(o.Root, o.Branch)
	return Report{LaneID: o.LaneID, Attempted: true, Succeeded: true, Classifications: a.cls}
}

func firstNonEmpty(a, b string) string {
	if s := strings.TrimSpace(a); s != "" {
		return s
	}
	return strings.TrimSpace(b)
}

// driverDefs are the custom merge drivers (name, description, subcommand).
var driverDefs = [][3]string{
	{"spec-kitty-event-log", "Spec Kitty event log union merge", "merge-driver-event-log"},
	{"spec-kitty-meta", "Spec Kitty mission meta field merge", "merge-driver-meta"},
	{"spec-kitty-traces", "Spec Kitty mission traces union merge", "merge-driver-traces"},
	{"spec-kitty-acceptance-matrix", "Spec Kitty acceptance matrix filled-side merge", "merge-driver-acceptance-matrix"},
	{"spec-kitty-issue-matrix", "Spec Kitty issue matrix row-aware merge", "merge-driver-issue-matrix"},
	{"spec-kitty-review-cycle", "Spec Kitty review-cycle verdict collision refusal", "merge-driver-review-cycle"},
	{"spec-kitty-decision-index", "Spec Kitty decision-index entry union merge", "merge-driver-decision-index"},
}

// EnsureDriverConfig is _ensure_merge_driver_git_config: define the custom
// merge drivers in the repository config (info/attributes is not seeded).
func EnsureDriverConfig(root string) {
	for _, d := range driverDefs {
		if !gitx.OK(root, "config", "--get", "merge."+d[0]+".driver") {
			gitx.Run(root, "config", "merge."+d[0]+".name", d[1])
			gitx.Run(root, "config", "merge."+d[0]+".driver", "spec-kitty "+d[2]+" %O %A %B")
		}
	}
}

// MergeEnv is _make_merge_env: PATH prefixed with this executable's
// directory (so `spec-kitty merge-driver-*` resolves to this binary) and
// the meta driver in three-way mode.
func MergeEnv() []string {
	dir := ""
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	return []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"), mergedriver.MetaTwoWayEnv + "="}
}
