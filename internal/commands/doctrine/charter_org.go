package doctrine

import (
	"fmt"
	"os"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

// charter org init / validate (cli/commands/charter/org.py).

func init() { registerCharterLeaf(charterOrgGroup) }

func charterOrgGroup() *cli.Command {
	short, long := helpK("charter_org")
	g := &cli.Command{Name: "org", Short: short, Long: long}
	g.InvokeWithoutCommand = true
	g.Run = func(ctx *cli.Context) error {
		cli.WriteHelp(ctx.Stdout, ctx.Cmd)
		return cli.Exit(2)
	}
	g.Add(orgInitCmd(), orgValidateCmd())
	return g
}

func orgInitCmd() *cli.Command {
	short, long := helpK("charter_org_init")
	return &cli.Command{
		Name: "init", Short: short, Long: long,
		Args: []*cli.Arg{{Name: "pack_path", Help: "Destination directory for the scaffold or rendered charter pack tree.", Required: true, Metavar: "pack_path"}},
		Flags: []*cli.Flag{
			{Name: "force", Kind: cli.Bool, Help: "Overwrite an existing pack directory."},
			{Name: "template", Kind: cli.String, Help: "Local template directory or git URL (HTTPS/SSH; optional #branch). When omitted, scaffolds the minimal four-file pack."},
			{Name: "org-name", Kind: cli.String, Help: "Validated org/pack identity for {{ORG_NAME}} (required with --template)."},
			{Name: "local-path", Kind: cli.String, Help: "Value for {{LOCAL_PATH}} (default: pack). Distinct from PACK_PATH."},
			{Name: "branch", Kind: cli.String, Help: "Git ref when --template is a git URL (may also be encoded in TEMPLATE)."},
		},
		Run: runOrgInit,
	}
}

func runOrgInit(ctx *cli.Context) error {
	w := ctx.Stdout
	packPath := ctx.Arg("pack_path")
	force := ctx.Bool("force")
	if tmpl := ctx.OptString("template"); tmpl != nil {
		orgName := ctx.String("org-name")
		if orgName == "" {
			cprint(w, "[red]ORG_NAME is required when --template is set[/red] (org_name.required).")
			return cli.Exit(1)
		}
		if err := charter.RenderOrgPack(packPath, *tmpl, orgName, ctx.OptString("local-path"), ctx.String("branch"), ctx.IsSet("branch"), force); err != nil {
			cprint(w, "[red]%s[/red]", err.Message)
			return cli.Exit(1)
		}
		cprint(w, "[green]Org doctrine rendered at:[/green] %s", packPath)
		cprint(w, "  Full template tree written (minus .templateignore); ORG_NAME / LOCAL_PATH tokens substituted.")
		cprint(w, "\nRun [bold]spec-kitty charter org validate %s/pack[/bold] or your template's quality-check if applicable.", packPath)
		return nil
	}
	if _, err := os.Stat(packPath); err == nil && !force {
		cprint(w, "[red]Target directory already exists:[/red] %s\nPass [bold]--force[/bold] to overwrite.", packPath)
		return cli.Exit(1)
	}
	if err := charter.WriteOrgScaffold(packPath); err != nil {
		return err
	}
	cprint(w, "[green]Org pack scaffolded at:[/green] %s", packPath)
	for _, l := range []string{"org-charter.yaml", "drg/fragment.yaml", "presets/starter.yaml", "README.md"} {
		cprint(w, "  %s", l)
	}
	cprint(w, "\nRun [bold]spec-kitty charter org validate %s[/bold] to confirm.", packPath)
	return nil
}

func orgValidateCmd() *cli.Command {
	short, long := helpK("charter_org_validate")
	return &cli.Command{
		Name: "validate", Short: short, Long: long,
		Args: []*cli.Arg{{Name: "pack_path", Help: "Path to the org Charter Pack directory to validate.", Required: true, Metavar: "pack_path"}},
		Run: func(ctx *cli.Context) error {
			res := charter.ValidatePack(ctx.Arg("pack_path"), true)
			charter.RenderValidationResult(ctx.Stdout, res, false)
			if !res.OK() {
				return cli.Exit(1)
			}
			return nil
		},
	}
}

var _ = fmt.Sprint
