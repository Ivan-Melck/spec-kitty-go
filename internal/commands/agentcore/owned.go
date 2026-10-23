package agentcore

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

// resolveOwned is resolve_owned_or_adopt + emit_owned_refusal: the fact
// (nil when neither the flag nor adoption applies), or an exit error after
// the refusal was rendered through envelope. A corrupt meta.json
// (MissionMetaReadError) is returned unrendered.
func resolveOwned(ctx *cli.Context, root, handle string, jsonMode bool, envelope ownedcheckout.Envelope, opts ownedcheckout.Options) (*ownedcheckout.Fact, error) {
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
