package doctrine

import (
	"regexp"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

func init() { packLeafBuilders = append(packLeafBuilders, packVendorCmd) }
func packVendorCmd() *cli.Command {
	return &cli.Command{Name: "vendor", Short: "Bump (or verify) a pack pin; the pack tree itself is fetched by ``make sync-packs``.", Flags: []*cli.Flag{
		{Name: "check", Kind: cli.Bool, Help: "Verify only (offline): compare packs/<pack>/ with its pin lockfile; exit 1 on drift. Mutates nothing."},
		{Name: "ref", Kind: cli.String, Help: "Bump the pin to this open-packs commit SHA or branch (resolved to a SHA; only the SHA is persisted)."},
		{Name: "pack", Kind: cli.String, Default: "built-in", Help: "Pack whose packs/<pack>.source.yaml pin to use."},
		{Name: "subtree", Kind: cli.String, Help: "Override the pin's subtree (the pack's path inside the open-packs repository)."},
	}, Run: runPackVendor}
}
func runPackVendor(ctx *cli.Context) error {
	check, pack, ref, subtree := ctx.Bool("check"), ctx.String("pack"), ctx.OptString("ref"), ctx.OptString("subtree")
	fail := func(message string, code int) error {
		cprint(ctx.Stdout, "[red]%s[/red]", message)
		return cli.Exit(code)
	}
	if check && (ref != nil || subtree != nil) {
		return fail("--check is verify-only and cannot be combined with --ref or --subtree.", 2)
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(pack) {
		return fail("invalid --pack "+dct.PyRepr(pack)+": use a lowercase pack name such as built-in or internal.", 2)
	}
	if !check && ref == nil && subtree == nil {
		return fail(charter.PackPinNothingToBump, 2)
	}
	live := dct.BuiltInRoot()
	var pin *charter.PackPin
	var err error
	if check {
		pin, err = charter.VerifyVendorPin(live, pack)
	} else {
		pin, err = charter.BumpPackPin(live, pack, ref, subtree)
	}
	if err != nil {
		return fail(err.Error(), 1)
	}
	if check {
		cprint(ctx.Stdout, "[green]packs/%s/ matches the pin (%s).[/green]", pack, pin.Commit)
	} else {
		cprint(ctx.Stdout, "[green]Pinned packs/%s.source.yaml to %s (tree_digest %s).[/green]", pack, pin.Commit, pin.Digest)
	}
	return nil
}
