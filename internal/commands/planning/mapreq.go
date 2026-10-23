package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/reqmap"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

const mapReqCmd = "spec-kitty agent tasks map-requirements"

var grammarHint = "Requirement IDs must match " + reqmap.RuleText + " with kind FR, NFR, C or SC (e.g. FR-003, FR-003a, SC-001); cite another mission's ID as <mission-slug>#<ID>."

var staleHint = grammarHint + " 'malformed' refs violate that format; 'unknown_spec_id' refs are well-formed but not declared in spec.md (see parsed_spec_ids). Re-run with --replace to correct, e.g.: map-requirements --wp WP01 --refs FR-001 --replace"

func init() {
	addCommands(func(_, _, _, tasksG *cli.Command) {
		tasksG.Add(&cli.Command{
			Name:  "map-requirements",
			Short: "Register requirement-to-WP mappings with immediate validation.",
			Flags: []*cli.Flag{
				{Name: "wp", Kind: cli.String, Metavar: "<str>", Help: "WP ID (e.g., WP04)"},
				{Name: "refs", Kind: cli.String, Metavar: "<str>", Help: "Comma-separated requirement refs (e.g., FR-001,FR-002)"},
				{Name: "batch", Kind: cli.String, Metavar: "<str>", Help: `JSON batch mapping (e.g., '{"WP01":["FR-001"],"WP02":["FR-003"]}')`},
				{Name: "replace", Kind: cli.Bool, Help: "Replace existing refs instead of merging (default: merge/union)"},
				{Name: "tracker-ref", Kind: cli.StringList, Metavar: "<str>", Help: "External tracker reference (e.g., '#1298' or 'JIRA-123'). Repeatable; requires --wp. Persists to the WP frontmatter as tracker_refs."},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
				{Name: "auto-commit", Kind: cli.Bool, Negatable: true, Help: "Automatically commit WP file changes (default: from project config)"},
				ownedFlag(),
			},
			Run: runMapRequirements,
		})
	})
}

func dedupKey(item string) string {
	if c := reqmap.Canonical(item, "software-dev"); c != "" {
		return c
	}
	return item
}

func canonicalOr(ref, mt string) string {
	if c := reqmap.Canonical(ref, mt); c != "" {
		return c
	}
	return ref
}

func wpFileFor(tasksDir, id string) string {
	m, _ := filepath.Glob(filepath.Join(tasksDir, id+"*.md"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

var wpIDPrefix = regexp.MustCompile(`^(WP\d{2})`)

func sortedList(xs []string) []any {
	c := append([]string(nil), xs...)
	sort.Strings(c)
	return strList(c)
}

func dedupSorted(xs []string) []string {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	out := make([]string, 0, len(m))
	for x := range m {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

func runMapRequirements(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	fail := func(msg string) error { tasksOutputError(ctx, jsonMode, msg); return cli.Exit(1) }
	// Owned resolution runs at the Typer edge (tasks._resolve_task_owned),
	// before the mode validation.
	var owned *ownedcheckout.Fact
	if r0 := project.Locate(""); r0 != "" {
		f, err := resolveOwnedOrRefuse(ctx, r0, ctx.String("mission"), jsonMode, ownedcheckout.FlatErrorEnvelope, ownedcheckout.Options{})
		if err != nil {
			if _, ok := err.(*ownedcheckout.MetaReadError); ok {
				return fail(err.Error())
			}
			return err
		}
		owned = f
	}
	wpOpt, refsOpt, batchOpt := ctx.String("wp"), ctx.String("refs"), ctx.String("batch")
	var trackers []string
	for _, t := range ctx.Strings("tracker-ref") {
		if strings.TrimSpace(t) != "" {
			trackers = append(trackers, strings.TrimSpace(t))
		}
	}
	if batchOpt != "" && (wpOpt != "" || refsOpt != "") {
		return fail("Cannot combine --batch with --wp/--refs. Use one mode.")
	}
	if len(trackers) > 0 && (batchOpt != "" || !ctx.IsSet("wp")) {
		return fail("--tracker-ref requires --wp (cannot be combined with --batch).")
	}
	trackerOnly := len(trackers) > 0 && ctx.IsSet("wp") && refsOpt == ""
	if batchOpt == "" && !(wpOpt != "" && refsOpt != "") && !trackerOnly {
		return fail("Provide either --wp + --refs (individual), --batch, or --wp + --tracker-ref.")
	}
	root := project.Locate("")
	if root == "" {
		return fail("Could not locate project root")
	}
	var slug string
	if owned != nil {
		root, slug = owned.OwnedRoot, owned.MissionSlug
	} else {
		var err error
		slug, err = resolveTasksMission(ctx, root, ctx.String("mission"), jsonMode)
		if err != nil {
			return err
		}
		if _, err := targetBanner(ctx, root, slug, jsonMode); err != nil {
			return fail(err.Error())
		}
	}
	autoCommit := mission.AutoCommit(root)
	if ctx.IsSet("auto-commit") {
		autoCommit = ctx.Bool("auto-commit")
	}
	if autoCommit && owned != nil {
		if ref := safecommit.OwnedPlacement(owned, mission.KindWorkPackageTask); owned.IsProtected(ref, "") {
			return fail("Protected owned write branch: " + ref)
		}
	} else if autoCommit {
		ref := safecommit.Placement(root, slug, mission.KindWorkPackageTask)
		if msg := protectedStatusCommitError(ref, root, mapReqCmd, slug); msg != "" {
			return fail(msg)
		}
	}
	primary := project.Specs(root, slug)
	if st, err := os.Stat(primary); err != nil || !st.IsDir() {
		return fail("Mission directory not found: " + primary)
	}
	specPath := filepath.Join(primary, "spec.md")
	specB, err := os.ReadFile(specPath)
	if err != nil {
		return fail("spec.md not found: " + specPath)
	}
	spec := string(specB)
	mt := missionart.MissionTypeFor(primary)
	ids := reqmap.ParseSpecIDs(spec, mt)
	declared := map[string]bool{}
	for _, id := range ids.All {
		declared[id] = true
	}
	functional := map[string]bool{}
	for _, id := range ids.Functional() {
		functional[id] = true
	}
	extractionWarnings := reqmap.FindUndeclaredRequirementCitations(spec)

	newMappings := map[string][]string{}
	var newOrder []string
	add := func(id string, refs []string) {
		if _, ok := newMappings[id]; !ok {
			newOrder = append(newOrder, id)
		}
		newMappings[id] = refs
	}
	switch {
	case batchOpt != "":
		v, err := pyjson.Loads(batchOpt)
		if err != nil {
			return fail("Invalid JSON in --batch: " + err.Error())
		}
		obj, ok := v.(*jsonx.Object)
		if !ok {
			return fail("--batch must be a JSON object {WP_ID: [refs]}")
		}
		for _, kv := range obj.Pairs() {
			list, ok := kv.Value.([]any)
			allStr := ok
			for _, x := range list {
				if _, s := x.(string); !s {
					allStr = false
				}
			}
			if !allStr {
				return fail(fmt.Sprintf("Refs for %s must be a list of strings", kv.Key))
			}
			var refs []string
			for _, t := range reqmap.TokenizeRefs(list) {
				refs = append(refs, canonicalOr(t, "software-dev"))
			}
			add(strings.ToUpper(kv.Key), refs)
		}
	case trackerOnly:
		add(strings.ToUpper(wpOpt), []string{})
	default:
		var refs []string
		for _, t := range reqmap.TokenizeRefs(refsOpt) {
			refs = append(refs, canonicalOr(t, "software-dev"))
		}
		add(strings.ToUpper(wpOpt), refs)
	}
	for id, refs := range newMappings {
		out := make([]string, 0, len(refs))
		for _, r := range refs {
			out = append(out, canonicalOr(r, mt))
		}
		newMappings[id] = out
	}
	tasksDir := filepath.Join(primary, "tasks")
	existing := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(tasksDir, "WP*.md"))
	for _, f := range files {
		if m := wpIDPrefix.FindStringSubmatch(filepath.Base(f)); m != nil {
			existing[m[1]] = true
		}
	}
	var unknown []string
	for id := range newMappings {
		if !existing[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		var ex []string
		for k := range existing {
			ex = append(ex, k)
		}
		sort.Strings(ex)
		hint := "No WP files found in tasks/"
		if len(ex) > 0 {
			hint = "Available WPs: " + strings.Join(ex, ", ")
		}
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("error", "Unknown WP IDs", "unknown_wps", strList(unknown), "hint", hint))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] Unknown WP IDs: "+strings.Join(unknown, ", "))
			say(ctx.Stdout, "  "+hint)
		}
		return cli.Exit(1)
	}

	// --- plan ---
	existingRefs, _ := reqmap.ReadAllWPRawRefs(tasksDir)
	tasksMDRefs := map[string][]string{}
	if b, err := os.ReadFile(filepath.Join(primary, "tasks.md")); err == nil {
		tasksMDRefs = reqmap.TasksMDRefs(string(b), mt)
	}
	var bare []string
	for _, c := range reqmap.FindBareProseRequirementIDs(spec) {
		bare = append(bare, c.IDs...)
	}
	bare = dedupSorted(bare)
	var malformed, unknownRefs []string
	for _, id := range newOrder {
		for _, r := range newMappings[id] {
			if _, reason := reqmap.Classify(r, declared, mt); reason == reqmap.Malformed {
				malformed = append(malformed, r)
			} else if reason == reqmap.UnknownSpecID {
				unknownRefs = append(unknownRefs, r)
			}
		}
	}
	toWrite := map[string][]string{}
	removed := map[string][]string{}
	if !trackerOnly {
		for _, id := range newOrder {
			ex := existingRefs[id]
			var written []string
			if ctx.Bool("replace") {
				byKey := map[string]string{}
				for _, it := range ex {
					if _, ok := byKey[dedupKey(it)]; !ok {
						byKey[dedupKey(it)] = it
					}
				}
				w := map[string]string{}
				for _, r := range newMappings[id] {
					k := dedupKey(r)
					if _, ok := w[k]; !ok {
						if s, ok := byKey[k]; ok {
							w[k] = s
						} else {
							w[k] = r
						}
					}
				}
				for _, v := range w {
					written = append(written, v)
				}
				sort.Strings(written)
				wk := map[string]bool{}
				for _, v := range written {
					wk[dedupKey(v)] = true
				}
				rem := []string{}
				for _, it := range ex {
					if !wk[dedupKey(it)] {
						rem = append(rem, it)
					}
				}
				removed[id] = rem
			} else {
				base := ex
				if len(base) == 0 {
					base = tasksMDRefs[id]
				}
				written = append([]string{}, base...)
				seen := map[string]bool{}
				for _, it := range base {
					seen[dedupKey(it)] = true
				}
				for _, r := range newMappings[id] {
					if k := dedupKey(r); !seen[k] {
						seen[k] = true
						written = append(written, r)
					}
				}
			}
			if written == nil {
				written = []string{}
			}
			toWrite[id] = written
		}
	}
	projected := map[string][]string{}
	for k, v := range existingRefs {
		projected[k] = v
	}
	for k, v := range toWrite {
		projected[k] = v
	}
	cov := reqmap.ComputeCoverage(projected, functional)
	sortedIDs := ids.All
	if len(malformed) > 0 {
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("error", "Invalid requirement ref format", "malformed_refs", strList(malformed), "parsed_spec_ids", strList(sortedIDs), "hint", grammarHint))
		} else {
			sayPlain(ctx.Stdout, "Error: Invalid ref format: "+strings.Join(malformed, ", "))
			sayPlain(ctx.Stdout, "  "+grammarHint)
			sayPlain(ctx.Stdout, "  Parsed spec IDs: "+orNone(strings.Join(sortedIDs, ", ")))
		}
		return cli.Exit(1)
	}
	if len(unknownRefs) > 0 {
		u := dedupSorted(unknownRefs)
		avail := "No requirement IDs found in spec.md"
		if len(sortedIDs) > 0 {
			avail = "Available: " + strings.Join(sortedIDs, ", ")
		}
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("error", "Invalid requirement refs", "unknown_refs", strList(u), "parsed_spec_ids", strList(sortedIDs), "hint", "Refs not found in spec.md. "+avail))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] Unknown refs: "+strings.Join(u, ", "))
			say(ctx.Stdout, "  "+avail)
		}
		return cli.Exit(1)
	}

	// --- write ---
	for _, id := range newOrder {
		f := wpFileFor(tasksDir, id)
		if f == "" {
			continue
		}
		if !trackerOnly {
			w, err := wp.Read(f)
			if err != nil {
				return fail(err.Error())
			}
			w.Doc.Meta.Set("requirement_refs", strList(toWrite[id]))
			if err := writeWPPreservingFlow(w, "requirement_refs"); err != nil {
				return fail(err.Error())
			}
		}
		if len(trackers) > 0 && id == strings.ToUpper(wpOpt) {
			var delta status.Delta
			if ctx.Bool("replace") {
				v := dedupSorted(trackers)
				delta.TrackerRefsReplace = &v
			} else {
				v := append([]string(nil), trackers...)
				delta.TrackerRefs = &v
			}
			// emit_inner_state_changed on the WORK_PACKAGE_TASK read dir
			// (the primary mission dir), then materialize status.json there.
			a, err := status.NewAnnotation(id, delta, status.Actor{Name: mapReqCmd}, "")
			if err != nil {
				return fail(err.Error())
			}
			if err := status.AppendEvents(primary, a); err != nil {
				return fail(err.Error())
			}
			status.Materialize(primary)
		}
	}

	// --- stale gate ---
	all, allOrder := reqmap.ReadAllWPRawRefs(tasksDir)
	staleRefs := jsonx.NewObject()
	staleReasons := jsonx.NewObject()
	blocking := false
	sortedWP := append([]string(nil), allOrder...)
	sort.Strings(sortedWP)
	for _, id := range allOrder {
		buckets := map[string][]string{reqmap.Malformed: {}, reqmap.UnknownSpecID: {}, reqmap.ForeignQualified: {}}
		for _, tok := range all[id] {
			if _, reason := reqmap.Classify(tok, declared, mt); reason != "" {
				buckets[reason] = append(buckets[reason], tok)
			}
		}
		for k := range buckets {
			sort.Strings(buckets[k])
		}
		n := len(buckets[reqmap.Malformed]) + len(buckets[reqmap.UnknownSpecID]) + len(buckets[reqmap.ForeignQualified])
		if n == 0 {
			continue
		}
		b := append(append([]string{}, buckets[reqmap.Malformed]...), buckets[reqmap.UnknownSpecID]...)
		sort.Strings(b)
		if len(b) > 0 {
			staleRefs.Set(id, strList(b))
			blocking = true
		}
		staleReasons.Set(id, jsonx.NewObject(reqmap.Malformed, strList(buckets[reqmap.Malformed]), reqmap.UnknownSpecID, strList(buckets[reqmap.UnknownSpecID]),
			reqmap.ForeignQualified, strList(buckets[reqmap.ForeignQualified])))
	}
	if blocking {
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("error", "Stale or invalid refs in WP frontmatter", "stale_refs", staleRefs, "stale_ref_reasons", staleReasons,
				"parsed_spec_ids", strList(sortedIDs), "hint", staleHint))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] Stale or invalid refs in WP frontmatter:")
			sayPlain(ctx.Stdout, "  "+grammarHint)
			keys := staleRefs.Keys()
			sort.Strings(keys)
			for _, k := range keys {
				var parts []string
				for _, x := range staleRefs.GetList(k) {
					parts = append(parts, x.(string))
				}
				say(ctx.Stdout, fmt.Sprintf("  %s: %s", k, strings.Join(parts, ", ")))
			}
			say(ctx.Stdout, "  Parsed spec IDs: "+orNone(strings.Join(sortedIDs, ", ")))
			say(ctx.Stdout, "  Use --replace to correct mappings")
		}
		return cli.Exit(1)
	}

	// --- auto-commit ---
	committed := false
	var commitSHA, commitResult any
	surfaces := []any{}
	if autoCommit {
		var written []string
		for _, id := range newOrder {
			if f := wpFileFor(tasksDir, id); f != "" {
				written = append(written, f)
			}
		}
		if owned != nil && len(trackers) > 0 {
			for _, n := range []string{status.EventsFile, status.SnapshotFile} {
				if p := filepath.Join(primary, n); fileExists(p) {
					written = append(written, p)
				}
			}
		}
		if len(written) > 0 {
			ids := append([]string(nil), newOrder...)
			sort.Strings(ids)
			specNum := slug
			if i := strings.Index(slug, "-"); i >= 0 {
				specNum = slug[:i]
			}
			msg := fmt.Sprintf("chore: Map requirements for %s on spec %s", strings.Join(ids, ", "), specNum)
			r := safecommit.CommitForMission(root, slug, written, msg, mission.KindWorkPackageTask, safecommit.RouterOptions{Owned: owned})
			surfaces = r.SurfacesPayload()
			if r.Status == safecommit.StatusCommitted {
				committed = true
				commitSHA = r.CommitHash
				commitResult = jsonx.NewObject("sha", r.CommitHash, "destination_ref", r.PlacementRef, "worktree_root", root)
			}
			if !jsonMode && surfaceNeedsWarning(r) {
				for _, l := range r.Render() {
					sayPlain(ctx.Stdout, "Warning: "+l)
				}
			}
		}
	}

	// --- output ---
	allAfter, _ := reqmap.ReadAllWPRawRefs(tasksDir)
	total := jsonx.NewObject()
	for _, id := range allOrder {
		acc := map[string]bool{}
		for _, tok := range allAfter[id] {
			if a, reason := reqmap.Classify(tok, declared, mt); reason == "" {
				acc[a.Canonical()] = true
			}
		}
		if len(acc) > 0 {
			var l []string
			for k := range acc {
				l = append(l, k)
			}
			total.Set(id, sortedList(l))
		}
	}
	mapped := jsonx.NewObject()
	for _, id := range newOrder {
		mapped.Set(id, sortedList(newMappings[id]))
	}
	payload := jsonx.NewObject("result", "success")
	for _, kv := range identityPayload(primary).Pairs() {
		payload.Set(kv.Key, kv.Value)
	}
	unmapped := cov.Unmapped
	if unmapped == nil {
		unmapped = []string{}
	}
	coverage := jsonx.NewObject("total_functional", cov.Total, "mapped_functional", cov.Total-len(unmapped), "unmapped_functional", strList(unmapped))
	payload.Set("mapped", mapped)
	payload.Set("total_mappings", total)
	payload.Set("coverage", coverage)
	payload.Set("committed", committed)
	payload.Set("commit_sha", commitSHA)
	payload.Set("commit_result", commitResult)
	payload.Set("surfaces", surfaces)
	payload.Set("requirement_extraction_warnings", strList(nonNil(extractionWarnings)))
	payload.Set("bare_prose_requirement_ids", strList(nonNil(bare)))
	if owned != nil {
		payload.Set("stale_repository_root_copy", ownedcheckout.StaleCopyValue(owned, nil))
		if !jsonMode {
			ownedcheckout.EchoStaleCopyWarning(ctx.Stderr, owned)
		}
	}
	if ctx.Bool("replace") {
		rm := jsonx.NewObject()
		for _, id := range newOrder {
			if v, ok := removed[id]; ok {
				rm.Set(id, strList(v))
			}
		}
		payload.Set("replaced_refs_removed", rm)
	}
	if jsonMode {
		printCompact(ctx.Stdout, payload)
		return nil
	}
	say(ctx.Stdout, "[green]✓[/green] Requirement mappings saved")
	ids2 := append([]string(nil), newOrder...)
	sort.Strings(ids2)
	for _, id := range ids2 {
		sayPlain(ctx.Stdout, fmt.Sprintf("  %s: %s", id, strings.Join(newMappings[id], ", ")))
	}
	say(ctx.Stdout, fmt.Sprintf("\n  Coverage: %d/%d FRs mapped", cov.Total-len(unmapped), cov.Total))
	if len(unmapped) > 0 {
		say(ctx.Stdout, "  [yellow]Unmapped:[/yellow] "+strings.Join(unmapped, ", "))
	}
	if committed {
		say(ctx.Stdout, "[cyan]→ Committed mapping changes[/cyan]")
	}
	if ctx.Bool("replace") {
		for _, id := range ids2 {
			if len(removed[id]) > 0 {
				say(ctx.Stdout, fmt.Sprintf("  Removed by --replace: %s: %s", id, strings.Join(removed[id], ", ")))
			}
		}
	}
	for _, w := range extractionWarnings {
		say(ctx.Stdout, "[yellow]Warning:[/yellow] "+w)
	}
	if len(bare) > 0 {
		say(ctx.Stdout, "  [red]Bare-prose requirement id(s) found, uncounted:[/red] "+strings.Join(bare, ", "))
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func surfaceNeedsWarning(r *safecommit.RouterResult) bool {
	for _, o := range r.Surfaces {
		if o.Status != safecommit.StatusCommitted && o.Status != safecommit.StatusUnchanged {
			return true
		}
		for _, f := range o.Skipped {
			if f.Reason == safecommit.ReasonCoordRecordInRoot {
				return true
			}
		}
	}
	return false
}
