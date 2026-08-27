package mission

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

// StepContractSpec is construction input for immutable step law.
type StepContractSpec struct {
	Key              StepKey
	RequiredInputs   []ArtifactKind
	ProducedOutputs  []ArtifactKind
	RequiredEvidence []EvidenceKind
	ValidationPolicy []ValidationRequirement
	ApprovalPolicy   ApprovalPolicy
	Predecessors     []StepKey
	Successors       []StepKey
	GovernanceAction string
	Mutating         bool
	TerminalOutcome  TerminalOutcome
}

// StepContract is immutable after validated construction.
type StepContract struct{ spec StepContractSpec }

func NewStepContract(spec StepContractSpec) (StepContract, error) {
	return newStepContract(spec, -1)
}

func newStepContract(spec StepContractSpec, index int) (StepContract, error) {
	spec = normalizeStepSpec(spec)
	if err := validateStepSpec(spec); err != nil {
		violation, ok := err.(*stepSpecViolation)
		if !ok {
			violation = &stepSpecViolation{rule: "invalid"}
		}
		return StepContract{}, stepContractRefusal(spec.Key, index, violation.rule)
	}
	return StepContract{spec: spec}, nil
}

func stepContractRefusal(key StepKey, index int, rule string) *Error {
	message := "step contract violates " + rule
	if key.Validate() == nil {
		message = fmt.Sprintf("step %s violates %s", key, rule)
	} else if index >= 0 {
		message = fmt.Sprintf("step index %d violates %s", index, rule)
	}
	return refusal(KindDefinitionMalformed, RecoveryCorrectInput, "step_contract."+rule, message)
}

func definitionStepRefusal(rule string, key StepKey) *Error {
	return refusal(
		KindDefinitionMalformed,
		RecoveryCorrectInput,
		"definition."+rule,
		fmt.Sprintf("step %s violates %s", key, rule),
	)
}

func definitionEdgeRefusal(rule string, source, target StepKey) *Error {
	return refusal(
		KindDefinitionMalformed,
		RecoveryCorrectInput,
		"definition."+rule,
		fmt.Sprintf("edge %s -> %s violates %s", source, target, rule),
	)
}

type stepSpecViolation struct{ rule string }

func (e *stepSpecViolation) Error() string { return "step contract rule=" + e.rule }

func stepViolation(rule string) error { return &stepSpecViolation{rule: rule} }

func (s StepContract) Spec() StepContractSpec { return cloneStepSpec(s.spec) }
func (s StepContract) Key() StepKey           { return s.spec.Key }

func normalizeStepSpec(spec StepContractSpec) StepContractSpec {
	spec = cloneStepSpec(spec)
	sort.Slice(spec.RequiredInputs, func(i, j int) bool { return spec.RequiredInputs[i] < spec.RequiredInputs[j] })
	sort.Slice(spec.ProducedOutputs, func(i, j int) bool { return spec.ProducedOutputs[i] < spec.ProducedOutputs[j] })
	sort.Slice(spec.RequiredEvidence, func(i, j int) bool { return spec.RequiredEvidence[i] < spec.RequiredEvidence[j] })
	sort.Slice(spec.ValidationPolicy, func(i, j int) bool { return spec.ValidationPolicy[i] < spec.ValidationPolicy[j] })
	sort.Slice(spec.Predecessors, func(i, j int) bool { return spec.Predecessors[i] < spec.Predecessors[j] })
	sort.Slice(spec.Successors, func(i, j int) bool { return spec.Successors[i] < spec.Successors[j] })
	return spec
}

func cloneStepSpec(spec StepContractSpec) StepContractSpec {
	spec.RequiredInputs = append([]ArtifactKind(nil), spec.RequiredInputs...)
	spec.ProducedOutputs = append([]ArtifactKind(nil), spec.ProducedOutputs...)
	spec.RequiredEvidence = append([]EvidenceKind(nil), spec.RequiredEvidence...)
	spec.ValidationPolicy = append([]ValidationRequirement(nil), spec.ValidationPolicy...)
	spec.Predecessors = append([]StepKey(nil), spec.Predecessors...)
	spec.Successors = append([]StepKey(nil), spec.Successors...)
	return spec
}

func validateStepSpec(spec StepContractSpec) error {
	if err := spec.Key.Validate(); err != nil || spec.Key.Reserved() {
		return stepViolation("unsupported_key")
	}
	for _, values := range []struct {
		rule  string
		items []string
	}{
		{rule: "duplicate_required_input", items: artifactStrings(spec.RequiredInputs)},
		{rule: "duplicate_produced_output", items: artifactStrings(spec.ProducedOutputs)},
		{rule: "duplicate_required_evidence", items: evidenceStrings(spec.RequiredEvidence)},
		{rule: "duplicate_validation", items: validationStrings(spec.ValidationPolicy)},
		{rule: "duplicate_predecessor", items: stepStrings(spec.Predecessors)},
		{rule: "duplicate_successor", items: stepStrings(spec.Successors)},
	} {
		if duplicate := firstDuplicate(values.items); duplicate != "" {
			return stepViolation(values.rule)
		}
	}
	for _, value := range spec.RequiredInputs {
		if err := value.Validate(); err != nil {
			return stepViolation("invalid_required_input")
		}
	}
	for _, value := range spec.ProducedOutputs {
		if err := value.Validate(); err != nil {
			return stepViolation("invalid_produced_output")
		}
	}
	for _, value := range spec.RequiredEvidence {
		if err := value.Validate(); err != nil {
			return stepViolation("invalid_required_evidence")
		}
	}
	for _, value := range spec.ValidationPolicy {
		if err := value.Validate(); err != nil {
			return stepViolation("invalid_validation")
		}
	}
	for _, adjacent := range append(append([]StepKey(nil), spec.Predecessors...), spec.Successors...) {
		if err := adjacent.Validate(); err != nil || adjacent.Reserved() {
			return stepViolation("unsupported_adjacent_step")
		}
		if adjacent == spec.Key {
			return stepViolation("self_edge")
		}
	}
	if err := spec.ApprovalPolicy.Validate(); err != nil {
		return stepViolation("invalid_approval_policy")
	}
	if spec.Mutating {
		if _, err := ids.ParseOperationID(spec.GovernanceAction); err != nil {
			return stepViolation("invalid_governance_action")
		}
	} else if spec.GovernanceAction != "" {
		return stepViolation("unexpected_governance_action")
	}
	wants := EvidenceKind("")
	switch spec.ApprovalPolicy {
	case ApprovalSpecification:
		wants = EvidenceSpecificationOK
	case ApprovalPlan:
		wants = EvidencePlanOK
	}
	if wants != "" && !containsEvidence(spec.RequiredEvidence, wants) {
		return stepViolation("missing_approval_evidence")
	}
	return nil
}

// DefinitionSpec is explicit construction input for one mission method.
type DefinitionSpec struct {
	ID            ids.MissionDefinitionID
	Version       DefinitionVersion
	Steps         []StepContractSpec
	InitialStep   StepKey
	TerminalSteps []StepKey
}

// MissionDefinition is immutable, canonicalized lifecycle law.
type MissionDefinition struct {
	id         ids.MissionDefinitionID
	version    DefinitionVersion
	steps      []StepContract
	initial    StepKey
	terminals  []StepKey
	commitment DefinitionCommitment
}

// NewDefinition is the single constructor for validated mission-definition
// authority. Static mission-type packages call it explicitly.
func NewDefinition(spec DefinitionSpec) (MissionDefinition, error) {
	if _, err := ids.ParseMissionDefinitionID(spec.ID.String()); err != nil {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "definition_id", "definition identity is invalid")
	}
	if err := spec.Version.Validate(); err != nil {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "definition_version", "definition version must be positive")
	}
	if len(spec.Steps) == 0 {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "steps", "definition has no steps")
	}
	if err := spec.InitialStep.Validate(); err != nil || spec.InitialStep.Reserved() {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "initial_step", "initial step is invalid")
	}
	if len(spec.TerminalSteps) == 0 {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "terminal_steps", "definition has no terminal step")
	}

	steps := make([]StepContract, 0, len(spec.Steps))
	byKey := make(map[StepKey]StepContract, len(spec.Steps))
	for index, input := range spec.Steps {
		step, err := newStepContract(input, index)
		if err != nil {
			return MissionDefinition{}, err
		}
		if _, exists := byKey[step.Key()]; exists {
			return MissionDefinition{}, definitionStepRefusal("duplicate_step", step.Key())
		}
		steps = append(steps, step)
		byKey[step.Key()] = step
	}
	if _, exists := byKey[spec.InitialStep]; !exists {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "initial_step", "definition names an unknown initial step")
	}

	terminals := append([]StepKey(nil), spec.TerminalSteps...)
	sort.Slice(terminals, func(i, j int) bool { return terminals[i] < terminals[j] })
	if duplicate := firstDuplicate(stepStrings(terminals)); duplicate != "" {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "terminal_steps", "definition has a duplicate terminal step")
	}
	terminalSet := make(map[StepKey]bool, len(terminals))
	for _, terminal := range terminals {
		contract, exists := byKey[terminal]
		if !exists {
			return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "terminal_steps", "definition names an unknown terminal step")
		}
		if len(contract.spec.Successors) != 0 {
			return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "terminal_steps", "terminal step exposes successors")
		}
		if err := contract.spec.TerminalOutcome.Validate(); err != nil {
			return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "terminal_steps", "terminal step lacks a valid outcome")
		}
		terminalSet[terminal] = true
	}
	for _, step := range steps {
		if !terminalSet[step.Key()] && step.spec.TerminalOutcome != "" {
			return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "steps", "nonterminal step declares a terminal outcome")
		}
		for _, successor := range step.spec.Successors {
			target, exists := byKey[successor]
			if !exists {
				return MissionDefinition{}, definitionEdgeRefusal("unknown_successor", step.Key(), successor)
			}
			if !containsStep(target.spec.Predecessors, step.Key()) {
				return MissionDefinition{}, definitionEdgeRefusal("asymmetric_edge", step.Key(), successor)
			}
		}
		for _, predecessor := range step.spec.Predecessors {
			source, exists := byKey[predecessor]
			if !exists {
				return MissionDefinition{}, definitionEdgeRefusal("unknown_predecessor", predecessor, step.Key())
			}
			if !containsStep(source.spec.Successors, step.Key()) {
				return MissionDefinition{}, definitionEdgeRefusal("asymmetric_edge", predecessor, step.Key())
			}
		}
	}
	if err := validateAcyclicReachable(spec.InitialStep, byKey); err != nil {
		return MissionDefinition{}, err
	}

	sort.Slice(steps, func(i, j int) bool { return steps[i].Key() < steps[j].Key() })
	definition := MissionDefinition{id: spec.ID, version: spec.Version, steps: steps, initial: spec.InitialStep, terminals: terminals}
	commitment, err := definition.computeCommitment()
	if err != nil {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "commitment", "canonical definition encoding failed")
	}
	definition.commitment = commitment
	return definition, nil
}

func validateAcyclicReachable(initial StepKey, steps map[StepKey]StepContract) error {
	state := make(map[StepKey]uint8, len(steps))
	var visit func(StepKey) error
	visit = func(key StepKey) error {
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		for _, successor := range steps[key].spec.Successors {
			if state[successor] == 1 {
				return definitionEdgeRefusal("cycle", key, successor)
			}
			if err := visit(successor); err != nil {
				return err
			}
		}
		state[key] = 2
		return nil
	}
	if err := visit(initial); err != nil {
		return err
	}
	if len(state) != len(steps) {
		unreachable := make([]StepKey, 0, len(steps)-len(state))
		for key := range steps {
			if state[key] == 0 {
				unreachable = append(unreachable, key)
			}
		}
		sort.Slice(unreachable, func(i, j int) bool { return unreachable[i] < unreachable[j] })
		return definitionStepRefusal("unreachable_step", unreachable[0])
	}
	return nil
}

func (d MissionDefinition) computeCommitment() (DefinitionCommitment, error) {
	type canonicalStep struct {
		Key              StepKey
		RequiredInputs   []ArtifactKind
		ProducedOutputs  []ArtifactKind
		RequiredEvidence []EvidenceKind
		ValidationPolicy []ValidationRequirement
		ApprovalPolicy   ApprovalPolicy
		Predecessors     []StepKey
		Successors       []StepKey
		GovernanceAction string
		Mutating         bool
		TerminalOutcome  TerminalOutcome
	}
	canonical := struct {
		ID        ids.MissionDefinitionID
		Version   DefinitionVersion
		Initial   StepKey
		Terminals []StepKey
		Steps     []canonicalStep
	}{ID: d.id, Version: d.version, Initial: d.initial, Terminals: append([]StepKey(nil), d.terminals...)}
	for _, step := range d.steps {
		spec := step.Spec()
		canonical.Steps = append(canonical.Steps, canonicalStep(spec))
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return DefinitionCommitment(fmt.Sprintf("%x", digest)), nil
}

func (d MissionDefinition) ID() ids.MissionDefinitionID      { return d.id }
func (d MissionDefinition) Version() DefinitionVersion       { return d.version }
func (d MissionDefinition) InitialStep() StepKey             { return d.initial }
func (d MissionDefinition) Commitment() DefinitionCommitment { return d.commitment }
func (d MissionDefinition) TerminalSteps() []StepKey         { return append([]StepKey(nil), d.terminals...) }
func (d MissionDefinition) Steps() []StepContract {
	result := make([]StepContract, len(d.steps))
	for i := range d.steps {
		result[i] = StepContract{spec: cloneStepSpec(d.steps[i].spec)}
	}
	return result
}

func (d MissionDefinition) Step(key StepKey) (StepContract, bool) {
	index := sort.Search(len(d.steps), func(i int) bool { return d.steps[i].Key() >= key })
	if index == len(d.steps) || d.steps[index].Key() != key {
		return StepContract{}, false
	}
	return StepContract{spec: cloneStepSpec(d.steps[index].spec)}, true
}

func (d MissionDefinition) IsTerminal(key StepKey) bool { return containsStep(d.terminals, key) }

func (d MissionDefinition) clone() MissionDefinition {
	return MissionDefinition{id: d.id, version: d.version, steps: d.Steps(), initial: d.initial, terminals: d.TerminalSteps(), commitment: d.commitment}
}

type registryKey struct {
	id      ids.MissionDefinitionID
	version DefinitionVersion
}

// Registry is a closed immutable set of exact mission definitions.
type Registry struct {
	definitions map[registryKey]MissionDefinition
	versions    map[ids.MissionDefinitionID]map[DefinitionVersion]bool
}

func NewRegistry(definitions ...MissionDefinition) (Registry, error) {
	registry := Registry{definitions: make(map[registryKey]MissionDefinition, len(definitions)), versions: make(map[ids.MissionDefinitionID]map[DefinitionVersion]bool)}
	for _, definition := range definitions {
		if _, err := ids.ParseMissionDefinitionID(definition.id.String()); err != nil || definition.version.Validate() != nil || definition.commitment.Validate() != nil {
			return Registry{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "definition", "registry received invalid definition")
		}
		key := registryKey{id: definition.id, version: definition.version}
		if existing, found := registry.definitions[key]; found {
			if existing.commitment == definition.commitment {
				return Registry{}, refusal(KindDuplicateDefinition, RecoveryCorrectInput, "definition", "duplicate exact definition")
			}
			return Registry{}, refusal(KindDefinitionConflict, RecoveryCorrectInput, "definition", "definition key and version have conflicting commitments")
		}
		registry.definitions[key] = definition.clone()
		if registry.versions[definition.id] == nil {
			registry.versions[definition.id] = make(map[DefinitionVersion]bool)
		}
		registry.versions[definition.id][definition.version] = true
	}
	return registry, nil
}

func (r Registry) Resolve(id ids.MissionDefinitionID, version DefinitionVersion) (MissionDefinition, error) {
	if _, err := ids.ParseMissionDefinitionID(id.String()); err != nil || version.Validate() != nil {
		return MissionDefinition{}, refusal(KindDefinitionMalformed, RecoveryCorrectInput, "selection", "definition selection is malformed")
	}
	versions, found := r.versions[id]
	if !found {
		return MissionDefinition{}, refusal(KindDefinitionNotFound, RecoverySelectDefinition, "definition_id", "definition is not registered")
	}
	if !versions[version] {
		return MissionDefinition{}, refusal(KindDefinitionVersionUnsupported, RecoverySelectDefinition, "definition_version", "definition version is not registered")
	}
	return r.definitions[registryKey{id: id, version: version}].clone(), nil
}

func artifactStrings(values []ArtifactKind) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = string(values[i])
	}
	return result
}
func evidenceStrings(values []EvidenceKind) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = string(values[i])
	}
	return result
}
func validationStrings(values []ValidationRequirement) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = string(values[i])
	}
	return result
}
func stepStrings(values []StepKey) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = string(values[i])
	}
	return result
}
func firstDuplicate(values []string) string {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return value
		}
		seen[value] = true
	}
	return ""
}
func containsStep(values []StepKey, want StepKey) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func containsEvidence(values []EvidenceKind, want EvidenceKind) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
