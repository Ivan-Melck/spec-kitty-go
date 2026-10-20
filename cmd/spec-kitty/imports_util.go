package main

// D0: standalone utility commands (see docs/parity/util.md).
import (
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/commitguard"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/cutover"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/docs"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/drivers"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/encoding"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/events"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/lint"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/plugin"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/validatetasks"
	_ "github.com/spec-kitty/go-kitty3/internal/commands/util/workflow"
)
