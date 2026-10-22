package runtime

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
)

// charterPreflightForNext is next_cmd._run_charter_preflight_for_next:
// advancing mode aborts on a failed preflight (JSON mode reports a
// CHARTER_PREFLIGHT_FAILED envelope on stdout); query mode logs the failure
// and continues.
func charterPreflightForNext(ctx *cli.Context, root string, advancing, jsonMode bool) error {
	if !advancing {
		var logf func(string)
		if !jsonMode {
			logf = func(msg string) { rich.Log(ctx.Stderr, "WARNING", msg) }
		}
		workflow.CharterPreflightWarnOnly(ctx.Stderr, root, "next", logf)
		return nil
	}
	if !jsonMode {
		if err := workflow.CharterPreflightOrAbort(ctx.Stderr, root, "next"); err != nil {
			return cli.Exit(1)
		}
		return nil
	}
	var buf bytes.Buffer
	if err := workflow.CharterPreflightOrAbort(&buf, root, "next"); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			msg = "charter preflight failed"
		}
		blocked := strings.TrimSpace(strings.TrimPrefix(msg, "Error: "))
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("error_code", "CHARTER_PREFLIGHT_FAILED", "error", msg, "blocked_reason", blocked), jsonx.Options{Indent: -1, EnsureASCII: true}))
		return cli.Exit(1)
	}
	ctx.Stderr.Write(buf.Bytes())
	return nil
}
