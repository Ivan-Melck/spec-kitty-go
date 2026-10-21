// Package hiddencmd registers upstream's hidden framework commands.
package hiddencmd

import (
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

func init() {
	app.Register(func(root *cli.Command) {
		// Upstream's init module registers this no-op so typer keeps the
		// root in multi-command mode; invoking it does nothing (exit 0).
		root.Add(&cli.Command{
			Name:   "__force_multi_command_mode__",
			Hidden: true,
			Run:    func(*cli.Context) error { return nil },
		})
	})
}
