// Package setup holds area B's commands (init, agent config, upgrade,
// verify-setup, config, session hooks, doctor setup leaves) and installs the
// startup bootstrap upstream runs in main_callback.
package setup

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

func init() {
	prev := app.RootCallback
	app.RootCallback = func(ctx *cli.Context) error {
		if prev != nil {
			if err := prev(ctx); err != nil {
				return err
			}
		}
		rootUpgradeNotices(ctx, os.Args[1:])
		if err := StartupBootstrap(ctx, os.Args[1:]); err != nil {
			return err
		}
		if os.Getenv("SPEC_KITTY_GO_SKIP_BOOTSTRAP") == "1" {
			return nil
		}
		if err := SchemaGate(ctx, os.Args[1:]); err != nil {
			return err
		}
		return LegacyCharterGate(ctx, os.Args[1:])
	}
}

// positionals returns argv entries that do not start with '-'.
func positionals(argv []string) []string {
	var out []string
	for _, a := range argv {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// IsNextInvocation is _is_next_invocation.
func IsNextInvocation(argv []string) bool {
	for _, a := range argv {
		if a == "--help" || a == "-h" {
			return false
		}
		if a == "next" {
			return true
		}
		if !strings.HasPrefix(a, "-") {
			return false
		}
	}
	return false
}

// SkipsBootstrap reports whether argv takes one of upstream's startup fast
// paths (next, live-work hook, session-start) or defers the global repair
// (upgrade handles it itself, migrate defers root bootstrap).
func SkipsBootstrap(argv []string) bool {
	if IsNextInvocation(argv) {
		return true
	}
	p := positionals(argv)
	if len(p) >= 2 && p[0] == "live-work" && p[1] == "hook" {
		return true
	}
	if len(p) >= 1 && (p[0] == "session-start") {
		return true
	}
	// upgrade/migrate: first token naming the command (resolve_command uses
	// the first positional of the root command line).
	if first := firstCommandToken(argv); first == "upgrade" || first == "migrate" {
		return true
	}
	return false
}

func firstCommandToken(argv []string) string {
	for _, a := range argv {
		if a == "--" {
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func isDoctorSkills(argv []string) bool {
	p := positionals(argv)
	return len(p) >= 2 && p[0] == "doctor" && p[1] == "skills"
}

// StartupBootstrap runs ensure_runtime, ensure_global_agent_skills and
// (except for `doctor skills`) ensure_global_agent_commands.
func StartupBootstrap(ctx *cli.Context, argv []string) error {
	if os.Getenv("SPEC_KITTY_GO_SKIP_BOOTSTRAP") == "1" || SkipsBootstrap(argv) {
		return nil
	}
	warnings, err := bootstrap.EnsureAll(bootstrap.Options{SkipCommands: isDoctorSkills(argv)})
	for _, w := range warnings {
		fmt.Fprintf(ctx.Stderr, "Warning: preserved unmigrated command file %s\n", w)
	}
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "Error: %v\n", err)
		return cli.Exit(1)
	}
	return nil
}
