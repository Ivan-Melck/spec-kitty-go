package planning

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

func init() {
	addCommands(func(_, _, missionG, _ *cli.Command) {
		missionG.Add(&cli.Command{
			Name:  "repair",
			Short: "Detect and forward-only repair a pre-existing cross-partition content split-brain for one mission (FR-005, NFR-005).",
			Long: "Detect and forward-only repair a pre-existing cross-partition content\nsplit-brain for one mission (FR-005, NFR-005).\n\n" +
				"Fast-forwards under strict-ancestor + clean worktree, zero data loss.\nRefuses with a unified diff (scoped to the mission's own content) and\nmutates NOTHING on genuine (non-ancestor) divergence.\n\n" +
				"Examples:\n    spec-kitty agent mission repair --mission 020-my-mission",
			Flags: []*cli.Flag{{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission slug/handle to repair"}},
			Run:   runMissionRepair,
		})
	})
}

func isFFCandidate(root, a, b string) bool {
	return a != "" && b != "" && a != b && gitx.IsAncestor(root, a, b)
}

func worktreeForBranch(root, branch string) string {
	wts, err := gitx.Worktrees(root)
	if err != nil {
		return ""
	}
	for _, w := range wts {
		if w.Branch == branch {
			return w.Path
		}
	}
	return ""
}

func runMissionRepair(ctx *cli.Context) error {
	out := ctx.Stdout
	m := ctx.String("mission")
	root := project.Locate("")
	if root == "" {
		say(out, "[red]Error:[/red] Not in a spec-kitty project")
		return cli.Exit(1)
	}
	dir := project.Specs(root, m)
	meta, err := mission.ReadMeta(dir)
	if err != nil || meta == nil {
		if _, serr := os.Stat(dir); serr != nil {
			say(out, "[red]Error:[/red] Mission not found: "+mission.PyRepr(m))
		} else {
			say(out, fmt.Sprintf("[red]Error:[/red] Could not read meta.json for mission %s (missing or malformed).", mission.PyRepr(m)))
		}
		return cli.Exit(1)
	}
	coordBranch := strings.TrimSpace(meta.GetString("coordination_branch"))
	slug := meta.GetString("mission_slug")
	if slug == "" {
		slug = meta.GetString("slug")
	}
	target := meta.GetString("target_branch")
	var coordSHA, targetSHA string
	if coordBranch != "" && slug != "" && meta.GetString("mission_id") != "" && target != "" {
		coordSHA, _ = gitx.RevParse(root, "refs/heads/"+coordBranch)
		targetSHA, _ = gitx.RevParse(root, "refs/heads/"+target)
	}
	if coordSHA == "" || targetSHA == "" {
		say(out, fmt.Sprintf("[green]Nothing to repair:[/green] mission %s has no coordination partition (or incomplete branch identity) — there is no cross-partition content to reconcile.", mission.PyRepr(m)))
		return nil
	}
	if coordSHA == targetSHA {
		say(out, fmt.Sprintf("[green]Clean:[/green] mission %s's coordination partition (%s) and primary partition (%s) already match (%s). Nothing to repair.",
			mission.PyRepr(m), mission.PyRepr(coordBranch), mission.PyRepr(target), coordSHA[:8]))
		return nil
	}
	var behind, ahead, behindSHA, aheadSHA string
	switch {
	case isFFCandidate(root, coordSHA, targetSHA):
		behind, ahead, behindSHA, aheadSHA = coordBranch, target, coordSHA, targetSHA
	case isFFCandidate(root, targetSHA, coordSHA):
		behind, ahead, behindSHA, aheadSHA = target, coordBranch, targetSHA, coordSHA
	default:
		say(out, fmt.Sprintf("[red]Refusing to repair:[/red] mission %s's coordination partition (%s, %s) and primary partition (%s, %s) have diverged (neither is an ancestor of the other) — repair mutates nothing. Reconcile manually using the diff below.",
			mission.PyRepr(m), mission.PyRepr(coordBranch), coordSHA[:8], mission.PyRepr(target), targetSHA[:8]))
		res, _ := gitx.Exec(root, nil, nil, "diff", coordSHA, targetSHA, "--", "kitty-specs/"+m+"/")
		if res.Stdout != "" {
			// console.print(diff_text) keeps git's trailing newline.
			sayPlain(out, res.Stdout)
		} else {
			sayPlain(out, "(no content diff detected under this mission's own directory — the divergence lies elsewhere in the two branches' history)")
		}
		return cli.Exit(1)
	}
	refuse := func(reason string) error {
		say(out, fmt.Sprintf("[red]Refusing to repair:[/red] mission %s is a fast-forward candidate (%s -> %s) but the forward is not safe right now: %s. Repair mutates nothing.",
			mission.PyRepr(m), mission.PyRepr(behind), mission.PyRepr(ahead), reason))
		return cli.Exit(1)
	}
	if !isFFCandidate(root, behindSHA, aheadSHA) {
		return refuse(fmt.Sprintf("%s is no longer a strict ancestor of %s (it moved between classification and repair)", mission.PyRepr(behind), mission.PyRepr(ahead)))
	}
	wt := worktreeForBranch(root, behind)
	if wt == "" {
		return refuse(fmt.Sprintf("no worktree is checked out to %s; cannot safely fast-forward it without one", mission.PyRepr(behind)))
	}
	if e, err := safecommit.StatusEntries(wt, ""); err != nil || len(e) > 0 {
		return refuse(fmt.Sprintf("the worktree for %s (%s) has uncommitted changes", mission.PyRepr(behind), wt))
	}
	if _, err := gitx.Run(wt, "merge", "--ff-only", "--end-of-options", ahead); err != nil {
		say(out, "[red]Error:[/red] "+err.Error())
		return cli.Exit(1)
	}
	say(out, fmt.Sprintf("[green]Repaired:[/green] fast-forwarded %s onto %s for mission %s (zero data loss).", mission.PyRepr(behind), mission.PyRepr(ahead), mission.PyRepr(m)))
	return nil
}
