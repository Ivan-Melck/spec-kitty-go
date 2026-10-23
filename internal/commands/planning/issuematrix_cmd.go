package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/issuematrix"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

func init() {
	addCommands(func(root, agent, _, _ *cli.Command) {
		g := &cli.Command{Name: "issue-matrix", Short: "Issue-matrix commands (structured issue-matrix.json)."}
		g.Add(&cli.Command{
			Name:  "migrate",
			Short: "Bulk-migrate legacy ``issue-matrix.md`` missions to ``issue-matrix.json`` (FR-013).",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Scope to a single mission handle. Omit to migrate all missions."},
				{Name: "json", Kind: cli.Bool, Help: "Emit structured JSON output."},
			},
			Run: runIssueMatrixMigrate,
		})
		root.Add(g)
		agent.Add(&cli.Command{
			Name:  "issue-verdict",
			Short: "Set an issue-matrix row's verdict, routed via ``write_target(ISSUE_MATRIX)``.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle (slug, mission_id, or mid8)."},
				{Name: "issue", Kind: cli.String, Metavar: "<str>", Required: true, Help: `Issue reference, e.g. "#1726".`},
				{Name: "verdict", Kind: cli.String, Metavar: "<str>", Required: true, Help: "fixed | verified-already-fixed | deferred-with-followup | in-mission | not-applicable (non-gating: cited for context or a PR/commit reference, no work owed; unlike in-mission it is terminal -- it also passes the done/merge completeness gate unchanged)"},
				{Name: "actor", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Identity of the acting agent."},
				{Name: "wp", Kind: cli.String, Metavar: "<str>", Help: "Owning work-package id (e.g. WP01)."},
				{Name: "evidence-ref", Kind: cli.String, Metavar: "<str>", Help: "Evidence text or link for the verdict. Required when --verdict is deferred-with-followup: the value must contain a follow-up handle ('#NNN' or a 'Follow-up:' substring), or the row fails validation."},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedcheckout.Help},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format."},
			},
			Run: runIssueVerdict,
		})
	})
}

// issueMatrixWriteDir is write_dir(ISSUE_MATRIX).
func issueMatrixWriteDir(root, slug string) string {
	if d, err := coord.Resolve(root, slug).WriteDir(); err == nil {
		return d
	}
	return project.Specs(root, slug)
}

// issueMatrixReadDir is coord_read_dir_for(ISSUE_MATRIX) or the feature dir.
func issueMatrixReadDir(root, slug string) string {
	return coord.Resolve(root, slug).StatusDir()
}

// writeIssueMatrix is write_issue_matrix: write the JSON into dir and
// commit through the router.
func writeIssueMatrix(root, slug string, rows *issuematrix.Rows, dir string, owned ...*ownedcheckout.Fact) (*safecommit.RouterResult, error) {
	if dir == "" {
		dir = issueMatrixWriteDir(root, slug)
	}
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, issuematrix.JSONFile)
	if err := os.WriteFile(p, rows.Render(), 0o644); err != nil {
		return nil, err
	}
	opts := safecommit.RouterOptions{}
	if len(owned) > 0 {
		opts.Owned = owned[0]
	}
	return safecommit.CommitForMission(root, slug, []string{p}, "chore: update issue-matrix for "+slug, mission.KindIssueMatrix, opts), nil
}

// migrateIssueMatrix is migrate_issue_matrix_to_json (nil = nothing to do).
func migrateIssueMatrix(root, slug, readDir string) (*safecommit.RouterResult, error) {
	if fileExists(filepath.Join(readDir, issuematrix.JSONFile)) {
		return nil, nil
	}
	md := filepath.Join(readDir, issuematrix.MDFile)
	if !fileExists(md) {
		return nil, nil
	}
	v := issuematrix.Validate(md, nil)
	if !v.Passed && len(v.Rows) == 0 {
		return nil, fmt.Errorf("legacy issue-matrix.md at %s is malformed and yields no rows; refusing to migrate it to an empty (lossy) issue-matrix.json", md)
	}
	return writeIssueMatrix(root, slug, issuematrix.RowsFromLegacy(v.Rows), "")
}

func runIssueMatrixMigrate(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := project.Locate("")
	if root == "" {
		if jsonMode {
			sayPlain(ctx.Stdout, `{"error": "Could not locate project root"}`)
		} else {
			say(ctx.Stdout, "[red]Error:[/red] Could not locate project root")
		}
		return cli.Exit(1)
	}
	var slugs []string
	if ctx.IsSet("mission") {
		name, err := mission.ResolveHandle(root, ctx.String("mission"))
		if err != nil {
			return renderResolveError(ctx, err, jsonMode)
		}
		slugs = []string{name}
	} else {
		entries, _ := os.ReadDir(project.Specs(root))
		for _, e := range entries {
			if e.IsDir() && fileExists(project.Specs(root, e.Name(), "meta.json")) {
				slugs = append(slugs, e.Name())
			}
		}
		sort.Strings(slugs)
	}
	migrated, skipped := []string{}, []string{}
	for _, s := range slugs {
		r, err := migrateIssueMatrix(root, s, project.Specs(root, s))
		if err == nil && r != nil && (r.Status == safecommit.StatusCommitted || r.Status == safecommit.StatusUnchanged) {
			migrated = append(migrated, s)
		} else {
			skipped = append(skipped, s)
		}
	}
	if jsonMode {
		sayPlain(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("ok", true, "migrated_missions", strList(migrated), "skipped", strList(skipped)), compact))
		return nil
	}
	none := func(xs []string) string {
		if len(xs) == 0 {
			return "(none)"
		}
		return strings.Join(xs, ", ")
	}
	say(ctx.Stdout, fmt.Sprintf("[green]Migrated %d mission(s)[/green]: %s", len(migrated), none(migrated)))
	say(ctx.Stdout, fmt.Sprintf("[dim]Skipped %d mission(s)[/dim]: %s", len(skipped), none(skipped)))
	return nil
}

func runIssueVerdict(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	fail := func(msg, code string) error {
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("error", msg, "code", code))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return cli.Exit(1)
	}
	actor := ctx.String("actor")
	if strings.TrimSpace(actor) == "" {
		return fail("--actor must be a non-empty string", "empty_actor")
	}
	verdict := ctx.String("verdict")
	ok := false
	for _, v := range issuematrix.Verdicts {
		if v == verdict {
			ok = true
		}
	}
	if !ok {
		return fail(fmt.Sprintf("--verdict %s is not in the allowed set: %s", mission.PyRepr(verdict), strings.Join(issuematrix.Verdicts, ", ")), "invalid_verdict")
	}
	ref := strings.TrimSpace(ctx.String("issue"))
	if !strings.HasPrefix(ref, "#") {
		ref = "#" + ref
	}
	root := project.Locate("")
	if root == "" {
		root = cwd()
	}
	owned, err := resolveOwnedOrRefuse(ctx, root, ctx.String("mission"), jsonMode, ownedcheckout.SuccessFalseEnvelope, ownedcheckout.Options{})
	if err != nil {
		return err
	}
	slug := ""
	if owned != nil {
		slug = owned.MissionSlug
	} else {
		slug, err = mission.ResolveHandle(root, ctx.String("mission"))
		if err != nil {
			return renderResolveError(ctx, err, jsonMode)
		}
	}
	readDir := issueMatrixReadDir(root, slug)
	if owned != nil {
		readDir = owned.MissionDir
	}
	migrated := false
	if !fileExists(filepath.Join(readDir, issuematrix.JSONFile)) {
		r, err := migrateIssueMatrix(root, slug, readDir)
		if err != nil {
			return fail(err.Error(), "malformed_legacy_matrix")
		}
		migrated = r != nil && r.Status == safecommit.StatusCommitted
	}
	matrixDir := issueMatrixWriteDir(root, slug)
	if owned != nil {
		matrixDir = owned.MissionDir
	}
	rows := issuematrix.LoadRows(matrixDir)
	entry, exists := rows.By[ref]
	evidence := optFlag(ctx, "evidence-ref")
	wpFlag := optFlag(ctx, "wp")
	if !exists {
		entry = issuematrix.Entry{Verdict: verdict, WP: wpFlag}
		if evidence != nil {
			entry.EvidenceRef = *evidence
		}
	} else {
		entry.Verdict = verdict
		if evidence != nil {
			entry.EvidenceRef = *evidence
		}
		if wpFlag != nil {
			entry.WP = wpFlag
		}
	}
	rows.Set(ref, entry)
	r, err := writeIssueMatrix(root, slug, rows, matrixDir, owned)
	if err != nil {
		return fail(err.Error(), "write_failed")
	}
	okStatus := r.Status == safecommit.StatusCommitted || r.Status == safecommit.StatusUnchanged
	status := r.Status
	payload := jsonx.NewObject("ok", okStatus, "kind", "ISSUE_MATRIX", "destination_surface", r.PlacementRef, "row_or_entry_ref", ref, "migrated", migrated, "status", status)
	if status == "refused" {
		payload.Set("refusal", jsonx.NewObject("reason", optString(r.Diagnostic), "deferred_to", "#3033"))
	}
	if jsonMode {
		printCompact(ctx.Stdout, sortedObject(payload))
	} else {
		say(ctx.Stdout, fmt.Sprintf("[green]OK[/green] %s -> %s (%s, surface=%s)", ref, verdict, status, r.PlacementRef))
	}
	if !okStatus {
		return cli.Exit(1)
	}
	return nil
}

func sortedObject(o *jsonx.Object) *jsonx.Object {
	keys := o.Keys()
	sort.Strings(keys)
	out := jsonx.NewObject()
	for _, k := range keys {
		v, _ := o.Get(k)
		if vo, ok := v.(*jsonx.Object); ok {
			v = sortedObject(vo)
		}
		out.Set(k, v)
	}
	return out
}
