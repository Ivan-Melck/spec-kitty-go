package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// LegacyCharterGate shares the cutover migration's structural predicate.
// RC7 writes the refusal to stderr even when the command requests JSON.
func LegacyCharterGate(ctx *cli.Context, argv []string) error {
	first := firstCommandToken(argv)
	switch first {
	case "upgrade", "init", "session-start", "session-stop", "commit-guard-hook":
		return nil
	}
	if strings.HasPrefix(first, "merge-driver-") {
		return nil
	}
	pos := positionals(argv)
	if len(pos) >= 2 && pos[0] == "live-work" && pos[1] == "hook" {
		return nil
	}
	for _, a := range argv {
		if a == "--help" || a == "-h" {
			return nil
		}
	}
	if len(argv) > 0 && (argv[0] == "--version" || argv[0] == "-v") {
		return nil
	}
	root := project.Locate("")
	if root == "" {
		return nil
	}
	checkout := ""
	if cwd, err := os.Getwd(); err == nil {
		for p := cwd; ; p = filepath.Dir(p) {
			if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				checkout = p
				break
			}
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	for i, candidate := range []string{root, checkout} {
		if candidate == "" || (i == 1 && sameDirectory(candidate, root)) {
			continue
		}
		if st, err := os.Stat(filepath.Join(candidate, ".kittify")); err != nil || !st.IsDir() {
			continue
		}
		findings := detectLegacyCharterLayout(candidate)
		if len(findings) == 0 {
			continue
		}
		if err := cli.ProbeOptionErrors(ctx.Cmd, argv); err != nil {
			return err
		}
		message := fmt.Sprintf("Error (LEGACY_CHARTER_STATE): This project uses the retired doctrine layout (%s).\nRun `spec-kitty upgrade` to migrate it.", findings[0])
		if i == 1 {
			message += " In a lane worktree, upgrade the\nrepository root and merge the target into this lane (do not rebase)."
		}
		fmt.Fprintln(ctx.Stderr, message+"\nSee docs/migrations/charter-pack-cutover.md.")
		return cli.Exit(1)
	}
	return nil
}

func sameDirectory(a, b string) bool {
	left, le := os.Stat(a)
	right, re := os.Stat(b)
	if le == nil && re == nil {
		return os.SameFile(left, right)
	}
	return a == b
}
