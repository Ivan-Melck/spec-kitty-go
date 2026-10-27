package doctrine

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/charterenc"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func charterContextCmd() *cli.Command {
	return &cli.Command{
		Name:  "context",
		Short: "Render charter context for a specific workflow action.",
		Flags: []*cli.Flag{
			{Name: "action", Kind: cli.String, Help: "Workflow action (specify|plan|implement|review)"},
			{Name: "include", Kind: cli.String, Help: "Fetch selector, e.g. agent-profile:<id>, template:<mission>/<name>, directive:<id>, section:<slug>."},
			{Name: "mark-loaded", Kind: cli.Bool, Negatable: true, Default: true, Help: "Persist first-load state"},
			{Name: "mission-type", Kind: cli.String, Help: "Canonical mission type (e.g. documentation|research|plan|software-dev) for the action doctrine grain. Required when rendering action context from the repo root — without it, and without a mission's meta.json, the action grain is typeless and never inherits software-dev (#883)."},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON. `context_schema_version` stamps the top-level payload shape (#2787, tracking contract -- not yet frozen). `directives` is action-scoped; `all_directives` and `project_charter` describe the project-local charter, while `org_charter` describes imported org packs."},
			{Name: "include-all", Kind: cli.Bool, Help: "Escape hatch: materialise the entire reachable closure inline in the structured (--json) payload instead of the default progressive disclosure (requires eager, suggests linked). Output is a superset of the progressive render for the same grain."},
		},
		Run: catchCharterLoad(runCharterContext),
	}
}

// emitCharterError is upstream charter _common._emit_error.
func emitCharterError(w io.Writer, jsonMode bool, message, code string, extra *jsonx.Object) {
	if jsonMode {
		o := &jsonx.Object{}
		if extra != nil {
			for _, kv := range extra.Pairs() {
				o.Set(kv.Key, kv.Value)
			}
		}
		if code != "" {
			o.Set("code", code)
		}
		o.Set("result", "error")
		o.Set("success", false)
		o.Set("error", message)
		jsonOut(w, o, jsonx.Line)
		return
	}
	if code != "" {
		cprintRaw(w, fmt.Sprintf("[red]Error (%s):[/red] %s", rich.EscapeMarkup(code), rich.EscapeMarkup(message)))
		return
	}
	cprintRaw(w, "[red]Error:[/red] "+rich.EscapeMarkup(message))
}

func charterErr(ctx *cli.Context, jsonMode bool, err error) error {
	var ce *dct.ConfigError
	if errors.As(err, &ce) {
		emitCharterError(ctx.Stdout, jsonMode, ce.Body, ce.Code(), nil)
	} else {
		emitCharterError(ctx.Stdout, jsonMode, err.Error(), "", nil)
	}
	return cli.Exit(1)
}

// codedInvariant reports a KittyInternalConsistencyError other than
// ActiveCharterConfigError (rendered by its own branch): its code and body.
func codedInvariant(err error) (code, body string, ok bool) {
	var sm *charter.SynthesisMembershipUnreadableError
	var ee *charterenc.Error
	var dp *kittify.DefaultPresetError
	switch {
	case errors.As(err, &sm):
		return sm.Code(), sm.Body, true
	case errors.As(err, &ee):
		return ee.Code, ee.Body, true
	case errors.As(err, &dp):
		return dp.Code(), dp.Body, true
	}
	return "", "", false
}

func runCharterContext(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := findRepoRoot()
	if root == "" {
		emitCharterError(ctx.Stdout, jsonMode, repoRootMissing, "", nil)
		return cli.Exit(1)
	}
	if sel := ctx.String("include"); ctx.IsSet("include") && sel != "" {
		text, err := charter.BuildInclude(root, sel)
		if err != nil {
			return charterErr(ctx, jsonMode, err)
		}
		if jsonMode {
			jsonOut(ctx.Stdout, jsonx.NewObject("result", "success", "success", true, "include", sel, "context", text, "text", text), jsonx.Pretty)
			return nil
		}
		cprintPlain(ctx.Stdout, strings.TrimSuffix(text, "\n"))
		return nil
	}
	if !ctx.IsSet("action") {
		return charterErr(ctx, jsonMode, errors.New("--action is required unless --include is provided."))
	}
	mt := ctx.String("mission-type")
	res, err := charter.BuildContext(root, ctx.String("action"), charter.Options{MarkLoaded: ctx.Bool("mark-loaded"), MissionType: mt})
	if err != nil {
		if code, body, ok := codedInvariant(err); ok {
			// #6454: a coded invariant violation (KittyInternalConsistencyError,
			// e.g. SYNTHESIS_MEMBERSHIP_UNREADABLE) refuses with its body and
			// remedy; str(exc) is only the code.
			emitCharterError(ctx.Stdout, jsonMode, body, code, nil)
			return cli.Exit(1)
		}
		var ce *dct.ConfigError
		if !errors.As(err, &ce) && charter.IsUnexpectedError(err) {
			// A non-ValueError exception: `except Exception` -> "Unexpected error".
			if jsonMode {
				emitCharterError(ctx.Stdout, true, err.Error(), "", nil)
			} else {
				cprint(ctx.Stdout, "[red]Unexpected error:[/red] %s", uconsoleEscape(err.Error()))
			}
			return cli.Exit(1)
		}
		return charterErr(ctx, jsonMode, err)
	}
	if !jsonMode {
		for _, w := range res.Warnings {
			rich.LogWarning(ctx.Stderr, w)
		}
	}
	if jsonMode {
		jsonOut(ctx.Stdout, charter.ContextJSON(root, res, mt, ctx.Bool("include-all")), jsonx.Pretty)
		return nil
	}
	if res.Mode == "bootstrap" {
		cprintRaw(ctx.Stdout, fmt.Sprintf("Action: %s (%s)", res.Action, res.Mode))
	}
	cprintPlain(ctx.Stdout, res.Text)
	return nil
}
