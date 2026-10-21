// Package orchestrator implements `spec-kitty orchestrator-api` (upstream
// specify_cli.orchestrator_api): a JSON-only machine contract whose every
// stdout emission, usage errors included, is one canonical envelope.
package orchestrator

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

const (
	helpMission = "Mission slug"
	helpActor   = "Actor identity"
	helpPolicy  = "Policy metadata JSON (required)"
	helpWP      = "Work package ID"
)

// rootCmd is the full command tree (captured at registration) used for
// in-process delegation to host commands.
var rootCmd *cli.Command

func str(name, help string, required bool) *cli.Flag {
	return &cli.Flag{Name: name, Kind: cli.String, Metavar: "<str>", Help: help, Required: required}
}

func boolFlag(name, help string) *cli.Flag {
	return &cli.Flag{Name: name, Kind: cli.Bool, Help: help}
}

func missionFlag() *cli.Flag { return str("mission", helpMission, true) }

func init() {
	app.Register(func(root *cli.Command) {
		rootCmd = root
		g := app.Group(root, "orchestrator-api", "Machine-contract API for external orchestrators (JSON-first)")
		g.NoArgsIsError = true
		g.UsageErrorHandler = func(io_ cli.IO, e *cli.UsageError) int {
			orch.UsageError(io_.Stdout, usageMessage(e.Msg))
			return 2
		}
		g.Add(verbs()...)
	})
}

func verbs() []*cli.Command {
	return []*cli.Command{
		contractVersionCmd(),
		missionStateCmd(),
		listReadyCmd(),
		resolveWorkspaceCmd(),
		startImplementationCmd(),
		startReviewCmd(),
		transitionCmd(),
		appendHistoryCmd(),
		acceptMissionCmd(),
		consolidateMissionCmd(),
		specifyCmd(),
		planCmd(),
		tasksCmd(),
		checkPrerequisitesCmd(),
		recordAnalysisCmd(),
		openDecisionCmd(),
		resolveDecisionCmd(),
		deferDecisionCmd(),
		cancelDecisionCmd(),
		answerDecisionCmd(),
		designStatusCmd(),
		artifactReadCmd(),
		artifactSubmitCmd(),
		designValidateCmd(),
		designContextCmd(),
		interviewRecordCmd(),
		nextCmd(),
	}
}

var invalidChoiceRe = regexp.MustCompile(`^Invalid value for '[^']*': '(.*)' is not one of (.*)\.$`)

// usageMessage is click's UsageError.format_message() as the group's JSON
// handler sees it: a click.Choice failure carries no parameter hint and
// joins the choices with ", ".
func usageMessage(msg string) string {
	if m := invalidChoiceRe.FindStringSubmatch(msg); m != nil {
		choices := m[2]
		if i := strings.LastIndex(choices, "' or '"); i >= 0 {
			choices = choices[:i] + "', '" + choices[i+len("' or '"):]
		}
		return fmt.Sprintf("Invalid value: '%s' is not one of %s.", m[1], choices)
	}
	return msg
}

// verb carries one invocation's context and renders its envelopes.
type verb struct {
	ctx  *cli.Context
	name string
}

func newVerb(ctx *cli.Context, name string) *verb { return &verb{ctx: ctx, name: name} }

// fail prints the failure envelope and returns exit 1.
func (v *verb) fail(code, message string, data *jsonx.Object) error {
	orch.Fail(v.ctx.Stdout, v.name, code, message, data)
	return cli.Exit(1)
}

// ok prints the success envelope.
func (v *verb) ok(data *jsonx.Object) error {
	orch.Emit(v.ctx.Stdout, orch.Envelope(v.name, true, data, ""))
	return nil
}

// root is _get_main_repo_root().
func (v *verb) root() string {
	if r := project.Locate(""); r != "" {
		return r
	}
	cwd, _ := os.Getwd()
	if r := project.MainRepoRoot(cwd); r != "" {
		return r
	}
	return cwd
}

// mission resolves --mission or emits the resolution failure envelope.
func (v *verb) mission(root, handle string) (*orch.Mission, error) {
	m, err := orch.ResolveMission(root, handle)
	if err != nil {
		re := err.(*orch.ResolveError)
		return nil, v.fail(re.Code, re.Message, re.Data)
	}
	return m, nil
}

// requirePolicy enforces POLICY_METADATA_REQUIRED + POLICY_VALIDATION_FAILED.
func (v *verb) requirePolicy(raw, requiredMsg string) (*orch.Policy, error) {
	if raw == "" {
		return nil, v.fail("POLICY_METADATA_REQUIRED", requiredMsg, nil)
	}
	return v.parsePolicy(raw)
}

func (v *verb) parsePolicy(raw string) (*orch.Policy, error) {
	p, err := orch.ParsePolicy(raw)
	if err != nil {
		return nil, v.fail("POLICY_VALIDATION_FAILED", err.Error(), nil)
	}
	return p, nil
}

// identity is the mission identity payload merged with extra pairs.
func identity(dir string, pairs ...any) *jsonx.Object {
	o := orch.Identity(dir)
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

func optString(ctx *cli.Context, name string) *string {
	if !ctx.IsSet(name) {
		return nil
	}
	s := ctx.String(name)
	return &s
}
