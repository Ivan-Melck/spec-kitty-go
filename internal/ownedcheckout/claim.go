// Package ownedcheckout ports upstream's owned-checkout authority: the
// checkout-ownership claim validator (core/checkout_ownership.py), the sole
// minter of the OwnedCheckout fact and flagless adoption
// (core/owned_mission.py), the shared CLI surface (cli/commands/
// _owned_checkout.py: refusal envelopes, stale repository-root copy) and the
// mission-scoped owned protection fold (ProtectionPolicy.resolve_for_owned).
package ownedcheckout

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// Error is mission_runtime.ActionContextError: a typed refusal with a code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Refusal codes (OwnedRefusalCode plus the two pre-existing wire codes and
// the registry-unavailable code).
const (
	WorktreeInvocationRefused        = "WORKTREE_INVOCATION_REFUSED"
	OwnershipNested                  = "OWNERSHIP_NESTED"
	OwnershipForeign                 = "OWNERSHIP_FOREIGN"
	OwnershipBrokenPointer           = "OWNERSHIP_BROKEN_POINTER"
	OwnedMissionPathRefused          = "OWNED_MISSION_PATH_REFUSED"
	OwnedTopologyUnsupported         = "OWNED_TOPOLOGY_UNSUPPORTED"
	OwnedBranchRefused               = "OWNED_BRANCH_REFUSED"
	OwnedIndexRefused                = "OWNED_INDEX_REFUSED"
	OwnedCheckoutIsRepositoryRoot    = "OWNED_CHECKOUT_IS_REPOSITORY_ROOT"
	OwnedCheckoutIsMissionWorktree   = "OWNED_CHECKOUT_IS_MISSION_WORKTREE"
	OwnedActionUnsupported           = "OWNED_ACTION_UNSUPPORTED"
	OwnedReviewBaseUnavailable       = "OWNED_REVIEW_BASE_UNAVAILABLE"
	OwnedCoordWorkspaceUnavailable   = "OWNED_COORDINATION_WORKSPACE_UNAVAILABLE"
	WorkPackageUnresolved            = "WORK_PACKAGE_UNRESOLVED"
	OwnedOptionUnsupported           = "OWNED_OPTION_UNSUPPORTED"
	OwnedInputInvalid                = "OWNED_INPUT_INVALID"
	MissionContextUnresolved         = "MISSION_CONTEXT_UNRESOLVED"
	MissionContextConflict           = "MISSION_CONTEXT_CONFLICT"
	WorktreeRegistryUnavailableCode  = "WORKTREE_REGISTRY_UNAVAILABLE"
	repositoryRootRefusal            = "--owned-checkout names the repository root checkout; pass a linked owned checkout instead."
	missionWorktreeRefusal           = "--owned-checkout names a worktree already registered to this mission (coordination worktree or lane worktree); pass a linked owned checkout instead."
	ownershipValidationResultOwned   = "OWNED"
	ownershipValidationResultNested  = "NESTED"
	ownershipValidationResultForeign = "FOREIGN_OR_MISMATCHED"
	ownershipValidationResultBroken  = "BROKEN_POINTER"
)

// PyResolve is pathlib's non-strict Path.resolve(): an absolute path with
// every existing component's symlinks resolved and the missing tail kept.
func PyResolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	dir, rest := abs, ""
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs
		}
		if rest == "" {
			rest = filepath.Base(dir)
		} else {
			rest = filepath.Join(filepath.Base(dir), rest)
		}
		dir = parent
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
	}
}

func posixRepr(p string) string { return "PosixPath(" + mission.PyRepr(p) + ")" }

// topologyError is kernel.git_topology's GitTopologyError family.
type topologyError struct{ msg string }

func (e *topologyError) Error() string { return e.msg }

func notARepo(resolved string) error {
	return &topologyError{posixRepr(resolved) + " is not inside a git repository."}
}

// IsNotARepository reports a NotAGitRepositoryError from GitToplevel /
// GitCommonDir (as opposed to git being unavailable).
func IsNotARepository(err error) bool {
	var te *topologyError
	return errors.As(err, &te) && strings.HasSuffix(te.msg, " is not inside a git repository.")
}

func unavailable(resolved, detail string) error {
	return &topologyError{"git topology probe failed for " + posixRepr(resolved) + ": " + detail}
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func revParse(path, flag string) (resolved, cwd string, res gitx.Result, err error) {
	resolved = PyResolve(path)
	cwd = resolved
	if isFile(resolved) {
		cwd = filepath.Dir(resolved)
	}
	if !isDir(cwd) {
		// subprocess.run(cwd=<missing>) raises FileNotFoundError, which the
		// probe reports as a missing git binary.
		return resolved, cwd, res, unavailable(resolved, "git binary not found on PATH")
	}
	res, err = gitx.Exec(cwd, nil, nil, "rev-parse", flag)
	if err != nil {
		return resolved, cwd, res, unavailable(resolved, "git binary not found on PATH")
	}
	return resolved, cwd, res, nil
}

// GitToplevel is kernel.git_topology.git_toplevel.
func GitToplevel(path string) (string, error) {
	resolved, cwd, res, err := revParse(path, "--show-toplevel")
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(res.Stdout)
	if res.Code != 0 || raw == "" {
		if strings.Contains(strings.ToLower(res.Stderr), "not a git repository") {
			return "", notARepo(resolved)
		}
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = "exit " + itoa(res.Code)
		}
		return "", unavailable(resolved, detail)
	}
	return PyResolve(joinRaw(cwd, raw)), nil
}

// GitCommonDir is kernel.git_topology.git_common_dir.
func GitCommonDir(path string) (string, error) {
	resolved, cwd, res, err := revParse(path, "--git-common-dir")
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		if strings.Contains(strings.ToLower(res.Stderr), "not a git repository") {
			return "", notARepo(resolved)
		}
		return "", unavailable(resolved, strings.TrimSpace(res.Stderr))
	}
	common := PyResolve(joinRaw(cwd, strings.TrimSpace(res.Stdout)))
	if isWithin(resolved, common) {
		return "", notARepo(resolved)
	}
	return common, nil
}

func joinRaw(cwd, raw string) string {
	if filepath.IsAbs(raw) {
		return raw
	}
	return filepath.Join(cwd, raw)
}

func itoa(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
		if n == 0 {
			break
		}
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// isWithin reports whether path is ancestor itself or nested beneath it.
func isWithin(path, ancestor string) bool {
	if path == ancestor {
		return true
	}
	rel, err := filepath.Rel(ancestor, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// IsWorktreeOf is git.commit_helpers.is_worktree_of: worktree is the
// toplevel of a working tree sharing repo's common git dir.
func IsWorktreeOf(repo, worktree string) bool {
	top, err := GitToplevel(worktree)
	if err != nil {
		return false
	}
	resolved := PyResolve(worktree)
	if top != resolved {
		return false
	}
	if resolved == PyResolve(repo) {
		return true
	}
	a, err := GitCommonDir(worktree)
	if err != nil {
		return false
	}
	b, err := GitCommonDir(repo)
	if err != nil {
		return false
	}
	return a == b
}

// RegistryError is coordination.surface_resolver.WorktreeRegistryUnavailable.
type RegistryError struct{ RepoRoot, Detail string }

func (e *RegistryError) Error() string {
	return "Could not read the git worktree registry at " + e.RepoRoot + ": " + e.Detail + ". Topology cannot be determined from path shape alone; fail closed."
}

// ReadWorktreeRegistry is read_worktree_registry: the resolved paths
// `git worktree list --porcelain` registers.
func ReadWorktreeRegistry(repoRoot string) (map[string]bool, error) {
	res, err := gitx.Exec("", nil, nil, "-C", repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, &RegistryError{RepoRoot: repoRoot, Detail: err.Error()}
	}
	if res.Code != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		if detail == "" {
			detail = "exit " + itoa(res.Code)
		}
		return nil, &RegistryError{RepoRoot: repoRoot, Detail: detail}
	}
	out := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			out[PyResolve(strings.TrimSpace(strings.TrimPrefix(line, "worktree ")))] = true
		}
	}
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// enclosingWorktreeRoot is _enclosing_worktree_root: the first
// `.worktrees/<name>` ancestor (path included) of the resolved path.
func enclosingWorktreeRoot(path string) string {
	cur := PyResolve(path)
	for {
		if filepath.Base(filepath.Dir(cur)) == ".worktrees" {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// Worktree topologies (WorktreeTopology).
const (
	TopologyPrimary      = "primary"
	TopologyCoord        = "coord_worktree"
	TopologyLane         = "lane_worktree"
	TopologyUnregistered = "unregistered"
)

// ClassifyWorktreeTopology is classify_worktree_topology(path,
// repo_root=repoRoot).
func ClassifyWorktreeTopology(path, repoRoot string) (string, error) {
	root := enclosingWorktreeRoot(path)
	if root == "" {
		return TopologyPrimary, nil
	}
	if repoRoot == "" {
		repoRoot = path
	}
	reg, err := ReadWorktreeRegistry(repoRoot)
	if err != nil {
		return "", err
	}
	if !reg[root] {
		return TopologyUnregistered, nil
	}
	if strings.HasSuffix(filepath.Base(root), "-coord") {
		return TopologyCoord, nil
	}
	return TopologyLane, nil
}

// Claim is checkout_ownership.OwnershipClaim.
type Claim struct {
	Claimed, Primary string
	Result           string
	Detail           string
}

func brokenPointer(claimed, primary string, err error) Claim {
	return Claim{claimed, primary, ownershipValidationResultBroken,
		"Cannot validate claimed checkout " + claimed + " against resolved primary " + primary + ": " + err.Error()}
}

func rejectedComparatorClaim(claimed, primary string) Claim {
	top, err := GitToplevel(claimed)
	if err != nil {
		return brokenPointer(claimed, primary, err)
	}
	cc, err := GitCommonDir(claimed)
	if err != nil {
		return brokenPointer(claimed, primary, err)
	}
	pc, err := GitCommonDir(primary)
	if err != nil {
		return brokenPointer(claimed, primary, err)
	}
	if top != claimed {
		return Claim{claimed, primary, ownershipValidationResultNested,
			"Claimed checkout " + claimed + " is nested inside checkout root " + top + "."}
	}
	if cc == pc {
		return Claim{claimed, primary, ownershipValidationResultBroken,
			"Git rejected claimed checkout " + claimed + " despite common dir " + cc + " matching resolved primary " + primary + "."}
	}
	return Claim{claimed, primary, ownershipValidationResultForeign,
		"Claimed checkout " + claimed + " uses common dir " + cc + "; resolved primary " + primary + " uses common dir " + pc + "."}
}

// ResolveOwnershipClaim is resolve_ownership_claim(checkout,
// resolved_primary=primary) for an explicit (opted-in) claim.
func ResolveOwnershipClaim(checkout, primary string) Claim {
	primary = PyResolve(primary)
	claimed := PyResolve(checkout)
	if !IsWorktreeOf(primary, claimed) {
		return rejectedComparatorClaim(claimed, primary)
	}
	if claimed == primary {
		return Claim{claimed, primary, ownershipValidationResultOwned, ""}
	}
	reg, err := ReadWorktreeRegistry(primary)
	if err != nil {
		return Claim{claimed, primary, ownershipValidationResultBroken,
			"Cannot inspect worktree registry for claimed checkout " + claimed + " and resolved primary " + primary + ": " + err.Error()}
	}
	for _, registered := range sortedKeys(reg) {
		if registered == claimed || registered == primary {
			continue
		}
		if isWithin(claimed, registered) {
			return Claim{claimed, primary, ownershipValidationResultNested,
				"Claimed checkout " + claimed + " is nested inside registered worktree " + registered + "."}
		}
	}
	return Claim{claimed, primary, ownershipValidationResultOwned, ""}
}

// Err is error_for_claim: the typed refusal, or nil when owned.
func (c Claim) Err() *Error {
	code := ""
	switch c.Result {
	case ownershipValidationResultNested:
		code = OwnershipNested
	case ownershipValidationResultForeign:
		code = OwnershipForeign
	case ownershipValidationResultBroken:
		code = OwnershipBrokenPointer
	default:
		return nil
	}
	msg := c.Detail
	if msg == "" {
		msg = c.Result
	}
	return refuse(code, msg)
}

// requireOwnedClaim is _require_owned_claim: validate the claim and refuse
// the repository root itself.
func requireOwnedClaim(repositoryRoot, checkout string) (Claim, error) {
	c := ResolveOwnershipClaim(checkout, repositoryRoot)
	if e := c.Err(); e != nil {
		return c, e
	}
	if c.Claimed == c.Primary {
		return c, refuse(OwnedCheckoutIsRepositoryRoot, repositoryRootRefusal)
	}
	return c, nil
}
