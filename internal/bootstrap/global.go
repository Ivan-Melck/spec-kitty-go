package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// ManagedMissionDirs are the package-managed mission trees copied into
// ~/.kittify/missions (merge.MANAGED_DIRS; audit/refactor/scripts are absent).
var ManagedMissionDirs = []string{"software-dev", "research", "documentation", "plan"}

const missionsAssetRoot = "packs/built-in/missions"

// KittifyHome is ~/.kittify (SPEC_KITTY_HOME override).
func KittifyHome() string { return project.GlobalKittify() }

func runtimeStateRoot() string { return project.GlobalSpecKitty() }

// anchorFor is commonpath(HOME, *paths).parent.
func anchorFor(paths ...string) string {
	all := append([]string{project.UserHome()}, paths...)
	common := filepath.Clean(all[0])
	for _, p := range all[1:] {
		common = commonPath(common, filepath.Clean(p))
	}
	return filepath.Dir(common)
}

func commonPath(a, b string) string {
	as := strings.Split(a, string(filepath.Separator))
	bs := strings.Split(b, string(filepath.Separator))
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] {
		n++
	}
	if n == 0 {
		return string(filepath.Separator)
	}
	r := strings.Join(as[:n], string(filepath.Separator))
	if r == "" {
		return string(filepath.Separator)
	}
	return r
}

// AssetError is a terminal startup-asset failure.
type AssetError struct {
	Owner string
	Err   error
}

func (e *AssetError) Error() string {
	hint := "re-run the command; if it persists, run `spec-kitty doctor --help` to find the right diagnostic"
	if IsUnwritableHomeError(e.Err) {
		hint = UnwritableHomeHint
	}
	return fmt.Sprintf("%s: %s; %s", e.Owner, pyjson.OSErrorString(e.Err), hint)
}

func (e *AssetError) Unwrap() error { return e.Err }

const UnwritableHomeHint = "a home-folder location could not be written or read (permission denied or a read-only file system) -- fix its permissions, or run spec-kitty once where the home folder is writable, to install the runtime assets and the global agent commands and skills"

func IsUnwritableHomeError(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EROFS)
}

type StartupOwner struct {
	Label   string
	Prepare func() error
}

// PrepareStartupAssets degrades only permission and read-only-home failures.
// Direct owner calls (init, migrate and repair) remain terminal.
func PrepareStartupAssets(owners []StartupOwner) (string, error) {
	var labels []string
	first := ""
	for _, owner := range owners {
		if err := owner.Prepare(); err != nil {
			if !IsUnwritableHomeError(err) {
				return "", err
			}
			labels = append(labels, owner.Label)
			if first == "" {
				if _, ok := err.(*AssetError); ok {
					first = err.Error()
				} else {
					first = pyjson.OSErrorString(err) + "; " + UnwritableHomeHint
				}
			}
		}
	}
	if len(labels) == 0 {
		return "", nil
	}
	joined := labels[0]
	if len(labels) > 1 {
		joined = strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1]
	}
	return fmt.Sprintf("spec-kitty could not update its %s; the command continues, but the runtime mission templates and the global agent commands and skills may be missing or out of date (%s).", joined, first), nil
}

// EnsureRuntime populates ~/.kittify/missions (ensure_runtime).
func EnsureRuntime() error {
	p, err := AssessRuntime()
	if err != nil {
		return &AssetError{"runtime_bootstrap", err}
	}
	if err := p.apply(); err != nil {
		return &AssetError{"runtime_bootstrap", err}
	}
	return nil
}

// AssessRuntime plans the runtime_bootstrap owner without writing.
func AssessRuntime() (*prep, error) {
	assets.WarnPackRoot(assets.MissionAssetRoot)
	home := KittifyHome()
	p, err := newPrep("runtime_bootstrap", anchorFor(home), filepath.Join(home, "cache"), ".update.lock")
	if err != nil {
		return nil, err
	}
	for _, m := range ManagedMissionDirs {
		src := missionsAssetRoot + "/" + m
		if !assets.Exists(src) {
			continue
		}
		dst := filepath.Join(home, "missions", m)
		if err := p.assetTree(src, dst, false, true); err != nil {
			return nil, err
		}
		if err := p.pruneMissing(dst); err != nil {
			return nil, err
		}
	}
	if err := p.finish(filepath.Join(home, "cache", "version.lock"), app.Version); err != nil {
		return nil, err
	}
	return p, nil
}

// assetTree mirrors AssetPreparation.tree for an embedded source tree.
func (p *prep) assetTree(src, dst string, readonly, managed bool) error {
	st, err := p.observe(dst)
	if err != nil {
		return err
	}
	if st.Kind != "directory" && st.Kind != "absent" {
		p.preserve(dst, "Unproven asset tree replacement", false)
		return nil
	}
	if err := p.asset(dst, nil, 0o755, managed, false); err != nil {
		return err
	}
	ents, err := assets.List(src)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(ents))
	dirs := map[string]bool{}
	for _, e := range ents {
		names = append(names, e.Name())
		dirs[e.Name()] = e.IsDir()
	}
	sort.Strings(names)
	for _, n := range names {
		target := filepath.Join(dst, n)
		if dirs[n] {
			if err := p.assetTree(src+"/"+n, target, readonly, managed); err != nil {
				return err
			}
			continue
		}
		b, err := assets.ReadFile(src + "/" + n)
		if err != nil {
			return err
		}
		mode := 0o644
		if readonly {
			mode &^= 0o222
		}
		if err := p.asset(target, b, mode, managed, false); err != nil {
			return err
		}
	}
	return nil
}

// EnsureGlobalSkills installs doctrine skills into the user-global roots.
func EnsureGlobalSkills() error {
	p, err := AssessGlobalSkills()
	if err != nil {
		return &AssetError{"global_skills", err}
	}
	if err := p.apply(); err != nil {
		return &AssetError{"global_skills", err}
	}
	return nil
}

// AssessGlobalSkills plans the global_skills owner.
func AssessGlobalSkills() (*prep, error) {
	home := KittifyHome()
	roots := agents.UniqueGlobalSkillRoots()
	p, err := newPrep("global_skills", anchorFor(append([]string{home}, roots...)...), filepath.Join(home, "cache"), ".agent-skills.lock")
	if err != nil {
		return nil, err
	}
	skills := agents.Skills()
	if len(skills) == 0 {
		return nil, fmt.Errorf("Required canonical skill registry is empty")
	}
	canonical := map[string]bool{}
	for _, s := range skills {
		canonical[s.Name] = true
	}
	for _, root := range roots {
		st, err := p.observe(root)
		if err != nil {
			return nil, err
		}
		if st.Kind != "directory" && st.Kind != "absent" {
			p.preserve(root, "Unproven global skill root replacement", false)
			continue
		}
		for _, s := range skills {
			if err := p.skillTree(s, "", filepath.Join(root, s.Name)); err != nil {
				return nil, err
			}
			if err := p.pruneMissing(filepath.Join(root, s.Name)); err != nil {
				return nil, err
			}
		}
		if st.Kind == "directory" {
			ents, _ := os.ReadDir(root)
			for _, e := range ents {
				if canonical[e.Name()] {
					continue
				}
				if agents.RetiredSkillNames[e.Name()] {
					if _, err := p.retire(filepath.Join(root, e.Name())); err != nil {
						return nil, err
					}
				} else {
					p.preserve(filepath.Join(root, e.Name()), "Unproven custom skill; preserve content and links", false)
				}
			}
		}
	}
	if err := p.finish(filepath.Join(home, "cache", "agent-skills.lock"), app.Version); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *prep) skillTree(s agents.Skill, rel, dst string) error {
	st, err := p.observe(dst)
	if err != nil {
		return err
	}
	if st.Kind != "directory" && st.Kind != "absent" {
		p.preserve(dst, "Unproven canonical skill path replacement", false)
		return nil
	}
	if err := p.asset(dst, nil, 0o755, false, false); err != nil {
		return err
	}
	src := s.Dir
	if rel != "" {
		src += "/" + rel
	}
	ents, err := assets.List(src)
	if err != nil {
		return err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	for _, e := range ents {
		childRel := e.Name()
		if rel != "" {
			childRel = rel + "/" + e.Name()
		}
		target := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := p.skillTree(s, childRel, target); err != nil {
				return err
			}
			continue
		}
		b, err := assets.ReadFile(path.Join(s.Dir, childRel))
		if err != nil {
			return err
		}
		if childRel == "SKILL.md" {
			skillRoot, err := filepath.Rel(project.UserHome(), filepath.Dir(dst))
			if err != nil {
				return err
			}
			projected, err := agents.ProjectSkillForRoot(string(b), filepath.ToSlash(skillRoot), s.Name)
			if err != nil {
				return err
			}
			b = []byte(projected)
		}
		if err := p.asset(target, b, 0o444, false, false); err != nil {
			return err
		}
	}
	return nil
}

const (
	commandsVersionFile   = "agent-commands.lock"
	commandsLockFile      = ".agent-commands.lock"
	commandsFreshnessFile = "agent-commands-freshness.lock"
)

// renderPipelineID identifies the Go rendering code for the freshness stamp
// (upstream hashes its Python renderer sources; any change here must bump it).
const renderPipelineID = "go-kitty3/agents.RenderAgentCommands/v1"

// TemplateSourceSignature hashes every command template file plus the
// rendering pipeline identity.
func TemplateSourceSignature() (string, error) {
	files, err := assets.Walk(agents.CommandTemplatesDir)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range files {
		rel := strings.TrimPrefix(f, agents.CommandTemplatesDir+"/")
		b, err := assets.ReadFile(f)
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	p := sha256.New()
	p.Write([]byte(renderPipelineID))
	p.Write([]byte{0})
	keys := agents.SortedCommandAgents()
	cfg := &jsonx.Object{}
	for _, k := range keys {
		c := agents.CommandConfigs[k]
		cfg.Set(k, jsonx.NewObject("arg_format", c.ArgFormat, "dir", c.Dir, "ext", c.Ext))
	}
	p.Write([]byte(jsonx.Dumps(cfg, jsonx.Line)))
	h.Write(p.Sum(nil))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func freshnessPayload(sig string, selections ...[]string) []byte {
	keys := agents.SortedCommandAgents()
	if len(selections) > 0 {
		keys = selections[0]
	}
	covered := map[string]bool{}
	for _, key := range keys {
		covered[key] = true
	}
	if b, err := os.ReadFile(filepath.Join(KittifyHome(), "cache", commandsFreshnessFile)); err == nil {
		if old, err := jsonx.DecodeObject(b); err == nil && old.GetString("cli_version") == app.Version && old.GetString("template_source_signature") == sig {
			if schema, ok := old.GetInt("schema"); ok && schema == 2 {
				for _, raw := range old.GetList("healthy_keys") {
					if key, ok := raw.(string); ok {
						covered[key] = true
					}
				}
			}
		}
	}
	keys = nil
	for key := range covered {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	l := make([]any, len(keys))
	for i, k := range keys {
		l[i] = k
	}
	return []byte(jsonx.Dumps(jsonx.NewObject("schema", 2, "cli_version", app.Version, "template_source_signature", sig, "healthy_keys", l, "retired_surfaces_cleaned", true), jsonx.Options{Indent: -1, SortKeys: true}))
}

// GlobalCommandsHealthy reports whether every command agent directory holds
// exactly the canonical file set with this version's marker.
func GlobalCommandsHealthy(keys []string) bool {
	for _, k := range keys {
		dir := agents.GlobalCommandDir(k)
		ents, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		expected := map[string]bool{}
		for _, c := range agents.ConsumerCommands {
			expected[agents.CommandFilename(c, k)] = true
		}
		n := 0
		for _, e := range ents {
			if !strings.HasPrefix(e.Name(), "spec-kitty.") {
				continue
			}
			if !e.Type().IsRegular() || !expected[e.Name()] {
				return false
			}
			n++
		}
		if n != len(expected) {
			return false
		}
		for name := range expected {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || !agents.HasCurrentVersionMarker(b, app.Version) {
				return false
			}
		}
	}
	return true
}

func freshCommands(home string, selections ...[]string) bool {
	sig, err := TemplateSourceSignature()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(home, "cache", commandsFreshnessFile))
	keys := agents.SortedCommandAgents()
	if len(selections) > 0 {
		keys = selections[0]
	}
	if err != nil || string(b) != string(freshnessPayload(sig, keys)) {
		// tolerate key-order/format differences by decoding
		if err != nil {
			return false
		}
		o, derr := jsonx.DecodeObject(b)
		if derr != nil || o.GetString("cli_version") != app.Version || o.GetString("template_source_signature") != sig {
			return false
		}
		schema, ok := o.GetInt("schema")
		if !ok || schema != 2 {
			return false
		}
		covered := map[string]bool{}
		for _, raw := range o.GetList("healthy_keys") {
			key, ok := raw.(string)
			if !ok {
				return false
			}
			covered[key] = true
		}
		for _, key := range keys {
			if !covered[key] {
				return false
			}
		}
	}
	return GlobalCommandsHealthy(keys)
}

// UnmigratedCommands lists canonical command files preserved by the last
// assessment (for the upstream warning).
func (p *prep) unmigrated(canonical map[string]bool) []string {
	var out []string
	for _, d := range p.disp {
		if strings.HasPrefix(d.Path, filepath.Join(project.UserHome(), ".agent", "workflows")+string(filepath.Separator)) {
			continue
		}
		if (d.State == "preserve" || d.State == "consent_required") && canonical[path.Base(d.Path)] {
			out = append(out, d.Path+" ("+d.Reason+")")
		}
	}
	sort.Strings(out)
	return out
}

// EnsureGlobalCommands installs slash commands for all 13 command agents
// (ensure_global_agent_commands). keys==nil means the full fleet.
func EnsureGlobalCommands(keys []string) ([]string, error) {
	home := KittifyHome()
	if keys == nil {
		keys = []string{}
		if root := project.Locate(""); root != "" && kittify.ConfigExists(root) {
			var ac kittify.AgentConfig
			var err error
			kittify.QuietAgentConfig(func() { ac, err = kittify.LoadAgentConfig(root) })
			if err == nil {
				for _, key := range ac.Available {
					if _, ok := agents.CommandConfigs[key]; ok {
						keys = append(keys, key)
					}
				}
			}
		}
	}
	if freshCommands(home, keys) {
		return nil, nil
	}
	p, canonical, err := AssessGlobalCommands(keys)
	if err != nil {
		return nil, &AssetError{"slash_commands", err}
	}
	warn := p.unmigrated(canonical)
	if err := p.apply(); err != nil {
		return warn, &AssetError{"slash_commands", err}
	}
	return warn, nil
}

// AssessGlobalCommands plans the slash_commands owner.
func AssessGlobalCommands(keys []string) (*prep, map[string]bool, error) {
	home := KittifyHome()
	var roots []string
	for _, k := range agents.CommandAgents {
		roots = append(roots, agents.GlobalCommandDir(k))
	}
	p, err := newPrep("slash_commands", anchorFor(append([]string{home}, roots...)...), filepath.Join(home, "cache"), commandsLockFile)
	if err != nil {
		return nil, nil, err
	}
	full := keys == nil
	if full {
		keys = agents.SortedCommandAgents()
	} else {
		keys = append([]string(nil), keys...)
		sort.Strings(keys)
	}
	canonicalNames := map[string]bool{}
	for _, k := range keys {
		if _, ok := agents.CommandConfigs[k]; !ok {
			return nil, nil, fmt.Errorf("Unknown slash-command agent: %s", k)
		}
		out := agents.GlobalCommandDir(k)
		st, err := p.observe(out)
		if err != nil {
			return nil, nil, err
		}
		if st.Kind != "directory" && st.Kind != "absent" {
			p.preserve(out, "Unproven command directory replacement", false)
			continue
		}
		rendered, err := agents.RenderAgentCommands(k)
		if err != nil {
			return nil, nil, err
		}
		names := map[string]bool{}
		for _, f := range rendered {
			names[f.Name] = true
			canonicalNames[f.Name] = true
			target := filepath.Join(out, f.Name)
			pred := false
			if ts, _ := p.observe(target); ts.Kind == "file" {
				if existing, err := os.ReadFile(target); err == nil {
					pred = agents.IsCanonicalPredecessor(existing, f.Content, app.Version)
				}
			}
			if err := p.asset(target, f.Content, 0o444, false, pred); err != nil {
				return nil, nil, err
			}
		}
		if st.Kind == "directory" {
			ents, _ := os.ReadDir(out)
			for _, e := range ents {
				if !names[e.Name()] {
					if _, err := p.retire(filepath.Join(out, e.Name())); err != nil {
						return nil, nil, err
					}
				}
			}
		}
	}
	var stamp string
	if err := p.retireAntigravityWorkflows(); err != nil {
		return nil, nil, err
	}
	{
		sig, err := TemplateSourceSignature()
		if err != nil {
			return nil, nil, err
		}
		if err := p.overwriteInternal(filepath.Join(home, "cache", commandsFreshnessFile), freshnessPayload(sig, keys)); err != nil {
			return nil, nil, err
		}
		if full {
			stamp = filepath.Join(home, "cache", commandsVersionFile)
		}
	}
	if err := p.finish(stamp, app.Version); err != nil {
		return nil, nil, err
	}
	return p, canonicalNames, nil
}

// Options controls EnsureAll.
type Options struct {
	SkipCommands bool
}

// EnsureAll runs the three global owners in upstream order.
func EnsureAll(opts Options) (warnings []string, err error) {
	if err := EnsureRuntime(); err != nil {
		return nil, err
	}
	if err := EnsureGlobalSkills(); err != nil {
		return nil, err
	}
	if opts.SkipCommands {
		return nil, nil
	}
	return EnsureGlobalCommands(nil)
}

// HasGlobalRuntime reports whether ~/.kittify/missions has a mission subdir.
func HasGlobalRuntime() bool {
	ents, err := os.ReadDir(filepath.Join(KittifyHome(), "missions"))
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.IsDir() {
			return true
		}
	}
	return false
}
