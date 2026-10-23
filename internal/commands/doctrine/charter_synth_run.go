package doctrine

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// The adapter half of `charter synthesize` (evidence, dry-run, real run)
// and `charter resynthesize --topic`.

// synthPanel is render_error_panel: an expanded rounded rich Panel whose
// body is a plain Text (no markup) with a centred title.
func synthPanel(w io.Writer, body, title string, bodyStyle any) {
	rich.PrintTo(w, &rich.RichPanel{R: rich.NewStyledText(body, bodyStyle), Title: "[bold red]" + title + "[/]", BorderStyle: "red"})
}

func synthFailureEnvelope(w io.Writer, adapter string, warnings []any) {
	jsonOut(w, jsonx.NewObject("result", "failure", "adapter", jsonx.NewObject("id", adapter, "version", "unknown"),
		"written_artifacts", []any{}, "warnings", warnings), jsonx.SortedPretty)
}

func refList(refs []charter.NodeOrEdgeRef) []any {
	out := []any{}
	for _, r := range refs {
		out = append(out, r.Object())
	}
	return out
}

func conflictList(cs []charter.ReconConflict) []any {
	out := []any{}
	for _, c := range cs {
		out = append(out, c.Object())
	}
	return out
}

func formatConflictLine(kind, target, backing, remediation string) string {
	if backing == "" {
		backing = "(orphaned -- no backing artifact on disk)"
	}
	return fmt.Sprintf("  [%s] %s (backing: %s)\n      %s", kind, target, backing, remediation)
}

func synthesizeMain(ctx *cli.Context, root, adapterName string) error {
	w := ctx.Stdout
	jsonMode, dryRun, prune := ctx.Bool("json"), ctx.Bool("dry-run"), ctx.Bool("prune")
	warnings := []any{}
	fail := func(err error) error { return synthHandleError(ctx, jsonMode, adapterName, warnings, err) }
	installSynthLogger(ctx, jsonMode)
	ev, evWarn, err := charter.CollectEvidence(root, ctx.Bool("skip-code-evidence"), ctx.Bool("skip-corpus"))
	if err != nil {
		return fail(err)
	}
	for _, x := range evWarn {
		warnings = append(warnings, x)
	}
	if !jsonMode {
		for _, x := range warnings {
			cprint(w, "%s", "[yellow]⚠ "+fmt.Sprint(x)+"[/yellow]")
		}
	}
	if ctx.Bool("dry-run-evidence") {
		if jsonMode {
			var code, corpus any
			if ev.Code != nil {
				code = jsonx.NewObject("stack_id", ev.Code.StackID, "primary_language", ev.Code.PrimaryLanguage, "representative_files_count", len(ev.Code.Representative))
			}
			if ev.Corpus != nil {
				corpus = jsonx.NewObject("snapshot_id", ev.Corpus.SnapshotID, "entries_count", len(ev.Corpus.Entries))
			}
			jsonOut(w, jsonx.NewObject("result", "success", "adapter", jsonx.NewObject("id", adapterName, "version", "evidence-dry-run"),
				"written_artifacts", []any{}, "warnings", warnings, "mode", "evidence_dry_run",
				"evidence", jsonx.NewObject("code_signals", code, "url_list_count", len(ev.URLs), "corpus", corpus)), jsonx.SortedPretty)
			return cli.Exit(0)
		}
		cprint(w, "[bold]Evidence dry-run summary:[/bold]")
		if ev.Code != nil {
			cprintRaw(w, fmt.Sprintf("  Code signals: stack=%s, lang=%s", ev.Code.StackID, ev.Code.PrimaryLanguage))
			cprintRaw(w, fmt.Sprintf("  Representative files: %d found", len(ev.Code.Representative)))
		} else {
			cprintRaw(w, "  Code signals: none (skipped or not detected)")
		}
		cprintRaw(w, fmt.Sprintf("  URL list: %d URL(s) configured", len(ev.URLs)))
		if ev.Corpus != nil {
			cprintRaw(w, fmt.Sprintf("  Corpus: %s (%d entries)", ev.Corpus.SnapshotID, len(ev.Corpus.Entries)))
		} else {
			cprintRaw(w, "  Corpus: none")
		}
		for _, x := range warnings {
			cprint(w, "%s", "  [yellow]Warning: "+fmt.Sprint(x)+"[/yellow]")
		}
		return cli.Exit(0)
	}
	req, err := charter.BuildSynthesisRequest(root, adapterName)
	if err != nil {
		return fail(err)
	}
	req.Evidence = ev
	ad := charter.NewSynthAdapter(root, adapterName)
	if dryRun {
		staged, written, delta, err := charter.SynthesisDryRun(root, req, ad, app.Version)
		if err != nil {
			return fail(err)
		}
		if jsonMode {
			wa := []any{}
			for _, a := range written {
				wa = append(wa, a.Object())
			}
			jsonOut(w, jsonx.NewObject("result", "dry_run", "adapter", jsonx.NewObject("id", ad.ID, "version", ad.Version),
				"written_artifacts", wa, "warnings", warnings, "staged_artifacts", strs(staged), "artifact_count", len(staged),
				"validated", true, "planned_deletes", refList(delta.Removable), "conflicts", conflictList(delta.Conflicts)), jsonx.SortedPretty)
			return nil
		}
		cprint(w, "[yellow]Dry-run:[/yellow] synthesis staged and validated (not promoted)")
		for _, f := range staged {
			cprint(w, "%s", "  [dim]staged:[/dim] "+f)
		}
		stripped := charter.StrippedLegacyEdges(delta)
		printStrippedEdges(ctx, stripped, true)
		pruneOnly := []charter.NodeOrEdgeRef{}
		for _, ref := range delta.Removable {
			if ref.Reason == "" {
				pruneOnly = append(pruneOnly, ref)
			}
		}
		if len(pruneOnly) > 0 {
			cprint(w, "[yellow]Planned deletions (would run under --prune):[/yellow]")
			for _, r := range pruneOnly {
				cprint(w, "%s", "  - "+r.RefKind+": "+r.URN)
			}
		} else if len(stripped) == 0 {
			cprint(w, "[dim]No divergence: --prune would remove nothing.[/dim]")
		}
		for _, c := range delta.Conflicts {
			cprint(w, "%s", "[yellow]"+formatConflictLine(c.Kind, c.TargetID, c.BackingArtifact, c.Remediation)+"[/yellow]")
		}
		return nil
	}
	if err := charter.RequireBundleComplete(root); err != nil {
		return fail(err)
	}
	mode := charter.ModePreserve
	if prune {
		mode = charter.ModePrune
	}
	out, err := charter.Synthesize(root, req, ad, app.Version, mode)
	if err != nil {
		return fail(err)
	}
	for _, x := range out.ReferenceWarnings {
		warnings = append(warnings, x)
		if !jsonMode {
			cprint(w, "%s", "[yellow]⚠ "+x+"[/yellow]")
		}
	}
	if orphaned := charter.OrphanedRemovals(out.Delta); len(orphaned) > 0 && !prune {
		var lines []string
		for _, r := range orphaned {
			rem := fmt.Sprintf("Remediation (orphaned %s): '%s' has no backing artifact on disk. Restore the backing artifact, or run `spec-kitty charter synthesize --prune` to remove it.", r.RefKind, r.URN)
			lines = append(lines, formatConflictLine("orphaned_"+r.RefKind, r.URN, r.BackingArtifact, rem))
		}
		if jsonMode {
			synthFailureEnvelope(w, adapterName, append(warnings, strs(lines)...))
		} else {
			cprint(ctx.Stderr, "[red]Refused:[/red] this run preserved orphaned content instead of dropping it; the following references are dangling (backing artifact deleted):")
			for _, l := range lines {
				cprint(ctx.Stderr, "%s", l)
			}
			cprint(ctx.Stderr, "[yellow]Re-run with `--prune` to remove it, or restore the backing artifact.[/yellow]")
		}
		return cli.Exit(1)
	}
	activationWarnings, err := charter.AppendSynthesizedActivations(root, charter.CreatedSlugsByKind(out.Delta))
	if err != nil {
		return fail(err)
	}
	for _, warning := range activationWarnings {
		warnings = append(warnings, warning)
		if !jsonMode {
			cprint(w, "[yellow]⚠ %s[/yellow]", warning)
		}
	}
	written := charter.WrittenArtifactsFromManifest(root)
	if jsonMode {
		p := jsonx.NewObject("result", "success", "adapter", jsonx.NewObject("id", out.AdapterID, "version", out.AdapterVersion),
			"written_artifacts", written, "warnings", warnings, "target_kind", out.TargetKind, "target_slug", out.TargetSlug,
			"inputs_hash", out.InputsHash, "adapter_id", out.AdapterID, "adapter_version", out.AdapterVersion)
		if prune {
			p.Set("deletions", refList(out.Delta.Removable))
		} else {
			p.Set("retained", refList(out.Delta.Retained))
			p.Set("conflicts", conflictList(out.Delta.Conflicts))
			p.Set("stripped_legacy_edges", refList(charter.StrippedLegacyEdges(out.Delta)))
		}
		jsonOut(w, p, jsonx.SortedPretty)
		return nil
	}
	cprint(w, "[green]Charter synthesis complete[/green]")
	cprint(w, "%s", "Primary artifact: "+out.TargetKind+":"+out.TargetSlug)
	cprint(w, "%s", "Adapter: "+out.AdapterID+" v"+out.AdapterVersion)
	if prune {
		if len(out.Delta.Removable) > 0 {
			cprint(w, "[yellow]Pruned (removed):[/yellow]")
			for _, r := range out.Delta.Removable {
				cprint(w, "%s", "  - "+r.RefKind+": "+r.URN)
			}
		} else {
			cprint(w, "[dim]--prune: nothing to prune.[/dim]")
		}
	} else {
		if len(out.Delta.Retained) > 0 {
			cprint(w, "[dim]Retained on-disk content (untargeted by this run):[/dim]")
			for _, r := range out.Delta.Retained {
				cprint(w, "%s", "  - "+r.RefKind+": "+r.URN)
			}
		}
		printStrippedEdges(ctx, charter.StrippedLegacyEdges(out.Delta), false)
		for _, c := range out.Delta.Conflicts {
			cprint(w, "%s", "[yellow]"+formatConflictLine(c.Kind, c.TargetID, c.BackingArtifact, c.Remediation)+"[/yellow]")
		}
	}
	if len(written) > 0 {
		printSynthesisCommitReminder(w, root)
	}
	return nil
}

// installSynthLogger routes project_drg logging warnings to the rich log
// handler on stderr in human mode.
func installSynthLogger(ctx *cli.Context, jsonMode bool) {
	charter.SynthWarningLogger = nil
	if !jsonMode {
		charter.SynthWarningLogger = func(m string) { rich.LogWarning(ctx.Stderr, m) }
	}
}

// synthHandleError is charter_synthesize's typed exception envelopes.
func synthHandleError(ctx *cli.Context, jsonMode bool, adapter string, warnings []any, err error) error {
	w := ctx.Stdout
	var renameError *charter.LegacyRenameRefusedError
	var dle *charter.DRGLoadError
	var se *charter.SynthError
	var re *charter.RequestError
	var cse *charter.ConfigShapeError
	var ce *dct.ConfigError
	switch {
	case errors.As(err, &renameError):
		synthPanel(ctx.Stderr, err.Error(), "LegacyRenameRefusedError", "red")
		if jsonMode {
			synthFailureEnvelope(w, adapter, append(warnings, "SynthesisError: "+err.Error()))
		}
	case errors.As(err, &dle):
		detail := "Refused: the on-disk doctrine overlay could not be parsed (" + dle.Msg + "). No write was made. Repair or remove the corrupt overlay under .kittify/charter-packs/ and re-run `spec-kitty charter synthesize`."
		if jsonMode {
			synthFailureEnvelope(w, adapter, append(warnings, detail))
		} else {
			cprint(ctx.Stderr, "%s", "[red]Error:[/red] "+detail)
		}
	case errors.As(err, &se) && se.Type == "NeutralityGateViolation":
		synthPanel(ctx.Stderr, se.Msg, se.Type, "red")
		cprint(ctx.Stderr, "%s", "\n[yellow]Staging directory preserved at:[/yellow] "+se.StagingDir+"\nInspect the staged artifacts, adjust the synthesis prompt or scope, and retry.")
		if jsonMode {
			synthFailureEnvelope(w, adapter, append(warnings, "NeutralityGateViolation: "+se.Msg))
		}
	case errors.As(err, &se):
		synthPanel(ctx.Stderr, se.Msg, se.Type, "red")
		if se.Type == "GeneratedArtifactMissingError" {
			hint := "Charter prerequisite missing: no generated charter artifacts found. Run `spec-kitty charter generate` (or `charter interview` first) to produce the artifacts that synthesis needs."
			if jsonMode {
				synthFailureEnvelope(w, adapter, append(warnings, hint))
			} else {
				cprint(w, "%s", "[red]Error:[/red] "+hint)
			}
		} else if jsonMode {
			synthFailureEnvelope(w, adapter, append(warnings, "SynthesisError: "+se.Msg))
		}
	case errors.As(err, &re), errors.As(err, &cse):
		if jsonMode {
			synthFailureEnvelope(w, adapter, append(warnings, err.Error()))
		} else {
			cprint(w, "%s", "[red]Error:[/red] "+err.Error())
		}
	case errors.As(err, &ce):
		detail := ce.Code()
		if ce.Body != "" {
			detail += ": " + ce.Body
		}
		if jsonMode {
			jsonOut(w, jsonx.NewObject("result", "failure", "adapter", jsonx.NewObject("id", adapter, "version", "unknown"), "code", ce.Code(), "written_artifacts", []any{}, "warnings", append(warnings, detail)), jsonx.SortedPretty)
		} else {
			cprint(w, "[red]Error:[/red] %s", detail)
		}
	default:
		if jsonMode {
			synthFailureEnvelope(w, adapter, append(warnings, "Unexpected error: "+err.Error()))
		} else {
			cprint(w, "%s", "[red]Unexpected error:[/red] "+err.Error())
		}
	}
	return cli.Exit(1)
}

func runCharterResynthesize(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonMode := ctx.Bool("json")
	installSynthLogger(ctx, jsonMode)
	fail := func(msg string, unexpected bool) error {
		if jsonMode {
			emitCharterError(w, true, msg, "", nil)
		} else {
			label := "Error"
			if unexpected {
				label = "Unexpected error"
			}
			cprint(w, "%s", "[red]"+label+":[/red] "+uconsoleEscape(msg))
		}
		return cli.Exit(1)
	}
	if err := writeRootError(); err != nil {
		return fail(err.Error(), false)
	}
	root := findRepoRoot()
	if root == "" {
		return fail(repoRootMissing, false)
	}
	if err := charter.AssertCharterYAMLSound(root); err != nil {
		var ce *dct.ConfigError
		if errors.As(err, &ce) {
			emitCharterError(w, jsonMode, ce.Body, ce.Code(), nil)
			return cli.Exit(1)
		}
		return fail(err.Error(), false)
	}
	if err := convergeLegacyProjectIntent(ctx, root, ctx.Bool("list-topics")); err != nil {
		if jsonMode {
			return fail(err.Error(), false)
		}
		synthPanel(ctx.Stderr, err.Error(), "LegacyRenameRefusedError", "red")
		return cli.Exit(1)
	}
	if fileExists(charter.Path(root, charter.CharterYAML)) {
		if err := charter.AssertBundleCompatible(root); err != nil {
			return fail(err.Error(), false)
		}
	}
	ev, evWarn, err := charter.CollectEvidence(root, ctx.Bool("skip-code-evidence"), ctx.Bool("skip-corpus"))
	if err != nil {
		return fail(err.Error(), false)
	}
	warnings := strs(evWarn)
	if !jsonMode {
		for _, x := range evWarn {
			cprint(w, "%s", "[yellow]⚠ "+x+"[/yellow]")
		}
	}
	req, err := charter.BuildSynthesisRequest(root, ctx.String("adapter"))
	if err != nil {
		var re *charter.RequestError
		return fail(err.Error(), !errors.As(err, &re))
	}
	req.Evidence = ev
	if ctx.Bool("list-topics") {
		topics := charter.ResynthesisTopics(root, req)
		if jsonMode {
			jsonOut(w, jsonx.NewObject("result", "success", "topics", topics, "warnings", warnings), jsonx.Pretty)
			return nil
		}
		pa, du, is := topics.GetList("project_artifacts"), topics.GetList("drg_urns"), topics.GetList("interview_sections")
		if len(pa)+len(du)+len(is) == 0 {
			cprint(w, "[yellow]No topic selectors available yet.[/yellow]")
			return nil
		}
		if len(pa) > 0 {
			cprint(w, "[bold]Project artifact selectors[/bold]")
			for _, s := range pa {
				cprint(w, "%s", "  "+fmt.Sprint(s))
			}
		}
		if len(du) > 0 {
			cprint(w, "[bold]DRG URNs[/bold]")
			for _, s := range du {
				cprint(w, "%s", "  "+fmt.Sprint(s))
			}
		}
		if len(is) > 0 {
			cprint(w, "[bold]Interview sections[/bold]")
			for _, s := range is {
				sel := fmt.Sprint(s)
				cprint(w, "%s", "  "+sel+"  [dim](alias: "+strings.ReplaceAll(sel, "_", "-")+")[/dim]")
			}
		}
		return nil
	}
	if !ctx.IsSet("topic") {
		return fail("Pass --topic <selector> or use --list-topics.", false)
	}
	topic := ctx.String("topic")
	res, err := charter.Resynthesize(root, req, charter.NewSynthAdapter(root, ctx.String("adapter")), app.Version, topic)
	if err != nil {
		var se *charter.SynthError
		if errors.As(err, &se) && se.Type == "TopicSelectorUnresolvedError" {
			body := se.Msg
			if len(se.Candidates) > 0 {
				var cl []string
				for _, c := range se.Candidates {
					cl = append(cl, "  * "+c)
				}
				body += "\n\nNearest candidates:\n" + strings.Join(cl, "\n")
			}
			body += "\n\nRun 'spec-kitty charter resynthesize --list-topics' to see all valid selectors."
			if jsonMode {
				emitCharterError(w, true, body, "", nil)
			} else {
				synthPanel(ctx.Stderr, body, `Cannot resolve --topic "`+se.Raw+`"`, nil)
			}
			return cli.Exit(2)
		}
		if errors.As(err, &se) {
			if jsonMode {
				emitCharterError(w, true, se.Msg, "", nil)
			} else {
				synthPanel(ctx.Stderr, se.Msg, se.Type, "red")
			}
			return cli.Exit(1)
		}
		var re *charter.RequestError
		return fail(err.Error(), !charter.IsMissingManifest(err) && !errors.As(err, &re))
	}
	if res.Noop {
		if jsonMode {
			jsonOut(w, jsonx.NewObject("result", "noop", "topic", topic, "diagnostic", res.Diagnostic, "matched_form", res.MatchedForm,
				"targets_count", 0, "warnings", warnings), jsonx.Pretty)
			return nil
		}
		cprint(w, "%s", "[yellow]No-op:[/yellow] "+res.Diagnostic)
		return nil
	}
	for _, warning := range res.ReferenceWarnings {
		warnings = append(warnings, warning)
		if !jsonMode {
			cprint(w, "[yellow]⚠ %s[/yellow]", warning)
		}
	}
	activationWarnings, err := charter.AppendSynthesizedActivations(root, res.CreatedSlugs)
	if err != nil {
		return fail(err.Error(), true)
	}
	for _, warning := range activationWarnings {
		warnings = append(warnings, warning)
		if !jsonMode {
			cprint(w, "[yellow]⚠ %s[/yellow]", warning)
		}
	}
	var regen []string
	for _, t := range res.Targets {
		regen = append(regen, t.Kind+":"+t.Slug)
	}
	if jsonMode {
		jsonOut(w, jsonx.NewObject("result", "success", "topic", topic, "matched_form", res.MatchedForm, "matched_value", res.MatchedValue,
			"regenerated", strs(regen), "run_id", res.RunID, "manifest_artifacts", res.ManifestArtifacts, "warnings", warnings), jsonx.Pretty)
		return nil
	}
	cprint(w, "%s", "[green]Resynthesis complete[/green] (topic: "+dct.PyRepr(topic)+")")
	cprint(w, "%s", "Matched form: "+res.MatchedForm)
	cprint(w, "%s", "Run ID: "+res.RunID)
	cprint(w, "Regenerated artifacts:")
	for _, a := range regen {
		cprint(w, "%s", "  [green]✓[/green] "+a)
	}
	return nil
}

func convergeLegacyProjectIntent(ctx *cli.Context, root string, dry bool) error {
	report, err := charter.RenameLegacyProjectIntentDirective(root, dry)
	if err != nil {
		return err
	}
	if len(report.Actions) == 0 {
		return nil
	}
	sink := ctx.Stdout
	if ctx.Bool("json") {
		sink = ctx.Stderr
	}
	heading := "Renamed the legacy mission-type-scope-directive to project-intent-directive"
	if dry {
		heading = "Legacy project-intent directive found (dry run, nothing changed)"
	}
	cprint(sink, "[yellow]%s:[/yellow]", heading)
	for _, action := range report.Actions {
		cprint(sink, "  - %s", action)
	}
	return nil
}

func printStrippedEdges(ctx *cli.Context, refs []charter.NodeOrEdgeRef, dry bool) {
	var dead, replaced []charter.NodeOrEdgeRef
	for _, ref := range refs {
		if ref.Reason == charter.DeadMissionTypeEdgeReason {
			dead = append(dead, ref)
		} else {
			replaced = append(replaced, ref)
		}
	}
	if len(replaced) > 0 {
		if dry {
			cprint(ctx.Stdout, "[yellow]Replaced legacy edges (removed without --prune):[/yellow]")
		} else {
			cprint(ctx.Stdout, "[yellow]Replaced %d legacy 'applies' edge(s) with 'suggests':[/yellow]", len(replaced))
		}
		for _, ref := range replaced {
			if dry {
				cprint(ctx.Stdout, "  - %s: %s", ref.RefKind, ref.URN)
			} else {
				cprint(ctx.Stdout, "  - %s", ref.URN)
			}
		}
	}
	if len(dead) > 0 {
		suffix := ""
		if dry {
			suffix = " (removed without --prune)"
		}
		cprint(ctx.Stdout, "[yellow]Removed %d dead mission-type 'requires' edge(s)%s:[/yellow]", len(dead), suffix)
		for _, ref := range dead {
			cprint(ctx.Stdout, "  - %s", ref.URN)
		}
	}
}
