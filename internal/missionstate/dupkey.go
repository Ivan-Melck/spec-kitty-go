package missionstate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Port of status/dup_key_repair.py: the raw-text scanner for legacy dual-key
// review_feedback artifacts and its keep-last-non-empty planner.

var topLevelKeyRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):(.*)$`)

type keyOcc struct {
	idx   int
	empty bool
}

// DupKeyError is DuplicateKeyRepairError.
type DupKeyError struct{ Msg string }

func (e *DupKeyError) Error() string { return e.Msg }

func frontmatterClosing(lines []string) int {
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "---") {
		return -1
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i
		}
	}
	return -1
}

func scanOccurrences(lines []string, closing int) ([]string, map[string][]keyOcc) {
	var order []string
	seen := map[string][]keyOcc{}
	for i := 1; i < closing; i++ {
		m := topLevelKeyRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[2])
		if _, ok := seen[m[1]]; !ok {
			order = append(order, m[1])
		}
		seen[m[1]] = append(seen[m[1]], keyOcc{i, v == "" || v == "''" || v == `""`})
	}
	var dupOrder []string
	dups := map[string][]keyOcc{}
	for _, k := range order {
		if len(seen[k]) > 1 {
			dupOrder = append(dupOrder, k)
			dups[k] = seen[k]
		}
	}
	return dupOrder, dups
}

func hasDuplicateKeys(text string) bool {
	lines := strings.Split(text, "\n")
	c := frontmatterClosing(lines)
	if c < 0 {
		return false
	}
	order, _ := scanOccurrences(lines, c)
	return len(order) > 0
}

// detectDupKeyArtifacts is detect_duplicate_key_artifacts: every *.md under
// scanDir (path order) carrying a duplicated top-level frontmatter key.
func detectDupKeyArtifacts(scanDir string) []string {
	var paths []string
	_ = filepath.WalkDir(scanDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	sort.SliceStable(paths, func(i, j int) bool { return migrate.PyPathLess(paths[i], paths[j]) })
	var out []string
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text, err := pyjson.DecodeUTF8(b)
		if err != nil {
			continue
		}
		if hasDuplicateKeys(text) {
			out = append(out, p)
		}
	}
	return out
}

type dupPlan struct {
	path, repaired string
}

func removalSafe(lines []string, idx, closing int) bool {
	m := topLevelKeyRe.FindStringSubmatch(lines[idx])
	if m == nil {
		return false
	}
	if strings.TrimSpace(m[2]) != "" {
		return true
	}
	if idx+1 >= closing {
		return true
	}
	return topLevelKeyRe.MatchString(lines[idx+1])
}

// planDupKeyRepair is plan_artifact_repair.
func planDupKeyRepair(path, text string) (*dupPlan, error) {
	lines := strings.Split(text, "\n")
	closing := frontmatterClosing(lines)
	if closing < 0 {
		return nil, nil
	}
	order, dups := scanOccurrences(lines, closing)
	if len(order) == 0 {
		return nil, nil
	}
	remove := map[int]bool{}
	for _, key := range order {
		occ := dups[key]
		survivor := occ[len(occ)-1]
		for i := len(occ) - 1; i >= 0; i-- {
			if !occ[i].empty {
				survivor = occ[i]
				break
			}
		}
		for _, o := range occ {
			if o.idx == survivor.idx {
				continue
			}
			if !removalSafe(lines, o.idx, closing) {
				return nil, &DupKeyError{fmt.Sprintf("%s: cannot safely repair duplicate key %s at line %d (non-scalar occurrence).", path, pyjson.ReprString(key), o.idx+1)}
			}
			remove[o.idx] = true
		}
	}
	var kept []string
	for i, l := range lines {
		if !remove[i] {
			kept = append(kept, l)
		}
	}
	newClosing := closing - len(remove)
	body := strings.Join(kept[1:newClosing], "\n")
	if _, err := yamlx.Parse([]byte(body)); err != nil {
		return nil, &DupKeyError{fmt.Sprintf("%s: repaired frontmatter is still invalid YAML: %v", path, err)}
	}
	if hasDuplicateKeys(strings.Join(kept, "\n")) {
		return nil, &DupKeyError{fmt.Sprintf("%s: repaired frontmatter is still invalid YAML: duplicate key", path)}
	}
	return &dupPlan{path: path, repaired: strings.Join(kept, "\n")}, nil
}

func dupKeyMissionSlug(scanDir, path string) string {
	rel, err := filepath.Rel(absResolved(scanDir), absResolved(path))
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Base(filepath.Dir(path))
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) > 1 {
		return parts[0]
	}
	return filepath.Base(path)
}

// RepairDuplicateKeys is repair_duplicate_key_artifacts: batch-atomic
// keep-last-non-empty heal of kitty-specs/**/*.md, run before --fix.
func RepairDuplicateKeys(root string, allowDirty bool, argv []string, cliVersion string) (*RepairReport, error) {
	return RepairDuplicateKeysIn(root, "", allowDirty, argv, cliVersion)
}

// RepairDuplicateKeysIn is repair_duplicate_key_artifacts(scan_root=...).
func RepairDuplicateKeysIn(root, scanRoot string, allowDirty bool, argv []string, cliVersion string) (*RepairReport, error) {
	scanDir := absResolved(filepath.Join(root, "kitty-specs"))
	if scanRoot != "" {
		scanDir = absResolved(scanRoot)
	}
	var plans []*dupPlan
	for _, p := range detectDupKeyArtifacts(scanDir) {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		plan, err := planDupKeyRepair(p, string(b))
		if err != nil {
			return nil, err
		}
		if plan != nil {
			plans = append(plans, plan)
		}
	}
	sorted := append([]*dupPlan(nil), plans...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })
	payload := "spec-kitty:dup-key-repair:v1\n"
	for _, p := range sorted {
		payload += repoRelpath(root, p.path) + "\x00" + p.repaired + "\x00"
	}
	runID := sha256Text(payload)[:16]
	manifestAbs := resolveRepoRelative(root, filepath.Join(AuditRoot, dupKeyManifestPrefix+runID+".json"))
	var rels []string
	for _, p := range plans {
		rels = append(rels, repoRelpath(root, p.path))
	}
	if err := assertGitSafe(root, rels, allowDirty); err != nil {
		return nil, err
	}
	release, err := acquireRepairLock(root)
	if err != nil {
		return nil, err
	}
	defer release()
	changes := map[string]FileChange{}
	for _, p := range plans {
		before := fileFingerprint(p.path)
		if err := atomicfile.Write(p.path, []byte(p.repaired)); err != nil {
			return nil, err
		}
		after := fileFingerprint(p.path)
		if !before.equal(after) {
			rel := repoRelpath(root, p.path)
			changes[rel] = FileChange{Path: rel, OldSHA: before.sha, NewSHA: after.sha, OldSize: before.size, NewSize: after.size}
		}
	}
	grouped := map[string]*MissionResult{}
	for _, p := range plans {
		slug := dupKeyMissionSlug(scanDir, p.path)
		r, ok := grouped[slug]
		if !ok {
			r = &MissionResult{Slug: slug, Status: "unchanged"}
			grouped[slug] = r
		}
		if c, ok := changes[repoRelpath(root, p.path)]; ok {
			r.FileChanges = append(r.FileChanges, c)
			r.Status = "updated"
		}
	}
	var slugs []string
	for s := range grouped {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	rep := &RepairReport{
		RunID: runID, RepoHead: gitHead(root), TargetMissions: slugs,
		ManifestPath: repoRelpath(root, manifestAbs), CLIVersion: cliVersion,
		CommandArgs: ScrubSecretArgs(argv), GeneratedIDs: []string{runID},
		Policy: buildPolicy(dupKeyPolicyTracked, nil),
	}
	for _, s := range slugs {
		rep.Missions = append(rep.Missions, grouped[s])
	}
	if len(changes) > 0 {
		if err := atomicfile.Write(manifestAbs, []byte(rep.ToJSON())); err != nil {
			return nil, err
		}
	}
	return rep, nil
}
