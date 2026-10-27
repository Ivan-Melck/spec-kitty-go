package planning

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/filelock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// `agent decision repair-runtime-lock` (cli/commands/decision.py
// _repair_runtime_lock + decisions/service.py _runtime_lock_ignore_state):
// stop tracking the exact decision index lock of an explicitly owned
// mission, adding the exact owner ignore rule when it is missing.

// lockRaceError is _RuntimeLockRaceError (RUNTIME_LOCK_RACE).
type lockRaceError struct{ msg string }

func (e *lockRaceError) Error() string { return e.msg }

func runtimeLockErrorCode(err error) string {
	var race *lockRaceError
	var pre *ownedcheckout.PreconditionError
	switch {
	case errors.As(err, &race):
		return "RUNTIME_LOCK_RACE"
	case errors.As(err, &pre):
		return "RUNTIME_LOCK_PRECONDITION_REFUSED"
	}
	return "RUNTIME_LOCK_REPAIR_FAILED"
}

func lockRel(root, p string) string { return ownedcheckout.RelTo(root, p) }

func lockTracked(owned *ownedcheckout.Fact, lock string) bool {
	out, err := gitx.Run(owned.OwnedRoot, "ls-files", "--", lockRel(owned.OwnedRoot, lock))
	return err == nil && strings.TrimSpace(out) != ""
}

// repairRuntimeLock is _repair_runtime_lock.
func repairRuntimeLock(owned *ownedcheckout.Fact, dryRun bool) (*jsonx.Object, error) {
	lock, ignore, ignored, err := ownedcheckout.RuntimeLockIgnoreState(owned)
	if err != nil {
		return nil, err
	}
	relLock, relIgnore := lockRel(owned.OwnedRoot, lock), lockRel(owned.OwnedRoot, ignore)
	entries, err := safecommit.StatusEntries(owned.OwnedRoot, "all", relLock, relIgnore)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !(e.Untracked() && e.Path == relLock) {
			return nil, &ownedcheckout.PreconditionError{Msg: "Runtime lock repair refuses preexisting changes or staging on its lock or .gitignore"}
		}
	}
	tracked := lockTracked(owned, lock)
	paths := []string{ignore}
	if tracked {
		paths = []string{ignore, lock}
	}
	target := safecommit.OwnedPlacement(owned, mission.KindPrimaryMetadataUnknown)
	message := "chore(decisions): stop tracking runtime lock for " + owned.MissionSlug
	if _, err := safecommit.Preflight(owned.OwnedRoot, owned.OwnedRoot, target, paths, safecommit.Options{Owned: owned}); err != nil {
		return nil, err
	}
	changed := tracked || !ignored
	payload := jsonx.NewObject("status", "ok", "mission_slug", owned.MissionSlug, "dry_run", dryRun, "tracked", tracked, "ignored", ignored, "changed", changed)
	if dryRun || !changed {
		return payload, nil
	}
	os.MkdirAll(filepath.Dir(lock), 0o755)
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := filelock.LockFile(f); err != nil {
		return nil, err
	}
	defer filelock.UnlockFile(f)
	_, _, ignoredNow, err := ownedcheckout.RuntimeLockIgnoreState(owned)
	if err != nil {
		return nil, err
	}
	if lockTracked(owned, lock) != tracked {
		return nil, &lockRaceError{"Runtime lock index changed while waiting for the owner lock"}
	}
	if out, _ := gitx.Run(owned.OwnedRoot, "diff", "--cached", "HEAD", "--", relLock); strings.TrimSpace(out) != "" {
		return nil, &lockRaceError{"Runtime lock staging changed while waiting for the owner lock"}
	}
	parent, err := gitx.HeadSHA(owned.OwnedRoot)
	if err != nil {
		return nil, err
	}
	old, oldErr := os.ReadFile(ignore)
	var written []byte
	if !ignoredNow {
		if _, err := ownedcheckout.EnsureRuntimeLockIgnore(owned); err != nil {
			return nil, err
		}
	}
	if b, err := os.ReadFile(ignore); err == nil {
		written = b
	}
	sha, cerr := commitLockRepair(owned, parent, message, relIgnore, relLock, written != nil, tracked)
	if cerr != nil {
		if h, _ := gitx.HeadSHA(owned.OwnedRoot); h == parent && written != nil {
			if cur, err := os.ReadFile(ignore); err == nil && string(cur) == string(written) {
				if oldErr != nil {
					os.Remove(ignore)
				} else {
					os.WriteFile(ignore, old, 0o644)
				}
			}
		}
		return nil, cerr
	}
	payload.Set("commit", sha)
	return payload, nil
}

// commitLockRepair commits exactly the ignore file (when written) and the
// index-only removal of the lock onto the owned write branch, through a
// temporary index, then mirrors both changes into the real index.
func commitLockRepair(owned *ownedcheckout.Fact, parent, message, relIgnore, relLock string, withIgnore, removeLock bool) (string, error) {
	root := owned.OwnedRoot
	tmp, err := os.CreateTemp("", "spec-kitty-index-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath)
	defer os.Remove(tmpPath)
	env := []string{"GIT_INDEX_FILE=" + tmpPath}
	if _, err := gitx.RunEnv(root, env, "read-tree", parent); err != nil {
		return "", err
	}
	if withIgnore {
		if _, err := gitx.RunEnv(root, env, "add", "--force", "--", relIgnore); err != nil {
			return "", err
		}
	}
	if removeLock {
		if _, err := gitx.RunEnv(root, env, "rm", "--cached", "-q", "--", relLock); err != nil {
			return "", err
		}
	}
	tree, err := gitx.RunEnv(root, env, "write-tree")
	if err != nil {
		return "", err
	}
	sha, err := gitx.Run(root, "-c", "commit.gpgsign=false", "commit-tree", strings.TrimSpace(tree), "-p", parent, "-m", message)
	if err != nil {
		return "", err
	}
	sha = strings.TrimSpace(sha)
	if _, err := gitx.Run(root, "update-ref", "-m", "commit: "+message, "refs/heads/"+owned.WriteBranch, sha, parent); err != nil {
		return "", err
	}
	if withIgnore {
		gitx.Run(root, "add", "--force", "--", relIgnore)
	}
	if removeLock {
		gitx.Run(root, "rm", "--cached", "-q", "--", relLock)
	}
	return sha, nil
}

func runRepairRuntimeLockOwned(ctx *cli.Context) error {
	if !ctx.IsSet("owned-checkout") {
		return cli.Usage(ctx.Cmd, "Invalid value: repair-runtime-lock requires explicit --owned-checkout")
	}
	root := project.Locate("")
	if root == "" {
		root = cwd()
	}
	owned, err := resolveOwnedOrRefuse(ctx, root, ctx.String("mission"), true, func(code, msg string) *jsonx.Object {
		return jsonx.NewObject("status", "error", "code", code, "message", msg)
	}, ownedcheckout.Options{})
	if err != nil {
		var me *ownedcheckout.MetaReadError
		if errors.As(err, &me) {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("status", "error", "code", "RUNTIME_LOCK_REPAIR_FAILED", "message", err.Error()), compact))
			return cli.Exit(1)
		}
		return err
	}
	if owned == nil {
		return cli.Exit(1)
	}
	payload, err := repairRuntimeLock(owned, ctx.Bool("dry-run"))
	if err != nil {
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("status", "error", "code", runtimeLockErrorCode(err), "message", err.Error()), compact))
		return cli.Exit(1)
	}
	fmt.Fprintln(ctx.Stdout, jsonx.Dumps(payload, compact))
	return nil
}
