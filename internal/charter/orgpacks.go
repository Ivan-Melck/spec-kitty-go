package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// This file ports charter.offering.drg.org_pack_config: the
// charter_packs.org.packs registry of .kittify/config.yaml.

// OrgPackConfig is one configured org charter pack.
type OrgPackConfig struct {
	Name       string
	PackID     *string
	LocalPath  string // as written (unexpanded)
	Subdir     *string
	SourceType *string // git | https | artifactory | api
	URL        *string
	Ref        *string
}

// PackRegistry is the ordered pack list.
type PackRegistry struct {
	Packs []OrgPackConfig
	// Warning is the UserWarning upstream emits for an unreadable config or
	// an invalid org-pack block (the registry is empty then).
	Warning string
}

// Names returns the pack names in declaration order.
func (r *PackRegistry) Names() []string {
	out := make([]string, len(r.Packs))
	for i, p := range r.Packs {
		out[i] = p.Name
	}
	return out
}

// Get returns the named pack (nil when absent).
func (r *PackRegistry) Get(name string) *OrgPackConfig {
	for i := range r.Packs {
		if r.Packs[i].Name == name {
			return &r.Packs[i]
		}
	}
	return nil
}

// OrgPackEnvVarUnsetError is raised for an unset/empty env token in
// local_path.
type OrgPackEnvVarUnsetError struct{ Pack, Raw, Token string }

func (e *OrgPackEnvVarUnsetError) Error() string {
	return fmt.Sprintf("Org pack %s local_path %s references environment variable %s which is unset or empty (or is itself a nested ${VAR} token — expansion is not recursive). Set the variable directly, or update local_path in .kittify/config.yaml.",
		dct.PyRepr(e.Pack), dct.PyRepr(e.Raw), dct.PyRepr(e.Token))
}

// OrgPackSubdirEscapeError is a containment violation.
type OrgPackSubdirEscapeError struct{ Msg string }

func (e *OrgPackSubdirEscapeError) Error() string { return e.Msg }

var envTokenRe = regexp.MustCompile(`\$\{[^}]+\}|\$[A-Za-z_][A-Za-z0-9_]*`)

func tokenVar(tok string) string {
	if strings.HasPrefix(tok, "${") {
		return tok[2 : len(tok)-1]
	}
	return tok[1:]
}

// pyExpandVars is os.path.expandvars (unknown variables left verbatim).
func pyExpandVars(s string) string {
	if !strings.Contains(s, "$") {
		return s
	}
	return envTokenRe.ReplaceAllStringFunc(s, func(tok string) string {
		if v, ok := os.LookupEnv(tokenVar(tok)); ok {
			return v
		}
		return tok
	})
}

// pyExpandUser is os.path.expanduser for "~" and "~/..." (and "~user").
func pyExpandUser(s string) string {
	if !strings.HasPrefix(s, "~") {
		return s
	}
	i := strings.Index(s, "/")
	if i < 0 {
		i = len(s)
	}
	if i == 1 {
		return strings.TrimSuffix(project.UserHome(), "/") + s[1:]
	}
	return s
}

// LocalPathRoot is OrgPackConfig.local_path_root.
func (p *OrgPackConfig) LocalPathRoot(repoRoot string) (string, error) {
	raw := p.LocalPath
	exp := pyExpandUser(pyExpandVars(raw))
	if tok := envTokenRe.FindString(exp); tok != "" {
		return "", &OrgPackEnvVarUnsetError{p.Name, raw, tok}
	}
	for _, tok := range envTokenRe.FindAllString(raw, -1) {
		if v, ok := os.LookupEnv(tokenVar(tok)); ok && v == "" {
			return "", &OrgPackEnvVarUnsetError{p.Name, raw, tok}
		}
	}
	if filepath.IsAbs(exp) {
		return pyPath(exp), nil
	}
	return pyPath(repoRoot + "/" + exp), nil
}

// pyPath is pathlib's lexical normalisation: repeated separators and "."
// components are dropped, ".." is kept.
func pyPath(p string) string {
	abs := strings.HasPrefix(p, "/")
	var parts []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." {
			continue
		}
		parts = append(parts, c)
	}
	out := strings.Join(parts, "/")
	if abs {
		return "/" + out
	}
	if out == "" {
		return "."
	}
	return out
}

// pyResolve is Path.resolve(strict=False).
func pyResolve(p string) string {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		wd, _ := os.Getwd()
		p = filepath.Join(wd, p)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir, base := filepath.Split(p)
	if dir == "" || dir == p || dir == "/" {
		return p
	}
	return filepath.Join(pyResolve(strings.TrimSuffix(dir, "/")), base)
}

// ResolveWithinRoot is resolve_relative_path_within_root.
func ResolveWithinRoot(root, rel string) (string, error) {
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || (len(rel) >= 2 && rel[1] == ':') {
		return "", &OrgPackSubdirEscapeError{fmt.Sprintf("path %s must be a relative path, got an absolute path", dct.PyRepr(rel))}
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", &OrgPackSubdirEscapeError{fmt.Sprintf("path %s must not contain '..' components", dct.PyRepr(rel))}
		}
	}
	rr := pyResolve(root)
	cand := pyResolve(filepath.Join(root, rel))
	if r, err := filepath.Rel(rr, cand); err != nil || r == ".." || strings.HasPrefix(r, "../") {
		return "", &OrgPackSubdirEscapeError{fmt.Sprintf("path %s resolves outside root %s: %s", dct.PyRepr(rel), rr, cand)}
	}
	return cand, nil
}

// EffectiveRoot is OrgPackConfig.effective_root.
func (p *OrgPackConfig) EffectiveRoot(repoRoot string) (string, error) {
	root, err := p.LocalPathRoot(repoRoot)
	if err != nil {
		return "", err
	}
	if p.Subdir == nil {
		return pyResolve(root), nil
	}
	return ResolveWithinRoot(root, *p.Subdir)
}

var ulidRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{26}$`)

// LoadPackRegistry is load_pack_registry (lenient): an unreadable config or
// an invalid org block yields an empty registry plus Warning.
func LoadPackRegistry(repoRoot string) *PackRegistry {
	reg := &PackRegistry{}
	cfg, err := dct.LoadConfig(repoRoot)
	if err != nil {
		reg.Warning = "Failed to read .kittify/config.yaml; org doctrine disabled: " + err.Error()
		return reg
	}
	cp, _ := cfg.Get("charter_packs")
	cpo, ok := cp.(*jsonx.Object)
	if !ok {
		return reg
	}
	org, _ := cpo.Get("org")
	orgo, ok := org.(*jsonx.Object)
	if !ok {
		return reg
	}
	rawPacks, hasPacks := orgo.Get("packs")
	if !hasPacks {
		if _, ok := orgo.Get("local_path"); ok {
			reg.Warning = "Invalid org-pack config; ignoring org layer: `charter_packs.org.local_path` is not a pack declaration; declare the pack as a named entry of `charter_packs.org.packs[]` (name, local_path)"
		}
		return reg
	}
	packs, err := parsePackList(rawPacks)
	if err != nil {
		reg.Warning = "Invalid org-pack config; ignoring org layer: " + err.Error()
		return reg
	}
	reg.Packs = packs
	return reg
}

func optStrField(o *jsonx.Object, key string) (*string, error) {
	v, ok := o.Get(key)
	if !ok || v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%s: Input should be a valid string", key)
	}
	return &s, nil
}

func parsePackList(raw any) ([]OrgPackConfig, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("1 validation error for PackRegistry\npacks\n  Input should be a valid list")
	}
	var out []OrgPackConfig
	known := map[string]bool{"name": true, "pack_id": true, "local_path": true, "subdir": true, "source_type": true, "url": true, "ref": true}
	for i, item := range list {
		o, ok := item.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d\n  Input should be a valid dictionary or instance of OrgPackConfig", i)
		}
		for _, kv := range o.Pairs() {
			if !known[kv.Key] {
				return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d.%s\n  Extra inputs are not permitted", i, kv.Key)
			}
		}
		nv, _ := o.Get("name")
		name, ok := nv.(string)
		if !ok {
			return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d.name\n  Field required", i)
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d.name\n  Value error, pack name must be a non-empty string", i)
		}
		lv, ok := o.Get("local_path")
		if !ok || lv == nil {
			return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d.local_path\n  Field required", i)
		}
		p := OrgPackConfig{Name: name, LocalPath: fmt.Sprint(lv)}
		var err error
		if p.PackID, err = optStrField(o, "pack_id"); err != nil {
			return nil, err
		}
		if p.PackID != nil {
			if !ulidRe.MatchString(*p.PackID) {
				return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d.pack_id\n  Value error, pack_id must be a valid 26-char ULID, got %s", i, dct.PyRepr(*p.PackID))
			}
			u := strings.ToUpper(*p.PackID)
			p.PackID = &u
		}
		if sv, ok := o.Get("subdir"); ok && sv != nil {
			s := strings.TrimSpace(fmt.Sprint(sv))
			if s != "" && s != "." {
				if strings.HasPrefix(s, "/") {
					return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d.subdir\n  Value error, subdir must be a relative path, got absolute POSIX path: %s", i, dct.PyRepr(s))
				}
				for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
					if part == ".." {
						return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d.subdir\n  Value error, subdir must not contain '..' components, got: %s", i, dct.PyRepr(s))
					}
				}
				p.Subdir = &s
			}
		}
		if p.SourceType, err = optStrField(o, "source_type"); err != nil {
			return nil, err
		}
		if p.SourceType != nil {
			switch *p.SourceType {
			case "git", "https", "artifactory", "api":
			default:
				return nil, fmt.Errorf("1 validation error for PackRegistry\npacks.%d.source_type\n  Input should be 'git', 'https', 'artifactory' or 'api'", i)
			}
		}
		if p.URL, err = optStrField(o, "url"); err != nil {
			return nil, err
		}
		if p.Ref, err = optStrField(o, "ref"); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	seen := map[string]int{}
	for _, p := range out {
		seen[p.Name]++
	}
	var dupes []string
	for n, c := range seen {
		if c > 1 {
			dupes = append(dupes, n)
		}
	}
	if len(dupes) > 0 {
		sort.Strings(dupes)
		q := make([]string, len(dupes))
		for i, d := range dupes {
			q[i] = dct.PyRepr(d)
		}
		return nil, fmt.Errorf("1 validation error for PackRegistry\n  Value error, Duplicate pack names in charter_packs.org.packs: [%s]", strings.Join(q, ", "))
	}
	return out, nil
}

// ResolvePackChain is resolve_pack_chain(strict=False): the effective roots
// of the configured packs that exist on disk, in declaration order.
func ResolvePackChain(repoRoot string) []string {
	var out []string
	for _, p := range LoadPackRegistry(repoRoot).Packs {
		r, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			continue
		}
		if _, err := os.Stat(r); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// ProjectPackRootIfExists is the project pack root (.kittify/charter-packs)
// when it exists, else "".
func ProjectPackRootIfExists(repoRoot string) string {
	p := filepath.Join(repoRoot, filepath.FromSlash(dct.ProjectPackRoot))
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}
