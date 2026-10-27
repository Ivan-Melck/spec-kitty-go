package nextrt

// Composition dispatch (runtime_bridge_composition + StepContractExecutor):
// when a mission step that belongs to the mission type's action_sequence
// completes, its step contract is executed as a chain of governed Ops
// (kitty-ops/<ULID>.jsonl, opened and closed immediately, each committed
// as `op(<profile>): <action> [<id8>]`).

import (
	"fmt"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// actionProfileDefaults is _ACTION_PROFILE_DEFAULTS (built-in missions only).
var actionProfileDefaults = map[[2]string]string{
	{"software-dev", "specify"}: "researcher-robbie", {"software-dev", "plan"}: "architect-alphonso",
	{"software-dev", "tasks"}: "architect-alphonso", {"software-dev", "implement"}: "implementer-ivan",
	{"software-dev", "review"}: "reviewer-renata",
	{"plan", "specify"}:        "researcher-robbie", {"plan", "research"}: "researcher-robbie",
	{"plan", "plan"}: "architect-alphonso", {"plan", "review"}: "reviewer-renata",
	{"research", "scoping"}: "researcher-robbie", {"research", "methodology"}: "researcher-robbie",
	{"research", "gathering"}: "researcher-robbie", {"research", "synthesis"}: "researcher-robbie",
	{"research", "output"}:        "reviewer-renata",
	{"documentation", "discover"}: "researcher-robbie", {"documentation", "audit"}: "researcher-robbie",
	{"documentation", "design"}: "architect-alphonso", {"documentation", "generate"}: "implementer-ivan",
	{"documentation", "validate"}: "reviewer-renata", {"documentation", "publish"}: "reviewer-renata",
	{"documentation", "accept"}: "reviewer-renata",
}

// defaultProfileRoles is _DEFAULT_PROFILE_ROLES.
var defaultProfileRoles = map[string]string{
	"researcher-robbie": "researcher", "architect-alphonso": "architect",
	"implementer-ivan": "implementer", "reviewer-renata": "reviewer",
}

// StepInput is MissionStepInput.
type StepInput struct {
	Flag, Source string
	Optional     bool
}

// ContractStep is MissionStepContractStep.
type ContractStep struct {
	ID, Description, Command, Guidance string
	Inputs                             []StepInput
	DelegatesKind                      string
	Candidates                         []string
}

// StepContract is MissionStepContract.
type StepContract struct {
	ID, Mission, Action string
	Steps               []ContractStep
}

// StepContractFromObject decodes a *.step-contract.yaml document.
func StepContractFromObject(o *jsonx.Object) *StepContract {
	c := &StepContract{ID: o.GetString("id"), Mission: o.GetString("mission"), Action: o.GetString("action")}
	for _, it := range o.GetList("steps") {
		so, ok := it.(*jsonx.Object)
		if !ok {
			continue
		}
		st := ContractStep{ID: so.GetString("id"), Description: so.GetString("description"),
			Command: so.GetString("command"), Guidance: so.GetString("guidance")}
		for _, in := range so.GetList("inputs") {
			if io, ok := in.(*jsonx.Object); ok {
				st.Inputs = append(st.Inputs, StepInput{Flag: io.GetString("flag"), Source: io.GetString("source"), Optional: io.GetBool("optional")})
			}
		}
		if d := so.GetObject("delegates_to"); d != nil {
			st.DelegatesKind = d.GetString("kind")
			st.Candidates = strList(d, "candidates")
		}
		c.Steps = append(c.Steps, st)
	}
	return c
}

// LookupStepContract is MissionStepContractRepository.get_by_action.
func LookupStepContract(root, mission, action string) *StepContract {
	repo := dct.NewOffering(root, dct.OrgRoots(root)).Repo(dct.MissionStepContract)
	for _, a := range repo.List() {
		if a.Data.GetString("mission") == mission && a.Data.GetString("action") == action {
			return StepContractFromObject(a.Data)
		}
	}
	return nil
}

// LookupStepContractByID resolves a contract_ref (lookup_contract).
func LookupStepContractByID(root, id string) *StepContract {
	a := dct.NewOffering(root, dct.OrgRoots(root)).Repo(dct.MissionStepContract).Get(id)
	if a == nil {
		return nil
	}
	return StepContractFromObject(a.Data)
}

// StepContractError is StepContractExecutionError.
type StepContractError struct{ Msg string }

func (e *StepContractError) Error() string { return e.Msg }

func profileRoles(p *dct.Profile) []string {
	var out []string
	for _, r := range p.Data.GetList("roles") {
		if s, ok := r.(string); ok {
			out = append(out, strings.ToLower(s))
		}
	}
	if len(out) == 0 && p.Data.GetString("role") != "" {
		out = append(out, strings.ToLower(p.Data.GetString("role")))
	}
	return out
}

// resolveProfileHint is StepContractExecutor._resolve_profile_hint.
func resolveProfileHint(root string, c *StepContract, hint string) (string, error) {
	if hint != "" {
		return hint, nil
	}
	def, ok := actionProfileDefaults[[2]string{c.Mission, c.Action}]
	if !ok {
		return "", &StepContractError{fmt.Sprintf("profile_hint is required when no action default exists for %s/%s", c.Mission, c.Action)}
	}
	catalog := invocation.AvailableProfiles(root)
	for _, p := range catalog {
		if p.ID == def {
			return def, nil
		}
	}
	role, hasRole := defaultProfileRoles[def]
	if hasRole {
		var same []*dct.Profile
		for _, p := range catalog {
			for _, r := range profileRoles(p) {
				if r == role {
					same = append(same, p)
					break
				}
			}
		}
		sort.SliceStable(same, func(i, j int) bool {
			if same[i].RoutingPriority() != same[j].RoutingPriority() {
				return same[i].RoutingPriority() > same[j].RoutingPriority()
			}
			return same[i].ID < same[j].ID
		})
		if len(same) > 0 {
			return same[0].ID, nil
		}
	}
	note := ""
	if hasRole {
		note = fmt.Sprintf(" (role '%s')", role)
	}
	return "", &StepContractError{fmt.Sprintf("No available profile for %s/%s: the built-in default profile '%s'%s is deactivated and no available profile carries that role. Activate an agent profile with that role, or supply an explicit profile for the step.", c.Mission, c.Action, def, note)}
}

// delegationKinds are the ArtifactKind values carrying a delegation node
// kind (anti_pattern and skill excluded).
var delegationKinds = map[string]bool{"directive": true, "tactic": true, "paradigm": true, "styleguide": true,
	"toolguide": true, "procedure": true, "agent_profile": true, "mission_step_contract": true, "template": true,
	"asset": true, "glossary_pack": true, "mission_type": true}

func candidateURN(g *dct.Graph, kind, cand string) string {
	if !delegationKinds[kind] {
		return ""
	}
	direct := kind + ":" + cand
	if n := g.Node(direct); n != nil && n.Kind == kind {
		return direct
	}
	if kind == "directive" {
		num := ""
		for _, r := range cand {
			if r < '0' || r > '9' {
				break
			}
			num += string(r)
		}
		if num != "" {
			for len(num) < 3 {
				num = "0" + num
			}
			u := "directive:DIRECTIVE_" + num
			if n := g.Node(u); n != nil && n.Kind == kind {
				return u
			}
		}
	}
	var matches []string
	for _, n := range g.Nodes {
		if n.Kind != kind {
			continue
		}
		if _, id, ok := strings.Cut(n.URN, ":"); ok && id == cand {
			matches = append(matches, n.URN)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func formatStepInput(in StepInput) string {
	r := fmt.Sprintf("%s {%s}", in.Flag, in.Source)
	if in.Optional {
		return "[" + r + "]"
	}
	return r
}

func buildRequestText(c *StepContract, st ContractStep, requestText string, resolved, unresolved []string) string {
	lines := []string{
		fmt.Sprintf("Execute mission step contract %s (%s/%s).", c.ID, c.Mission, c.Action),
		fmt.Sprintf("Step %s: %s", st.ID, st.Description),
	}
	if requestText != "" {
		lines = append(lines, "Run request: "+requestText)
	}
	var ins []string
	for _, in := range st.Inputs {
		ins = append(ins, formatStepInput(in))
	}
	if st.Command != "" {
		cmd := st.Command
		if len(ins) > 0 {
			cmd += " " + strings.Join(ins, " ")
		}
		lines = append(lines, "Declared command: "+cmd, "Command status: declared only; the host/operator owns execution.")
	} else if len(ins) > 0 {
		lines = append(lines, "Declared step inputs: "+strings.Join(ins, " "))
	}
	if len(resolved) > 0 {
		lines = append(lines, "Resolved delegations: "+strings.Join(resolved, ", "))
	}
	if len(unresolved) > 0 {
		lines = append(lines, "Unresolved delegation candidates: "+strings.Join(unresolved, ", "))
	}
	if st.Guidance != "" {
		lines = append(lines, "Step guidance: "+st.Guidance)
	}
	return strings.Join(lines, "\n")
}

// ExecuteStepContract is StepContractExecutor.execute: every contract step
// becomes one Op (started, completed "done", committed). Returns the
// invocation ids.
func ExecuteStepContract(root, mission, action, actor, profileHint, requestText string, c *StepContract) ([]string, error) {
	if c == nil {
		c = LookupStepContract(root, mission, action)
	}
	if c == nil {
		return nil, &StepContractError{fmt.Sprintf("No step contract found for mission/action %s/%s", mission, action)}
	}
	profile, err := resolveProfileHint(root, c, profileHint)
	if err != nil {
		return nil, err
	}
	orgRoots := dct.OrgRoots(root)
	g, err := dct.LoadGraph(root, orgRoots)
	if err != nil {
		return nil, err
	}
	if act, err := dct.LoadActivation(root); err == nil {
		g = dct.FilterByActivation(g, act, dct.NewOffering(root, orgRoots))
	}
	rc := dct.ResolveContext(g, "action:"+c.Mission+"/"+c.Action, 2)
	if actor == "" {
		actor = "unknown"
	}
	var ids []string
	for _, st := range c.Steps {
		var resolved, unresolved []string
		for _, cand := range st.Candidates {
			u := candidateURN(g, st.DelegatesKind, cand)
			if u == "" || !rc.ArtifactURNs[u] {
				unresolved = append(unresolved, cand)
				continue
			}
			resolved = append(resolved, u)
		}
		p, err := invocation.InvokeWithHint(root, buildRequestText(c, st, requestText, resolved, unresolved),
			invocation.InvokeOptions{ProfileHint: profile, Actor: actor, ActionHint: c.Action})
		if err != nil {
			return ids, err
		}
		ids = append(ids, p.InvocationID)
		if _, _, err := invocation.Complete(root, p.InvocationID, "done", invocation.CompleteOptions{}); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// normalizeComposedAction is _normalize_action_for_composition.
func normalizeComposedAction(step string) string {
	switch step {
	case "tasks_outline", "tasks_packages", "tasks_finalize":
		return "tasks"
	}
	return step
}

// stepBinding is _resolve_step_binding over the frozen template.
func stepBinding(t *Template, step string) (profile, contractRef string) {
	if t == nil {
		return "", ""
	}
	n := normalizeComposedAction(step)
	for _, s := range t.Steps {
		if s.ID == step || s.ID == n {
			return strings.TrimSpace(s.AgentProfile), strings.TrimSpace(s.ContractRef)
		}
	}
	return "", ""
}

// shouldDispatchViaComposition is _should_dispatch_via_composition.
func shouldDispatchViaComposition(root, missionType, step string, t *Template) bool {
	if mt := dct.MissionTypeRoster(root)[missionType]; mt != nil {
		n := normalizeComposedAction(step)
		for _, a := range mt.ActionSequence {
			if a == n {
				return true
			}
		}
	}
	p, c := stepBinding(t, step)
	return p != "" || c != ""
}

// compositionInputs is _composition_dispatch_inputs: (profile hint, contract).
func compositionInputs(root, missionType, step, action string, t *Template) (string, *StepContract) {
	hint := ""
	if _, builtIn := actionProfileDefaults[[2]string{missionType, action}]; !builtIn {
		hint, _ = stepBinding(t, step)
	}
	profile, ref := stepBinding(t, step)
	switch {
	case ref != "":
		return hint, LookupStepContractByID(root, ref)
	case profile != "":
		return hint, synthesizeContract(t, missionType, normalizeComposedAction(step))
	}
	return hint, nil
}

// synthesizeContract is contract_synthesis.synthesize_contracts for one
// custom step (custom:<mission key>:<step id>, one "<step>.execute" step).
func synthesizeContract(t *Template, missionType, action string) *StepContract {
	key := t.Key
	if key == "" {
		key = missionType
	}
	if "custom:"+key+":"+action != "custom:"+missionType+":"+action {
		return nil
	}
	for _, s := range t.Steps {
		if s.ID != action || len(s.RequiresInputs) > 0 || s.ContractRef != "" || s.ID == "retrospective" {
			continue
		}
		desc := firstNonEmpty(s.Description, s.Title, s.ID)
		return &StepContract{ID: "custom:" + key + ":" + action, Mission: key, Action: action,
			Steps: []ContractStep{{ID: action + ".execute", Description: desc}}}
	}
	return nil
}
