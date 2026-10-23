package closeout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/retrospective"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// This file ports `agent retrospect synthesize` (agent_retrospect.synthesize_cmd):
// legacy-schema records apply their accepted proposals through
// retrospective.ApplyProposals; generator-schema records run as an empty
// batch; --fabricate-empty writes an empty record when the mission's
// artifacts allow it.

// retroRecordPath is writer.resolve_existing_record_path.
func retroRecordPath(root, slug, missionID string) string {
	tracked := filepath.Join(projectSpecs(root, slug), retrospective.FileName)
	if _, err := os.Stat(tracked); err == nil {
		return tracked
	}
	if missionID != "" {
		legacy := filepath.Join(root, ".kittify", "missions", missionID, retrospective.FileName)
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return tracked
}

// artifactsSufficientForEmptyRecord is _mission_artifacts_sufficient_for_empty_record.
func artifactsSufficientForEmptyRecord(root, slug, dir string) bool {
	for _, f := range []string{"spec.md", "plan.md", "tasks.md"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "tasks", "WP*.md")); len(m) == 0 {
		return false
	}
	snap, err := status.ReduceDir(coord.Resolve(root, slug).StatusDir())
	if err != nil || snap == nil || snap.EventCount == 0 || snap.WorkPackages == nil || snap.WorkPackages.Len() == 0 {
		return false
	}
	for _, kv := range snap.WorkPackages.Pairs() {
		if l := snap.Lane(kv.Key); l != "approved" && l != "done" {
			return false
		}
	}
	return true
}

// fabricateEmptyRecord is _create_empty_retrospective_record.
func fabricateEmptyRecord(root, slug, missionID, actorID string) (string, error) {
	now := clock.NowUTCISO()
	actor := jsonx.NewObject("kind", "agent", "id", actorID)
	rec := jsonx.NewObject("schema_version", 1, "mission_id", missionID, "mission_slug", slug, "mission_number", nil,
		"friendly_name", "", "mission_type", "software-dev", "target_branch", "main", "created_at", now, "created_by", actor,
		"provenance", jsonx.NewObject("kind", "synthesize_fabricate", "invoked_at", now, "policy_resolved_from", jsonx.NewObject(),
			"command", "agent retrospect synthesize --fabricate-empty"),
		"policy_source", jsonx.NewObject(), "findings_status", "ran_no_findings", "helped", []any{}, "not_helpful", []any{},
		"gaps", []any{}, "proposals", []any{}, "evidence_refs", []any{}, "generator_version", "spec-kitty-cli", "provenance_history", []any{})
	dir := projectSpecs(root, slug)
	path := filepath.Join(dir, retrospective.FileName)
	if _, err := os.Stat(path); err == nil {
		return path, fmt.Errorf("Retrospective record already exists at %s", path)
	}
	if err := os.WriteFile(path, []byte(retrospective.Serialize(rec)), 0o644); err != nil {
		return path, err
	}
	wd, werr := coord.Resolve(root, slug).WriteDir()
	ev := jsonx.NewObject("type", "RetrospectiveCaptured", "schema_version", 1, "event_id", ulid.New(), "lamport", retrospective.NextLamport(wd),
		"at", clock.NowUTCISO(), "actor", jsonx.NewObject("kind", "agent", "id", actorID, "display", "agent retrospect synthesize"),
		"mission_id", missionID, "mission_slug", slug, "wp_id", nil, "force", false, "execution_mode", "main",
		"findings_status", "ran_no_findings", "record_path", "kitty-specs/"+slug+"/"+retrospective.FileName,
		"generator_version", "spec-kitty-cli", "policy_source", jsonx.NewObject(), "provenance_kind", "explicit_create",
		"proposal_count", 0, "evidence_ref_count", 0)
	if werr == nil {
		status.AppendRows(status.EventsPath(wd), ev)
	}
	return path, nil
}

func runRetroSynthesize(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		say(ctx.Stderr, "[red]Error:[/red] Could not locate project root. Ensure you are inside a spec-kitty project (has .kittify/ or kitty-specs/).")
		return cli.Exit(1)
	}
	handle := ctx.String("mission")
	r, err := mission.ResolveIdentity(root, handle)
	switch e := err.(type) {
	case nil:
	case *mission.NotFoundError:
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("schema_version", "1", "command", "agent.retrospect.synthesize", "status", "error",
				"outcome", "MISSION_NOT_FOUND", "error", "MISSION_NOT_FOUND", "handle", e.Handle,
				"next_action", "Check the mission handle or run `spec-kitty agent mission list`."))
		} else {
			say(ctx.Stderr, "[red]Error:[/red] "+e.HumanMessage())
		}
		return cli.Exit(1)
	default:
		sayLiteral(ctx.Stderr, "", err.Error())
		return cli.Exit(1)
	}
	actorID := "agent"
	if v := ctx.OptString("actor-id"); v != nil && *v != "" {
		actorID = *v
	}
	actor := retrospective.LegacyActor{Kind: "agent", ID: actorID}
	path := retroRecordPath(root, r.Slug, r.MissionID)
	outcome := "retrospective_synthesized"
	var legacy *retrospective.LegacyRecord
	generatorShaped, fabricated := false, false
	if _, err := os.Stat(path); err != nil {
		if !ctx.Bool("fabricate-empty") {
			if jsonMode {
				printJSON(ctx.Stdout, jsonx.NewObject("result", "blocked", "code", "RETROSPECTIVE_RECORD_MISSING", "mission_id", r.MissionID,
					"mission_slug", r.Slug, "blocked_reason", "No retrospective record found for this mission. Author one with: spec-kitty retrospect create --mission "+r.Slug, "exit_code", 1))
			} else {
				say(ctx.Stderr, "[red]Error RETROSPECTIVE_RECORD_MISSING:[/red] No retrospective record found for this mission.\nAuthor one with: [bold]spec-kitty retrospect create --mission "+r.Slug+"[/bold]")
			}
			return cli.Exit(1)
		}
		dir := projectSpecs(root, r.Slug)
		if !artifactsSufficientForEmptyRecord(root, r.Slug, dir) {
			if jsonMode {
				printJSON(ctx.Stdout, jsonx.NewObject("schema_version", "1", "command", "agent.retrospect.synthesize", "status", "error",
					"outcome", "insufficient_mission_artifacts", "mission_id", r.MissionID, "mission_slug", r.Slug, "error", "record_not_found",
					"path", path, "next_action", "Complete mission artifacts and approved/done WP status, then rerun synthesize."))
				return nil
			}
			sayLiteral(ctx.Stderr, "[red]Error:[/red] ", "Retrospective record not found and mission artifacts are insufficient: "+path)
			return cli.Exit(3)
		}
		p, err := fabricateEmptyRecord(root, r.Slug, r.MissionID, actorID)
		path = p
		if err != nil {
			if jsonMode {
				printJSON(ctx.Stdout, jsonx.NewObject("schema_version", "1", "command", "agent.retrospect.synthesize", "status", "error",
					"outcome", "insufficient_mission_artifacts", "mission_id", r.MissionID, "mission_slug", r.Slug, "error", "record_create_failed",
					"detail", err.Error(), "path", path, "next_action", "Create or repair retrospective source artifacts, then rerun synthesize."))
			} else {
				sayLiteral(ctx.Stderr, "[red]Error:[/red] ", "Could not create retrospective record: "+err.Error())
			}
			return cli.Exit(3)
		}
		outcome, fabricated = "retrospective_record_created", true
	} else if rec, ok := retrospective.ReadLegacyRecord(path); ok {
		legacy = rec
	} else {
		gen, reason := retrospective.LoadRecord(path)
		if gen == nil {
			if jsonMode {
				printJSON(ctx.Stderr, jsonx.NewObject("error", "record_malformed", "detail", reason))
			} else {
				sayLiteral(ctx.Stderr, "[red]Error:[/red] ", "Retrospective record malformed: "+reason)
			}
			return cli.Exit(3)
		}
		generatorShaped = true
		if len(gen.GetList("proposals")) > 0 {
			say(ctx.Stderr, "[yellow]Warning:[/yellow] This retrospective uses the generator record schema; proposal application is not available for generator-shape proposals yet, so synthesize will run as an empty dry-run batch.")
		}
	}
	var proposals []retrospective.LegacyProposal
	if legacy != nil {
		proposals = legacy.Proposals
	}
	approved := map[string]bool{}
	if ids := ctx.Strings("proposal-id"); len(ids) > 0 {
		var uniq []string
		seen := map[string]bool{}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				uniq = append(uniq, id)
			}
		}
		if fabricated || generatorShaped {
			written := "Nothing"
			if fabricated {
				written = "The empty retrospective record was written, but no proposal"
			}
			detail := fmt.Sprintf("--proposal-id cannot pick proposals in this retrospective record: it uses the generator record schema, and proposal application does not support generator-shaped proposals yet. Requested: %s. %s was applied.", strings.Join(uniq, ", "), written)
			if jsonMode {
				st := &jsonx.Object{}
				idl := []any{}
				for _, id := range uniq {
					st.Set(id, "unsupported_generator_record")
					idl = append(idl, id)
				}
				printJSON(ctx.Stderr, jsonx.NewObject("error", "unsupported_generator_record", "detail", detail, "proposal_ids", idl, "statuses", st))
			} else {
				fmt.Fprintln(ctx.Stderr, "Error: "+detail)
			}
			return cli.Exit(1)
		}
		statusByID := map[string]string{}
		for _, p := range proposals {
			statusByID[p.ID] = p.Status
		}
		st := &jsonx.Object{}
		var listing []string
		var offending []any
		for _, id := range uniq {
			s, ok := statusByID[id]
			if !ok {
				s = "unknown"
			}
			if s != "accepted" {
				st.Set(id, s)
				listing = append(listing, id+" ("+s+")")
				offending = append(offending, id)
			}
		}
		if len(listing) > 0 {
			detail := "--proposal-id only selects accepted proposals; not accepted in the retrospective record: " + strings.Join(listing, ", ") + ". Nothing was applied."
			if jsonMode {
				printJSON(ctx.Stderr, jsonx.NewObject("error", "proposal_not_accepted", "detail", detail, "proposal_ids", offending, "statuses", st))
			} else {
				fmt.Fprintln(ctx.Stderr, "Error: "+detail)
			}
			return cli.Exit(1)
		}
		for _, id := range uniq {
			approved[id] = true
		}
	} else {
		for _, p := range proposals {
			if p.Status == "accepted" {
				approved[p.ID] = true
			}
		}
	}
	dry := !ctx.Bool("apply")
	res := retrospective.ApplyProposals(r.MissionID, root, proposals, approved, actor, dry)
	env := jsonx.NewObject("schema_version", "1", "command", "agent.retrospect.synthesize", "generated_at", clock.NowUTCISO(),
		"dry_run", dry, "status", "ok", "outcome", outcome, "result", res.Object(),
		"mission_id", r.MissionID, "mission_slug", r.Slug, "retrospective_path", path)
	if jsonMode {
		printJSON(ctx.Stdout, env)
	} else {
		renderSynthesis(ctx, res, dry)
	}
	if out := ctx.String("json-out"); out != "" {
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, []byte(jsonx.Dumps(env, jsonx.Pretty)), 0o644)
		if !jsonMode {
			say(ctx.Stdout, "\n[dim]JSON written to "+out+"[/dim]")
		}
	}
	if !dry {
		if len(res.Conflicts) > 0 {
			return cli.Exit(4)
		}
		for _, x := range res.Rejected {
			if x.Reason == "stale_evidence" || x.Reason == "invalid_payload" {
				return cli.Exit(5)
			}
		}
	}
	return nil
}

// renderSynthesis is _render_rich.
func renderSynthesis(ctx *cli.Context, res *retrospective.SynthResult, dry bool) {
	w := ctx.Stdout
	label := "[yellow]DRY-RUN[/yellow]"
	if !dry {
		label = "[green]APPLY[/green]"
	}
	say(w, "\n[bold]agent retrospect synthesize[/bold]  ("+label+")\n")
	tbl := func(title, hs string, cols []string, dimFirst bool) *rich.RichTable {
		t := rich.NewTable()
		t.Title, t.HeaderStyle = title, hs
		for i, c := range cols {
			col := rich.Column{}
			if i == 0 && dimFirst {
				col.Style, col.NoWrap = "dim", true
			}
			t.AddColumn(c, col)
		}
		return t
	}
	if len(res.Planned) > 0 {
		t := tbl("Planned Applications", "bold cyan", []string{"proposal_id", "kind", "diff_preview"}, true)
		for _, p := range res.Planned {
			t.AddRow(p.ProposalID, p.Kind, p.DiffPreview)
		}
		rich.PrintTo(w, t)
	} else {
		say(w, "[dim]No planned applications.[/dim]")
	}
	if len(res.Applied) > 0 {
		t := tbl("Applied Changes", "bold green", []string{"proposal_id", "target_urn", "artifact_path", "re_applied"}, true)
		for _, a := range res.Applied {
			re := "False"
			if a.ReApplied {
				re = "True"
			}
			t.AddRow(a.ProposalID, a.TargetURN, a.ArtifactPath, re)
		}
		rich.PrintTo(w, t)
	}
	if len(res.Conflicts) > 0 {
		t := tbl("Conflicts", "bold red", []string{"proposal_ids", "reason"}, false)
		for _, c := range res.Conflicts {
			t.AddRow(strings.Join(c.ProposalIDs, ", "), c.Reason)
		}
		rich.PrintTo(w, t)
	}
	if len(res.Rejected) > 0 {
		t := tbl("Rejections", "bold magenta", []string{"proposal_id", "reason", "detail"}, true)
		for _, x := range res.Rejected {
			t.AddRow(x.ProposalID, x.Reason, x.Detail)
		}
		rich.PrintTo(w, t)
	}
	say(w, fmt.Sprintf("\n[bold]Summary:[/bold] planned=%d applied=%d conflicts=%d rejected=%d events_emitted=%d",
		len(res.Planned), len(res.Applied), len(res.Conflicts), len(res.Rejected), len(res.EventsEmitted)))
}
