package charter

import (
	"fmt"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

// WarnLog receives the logger.warning records upstream's charter modules
// emit (merge_three_layers override/dangling/rationale warnings, ...).
// Commands install a sink that renders them with rich.LogWarning on stderr
// in human mode; the CLI logging bootstrap silences them under --json, so
// the default (nil) discards them.
var WarnLog func(msg string)

// PyWarnLog receives Python `warnings.warn` records routed through
// logging.captureWarnings: (module path, line, category, message, source
// line). Nil discards them.
var PyWarnLog func(module string, line int, category, msg, source string)

func warnLog(format string, a ...any) {
	if WarnLog != nil {
		WarnLog(fmt.Sprintf(format, a...))
	}
}

// pyWarnSeen is the warnings registry ("default" action: one emission per
// (message, category, module, lineno) per process).
var pyWarnSeen = map[string]bool{}

// pyWarnCapture > 0 while inside a warnings.catch_warnings(record=True)
// block: records are captured, not emitted.
var pyWarnCapture int

// catchWarnings runs fn like `with warnings.catch_warnings(record=True)`:
// nothing is emitted inside, and entering/leaving mutates the filters,
// which invalidates every warnings registry (dedupe starts over).
func catchWarnings(fn func()) {
	pyWarnSeen = map[string]bool{}
	pyWarnCapture++
	defer func() {
		pyWarnCapture--
		pyWarnSeen = map[string]bool{}
	}()
	fn()
}

func pyWarn(module string, line int, category, msg, source string) {
	if pyWarnCapture > 0 {
		return
	}
	key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", module, line, category, msg)
	if pyWarnSeen[key] {
		return
	}
	pyWarnSeen[key] = true
	if PyWarnLog != nil {
		PyWarnLog(module, line, category, msg, source)
	}
}

// warnDiscardedEdgeRationale is merge._warn_discarded_edge_rationale.
func warnDiscardedEdgeRationale(kept MergedEdge, keptSource string, keptReason, discarded *string, source string) {
	if discarded == nil || *discarded == "" || keptReason == nil || *keptReason == "" || *discarded == *keptReason {
		return
	}
	if source == keptSource {
		warnLog("Org doctrine %s declares the edge (%s --%s--> %s) twice with different reasons, so it is collapsed to one edge and only the first rationale survives. Discarded: %s (kept: %s). Keep one declaration and fold both rationales into it.",
			dct.PyRepr(source), kept.Source, kept.Relation, kept.Target, dct.PyRepr(*discarded), dct.PyRepr(*keptReason))
		return
	}
	warnLog("Org doctrine %s restates the edge (%s --%s--> %s) already contributed by %s, so it is collapsed to one edge and only the first rationale survives. Discarded reason from %s: %s (kept: %s). Reconcile the two packs onto a single rationale — the triple can only carry one edge.",
		dct.PyRepr(source), kept.Source, kept.Relation, kept.Target, dct.PyRepr(keptSource), dct.PyRepr(source), dct.PyRepr(*discarded), dct.PyRepr(*keptReason))
}
