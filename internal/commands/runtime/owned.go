package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/decision"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/nextrt"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// refuseOwnedAction is workflow._refuse_owned_action: `agent action
// implement|review --owned-checkout` validates the claim (and the mission
// when --mission is given) before the unconditional OWNED_ACTION_UNSUPPORTED
// refusal, rendered on stderr (these commands have no --json).
func refuseOwnedAction(ctx *cli.Context, action string) error {
	root := locateRoot()
	if root == "" {
		fmt.Fprintln(ctx.Stdout, "Error: Could not locate project root")
		return cli.Exit(1)
	}
	err := ownedcheckout.RefuseAction(project.MainRepoRoot(root), ctx.String("owned-checkout"), ctx.String("mission"), action)
	if e, ok := err.(*ownedcheckout.Error); ok {
		ownedcheckout.Emit(ctx.Stdout, ctx.Stderr, e, false, nil)
		return cli.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(ctx.Stdout, "Error: "+err.Error())
	}
	return cli.Exit(1)
}

// resolveNextOwned is next_cmd._resolve_next_owned: the single ownership
// validation for `next` (NEXT_OWNED_TOPOLOGIES, handle-less discovery of
// the sole mission inside the claimed checkout).
func resolveNextOwned(ctx *cli.Context, root string, jsonMode bool) (*ownedcheckout.Fact, error) {
	if root == "" {
		return nil, nil
	}
	var claim *string
	if ctx.IsSet("owned-checkout") {
		v := ctx.String("owned-checkout")
		claim = &v
	}
	cwd, _ := os.Getwd()
	f, err := ownedcheckout.ResolveOrAdopt(root, claim, strings.TrimSpace(ctx.String("mission")), cwd,
		ownedcheckout.Options{Allowed: ownedcheckout.NextTopologies, DiscoverSole: true})
	if err != nil {
		switch e := err.(type) {
		case *ownedcheckout.SelectionRequired:
			// Zero / several missions inside the claimed checkout: the
			// missing-handle discovery listing (nudge or selection list).
			if _, serr := resolveNextSlug(ctx, e.Root, "", jsonMode); serr != nil {
				return nil, serr
			}
			return nil, cli.Exit(1)
		case *ownedcheckout.Error:
			ownedcheckout.Emit(ctx.Stdout, ctx.Stderr, e, jsonMode, ownedcheckout.SuccessFalseEnvelope)
			return nil, cli.Exit(1)
		}
		ctx.Eprintf("Error: %s\n", err.Error())
		return nil, cli.Exit(1)
	}
	return f, nil
}

// printDecisionOwned is _print_decision: owned runs (only) also report the
// stale repository-root copy (JSON key; stderr warning after the decision).
func printDecisionOwned(ctx *cli.Context, d *nextrt.Decision, jsonMode bool, answeredID, answer *string, owned *ownedcheckout.Fact) {
	if owned == nil {
		printDecision(ctx, d, jsonMode, answeredID, answer)
		return
	}
	if jsonMode {
		o := d.ToObject()
		if answeredID != nil {
			o.Set("answered", *answeredID)
			o.Set("answer", *answer)
		}
		o.Set("stale_repository_root_copy", ownedcheckout.StaleCopyValue(owned, nil))
		printIndentJSON(ctx, o)
		return
	}
	printDecision(ctx, d, false, answeredID, answer)
	ownedcheckout.EchoStaleCopyWarning(ctx.Stderr, owned)
}

// commitOwnedNextMutations is _commit_owned_next_mutations: commit the
// owned advancement (the mission dir minus the decision runtime lock, plus
// kitty-ops/lifecycle.jsonl) onto the fact's write branch, provisioning the
// lock's ignore rule first when the lock exists.
func commitOwnedNextMutations(f *ownedcheckout.Fact) error {
	p := f.OwnedRoot
	target := safecommit.OwnedPlacement(f, mission.KindPrimaryMetadataUnknown)
	message := "chore(next): persist " + f.MissionSlug + " advancement [skip ci]"
	lock := decision.LockPath(f.MissionDir)
	var delta *ownedcheckout.IgnoreDelta
	parent := ""
	if _, err := os.Stat(lock); err == nil {
		ignore := filepath.Join(filepath.Dir(lock), ".gitignore")
		if _, err := safecommit.Preflight(p, p, target, []string{ignore}, safecommit.Options{Owned: f}); err != nil {
			return err
		}
		parent, _ = gitx.HeadSHA(p)
		d, err := ownedcheckout.EnsureRuntimeLockIgnore(f)
		if err != nil {
			if _, ok := err.(*ownedcheckout.PreconditionError); !ok {
				return err
			}
		} else {
			delta = d
		}
	}
	var paths []string
	filepath.Walk(f.MissionDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() && path != lock {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	if lc := filepath.Join(p, "kitty-ops", "lifecycle.jsonl"); isRegular(lc) {
		paths = append(paths, lc)
	}
	if len(paths) == 0 {
		return nil
	}
	if _, err := safecommit.Commit(p, p, target, message, paths, safecommit.Options{Owned: f}); err != nil {
		if safecommit.IsCode(err, safecommit.CodeStagedTreeUnchanged) {
			return nil
		}
		if delta != nil {
			if h, _ := gitx.HeadSHA(p); h == parent {
				delta.Restore()
			}
		}
		return err
	}
	return nil
}

func isRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
