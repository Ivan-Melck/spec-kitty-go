package planning

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
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

// withCLIVersion is _with_cli_version: spec_kitty_version appended.
func withCLIVersion(o *jsonx.Object) *jsonx.Object {
	if !o.Has("spec_kitty_version") {
		o.Set("spec_kitty_version", app.Version)
	}
	return o
}

// pyStrOrEmpty is str(meta.get(key) or "").
func pyStrOrEmpty(meta *jsonx.Object, key string) string {
	v, _ := meta.Get(key)
	if !pyjson.Truthy(v) {
		return ""
	}
	return pyjson.Str(v)
}
