package consolidate

// Post-checkpoint bookkeeping projection and the squash projected-content
// proof (consolidation.bookkeeping_projection): files a concurrent commit
// landed on the coordination branch during the run are carried onto the
// target, and a squash PASS proves each one landed (byte-equal, or the
// registered merge driver's replayed output when the target diverged).

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mergedriver"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// coordCheckpoint is _CoordCheckpoint (ref + tip captured at claim time).
type coordCheckpoint struct{ ref, sha string }

func readBlobBytes(root, ref, rel string) ([]byte, bool) {
	res, _ := gitx.Exec(root, nil, nil, "show", ref+":"+rel)
	if res.Code != 0 {
		return nil, false
	}
	return []byte(res.Stdout), true
}

// postCheckpointMissionPaths is _post_checkpoint_mission_paths.
func postCheckpointMissionPaths(root, slug, checkpoint, coordRef string) ([]string, error) {
	prefix := "kitty-specs/" + slug + "/"
	res := git(root, "-c", "core.quotePath=false", "diff", "--name-only", "-z", "-M", checkpoint, coordRef, "--", prefix)
	if res.Code != 0 {
		return nil, probeErr("git diff --name-only %s %s failed (exit %d): %s", checkpoint, coordRef, res.Code, strings.TrimSpace(res.Stderr))
	}
	var out []string
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p == "" || !strings.HasPrefix(p, prefix) {
			continue
		}
		switch path.Base(p) {
		case "status.events.jsonl", "status.json", "meta.json":
			continue
		}
		kind := mission.ArtifactKindForPath(p)
		if kind != "" && (!mission.CoordinationKinds[kind] || kind == mission.KindIssueMatrix || kind == mission.KindAcceptanceMatrix) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// projectPostCheckpoint is project_post_checkpoint_commits_to_target: the
// repo-relative paths written into the primary mission directory.
func projectPostCheckpoint(root, slug, coordRef, checkpoint, primaryDir string) []string {
	tip := revParse(root, coordRef)
	if tip == "" || checkpoint == "" {
		return nil
	}
	if cs, err := commitsInRange(root, checkpoint, coordRef); err != nil || len(cs) == 0 {
		return nil
	}
	paths, err := postCheckpointMissionPaths(root, slug, checkpoint, coordRef)
	if err != nil {
		return nil
	}
	var projected []string
	for _, rel := range paths {
		content, ok := readBlobBytes(root, coordRef, rel)
		if !ok {
			continue
		}
		dst := filepath.Join(primaryDir, filepath.FromSlash(strings.TrimPrefix(rel, "kitty-specs/"+slug+"/")))
		base, baseOK := readBlobBytes(root, checkpoint, rel)
		if cur, err := os.ReadFile(dst); err == nil && (!baseOK || !bytes.Equal(cur, base)) {
			continue
		}
		os.MkdirAll(filepath.Dir(dst), 0o755)
		os.WriteFile(dst, content, 0o644)
		projected = append(projected, rel)
	}
	return projected
}

// driverReplay is driver_replay_expected_bytes.
func driverReplay(root, rel, baseRef, oursRef, theirsRef string) ([]byte, error) {
	res := git(root, "check-attr", "--source", oursRef, "merge", "--", rel)
	if res.Code != 0 {
		return nil, probeErr("driver replay for '%s': no registered merge driver at %s", rel, oursRef)
	}
	line := strings.TrimSpace(res.Stdout)
	value := line
	if i := strings.LastIndex(line, ": "); i >= 0 {
		value = strings.TrimSpace(line[i+2:])
	}
	switch value {
	case "", "unspecified", "unset", "set":
		return nil, probeErr("driver replay for '%s': no registered merge driver at %s", rel, oursRef)
	}
	body := mergedriver.Bodies[strings.Replace(value, "spec-kitty-", "merge-driver-", 1)]
	if body == nil {
		return nil, probeErr("no merge-driver registry entry for config key '%s'", value)
	}
	ours, ok1 := readBlobBytes(root, oursRef, rel)
	theirs, ok2 := readBlobBytes(root, theirsRef, rel)
	if !ok1 || !ok2 {
		return nil, probeErr("driver replay for '%s': missing ours (%s) or theirs (%s) blob", rel, oursRef, theirsRef)
	}
	base, _ := readBlobBytes(root, baseRef, rel)
	dir, err := os.MkdirTemp("", "kitty-driver-replay-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	o, a, b := filepath.Join(dir, "O"), filepath.Join(dir, "A"), filepath.Join(dir, "B")
	os.WriteFile(o, base, 0o644)
	os.WriteFile(a, ours, 0o644)
	os.WriteFile(b, theirs, 0o644)
	if _, err := body(o, a, b); err != nil {
		return nil, probeErr("driver replay for '%s' (%s) failed: %s", rel, value, err)
	}
	return os.ReadFile(a)
}

// projectedContentMatches is projected_content_matches_target.
func projectedContentMatches(root, coordRef, targetRef string, paths []string, checkpoint, preSquash string) bool {
	for _, rel := range paths {
		coordBytes, ok := readBlobBytes(root, coordRef, rel)
		if !ok {
			return false
		}
		target, tOK := readBlobBytes(root, targetRef, rel)
		base, baseOK := readBlobBytes(root, checkpoint, rel)
		pre, preOK := readBlobBytes(root, preSquash, rel)
		if preOK == baseOK && bytes.Equal(pre, base) {
			if !tOK || !bytes.Equal(target, coordBytes) {
				return false
			}
			continue
		}
		expected, err := driverReplay(root, rel, checkpoint, preSquash, coordRef)
		if err != nil || !tOK || !bytes.Equal(target, expected) {
			return false
		}
	}
	return true
}
