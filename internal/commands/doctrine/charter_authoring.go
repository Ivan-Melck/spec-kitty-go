package doctrine

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// charter new / charter validate (specify_cli.cli.commands.charter.authoring).

func init() {
	registerCharterLeaf(charterNewCmd)
	registerCharterLeaf(charterValidateCmd)
}

var stubTemplates = map[string]string{
	"directive":  "schema_version: \"1.0\"\nid: {artifact_id}\ntitle: TODO short title\nintent: TODO why this directive exists\nenforcement: advisory\n",
	"tactic":     "schema_version: \"1.0\"\nid: {artifact_id}\nname: TODO short name\npurpose: TODO when to apply this tactic\nsteps:\n  - title: TODO first step\n    description: TODO what the step does\n",
	"styleguide": "schema_version: \"1.0\"\nid: {artifact_id}\ntitle: TODO short title\nscope: code\nprinciples:\n  - TODO first principle\napplies_to_languages: []\n",
	"toolguide":  "schema_version: \"1.0\"\nid: {artifact_id}\ntool: TODO tool name\ntitle: TODO short title\nguide_path: src/charter/offering/toolguides/{artifact_id}.md\nsummary: TODO one-line summary\n",
	"paradigm":   "schema_version: \"1.0\"\nid: {artifact_id}\nname: TODO short name\nsummary: TODO one-line summary of the paradigm\n",
	"procedure": "schema_version: \"1.0\"\nid: {artifact_id}\nname: TODO short name\npurpose: TODO why this procedure exists\nentry_condition: TODO when to enter\n" +
		"exit_condition: TODO when complete\nsteps:\n  - title: TODO first step\n",
	"agent_profile": "schema-version: \"1.0\"\nprofile-id: {artifact_id}\nname: TODO agent display name\nroles: [implementer]\npurpose: TODO one-line purpose statement\n" +
		"specialization:\n  primary-focus: TODO primary focus area\n",
	"mission_step_contract": "id: {artifact_id}\nschema_version: \"1.0\"\naction: TODO action verb\nmission: TODO mission slug\nsteps:\n  - id: step-1\n    description: TODO step description\n",
	"asset":                 "id: {artifact_id}\nmime: text/plain\npath: TODO-relative-path-under-assets.txt\ntitle: TODO asset display name\n",
}

func scaffoldableKinds() []string {
	var ks []string
	for k := range stubTemplates {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// slugFor is artifact_kinds.slug_for.
func slugFor(kind, id string) string {
	n := id
	if kind == "directive" {
		n = strings.ReplaceAll(strings.ToLower(id), "_", "-")
	}
	return strings.ReplaceAll(url.PathEscape(n), "+", "%2B")
}

func charterNewCmd() *cli.Command {
	return &cli.Command{
		Name:  "new",
		Short: "Scaffold a stub doctrine artifact YAML (FR-016).",
		Long:  "Scaffold a stub doctrine artifact YAML (FR-016).\n\nThe scaffolder pre-fills the canonical schema's required fields with\n``TODO …`` placeholders so the file passes ``charter validate`` on\nfirst emit.  Refuses to overwrite an existing file.",
		Args: []*cli.Arg{
			{Name: "kind", Required: true, Metavar: "{kind}", Help: "Artifact kind (singular): one of " + strings.Join(scaffoldableKinds(), ", ") + "."},
			{Name: "ID", Required: true, Metavar: "{ID}", Help: "Artifact identifier (kebab-case for most kinds; SCREAMING_SNAKE for directives)."},
		},
		Flags: []*cli.Flag{
			{Name: "pack", Kind: cli.String, Metavar: "<path>", Help: "Scaffold inside a Charter Pack directory instead of the project layer. When omitted, the stub lands under .kittify/charter-packs/."},
		},
		Run: runCharterNew,
	}
}

func runCharterNew(ctx *cli.Context) error {
	w := ctx.Stdout
	raw := ctx.Arg("kind")
	kind := strings.ToLower(strings.TrimSpace(raw))
	tmpl, ok := stubTemplates[kind]
	if !ok {
		cprint(w, "%s", "[red]Unknown artifact kind '"+raw+"'.[/red] Expected one of: "+strings.Join(scaffoldableKinds(), ", ")+".")
		return cli.Exit(2)
	}
	id := ctx.Arg("ID")
	k, _ := dct.ParseKind(kind)
	var packRoot, dirName string
	if p := ctx.OptString("pack"); p != nil {
		packRoot, dirName = *p, k.Plural()
	} else {
		root := project.Locate("")
		if root == "" {
			cprint(w, "%s", "[red]Could not locate spec-kitty project root. Run from inside a project containing .kittify/ or pass --pack to target an explicit pack directory.[/red]")
			return cli.Exit(1)
		}
		packRoot, dirName = filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot)), dct.ProjectKindDir(k)
	}
	dir := filepath.Join(packRoot, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, slugFor(kind, id)+k.Suffix())
	if _, err := os.Stat(target); err == nil {
		cprint(w, "%s", "[red]Refusing to overwrite existing file:[/red] "+target)
		return cli.Exit(1)
	}
	stub := strings.ReplaceAll(tmpl, "{artifact_id}", id)
	parsed, err := yamlx.ParseObject([]byte(stub))
	if err != nil {
		parsed = &jsonx.Object{}
	}
	if msg := charter.ValidateArtifactSchema(k.Plural(), parsed); msg != "" {
		cprint(w, "%s", "[red]Internal error:[/red] stub for kind '"+kind+"' failed schema validation: "+msg)
		return cli.Exit(1)
	}
	if err := os.WriteFile(target, []byte(stub), 0o644); err != nil {
		return err
	}
	cprint(w, "%s", "[green]Created stub artifact:[/green] "+target+"\nRun [bold]spec-kitty charter validate "+target+"[/bold] to confirm.")
	return nil
}

var suffixToKind = [][2]string{
	{".directive.yaml", "directives"}, {".tactic.yaml", "tactics"}, {".styleguide.yaml", "styleguides"},
	{".toolguide.yaml", "toolguides"}, {".paradigm.yaml", "paradigms"}, {".procedure.yaml", "procedures"},
	{".agent.yaml", "agent_profiles"}, {".step-contract.yaml", "mission_step_contracts"}, {".asset.yaml", "assets"},
}

func detectArtifactPlural(p string) string {
	n := strings.ToLower(filepath.Base(p))
	for _, s := range suffixToKind {
		if strings.HasSuffix(n, s[0]) {
			return s[1]
		}
	}
	return ""
}

func checkAppliesToLanguages(o *jsonx.Object) string {
	v, ok := o.Get("applies_to_languages")
	l, isList := v.([]any)
	if !ok || !isList {
		return ""
	}
	var reserved, bad []string
	for _, x := range l {
		s, isStr := x.(string)
		if !isStr {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "unknown":
			reserved = append(reserved, "'"+s+"'")
		case "any", "all":
			bad = append(bad, "'"+s+"'")
		}
	}
	if len(reserved) > 0 {
		return "`unknown` is a reserved value set by Spec Kitty for unrecognised project languages; artifacts cannot target it (found: " + strings.Join(reserved, ", ") + ")"
	}
	if len(bad) > 0 {
		return "`any`/`all` are not language tokens — omit `applies_to_languages` to mean always-applicable (found: " + strings.Join(bad, ", ") + ")"
	}
	return ""
}

func validateSingleArtifact(p string) string {
	plural := detectArtifactPlural(p)
	if plural == "" {
		var sfx []string
		for _, s := range suffixToKind {
			sfx = append(sfx, s[0])
		}
		sort.Strings(sfx)
		return "unrecognised artifact filename suffix (expected one of " + strings.Join(sfx, ", ") + ")"
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "YAML parse error: " + err.Error()
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return "YAML parse error: " + err.Error()
	}
	if v == nil {
		return "empty YAML document"
	}
	o, isObj := v.(*jsonx.Object)
	if !isObj {
		return "expected a YAML mapping at top level"
	}
	if m := checkAppliesToLanguages(o); m != "" {
		return m
	}
	if m := charter.ValidateArtifactSchema(plural, o); m != "" {
		return "schema validation failed: " + m
	}
	return ""
}

func charterValidateCmd() *cli.Command {
	return &cli.Command{
		Name:  "validate",
		Short: "Validate project-layer doctrine artifacts against their schemas (FR-017).",
		Long:  "Validate project-layer doctrine artifacts against their schemas (FR-017).\n\nWhen *path* is a single file, validates that file.  When *path* is a\ndirectory, walks the tree for ``*.yaml`` files whose filename suffix\nmatches a canonical artifact kind and validates each one.\n\nExit code: ``0`` if every artifact validates; ``1`` if any artifact\nfails.  A per-file error report is printed for failures.",
		Args: []*cli.Arg{
			{Name: "path", Required: true, Metavar: "{path}", Help: "Artifact YAML file or a directory containing project-layer doctrine artifacts (recurses into per-kind subdirectories)."},
		},
		Run: runCharterValidate,
	}
}

func runCharterValidate(ctx *cli.Context) error {
	w := ctx.Stdout
	p := ctx.Arg("path")
	st, err := os.Stat(p)
	if err != nil {
		cprint(w, "%s", "[red]Path not found:[/red] "+p)
		return cli.Exit(2)
	}
	var targets []string
	if !st.IsDir() {
		targets = []string{p}
	} else {
		filepath.WalkDir(p, func(fp string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".yaml") && detectArtifactPlural(fp) != "" {
				targets = append(targets, fp)
			}
			return nil
		})
		sortPyPaths(targets)
	}
	if len(targets) == 0 {
		cprint(w, "%s", "[yellow]No doctrine artifact files found under "+p+".[/yellow]")
		return cli.Exit(0)
	}
	failures := 0
	for _, t := range targets {
		if msg := validateSingleArtifact(t); msg == "" {
			cprint(w, "%s", "[green]OK[/green] "+t)
		} else {
			failures++
			cprint(w, "%s", "[red]FAIL[/red] "+t+": "+msg)
		}
	}
	if failures > 0 {
		cprint(w, "%s", fmt.Sprintf("\n[red]%d of %d artifact(s) failed validation.[/red]", failures, len(targets)))
		return cli.Exit(1)
	}
	cprint(w, "%s", fmt.Sprintf("\n[green]%d artifact(s) passed validation.[/green]", len(targets)))
	return cli.Exit(0)
}

// sortPyPaths orders paths like Python's sorted(Path) (component-wise).
func sortPyPaths(ps []string) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := strings.Split(filepath.ToSlash(ps[i]), "/"), strings.Split(filepath.ToSlash(ps[j]), "/")
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}

var _ = uconsole.Print
