// Package plugin implements `spec-kitty plugin build`: stage a Claude Code
// or Codex plugin bundle (command skills, agent profiles, manifests,
// wrappers) under --output-dir with a .spec-kitty-bundle.json ownership
// ledger, never installing or publishing it.
package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

const buildLong = "Build a Spec Kitty plugin bundle for a specific target harness.\n\n" +
	"The bundle is written to ``<output-dir>/<target>/`` and includes a\n" +
	"``plugin.json`` manifest, rendered command skills, and agent profiles.\n\n" +
	"Example::\n\n" +
	"    spec-kitty plugin build --target claude-code\n" +
	"    spec-kitty plugin build --target claude-code --output-dir /tmp/out\n" +
	"    spec-kitty plugin build --target claude-code --skip-validate"

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "plugin", "Plugin bundle commands")
		g.Add(&cli.Command{
			Name:  "build",
			Short: "Build a Spec Kitty plugin bundle for a specific target harness.",
			Long:  buildLong,
			Flags: []*cli.Flag{
				{Name: "target", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Plugin target (claude-code, codex)."},
				{Name: "output-dir", Kind: cli.String, Metavar: "<path>", Default: "dist/spec-kitty-plugins", Help: "Root directory under which the bundle is written."},
				{Name: "skip-validate", Kind: cli.Bool, Help: "Skip the 'claude plugin validate --strict' step."},
			},
			Run: runBuild,
		})
	})
}

// crash prints an uncaught build exception as upstream's rich traceback;
// line is the plugin_build line (specify_cli/cli/commands/plugin.py) of the
// projector's build() call.
func crash(ctx *cli.Context, line int, err error) error {
	frame := rich.Frame{Name: "plugin_build", Line: line}
	var be *BuildError
	var pe *pyError
	switch {
	case errors.As(err, &be):
		rich.Traceback(ctx.Stderr, "BuildError", be.Msg, frame)
	case errors.As(err, &pe):
		rich.Traceback(ctx.Stderr, pe.Type, pe.Msg, frame)
	default:
		rich.Traceback(ctx.Stderr, "OSError", err.Error(), frame)
	}
	return cli.Exit(1)
}

func runBuild(ctx *cli.Context) error {
	target := ctx.String("target")
	out := ctx.String("output-dir")
	skip := ctx.Bool("skip-validate")
	switch target {
	case "claude-code":
		dir, err := BuildClaude(ctx.Stderr, out)
		if err != nil {
			return crash(ctx, 62, err)
		}
		if err := validateClaude(ctx, dir, skip); err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stdout, "Bundle written to %s\n", dir)
	case "codex":
		dir, err := BuildCodex(ctx.Stderr, out)
		if err != nil {
			return crash(ctx, 69, err)
		}
		if skip {
			fmt.Fprintln(ctx.Stderr, "Warning: Skipping Codex plugin validation (--skip-validate passed).")
		}
		fmt.Fprintf(ctx.Stdout, "Codex marketplace.json written to %s/marketplace.json\n", dir)
		fmt.Fprintln(ctx.Stdout, "  To install: codex plugin marketplace add dist/spec-kitty-plugins/codex/marketplace.json")
		fmt.Fprintln(ctx.Stdout, "  Or:         codex plugin install dist/spec-kitty-plugins/codex")
		fmt.Fprintf(ctx.Stdout, "Bundle written to %s\n", dir)
	default:
		return cli.Usage(ctx.Cmd, "Invalid value: Unknown target: %s. Supported: claude-code, codex.", pyjson.ReprString(target))
	}
	return nil
}

// validateClaude runs `claude plugin validate --strict <bundle>` (skipped
// when asked or when the claude CLI is not on PATH).
func validateClaude(ctx *cli.Context, dir string, skip bool) error {
	if skip {
		fmt.Fprintln(ctx.Stderr, "Warning: Skipping claude plugin validate (--skip-validate passed).")
		return nil
	}
	exe, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintln(ctx.Stderr, "Warning: claude CLI not found — skipping validation. Install claude CLI to validate.")
		return nil
	}
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, exe, "plugin", "validate", "--strict", dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if c.Err() != nil {
		rich.Traceback(ctx.Stderr, "TimeoutExpired", fmt.Sprintf("Command '['claude', 'plugin', 'validate', '--strict', %s]' timed out after 30 seconds", pyjson.ReprString(dir)),
			rich.Frame{Name: "plugin_build", Line: 62})
		return cli.Exit(1)
	}
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			fmt.Fprintln(ctx.Stderr, "Warning: claude CLI not found — skipping validation. Install claude CLI to validate.")
			return nil
		}
		fmt.Fprintln(ctx.Stderr, "claude plugin validate --strict FAILED:")
		if stdout.Len() > 0 {
			fmt.Fprintln(ctx.Stderr, stdout.String())
		}
		if stderr.Len() > 0 {
			fmt.Fprintln(ctx.Stderr, stderr.String())
		}
		return cli.Exit(1)
	}
	fmt.Fprintln(ctx.Stdout, "claude plugin validate --strict passed.")
	return nil
}
