package missionart

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func realpath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// RepoTreePath is kernel.paths.repo_tree_path: (git cwd, tree path).
func RepoTreePath(file, repoRoot string) (string, string, bool) {
	ra := realpath(repoRoot)
	rel, err := filepath.Rel(ra, realpath(file))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 2 && parts[0] == ".worktrees" {
		wt := filepath.Join(ra, parts[0], parts[1])
		if st, err := os.Stat(wt); err == nil && st.IsDir() {
			return wt, strings.Join(parts[2:], "/"), true
		}
	}
	return ra, strings.Join(parts, "/"), true
}

// IsCommitted is _substantive.is_committed: tracked and present at HEAD of
// the surface the file lives on.
func IsCommitted(file, repoRoot string) bool {
	cwd, tree, ok := RepoTreePath(file, repoRoot)
	if !ok {
		return false
	}
	out, err := gitx.Run(cwd, "--literal-pathspecs", "ls-files", "--error-unmatch", "--", tree)
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	return gitx.OK(cwd, "cat-file", "-e", "HEAD:"+tree)
}

// MissionTypeFor is get_mission_type(feature_dir) or software-dev.
func MissionTypeFor(featureDir string) string {
	meta := mission.ReadMetaOrEmpty(featureDir)
	if t := mission.MissionType(meta); t != "" {
		return t
	}
	return "software-dev"
}

// MissionYAML loads the built-in mission.yaml for missionType (nil when absent).
func MissionYAML(missionType string) *jsonx.Object {
	b, err := assets.ReadFile("packs/built-in/missions/" + missionType + "/mission.yaml")
	if err != nil {
		return nil
	}
	o, err := yamlx.ParseObject(b)
	if err != nil {
		return nil
	}
	return o
}

// WriterArtifacts is _feature_writer_artifacts: (file artifacts, dir artifacts).
func WriterArtifacts(featureDir string) ([]string, []string) {
	y := MissionYAML(MissionTypeFor(featureDir))
	if y == nil {
		y = MissionYAML("software-dev")
	}
	if y == nil {
		return nil, nil
	}
	a := y.GetObject("artifacts")
	var all []string
	if a != nil {
		for _, k := range []string{"required", "optional"} {
			for _, v := range a.GetList(k) {
				if s, ok := v.(string); ok {
					all = append(all, s)
				}
			}
		}
	}
	var files, dirs []string
	for _, n := range all {
		if strings.HasSuffix(n, "/") {
			dirs = append(dirs, n)
		} else {
			files = append(files, n)
		}
	}
	return files, dirs
}

var nonAlnum = regexp.MustCompile(`[^0-9a-zA-Z]+`)

// InventoryKey is _artifact_inventory_key.
func InventoryKey(name, suffix string) string {
	stem := strings.TrimRight(name, "/")
	if !strings.HasSuffix(name, "/") {
		if i := strings.LastIndex(stem, "."); i >= 0 {
			stem = stem[:i]
		}
	}
	slug := strings.ToLower(strings.Trim(nonAlnum.ReplaceAllString(stem, "_"), "_"))
	return slug + "_" + suffix
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Structure is validate_feature_structure's result.
type Structure struct {
	Valid         bool
	Errors        []string
	Warnings      []string
	Paths         *jsonx.Object
	ArtifactFiles *jsonx.Object
	ArtifactDirs  *jsonx.Object
	AvailableDocs []string
	MissionDir    string // "" when the dir is missing
}

func setDefault(o *jsonx.Object, k, v string) {
	if !o.Has(k) {
		o.Set(k, v)
	}
}

// ValidateFeatureStructure is core.worktree.validate_feature_structure.
func ValidateFeatureStructure(featureDir string, checkTasks bool) *Structure {
	s := &Structure{Paths: &jsonx.Object{}, ArtifactFiles: &jsonx.Object{}, ArtifactDirs: &jsonx.Object{}, Errors: []string{}, Warnings: []string{}, AvailableDocs: []string{}}
	if !exists(featureDir) {
		s.Errors = append(s.Errors, "Feature directory not found: "+featureDir)
		return s
	}
	var docs []string
	spec := filepath.Join(featureDir, "spec.md")
	if !exists(spec) {
		s.Errors = append(s.Errors, "Missing required file: spec.md")
	} else {
		s.Paths.Set("spec_file", spec)
		s.ArtifactFiles.Set("spec_file", spec)
		docs = append(docs, "spec.md")
	}
	if plan := filepath.Join(featureDir, "plan.md"); exists(plan) {
		s.Paths.Set("plan_file", plan)
		s.ArtifactFiles.Set("plan_file", plan)
		docs = append(docs, "plan.md")
	}
	for _, d := range []string{"checklists", "research", "tasks"} {
		if !exists(filepath.Join(featureDir, d)) {
			s.Warnings = append(s.Warnings, "Missing recommended directory: "+d+"/")
		}
	}
	for _, d := range []string{"checklists", "tasks"} {
		if p := filepath.Join(featureDir, d); exists(p) {
			s.Paths.Set(d+"_dir", p)
			s.ArtifactDirs.Set(d+"_dir", p)
		}
	}
	tasks := filepath.Join(featureDir, "tasks.md")
	if exists(tasks) {
		s.Paths.Set("tasks_file", tasks)
		s.ArtifactFiles.Set("tasks_file", tasks)
		docs = append(docs, "tasks.md")
	} else if checkTasks {
		s.Errors = append(s.Errors, "Missing required file: tasks.md")
	}
	s.Paths.Set("mission_dir", featureDir)
	s.Paths.Set("feature_dir", featureDir)
	s.ArtifactDirs.Set("feature_dir", featureDir)
	files, dirs := WriterArtifacts(featureDir)
	for _, n := range files {
		c := filepath.Join(featureDir, n)
		if !isFile(c) {
			continue
		}
		k := InventoryKey(n, "file")
		setDefault(s.Paths, k, c)
		setDefault(s.ArtifactFiles, k, c)
		docs = append(docs, n)
	}
	for _, n := range dirs {
		c := filepath.Join(featureDir, strings.TrimRight(n, "/"))
		if !isDir(c) {
			continue
		}
		k := InventoryKey(n, "dir")
		setDefault(s.Paths, k, c)
		setDefault(s.ArtifactDirs, k, c)
	}
	seen := map[string]bool{}
	for _, d := range docs {
		if !seen[d] {
			seen[d] = true
			s.AvailableDocs = append(s.AvailableDocs, d)
		}
	}
	sort.Strings(s.AvailableDocs)
	s.Valid = len(s.Errors) == 0
	s.MissionDir = featureDir
	return s
}

// Payload renders the validation dict (key order as upstream).
func (s *Structure) Payload() *jsonx.Object {
	o := jsonx.NewObject("valid", s.Valid, "errors", strList(s.Errors), "warnings", strList(s.Warnings), "paths", s.Paths,
		"artifact_files", s.ArtifactFiles, "artifact_dirs", s.ArtifactDirs, "available_docs", strList(s.AvailableDocs))
	if s.MissionDir == "" {
		o.Set("FEATURE_DIR", "")
		o.Set("AVAILABLE_DOCS", strList(s.AvailableDocs))
		return o
	}
	o.Set("mission_dir", s.MissionDir)
	o.Set("FEATURE_DIR", s.MissionDir)
	o.Set("AVAILABLE_DOCS", strList(s.AvailableDocs))
	return o
}

func strList(xs []string) []any {
	out := make([]any, 0, len(xs))
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}
