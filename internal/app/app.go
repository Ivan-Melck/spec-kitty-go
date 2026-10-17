// Package app assembles the spec-kitty command tree. Command packages call
// Register from init(); cmd/spec-kitty blank-imports them.
package app

import (
	"fmt"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/cli"
)

// Version is the upstream CLI version this port is functionally aligned with.
const Version = "4.0.0rc8"

// Registrar adds commands to the root.
type Registrar struct {
	Order int // lower runs first (groups that others extend use < 0)
	Fn    func(root *cli.Command)
}

var registrars []Registrar

// Register adds a registrar.
func Register(fn func(root *cli.Command)) { registrars = append(registrars, Registrar{Fn: fn}) }

// RegisterOrdered adds a registrar with an explicit order.
func RegisterOrdered(order int, fn func(root *cli.Command)) {
	registrars = append(registrars, Registrar{Order: order, Fn: fn})
}

// Group returns parent's subcommand name, creating an empty group if needed.
func Group(parent *cli.Command, name, short string) *cli.Command {
	if g := parent.Find(name); g != nil {
		if g.Short == "" {
			g.Short = short
		}
		return g
	}
	g := &cli.Command{Name: name, Short: short}
	parent.Add(g)
	return g
}

// Root builds the full command tree.
func Root() *cli.Command {
	root := &cli.Command{
		Name: "spec-kitty",
		Long: "Setup tool for Spec Kitty spec-driven development projects.\n\nSet SPEC_KITTY_NO_UPGRADE_CHECK=1 to disable the upgrade-check notice.",
		Flags: []*cli.Flag{
			{Name: "version", Short: "v", Kind: cli.Bool, Help: "Show version and exit"},
			{Name: "install-completion", Kind: cli.Bool, Help: "Install completion for the current shell."},
			{Name: "show-completion", Kind: cli.Bool, Help: "Show completion for the current shell, to copy it or customize the installation."},
		},
	}
	root.Callback = func(ctx *cli.Context) error {
		if ctx.Bool("version") {
			fmt.Fprintf(ctx.Stdout, "spec-kitty-cli version %s\n", Version)
			return cli.Stop()
		}
		if RootCallback != nil {
			return RootCallback(ctx)
		}
		return nil
	}
	sort.SliceStable(registrars, func(i, j int) bool { return registrars[i].Order < registrars[j].Order })
	for _, r := range registrars {
		r.Fn(root)
	}
	sort.SliceStable(root.Subcommands, func(i, j int) bool { return root.Subcommands[i].Name < root.Subcommands[j].Name })
	return root
}

// RootCallback runs after root options are parsed (startup bootstrap).
var RootCallback func(ctx *cli.Context) error
