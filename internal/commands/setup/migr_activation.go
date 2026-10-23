package setup

// Charter activation / verdict provenance migrations:
// unify_charter_activation_promote_answers, verdict_provenance_backfill.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func init() {
	migrationImpls["normalize_activation_absence"] = neverNeeded("Superseded by the charter-pack cutover")
	migrationImpls["unify_charter_activation_promote_answers"] = mPromoteAnswers()
	migrationImpls["verdict_provenance_backfill"] = mVerdictProvenance()
}

// promotableKinds is _PROMOTABLE_KINDS: (operator token, plural).
var promotableKinds = [][2]string{{"directive", "directives"}, {"tactic", "tactics"}, {"styleguide", "styleguides"}, {"toolguide", "toolguides"},
	{"paradigm", "paradigms"}, {"procedure", "procedures"}, {"agent-profile", "agent_profiles"}, {"mission-step-contract", "mission_step_contracts"}}

func answersPath(root string) string {
	return filepath.Join(root, ".kittify", "charter", "interview", "answers.yaml")
}

// computePromotions is _compute_promotions: the answers-only stems per
// activation key (resolve_selected_id_to_stem against the built-in
// offering) plus the "<token>:<raw>" ids that resolve nowhere.
func computePromotions(root string, answers, cfg *jsonx.Object) (*jsonx.Object, []string) {
	promotions := &jsonx.Object{}
	var unresolved []string
	for _, k := range promotableKinds {
		raw, _ := answers.Get("selected_" + k[1])
		ids, _ := raw.([]any)
		existing := map[string]bool{}
		if cur, ok := cfg.Get("activated_" + k[1]); ok {
			if l, isL := cur.([]any); isL {
				for _, x := range l {
					existing[pyStrValue(x)] = true
				}
			}
		}
		kind, kerr := dct.ParseKind(k[0])
		var stems []any
		seen := map[string]bool{}
		for _, x := range ids {
			id := pyStrValue(x)
			stem := ""
			if kerr == nil {
				stem = charter.SelectedIDToStem(kind, id, nil, nil)
			}
			if stem == "" {
				unresolved = append(unresolved, k[0]+":"+id)
				continue
			}
			if !existing[stem] && !seen[stem] {
				seen[stem] = true
				stems = append(stems, stem)
			}
		}
		if len(stems) > 0 {
			promotions.Set("activated_"+k[1], stems)
		}
	}
	return promotions, unresolved
}

// leftAbsentMessage is PromotionOutcome.left_absent_messages for one key.
func leftAbsentMessage(key, reason string) string {
	return fmt.Sprintf("%s was left absent and nothing was promoted into it: the effective set could not be resolved (%s).", key, reason)
}

func mPromoteAnswers() migrationImpl {
	detect := func(root string) bool {
		if !pathExists(kittify_ConfigPath(root)) || !pathExists(answersPath(root)) {
			return false
		}
		cfg := loadYAMLMappingSafe(kittify_ConfigPath(root))
		ans := loadYAMLMappingSafe(answersPath(root))
		if cfg.Len() == 0 || ans.Len() == 0 {
			return false
		}
		p, _ := computePromotions(root, ans, cfg)
		return p.Len() > 0
	}
	return migrationImpl{
		detect: detect,
		canApply: func(root string) (bool, string) {
			if detect(root) {
				return ok()
			}
			return false, "no answers-only selections to promote"
		},
		apply: func(root string, dryRun bool) migrationResult {
			if !pathExists(kittify_ConfigPath(root)) {
				return migrationResult{Success: true, Changes: []string{"No .kittify/config.yaml found; nothing to promote"}}
			}
			if !pathExists(answersPath(root)) {
				return migrationResult{Success: true, Changes: []string{"No answers.yaml found; nothing to promote"}}
			}
			cfg, err := loadConfigRoundTrip(root)
			if err != nil {
				return migrationResult{Errors: []string{fmt.Sprintf("Invalid .kittify/config.yaml: %v", err)}}
			}
			if cfg == nil {
				return migrationResult{Errors: []string{".kittify/config.yaml root must be a mapping"}}
			}
			ans := loadYAMLMappingSafe(answersPath(root))
			if ans.Len() == 0 {
				return migrationResult{Success: true, Changes: []string{"answers.yaml is empty or has no selections; nothing to promote"}}
			}
			prom, unresolved := computePromotions(root, ans, cfg)
			unresolvedWarn := func() []string {
				if len(unresolved) == 0 {
					return nil
				}
				return []string{"Unresolved answers-only ids skipped (not promoted): " + strings.Join(unresolved, ", ")}
			}
			if prom.Len() == 0 {
				return migrationResult{Success: true, Changes: []string{"No answers-only selections to promote"}, Warnings: unresolvedWarn()}
			}
			// resolve_effective_sets for the absent keys (FR-015, #4400):
			// an absent key is seeded from its effective set, or left
			// absent and reported when that set is unresolved.
			type effective struct {
				ids    []string
				reason string
			}
			sets := map[string]effective{}
			for _, kv := range prom.Pairs() {
				if cur, ok := cfg.Get(kv.Key); !ok || cur == nil {
					tok := ""
					for _, k := range promotableKinds {
						if "activated_"+k[1] == kv.Key {
							tok = k[0]
						}
					}
					ids, reason := charter.EffectiveSet(root, tok)
					sets[kv.Key] = effective{ids, reason}
				}
			}
			if dryRun {
				var parts, warns []string
				for _, kv := range prom.Pairs() {
					if e, ok := sets[kv.Key]; ok && e.reason != "" {
						warns = append(warns, "dry-run: "+leftAbsentMessage(kv.Key, e.reason))
						continue
					}
					parts = append(parts, fmt.Sprintf("%s: +%s", kv.Key, pyListAny(kv.Value.([]any))))
				}
				changes := []string{"dry-run: nothing would be promoted"}
				if len(parts) > 0 {
					changes = []string{"dry-run: would promote " + pyListStrs(parts)}
				}
				return migrationResult{Success: true, Changes: changes, Warnings: append(warns, unresolvedWarn()...)}
			}
			var ch, warns, leftAbsent []string
			written := false
			for _, kv := range prom.Pairs() {
				var list []any
				if e, absent := sets[kv.Key]; absent {
					if e.reason != "" {
						leftAbsent = append(leftAbsent, leftAbsentMessage(kv.Key, e.reason))
						continue
					}
					seeded := append([]string(nil), e.ids...)
					sort.Strings(seeded)
					for _, id := range seeded {
						list = append(list, id)
					}
					warns = append(warns, fmt.Sprintf("Key %s had no explicit activation set. Preserved %d effective entries before promotion (absent-key parity).", pyRepr(kv.Key), len(seeded)))
				} else {
					cur, _ := cfg.Get(kv.Key)
					l, _ := cur.([]any)
					list = append(list, l...)
				}
				var activated []any
				for _, x := range kv.Value.([]any) {
					found := false
					for _, y := range list {
						found = found || pyStrValue(y) == pyStrValue(x)
					}
					if found {
						warns = append(warns, fmt.Sprintf("%s is already activated for key %s.", pyRepr(pyStrValue(x)), pyRepr(kv.Key)))
						continue
					}
					list = append(list, x)
					activated = append(activated, x)
				}
				if list == nil {
					list = []any{}
				}
				cfg.Set(kv.Key, list)
				written = true
				if len(activated) > 0 {
					ch = append(ch, fmt.Sprintf("Promoted %s into %s", pyListAny(activated), kv.Key))
				}
			}
			if written {
				writeConfigRoundTrip(root, cfg)
			}
			if len(ch) == 0 {
				ch = []string{"No changes (all promotions already present)"}
			}
			warns = append(warns, leftAbsent...)
			return migrationResult{Success: true, Changes: ch, Warnings: append(warns, unresolvedWarn()...)}
		},
	}
}

func pyListAny(xs []any) string {
	var s []string
	for _, x := range xs {
		s = append(s, pyStrValue(x))
	}
	return pyListRepr(s)
}

func pyListStrs(xs []string) string { return pyListRepr(xs) }

// kittify_ConfigPath avoids importing kittify here twice.
func kittify_ConfigPath(root string) string { return filepath.Join(root, ".kittify", "config.yaml") }

// --- verdict provenance -------------------------------------------------------------

var (
	wpDirPrefixRe   = regexp.MustCompile(`^(WP\d+)`)
	reviewCycleFile = regexp.MustCompile(`^review-cycle-(\d+)\.md$`)
)

func wpIDsWithReviewCycles(feature string) []string {
	tasks := filepath.Join(feature, "tasks")
	set := map[string]bool{}
	for _, d := range subdirs(tasks) {
		m := wpDirPrefixRe.FindStringSubmatch(filepath.Base(d))
		if m == nil {
			continue
		}
		if len(globFiles(d, "review-cycle-*.md")) > 0 {
			set[m[1]] = true
		}
	}
	return sortedKeys(set)
}

func cycleNumber(p string) int {
	m := reviewCycleFile.FindStringSubmatch(filepath.Base(p))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func legacyFrontmatterVerdict(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	t := string(b)
	if !strings.HasPrefix(t, "---") {
		return "", false
	}
	rest := strings.TrimPrefix(t[3:], "\n")
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return "", false
	}
	o, err := yamlx.ParseObject([]byte(rest[:i]))
	if err != nil || o == nil {
		return "", false
	}
	v, ok := o.Get("verdict")
	s, isS := v.(string)
	return s, ok && isS
}

// terminalLegacyVerdict is terminal_review_artifact's legacy verdict for the
// highest-numbered review cycle of wpID.
func terminalLegacyVerdict(feature, wpID string) (string, bool) {
	tasks := filepath.Join(feature, "tasks")
	var dirs []string
	if d := filepath.Join(tasks, wpID); isDir(d) {
		dirs = append(dirs, d)
	}
	for _, d := range subdirs(tasks) {
		if strings.HasPrefix(filepath.Base(d), wpID+"-") {
			dirs = append(dirs, d)
		}
	}
	best, bestN := "", -1
	for _, d := range dirs {
		c := globFiles(d, "review-cycle-*.md")
		if len(c) == 0 {
			continue
		}
		sort.SliceStable(c, func(i, j int) bool { return cycleNumber(c[i]) < cycleNumber(c[j]) })
		if n := cycleNumber(c[len(c)-1]); n > bestN {
			bestN, best = n, c[len(c)-1]
		}
	}
	if best == "" {
		return "", false
	}
	return legacyFrontmatterVerdict(best)
}

func reviewResultSlotPresent(feature, wpID string) bool {
	rows, err := status.ReadRows(feature)
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.GetString("wp_id") != wpID {
			continue
		}
		if v, ok := r.Get("review_result"); ok && v != nil {
			return true
		}
	}
	return false
}

func strandedVerdicts(feature string) []string {
	var out []string
	for _, wp := range wpIDsWithReviewCycles(feature) {
		if _, has := terminalLegacyVerdict(feature, wp); !has {
			continue
		}
		if reviewResultSlotPresent(feature, wp) {
			continue
		}
		out = append(out, wp)
	}
	return out
}

// terminalReviewArtifact returns the terminal review-cycle file of wpID and
// its parsed frontmatter.
func terminalReviewArtifact(feature, wpID string) (string, *jsonx.Object) {
	tasks := filepath.Join(feature, "tasks")
	var dirs []string
	if d := filepath.Join(tasks, wpID); isDir(d) {
		dirs = append(dirs, d)
	}
	for _, d := range subdirs(tasks) {
		if strings.HasPrefix(filepath.Base(d), wpID+"-") {
			dirs = append(dirs, d)
		}
	}
	best, bestN := "", -1
	var bestFM *jsonx.Object
	for _, d := range dirs {
		c := globFiles(d, "review-cycle-*.md")
		if len(c) == 0 {
			continue
		}
		sort.SliceStable(c, func(i, j int) bool { return cycleNumber(c[i]) < cycleNumber(c[j]) })
		p := c[len(c)-1]
		b, err := os.ReadFile(p)
		if err != nil || !strings.HasPrefix(string(b), "---") {
			continue
		}
		rest := strings.TrimPrefix(string(b)[3:], "\n")
		i := strings.Index(rest, "\n---")
		if i < 0 {
			continue
		}
		fm, err := yamlx.ParseObject([]byte(rest[:i]))
		if err != nil || fm == nil {
			continue
		}
		n, _ := fm.GetInt("cycle_number")
		if n > bestN {
			best, bestN, bestFM = p, n, fm
		}
	}
	return best, bestFM
}

func backfillVerdicts(missions []string) (int, []string) {
	seeded := 0
	var warn []string
	for _, f := range missions {
		meta := loadYAMLMappingSafe(filepath.Join(f, "meta.json"))
		var missionID *string
		if id := meta.GetString("mission_id"); id != "" {
			missionID = &id
		}
		var events []any
		for _, wp := range strandedVerdicts(f) {
			p, fm := terminalReviewArtifact(f, wp)
			legacy, has := legacyFrontmatterVerdict(p)
			if fm == nil || !has {
				continue
			}
			bridged := "approved"
			to := status.Lane("approved")
			if legacy == "rejected" {
				bridged, to = "changes_requested", status.Lane("in_progress")
			} else if legacy != "approved" {
				continue
			}
			rr := &status.ReviewResult{Reviewer: fm.GetString("reviewer_agent"), Verdict: bridged,
				Reference: fmt.Sprintf("review-cycle://%s/%s/%s", filepath.Base(f), filepath.Base(filepath.Dir(p)), filepath.Base(p))}
			if bridged == "changes_requested" {
				rr.FeedbackPath = &p
			}
			n, _ := fm.GetInt("cycle_number")
			seedID := filepath.Base(f)
			if missionID != nil {
				seedID = *missionID
			}
			ev := &status.Event{EventID: migrateDeterministicID(fmt.Sprintf("%s|%s|review_result|%s|%d", seedID, wp, legacy, n)),
				MissionSlug: filepath.Base(f), WPID: wp, From: status.Lane("in_review"), To: to, At: fm.GetString("reviewed_at"),
				Actor: status.StrActor("migration:verdict_provenance_backfill"), ExecutionMode: "worktree", ReviewResult: rr, MissionID: missionID}
			events = append(events, ev)
		}
		if len(events) == 0 {
			continue
		}
		if err := status.AppendEvents(f, events...); err == nil {
			seeded += len(events)
			if pathExists(filepath.Join(f, status.SnapshotFile)) {
				if _, err := status.Materialize(f); err != nil {
					warn = append(warn, fmt.Sprintf("%s: %s; heal with `spec-kitty agent status materialize --mission %s`", filepath.Base(f), err, filepath.Base(f)))
				}
			}
		}
	}
	return seeded, warn
}

func mVerdictProvenance() migrationImpl {
	detect := func(root string) bool {
		for _, f := range featureDirs(root) {
			if len(strandedVerdicts(f)) > 0 {
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
			return false, "no stranded verdict provenance found under kitty-specs/"
		},
		apply: func(root string, dryRun bool) migrationResult {
			missions := featureDirs(root)
			if len(missions) == 0 {
				return migrationResult{Success: true}
			}
			n := 0
			for _, f := range missions {
				n += len(strandedVerdicts(f))
			}
			if n == 0 {
				return migrationResult{Success: true}
			}
			if dryRun {
				return migrationResult{Success: true, Changes: []string{fmt.Sprintf("dry-run: would backfill %d stranded verdict event(s) across %d mission(s) scanned", n, len(missions))}}
			}
			seeded, warn := backfillVerdicts(missions)
			if seeded == 0 {
				return migrationResult{Success: true, Warnings: warn}
			}
			return migrationResult{Success: true, Changes: []string{fmt.Sprintf("Backfilled %d stranded verdict event(s) across %d mission(s) scanned", seeded, len(missions))}, Warnings: warn}
		},
	}
}

func migrateDeterministicID(seed string) string { return migrate.DeterministicID(seed) }
