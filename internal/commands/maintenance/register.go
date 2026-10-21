package maintenance

import (
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(newMaterializeCmd())
		registerMigrate(root)
	})
}
