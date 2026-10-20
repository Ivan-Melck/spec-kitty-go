package mergedriver

import "os"

// Driver names (the hidden `spec-kitty <name>` commands and the git
// merge.<driver> registrations).
const (
	DriverEventLog         = "merge-driver-event-log"
	DriverMeta             = "merge-driver-meta"
	DriverTraces           = "merge-driver-traces"
	DriverIssueMatrix      = "merge-driver-issue-matrix"
	DriverAcceptanceMatrix = "merge-driver-acceptance-matrix"
	DriverReviewCycle      = "merge-driver-review-cycle"
	DriverDecisionIndex    = "merge-driver-decision-index"
)

// Names lists the seven drivers in upstream registration order.
var Names = []string{DriverEventLog, DriverMeta, DriverTraces, DriverAcceptanceMatrix, DriverIssueMatrix, DriverReviewCycle, DriverDecisionIndex}

// Bodies is MERGE_DRIVER_BODIES: the single table both the CLI shell and
// in-process replay resolve against. The meta body here is base-aware
// (two_way=False), like upstream's registry entry; use RunMeta or
// RunMetaFromEnv for the two-way opt-out.
var Bodies = map[string]Body{
	DriverEventLog:         RunEventLog,
	DriverMeta:             func(b, o, t string) (Outcome, error) { return RunMeta(b, o, t, false) },
	DriverTraces:           RunTraces,
	DriverIssueMatrix:      RunIssueMatrix,
	DriverAcceptanceMatrix: RunAcceptanceMatrix,
	DriverReviewCycle:      RunReviewCycle,
	DriverDecisionIndex:    RunDecisionIndex,
}

// CLIBody returns the body the hidden CLI command runs: identical to
// Bodies except merge-driver-meta, which reads MetaTwoWayEnv from the
// process environment.
func CLIBody(name string) Body {
	if name == DriverMeta {
		return RunMetaFromEnv(os.Getenv)
	}
	return Bodies[name]
}
