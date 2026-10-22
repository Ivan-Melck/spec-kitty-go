package charter

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

// This file ports `charter org init`: the minimal org-pack scaffold
// (cli/commands/charter/org.py) and the template render pipeline
// (specify_cli.charter_packs.template_render).

// OrgCharterStub is the scaffolded org-charter.yaml.
const OrgCharterStub = `schema_version: "2"
org_name: TODO replace with your organisation name
required_directives: []
required_tactics: []
required_paradigms: []
required_styleguides: []
required_toolguides: []
required_procedures: []
required_agent_profiles: []
required_mission_step_contracts: []
governance_policies: []
activations: []
`

// DRGFragmentStub is the scaffolded drg/fragment.yaml.
const DRGFragmentStub = `# pydantic_model: charter.drg.OrgDRGFragment
# expect: valid
pack_name: TODO replace with your pack name
source_kind: local_path
source_ref: .
layer_index: 1
provenance_marker: org
nodes: []
edges: []
`

// OrgPackReadmeStub is the scaffolded README.md.
const OrgPackReadmeStub = "# Org Charter Pack\n\n> Scaffolded by `spec-kitty charter org init`.\n\n## Contents\n\n" +
	"- `org-charter.yaml` — organisation-level governance policy\n" +
	"- `drg/fragment.yaml` — DRG extension fragment declaring org-tier nodes\n" +
	"- `presets/` — activation presets (`spec-kitty charter activate --preset <name>`)\n" +
	"- Additional artifact subdirectories (e.g. `directives/`, `tactics/`) may\n  be added alongside the `org-charter.yaml`.\n\n" +
	"## Validation\n\n```bash\nspec-kitty charter org validate .\n```\n"

// ExamplePreset is the scaffolded presets/starter.yaml.
const ExamplePreset = `# Example activation preset, scaffolded by ` + "`spec-kitty charter org init`" + `.
# Apply it with: spec-kitty charter activate --preset starter
#
# A preset replaces the activation keys it carries. A key left out keeps its
# kind unrestricted (every artifact effective); an empty list activates none.
# To curate a kind, add its key, for example:
#
#   activated_directives:
#     - 010-specification-fidelity-requirement
#   activated_tactics:
#     - acceptance-test-first
name: starter
description: Starter preset for this pack; activates the software-dev mission type.
mission_type_activations:
  - software-dev
`

// WriteOrgScaffold writes the minimal four-file org pack.
func WriteOrgScaffold(packPath string) error {
	if err := os.MkdirAll(filepath.Join(packPath, "drg"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(packPath, "presets"), 0o755); err != nil {
		return err
	}
	for rel, body := range map[string]string{"org-charter.yaml": OrgCharterStub, "drg/fragment.yaml": DRGFragmentStub,
		"presets/starter.yaml": ExamplePreset, "README.md": OrgPackReadmeStub} {
		if err := os.WriteFile(filepath.Join(packPath, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// RenderError is a template-render PipelineError.
type RenderError struct{ RuleID, Message string }

func (e *RenderError) Error() string { return e.Message }

var orgNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

func validateOrgName(v string) *RenderError {
	if strings.Contains(v, "TODO") || v == "ORG_NAME" || v == "{{ORG_NAME}}" {
		return &RenderError{"org_name.placeholder", fmt.Sprintf("ORG_NAME rejected as placeholder (org_name.placeholder): %s", dct.PyRepr(v))}
	}
	if n := utf8.RuneCountInString(v); n < 2 || n > 64 {
		return &RenderError{"org_name.length", fmt.Sprintf("ORG_NAME length must be 2-64 (org_name.length): %s (len=%d)", dct.PyRepr(v), n)}
	}
	if strings.EqualFold(v, "doctrine-org") {
		return &RenderError{"org_name.reserved", fmt.Sprintf("ORG_NAME must not equal reserved base pack name 'doctrine-org' (org_name.reserved): %s", dct.PyRepr(v))}
	}
	if !orgNameRe.MatchString(v) {
		return &RenderError{"org_name.format", fmt.Sprintf("ORG_NAME must be lowercase kebab-case (org_name.format): %s", dct.PyRepr(v))}
	}
	return nil
}

func validateLocalPath(v string) *RenderError {
	if strings.TrimSpace(v) == "" {
		return &RenderError{"local_path.empty", fmt.Sprintf("LOCAL_PATH must be a non-empty path string (local_path.empty): %s", dct.PyRepr(v))}
	}
	if strings.Contains(v, "TODO") || v == "LOCAL_PATH" || v == "{{LOCAL_PATH}}" {
		return &RenderError{"local_path.placeholder", fmt.Sprintf("LOCAL_PATH rejected as placeholder (local_path.placeholder): %s", dct.PyRepr(v))}
	}
	return nil
}

var httpsAtRef = regexp.MustCompile(`^(https://.+?)@([^@]+)$`)

func classifyLocation(loc string) string {
	for _, p := range []string{"https://", "http://", "ssh://", "git@"} {
		if strings.HasPrefix(loc, p) {
			return "git"
		}
	}
	if u, err := url.Parse(loc); err == nil {
		switch u.Scheme {
		case "https", "http", "ssh", "git":
			return "git"
		}
	}
	return "local"
}

func parseTemplateRef(t string) (loc, ref, kind string) {
	s := strings.TrimSpace(t)
	if strings.Contains(s, "#") {
		l, r, _ := strings.Cut(s, "#")
		return l, strings.TrimSpace(r), classifyLocation(l)
	}
	if m := httpsAtRef.FindStringSubmatch(s); m != nil {
		return m[1], m[2], "git"
	}
	if strings.HasPrefix(s, "ssh://") {
		authority, p, sep := strings.Cut(strings.TrimPrefix(s, "ssh://"), "/")
		if sep && strings.Contains(p, "@") {
			i := strings.LastIndex(p, "@")
			if repo, r := p[:i], p[i+1:]; repo != "" && r != "" {
				return "ssh://" + authority + "/" + repo, r, "git"
			}
		}
		return s, "", "git"
	}
	return s, "", classifyLocation(s)
}

func mergeBranchRefs(enc, opt string, optSet bool) (string, *RenderError) {
	for _, r := range []string{enc, opt} {
		if strings.HasPrefix(strings.TrimLeft(r, " \t"), "-") {
			return "", &RenderError{"branch.invalid", "Git ref must not be an option or contain control characters (branch.invalid)."}
		}
		for _, c := range r {
			if c < 32 || c == 127 {
				return "", &RenderError{"branch.invalid", "Git ref must not be an option or contain control characters (branch.invalid)."}
			}
		}
	}
	o, e := strings.TrimSpace(opt), strings.TrimSpace(enc)
	if o != "" && e != "" && o != e {
		return "", &RenderError{"branch.conflict", fmt.Sprintf("Conflicting git refs (branch.conflict): --branch=%s vs TEMPLATE-encoded=%s", dct.PyRepr(o), dct.PyRepr(e))}
	}
	if o != "" {
		return o, nil
	}
	return e, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// fnmatch is Python fnmatch.fnmatch (case-sensitive on POSIX).
func fnmatch(name, pat string) bool {
	ok, err := path.Match(pat, name)
	if err == nil && ok {
		return true
	}
	// path.Match does not let '*' cross '/', fnmatch does.
	re := "^" + fnmatchToRegex(pat) + "$"
	m, _ := regexp.MatchString(re, name)
	return m
}

func fnmatchToRegex(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := strings.IndexByte(p[i+1:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			cls := p[i+1 : i+1+j]
			if strings.HasPrefix(cls, "!") {
				cls = "^" + cls[1:]
			}
			b.WriteString("[" + cls + "]")
			i += j + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

func ignorePatternMatches(pat, rel string) bool {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return false
	}
	if strings.HasSuffix(pat, "/") {
		base := strings.TrimRight(pat, "/")
		if rel == base || strings.HasPrefix(rel, base+"/") {
			return true
		}
		return fnmatch(rel, base) || fnmatch(rel, pat+"*")
	}
	if fnmatch(rel, pat) {
		return true
	}
	if !strings.Contains(strings.TrimRight(pat, "/"), "/") {
		return fnmatch(path.Base(rel), pat)
	}
	return false
}

func loadIgnoreRules(root string) ([]string, *RenderError) {
	pats := []string{".git", ".git/", ".templateignore"}
	p := filepath.Join(root, ".templateignore")
	if st, err := os.Lstat(p); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, &RenderError{"ignore_rules.templateignore_symlink", "Template render failed (ignore_rules.templateignore_symlink): '.templateignore' must be a regular file, not a symlink"}
		}
		if st.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err == nil {
				if !utf8.Valid(b) {
					return nil, &RenderError{"ignore_rules.templateignore_decode", "Template render failed (ignore_rules.templateignore_decode): '.templateignore' is not valid UTF-8"}
				}
				for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
					l = strings.TrimSpace(l)
					if l == "" || strings.HasPrefix(l, "#") {
						continue
					}
					pats = append(pats, l)
				}
			}
		}
	}
	return pats, nil
}

func ignored(pats []string, rel string) bool {
	rel = strings.TrimPrefix(rel, "./")
	for _, p := range pats {
		if ignorePatternMatches(p, rel) {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, _ := in.Stat()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, st.ModTime(), st.ModTime())
}

func copyTemplateTree(src, dst string, pats []string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == src {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		for _, part := range strings.Split(rel, "/") {
			if part == ".git" {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		parts := strings.Split(rel, "/")
		skip := ignored(pats, rel)
		for i := 1; i < len(parts) && !skip; i++ {
			parent := strings.Join(parts[:i], "/")
			skip = ignored(pats, parent) || ignored(pats, parent+"/")
		}
		if skip {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}

func substituteTokens(dst, orgName, localPath string) *RenderError {
	var offenders []string
	var files []string
	_ = filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dst {
			return nil
		}
		rel, _ := filepath.Rel(dst, p)
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.Contains(part, "{{ORG_NAME}}") || strings.Contains(part, "{{LOCAL_PATH}}") {
				offenders = append(offenders, filepath.ToSlash(rel))
				break
			}
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if len(offenders) > 0 {
		sample := offenders
		more := ""
		if len(offenders) > 5 {
			sample = offenders[:5]
			more = fmt.Sprintf(" (+%d more)", len(offenders)-5)
		}
		return &RenderError{"substitute.path_token", fmt.Sprintf("Template path tokens are not allowed (substitute.path_token) in: %s%s", strings.Join(sample, ", "), more)}
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil || !utf8.Valid(b) {
			continue
		}
		text := string(b)
		rep := strings.ReplaceAll(strings.ReplaceAll(text, "{{ORG_NAME}}", orgName), "{{LOCAL_PATH}}", localPath)
		if strings.Contains(rep, "{{ORG_NAME}}") || strings.Contains(rep, "{{LOCAL_PATH}}") {
			return &RenderError{"substitute.leftover_tokens", "Unfilled template tokens remain (substitute.leftover_tokens) in: " + filepath.Base(f)}
		}
		if rep != text {
			st, _ := os.Stat(f)
			_ = os.WriteFile(f, []byte(rep), st.Mode().Perm())
		}
	}
	return nil
}

func gitClone(urlStr, ref, target string) error {
	cmd := exec.Command("git", "clone", "--quiet", urlStr, target)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "git clone failed"
		}
		return fmt.Errorf("%s", msg)
	}
	if ref != "" {
		if out, err := exec.Command("git", "-C", target, "checkout", "--quiet", ref).CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = "git checkout failed"
			}
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

// RenderOrgPack is render_org_pack.
func RenderOrgPack(packPath, template, orgName string, localPath *string, branch string, branchSet, force bool) *RenderError {
	if err := validateOrgName(orgName); err != nil {
		return err
	}
	lp := "pack"
	if localPath != nil {
		lp = *localPath
	}
	if err := validateLocalPath(lp); err != nil {
		return err
	}
	abs, _ := filepath.Abs(packPath)
	if st, err := os.Lstat(abs); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return &RenderError{"pack_path.symlink", "Template destination must not be a symlink (pack_path.symlink)."}
		}
		if !force {
			return &RenderError{"pack_path.exists", fmt.Sprintf("Target directory already exists (pack_path.exists): %s. Pass --force to overwrite.", abs)}
		}
	}
	if s := strings.TrimSpace(template); strings.HasPrefix(strings.ToLower(s), "https://") {
		if strings.Contains(strings.SplitN(s[len("https://"):], "/", 2)[0], "@") {
			return &RenderError{"template.userinfo_rejected", "TEMPLATE HTTPS URLs must not contain credentials (template.userinfo_rejected); use SSH or a credential helper"}
		}
	}
	loc, encRef, kind := parseTemplateRef(template)
	ref, rerr := mergeBranchRefs(encRef, branch, branchSet)
	if rerr != nil {
		return rerr
	}
	rejected := strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "git://")
	if u, err := url.Parse(loc); err == nil && (u.Scheme == "http" || u.Scheme == "git") {
		rejected = true
	}
	if rejected {
		return &RenderError{"template.scheme_rejected", fmt.Sprintf("TEMPLATE scheme rejected (template.scheme_rejected): only https://, ssh://, git@, and local paths are allowed (got %s)", dct.PyRepr(loc))}
	}
	var root string
	cleanup := false
	if kind == "local" {
		l := loc
		if strings.HasPrefix(l, "~") {
			l = pyExpandUser(l)
		}
		root = pyResolve(l)
		st, err := os.Stat(root)
		if err != nil {
			return &RenderError{"template.missing", "TEMPLATE path does not exist (template.missing): " + root}
		}
		if !st.IsDir() {
			return &RenderError{"template.not_directory", "TEMPLATE path is not a directory (template.not_directory): " + root}
		}
	} else {
		tmp, err := os.MkdirTemp("", "spec-kitty-template-")
		if err != nil {
			return &RenderError{"template.git_fetch", "TEMPLATE git resolve failed (template.git_fetch): " + err.Error()}
		}
		clone := filepath.Join(tmp, "clone")
		if err := gitClone(loc, ref, clone); err != nil {
			os.RemoveAll(tmp)
			return &RenderError{"template.git_fetch", "TEMPLATE git resolve failed (template.git_fetch): " + err.Error()}
		}
		root, cleanup = clone, true
		defer os.RemoveAll(tmp)
	}
	_ = cleanup
	rp := pyResolve(abs)
	within := func(a, b string) bool {
		r, err := filepath.Rel(b, a)
		return err == nil && r != ".." && !strings.HasPrefix(r, "../")
	}
	if within(rp, root) || within(root, rp) {
		return &RenderError{"pack_path.overlap", "Template source and destination must not contain one another (pack_path.overlap)."}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return &RenderError{"pipeline.copy", "Template render failed (pipeline.copy): " + err.Error()}
	}
	staging, err := os.MkdirTemp(filepath.Dir(abs), ".spec-kitty-render-")
	if err != nil {
		return &RenderError{"pipeline.copy", "Template render failed (pipeline.copy): " + err.Error()}
	}
	defer os.RemoveAll(staging)
	pats, ierr := loadIgnoreRules(root)
	if ierr != nil {
		return ierr
	}
	if err := copyTemplateTree(root, staging, pats); err != nil {
		return &RenderError{"pipeline.copy", "Template render failed (pipeline.copy): " + err.Error()}
	}
	if serr := substituteTokens(staging, orgName, lp); serr != nil {
		return serr
	}
	if _, err := os.Lstat(abs); err == nil {
		if !force {
			return &RenderError{"pipeline.dest_exists", "destination exists without force (pipeline.dest_exists): " + abs}
		}
		backup := filepath.Join(filepath.Dir(abs), filepath.Base(abs)+".bak-"+randHex(16))
		if err := os.Rename(abs, backup); err != nil {
			return &RenderError{"pipeline.force_backup", "Failed to move aside existing pack (pipeline.force_backup): " + err.Error()}
		}
		if err := os.Rename(staging, abs); err != nil {
			_ = os.Rename(backup, abs)
			return &RenderError{"pipeline.force_swap", fmt.Sprintf("Failed to install staging over pack (pipeline.force_swap): %v. Original pack restored.", err)}
		}
		os.RemoveAll(backup)
		return nil
	}
	if err := os.Rename(staging, abs); err != nil {
		return &RenderError{"pipeline.copy", "Template render failed (pipeline.copy): " + err.Error()}
	}
	return nil
}
