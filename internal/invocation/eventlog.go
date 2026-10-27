package invocation

// The Op trail's interim locked appends (kernel.event_log_append, #6606),
// through the shared event-log primitive.

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/eventlog"
)

// recordMode is _RECORD_MODE: Op-trail files are owner-only.
const recordMode os.FileMode = 0o600

// eventLogLock is event_log_lock: hold the interim lock anchored on anchor
// while fn runs.
func eventLogLock(anchor string, fn func() error) error {
	return eventlog.WithLock(anchor, eventlog.DefaultTimeout, fn)
}

// AppendEventLogLine is append_event_log_lines(log, [line],
// create_parents=True) for the other invocation-family logs (the
// profile-invocation lifecycle log): one locked append that refuses a torn
// last line. A refusal is an *eventlog.Error.
func AppendEventLogLine(log, line string) error {
	return eventlog.Append(log, []string{line}, eventlog.Options{CreateParents: true})
}
