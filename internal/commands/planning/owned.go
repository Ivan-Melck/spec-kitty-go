package planning

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
)

// ownedClaim returns the raw --owned-checkout value (nil when not given).
func ownedClaim(ctx *cli.Context) *string {
	if !ctx.IsSet("owned-checkout") {
		return nil
	}
	v := ctx.String("owned-checkout")
	return &v
}

// resolveOwnedOrRefuse is _owned_checkout.resolve_owned_or_refuse: the
// validated fact (nil when neither --owned-checkout nor flagless adoption
// applies); a refusal is rendered through envelope (emit_owned_refusal) and
// returned as exit 1. A non-refusal error (corrupt meta.json) is returned
// unrendered.
func resolveOwnedOrRefuse(ctx *cli.Context, root, handle string, jsonMode bool, envelope ownedcheckout.Envelope, opts ownedcheckout.Options) (*ownedcheckout.Fact, error) {
	cwd, _ := os.Getwd()
	f, err := ownedcheckout.ResolveOrAdopt(root, ownedClaim(ctx), handle, cwd, opts)
	if err != nil {
		if e, ok := err.(*ownedcheckout.Error); ok {
			ownedcheckout.Emit(ctx.Stdout, ctx.Stderr, e, jsonMode, envelope)
			return nil, cli.Exit(1)
		}
		return nil, err
	}
	return f, nil
}
