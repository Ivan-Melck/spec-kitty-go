// Command dishonest_bd is a deterministic, script-controlled stand-in for
// bd. It is compiled by tests and deliberately lives outside production
// packages. Its only configuration root is the explicit BEADS_DIR binding.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const scenarioVersion = 1

type scenario struct {
	SchemaVersion int               `json:"schema_version"`
	InitialState  map[string]string `json:"initial_state,omitempty"`
	Steps         []step            `json:"steps"`
}

type step struct {
	ID               string            `json:"id"`
	Argv             []string          `json:"argv"`
	RequiredEnv      map[string]string `json:"required_env"`
	Workspace        string            `json:"workspace"`
	Precondition     map[string]string `json:"precondition,omitempty"`
	TransitionTiming string            `json:"transition_timing,omitempty"`
	Transition       map[string]string `json:"transition,omitempty"`
	Stdout           string            `json:"stdout,omitempty"`
	Stderr           string            `json:"stderr,omitempty"`
	ExitCode         int               `json:"exit_code,omitempty"`
	WaitFor          string            `json:"wait_for,omitempty"`
	WaitTiming       string            `json:"wait_timing,omitempty"`
	NextObservation  map[string]string `json:"next_observation,omitempty"`
}

type runtimeState struct {
	SchemaVersion int               `json:"schema_version"`
	Position      int               `json:"position"`
	State         map[string]string `json:"state"`
	Invocations   []invocation      `json:"invocations"`
}

type invocation struct {
	StepID string   `json:"step_id"`
	Argv   []string `json:"argv"`
}

type machineFailure struct {
	SchemaVersion int    `json:"schema_version"`
	Error         string `json:"error"`
	Code          string `json:"code"`
	StepID        string `json:"step_id,omitempty"`
}

func main() {
	if err := run(); err != nil {
		failure := machineFailure{SchemaVersion: scenarioVersion, Error: "dishonest provider script rejected invocation", Code: "test_script_mismatch"}
		var stepErr *stepFailure
		if errors.As(err, &stepErr) {
			failure.StepID = stepErr.id
		}
		_ = json.NewEncoder(os.Stderr).Encode(failure)
		os.Exit(97)
	}
}

type stepFailure struct{ id, reason string }

func (failure *stepFailure) Error() string { return "step " + failure.id + ": " + failure.reason }

func run() error {
	root := os.Getenv("BEADS_DIR")
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fmt.Errorf("invalid workspace")
	}
	plan, err := loadScenario(filepath.Join(root, "scenario.json"))
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(root, "scenario.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	statePath := filepath.Join(root, "scenario.state.json")
	state, err := loadState(statePath, plan.InitialState)
	if err != nil {
		return err
	}
	if state.Position >= len(plan.Steps) {
		return fmt.Errorf("scenario exhausted")
	}
	current := plan.Steps[state.Position]
	if err := validateInvocation(root, current, os.Args[1:], state.State); err != nil {
		return err
	}
	if current.WaitFor != "" && current.WaitTiming == "before_apply" {
		if err := enterBarrier(root, current.WaitFor); err != nil {
			return err
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
			return err
		}
		if err := awaitBarrier(root, current.WaitFor); err != nil {
			return err
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			return err
		}
		state, err = loadState(statePath, plan.InitialState)
		if err != nil || state.Position >= len(plan.Steps) || state.Position != currentPosition(plan, current.ID) {
			return fmt.Errorf("scenario moved while waiting before apply")
		}
	}
	if current.TransitionTiming == "before_response" {
		apply(state.State, current.Transition)
		if err := assertObservation(current, state.State); err != nil {
			return err
		}
	}
	state.Invocations = append(state.Invocations, invocation{StepID: current.ID, Argv: append([]string(nil), os.Args[1:]...)})
	state.Position++
	if err := saveState(statePath, state); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		return err
	}

	if current.WaitFor != "" && current.WaitTiming != "before_apply" {
		if err := enterBarrier(root, current.WaitFor); err != nil {
			return err
		}
		if err := awaitBarrier(root, current.WaitFor); err != nil {
			return err
		}
	}
	if _, err := io.Copy(os.Stdout, strings.NewReader(current.Stdout)); err != nil {
		return err
	}
	if _, err := io.Copy(os.Stderr, strings.NewReader(current.Stderr)); err != nil {
		return err
	}
	if current.TransitionTiming == "after_response" {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			return err
		}
		latest, err := loadState(statePath, plan.InitialState)
		if err != nil {
			return err
		}
		apply(latest.State, current.Transition)
		if err := assertObservation(current, latest.State); err != nil {
			return err
		}
		if err := saveState(statePath, latest); err != nil {
			return err
		}
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
	if current.ExitCode != 0 {
		os.Exit(current.ExitCode)
	}
	return nil
}

func currentPosition(plan scenario, id string) int {
	for index := range plan.Steps {
		if plan.Steps[index].ID == id {
			return index
		}
	}
	return -1
}

func enterBarrier(root, name string) error {
	return os.WriteFile(filepath.Join(root, name+".entered"), []byte("entered\n"), 0o600)
}

func awaitBarrier(root, name string) error {
	barrier := filepath.Join(root, name)
	for {
		if _, err := os.Stat(barrier); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		_ = syscall.Nanosleep(&syscall.Timespec{Nsec: 1_000_000}, nil)
	}
}

func loadScenario(path string) (scenario, error) {
	var plan scenario
	if err := decodeStrictFile(path, &plan); err != nil {
		return plan, err
	}
	if plan.SchemaVersion != scenarioVersion || len(plan.Steps) == 0 {
		return plan, fmt.Errorf("unsupported or empty scenario")
	}
	seen := map[string]struct{}{}
	for _, current := range plan.Steps {
		if current.ID == "" || len(current.Argv) == 0 || current.Workspace == "" {
			return plan, fmt.Errorf("incomplete step")
		}
		if !knownAction(current.Argv) {
			return plan, fmt.Errorf("unknown scripted action")
		}
		if _, ok := seen[current.ID]; ok {
			return plan, fmt.Errorf("duplicate step")
		}
		seen[current.ID] = struct{}{}
		switch current.TransitionTiming {
		case "", "none", "before_response", "after_response":
		default:
			return plan, fmt.Errorf("unknown transition timing")
		}
		switch current.WaitTiming {
		case "", "before_apply", "after_apply":
		default:
			return plan, fmt.Errorf("unknown wait timing")
		}
		if filepath.IsAbs(current.WaitFor) || strings.Contains(current.WaitFor, "..") {
			return plan, fmt.Errorf("unsafe barrier")
		}
	}
	return plan, nil
}

func knownAction(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch argv[0] {
	case "version", "where", "list", "ready", "export":
		return len(argv) == 2 && argv[1] == "--json"
	case "show":
		return len(argv) == 3 && argv[1] != "" && argv[2] == "--json"
	case "create":
		return validOptionPairs(argv, 1, map[string]bool{"--title": true, "--description": true, "--acceptance": true, "--metadata": true}) && containsOption(argv, "--title")
	case "update":
		if len(argv) == 4 && argv[1] != "" && argv[2] == "--claim" && argv[3] == "--json" {
			return true
		}
		return len(argv) >= 5 && argv[1] != "" && validOptionPairs(argv, 2, map[string]bool{"--title": true, "--description": true, "--acceptance": true, "--metadata": true, "--assignee": true})
	case "dolt":
		return len(argv) == 3 && (argv[1] == "show" || argv[1] == "status") && argv[2] == "--json"
	case "dep":
		return (len(argv) == 5 && argv[1] == "remove" && argv[2] != "" && argv[3] != "" && argv[4] == "--json") ||
			(len(argv) == 7 && argv[1] == "add" && argv[2] != "" && argv[3] != "" && argv[4] == "--type" && argv[5] == "blocks" && argv[6] == "--json") ||
			(len(argv) == 4 && argv[1] == "list" && argv[2] != "" && argv[3] == "--json")
	case "comments":
		return (len(argv) == 3 && argv[1] != "" && argv[2] == "--json") ||
			(len(argv) == 5 && argv[1] == "add" && argv[2] != "" && argv[3] != "" && argv[4] == "--json")
	case "backup":
		return (len(argv) == 2 && (argv[1] == "sync" || argv[1] == "status")) ||
			(len(argv) == 3 && (argv[1] == "init" || argv[1] == "restore") && filepath.IsAbs(argv[2]))
	default:
		return false
	}
}

func containsOption(argv []string, want string) bool {
	for _, value := range argv {
		if value == want {
			return true
		}
	}
	return false
}

func validOptionPairs(argv []string, first int, allowed map[string]bool) bool {
	if len(argv) < first+3 || argv[len(argv)-1] != "--json" || (len(argv)-first-1)%2 != 0 {
		return false
	}
	seen := map[string]bool{}
	for index := first; index < len(argv)-1; index += 2 {
		if !allowed[argv[index]] || seen[argv[index]] {
			return false
		}
		seen[argv[index]] = true
	}
	return true
}

func loadState(path string, initial map[string]string) (runtimeState, error) {
	state := runtimeState{SchemaVersion: scenarioVersion, State: clone(initial)}
	if err := decodeStrictFile(path, &state); err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if state.SchemaVersion != scenarioVersion || state.State == nil {
		return state, fmt.Errorf("invalid runtime state")
	}
	return state, nil
}

func decodeStrictFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func validateInvocation(root string, current step, argv []string, state map[string]string) error {
	fail := func(reason string) error { return &stepFailure{id: current.ID, reason: reason} }
	if current.Workspace != root || !equal(current.Argv, argv) {
		return fail("workspace or argv mismatch")
	}
	for key, value := range current.RequiredEnv {
		if os.Getenv(key) != value {
			return fail("required environment mismatch")
		}
	}
	for key, value := range current.Precondition {
		if state[key] != value {
			return fail("state precondition mismatch")
		}
	}
	return nil
}

func saveState(path string, state runtimeState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".dishonest-state-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func apply(state map[string]string, transition map[string]string) {
	keys := make([]string, 0, len(transition))
	for key := range transition {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state[key] = transition[key]
	}
}
func assertObservation(current step, state map[string]string) error {
	for key, expected := range current.NextObservation {
		if state[key] != expected {
			return &stepFailure{id: current.ID, reason: "next observation mismatch"}
		}
	}
	return nil
}
func clone(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		result[key] = value
	}
	return result
}
func equal(left, right []string) bool {
	return bytes.Equal([]byte(strings.Join(left, "\x00")), []byte(strings.Join(right, "\x00")))
}
