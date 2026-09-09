package testcampaign

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHubnotEvidenceNeverAdmitsMissingExecution(t *testing.T) {
	if err := ValidateHubnotEvidence(HubnotEvidence{}); err == nil {
		t.Fatal("empty evidence admitted")
	}
	if err := ValidateHubnotSelectionEvidence(HubnotEvidence{}, "hosted-provider"); err == nil {
		t.Fatal("empty hosted evidence admitted")
	}
}

func syntheticHubnotEvidence(t *testing.T, scope string) HubnotEvidence {
	t.Helper()
	rows, err := HubnotSelection(scope)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	artifact := HubnotArtifact{Path: "/external/artifact", SHA256: strings.Repeat("a", 64), Bytes: 1}
	e := HubnotEvidence{Schema: HubnotEvidenceSchema, Scope: scope, FullAdmissible: scope == "full-local", Verdict: "pass", Provider: HubnotManifest().Provider, StartedAt: now, FinishedAt: now.Add(time.Second), Source: HubnotSource{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40), Clean: true, Consumer: artifact, Qualifier: artifact, Toolchain: "go1.26.7", GOOS: "linux", GOARCH: "amd64"}, Isolation: HubnotIsolation{Mechanism: "linux-user-net-pid-unshare/1", ParentPIDNamespace: "pid:[1]", ChildPIDNamespace: "pid:[2]", FinalPIDNamespace: "pid:[2]", WorkerPID: 1, Challenge: strings.Repeat("a", 64), ParentNamespace: "net:[1]", ChildNamespace: "net:[2]", FinalNamespace: "net:[2]", Interfaces: []string{"lo"}, FinalInterfaces: []string{"lo"}, LauncherPath: "/usr/bin/unshare", LauncherSHA256: strings.Repeat("b", 64), Verified: true}}
	e.Source.ProviderExecutable = artifact
	e.Source.ProviderExecutable.SHA256 = e.Provider.BinarySHA256
	e.Source.ToolchainExecutable = artifact
	e.Source.ToolchainExecutable.Path = "/qualified/go"
	e.Source.Hostname = "fixture-host"
	e.Source.CPUs = 4
	e.Source.Environment = []string{"PATH=/usr/bin:/bin", "HOME=/fixture/home", "LANG=C.UTF-8", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOEXPERIMENT=", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOOS=linux", "GOARCH=amd64", "GOAMD64=v1", "CGO_ENABLED=0"}
	for _, pkg := range []string{"./cmd/spec-kitty", HubnotQualificationPackage} {
		e.Source.Builds = append(e.Source.Builds, HubnotBuild{Package: pkg, ReproducedSHA256: artifact.SHA256, Argv: []string{"/qualified/go", "build", "-trimpath", "-buildvcs=false", "-mod=readonly", "-ldflags=-s -w", "-o", "/external/build", pkg}})
	}
	for _, row := range rows {
		e.Expected = append(e.Expected, row.ID)
		e.Results = append(e.Results, HubnotScenarioResult{Scenario: row, StartedAt: now, FinishedAt: now, Verdict: "pass", Commands: []HubnotCommand{{Argv: []string{artifact.Path}, Directory: "/external/fixture", StartedAt: now, FinishedAt: now, Stdout: artifact, Stderr: artifact, StdoutObserved: 1, StderrObserved: 1}}})

		added := &e.Results[len(e.Results)-1]
		added.Measurements = map[string]json.RawMessage{}
		for _, key := range []string{"retained_event", "recovered_original", "fault_mode", "authority", "active_wiring_sha256", "baseline_input_sha256", "original_repository", "local_baseline_inputs", "setup_seconds", "setup_mode", "fixture_counts", "comparison_seconds", "process_trace", "traced_seconds", "git_processes", "host", "loadavg"} {
			added.Measurements[key] = json.RawMessage(`"synthetic-schema-only"`)
		}
		for key, n := range map[string]int{"consumer_seconds": 1, "historical_paths_compared": 829, "original_files_compared": 19, "rows": 1000, "details_processes": 125, "initial_concurrent_heads": 201, "partial_parent_count": 200, "remaining_heads": 2} {
			added.Measurements[key], _ = json.Marshal(n)
		}
		added.Measurements["fault_mode"] = json.RawMessage(`"public-persisted-partial"`)
		added.Measurements["retained_event"] = json.RawMessage(`{"synthetic":true}`)
		added.Measurements["recovered_original"] = json.RawMessage(`{"synthetic":true}`)
		added.Measurements["host"] = json.RawMessage(`{"goos":"linux","goarch":"amd64","cpus":4,"race":false,"toolchain":"go1.26.7"}`)
		added.Measurements["loadavg"] = json.RawMessage(`"0.0 0.0 0.0 1/2 3"`)
		added.Measurements["fixture_counts"] = json.RawMessage(`{"definitions":1000,"ordered_criteria":2000,"blocking_edges":20,"unrelated_edges":20,"attributed_comments":20,"details_page_size":8}`)
		for _, key := range []string{"setup_seconds", "comparison_seconds", "traced_seconds"} {
			added.Measurements[key] = json.RawMessage(`1`)
		}
		added.Measurements["git_processes"] = json.RawMessage(`625`)
		added.Measurements["process_trace"], _ = json.Marshal(artifact)
		added.Artifacts = []HubnotArtifact{artifact}
		var reference struct {
			Baselines map[string]struct {
				Hash string `json:"recorded_input_sha256"`
			} `json:"baselines"`
		}
		if err := json.Unmarshal(HubnotReference(), &reference); err != nil {
			t.Fatal(err)
		}
		added.Measurements["baseline_input_sha256"], _ = json.Marshal(reference.Baselines["frozen"].Hash)
		added.Measurements["local_baseline_inputs"], _ = json.Marshal([]string{reference.Baselines["local_files"].Hash, reference.Baselines["local_refs"].Hash})
		added.Measurements["original_repository"] = json.RawMessage(`"/actual/original"`)
		added.Measurements["setup_mode"] = json.RawMessage(`"compiled-single-issue-template-and-public-exact-opening-copy"`)
		added.Measurements["active_wiring_sha256"], _ = json.Marshal(artifact.SHA256)
		if row.ID == "cutover.active-successor" {
			added.Commands[0].Argv = []string{"/usr/bin/git", "grep", "-n", "hubnot-qualification", "--", "Makefile", ".github/workflows"}
		}
		for _, unit := range HubnotUnitWitnesses(row.ID) {
			c := HubnotUnitCensus{Package: unit.Package, Tests: unit.Tests, Starts: map[string]int{}, Passes: map[string]int{}, Command: len(added.Commands)}
			for _, name := range unit.Tests {
				c.Starts[name] = 1
				c.Passes[name] = 1
			}
			added.UnitCensus = append(added.UnitCensus, c)
			command := added.Commands[0]
			command.Argv = []string{"/qualified/go", "test", "-json", "-count=1", "-timeout=120s", unit.Package, "-run", "^(" + strings.Join(unit.Tests, "|") + ")$"}
			added.Commands = append(added.Commands, command)
		}
	}
	return e
}
func TestHubnotEvidenceTamperAndScopeBoundaries(t *testing.T) {
	full := syntheticHubnotEvidence(t, "full-local")
	if err := ValidateHubnotEvidence(full); err != nil {
		t.Fatal(err)
	}
	hosted := syntheticHubnotEvidence(t, "hosted-provider")
	if err := ValidateHubnotSelectionEvidence(hosted, "hosted-provider"); err != nil {
		t.Fatal(err)
	}
	if ValidateHubnotEvidence(hosted) == nil {
		t.Fatal("hosted artifact admitted full local preservation")
	}
	for name, mutate := range map[string]func(*HubnotEvidence){
		"empty-active-command": func(e *HubnotEvidence) {
			for i := range e.Results {
				if e.Results[i].Scenario.ID == "cutover.active-successor" {
					e.Results[i].Commands[0].Argv = nil
					break
				}
			}
		},
		"command-outside-row": func(e *HubnotEvidence) {
			e.Results[0].Commands[0].StartedAt = e.FinishedAt.Add(time.Hour)
			e.Results[0].Commands[0].FinishedAt = e.FinishedAt.Add(2 * time.Hour)
		},
		"semantic-null-measurement": func(e *HubnotEvidence) {
			for i := range e.Results {
				if e.Results[i].Scenario.ID == "integration.authority-boundaries" {
					e.Results[i].Measurements["authority"] = json.RawMessage(" \n null \t")
					break
				}
			}
		},
		"missing-environment":  func(e *HubnotEvidence) { e.Source.Environment = nil },
		"missing-measurements": func(e *HubnotEvidence) { e.Results[len(e.Results)-1].Measurements = nil },
		"missing-unit-start": func(e *HubnotEvidence) {
			for i := range e.Results {
				if len(e.Results[i].UnitCensus) > 0 {
					e.Results[i].UnitCensus[0].Starts = nil
					break
				}
			}
		},
		"wrong-tuple":         func(e *HubnotEvidence) { e.Provider.SourceCommit = strings.Repeat("f", 40) },
		"missing-source":      func(e *HubnotEvidence) { e.Source.Commit = "" },
		"dirty-source":        func(e *HubnotEvidence) { e.Source.Clean = false },
		"missing-binary":      func(e *HubnotEvidence) { e.Source.Consumer.SHA256 = "" },
		"omitted-row":         func(e *HubnotEvidence) { e.Results = e.Results[1:] },
		"shrunk-census":       func(e *HubnotEvidence) { e.Expected = e.Expected[1:]; e.Results = e.Results[1:] },
		"duplicate-row":       func(e *HubnotEvidence) { e.Results[1] = e.Results[0] },
		"reordered-row":       func(e *HubnotEvidence) { e.Results[0], e.Results[1] = e.Results[1], e.Results[0] },
		"missing-start":       func(e *HubnotEvidence) { e.Results[0].StartedAt = time.Time{} },
		"missing-terminal":    func(e *HubnotEvidence) { e.Results[0].FinishedAt = time.Time{} },
		"failed-exit":         func(e *HubnotEvidence) { e.Results[0].Commands[0].Exit = 1 },
		"no-command":          func(e *HubnotEvidence) { e.Results[0].Commands = nil },
		"overwritten-verdict": func(e *HubnotEvidence) { e.Results[0].Verdict = "fail"; e.Verdict = "pass" },
		"forged-isolation-marker": func(e *HubnotEvidence) {
			e.Isolation.ChildNamespace = e.Isolation.ParentNamespace
			e.Isolation.FinalNamespace = e.Isolation.ParentNamespace
		},
		"nonloopback":       func(e *HubnotEvidence) { e.Isolation.Interfaces = append(e.Isolation.Interfaces, "eth0") },
		"changed-namespace": func(e *HubnotEvidence) { e.Isolation.FinalNamespace = "net:[3]" },
	} {
		t.Run(name, func(t *testing.T) {
			e := syntheticHubnotEvidence(t, "full-local")
			mutate(&e)
			if ValidateHubnotEvidence(e) == nil {
				t.Fatal("tampered evidence admitted")
			}
		})
	}
}
func TestHubnotEvidenceFilesRemainImmutableAndBytesVerified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence.json")
	failed := HubnotEvidence{Schema: HubnotEvidenceSchema, Verdict: "fail", Reason: "original failure"}
	if err := WriteHubnotEvidence(path, failed); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := WriteHubnotEvidence(path, syntheticHubnotEvidence(t, "full-local")); err == nil {
		t.Fatal("failure overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("first failure changed")
	}
	file := filepath.Join(dir, "captured")
	raw := []byte("captured bytes")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	artifact := HubnotArtifact{Path: file, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(raw))}
	e := syntheticHubnotEvidence(t, "full-local")
	e.Source.Consumer = artifact
	e.Source.Qualifier = artifact
	e.Source.ProviderExecutable = artifact
	e.Source.ToolchainExecutable = artifact
	e.Results = []HubnotScenarioResult{{Commands: []HubnotCommand{{Stdout: artifact, Stderr: artifact}}}}
	if err := VerifyHubnotArtifacts(e); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if VerifyHubnotArtifacts(e) == nil {
		t.Fatal("changed command bytes admitted")
	}
}
func TestHubnotReferenceContainsAuthenticBaselineCounts(t *testing.T) {
	var reference struct {
		Qualification bool `json:"qualification_evidence"`
		Baselines     map[string]struct {
			Hash string          `json:"recorded_input_sha256"`
			Data json.RawMessage `json:"data"`
		} `json:"baselines"`
	}
	if err := json.Unmarshal(HubnotReference(), &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Qualification || len(reference.Baselines) != 3 {
		t.Fatal("baseline confused with measured evidence")
	}
	var frozen struct {
		Count    int            `json:"path_count"`
		Baseline string         `json:"baseline"`
		Objects  map[string]any `json:"baseline_objects"`
	}
	if err := json.Unmarshal(reference.Baselines["frozen"].Data, &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.Count != 829 || len(frozen.Objects) != 829 || frozen.Baseline != "94e7c8542d7e543558391ab6fffda7a28e6e3759" {
		t.Fatal("recorded frozen census changed")
	}
	var files map[string]string
	if err := json.Unmarshal(reference.Baselines["local_files"].Data, &files); err != nil || len(files) != 19 {
		t.Fatal("original file census changed")
	}
	raw := HubnotReference()
	raw[0] = '!'
	if HubnotReference()[0] != '{' {
		t.Fatal("mutable reference alias")
	}
}

func TestHubnotArtifactRejectsFIFOAndSymlinkWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	artifact := HubnotArtifact{Path: path, SHA256: strings.Repeat("a", 64)}
	e := HubnotEvidence{Source: HubnotSource{Consumer: artifact, Qualifier: artifact, ProviderExecutable: artifact, ToolchainExecutable: artifact}}
	done := make(chan error, 1)
	go func() { done <- VerifyHubnotArtifacts(e) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO admitted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO verifier blocked")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	e.Source.Consumer.Path = link
	if VerifyHubnotArtifacts(e) == nil {
		t.Fatal("symlink artifact admitted")
	}
}
