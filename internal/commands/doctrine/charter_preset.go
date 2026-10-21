package doctrine

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// renderCodedError is _coded_errors.render_coded_error.
func renderCodedError(w io.Writer, code, msg string, details []string, payload *jsonx.Object, jsonMode bool) {
	if jsonMode {
		e := jsonx.NewObject("code", code, "message", msg)
		if payload != nil {
			for _, kv := range payload.Pairs() {
				e.Set(kv.Key, kv.Value)
			}
		}
		jsonOut(w, jsonx.NewObject("ok", false, "error", e), jsonx.PrettyUTF8)
		return
	}
	cprintRaw(w, fmt.Sprintf("Error (%s): %s", code, msg))
	for _, l := range details {
		cprintRaw(w, l)
	}
}

func renderCharterErr(w io.Writer, err error, jsonMode bool) {
	var ce *charter.CodedError
	var pf *charter.PresetFormatError
	var cfg *dct.ConfigError
	switch {
	case errors.As(err, &ce):
		renderCodedError(w, ce.Code, ce.Msg, ce.Details, ce.Payload, jsonMode)
	case errors.As(err, &pf):
		renderCodedError(w, "PRESET_INVALID", pf.Error(), nil, jsonx.NewObject("preset_file", pf.Path, "field", pf.Field), jsonMode)
	case errors.As(err, &cfg):
		renderCodedError(w, cfg.Code(), cfg.Body, nil, nil, jsonMode)
	default:
		renderCodedError(w, "ACTIVE_CHARTER_CONFIG_INVALID", err.Error(), nil, nil, jsonMode)
	}
}

func runApplyPreset(ctx *cli.Context, preset string) error {
	w := ctx.Stdout
	jsonMode := ctx.Bool("json")
	root := charterRoot(ctx)
	if _, err := dct.LoadActivation(root); err != nil {
		renderCharterErr(w, err, jsonMode)
		return cli.Exit(1)
	}
	plan, err := charter.PlanPreset(root, ctx.String("pack"), preset)
	if err == nil {
		err = charter.ApplyPresetPlan(root, plan, ctx.Bool("force"))
	}
	if err != nil {
		renderCharterErr(w, err, jsonMode)
		return cli.Exit(1)
	}
	if jsonMode {
		var discard strings.Builder
		sub := *ctx
		sub.Stdout = &discard
		recompileOrNotify(&sub, root)
		jsonOut(w, charter.PresetPayload(plan), jsonx.PrettyUTF8)
		return nil
	}
	if plan.IsNoop() {
		cprintRaw(w, fmt.Sprintf("Preset '%s' of pack '%s' is already in force in %s; nothing changed.", plan.Preset, plan.Pack, plan.TargetFile))
	} else {
		cprintRaw(w, fmt.Sprintf("Applied preset '%s' of pack '%s' to %s", plan.Preset, plan.Pack, plan.TargetFile))
		for _, x := range plan.Written {
			ids := strings.Join(x[1].([]string), ", ")
			if ids == "" {
				ids = "(none)"
			}
			cprintRaw(w, fmt.Sprintf("  Wrote %s: %s", x[0], ids))
		}
		for _, k := range plan.Removed {
			cprintRaw(w, fmt.Sprintf("  Removed %s (unrestricted)", k))
		}
	}
	recompileOrNotify(ctx, root)
	return nil
}
