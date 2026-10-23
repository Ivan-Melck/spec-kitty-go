package consolidate

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// oraclePython is the interpreter of the oracle install (the shebang of
// $SPEC_KITTY_ORACLE); the test skips without it.
func oraclePython(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("SPEC_KITTY_ORACLE")
	if bin == "" {
		t.Skip("SPEC_KITTY_ORACLE not set")
	}
	f, err := os.Open(bin)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	if !strings.HasPrefix(line, "#!") {
		t.Skip("oracle has no shebang")
	}
	return strings.TrimSpace(line[2:])
}

// TestOracleGitProbeErrorText compares every GitProbeError the
// reconciliation probes raise (git's own exit code and stderr inside
// upstream's wording) with the oracle's specify_cli.consolidation.git_probes
// functions, called on the same repository.
func TestOracleGitProbeErrorText(t *testing.T) {
	py := oraclePython(t)
	repo := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644)
	run("add", "a.txt")
	run("commit", "-q", "-m", "one")
	head := run("rev-parse", "HEAD")
	missing := strings.Repeat("ab", 20)

	type probe struct {
		name string
		py   string // python call expression (repo bound to R)
		goFn func() error
	}
	probes := []probe{
		{"resolve_commit", `resolve_commit(R, "nope")`, func() error { _, err := resolveCommit(repo, "nope"); return err }},
		{"commits_in_range", `commits_in_range(R, "nope", "main")`, func() error { _, err := commitsInRange(repo, "nope", "main"); return err }},
		{"first_parent_commits_in_range", `first_parent_commits_in_range(R, "main", "nope")`, func() error {
			_, err := firstParentCommitsInRange(repo, "main", "nope")
			return err
		}},
		{"patch_id_of", `patch_id_of(R, "` + missing + `")`, func() error { _, err := patchIDOf(repo, missing); return err }},
		{"blob_id_at", `blob_id_at(R, "main", "missing.txt")`, func() error { _, err := blobIDAt(repo, "main", "missing.txt"); return err }},
		{"blob_id_at_ref", `blob_id_at(R, "nope", "a.txt")`, func() error { _, err := blobIDAt(repo, "nope", "a.txt"); return err }},
		{"changed_paths_in_range", `changed_paths_in_range(R, "nope", "main")`, func() error { _, err := changedPathsInRange(repo, "nope", "main"); return err }},
		{"changed_paths_of", `changed_paths_of(R, "` + missing + `")`, func() error { _, err := changedPathsOf(repo, missing); return err }},
		{"path_state_at", `path_state_at(R, "nope", "a.txt")`, func() error { _, _, err := pathStateAt(repo, "nope", "a.txt"); return err }},
		{"is_merge_commit", `is_merge_commit(R, "nope")`, func() error { _, err := isMergeCommit(repo, "nope"); return err }},
		{"three_way_merge_blob", `three_way_merge_blob(R, "nope", "` + head + `", "a.txt")`, func() error {
			_, err := threeWayMergeBlob(repo, "nope", head, "a.txt")
			return err
		}},
		{"canceled_content_survives", `canceled_content_survives(R, "` + missing + `", "a.txt", "main")`, func() error {
			_, err := canceledContentSurvives(repo, missing, "a.txt", "main")
			return err
		}},
	}
	var script strings.Builder
	script.WriteString("import json, sys\nfrom pathlib import Path\nfrom specify_cli.consolidation.git_probes import *\nR = Path(sys.argv[1])\nout = {}\n")
	for _, p := range probes {
		script.WriteString("try:\n    " + p.py + "\n    out[" + jsonString(p.name) + "] = None\nexcept GitProbeError as e:\n    out[" + jsonString(p.name) + "] = str(e)\n")
	}
	script.WriteString("print(json.dumps(out))\n")
	cmd := exec.Command(py, "-I", "-c", script.String(), repo)
	cmd.Env = append(os.Environ(), "SPEC_KITTY_NO_UPGRADE_CHECK=1", "HOME="+t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("oracle python: %v\n%s", err, out)
	}
	var want map[string]*string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	for _, p := range probes {
		err := p.goFn()
		w := want[p.name]
		switch {
		case w == nil && err == nil:
		case w == nil:
			t.Errorf("%s: go raised %q, oracle did not", p.name, err)
		case err == nil:
			t.Errorf("%s: oracle raised %q, go did not", p.name, *w)
		case err.Error() != *w:
			t.Errorf("%s:\n  oracle: %s\n  go:     %s", p.name, *w, err.Error())
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
