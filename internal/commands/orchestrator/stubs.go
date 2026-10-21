package orchestrator

import (
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

func notYet(name string) func(*cli.Context) error {
	return func(ctx *cli.Context) error {
		return newVerb(ctx, name).fail("UNSUPPORTED_CAPABILITY", "not supported by this build", nil)
	}
}

const helpOriginCheck = "Refuse (enforce, the default) or only warn (warn) when the mission's status evidence or approved lanes are behind or unreachable on their remote; off contacts nothing and accepts stale evidence. Overrides SPEC_KITTY_ORIGIN_CHECK."

func originCheckFlag() *cli.Flag {
	return &cli.Flag{Name: "origin-check", Kind: cli.String, Metavar: "<function>", Choices: []string{"enforce", "warn", "off"}, Help: helpOriginCheck}
}

func acceptMissionCmd() *cli.Command {
	return &cli.Command{Name: "accept-mission", Short: "Accept a mission after all WPs are approved or done.",
		Flags: []*cli.Flag{missionFlag(), str("actor", helpActor, true), originCheckFlag()}, Run: runAcceptMission}
}

func consolidateMissionCmd() *cli.Command {
	return &cli.Command{Name: "consolidate-mission", Short: "Consolidate a lane-based mission into target.",
		Flags: []*cli.Flag{missionFlag(),
			str("target", "Target branch to merge into (auto-detected from meta.json)", false),
			{Name: "strategy", Kind: cli.String, Metavar: "<str>", Default: "merge", Help: "Merge strategy: merge, squash, or rebase"},
			boolFlag("push", "Push target branch after merge"),
			originCheckFlag()},
		Run: runConsolidateMission}
}

func designStatusCmd() *cli.Command {
	return &cli.Command{Name: "design-status",
		Short: "Read-only design-phase status query (FR-010) -- mirrors ``list-ready``'s\nno-state-transition, no-event-emission, no-``--policy`` contract for the\ndesign pipeline instead of the WP loop.",
		Flags: []*cli.Flag{missionFlag()}, Run: runDesignStatus}
}

func artifactReadCmd() *cli.Command {
	return &cli.Command{Name: "artifact-read", Short: "Read bounded canonical content and its exact revision (absent is explicit).",
		Flags: []*cli.Flag{missionFlag(), str("kind", "Registered authoring artifact kind", true), str("artifact-id", "Declared package key or contract basename", false)},
		Run:   runArtifactRead}
}

func artifactSubmitCmd() *cli.Command {
	return &cli.Command{Name: "artifact-submit", Short: "Submit design content through host placement, validators and commit authority.",
		Flags: []*cli.Flag{missionFlag(), str("request-json", "Closed bounded artifact batch JSON; '-' reads UTF-8 stdin", true),
			str("actor", "Accountable author", true), str("policy", helpPolicy, false)},
		Run: notYet("artifact-submit")}
}

func designValidateCmd() *cli.Command {
	return &cli.Command{Name: "design-validate", Short: "Check current canonical prerequisites without completing a lifecycle step.",
		Flags: []*cli.Flag{missionFlag(), str("stage", "specify | plan | tasks", true)}, Run: runDesignValidate}
}

func designContextCmd() *cli.Command {
	return &cli.Command{Name: "design-context", Short: "Read bounded resolved templates, governance and required interview slots.",
		Flags: []*cli.Flag{str("mission", "Mission handle; omitted before creation", false),
			{Name: "stage", Kind: cli.String, Metavar: "<str>", Default: "specify", Help: "specify | plan | tasks"},
			str("mission-type", "Activated mission type before creation", false)},
		Run: runDesignContext}
}

func interviewRecordCmd() *cli.Command {
	return &cli.Command{Name: "interview-record", Short: "Record incremental interview answers through the existing decision service.",
		Flags: []*cli.Flag{str("mission", "Mission handle", true), str("stage", "specify | plan", true),
			str("answers", "JSON mapping of canonical question IDs to nonempty answers", true), str("actor", helpActor, true),
			str("policy", "Required orchestrator policy JSON", false)},
		Run: runInterviewRecord}
}

func nextCmd() *cli.Command {
	return &cli.Command{Name: "next", Short: "Query or advance exactly the same authority and lifecycle as native next.",
		Flags: []*cli.Flag{str("mission", "Mission handle", true), str("agent", "Agent required for advancement", false),
			str("result", "Prior result; omit for read-only query", false), str("answer", "Native pending decision answer", false),
			str("decision-id", "Native decision identity", false), str("policy", "Policy JSON required for advancement", false)},
		Run: runNext}
}

func answerDecisionCmd() *cli.Command {
	return &cli.Command{Name: "answer-decision",
		Short: "Resolve a ``spec-kitty next`` ``decision_required`` moment (FR-013,\nMechanism B) with full CLI event/lifecycle-log parity (FR-014, operator\nruling SPEC-FRESH2-001). See the module comment block above this\nfunction for the full five-step composite and the response-shape\ncontract.",
		Flags: []*cli.Flag{missionFlag(), str("agent", "Agent/actor identity performing this call (required)", true),
			str("answer", "The answer value to persist for the pending decision", true),
			str("result", "Outcome of the current issuance: success | failed | blocked (required alongside --answer)", false),
			str("decision-id", "Run-snapshot pending decision id to answer (auto-resolved when omitted and exactly one decision is pending)", false),
			str("policy", helpPolicy, false)},
		Run: runAnswerDecision}
}
