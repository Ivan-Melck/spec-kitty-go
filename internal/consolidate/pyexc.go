package consolidate

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Frames of the uncaught tracebacks (rendered sourceless; upstream shows
// site-packages paths and source excerpts).
var (
	forecastFrames = []rich.Frame{{Name: "wrapper", Line: 216}, {Name: "consolidate", Line: 1135}, {Name: "run_consolidate", Line: 1347},
		{Name: "run_dry_run_forecast", Line: 323}, {Name: "_preview_review_artifacts", Line: 118},
		{Name: "run_review_artifact_consistency_preflight", Line: 325}, {Name: "find_rejected_review_artifact_conflicts", Line: 208},
		{Name: "materialize_snapshot", Line: 399}, {Name: "read_event_stream", Line: 822}}
	mergeFrames = []rich.Frame{{Name: "wrapper", Line: 216}, {Name: "consolidate", Line: 1135}, {Name: "run_consolidate", Line: 1363},
		{Name: "_run_real_merge", Line: 829}, {Name: "_run_lane_based_consolidation", Line: 776}}
	driftFrames = append(append([]rich.Frame(nil), mergeFrames...), rich.Frame{Name: "_warn_on_materialization_drift", Line: 685},
		rich.Frame{Name: "validate_materialization_drift", Line: 346}, rich.Frame{Name: "materialize_snapshot", Line: 399},
		rich.Frame{Name: "read_event_stream", Line: 822})
)

// pyException maps a Go error to the Python exception upstream raises for
// the same failure (type, str(exc), chained cause).
func pyException(err error, frames []rich.Frame) *rich.Exception {
	var se *status.StoreError
	var de *pyjson.DecodeError
	var ue *pyjson.UnicodeError
	var oe *pyjson.OSError
	switch {
	case errors.As(err, &se):
		exc := &rich.Exception{Type: "StoreError", Value: se.Msg, Frames: append(frames, rich.Frame{Name: "_partition_event_stream_from_text", Line: 689})}
		if se.Cause != nil {
			exc.Cause = pyException(se.Cause, []rich.Frame{{Name: "_partition_event_stream_from_text", Line: 687},
				{Name: "loads", Line: 352}, {Name: "decode", Line: 345}, {Name: "raw_decode", Line: 361}})
		}
		return exc
	case errors.As(err, &de):
		return &rich.Exception{Type: "JSONDecodeError", Value: de.Error(), Frames: frames}
	case errors.As(err, &ue):
		return &rich.Exception{Type: "UnicodeDecodeError", Value: ue.Error(), Frames: append(frames, rich.Frame{Name: "read_text", Line: 546}, rich.Frame{Name: "decode", Line: 325})}
	case errors.As(err, &oe):
		return &rich.Exception{Type: oe.Type, Value: oe.Error(), Frames: append(frames, rich.Frame{Name: "read_text", Line: 546},
			rich.Frame{Name: "read_text", Line: 632}, rich.Frame{Name: "open", Line: 537})}
	}
	return rich.ParseException(err.Error(), frames...)
}

// uncaught prints err as an uncaught exception (rich traceback on stderr)
// and exits 1.
func uncaught(stderr io.Writer, err error, frames []rich.Frame) error {
	if stderr == nil {
		stderr = os.Stderr
	}
	rich.PrintTraceback(stderr, pyException(err, append([]rich.Frame(nil), frames...)))
	return exit(1)
}

// warnOnMaterializationDrift is _warn_on_materialization_drift: a present
// status.json that disagrees with its event log is reported (never refused).
func (r *run) warnOnMaterializationDrift() error {
	dir := r.sf.StatusDir()
	if _, err := os.Stat(filepath.Join(dir, status.SnapshotFile)); err != nil {
		return nil
	}
	findings, err := status.ValidateMaterializationDrift(dir)
	if err != nil {
		return uncaught(r.opts.Stderr, err, driftFrames)
	}
	var drift []string
	for _, f := range findings {
		if len(f) >= len("Materialization drift:") && f[:len("Materialization drift:")] == "Materialization drift:" {
			drift = append(drift, f)
		}
	}
	if len(drift) == 0 {
		return nil
	}
	say(r.w, "[yellow]Warning: this Mission's status.json has drifted from its event log (the event log is the sole authority for lane state); the landed snapshot may be dishonest.[/yellow]")
	for _, f := range drift {
		say(r.w, "  [yellow]•[/yellow] "+rich.EscapeMarkup(f))
	}
	say(r.w, "Re-materialize it with [bold]spec-kitty agent status materialize --mission "+rich.EscapeMarkup(r.slug)+"[/bold] and commit the refreshed status.json.")
	return nil
}
