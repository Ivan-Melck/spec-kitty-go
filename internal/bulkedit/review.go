package bulkedit

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

func ReviewDiff(dir, root, base, head string) (*DiffResult, error) {
	meta, err := mission.ReadMeta(dir)
	if err == mission.ErrNoMeta {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if meta.GetString("change_mode") != "bulk_edit" {
		return nil, nil
	}
	m, err := Load(dir)
	if err != nil {
		return nil, err
	}
	failure := func(message string) *DiffResult {
		return &DiffResult{Passed: false, Assessments: []Assessment{}, Errors: []string{message}, Warnings: []string{}}
	}
	if m == nil {
		return failure("Review diff check cannot run: occurrence_map.yaml is missing despite change_mode: bulk_edit."), nil
	}
	gitFailure := func(stderr, code string) *DiffResult {
		msg := fmt.Sprintf("Review diff check cannot run: git diff failed.; base_ref=%s; head_ref=%s; returncode=%s", mission.PyRepr(base), mission.PyRepr(head), code)
		if stderr != "" {
			msg += "; stderr=" + stderr
		}
		return failure(msg)
	}
	for _, ref := range [][2]string{{"base_ref", base}, {"head_ref", head}} {
		if strings.TrimSpace(ref[1]) == "" {
			return gitFailure(ref[0]+" is empty", "None"), nil
		}
		res, err := gitx.Exec(root, nil, nil, "rev-parse", "--verify", ref[1]+"^{commit}")
		if err != nil {
			return gitFailure(fmt.Sprintf("failed to verify %s %s: %v", ref[0], mission.PyRepr(ref[1]), err), "None"), nil
		}
		if res.Code != 0 {
			stderr := strings.TrimSpace(res.Stderr)
			if stderr == "" {
				stderr = fmt.Sprintf("%s %s could not be resolved", ref[0], mission.PyRepr(ref[1]))
			}
			return gitFailure(stderr, fmt.Sprint(res.Code)), nil
		}
	}
	res, err := gitx.Exec(root, nil, nil, "-c", "diff.relative=false", "diff", "--name-status", "-z", "--find-renames", "--end-of-options", base+".."+head, "--")
	if err != nil {
		return gitFailure(err.Error(), "None"), nil
	}
	if res.Code != 0 {
		return gitFailure(strings.TrimSpace(res.Stderr), fmt.Sprint(res.Code)), nil
	}
	var files []string
	statuses := map[string]string{}
	entries := strings.Split(strings.TrimSuffix(res.Stdout, "\x00"), "\x00")
	for i := 0; i+1 < len(entries); {
		status := entries[i]
		file := entries[i+1]
		i += 2
		if status != "" && (status[0] == 'R' || status[0] == 'C') && i < len(entries) {
			file = entries[i]
			i++
		}
		files = append(files, file)
		if status != "" {
			statuses[file] = strings.ToUpper(status[:1])
		}
	}
	anchor := ""
	relative, e := filepath.Rel(root, dir)
	if e == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		anchor = filepath.ToSlash(relative)
		parts := strings.Split(anchor, "/")
		for i := len(parts) - 2; i >= 0; i-- {
			if parts[i] == "kitty-specs" {
				anchor = strings.Join(parts[i:i+2], "/")
				break
			}
		}
	}
	r := CheckDiff(files, m, anchor, statuses)
	return &r, nil
}
