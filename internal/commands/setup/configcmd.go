package setup

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "config",
			Short: "Display project configuration and asset resolution information.",
			Flags: []*cli.Flag{
				{Name: "show-origin", Kind: cli.Bool, Help: "Show where each resolved asset comes from (tier label + path)"},
				{Name: "mission", Short: "m", Kind: cli.String, Metavar: "<str>", Default: "software-dev", Help: "Mission to resolve assets for"},
			},
			Run: runConfig,
		})
	})
}

const pkgMissions = "packs/built-in/missions"

// OriginEntry is runtime.show_origin.OriginEntry.
type OriginEntry struct {
	AssetType, Name, Path, Tier, Error string
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// packageDefault returns the embedded package-default path for an asset.
func packageDefault(mission, subdir, name string) string {
	p := path.Join(pkgMissions, mission, subdir, name)
	if assets.Exists(p) {
		return p
	}
	if subdir == "command-templates" {
		p = path.Join(pkgMissions, "mission-steps", mission, strings.TrimSuffix(name, ".md"), "prompt.md")
		if assets.Exists(p) {
			return p
		}
	}
	return ""
}

// resolveAsset is runtime.resolver._resolve_asset (org-pack tier omitted).
func resolveAsset(name, subdir, root, mission string) (string, string, error) {
	k := filepath.Join(root, ".kittify")
	for _, c := range []struct{ p, tier string }{
		{filepath.Join(k, "overrides", "missions", mission, subdir, name), "override"},
		{filepath.Join(k, "overrides", subdir, name), "override"},
		{filepath.Join(k, subdir, name), "legacy"},
		{filepath.Join(project.GlobalKittify(), "missions", mission, subdir, name), "global_mission"},
		{filepath.Join(project.GlobalKittify(), subdir, name), "global"},
	} {
		if isFile(c.p) {
			return c.p, c.tier, nil
		}
	}
	if p := packageDefault(mission, subdir, name); p != "" {
		return p, "package_default", nil
	}
	return "", "", fmt.Errorf("Asset '%s' not found in any resolution tier (subdir='%s', mission='%s', project=%s)", name, subdir, mission, root)
}

func resolveMissionConfig(name, root string) (string, string, error) {
	k := filepath.Join(root, ".kittify")
	for _, c := range []struct{ p, tier string }{
		{filepath.Join(k, "overrides", "missions", name, "mission.yaml"), "override"},
		{filepath.Join(k, "missions", name, "mission.yaml"), "legacy"},
		{filepath.Join(project.GlobalKittify(), "missions", name, "mission.yaml"), "global_mission"},
	} {
		if isFile(c.p) {
			return c.p, c.tier, nil
		}
	}
	if p := path.Join(pkgMissions, name, "mission.yaml"); assets.Exists(p) {
		return p, "package_default", nil
	}
	return "", "", fmt.Errorf("Mission '%s' not found in any resolution tier (project=%s)", name, root)
}

func assetNames(dir string, keep func(name string, isDir bool) (string, bool)) []string {
	ents, err := assets.List(dir)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, e := range ents {
		if n, ok := keep(e.Name(), e.IsDir()); ok {
			set[n] = true
		}
	}
	return sortedKeys(set)
}

// CollectOrigins is runtime.show_origin.collect_origins.
func CollectOrigins(root, mission string) []OriginEntry {
	var entries []OriginEntry
	templates := assetNames(path.Join(pkgMissions, mission, "templates"), func(n string, d bool) (string, bool) {
		return n, !d && strings.HasSuffix(n, ".md")
	})
	if len(templates) == 0 {
		templates = []string{"plan-template.md", "spec-template.md", "task-prompt-template.md", "tasks-template.md"}
	}
	for _, n := range templates {
		p, tier, err := resolveAsset(n, "templates", root, mission)
		entries = append(entries, originEntry("template", n, p, tier, err))
	}
	cmdSet := map[string]bool{}
	for _, n := range assetNames(path.Join(pkgMissions, mission, "command-templates"), func(n string, d bool) (string, bool) {
		return n, !d && strings.HasSuffix(n, ".md")
	}) {
		cmdSet[n] = true
	}
	for _, n := range assetNames(path.Join(pkgMissions, "mission-steps", mission), func(n string, d bool) (string, bool) {
		return n + ".md", d && assets.Exists(path.Join(pkgMissions, "mission-steps", mission, n, "prompt.md"))
	}) {
		cmdSet[n] = true
	}
	cmds := sortedKeys(cmdSet)
	if len(cmds) == 0 {
		cmds = []string{"accept.md", "implement.md", "merge.md", "plan.md", "review.md", "specify.md", "tasks.md"}
	}
	for _, n := range cmds {
		p, tier, err := resolveAsset(n, "command-templates", root, mission)
		entries = append(entries, originEntry("command", n, p, tier, err))
	}
	for _, n := range assetNames(pkgMissions, func(n string, d bool) (string, bool) {
		return n, d && assets.Exists(path.Join(pkgMissions, n, "mission.yaml"))
	}) {
		p, tier, err := resolveMissionConfig(n, root)
		entries = append(entries, originEntry("mission", n, p, tier, err))
	}
	scripts := filepath.Join(root, ".kittify", "scripts")
	var sp []string
	filepath.Walk(scripts, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			sp = append(sp, p)
		}
		return nil
	})
	sort.Strings(sp)
	for _, p := range sp {
		rel, _ := filepath.Rel(scripts, p)
		entries = append(entries, OriginEntry{AssetType: "script", Name: rel, Path: p, Tier: "project"})
	}
	if p := filepath.Join(root, ".kittify", "AGENTS.md"); isFile(p) {
		entries = append(entries, OriginEntry{AssetType: "file", Name: "AGENTS.md", Path: p, Tier: "project"})
	} else if assets.Exists("specify_cli/templates/AGENTS.md") {
		entries = append(entries, OriginEntry{AssetType: "file", Name: "AGENTS.md", Path: "specify_cli/templates/AGENTS.md", Tier: "package_default"})
	} else {
		entries = append(entries, OriginEntry{AssetType: "file", Name: "AGENTS.md", Error: "AGENTS.md not found in project or package"})
	}
	return entries
}

func originEntry(kind, name, p, tier string, err error) OriginEntry {
	if err != nil {
		return OriginEntry{AssetType: kind, Name: name, Error: err.Error()}
	}
	return OriginEntry{AssetType: kind, Name: name, Path: p, Tier: tier}
}

func runConfig(ctx *cli.Context) error {
	w := ctx.Stdout
	if !ctx.Bool("show-origin") {
		out(w, "[yellow]Use --show-origin to display asset resolution details.[/yellow]")
		return nil
	}
	root, err := findRepoRoot(w)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, e := range CollectOrigins(root, ctx.String("mission")) {
		title := strings.ToUpper(e.AssetType[:1]) + e.AssetType[1:]
		if e.Path == "" {
			rows = append(rows, []string{title, e.Name, "not found", e.Error})
			continue
		}
		p := e.Path
		if filepath.IsAbs(p) {
			if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
				p = rel
			}
		}
		rows = append(rows, []string{title, e.Name, e.Tier, p})
	}
	richTableX(w, tableSpec{Title: "Asset Resolution Origins", Headers: []string{"Type", "Name", "Tier", "Resolved Path"}, Width: []int{10}, ShowLines: true}, rows)
	checkVersionPin(ctx, root)
	return nil
}

func checkVersionPin(ctx *cli.Context, root string) {
	cfg, err := kittify.LoadConfig(root)
	if err != nil || cfg == nil {
		return
	}
	rt := cfg.GetObject("runtime")
	if rt == nil || !rt.Has("pin_version") {
		return
	}
	v, _ := rt.Get("pin_version")
	rich.LogWarning(ctx.Stderr, fmt.Sprintf("runtime.pin_version=%v found in .kittify/config.yaml. Version pinning is not yet supported. Using latest global assets. The pin will NOT be silently honored.", v))
}
