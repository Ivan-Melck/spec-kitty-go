package issuematrix

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// IssueMatrixRefReadError codes (mission_runtime.resolution).
const (
	RefAbsent    = "ISSUE_MATRIX_REF_ABSENT"
	ProbeError   = "ISSUE_MATRIX_PROBE_ERROR"
	EmptyContent = "ISSUE_MATRIX_EMPTY_CONTENT"
	// NotAuthored (#6653): the ref is valid and neither issue-matrix.json nor
	// issue-matrix.md was ever committed there, positively observed by a
	// tree-only probe. Callers may treat it as "no matrix" only where an
	// absent matrix is legitimately nothing to enforce.
	NotAuthored = "ISSUE_MATRIX_NOT_AUTHORED"
)

// RefReadError is IssueMatrixRefReadError: a typed fail-closed refusal of
// ReadRefContent. Error() is str(exc).
type RefReadError struct {
	Code string
	Msg  string
	// exited marks a probe error from a `git show` that ran and exited
	// non-zero (_IssueMatrixProbeExited): only that shape is eligible for
	// the NotAuthored presence check.
	exited bool
}

func (e *RefReadError) Error() string { return e.Msg }

// ReadRefContent is read_issue_matrix_ref_content for the mission dir slug
// at ref: issue-matrix.json, else the legacy issue-matrix.md, read with
// `git show`. It never falls back to the primary residue.
func ReadRefContent(root, slug, ref string) (string, error) {
	if !gitx.OK(root, "rev-parse", "--verify", "--quiet", ref) {
		return "", &RefReadError{Code: RefAbsent, Msg: fmt.Sprintf("issue-matrix read ref %s does not resolve in %s — it has been deleted; refusing rather than falling back to the primary residue.", mission.PyRepr(ref), root)}
	}
	jsonPath := "kitty-specs/" + slug + "/" + JSONFile
	mdPath := "kitty-specs/" + slug + "/" + MDFile
	content, obj, err := showRefObject(root, ref, jsonPath)
	if first, ok := err.(*RefReadError); ok && first.Code == ProbeError {
		content, obj, err = showRefObject(root, ref, mdPath)
		if md, ok := err.(*RefReadError); ok && first.exited && md.exited &&
			pathAbsentFromTree(root, ref, jsonPath) && pathAbsentFromTree(root, ref, mdPath) {
			return "", &RefReadError{Code: NotAuthored, Msg: fmt.Sprintf("no issue matrix was ever committed at %s (looked for %s, %s) in %s.", ref, jsonPath, mdPath, root)}
		}
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", &RefReadError{Code: EmptyContent, Msg: fmt.Sprintf("issue-matrix content at %s is empty — refusing rather than treating an empty authored set as nothing to enforce.", obj)}
	}
	return content, nil
}

func showRefObject(root, ref, path string) (string, string, error) {
	obj := ref + ":" + path
	res, err := gitx.Exec(root, nil, nil, "show", obj)
	if err != nil {
		return "", obj, &RefReadError{Code: ProbeError, Msg: fmt.Sprintf("git show %s could not be run in %s: %v", mission.PyRepr(obj), root, err)}
	}
	if res.Code != 0 {
		return "", obj, &RefReadError{Code: ProbeError, exited: true, Msg: fmt.Sprintf("git show %s failed in %s: %s", mission.PyRepr(obj), root, strings.TrimSpace(res.Stderr))}
	}
	return res.Stdout, obj, nil
}

// pathAbsentFromTree is _path_absent_from_tree: true only when a tree-only
// listing (`git ls-tree --full-tree -z <ref> -- <path>`) ran cleanly and has
// no entry at exactly path. A listed path whose object is unreadable, or a
// probe that fails, is not a positive absence.
func pathAbsentFromTree(root, ref, path string) bool {
	res, err := gitx.Exec(root, nil, nil, "ls-tree", "--full-tree", "-z", ref, "--", path)
	if err != nil || res.Code != 0 {
		return false
	}
	for _, rec := range bytes.Split([]byte(res.Stdout), []byte{0}) {
		if i := bytes.IndexByte(rec, '\t'); i >= 0 && string(rec[i+1:]) == path {
			return false
		}
	}
	return true
}
