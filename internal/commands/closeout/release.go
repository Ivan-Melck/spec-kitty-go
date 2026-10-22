package closeout

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

func init() {
	addCommands(func(_, agent, _ *cli.Command) {
		rel := app.Group(agent, "release", "Release packaging commands for AI agents")
		rel.Add(&cli.Command{
			Name:  "prep",
			Short: "Prepare release artifacts (changelog draft, version bump, structured inputs).",
			Long:  "Prepare release artifacts (changelog draft, version bump, structured inputs).\n\nReads kitty-specs/ artifacts and local git tags. No network calls.",
			Flags: []*cli.Flag{
				{Name: "channel", Kind: cli.String, Choices: []string{"alpha", "beta", "stable"}, Required: true, Help: "Release channel: alpha | beta | stable"},
				{Name: "repo", Kind: cli.String, Metavar: "<path>", Default: ".", Help: "Repository root (default: current directory)"},
				{Name: "json", Kind: cli.Bool, Help: "Emit JSON instead of human-readable text"},
			},
			Run: runReleasePrep,
		})
	})
}

var (
	rePre    = regexp.MustCompile(`^(\d+\.\d+\.\d+)(a|b)(\d+)$`)
	reStable = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
)

// proposeVersion is release.version.propose_version.
func proposeVersion(cur, channel string) (string, error) {
	pre, st := rePre.FindStringSubmatch(cur), reStable.FindStringSubmatch(cur)
	if pre == nil && st == nil {
		return "", fmt.Errorf("Cannot parse version %s. Expected 'X.Y.Za<N>', 'X.Y.Zb<N>', or 'X.Y.Z'. If current version uses 'rc' or '.dev', edit pyproject.toml manually before running release prep.", mission.PyRepr(cur))
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	switch channel {
	case "alpha":
		if pre != nil && pre[2] == "a" {
			return fmt.Sprintf("%sa%d", pre[1], n(pre[3])+1), nil
		}
		if st != nil {
			return cur + "a1", nil
		}
		return "", fmt.Errorf("Cannot promote a beta release (%s) to alpha. Beta is ahead of alpha in the release sequence.", mission.PyRepr(cur))
	case "beta":
		if pre != nil && pre[2] == "a" {
			return pre[1] + "b1", nil
		}
		if pre != nil {
			return fmt.Sprintf("%sb%d", pre[1], n(pre[3])+1), nil
		}
		return cur + "b1", nil
	}
	if pre != nil {
		return pre[1], nil
	}
	return fmt.Sprintf("%s.%s.%d", st[1], st[2], n(st[3])+1), nil
}

var reTomlSection = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
var reTomlVersion = regexp.MustCompile(`^\s*version\s*=\s*["']([^"']*)["']`)

func pyprojectVersion(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("[Errno 2] No such file or directory: %s", mission.PyRepr(path))
	}
	section := ""
	for _, l := range strings.Split(string(b), "\n") {
		if m := reTomlSection.FindStringSubmatch(l); m != nil {
			section = strings.TrimSpace(m[1])
			continue
		}
		if section == "project" {
			if m := reTomlVersion.FindStringSubmatch(l); m != nil {
				return m[1], nil
			}
		}
	}
	return "", fmt.Errorf("'version'")
}

func frontmatterField(text, key string) (string, bool) {
	if !strings.HasPrefix(text, "---") {
		return "", false
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return "", false
	}
	for _, l := range strings.Split(text[3:3+end], "\n") {
		k, v, ok := strings.Cut(l, ":")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `'"`), true
		}
	}
	return "", false
}

func changelogBlock(root string) (string, []string) {
	specs := filepath.Join(root, "kitty-specs")
	entries, err := os.ReadDir(specs)
	if err != nil {
		return "", nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var lines, slugs []string
	for _, name := range names {
		dir := filepath.Join(specs, name)
		wps, _ := filepath.Glob(filepath.Join(dir, "tasks", "WP*.md"))
		sort.Strings(wps)
		lanes := map[string]string{}
		if b, err := os.ReadFile(filepath.Join(dir, "status.events.jsonl")); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if o, err := jsonx.DecodeObject([]byte(strings.TrimSpace(l))); err == nil {
					if to := o.GetString("to_lane"); to != "" && o.GetString("wp_id") != "" {
						lanes[o.GetString("wp_id")] = to
					}
				}
			}
		}
		var accepted []string
		for _, p := range wps {
			b, _ := os.ReadFile(p)
			text := string(b)
			id, ok := frontmatterField(text, "work_package_id")
			if !ok {
				id = strings.TrimSuffix(filepath.Base(p), ".md")
			}
			ok = false
			if lane, has := lanes[id]; has {
				ok = lane == "approved" || lane == "done"
			} else if st, has := frontmatterField(text, "status"); has {
				st = strings.ToLower(st)
				ok = st == "done" || st == "accepted" || st == "merged"
			}
			if !ok {
				continue
			}
			title := strings.TrimSuffix(filepath.Base(p), ".md")
			for _, l := range strings.Split(text, "\n") {
				s := strings.TrimSpace(l)
				if strings.HasPrefix(s, "## ") || strings.HasPrefix(s, "# ") {
					title = strings.TrimSpace(strings.TrimLeft(s, "#"))
					break
				}
			}
			accepted = append(accepted, id+": "+title)
		}
		if len(accepted) == 0 {
			continue
		}
		display := mission.ReadMetaOrEmpty(dir).GetString("friendly_name")
		if display == "" {
			display = name
		}
		lines = append(lines, fmt.Sprintf("### %s (`%s`)", display, name), "")
		for _, a := range accepted {
			lines = append(lines, "- "+a)
		}
		lines = append(lines, "")
		slugs = append(slugs, name)
	}
	if len(slugs) == 0 {
		return "", nil
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\n") + "\n", slugs
}

// gridRows renders a box=None, padding (0, 2) table.
func gridRows(header []string, rows [][]string) string {
	n := len(rows[0])
	widths := make([]int, n)
	all := rows
	if header != nil {
		all = append([][]string{header}, rows...)
	}
	for _, r := range all {
		for i, c := range r {
			for _, l := range strings.Split(c, "\n") {
				if runeLen(l) > widths[i] {
					widths[i] = runeLen(l)
				}
			}
		}
	}
	var out []string
	for _, r := range all {
		cells := make([][]string, n)
		h := 1
		for i, c := range r {
			cells[i] = strings.Split(c, "\n")
			if len(cells[i]) > h {
				h = len(cells[i])
			}
		}
		for li := 0; li < h; li++ {
			var b strings.Builder
			for i := range r {
				v := ""
				if li < len(cells[i]) {
					v = cells[i][li]
				}
				b.WriteString("  " + v + strings.Repeat(" ", widths[i]-runeLen(v)) + "  ")
			}
			out = append(out, b.String())
		}
	}
	return strings.Join(out, "\n")
}

func runReleasePrep(ctx *cli.Context) error {
	switch ctx.String("channel") {
	case "alpha", "beta", "stable":
	default:
		return cli.Usage(ctx.Cmd, "Invalid value for '--channel': %s is not one of 'alpha', 'beta', 'stable'.", mission.PyRepr(ctx.String("channel")))
	}
	repo, _ := filepath.Abs(ctx.String("repo"))
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	jsonMode := ctx.Bool("json")
	pp := filepath.Join(repo, "pyproject.toml")
	fail := func(err error, kind string) error {
		if jsonMode {
			o := jsonx.NewObject("error", err.Error(), "kind", kind)
			if kind == "ReleasePyprojectError" {
				o.Set("path", pp)
			}
			printJSON(ctx.Stdout, o)
		} else {
			sayLiteral(ctx.Stderr, "Error: ", err.Error())
		}
		return cli.Exit(1)
	}
	cur, err := pyprojectVersion(pp)
	if err != nil {
		return fail(err, "ReleasePyprojectError")
	}
	channel := ctx.String("channel")
	next, err := proposeVersion(cur, channel)
	if err != nil {
		return fail(err, "ValueError")
	}
	block, slugs := changelogBlock(repo)
	tag := "v" + next
	inputs := jsonx.NewObject("version", next, "tag_name", tag, "release_title", "Release "+tag, "release_notes_body", block,
		"mission_slug_list", strings.Join(slugs, ", "))
	if jsonMode {
		sl := []any{}
		for _, s := range slugs {
			sl = append(sl, s)
		}
		printJSON(ctx.Stdout, jsonx.NewObject("channel", channel, "current_version", cur, "proposed_version", next,
			"changelog_block", block, "proposed_changelog_block", block, "mission_slug_list", sl, "target_branch", "main",
			"structured_inputs", inputs))
		return nil
	}
	w := ctx.Stdout
	fitPanelRaw(w, gridRows(nil, [][]string{{"Channel", channel}, {"Current version", cur}, {"Proposed version", next},
		{"Target branch", "main"}, {"Tag name", tag}}), "Version Bump")
	if strings.TrimSpace(block) != "" {
		fitPanelRaw(w, strings.TrimRight(block, " \t\n"), "Draft Changelog Block")
	} else {
		say(w, "[yellow]No accepted missions found for changelog block.[/yellow]")
	}
	var rows [][]string
	for _, kv := range inputs.Pairs() {
		v := kv.Value.(string)
		if kv.Key == "release_notes_body" && len([]rune(v)) > 60 {
			v = string([]rune(v)[:60]) + "..."
		}
		rows = append(rows, []string{kv.Key, v})
	}
	fitPanelRaw(w, gridRows([]string{"Input", "Value"}, rows), "Structured Inputs")
	notice := fmt.Sprintf("Still-manual steps (not automated by this command):\n  1. PR creation:  gh pr create --title \"Release %[1]s\" --body \"<changelog_block>\"\n"+
		"  2. Tag push:     git tag -a %[1]s -m \"Release %[1]s\" && git push origin %[1]s\n  3. Monitoring:   gh run watch\n\nIf you need these steps automated, please file a follow-up issue.", tag)
	fitPanelRaw(w, notice, "Next Steps (Manual)")
	return nil
}
