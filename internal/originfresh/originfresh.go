// Package originfresh is the origin freshness check every evidence gate
// (consolidate, accept) runs before trusting a clone's status log and lane
// branches (specify_cli.git.origin_freshness + git.origin_gate): one
// ls-remote and at most one fetch per remote, freshness verdicts, the
// enforce/warn/off policy (--origin-check, SPEC_KITTY_ORIGIN_CHECK) and the
// refusal text.
package originfresh

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Env is the opt-out environment variable.
const Env = "SPEC_KITTY_ORIGIN_CHECK"

// Refusal codes.
const (
	StatusStale    = "ORIGIN_STATUS_STALE"
	LaneStale      = "ORIGIN_LANE_STALE"
	LaneDiverged   = "ORIGIN_LANE_DIVERGED"
	Unreachable    = "ORIGIN_UNREACHABLE"
	RemoteAmbig    = "ORIGIN_REMOTE_AMBIGUOUS"
	CompareFailed  = "ORIGIN_COMPARE_FAILED"
	lsRemoteTimout = 30.0
	fetchTimeout   = 120.0
)

// Setting is the resolved mode and its source.
type Setting struct {
	Mode, Source, Warning string
}

// ReadOnly is READ_ONLY_ORIGIN_CHECK (accept --no-commit / --diagnose).
var ReadOnly = Setting{Mode: "warn", Source: "read-only"}

func parseMode(v string) string {
	switch n := strings.ToLower(strings.TrimSpace(v)); n {
	case "enforce", "warn", "off":
		return n
	}
	return ""
}

// ResolveMode is resolve_origin_check_mode: flag beats environment beats
// enforce.
func ResolveMode(flag *string) Setting {
	if flag != nil {
		return Setting{Mode: parseMode(*flag), Source: "flag"}
	}
	raw := strings.TrimSpace(os.Getenv(Env))
	if raw == "" {
		return Setting{Mode: "enforce", Source: "default"}
	}
	if m := parseMode(raw); m != "" {
		return Setting{Mode: m, Source: "environment"}
	}
	return Setting{Mode: "enforce", Source: "environment-invalid",
		Warning: fmt.Sprintf("%s=%s is not one of enforce, off, warn; enforcing the origin freshness check.", Env, pyjson.ReprString(raw))}
}

// NotCheckedWarning is origin_not_checked_warning.
func NotCheckedWarning(s Setting) string {
	return fmt.Sprintf("Origin was not checked (origin check is %s, source: %s): no remote was contacted, so a teammate's newer status evidence or lane work on origin is not seen and stale evidence is accepted.", s.Mode, s.Source)
}

// Verdict is one branch's freshness verdict.
type Verdict struct {
	Branch, Remote, State string
	Behind, Ahead         int
	Scope, Detail         string
	RemoteSHA             string
	TimeoutSeconds        float64
}

// Payload is verdict_payload (the origin_freshness row).
func (v Verdict) Payload() *jsonx.Object {
	opt := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	return jsonx.NewObject("branch", v.Branch, "remote", opt(v.Remote), "state", v.State, "behind", v.Behind,
		"ahead", v.Ahead, "scope", opt(v.Scope), "detail", opt(v.Detail))
}

type probe struct {
	branch string
	scope  []string
}

func (p probe) scopeText() string { return strings.Join(p.scope, ", ") }

func git(root string, args ...string) gitx.Result {
	r, _ := gitx.Exec(root, nil, nil, args...)
	return r
}

func noPromptEnv() []string { return []string{"GIT_TERMINAL_PROMPT=0"} }

// contact runs a remote-contacting git command; a failure is
// "remote <name> unreachable: <first stderr line>".
func contact(root, remote string, args ...string) (gitx.Result, error) {
	r, err := gitx.Exec(root, noPromptEnv(), nil, args...)
	if err != nil || r.Code != 0 {
		detail := strings.TrimSpace(r.Stderr)
		if detail != "" {
			detail = strings.SplitN(detail, "\n", 2)[0]
		} else {
			detail = "no error output"
		}
		return r, fmt.Errorf("remote %s unreachable: %s", remote, detail)
	}
	return r, nil
}

// ConfiguredRemotes is configured_remotes.
func ConfiguredRemotes(root string) []string {
	r := git(root, "remote")
	if r.Code != 0 {
		return nil
	}
	var out []string
	for _, l := range strings.Split(r.Stdout, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ResolveRemote is kernel.git.remote.resolve_remote ("" = none).
func ResolveRemote(root, branch string) string {
	if r := git(root, "config", "--get", "branch."+branch+".remote"); r.Code == 0 {
		if v := strings.TrimSpace(r.Stdout); v != "" && v != "." {
			return v
		}
	}
	rs := ConfiguredRemotes(root)
	if len(rs) == 1 {
		return rs[0]
	}
	for _, x := range rs {
		if x == "origin" {
			return x
		}
	}
	return ""
}

func trackingRef(remote, branch string) string { return "refs/remotes/" + remote + "/" + branch }

func refSHA(root, ref string) string {
	r := git(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if r.Code != 0 {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

// RemoteHeads is remote_heads.
func RemoteHeads(root, remote string, branches []string) (map[string]string, error) {
	heads := map[string]string{}
	if len(branches) == 0 {
		return heads, nil
	}
	args := []string{"ls-remote", "--heads", remote}
	wanted := map[string]string{}
	for _, b := range branches {
		args = append(args, "refs/heads/"+b)
		wanted["refs/heads/"+b] = b
	}
	r, err := contact(root, remote, args...)
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(r.Stdout, "\n") {
		sha, ref, ok := strings.Cut(l, "\t")
		if ok {
			if b, hit := wanted[strings.TrimSpace(ref)]; hit {
				heads[b] = strings.TrimSpace(sha)
			}
		}
	}
	return heads, nil
}

// FetchBranches is fetch_branches (known tips skip the transfer).
func FetchBranches(root, remote string, branches []string, known map[string]string) error {
	var want []string
	for _, b := range branches {
		if tip, ok := known[b]; ok && refSHA(root, trackingRef(remote, b)) == tip {
			continue
		}
		want = append(want, b)
	}
	if len(want) == 0 {
		return nil
	}
	args := []string{"fetch", "--no-tags", "--no-recurse-submodules", "--no-auto-gc", "--refmap=", remote}
	for _, b := range want {
		args = append(args, "+refs/heads/"+b+":"+trackingRef(remote, b))
	}
	_, err := contact(root, remote, args...)
	return err
}

func divergence(root, local, remote string, paths []string) (int, int, error) {
	args := []string{"rev-list", "--left-right", "--count", local + "..." + remote}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	r := git(root, args...)
	if r.Code != 0 {
		return 0, 0, fmt.Errorf("%s", strings.TrimSpace(r.Stderr))
	}
	f := strings.Fields(r.Stdout)
	if len(f) < 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output")
	}
	a, _ := strconv.Atoi(f[0])
	b, _ := strconv.Atoi(f[1])
	return a, b, nil
}

func classify(behind, ahead int, scoped bool) string {
	if behind > 0 {
		if ahead > 0 {
			return "diverged"
		}
		return "behind"
	}
	if ahead > 0 && !scoped {
		return "ahead"
	}
	return "up_to_date"
}

func treeOID(root, ref, path string) string {
	r := git(root, "rev-parse", "--verify", "--quiet", ref+":"+path)
	if r.Code != 0 {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

func compare(root, remote string, p probe, remoteSHA string) Verdict {
	local, rref := "refs/heads/"+p.branch, trackingRef(remote, p.branch)
	ahead, behind, err := divergence(root, local, rref, nil)
	if err == nil && len(p.scope) > 0 {
		_, behind, err = divergence(root, local, rref, p.scope)
	}
	if err != nil {
		return Verdict{Branch: p.branch, Remote: remote, State: "compare_failed", Scope: p.scopeText(), Detail: err.Error(), RemoteSHA: remoteSHA}
	}
	if behind > 0 && len(p.scope) > 0 {
		same := true
		for _, path := range p.scope {
			if treeOID(root, local, path) != treeOID(root, rref, path) {
				same = false
			}
		}
		if same {
			behind = 0
		}
	}
	return Verdict{Branch: p.branch, Remote: remote, State: classify(behind, ahead, len(p.scope) > 0), Behind: behind, Ahead: ahead,
		Scope: p.scopeText(), RemoteSHA: remoteSHA}
}

func checkRemote(root, remote string, probes []probe) []Verdict {
	var names []string
	seen := map[string]bool{}
	for _, p := range probes {
		if !seen[p.branch] {
			seen[p.branch] = true
			names = append(names, p.branch)
		}
	}
	heads, err := RemoteHeads(root, remote, names)
	if err == nil {
		var present []string
		for _, n := range names {
			if _, ok := heads[n]; ok {
				present = append(present, n)
			}
		}
		err = FetchBranches(root, remote, present, heads)
	}
	if err != nil {
		var out []Verdict
		for _, p := range probes {
			out = append(out, Verdict{Branch: p.branch, Remote: remote, State: "unreachable", Scope: p.scopeText(), Detail: err.Error()})
		}
		return out
	}
	var out []Verdict
	for _, p := range probes {
		sha, ok := heads[p.branch]
		switch {
		case !ok:
			detail := ""
			if refSHA(root, trackingRef(remote, p.branch)) != "" {
				detail = fmt.Sprintf("%s/%s was seen here before, but %s no longer lists %s: it may have been deleted on %s", remote, p.branch, remote, p.branch, remote)
			}
			out = append(out, Verdict{Branch: p.branch, Remote: remote, State: "remote_missing", Scope: p.scopeText(), Detail: detail})
		case !gitx.BranchExists(root, p.branch):
			out = append(out, Verdict{Branch: p.branch, Remote: remote, State: "local_missing", Scope: p.scopeText(), RemoteSHA: sha})
		default:
			out = append(out, compare(root, remote, p, sha))
		}
	}
	return out
}

func unresolvedVerdict(root string, p probe) Verdict {
	rs := ConfiguredRemotes(root)
	if len(rs) == 0 {
		detail := ""
		if r := git(root, "for-each-ref", "--count=1", "--format=%(refname)", "refs/remotes/"); r.Code == 0 && strings.TrimSpace(r.Stdout) != "" {
			detail = "This clone has remote-tracking refs but no remote is configured (the remote was removed?), so nothing was checked against origin: a teammate's newer status evidence or lane work would not be seen"
		}
		return Verdict{Branch: p.branch, State: "no_remote", Scope: p.scopeText(), Detail: detail}
	}
	return Verdict{Branch: p.branch, State: "remote_ambiguous", Scope: p.scopeText(), Detail: strings.Join(rs, ", ")}
}

func checkProbes(root string, probes []probe) []Verdict {
	found := make([]Verdict, len(probes))
	byRemote := map[string][]int{}
	var order []string
	for i, p := range probes {
		r := ResolveRemote(root, p.branch)
		if r == "" {
			found[i] = unresolvedVerdict(root, p)
			continue
		}
		if _, ok := byRemote[r]; !ok {
			order = append(order, r)
		}
		byRemote[r] = append(byRemote[r], i)
	}
	for _, r := range order {
		var ps []probe
		for _, i := range byRemote[r] {
			ps = append(ps, probes[i])
		}
		for k, v := range checkRemote(root, r, ps) {
			found[byRemote[r][k]] = v
		}
	}
	return found
}

// evidenceBranch is the STATUS_STATE write target of the mission.
func evidenceBranch(root, slug string) string {
	s := coord.Resolve(root, slug)
	if s.CoordBranch != "" {
		return s.CoordBranch
	}
	if t := strings.TrimSpace(s.TargetBranch()); t != "" {
		return t
	}
	return gitx.CurrentBranch(root)
}

// ApprovedLaneBranches is approved_lane_branches: code lanes that are not
// fully canceled and (on a resume) not already consolidated.
func ApprovedLaneBranches(root, slug string, m *lanes.Manifest, completed map[string]bool) []string {
	canceled := map[string]bool{}
	if snap, err := status.ReduceDir(coord.Resolve(root, slug).StatusDir()); err == nil {
		for _, id := range snap.WorkPackages.Keys() {
			if st := snap.WP(id); st != nil && st.GetString("lane") == "canceled" {
				canceled[id] = true
			}
		}
	}
	var out []string
	for _, l := range m.Lanes {
		if l.LaneID == lanes.PlanningLaneID {
			continue
		}
		allCanceled, allDone := len(l.WPIDs) > 0, len(l.WPIDs) > 0
		for _, id := range l.WPIDs {
			allCanceled = allCanceled && canceled[id]
			allDone = allDone && completed[id]
		}
		if allCanceled || allDone {
			continue
		}
		out = append(out, lanes.LaneBranchName(slug, l.LaneID, m.TargetBranch))
	}
	return out
}

// Refused is OriginFreshnessRefused.
type Refused struct {
	Code     string
	Codes    []string
	Verdicts []Verdict
	Message  string
	Headline string
}

func (e *Refused) Error() string { return e.Message }

func codeFor(v Verdict, evidence bool) string {
	switch v.State {
	case "unreachable":
		return Unreachable
	case "remote_ambiguous":
		return RemoteAmbig
	case "compare_failed":
		return CompareFailed
	case "behind", "diverged", "local_missing":
		if evidence {
			return StatusStale
		}
		return LaneStale
	}
	return ""
}

func headline(code string, v Verdict) string {
	where := v.Remote + "/" + v.Branch
	switch v.State {
	case "unreachable":
		d := v.Detail
		if d == "" {
			d = "the remote did not answer"
		}
		return fmt.Sprintf("%s: %s is unreachable (%s)", code, v.Branch, d)
	case "remote_ambiguous":
		return fmt.Sprintf("%s: %s has no single remote to check (remotes: %s)", code, v.Branch, v.Detail)
	case "compare_failed":
		d := v.Detail
		if d == "" {
			d = "git could not compare the refs"
		}
		return fmt.Sprintf("%s: %s could not be compared with %s (%s)", code, v.Branch, where, d)
	case "local_missing":
		return fmt.Sprintf("%s: %s is missing locally (it exists as %s)", code, v.Branch, where)
	}
	return fmt.Sprintf("%s: %s is %s (%d behind / %d ahead of %s)", code, v.Branch, v.State, v.Behind, v.Ahead, where)
}

const rerunLine = "  Then re-run."

func statusSteps(v Verdict, checkout string) []string {
	if v.State != "diverged" {
		if checkout == "" {
			return []string{fmt.Sprintf("git fetch %s %s:%s", v.Remote, v.Branch, v.Branch)}
		}
		return []string{fmt.Sprintf("git -C %s pull %s %s", checkout, v.Remote, v.Branch)}
	}
	if checkout != "" {
		return []string{fmt.Sprintf("git -C %s pull --no-rebase %s %s", checkout, v.Remote, v.Branch)}
	}
	return []string{fmt.Sprintf("git worktree add <path> %s    (or: git switch %s)", v.Branch, v.Branch),
		fmt.Sprintf("git pull --no-rebase %s %s    (in that checkout)", v.Remote, v.Branch)}
}

func remedy(code string, v Verdict, evidence bool, checkout string, mergeRecord bool) []string {
	switch v.State {
	case "remote_ambiguous":
		return []string{fmt.Sprintf("  Name the remote that owns it: git config branch.%s.remote <name>    (remotes: %s)", v.Branch, v.Detail), rerunLine}
	case "unreachable":
		return []string{fmt.Sprintf("  Check network access and credentials for remote %s.", v.Remote),
			fmt.Sprintf("  If you cannot reach it from here at all, %s=off (or --origin-check off) skips the contact and accepts stale evidence.", Env), rerunLine}
	case "compare_failed":
		return []string{fmt.Sprintf("  The remote answered, but git could not compare %s with %s/%s here (a shallow clone or a damaged ref).", v.Branch, v.Remote, v.Branch),
			fmt.Sprintf("  Deepen the history with: git fetch --unshallow %s    (or repair the ref), then re-run.", v.Remote),
			fmt.Sprintf("  If you cannot repair it, %s=off (or --origin-check off) skips the check and accepts stale evidence.", Env)}
	}
	if evidence && v.State != "local_missing" {
		steps := statusSteps(v, checkout)
		if mergeRecord {
			out := []string{"  A consolidation record exists. Recover with:", "    spec-kitty consolidate --abort"}
			for _, s := range steps {
				out = append(out, "    "+s)
			}
			return append(out, "    spec-kitty consolidate")
		}
		if len(steps) == 1 {
			return []string{"  Update it with: " + steps[0], rerunLine}
		}
		out := []string{"  Update it with:"}
		for _, s := range steps {
			out = append(out, "    "+s)
		}
		return append(out, rerunLine)
	}
	switch v.State {
	case "diverged":
		return []string{fmt.Sprintf("  Inspect: git log %s...%s/%s", v.Branch, v.Remote, v.Branch),
			fmt.Sprintf("  If the remote lane has work you need: git -C <lane worktree> merge %s/%s", v.Remote, v.Branch),
			fmt.Sprintf("  If the local lane is the truth: merge or rebase it onto %s/%s first, then git push %s %s", v.Remote, v.Branch, v.Remote, v.Branch),
			fmt.Sprintf("    (git push --force-with-lease would instead discard the commits on %s/%s: use it only if you mean to lose them.)", v.Remote, v.Branch),
			rerunLine}
	case "local_missing":
		return []string{fmt.Sprintf("  Create it with: git branch %s %s/%s", v.Branch, v.Remote, v.Branch), rerunLine}
	}
	return []string{fmt.Sprintf("  Update it with: git fetch %s %s", v.Remote, v.Branch),
		fmt.Sprintf("    then git branch -f %s %s/%s", v.Branch, v.Remote, v.Branch),
		fmt.Sprintf("    (or, when the lane is checked out: git -C <lane worktree> merge --ff-only %s/%s)", v.Remote, v.Branch),
		rerunLine}
}

func worktreeHolding(root, branch string) string {
	r := git(root, "worktree", "list", "--porcelain")
	cur := ""
	for _, l := range strings.Split(r.Stdout, "\n") {
		if strings.HasPrefix(l, "worktree ") {
			cur = strings.TrimPrefix(l, "worktree ")
		} else if l == "branch refs/heads/"+branch {
			return cur
		}
	}
	return ""
}

// Run is run_origin_gate: the warnings to surface, or a refusal (enforce).
func Run(root, slug string, s Setting, laneBranches []string, mergeRecord bool) ([]string, *Refused) {
	if s.Mode == "off" {
		return []string{NotCheckedWarning(s)}, nil
	}
	var ps []probe
	eb := evidenceBranch(root, slug)
	if eb != "" {
		ps = append(ps, probe{eb, []string{"kitty-specs/" + slug + "/" + status.EventsFile}})
	}
	for _, b := range laneBranches {
		ps = append(ps, probe{branch: b})
	}
	verdicts := checkProbes(root, ps)
	var evidence *Verdict
	lanesV := verdicts
	if eb != "" {
		evidence = &verdicts[0]
		lanesV = verdicts[1:]
		if evidence.State == "local_missing" && coord.Resolve(root, slug).CoordBranch != "" {
			evidence = nil
		}
	}
	var warnings []string
	if s.Warning != "" {
		warnings = append(warnings, s.Warning)
	}
	all := lanesV
	if evidence != nil {
		all = append([]Verdict{*evidence}, lanesV...)
	}
	seenNote := map[string]bool{}
	for _, v := range all {
		if (v.State == "remote_missing" || v.State == "no_remote") && v.Detail != "" && !seenNote[v.Detail] {
			seenNote[v.Detail] = true
			warnings = append(warnings, v.Detail)
		}
	}
	type violation struct {
		code     string
		v        Verdict
		evidence bool
	}
	var vs []violation
	if evidence != nil {
		if c := codeFor(*evidence, true); c != "" {
			vs = append(vs, violation{c, *evidence, true})
		}
	}
	for _, v := range lanesV {
		if c := codeFor(v, false); c != "" {
			vs = append(vs, violation{c, v, false})
		}
	}
	if len(vs) == 0 {
		return warnings, nil
	}
	if s.Mode == "warn" {
		for _, x := range vs {
			warnings = append(warnings, fmt.Sprintf("%s (origin check is %s, source: %s; continuing)", headline(x.code, x.v), s.Mode, s.Source))
		}
		return warnings, nil
	}
	checkout := ""
	if evidence != nil {
		checkout = worktreeHolding(root, evidence.Branch)
	}
	lines := append([]string{}, warnings...)
	var heads, codes []string
	codeSeen := map[string]bool{}
	var failing []Verdict
	for _, x := range vs {
		h := headline(x.code, x.v)
		heads = append(heads, h)
		lines = append(lines, h)
		lines = append(lines, remedy(x.code, x.v, x.evidence, checkout, mergeRecord)...)
		if !codeSeen[x.code] {
			codeSeen[x.code] = true
			codes = append(codes, x.code)
		}
		failing = append(failing, x.v)
	}
	lines = append(lines, fmt.Sprintf("Opt out (not recommended): --origin-check warn, or %s=warn.", Env))
	return nil, &Refused{Code: codes[0], Codes: codes, Verdicts: failing, Message: strings.Join(lines, "\n"), Headline: heads[0]}
}
