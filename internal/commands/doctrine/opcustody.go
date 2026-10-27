package doctrine

// Op custody at the command edge (cli/op_write_refusal.py and
// _owned_checkout.resolve_owned_op_record_root): the --owned-checkout option
// of dispatch / profile-invocation complete / invocations list, and the
// rendering of a pre-write Op admission refusal.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// Help of --owned-checkout on the Op commands, which are not bound to a Mission.
const (
	opRecordOwnedCheckoutHelp     = "Keep this Op's record in an owned checkout: a linked checkout of this repository. Refuses the repository root checkout. A record already in another checkout is closed where it lives."
	opRecordListOwnedCheckoutHelp = "List the Op records kept in an owned checkout: a linked checkout of this repository. Refuses the repository root checkout."
)

func ownedCheckoutFlag(help string) *cli.Flag {
	return &cli.Flag{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: help}
}

// resolveOwnedOpRecordRoot is resolve_owned_op_record_root: validate
// --owned-checkout once (the owned claim plus the repository-root refusal;
// no Mission is bound, so lane and coordination worktrees are not refused)
// and return the checkout that keeps the record; "" without the flag. A
// refusal prints the flat error envelope and exits 1 before anything is
// written.
func resolveOwnedOpRecordRoot(ctx *cli.Context, projectRoot string, jsonMode bool) (string, error) {
	if !ctx.IsSet("owned-checkout") {
		return "", nil
	}
	claim := ctx.String("owned-checkout")
	if !filepath.IsAbs(claim) {
		cwd, _ := os.Getwd()
		claim = filepath.Join(cwd, claim)
	}
	root := ownedcheckout.PyResolve(project.MainRepoRoot(projectRoot))
	cr, err := ownedcheckout.ResolveCreateRoot(root, claim)
	if err != nil {
		var oe *ownedcheckout.Error
		if errors.As(err, &oe) {
			ownedcheckout.Emit(ctx.Stdout, ctx.Stderr, oe, jsonMode, ownedcheckout.FlatErrorEnvelope)
			return "", cli.Exit(1)
		}
		return "", err
	}
	return cr.Checkout, nil
}

// invokingRecordRoot is the record root of an Op command: the owned checkout
// under --owned-checkout, else the invoking checkout (#6564).
func invokingRecordRoot(ctx *cli.Context, projectRoot string, jsonMode bool) (string, error) {
	owned, err := resolveOwnedOpRecordRoot(ctx, projectRoot, jsonMode)
	if err != nil || owned != "" {
		return owned, err
	}
	return invocation.ResolveRecordRoot(projectRoot, ""), nil
}

// exitOpWriteRefused is exit_op_write_refused: report the refusal on the
// command's error channel (JSON on stderr, rich text on stdout) and exit 1.
func exitOpWriteRefused(ctx *cli.Context, e *invocation.WriteRefusedError, jsonMode bool) error {
	if jsonMode {
		fmt.Fprintln(ctx.Stderr, jsonx.Dumps(jsonx.NewObject("error", "op_write_refused", "error_code", e.Refusal.Code,
			"message", e.Error(), "record_root", e.RecordRoot), lineJSON))
	} else {
		cprintRaw(ctx.Stdout, "[red]Error:[/red] "+rich.EscapeMarkup(e.Error()))
	}
	return cli.Exit(1)
}
