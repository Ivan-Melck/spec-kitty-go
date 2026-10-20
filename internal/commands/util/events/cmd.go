package events

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

const tailLong = "Tail a mission's event log (``status.events.jsonl``) as JSON lines on stdout.\n\n" +
	"Ordered dispatch (plan.md's CLI Surface section -- \"exactly four things, none of\nthem novel domain logic\"):\n\n" +
	"1. FR-004 usage check: ``--from-invariant`` without ``--from-offset``.\n" +
	"2. FR-009 mission resolve.\n" +
	"3. FR-013 resume validation (only when ``--from-offset`` is given).\n" +
	"4. Drive the core."

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "events", "Event log tailing commands")
		g.Add(&cli.Command{
			Name:  "tail",
			Short: "Tail a mission's event log (``status.events.jsonl``) as JSON lines on stdout.",
			Long:  tailLong,
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Required: true, Help: "Mission slug/handle whose status.events.jsonl to tail (no legacy feature-alias flag, C-003)."},
				{Name: "json", Kind: cli.Bool, Default: true, Help: "JSON output. Currently the only supported mode (accepted for parity with the issue's invocation and with `docs query --json`; no human-readable mode exists, so this flag has no observable effect either way)."},
				{Name: "once", Kind: cli.Bool, Help: "Poll exactly once and exit -- no generator, no sleep, ever."},
				{Name: "max-events", Kind: cli.Int, Help: "Stop the stream after emitting this many events/signals."},
				{Name: "from-offset", Kind: cli.Int, Help: "Resume from this byte offset in status.events.jsonl (FR-004/FR-013)."},
				{Name: "from-invariant", Kind: cli.String, Help: "Content invariant (SHA-256 hex digest) paired with --from-offset for cross-restart content verification (FR-004/FR-013). Requires --from-offset; supplying this alone is a usage error."},
			},
			Run: runTail,
		})
	})
}

func errEnvelope(w io.Writer, code, detail string) {
	fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("error", code, "detail", detail), jsonx.Options{Indent: -1}))
}

func traceback(w io.Writer, err error) error {
	fmt.Fprintf(w, "Traceback (most recent call last):\n%s\n", err.Error())
	return cli.Exit(1)
}

func runTail(ctx *cli.Context) error {
	if ctx.IsSet("from-invariant") && !ctx.IsSet("from-offset") {
		errEnvelope(ctx.Stderr, "usage_error", "--from-invariant requires --from-offset to anchor it")
		return cli.Exit(2)
	}
	root := project.Locate("")
	if root == "" {
		errEnvelope(ctx.Stderr, "repo_root_not_found", "Could not locate project root. Ensure you are inside a spec-kitty project (has .kittify/ or kitty-specs/).")
		return cli.Exit(1)
	}
	resolved, err := ResolveMission(ctx.String("mission"), root)
	if err != nil {
		fmt.Fprintln(ctx.Stdout, JSONErrorPayload(err))
		return cli.Exit(1)
	}
	logPath := EventLogPath(resolved.FeatureDir)
	cursor := Cursor{0, EmptyDigest}
	if ctx.IsSet("from-offset") {
		var inv *string
		if ctx.IsSet("from-invariant") {
			s := ctx.String("from-invariant")
			inv = &s
		}
		c, err := ValidateResume(logPath, int64(ctx.Int("from-offset")), inv)
		if err != nil {
			var rr *ResumeRefused
			if errors.As(err, &rr) {
				code := "invalid_resume_offset"
				if rr.Reason == "content_mismatch" {
					code = "resume_content_mismatch"
				}
				errEnvelope(ctx.Stderr, code, rr.Error())
				return cli.Exit(2)
			}
			return traceback(ctx.Stderr, err)
		}
		cursor = c
	}
	emit := func(ev *jsonx.Object) { Emit(ctx.Stdout, ev) }
	if ctx.Bool("once") {
		r, err := PollOnce(logPath, cursor)
		if err != nil {
			return traceback(ctx.Stderr, err)
		}
		for _, ev := range r.Events {
			emit(ev)
		}
		return nil
	}
	max := -1
	if ctx.IsSet("max-events") {
		max = ctx.Int("max-events")
		if max < 0 {
			return traceback(ctx.Stderr, &PyError{"ValueError", "Stop argument for islice() must be None or an integer: 0 <= x <= sys.maxsize."})
		}
	}
	if err := Tail(logPath, cursor, max, time.Sleep, emit); err != nil {
		return traceback(ctx.Stderr, err)
	}
	return nil
}
