package maintenance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func newDoctorRunIndexCmd() *cli.Command {
	return &cli.Command{Name: "run-index", Short: short("run-index"), Long: longHelp["doctor_run-index"], Flags: []*cli.Flag{boolFlag("json", machineJSON)}, Run: runDoctorRunIndex}
}

func newDoctorProvenanceCmd() *cli.Command {
	return &cli.Command{Name: "provenance", Short: short("provenance"), Long: longHelp["doctor_provenance"], Flags: []*cli.Flag{boolFlag("json", machineJSON)}, Run: runDoctorProvenance}
}

func newDoctorCutoverCmd() *cli.Command {
	return &cli.Command{Name: "cutover", Short: short("cutover"), Long: longHelp["doctor_cutover"], Flags: []*cli.Flag{boolFlag("json", machineJSON)}, Run: runDoctorCutover}
}

var winAbs = regexp.MustCompile(`^([A-Za-z]:[\\/]|\\\\)`)

func isAbsAnyOS(p string) bool { return filepath.IsAbs(p) || winAbs.MatchString(p) }

// runIndexLeaks is m_4_0_0rc5_heal_run_index_paths.describe_leaks.
func runIndexLeaks(root string) []string {
	b, err := os.ReadFile(project.Kittify(root, "runtime", "feature-runs.json"))
	if err != nil {
		return nil
	}
	v, err := pyjson.Loads(string(b))
	if err != nil {
		return nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil
	}
	var out []string
	for _, kv := range o.Pairs() {
		e, ok := kv.Value.(*jsonx.Object)
		if !ok {
			continue
		}
		if rd, ok := valueGet(e, "run_dir").(string); ok && rd != "" && isAbsAnyOS(rd) {
			out = append(out, fmt.Sprintf("feature-runs.json[%s].run_dir=%s (absolute; not portable across copy/move)", kv.Key, pyjson.ReprString(rd)))
		}
	}
	return out
}

func runDoctorRunIndex(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	leaks := runIndexLeaks(root)
	const hint = "spec-kitty migrate  # rewrites absolute run_dir paths to portable tokens"
	if jsonOut {
		var h any
		if len(leaks) > 0 {
			h = hint
		}
		printJSON(out, jsonx.NewObject("leaks", strList(leaks), "leak_count", len(leaks), "heal_hint", h))
		if len(leaks) > 0 {
			return cli.Exit(1)
		}
		return nil
	}
	if len(leaks) == 0 {
		cprint(out, "[green]Run index[/green]: no absolute run_dir leaks found.")
		return nil
	}
	cprintf(out, "\n[bold yellow]Run-index finding(s)[/bold yellow] -- %d absolute run_dir(s)\n", len(leaks))
	for _, l := range leaks {
		cprintf(out, "  • [yellow]%s[/yellow]", l)
	}
	cprintf(out, "\n  [dim]Heal a relocated run index with:[/dim] %s", hint)
	fmt.Fprintln(out)
	return cli.Exit(1)
}

// isBuiltInPackPath is charter.provenance.is_built_in_pack_path.
func isBuiltInPackPath(p string) bool { return healthcheck.IsBuiltInPackPath(p) }

// provenanceFindings is _provenance_doctor._collect_findings: describe_leaks
// (non-template-set catalog refs + agent profile manifest), then the
// template-set leaks and ambiguities of the 4.0.0rc5 heal.
func provenanceFindings(root string) ([]string, []string) {
	var leaks, unresolved, tsLeaks []string
	cp := project.Kittify(root, "charter", "charter.yaml")
	if b, err := os.ReadFile(cp); err == nil {
		v, perr := yamlx.Parse(b)
		if perr != nil {
			return nil, []string{"unreadable charter.yaml (" + ruamelErrorClass(perr) + "): provenance was not evaluated; nothing was changed"}
		}
		if doc, ok := v.(*jsonx.Object); ok {
			if cat, ok := valueGet(doc, "catalog").(*jsonx.Object); ok {
				for _, r := range cat.GetList("references") {
					ref, ok := r.(*jsonx.Object)
					if !ok || valueGet(ref, "kind") == "template_set" {
						continue
					}
					sp, _ := valueGet(ref, "source_path").(string)
					if sp == "" || !filepath.IsAbs(sp) || !isBuiltInPackPath(sp) {
						continue
					}
					leaks = append(leaks, fmt.Sprintf("charter.yaml catalog[%s].source_path=%s", refID(ref), pyjson.ReprString(sp)))
				}
				tsLeaks, unresolved = templateSetProvenance(cat)
			}
		}
	}
	if b, err := os.ReadFile(project.Kittify(root, "agent_profiles_manifest.json")); err == nil {
		if v, err := pyjson.Loads(string(b)); err == nil {
			walkManifest(v, &leaks)
		}
	}
	return append(leaks, tsLeaks...), unresolved
}

// ruamelErrorClass maps a YAML parse failure to the ruamel exception class
// name upstream reports (type(exc).__name__).
func ruamelErrorClass(err error) string {
	if c := pyyaml.ClassName(err); c != "" {
		return c
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "already defined"):
		return "DuplicateKeyError"
	case strings.Contains(msg, "cannot start any token"), strings.Contains(msg, "mapping values are not allowed"),
		strings.Contains(msg, "could not find expected ':'"), strings.Contains(msg, "unknown escape character"),
		strings.Contains(msg, "found a tab character"), strings.Contains(msg, "control characters are not allowed"),
		strings.Contains(msg, "unexpected end of stream"), strings.Contains(msg, "while scanning"):
		return "ScannerError"
	}
	return "ParserError"
}

func refID(ref *jsonx.Object) string {
	if x, ok := ref.Get("id"); ok {
		return pyjson.Str(x)
	}
	return "?"
}

// templateSetProvenance is describe_template_set_leaks /
// describe_template_set_ambiguities over catalog.references.
func templateSetProvenance(cat *jsonx.Object) (leaks, ambiguous []string) {
	mission, _ := valueGet(cat, "mission").(string)
	suffix := healthcheck.MissionSuffix(mission)
	if suffix == "" {
		return nil, nil
	}
	set, _ := valueGet(cat, "template_set").(string)
	if set == "" {
		return nil, nil
	}
	// _mission_token constructs the default mission repository and normalizes
	// its source path, even when no matching template-set reference exists.
	assets.WarnPackRoot(assets.OfferingPackRoot)
	assets.WarnPackRoot(assets.ProvenancePackRoot)
	expectedID := "TEMPLATE_SET:" + set
	refs, ok := valueGet(cat, "references").([]any)
	if !ok {
		return nil, nil
	}
	for _, r := range refs {
		ref, ok := r.(*jsonx.Object)
		if !ok || valueGet(ref, "kind") != "template_set" || valueGet(ref, "id") != expectedID {
			continue
		}
		sp, isStr := valueGet(ref, "source_path").(string)
		if !isStr {
			continue
		}
		if healthcheck.MatchesMissionSource(sp, suffix) {
			leaks = append(leaks, fmt.Sprintf("charter.yaml catalog[%s].source_path=%s", refID(ref), pyjson.ReprString(sp)))
			continue
		}
		if healthcheck.IsAbsolutePathAnyOS(sp) && healthcheck.SuffixMatches(sp, suffix) {
			ambiguous = append(ambiguous, fmt.Sprintf("ambiguous template-set provenance (checkout identity cannot be verified; path not healed and left unchanged): charter.yaml catalog[%s].source_path=%s", expectedID, pyjson.ReprString(sp)))
		}
	}
	return leaks, ambiguous
}

func walkManifest(v any, leaks *[]string) {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return
	}
	entries := o.GetList("entries")
	for _, e := range entries {
		eo, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		sp, _ := valueGet(eo, "source_path").(string)
		if sp != "" && filepath.IsAbs(sp) && isBuiltInPackPath(sp) {
			*leaks = append(*leaks, fmt.Sprintf("agent_profiles_manifest.json[%s/%s].source_path=%s", eo.GetString("profile_urn"), eo.GetString("tool_key"), pyjson.ReprString(sp)))
		}
	}
}

func runDoctorProvenance(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	leaks, unresolved := provenanceFindings(root)
	findings := append(append([]string{}, leaks...), unresolved...)
	const hint = "spec-kitty migrate  # applies pending provenance migrations"
	if jsonOut {
		var h any
		if len(leaks) > 0 {
			h = hint
		}
		printJSON(out, jsonx.NewObject("leaks", strList(leaks), "leak_count", len(leaks), "unresolved", strList(unresolved),
			"unresolved_count", len(unresolved), "finding_count", len(findings), "heal_hint", h))
		if len(findings) > 0 {
			return cli.Exit(1)
		}
		return nil
	}
	if len(findings) == 0 {
		cprint(out, "[green]Provenance[/green]: no absolute built-in-pack source_path leaks found.")
		return nil
	}
	cprintf(out, "\n[bold yellow]Provenance finding(s)[/bold yellow] -- %d source_path issue(s)\n", len(findings))
	for _, f := range findings {
		cprintf(out, "  • [yellow]%s[/yellow]", f)
	}
	if len(leaks) > 0 {
		cprintf(out, "\n  [dim]Heal verified built-in paths with:[/dim] %s", hint)
	}
	if len(unresolved) > 0 {
		cprint(out, "\n  [dim]Unverified paths are left unchanged; resolve them manually or restore checkout evidence.[/dim]")
	}
	fmt.Fprintln(out)
	return cli.Exit(1)
}

func runDoctorCutover(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	wireWarn(ctx)
	out := ctx.Stdout
	results := migrate.CutoverRepo(root, true)
	type entry struct {
		slug   string
		cut    bool
		reason string
	}
	var entries []entry
	count := 0
	for _, r := range results {
		reason := "not yet migrated (no verify result)"
		switch {
		case r.Residue:
			reason = migrate.ResidueNotice("kitty-specs/" + r.Slug)
		case r.Error != nil:
			reason = *r.Error
		case r.WouldFlip:
			reason = "verify passed -- cut over (or already flipped)"
			if r.Verify != nil && r.Verify.WPCount == 0 {
				reason = "no legacy runtime to migrate"
			}
		case r.Verify != nil && len(r.Verify.Mismatches) > 0:
			reason = strings.Join(r.Verify.Mismatches, "; ")
		}
		cut := r.WouldFlip || r.Residue
		if cut {
			count++
		}
		entries = append(entries, entry{r.Slug, cut, reason})
	}
	if jsonOut {
		ms := []any{}
		for _, e := range entries {
			ms = append(ms, jsonx.NewObject("slug", e.slug, "cut_over", e.cut, "reason", e.reason))
		}
		printJSON(out, jsonx.NewObject("missions", ms, "cut_over_count", count, "total", len(entries)))
		return nil
	}
	if len(entries) == 0 {
		cprint(out, "[green]Cutover[/green]: no missions found under kitty-specs/.")
		return nil
	}
	cprintf(out, "\n[bold]Cutover Audit[/bold] -- %d/%d mission(s) cut over\n", count, len(entries))
	var rows [][]string
	for _, e := range entries {
		s := "[yellow]no[/yellow]"
		if e.cut {
			s = "[green]yes[/green]"
		}
		rows = append(rows, []string{e.slug, s, e.reason})
	}
	plainTable(out, []string{"Mission", "Cut Over", "Reason"}, []int{24, 10, 30}, rows)
	fmt.Fprintln(out)
	return nil
}
