package setup

// Auto-commit of upgrade churn (upgrade/autocommit.py): the git-status
// baseline captured before the run, the upgrade-time mutation log that
// stands in for upstream's sys.audit hook on shutil/os moves and copies, the
// commit-candidate selection (prepare_upgrade_commit_files), the index
// untracks of migrations that `git rm --cached` a now-ignored path, and the
// commit itself.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
)

const (
	upgradeCommitSkipWarning      = "Could not auto-commit upgrade changes; please review and commit manually."
	detachedHeadWarning           = "Checkout is on a detached HEAD; skipped auto-committing upgrade changes — please review and commit manually."
	branchDetectionFailedWarning  = "Could not determine the current branch; skipped auto-committing upgrade changes — please review and commit manually."
	metadataDirtyAtBaselineWarn   = ".kittify/metadata.yaml had uncommitted changes before the upgrade; its new schema stamp was left uncommitted — commit it yourself."
	repairErrorsLeftUncommitted   = "Activation or repair preparation reported errors, so the upgrade changes were left uncommitted — review them and commit them yourself."
	heldForReviewSkippedCommitFmt = "Skipped auto-commit of %d file(s) held for manual review: %s"
	heldForReviewNotCommittedFmt  = "Held for manual review (not committed): %s"
)

// ---------------------------------------------------------------------------
// Mutation log (autocommit._MUTATION_EVENTS / record_upgrade_mutation)
// ---------------------------------------------------------------------------

type mutationEvent struct {
	seq      int
	src, dst string
	move     bool
}

var (
	mutMu     sync.Mutex
	mutEvents []mutationEvent
	mutSeq    int
)

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// recordUpgradeMutation logs a move (rename) or a copy from src to dst, as
// the upstream audit hook does for shutil.move/os.rename/os.replace and
// shutil.copyfile/copytree.
func recordUpgradeMutation(src, dst string, move bool) {
	mutMu.Lock()
	defer mutMu.Unlock()
	mutSeq++
	mutEvents = append(mutEvents, mutationEvent{mutSeq, absPath(src), absPath(dst), move})
}

func init() {
	migrate.OnLayoutMove = func(src, dst string) { recordUpgradeMutation(src, dst, true) }
}

// mvRename is os.rename/shutil.move with the mutation recorded first (the
// audit event fires before the operation, whether or not it succeeds).
func mvRename(src, dst string) error {
	recordUpgradeMutation(src, dst, true)
	return os.Rename(src, dst)
}

func mutationSequence() int {
	mutMu.Lock()
	defer mutMu.Unlock()
	return mutSeq
}

// recordedOrigin is autocommit._recorded_origin.
func recordedOrigin(checkout, dest string, after int) (string, bool) {
	root := absPath(checkout)
	cand := filepath.Join(root, filepath.FromSlash(dest))
	mutMu.Lock()
	events := append([]mutationEvent(nil), mutEvents...)
	mutMu.Unlock()
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.seq <= after {
			break
		}
		var rem string
		switch {
		case cand == e.dst:
			rem = ""
		case strings.HasPrefix(cand, e.dst+string(filepath.Separator)):
			rem = cand[len(e.dst)+1:]
		default:
			continue
		}
		src := e.src
		if rem != "" {
			src = filepath.Join(src, rem)
		}
		rel, err := filepath.Rel(root, src)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", e.move
		}
		return filepath.ToSlash(rel), e.move
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Git status snapshots
// ---------------------------------------------------------------------------

type statusOrigin struct {
	src    string
	rename bool
}

type gitStatusSnapshot struct {
	paths          map[string]bool
	origins        map[string]statusOrigin
	indexDeletions map[string]bool
}

// gitBaseline is the _GitStatusPaths captured before the run.
type gitBaseline struct {
	paths   map[string]bool
	ignored []string
	seq     int
}

func normStatusPath(p string) string {
	p = filepath.ToSlash(p)
	return strings.TrimPrefix(p, "./")
}

func statusRecords(root string, args ...string) ([][3]string, bool) {
	r, err := gitx.Exec(root, nil, nil, append([]string{"status", "--porcelain=v1", "-z"}, args...)...)
	if err != nil || r.Code != 0 {
		return nil, false
	}
	var out [][3]string
	fields := strings.Split(r.Stdout, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		rec := [3]string{f[:2], f[3:], ""}
		if f[0] == 'R' || f[0] == 'C' || f[1] == 'R' || f[1] == 'C' {
			if i+1 < len(fields) {
				rec[2] = fields[i+1]
				i++
			}
		}
		out = append(out, rec)
	}
	return out, true
}

// gitStatusPaths is autocommit.git_status_paths.
func gitStatusPaths(root string) (*gitStatusSnapshot, bool) {
	recs, ok := statusRecords(root, "--untracked-files=all")
	if !ok {
		return nil, false
	}
	s := &gitStatusSnapshot{paths: map[string]bool{}, origins: map[string]statusOrigin{}, indexDeletions: map[string]bool{}}
	for _, r := range recs {
		p := normStatusPath(r[1])
		if p == "" {
			continue
		}
		s.paths[p] = true
		if r[0][0] == 'D' {
			s.indexDeletions[p] = true
		}
		if r[2] != "" {
			if src := normStatusPath(r[2]); src != "" {
				s.origins[p] = statusOrigin{src, strings.Contains(r[0], "R")}
			}
		}
	}
	return s, true
}

// captureGitBaseline is autocommit.capture_upgrade_baseline.
func captureGitBaseline(root string) *gitBaseline {
	s, ok := gitStatusPaths(root)
	if !ok {
		return nil
	}
	b := &gitBaseline{paths: s.paths, seq: mutationSequence()}
	if recs, ok := statusRecords(root, "--untracked-files=normal", "--ignored"); ok {
		for _, r := range recs {
			if r[0] == "!!" {
				if p := normStatusPath(r[1]); p != "" {
					b.ignored = append(b.ignored, p)
				}
			}
		}
	}
	return b
}

func underIgnored(p string, ignored []string) bool {
	for _, e := range ignored {
		if p == e || p+"/" == e || (strings.HasSuffix(e, "/") && strings.HasPrefix(p, e)) {
			return true
		}
	}
	return false
}

// isUpgradeCommitEligible is autocommit.is_upgrade_commit_eligible.
func isUpgradeCommitEligible(p, checkout string) bool {
	if p == "" || strings.HasPrefix(p, "../") {
		return false
	}
	home, _ := filepath.EvalSymlinks(os.Getenv("HOME"))
	rr, _ := filepath.EvalSymlinks(checkout)
	if rr != "" && rr == home {
		return !(strings.HasPrefix(p, ".kittify/") || !strings.Contains(p, "/"))
	}
	return true
}

// pathPartsLess orders POSIX paths the way sorted() orders Path objects:
// component-wise.
func pathPartsLess(a, b string) bool {
	pa, pb := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// expandUpgradeCommitPath is autocommit.expand_upgrade_commit_path.
func expandUpgradeCommitPath(checkout, rel string) []string {
	abs := filepath.Join(checkout, filepath.FromSlash(rel))
	if st, err := os.Lstat(abs); err == nil && st.IsDir() {
		var out []string
		filepath.Walk(abs, func(p string, info os.FileInfo, err error) error {
			if err != nil || p == abs {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				if t, err := os.Stat(p); err == nil && t.IsDir() {
					return nil
				}
			}
			r, _ := filepath.Rel(checkout, p)
			out = append(out, filepath.ToSlash(r))
			return nil
		})
		sort.Slice(out, func(i, j int) bool { return pathPartsLess(out[i], out[j]) })
		return out
	}
	return []string{rel}
}

// upgradeCommitCandidates is autocommit.prepare_upgrade_commit_files.
func upgradeCommitCandidates(root string, b *gitBaseline) []string {
	if b == nil {
		return nil
	}
	cur, ok := gitStatusPaths(root)
	if !ok {
		return nil
	}
	type cand struct {
		path, src string
		stage     bool
	}
	keys := make([]string, 0, len(cur.paths))
	for p := range cur.paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	var cands []cand
	for _, p := range keys {
		src, stage := "", false
		if o, ok := cur.origins[p]; ok {
			src, stage = o.src, o.rename
		} else {
			var move bool
			src, move = recordedOrigin(root, p, b.seq)
			stage = move && src != "" && cur.paths[src]
		}
		if b.paths[p] || (src != "" && b.paths[src]) {
			continue
		}
		if underIgnored(p, b.ignored) || (src != "" && underIgnored(src, b.ignored)) {
			continue
		}
		if cur.indexDeletions[p] && lexists(filepath.Join(root, filepath.FromSlash(p))) {
			continue
		}
		if !isUpgradeCommitEligible(p, root) || (src != "" && !isUpgradeCommitEligible(src, root)) {
			continue
		}
		cands = append(cands, cand{p, src, stage})
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		stagePaths := []string{c.path}
		if c.src != "" && c.stage {
			stagePaths = append(stagePaths, c.src)
		}
		for _, sp := range stagePaths {
			for _, e := range expandUpgradeCommitPath(root, sp) {
				if seen[e] || underIgnored(e, b.ignored) {
					continue
				}
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return out
}

// isProjectedSkillsPath is skills.ownership.is_projected_skills_path.
func isProjectedSkillsPath(rel string, owned map[string]bool) bool {
	const prefix, suffix = ".agents/skills/spec-kitty.", "/SKILL.md"
	if strings.HasPrefix(rel, prefix) && strings.HasSuffix(rel, suffix) && len(rel) >= len(prefix)+len(suffix) {
		cmd := rel[len(prefix) : len(rel)-len(suffix)]
		for _, c := range agents.ConsumerCommands {
			if c == cmd {
				return true
			}
		}
	}
	return owned[rel]
}

// manifestOwnedSkillsPaths is skills.ownership.manifest_owned_skills_paths.
func manifestOwnedSkillsPaths(root string) map[string]bool {
	owned := map[string]bool{}
	for p := range agents.LoadCommandSkillsManifest(root) {
		owned[p] = true
	}
	if m := agents.LoadSkillsManifest(root); m != nil {
		for _, e := range m.Entries {
			owned[e.InstalledPath] = true
		}
	}
	return owned
}

// migrationIndexUntracks is autocommit.migration_index_untracks.
func migrationIndexUntracks(root string, b *gitBaseline) []string {
	if b == nil {
		return nil
	}
	cur, ok := gitStatusPaths(root)
	if !ok {
		return nil
	}
	var cands []string
	for p := range cur.indexDeletions {
		if b.paths[p] || underIgnored(p, b.ignored) || !isUpgradeCommitEligible(p, root) || !lexists(filepath.Join(root, filepath.FromSlash(p))) {
			continue
		}
		cands = append(cands, p)
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Strings(cands)
	recs, ok := statusRecords(root, append([]string{"--untracked-files=all", "--ignored", "--"}, cands...)...)
	if !ok {
		return nil
	}
	ignoredNow := map[string]bool{}
	for _, r := range recs {
		if r[0] == "!!" {
			ignoredNow[normStatusPath(r[1])] = true
		}
	}
	owned := manifestOwnedSkillsPaths(root)
	var out []string
	for _, p := range cands {
		if ignoredNow[p] && (!strings.HasPrefix(p, ".agents/skills/") || isProjectedSkillsPath(p, owned)) {
			out = append(out, p)
		}
	}
	return out
}

// commitUpgradeChurn is autocommit.commit_touched_checkout.
func commitUpgradeChurn(root string, b *gitBaseline, from, to string) (bool, []string, string) {
	files := upgradeCommitCandidates(root, b)
	dels := migrationIndexUntracks(root, b)
	if len(files) == 0 && len(dels) == 0 {
		return false, nil, ""
	}
	paths := append(append([]string(nil), files...), dels...)
	r, err := gitx.Exec(root, nil, nil, "branch", "--show-current")
	if err != nil || r.Code != 0 {
		return false, paths, branchDetectionFailedWarning
	}
	if strings.TrimSpace(r.Stdout) == "" {
		return false, paths, detachedHeadWarning
	}
	msg := fmt.Sprintf("chore: apply spec-kitty upgrade changes (%s -> %s)", from, to)
	if len(dels) > 0 {
		if !commitWithIndexDeletions(root, msg, files, dels) {
			return false, paths, upgradeCommitSkipWarning
		}
	} else {
		if _, err := gitx.Run(root, append([]string{"add", "-f", "--"}, files...)...); err != nil {
			return false, paths, upgradeCommitSkipWarning
		}
		if _, err := gitx.Run(root, append([]string{"commit", "-q", "--no-verify", "-m", msg, "--"}, files...)...); err != nil {
			return false, paths, upgradeCommitSkipWarning
		}
	}
	warn := ""
	if b.paths[".kittify/metadata.yaml"] {
		warn = metadataDirtyAtBaselineWarn
	}
	return true, paths, warn
}

// commitWithIndexDeletions is commit_helpers._commit_with_index_deletions:
// commit the requested adds and the index removals from a temporary index
// seeded from HEAD, then sync the real index for those paths.
func commitWithIndexDeletions(root, msg string, files, dels []string) bool {
	gd, err := gitx.Run(root, "rev-parse", "--absolute-git-dir")
	if err != nil || strings.TrimSpace(gd) == "" {
		return false
	}
	tmp := filepath.Join(strings.TrimSpace(gd), fmt.Sprintf("spec-kitty-index-deletions.%d.tmp", os.Getpid()))
	defer os.Remove(tmp)
	defer os.Remove(tmp + ".lock")
	env := []string{"GIT_INDEX_FILE=" + tmp}
	run := func(args ...string) bool {
		r, err := gitx.Exec(root, env, nil, args...)
		return err == nil && r.Code == 0
	}
	if !run("read-tree", "HEAD") {
		return false
	}
	for _, f := range files {
		if !run("add", "--force", "--", f) {
			return false
		}
	}
	if !run(append([]string{"rm", "--cached", "--quiet", "--ignore-unmatch", "--"}, dels...)...) {
		return false
	}
	if run("diff", "--cached", "--quiet") {
		return false
	}
	if !run("-c", "commit.gpgsign=false", "commit", "-m", msg) {
		return false
	}
	gitx.Exec(root, nil, nil, append([]string{"reset", "-q", "HEAD", "--"}, append(append([]string(nil), files...), dels...)...)...)
	return true
}
