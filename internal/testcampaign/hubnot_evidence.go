package testcampaign

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
)

//go:embed testdata/hubnot-qualification-reference.json
var hubnotReference []byte

// The reference contains recorded baseline inputs, not a manufactured passing
// qualification. Consumers receive a copy and verify its pinned source hashes.
func HubnotReference() []byte { return bytes.Clone(hubnotReference) }

type HubnotArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type HubnotCommand struct {
	Stdin          *HubnotArtifact `json:"stdin,omitempty"`
	StdoutObserved int64           `json:"stdout_observed_bytes"`
	StderrObserved int64           `json:"stderr_observed_bytes"`
	Argv           []string        `json:"argv"`
	Directory      string          `json:"directory"`
	Exit           int             `json:"exit"`
	ExpectedExit   int             `json:"expected_exit"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     time.Time       `json:"finished_at"`
	Stdout         HubnotArtifact  `json:"stdout"`
	Stderr         HubnotArtifact  `json:"stderr"`
}
type HubnotUnitCensus struct {
	Package  string         `json:"package"`
	Tests    []string       `json:"tests"`
	Starts   map[string]int `json:"starts"`
	Passes   map[string]int `json:"passes"`
	Subtests int            `json:"subtests"`
	Command  int            `json:"command"`
}
type HubnotScenarioResult struct {
	UnitCensus   []HubnotUnitCensus         `json:"unit_census,omitempty"`
	Artifacts    []HubnotArtifact           `json:"artifacts,omitempty"`
	Scenario     HubnotScenario             `json:"scenario"`
	StartedAt    time.Time                  `json:"started_at"`
	FinishedAt   time.Time                  `json:"finished_at"`
	Verdict      string                     `json:"verdict"`
	Reason       string                     `json:"reason,omitempty"`
	Commands     []HubnotCommand            `json:"commands"`
	Measurements map[string]json.RawMessage `json:"measurements,omitempty"`
}
type HubnotIsolation struct {
	ParentPIDNamespace string   `json:"parent_pid_namespace"`
	ChildPIDNamespace  string   `json:"child_pid_namespace"`
	FinalPIDNamespace  string   `json:"final_pid_namespace"`
	WorkerPID          int      `json:"worker_pid"`
	Mechanism          string   `json:"mechanism"`
	Challenge          string   `json:"challenge"`
	ParentNamespace    string   `json:"parent_namespace"`
	ChildNamespace     string   `json:"child_namespace"`
	FinalNamespace     string   `json:"final_namespace"`
	Interfaces         []string `json:"interfaces"`
	FinalInterfaces    []string `json:"final_interfaces"`
	LauncherPath       string   `json:"launcher_path"`
	LauncherSHA256     string   `json:"launcher_sha256"`
	Exit               int      `json:"exit"`
	Verified           bool     `json:"verified"`
}
type HubnotBuild struct {
	Argv             []string `json:"argv"`
	Package          string   `json:"package"`
	ReproducedSHA256 string   `json:"reproduced_sha256"`
}
type HubnotSource struct {
	ProviderExecutable  HubnotArtifact `json:"provider_executable"`
	ToolchainExecutable HubnotArtifact `json:"toolchain_executable"`
	Hostname            string         `json:"hostname"`
	CPUs                int            `json:"cpus"`
	Builds              []HubnotBuild  `json:"builds"`
	Commit              string         `json:"commit"`
	Tree                string         `json:"tree"`
	Clean               bool           `json:"clean"`
	Consumer            HubnotArtifact `json:"consumer"`
	Qualifier           HubnotArtifact `json:"qualifier"`
	Toolchain           string         `json:"toolchain"`
	GOOS                string         `json:"goos"`
	GOARCH              string         `json:"goarch"`
	Race                bool           `json:"race"`
	Environment         []string       `json:"environment"`
}
type HubnotEvidence struct {
	Schema         string                  `json:"schema"`
	Scope          string                  `json:"scope"`
	FullAdmissible bool                    `json:"full_admissible"`
	Verdict        string                  `json:"verdict"`
	Reason         string                  `json:"reason,omitempty"`
	Source         HubnotSource            `json:"source"`
	Provider       hubnot.ProviderIdentity `json:"provider"`
	Isolation      HubnotIsolation         `json:"isolation"`
	Expected       []string                `json:"expected"`
	StartedAt      time.Time               `json:"started_at"`
	FinishedAt     time.Time               `json:"finished_at"`
	Results        []HubnotScenarioResult  `json:"results"`
}

func hexSize(s string, n int) bool {
	raw, e := hex.DecodeString(s)
	return e == nil && len(raw) == n && s == strings.ToLower(s)
}
func validHubnotArtifact(a HubnotArtifact) bool {
	return filepath.IsAbs(a.Path) && filepath.Clean(a.Path) == a.Path && hexSize(a.SHA256, 32) && a.Bytes >= 0 && a.Bytes <= (256<<20)+1
}

func ValidateHubnotEvidence(e HubnotEvidence) error { return validateHubnotScope(e, "full-local") }
func ValidateHubnotSelectionEvidence(e HubnotEvidence, scope string) error {
	if scope == "full-local" {
		return fmt.Errorf("selected validator cannot admit full-local")
	}
	return validateHubnotScope(e, scope)
}
func validateHubnotScope(e HubnotEvidence, scope string) error {
	expected, err := HubnotSelection(scope)
	if err != nil {
		return err
	}
	if e.Schema != HubnotEvidenceSchema || e.Scope != scope || e.Verdict != "pass" || e.FullAdmissible != (scope == "full-local") {
		return fmt.Errorf("qualification scope or verdict does not admit %s", scope)
	}
	if e.Provider != hubnot.QualifiedProviderIdentity() || !hexSize(e.Source.Commit, 20) || !hexSize(e.Source.Tree, 20) || !e.Source.Clean || !validHubnotArtifact(e.Source.Consumer) || !validHubnotArtifact(e.Source.Qualifier) || e.Source.Toolchain != e.Provider.Toolchain || e.Source.GOOS != e.Provider.GOOS || e.Source.GOARCH != e.Provider.GOARCH {
		return fmt.Errorf("qualification source/build identity incomplete")
	}
	if !validHubnotArtifact(e.Source.ProviderExecutable) || e.Source.ProviderExecutable.SHA256 != e.Provider.BinarySHA256 || !validHubnotArtifact(e.Source.ToolchainExecutable) {
		return fmt.Errorf("qualified executable evidence missing")
	}
	if err := validateHubnotBuild(e.Source); err != nil {
		return err
	}
	if e.StartedAt.IsZero() || e.FinishedAt.Before(e.StartedAt) {
		return fmt.Errorf("qualification timestamps incomplete")
	}
	i := e.Isolation
	if !i.Verified || i.Mechanism != "linux-user-net-pid-unshare/1" || i.ParentPIDNamespace == "" || i.ChildPIDNamespace == "" || i.ParentPIDNamespace == i.ChildPIDNamespace || i.FinalPIDNamespace != i.ChildPIDNamespace || i.WorkerPID != 1 || !hexSize(i.Challenge, 32) || i.ParentNamespace == "" || i.ChildNamespace == "" || i.ParentNamespace == i.ChildNamespace || i.FinalNamespace != i.ChildNamespace || !slices.Equal(i.Interfaces, []string{"lo"}) || !slices.Equal(i.FinalInterfaces, i.Interfaces) || !filepath.IsAbs(i.LauncherPath) || !hexSize(i.LauncherSHA256, 32) || i.Exit != 0 {
		return fmt.Errorf("qualification isolation incomplete")
	}
	ids := make([]string, len(expected))
	for n, s := range expected {
		ids[n] = s.ID
	}
	if !slices.Equal(e.Expected, ids) || len(e.Results) != len(expected) {
		return fmt.Errorf("qualification census incomplete")
	}
	for n, want := range expected {
		row := e.Results[n]
		if !reflect.DeepEqual(row.Scenario, want) || row.Verdict != "pass" || row.StartedAt.Before(e.StartedAt) || row.FinishedAt.Before(row.StartedAt) || row.FinishedAt.After(e.FinishedAt) || len(row.Commands) == 0 {
			return fmt.Errorf("scenario %s is incomplete or contradictory", want.ID)
		}
		if err := validateHubnotRow(e.Source, row); err != nil {
			return fmt.Errorf("scenario %s: %w", want.ID, err)
		}
		for _, c := range row.Commands {
			if len(c.Argv) == 0 || !filepath.IsAbs(c.Argv[0]) || !filepath.IsAbs(c.Directory) || c.Exit != c.ExpectedExit || c.StartedAt.IsZero() || c.StartedAt.Before(row.StartedAt) || c.FinishedAt.After(row.FinishedAt) || c.FinishedAt.Before(c.StartedAt) || !validHubnotArtifact(c.Stdout) || !validHubnotArtifact(c.Stderr) || c.StdoutObserved != c.Stdout.Bytes || c.StderrObserved != c.Stderr.Bytes || (c.Stdin != nil && !validHubnotArtifact(*c.Stdin)) {
				return fmt.Errorf("scenario %s command evidence incomplete", want.ID)
			}
		}
	}
	return nil
}

// Artifacts are verified separately from their schema. A valid-looking digest or
// a top-level test pass cannot replace the original captured bytes.
func VerifyHubnotArtifacts(e HubnotEvidence) error {
	artifacts := []HubnotArtifact{e.Source.Consumer, e.Source.Qualifier, e.Source.ProviderExecutable, e.Source.ToolchainExecutable}
	unitBytes := map[string][]byte{}
	for _, r := range e.Results {
		for _, c := range r.UnitCensus {
			if c.Command < 0 || c.Command >= len(r.Commands) {
				return fmt.Errorf("unit artifact index invalid")
			}
			unitBytes[r.Commands[c.Command].Stdout.Path] = nil
		}
	}
	for _, r := range e.Results {
		artifacts = append(artifacts, r.Artifacts...)
		for _, c := range r.Commands {
			artifacts = append(artifacts, c.Stdout, c.Stderr)
			if c.Stdin != nil {
				artifacts = append(artifacts, *c.Stdin)
			}
		}
	}
	for _, a := range artifacts {
		if !validHubnotArtifact(a) {
			return fmt.Errorf("invalid artifact identity")
		}
		fd, err := syscall.Open(a.Path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), a.Path)
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != a.Bytes {
			_ = f.Close()
			return fmt.Errorf("artifact must be regular")
		}
		h := sha256.New()
		var retained bytes.Buffer
		var writer io.Writer = h
		if _, needed := unitBytes[a.Path]; needed {
			if a.Bytes > 8<<20 {
				_ = f.Close()
				return fmt.Errorf("unit artifact exceeds output bound")
			}
			writer = io.MultiWriter(h, &retained)
		}
		n, err := io.Copy(writer, io.LimitReader(f, a.Bytes+1))
		_ = f.Close()
		if _, needed := unitBytes[a.Path]; needed {
			unitBytes[a.Path] = retained.Bytes()
		}
		if err != nil || n != a.Bytes || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
			return fmt.Errorf("artifact changed: %s", a.Path)
		}
	}
	for _, r := range e.Results {
		for _, c := range r.UnitCensus {
			if c.Command < 0 || c.Command >= len(r.Commands) {
				return fmt.Errorf("unit artifact index invalid")
			}
			raw := unitBytes[r.Commands[c.Command].Stdout.Path]
			got, err := ValidateHubnotUnitOutput(raw, c.Package, c.Tests)
			if err != nil {
				return err
			}
			got.Command = c.Command
			if !reflect.DeepEqual(got, c) {
				return fmt.Errorf("raw unit census differs from receipt")
			}
		}
	}
	return nil
}

// Exclusive files preserve failed attempts. A later successful run uses a new
// directory, never updates an earlier receipt in place.
func WriteHubnotEvidence(path string, e HubnotEvidence) error {
	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(raw, '\n'))
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func ValidateHubnotUnitOutput(raw []byte, pkg string, tests []string) (HubnotUnitCensus, error) {
	c := HubnotUnitCensus{Package: pkg, Tests: slices.Clone(tests), Starts: map[string]int{}, Passes: map[string]int{}}
	d := json.NewDecoder(bytes.NewReader(raw))
	subStarts := map[string]int{}
	subPasses := map[string]int{}
	packageStarts, packagePasses := 0, 0
	for {
		var event struct{ Action, Test, Package string }
		err := d.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return c, err
		}
		if event.Package != "github.com/spec-kitty/go-kitty3/"+strings.TrimPrefix(pkg, "./") {
			return c, fmt.Errorf("unexpected unit package")
		}
		if event.Action == "skip" || event.Action == "fail" {
			return c, fmt.Errorf("selected unit execution skipped or failed")
		}
		if event.Test == "" {
			if event.Action == "start" {
				packageStarts++
			}
			if event.Action == "pass" {
				packagePasses++
			}
			continue
		}
		base, _, sub := strings.Cut(event.Test, "/")
		if !slices.Contains(tests, base) {
			return c, fmt.Errorf("unexpected unit test")
		}
		if sub {
			if event.Action == "run" {
				subStarts[event.Test]++
			}
			if event.Action == "pass" {
				subPasses[event.Test]++
			}
			continue
		}
		if event.Action == "run" {
			c.Starts[event.Test]++
		}
		if event.Action == "pass" {
			c.Passes[event.Test]++
		}
	}
	if packageStarts != 1 || packagePasses != 1 || len(c.Starts) != len(tests) || len(c.Passes) != len(tests) || len(subStarts) != len(subPasses) {
		return c, fmt.Errorf("unit start/terminal census differs")
	}
	for _, test := range tests {
		if c.Starts[test] != 1 || c.Passes[test] != 1 {
			return c, fmt.Errorf("unit witness missing or duplicated")
		}
	}
	for test, n := range subStarts {
		if n != 1 || subPasses[test] != 1 {
			return c, fmt.Errorf("unit subtest missing or duplicated")
		}
	}
	c.Subtests = len(subStarts)
	return c, nil
}

func validateHubnotBuild(s HubnotSource) error {
	if s.Hostname == "" || s.CPUs < 1 || s.Race || len(s.Builds) != 2 {
		return fmt.Errorf("source build/host evidence missing")
	}
	expected := map[string]string{"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8", "GOENV": "off", "GOWORK": "off", "GOFLAGS": "", "GOEXPERIMENT": "", "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1", "CGO_ENABLED": "0"}
	seen := map[string]bool{}
	for _, v := range s.Environment {
		k, value, ok := strings.Cut(v, "=")
		if !ok || seen[k] {
			return fmt.Errorf("duplicate/malformed build environment")
		}
		seen[k] = true
		if k == "HOME" {
			if !filepath.IsAbs(value) {
				return fmt.Errorf("build HOME missing")
			}
			continue
		}
		want, ok := expected[k]
		if !ok || value != want {
			return fmt.Errorf("unqualified build environment %s", k)
		}
	}
	if len(seen) != len(expected)+1 {
		return fmt.Errorf("build environment incomplete")
	}
	for i, b := range s.Builds {
		pkg, digest := "./cmd/spec-kitty", s.Consumer.SHA256
		if i == 1 {
			pkg, digest = HubnotQualificationPackage, s.Qualifier.SHA256
		}
		if b.Package != pkg || b.ReproducedSHA256 != digest || len(b.Argv) != 9 || b.Argv[0] != s.ToolchainExecutable.Path || !slices.Equal(b.Argv[1:6], []string{"build", "-trimpath", "-buildvcs=false", "-mod=readonly", "-ldflags=-s -w"}) || b.Argv[6] != "-o" || !filepath.IsAbs(b.Argv[7]) || b.Argv[8] != pkg {
			return fmt.Errorf("build recipe/reproduced digest differs")
		}
	}
	return nil
}
func validateHubnotRow(source HubnotSource, r HubnotScenarioResult) error {
	public := false
	for _, c := range r.Commands {
		if len(c.Argv) > 0 && c.Argv[0] == source.Consumer.Path {
			public = true
		}
	}
	if (r.Scenario.Mode == HubnotPublic || r.Scenario.Mode == HubnotPartial || r.Scenario.Mode == HubnotPerformance) && !public {
		return fmt.Errorf("compiled consumer witness missing")
	}
	for _, a := range r.Artifacts {
		if !validHubnotArtifact(a) {
			return fmt.Errorf("extra artifact invalid")
		}
	}
	for _, c := range r.UnitCensus {
		if c.Command < 0 || c.Command >= len(r.Commands) || len(c.Tests) == 0 || len(c.Starts) != len(c.Tests) || len(c.Passes) != len(c.Tests) || c.Subtests < 0 {
			return fmt.Errorf("unit census incomplete")
		}
		for _, test := range c.Tests {
			if c.Starts[test] != 1 || c.Passes[test] != 1 {
				return fmt.Errorf("unit start/terminal census incomplete")
			}
		}
		command := r.Commands[c.Command]
		if len(command.Argv) != 8 || !slices.Equal(command.Argv[1:], []string{"test", "-json", "-count=1", "-timeout=120s", c.Package, "-run", "^(" + strings.Join(c.Tests, "|") + ")$"}) {
			return fmt.Errorf("unit command/census differ")
		}
	}
	requiredUnits := HubnotUnitWitnesses(r.Scenario.ID)
	if len(requiredUnits) != len(r.UnitCensus) {
		return fmt.Errorf("required unit supplement census missing")
	}
	for i, want := range requiredUnits {
		got := r.UnitCensus[i]
		if got.Package != want.Package || !slices.Equal(got.Tests, want.Tests) || r.Commands[got.Command].Argv[0] != source.ToolchainExecutable.Path {
			return fmt.Errorf("unit supplement identity differs")
		}
	}

	required := map[string][]string{
		"graph.partial-definition": {"retained_event", "recovered_original", "fault_mode"}, "discussion.partial-pair": {"retained_event", "recovered_original", "fault_mode"},
		"bounds.large-heads":               {"initial_concurrent_heads", "partial_parent_count", "remaining_heads"},
		"integration.authority-boundaries": {"authority"}, "cutover.active-successor": {"active_wiring_sha256"},
		"cutover.frozen-source": {"historical_paths_compared", "baseline_input_sha256"}, "cutover.local-recovery": {"original_repository", "original_files_compared", "local_baseline_inputs"},
		"performance.inventory-1000":  {"setup_seconds", "setup_mode", "fixture_counts", "consumer_seconds", "comparison_seconds", "rows", "process_trace", "traced_seconds", "details_processes", "git_processes", "host", "loadavg"},
		"performance.known-reference": {"consumer_seconds", "host", "loadavg", "process_trace", "traced_seconds", "exec_counts", "observed_exec_child_pids", "untraced_command", "traced_command"}, "performance.original-recovery": {"consumer_seconds", "host", "loadavg", "process_trace", "traced_seconds", "exec_counts", "observed_exec_child_pids", "untraced_command", "traced_command"}}
	for _, key := range required[r.Scenario.ID] {
		v, ok := r.Measurements[key]
		if !ok || !json.Valid(v) || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("mandatory measurement %s missing", key)
		}
	}
	for key, want := range map[string]int{"historical_paths_compared": 829, "original_files_compared": 19, "rows": 1000, "details_processes": 125, "initial_concurrent_heads": 201, "partial_parent_count": 200, "remaining_heads": 2} {
		if raw, ok := r.Measurements[key]; ok {
			var n int
			if json.Unmarshal(raw, &n) != nil || n != want {
				return fmt.Errorf("measurement %s census differs", key)
			}
		}
	}
	var reference struct {
		Baselines map[string]struct {
			Hash string `json:"recorded_input_sha256"`
		} `json:"baselines"`
	}
	if err := json.Unmarshal(hubnotReference, &reference); err != nil {
		return err
	}
	stringMeasurement := func(key string) (string, error) {
		var value string
		if json.Unmarshal(r.Measurements[key], &value) != nil || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("measurement %s must be nonempty text", key)
		}
		return value, nil
	}
	switch r.Scenario.ID {
	case "integration.authority-boundaries":
		if _, err := stringMeasurement("authority"); err != nil {
			return err
		}
	case "cutover.active-successor":
		value, err := stringMeasurement("active_wiring_sha256")
		if err != nil || !hexSize(value, 32) {
			return fmt.Errorf("active wiring identity missing")
		}
		found := false
		for _, c := range r.Commands {
			if len(c.Argv) > 0 && filepath.Base(c.Argv[0]) == "git" && slices.Equal(c.Argv[1:], []string{"grep", "-n", "hubnot-qualification", "--", "Makefile", ".github/workflows"}) && c.Stdout.SHA256 == value {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("active wiring digest does not match captured inspection")
		}
	case "cutover.frozen-source":
		value, err := stringMeasurement("baseline_input_sha256")
		if err != nil || value != reference.Baselines["frozen"].Hash {
			return fmt.Errorf("frozen baseline identity differs")
		}
	case "cutover.local-recovery":
		value, err := stringMeasurement("original_repository")
		if err != nil || !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return fmt.Errorf("original repository identity missing")
		}
		var hashes []string
		if json.Unmarshal(r.Measurements["local_baseline_inputs"], &hashes) != nil || !slices.Equal(hashes, []string{reference.Baselines["local_files"].Hash, reference.Baselines["local_refs"].Hash}) {
			return fmt.Errorf("recorded local baseline identities differ")
		}
	case "performance.inventory-1000":
		value, err := stringMeasurement("setup_mode")
		if err != nil || value != "compiled-single-issue-template-and-public-exact-opening-copy" {
			return fmt.Errorf("fixture setup mode differs")
		}
	}
	if r.Scenario.ID == "performance.known-reference" || r.Scenario.ID == "performance.original-recovery" {
		var trace HubnotArtifact
		var seconds float64
		var fields map[string]*int
		counts := map[string]int{}
		var untracedIndex, tracedIndex int
		if json.Unmarshal(r.Measurements["process_trace"], &trace) != nil || !validHubnotArtifact(trace) || trace.Bytes == 0 || !slices.Contains(r.Artifacts, trace) {
			return fmt.Errorf("separate query trace artifact missing")
		}
		if json.Unmarshal(r.Measurements["traced_seconds"], &seconds) != nil || seconds <= 0 {
			return fmt.Errorf("separate query traced timing missing")
		}
		if json.Unmarshal(r.Measurements["exec_counts"], &fields) != nil || len(fields) != 5 {
			return fmt.Errorf("separate query process counts missing")
		}
		for _, key := range []string{"consumer", "hubnot", "git", "other", "failed_exec"} {
			n, ok := fields[key]
			if !ok || n == nil || *n < 0 {
				return fmt.Errorf("separate query process count %s invalid", key)
			}
			counts[key] = *n
		}
		var childPIDs int
		if json.Unmarshal(r.Measurements["observed_exec_child_pids"], &childPIDs) != nil || childPIDs <= 0 || childPIDs > counts["hubnot"]+counts["git"]+counts["other"] {
			return fmt.Errorf("observed successful-exec child PID census invalid")
		}
		if counts["consumer"] != 1 || counts["hubnot"] == 0 || counts["git"] == 0 {
			return fmt.Errorf("separate query child census incomplete")
		}
		if json.Unmarshal(r.Measurements["untraced_command"], &untracedIndex) != nil || json.Unmarshal(r.Measurements["traced_command"], &tracedIndex) != nil || untracedIndex < 0 || tracedIndex <= untracedIndex || tracedIndex >= len(r.Commands) {
			return fmt.Errorf("separate query command indices invalid")
		}
		u, t := r.Commands[untracedIndex], r.Commands[tracedIndex]
		action := "definition"
		if r.Scenario.ID == "performance.original-recovery" {
			action = "define"
		}
		if len(u.Argv) != 11 || u.Argv[0] != source.Consumer.Path || u.Argv[5] != "tracker" || u.Argv[6] != action || u.Stdin == nil || t.Stdin == nil || u.Stdin.SHA256 != t.Stdin.SHA256 || u.Stdin.Bytes != t.Stdin.Bytes || u.Directory != t.Directory || u.Exit != 0 || t.Exit != 0 || t.StartedAt.Before(u.FinishedAt) {
			return fmt.Errorf("separate query input/action differs")
		}
		suffix, traceErr := hubnotTraceSuffix(r, t, trace)
		if traceErr != nil || !slices.Equal(suffix, u.Argv) {
			return fmt.Errorf("separate query trace command differs")
		}
	}
	if r.Scenario.ID == "performance.inventory-1000" {
		var trace HubnotArtifact
		if json.Unmarshal(r.Measurements["process_trace"], &trace) != nil || !validHubnotArtifact(trace) || trace.Bytes <= 0 || !slices.Contains(r.Artifacts, trace) {
			return fmt.Errorf("inventory trace artifact missing")
		}
		index, err := hubnotInventoryTraceIndex(r, trace)
		if err != nil {
			return err
		}
		traced := r.Commands[index]
		suffix, err := hubnotTraceSuffix(r, traced, trace)
		if err != nil {
			return err
		}
		originals := 0
		for _, c := range r.Commands {
			if len(c.Argv) == 9 && c.Argv[0] == source.Consumer.Path && slices.Equal(c.Argv[1:3], []string{"--output", "json"}) && c.Argv[3] == "--control-root" && slices.Equal(c.Argv[5:8], []string{"tracker", "inventory", "--binding"}) {
				originals++
				expected := []string{source.Consumer.Path, "--output", "json", "tracker", "inventory", "--binding", c.Argv[8]}
				if !slices.Equal(suffix, expected) || c.Directory != traced.Directory || c.Exit != 0 || traced.Exit != 0 || traced.StartedAt.Before(c.FinishedAt) {
					return fmt.Errorf("inventory trace query differs")
				}
			}
		}
		if originals != 1 {
			return fmt.Errorf("inventory normal query census differs")
		}
	}
	if r.Scenario.Mode == HubnotPartial {
		var mode string
		var opening, recovered map[string]json.RawMessage
		if json.Unmarshal(r.Measurements["fault_mode"], &mode) != nil || mode != "public-persisted-partial" || json.Unmarshal(r.Measurements["retained_event"], &opening) != nil || len(opening) == 0 || json.Unmarshal(r.Measurements["recovered_original"], &recovered) != nil || len(recovered) == 0 {
			return fmt.Errorf("public persisted-partial evidence incomplete")
		}
	}
	if r.Scenario.Mode == HubnotPerformance {
		var host struct {
			GOOS, GOARCH, Toolchain string
			CPUs                    int
			Race                    bool
		}
		var load string
		if json.Unmarshal(r.Measurements["host"], &host) != nil || host.GOOS != source.GOOS || host.GOARCH != source.GOARCH || host.Toolchain != source.Toolchain || host.CPUs != source.CPUs || host.Race || json.Unmarshal(r.Measurements["loadavg"], &load) != nil || len(strings.Fields(load)) != 5 {
			return fmt.Errorf("performance host/mode missing")
		}
		if r.Scenario.ID == "performance.inventory-1000" {
			var counts map[string]int
			expected := map[string]int{"definitions": 1000, "ordered_criteria": 2000, "blocking_edges": 20, "unrelated_edges": 20, "attributed_comments": 20, "details_page_size": 8}
			if json.Unmarshal(r.Measurements["fixture_counts"], &counts) != nil || !reflect.DeepEqual(counts, expected) {
				return fmt.Errorf("performance fixture census differs")
			}
			var trace HubnotArtifact
			if json.Unmarshal(r.Measurements["process_trace"], &trace) != nil || !slices.Contains(r.Artifacts, trace) {
				return fmt.Errorf("process trace bytes missing")
			}
			for _, key := range []string{"setup_seconds", "comparison_seconds", "traced_seconds"} {
				var n float64
				if json.Unmarshal(r.Measurements[key], &n) != nil || n <= 0 {
					return fmt.Errorf("performance timing %s missing", key)
				}
			}
			var git int
			if json.Unmarshal(r.Measurements["git_processes"], &git) != nil || git < 125 {
				return fmt.Errorf("git process census missing")
			}
		}
	}
	if r.Scenario.Mode == HubnotPerformance {
		var seconds float64
		if json.Unmarshal(r.Measurements["consumer_seconds"], &seconds) != nil || seconds <= 0 || seconds > 120 {
			return fmt.Errorf("bounded consumer timing missing or exceeded")
		}
	}
	return nil
}

// Trace command forms are closed: the two recorded historical strace layouts,
// or the current diagnostic-affinity launcher with complete matching metadata.
func hubnotTraceSuffix(r HubnotScenarioResult, c HubnotCommand, trace HubnotArtifact) ([]string, error) {
	args := c.Argv
	raw, present := r.Measurements["trace_affinity"]
	if len(args) == 0 {
		return nil, fmt.Errorf("trace argv missing")
	}
	if args[0] == "/usr/bin/taskset" || args[0] == "/bin/taskset" {
		var fields map[string]json.RawMessage
		if !present || json.Unmarshal(raw, &fields) != nil || len(fields) != 6 {
			return nil, fmt.Errorf("affinity metadata missing")
		}
		var mode, mask, path, digest string
		var cpu *int
		var size *int64
		if json.Unmarshal(fields["mode"], &mode) != nil || mode != "diagnostic-only-first-allowed-cpu/1" || json.Unmarshal(fields["parent_allowed_cpus"], &mask) != nil || json.Unmarshal(fields["requested_cpu"], &cpu) != nil || cpu == nil || json.Unmarshal(fields["taskset_path"], &path) != nil || json.Unmarshal(fields["taskset_sha256"], &digest) != nil || !hexSize(digest, 32) || json.Unmarshal(fields["taskset_bytes"], &size) != nil || size == nil || *size <= 0 || path != args[0] {
			return nil, fmt.Errorf("affinity fields invalid")
		}
		first, err := hubnotFirstCPU(mask)
		if err != nil || first != *cpu {
			return nil, fmt.Errorf("requested CPU differs from parent first allowed CPU")
		}
		if len(args) < 15 || (args[3] != "/usr/bin/strace" && args[3] != "/bin/strace") || !slices.Equal(args[1:3], []string{"--cpu-list", strconv.Itoa(*cpu)}) || !slices.Equal(args[4:14], []string{"--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-e", "signal=none", "-o", trace.Path}) {
			return nil, fmt.Errorf("affinity trace prefix differs")
		}
		return args[14:], nil
	}
	if present {
		return nil, fmt.Errorf("legacy trace contains affinity metadata")
	}
	if args[0] != "/usr/bin/strace" && args[0] != "/bin/strace" {
		return nil, fmt.Errorf("unknown trace launcher")
	}
	for _, prefix := range [][]string{{"--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-o", trace.Path}, {"--seccomp-bpf", "-f", "-s", "8192", "-e", "trace=execve", "-e", "signal=none", "-o", trace.Path}} {
		if len(args) > len(prefix)+1 && slices.Equal(args[1:len(prefix)+1], prefix) {
			return args[len(prefix)+1:], nil
		}
	}
	return nil, fmt.Errorf("historical trace prefix differs")
}
func hubnotFirstCPU(mask string) (int, error) {
	if len(mask) == 0 || len(mask) > 4096 {
		return 0, fmt.Errorf("CPU list missing or excessive")
	}
	first, previous := -1, -1
	for _, part := range strings.Split(mask, ",") {
		ends := strings.Split(part, "-")
		if len(ends) > 2 {
			return 0, fmt.Errorf("CPU range malformed")
		}
		bounds := []int{}
		for _, v := range ends {
			n, e := strconv.ParseUint(v, 10, 31)
			if e != nil || strconv.FormatUint(n, 10) != v {
				return 0, fmt.Errorf("CPU integer malformed")
			}
			bounds = append(bounds, int(n))
		}
		lo, hi := bounds[0], bounds[len(bounds)-1]
		if lo <= previous || hi < lo {
			return 0, fmt.Errorf("CPU ranges unordered")
		}
		if first < 0 {
			first = lo
		}
		previous = hi
	}
	return first, nil
}
func hubnotInventoryTraceIndex(r HubnotScenarioResult, trace HubnotArtifact) (int, error) {
	if raw, exists := r.Measurements["traced_command"]; exists {
		var index *int
		if json.Unmarshal(raw, &index) != nil || index == nil || *index < 0 || *index >= len(r.Commands) {
			return 0, fmt.Errorf("inventory trace index invalid")
		}
		if _, err := hubnotTraceSuffix(r, r.Commands[*index], trace); err != nil {
			return 0, err
		}
		return *index, nil
	}
	if _, present := r.Measurements["trace_affinity"]; present {
		return 0, fmt.Errorf("affinity inventory index missing")
	}
	found := -1
	for i, c := range r.Commands {
		if len(c.Argv) == 0 {
			continue
		}
		if filepath.Base(c.Argv[0]) == "taskset" {
			return 0, fmt.Errorf("affinity inventory index missing")
		}
		if filepath.Base(c.Argv[0]) != "strace" {
			continue
		}
		if found >= 0 {
			return 0, fmt.Errorf("duplicate historical inventory trace")
		}
		if _, err := hubnotTraceSuffix(r, c, trace); err != nil {
			return 0, err
		}
		found = i
	}
	if found < 0 {
		return 0, fmt.Errorf("inventory trace missing")
	}
	return found, nil
}
