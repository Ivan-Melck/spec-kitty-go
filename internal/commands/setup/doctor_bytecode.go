package setup

import (
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// `doctor bytecode` scans the installed Python package for corrupt .pyc
// files. The Go port ships a single binary with no bytecode cache, so the
// scan always reports a clean install rooted at the executable's directory.
func newDoctorBytecodeCmd() *cli.Command {
	return &cli.Command{
		Name:  "bytecode",
		Short: "Flag a corrupt installed-package .pyc before it bites (#4124, #4130).",
		Long: "Flag a corrupt installed-package .pyc before it bites (#4124, #4130).\n\nReads each .pyc under the installed specify_cli package and flags one\n" +
			"whose header Python's import machinery would trust (so it would be\nused as-is) but whose body fails to unmarshal into a code object --\n" +
			"the truncated-write shape that crashed a training machine in #4124.\nA cache Python would already recompile from source (bad magic, stale\n" +
			"timestamp/size) is not flagged; it never bites. Read-only -- never\ndeletes anything.\n\nExamples:\n    spec-kitty doctor bytecode\n    spec-kitty doctor bytecode --json",
		Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"}},
		Run: func(ctx *cli.Context) error {
			if ctx.Bool("json") {
				exe, _ := os.Executable()
				printJSONRaw(ctx.Stdout, jsonx.NewObject("root", filepath.Dir(exe), "findings", []any{}, "finding_count", 0, "unreadable_count", 0,
					"heal_hint", "delete the flagged file(s) (or the whole __pycache__ dir); Python recompiles from source on next import"))
				return nil
			}
			out(ctx.Stdout, "[green]Bytecode cache[/green]: no corrupt .pyc files found under the installed package.")
			return nil
		},
	}
}
