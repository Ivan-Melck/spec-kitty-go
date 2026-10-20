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
	mergedriver.DriverReviewCycle:      "Reconcile a ``review-cycle-N.md`` collision, best-effort, non-aborting.",
	mergedriver.DriverDecisionIndex:    "Union ``decisions/index.json`` entries keyed by ``decision_id`` (FR-009b/#5023).",
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
		Short:                "(deprecated) [REMOVED] Renamed to ``spec-kitty consolidate`` -- this stub always fails.",
		Long:                 "(deprecated)\n[REMOVED] Renamed to ``spec-kitty consolidate`` -- this stub always fails.",
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
