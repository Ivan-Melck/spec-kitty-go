package invocation

// Op custody (invocation/op_custody.py, #6564/#6682): where an Op record
// belongs, and whether it may be written there.
//
// The project root (governance: charter, profiles, glossaries) and the record
// root (the checkout whose kitty-ops/ holds the Op records and whose branch
// takes their commit) are kept apart: an Op run from a linked worktree keeps
// its record in that worktree, never in the repository root checkout.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Error codes of the pre-write admission refusal (#6564).
const (
	OpWriteProtectedBranch         = "OP_WRITE_PROTECTED_BRANCH"
	OpWriteProtectionConfigInvalid = "OP_WRITE_PROTECTION_CONFIG_INVALID"
)

// Roots pairs the project root (governance is read there) with the record
// root (the checkout that owns the Op records). An empty Record means the
// project root.
type Roots struct{ Project, Record string }

// RecordRoot returns the checkout that owns the Op records.
func (r Roots) RecordRoot() string {
	if r.Record == "" {
		return r.Project
	}
	return r.Record
}

// ResolveRecordRoot is resolve_op_record_root: the git checkout containing
// start (default: the working directory) when it belongs to the project's
// repository, else projectRoot.
func ResolveRecordRoot(projectRoot, start string) string {
	if start == "" {
		start, _ = os.Getwd()
	}
	top, err := ownedcheckout.GitToplevel(start)
	if err != nil {
		return projectRoot
	}
	if !ownedcheckout.IsWorktreeOf(projectRoot, top) {
		return projectRoot
	}
	return top
}

// repositoryCheckouts lists every checkout `git worktree list` reports.
func repositoryCheckouts(projectRoot string) []string {
	res, err := gitx.Exec(projectRoot, nil, nil, "worktree", "list", "--porcelain")
	if err != nil || res.Code != 0 {
		return nil
	}
	var out []string
	for _, l := range strings.Split(res.Stdout, "\n") {
		l = strings.TrimSpace(l)
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			out = append(out, ownedcheckout.PyResolve(p))
		}
	}
	return out
}

func holdsRecord(checkout, id string) bool {
	p, err := RecordPath(checkout, id)
	if err != nil {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// LocateRecordRoot is locate_op_record_root: the checkout holding
// kitty-ops/<id>.jsonl — preferred (the invoking checkout) first, then the
// project root, then every other checkout of the repository; "" when none
// does (an invalid id is never found).
func LocateRecordRoot(projectRoot, id, preferred string) string {
	seen := map[string]bool{}
	var cands []string
	if preferred != "" {
		cands = append(cands, preferred)
	}
	cands = append(cands, projectRoot)
	cands = append(cands, repositoryCheckouts(projectRoot)...)
	for _, c := range cands {
		key := ownedcheckout.PyResolve(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		if holdsRecord(c, id) {
			return c
		}
	}
	return ""
}

// WriteRefusal is OpWriteRefusal: why an Op record may not be written in
// its checkout. Reason never names the protected-branch override.
type WriteRefusal struct{ Code, Reason string }

// WriteRefusedError is OpWriteRefused: the executor refused to write an Op
// record before writing anything. Action is "open the Op" or "close the Op".
type WriteRefusedError struct {
	Refusal    WriteRefusal
	RecordRoot string
	Action     string
}

func (e *WriteRefusedError) Error() string {
	var problem, remedy string
	if e.Refusal.Code == OpWriteProtectedBranch {
		problem = fmt.Sprintf("its Op record would be written in %s on %s, where the record cannot be committed", e.RecordRoot, e.Refusal.Reason)
		remedy = "Run the command from a topic branch, or from a worktree whose branch is not protected."
	} else {
		problem = fmt.Sprintf("the branch protection settings for %s cannot be read (%s)", e.RecordRoot, e.Refusal.Reason)
		remedy = "Fix the protection: block in .kittify/config.yaml, then run the command again."
	}
	return fmt.Sprintf("Refusing to %s: %s. Nothing was written. %s", e.Action, problem, remedy)
}

// admissionProbeRecord is _ADMISSION_PROBE_RECORD: ULID-shaped, so it is
// judged exactly like the record the command is about to write.
const admissionProbeRecord = EventsDir + "/00000000000000000000000000.jsonl"

// RequireOpWriteAdmitted is require_op_write_admitted.
func RequireOpWriteAdmitted(roots Roots, action string) error {
	if r := OpWriteRefusal(roots); r != nil {
		return &WriteRefusedError{Refusal: *r, RecordRoot: roots.RecordRoot(), Action: action}
	}
	return nil
}

// OpWriteRefusal is op_write_refusal: the Op record of a dispatch or agent
// close is committed on the branch checked out in the record root; where
// that commit would be refused by the protected-branch guard, say so before
// anything is written. The #6410 skip rules come first (a repository that
// keeps no Op records writes them locally, never commits); any other commit
// problem (no worktree, detached HEAD, missing branch) stays a post-write
// report. Returns nil when the write is admitted.
func OpWriteRefusal(roots Roots) *WriteRefusal {
	record := roots.RecordRoot()
	branch := currentBranch(record)
	if branch == "" {
		return nil
	}
	tracking, err := ProbePrimaryTracking(record)
	if err != nil {
		return nil
	}
	if skip, err := OpCommitSkipReason(record, admissionProbeRecord, tracking); err != nil || skip != "" {
		return nil
	}
	// preflight_commit: the record checkout must be a worktree of the
	// project's repository, on the branch, and the branch must exist.
	if !ownedcheckout.IsWorktreeOf(roots.Project, record) {
		return nil
	}
	if !gitx.OK(record, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) {
		return nil
	}
	protected, err := branchProtected(roots, branch)
	if err != nil {
		return &WriteRefusal{OpWriteProtectionConfigInvalid, err.Error()}
	}
	if protected {
		return &WriteRefusal{OpWriteProtectedBranch, "protected branch " + pyRepr(branch)}
	}
	return nil
}

// branchProtected is the preflight_commit protection check for an Op-record
// path: the project root's policy OR the record checkout's.
func branchProtected(roots Roots, branch string) (bool, error) {
	// Both policies are resolved (and so validated) before either is asked;
	// the operator hatch then unprotects every ref.
	repo, err := protectedBranchSet(roots.Project)
	if err != nil {
		return false, err
	}
	wt, err := protectedBranchSet(roots.RecordRoot())
	if err != nil {
		return false, err
	}
	if protectionHatch() {
		return false, nil
	}
	return repo[branch] || wt[branch], nil
}

// ProtectionConfigError is git.protection_policy.ProtectionConfigError.
type ProtectionConfigError struct{ Msg string }

func (e *ProtectionConfigError) Error() string { return e.Msg }

// protectedBranchSet is ProtectionPolicy.resolve(root).protected_branches:
// protection.protected_branches exactly when configured, else {main, master}
// plus the remote default branch; a malformed configuration is an error.
func protectedBranchSet(root string) (map[string]bool, error) {
	cfgPath := filepath.Join(root, ".kittify", "config.yaml")
	var data *jsonx.Object
	if b, err := os.ReadFile(cfgPath); err == nil {
		v, err := yamlx.ParseWith(b, yamlx.ParseOptions{TextFile: cfgPath})
		if err != nil {
			return nil, &ProtectionConfigError{fmt.Sprintf("Failed to parse %s: %v", cfgPath, err)}
		}
		switch t := v.(type) {
		case nil:
		case *jsonx.Object:
			data = t
		default:
			return nil, &ProtectionConfigError{fmt.Sprintf("%s must be a YAML mapping at the top level; found %s instead.", cfgPath, yamlTypeName(v))}
		}
	}
	if data != nil {
		if block := data.GetObject("protection"); block != nil {
			if raw, ok := block.Get("protected_branches"); ok && raw != nil {
				l, ok := raw.([]any)
				if !ok {
					return nil, &ProtectionConfigError{fmt.Sprintf("protection.protected_branches in .kittify/config.yaml must be a list, got %s: %s", yamlTypeName(raw), yamlRepr(raw))}
				}
				out := map[string]bool{}
				for _, x := range l {
					out[yamlStr(x)] = true
				}
				return out, nil
			}
		}
	}
	out := map[string]bool{"main": true, "master": true}
	if ref, err := gitx.Run(root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref = strings.TrimSpace(ref); strings.Contains(ref, "/") {
			out[ref[strings.LastIndex(ref, "/")+1:]] = true
		}
	}
	return out, nil
}

func yamlTypeName(v any) string {
	switch v.(type) {
	case string:
		return "str"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case bool:
		return "bool"
	case *jsonx.Object:
		return "CommentedMap"
	case []any:
		return "CommentedSeq"
	case nil:
		return "NoneType"
	}
	return fmt.Sprintf("%T", v)
}

func yamlRepr(v any) string {
	switch t := v.(type) {
	case string:
		return pyRepr(t)
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return fmt.Sprint(v)
}

func yamlStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return fmt.Sprint(v)
}
