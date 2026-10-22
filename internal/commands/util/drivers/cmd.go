// Package drivers registers the seven hidden `spec-kitty merge-driver-*`
// commands (thin adapters over internal/mergedriver) and the hidden,
// deprecated `merge` stub.
package drivers

import (
	"errors"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/mergedriver"
)

// Upstream docstrings (the commands' --help text).
var helpText = map[string]string{
	mergedriver.DriverEventLog: "Merge ``status.events.jsonl`` conflict inputs using event-log semantics.",
	mergedriver.DriverMeta:     "Field-merge conflicting ``meta.json`` blobs; write result to ``ours``.",
	mergedriver.DriverTraces: "Union conflicting ``traces/*.md`` documents; write result to ``ours`` (#4894).\n\n" +
		"3-way base-aware: reads ``%O`` so a section theirs left UNCHANGED from\n" +
		"base, while ours edited the same section, is recognized as stale and\n" +
		"dropped rather than resurrected alongside ours' edit (see\n" +
		":func:`specify_cli.consolidation.drivers._drop_stale_theirs_trace_blocks`). The\n" +
		"remaining union is still section-granularity and append-only via\n" +
		":func:`specify_cli.consolidation.drivers.union_trace_texts` -- never a lossy\n" +
		"line-level global dedup, never fail-closed on an ordinary repeat.",
	mergedriver.DriverIssueMatrix:      "Row-aware, 3-way merge of ``issue-matrix.json``; write result to ``ours`` (FR-008).",
	mergedriver.DriverAcceptanceMatrix: "Row-aware, 3-way merge of ``acceptance-matrix.json``; write result to ``ours`` (FR-008).",
	mergedriver.DriverReviewCycle: "Reconcile a ``review-cycle-N.md`` collision, best-effort, non-aborting.\n" +
		"\n" +
		"Two distinct verdict documents colliding under the same filename are\n" +
		"NEVER unioned/field-merged/interleaved into one document -- see the\n" +
		"module-level design-decision comment in ``specify_cli.consolidation.drivers``,\n" +
		"immediately above :func:`specify_cli.consolidation.drivers.run_review_cycle_driver`,\n" +
		"for the full reasoning (embed both verbatim, never fabricate a blended\n" +
		"verdict). Unlike WP18's original T077 driver, a divergent collision no\n" +
		"longer aborts the squash (FR-014/D-PLAN-6): the ``.md`` render is\n" +
		"non-authoritative, unread prose now that ``status.events.jsonl``'s\n" +
		"``review_result`` event slot is the sole verdict authority, so refusing\n" +
		"the merge over it is no longer justified.\n" +
		"\n" +
		"Identical content on both sides (byte-for-byte) is the trivial fast path:\n" +
		"resolves cleanly, exit 0, never reported as a conflict. Otherwise, both\n" +
		"raw documents are embedded verbatim inside standard git-style conflict\n" +
		"markers (never blended field-by-field -- a review verdict has no safely\n" +
		"mergeable sub-fields the way a JSON matrix row does) and the driver\n" +
		"exits 0, so ``git merge --squash`` treats the path as resolved\n" +
		"and the squash proceeds.",
	mergedriver.DriverDecisionIndex: "Union ``decisions/index.json`` entries keyed by ``decision_id`` (FR-009b/#5023).\n" +
		"\n" +
		"See ``specify_cli.consolidation.drivers.run_decision_index_driver``'s\n" +
		"docstring for the full collision/precedence semantics: a terminal status\n" +
		"(resolved/deferred/canceled) beats ``open``; the one legal reopen pair\n" +
		"(deferred -> resolved) takes the reopen target; any other divergence, or\n" +
		"malformed input, is a conflict (non-zero exit) -- never a silently\n" +
		"picked side.",
}

func init() {
	app.Register(func(root *cli.Command) {
		for _, name := range mergedriver.Names {
			name := name
			root.Add(&cli.Command{
				Name:   name,
				Short:  firstLine(helpText[name]),
				Long:   helpText[name],
				Hidden: true,
				Args: []*cli.Arg{
					{Name: "base", Metavar: "BASE", Required: true},
					{Name: "ours", Metavar: "OURS", Required: true},
					{Name: "theirs", Metavar: "THEIRS", Required: true},
				},
				Run: func(ctx *cli.Context) error {
					return run(ctx, mergedriver.CLIBody(name))
				},
			})
		}
		root.Add(mergeStub())
	})
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// run is merge_driver._run: a *mergedriver.Error is echoed to stderr
// verbatim (exit 1); a notice is echoed to stdout (exit 0). An upstream
// uncaught exception (*mergedriver.Crash) also exits 1, with the final
// traceback line on stderr.
func run(ctx *cli.Context, body mergedriver.Body) error {
	out, err := body(ctx.Arg("base"), ctx.Arg("ours"), ctx.Arg("theirs"))
	if err != nil {
		var de *mergedriver.Error
		if errors.As(err, &de) {
			fmt.Fprintln(ctx.Stderr, de.Msg)
			return cli.Exit(1)
		}
		var crash *mergedriver.Crash
		if errors.As(err, &crash) {
			fmt.Fprintf(ctx.Stderr, "Traceback (most recent call last):\n%s\n", crash.Error())
			return cli.Exit(1)
		}
		return err
	}
	if out.Notice != "" {
		fmt.Fprintln(ctx.Stdout, out.Notice)
	}
	return nil
}

// mergeStub is the hidden, deprecated `merge` command (renamed to
// `consolidate`): it accepts any arguments and always fails.
func mergeStub() *cli.Command {
	return &cli.Command{
		Name:                 "merge",
		Short:                "[REMOVED] Renamed to ``spec-kitty consolidate`` -- this stub always fails.",
		Deprecated:           true,
		Hidden:               true,
		AllowExtraArgs:       true,
		IgnoreUnknownOptions: true,
		Run: func(ctx *cli.Context) error {
			fmt.Fprintln(ctx.Stderr, "DeprecationWarning: The command 'merge' is deprecated.")
			fmt.Fprintln(ctx.Stdout, "Error: The `merge` command has been renamed to `spec-kitty consolidate`. Re-run with `spec-kitty consolidate` (same flags).")
			return cli.Exit(1)
		},
	}
}
