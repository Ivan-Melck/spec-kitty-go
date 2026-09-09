package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
)

const consumerOutputLimit = (256 << 20) + 1

type limitedOutput struct {
	mu       sync.Mutex
	data     bytes.Buffer
	limit    int
	observed int64
	exceeded bool
	overflow func()
	// sink receives only retained bytes, immediately; process teardown must not
	// discard diagnostics waiting in the worker's memory.
	sink    io.Writer
	sinkErr error
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.observed += int64(len(p))
	n := len(p)
	remaining := w.limit - w.data.Len()
	if remaining < 0 {
		remaining = 0
	}
	if n > remaining {
		w.exceeded = true
		p = p[:remaining]
	}
	_, _ = w.data.Write(p)
	if w.sink != nil && w.sinkErr == nil {
		n, err := w.sink.Write(p)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		w.sinkErr = err
	}
	if (w.exceeded || w.sinkErr != nil) && w.overflow != nil {
		w.overflow()
	}
	return n, nil
}
func (w *limitedOutput) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.data.Bytes())
}

type qualificationOptions struct {
	Repository         string        `json:"repository"`
	Product            string        `json:"product"`
	HN                 string        `json:"hn"`
	GoBinary           string        `json:"go_binary"`
	EvidenceDir        string        `json:"evidence_dir"`
	OriginalRepository string        `json:"original_repository"`
	Scope              string        `json:"scope"`
	Timeout            time.Duration `json:"timeout"`
}
type workerRequest struct {
	Options                                                                                        qualificationOptions
	Challenge, ParentNamespace, ParentPIDNamespace, ExecutableSHA256, LauncherPath, LauncherSHA256 string
}

func qualificationMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "__worker" {
		return isolatedWorker(ctx, stderr)
	}
	fs := flag.NewFlagSet("hubnot-qualification", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o qualificationOptions
	fs.StringVar(&o.Repository, "repository", "", "explicit clean source checkout")
	fs.StringVar(&o.Product, "product", "", "externally built compiled Go Kitty executable")
	fs.StringVar(&o.HN, "hn", "", "exact admitted hn executable")
	fs.StringVar(&o.GoBinary, "go-bin", "", "explicit Go 1.26.7 executable")
	fs.StringVar(&o.EvidenceDir, "evidence-dir", "", "new external directory; failed attempts never overwritten")
	fs.StringVar(&o.OriginalRepository, "original-repository", "", "actual original repository for full-local recovery observations")
	fs.StringVar(&o.Scope, "scope", "full-local", "full-local, hosted-provider (non-full), or identity-smoke (non-full)")
	fs.DurationVar(&o.Timeout, "timeout", 30*time.Minute, "whole run deadline, maximum one hour")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	if _, err := testcampaign.HubnotSelection(o.Scope); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if o.Timeout <= 0 || o.Timeout > time.Hour {
		return 2
	}
	if !filepath.IsAbs(o.EvidenceDir) || filepath.Clean(o.EvidenceDir) != o.EvidenceDir {
		fmt.Fprintln(stderr, "new absolute evidence directory required")
		return 2
	}
	if err := os.Mkdir(o.EvidenceDir, 0700); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	e, err := superviseQualification(ctx, o)
	if err != nil {
		fmt.Fprintln(stderr, "qualification:", err)
	}
	path := filepath.Join(o.EvidenceDir, "qualification.json")
	if writeErr := testcampaign.WriteHubnotEvidence(path, e); writeErr != nil {
		fmt.Fprintln(stderr, writeErr)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{"scope": o.Scope, "verdict": e.Verdict, "full_admissible": e.FullAdmissible, "evidence": path})
	if err != nil || e.Verdict != "pass" {
		return 1
	}
	return 0
}
func emptyEvidence(o qualificationOptions) testcampaign.HubnotEvidence {
	e := testcampaign.HubnotEvidence{Schema: testcampaign.HubnotEvidenceSchema, Scope: o.Scope, Verdict: "non_admissible", Provider: hubnot.QualifiedProviderIdentity(), StartedAt: time.Now().UTC()}
	selected, _ := testcampaign.HubnotSelection(o.Scope)
	for _, s := range selected {
		e.Expected = append(e.Expected, s.ID)
	}
	return e
}
func currentIsolation() (string, []string, error) {
	if runtime.GOOS != "linux" {
		return "", nil, errors.New("network namespaces on Linux required")
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return "", nil, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", nil, err
	}
	names := make([]string, 0, len(interfaces))
	for _, i := range interfaces {
		if i.Flags&net.FlagLoopback == 0 {
			return ns, nil, errors.New("non-loopback interface present")
		}
		names = append(names, i.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"lo"}) {
		return ns, names, errors.New("loopback-only topology unavailable")
	}
	return ns, names, nil
}
func superviseQualification(ctx context.Context, o qualificationOptions) (e testcampaign.HubnotEvidence, err error) {
	e = emptyEvidence(o)
	defer func() {
		e.FinishedAt = time.Now().UTC()
		if err != nil {
			e.Reason = err.Error()
			e.Verdict = "non_admissible"
			e.FullAdmissible = false
		}
	}()
	self, err := os.Executable()
	if err != nil {
		return e, err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return e, err
	}
	a, err := fileArtifact(self)
	if err != nil {
		return e, err
	}
	launcher, err := systemTool("unshare")
	if err != nil {
		return e, err
	}
	la, err := fileArtifact(launcher)
	if err != nil {
		return e, err
	}
	parentNS, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return e, err
	}
	parentPIDNS, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return e, err
	}
	challenge := make([]byte, 32)
	if _, err = rand.Read(challenge); err != nil {
		return e, err
	}
	req := workerRequest{Options: o, Challenge: hex.EncodeToString(challenge), ParentNamespace: parentNS, ParentPIDNamespace: parentPIDNS, ExecutableSHA256: a.SHA256, LauncherPath: launcher, LauncherSHA256: la.SHA256}
	inputR, inputW, err := os.Pipe()
	if err != nil {
		return e, err
	}
	defer inputR.Close()
	defer inputW.Close()
	reportR, reportW, err := os.Pipe()
	if err != nil {
		return e, err
	}
	defer reportR.Close()
	defer reportW.Close()
	c := exec.CommandContext(ctx, launcher, "--user", "--map-root-user", "--net", "--pid", "--fork", "--kill-child=SIGKILL", "--", self, "__worker")
	c.Env = buildEnvironment()
	c.ExtraFiles = []*os.File{inputR, reportW}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.WaitDelay = time.Second
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	diagnostics := &limitedOutput{limit: 64 << 10, overflow: func() { _ = c.Cancel() }}
	c.Stderr = diagnostics
	c.Stdout = diagnostics
	if err = c.Start(); err != nil {
		return e, err
	}
	_ = inputR.Close()
	_ = reportW.Close()
	if err = json.NewEncoder(inputW).Encode(req); err != nil {
		_ = c.Cancel()
	}
	_ = inputW.Close()
	waitErr := c.Wait()
	_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	report, readErr := io.ReadAll(io.LimitReader(reportR, 4097))
	_ = reportR.Close()
	if err = writeNew(filepath.Join(o.EvidenceDir, "worker.stderr"), diagnostics.bytes()); err != nil {
		return e, err
	}
	if waitErr != nil || readErr != nil || len(report) > 4096 {
		return e, fmt.Errorf("isolated worker failed: %v", waitErr)
	}
	var isolation testcampaign.HubnotIsolation
	if err = json.Unmarshal(report, &isolation); err != nil {
		return e, err
	}
	if isolation.Challenge != req.Challenge || isolation.ParentNamespace != parentNS || isolation.ChildNamespace == parentNS || isolation.ParentPIDNamespace != parentPIDNS || isolation.ChildPIDNamespace == parentPIDNS || !isolation.Verified {
		return e, errors.New("worker correlation/isolation mismatch")
	}
	raw, err := os.ReadFile(filepath.Join(o.EvidenceDir, "worker.json"))
	if err != nil {
		return e, err
	}
	if err = json.Unmarshal(raw, &e); err != nil {
		return e, err
	}
	e.Isolation = isolation
	if e.Verdict == "pass" {
		if o.Scope == "full-local" {
			err = testcampaign.ValidateHubnotEvidence(e)
		} else {
			err = testcampaign.ValidateHubnotSelectionEvidence(e, o.Scope)
		}
		if err == nil {
			err = testcampaign.VerifyHubnotArtifacts(e)
		}
	}
	return e, err
}
func isolatedWorker(ctx context.Context, stderr io.Writer) int {
	syscall.CloseOnExec(3)
	syscall.CloseOnExec(4)
	input := os.NewFile(3, "supervisor-input")
	report := os.NewFile(4, "supervisor-report")
	if input == nil || report == nil {
		return 2
	}
	defer input.Close()
	defer report.Close()
	for _, f := range []*os.File{input, report} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return 2
		}
	}
	var req workerRequest
	dec := json.NewDecoder(io.LimitReader(input, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return 2
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return 2
	}
	_ = input.Close()
	self, err := os.Executable()
	if err != nil {
		return 2
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return 2
	}
	a, err := fileArtifact(self)
	if err != nil || a.SHA256 != req.ExecutableSHA256 {
		return 2
	}
	pidNS, pidErr := os.Readlink("/proc/self/ns/pid")
	ns, names, err := currentIsolation()
	if pidErr != nil || pidNS == req.ParentPIDNamespace || os.Getpid() != 1 || err != nil || ns == req.ParentNamespace || len(req.Challenge) != 64 {
		fmt.Fprintln(stderr, "network isolation verification failed")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, req.Options.Timeout)
	defer cancel()
	e := emptyEvidence(req.Options)
	e.Isolation = testcampaign.HubnotIsolation{Mechanism: "linux-user-net-pid-unshare/1", ParentPIDNamespace: req.ParentPIDNamespace, ChildPIDNamespace: pidNS, WorkerPID: os.Getpid(), Challenge: req.Challenge, ParentNamespace: req.ParentNamespace, ChildNamespace: ns, Interfaces: names, LauncherPath: req.LauncherPath, LauncherSHA256: req.LauncherSHA256}
	e.Source, err = qualifySource(ctx, req.Options, self)
	if err != nil {
		e.Reason = err.Error()
	} else {
		runScenarios(ctx, req.Options, &e)
	}
	endNS, endNames, endErr := currentIsolation()
	e.Isolation.FinalNamespace = endNS
	e.Isolation.FinalInterfaces = endNames
	endPIDNS, endPIDErr := os.Readlink("/proc/self/ns/pid")
	e.Isolation.FinalPIDNamespace = endPIDNS
	e.Isolation.Verified = endErr == nil && endPIDErr == nil && pidNS == endPIDNS && os.Getpid() == 1 && ns == endNS && slices.Equal(names, endNames)
	e.FinishedAt = time.Now().UTC()
	if !e.Isolation.Verified {
		e.Verdict = "non_admissible"
		e.FullAdmissible = false
		e.Reason = "network namespace changed"
	}
	if err = testcampaign.WriteHubnotEvidence(filepath.Join(req.Options.EvidenceDir, "worker.json"), e); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err = json.NewEncoder(report).Encode(e.Isolation); err != nil {
		return 1
	}
	return 0
}
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, err := f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return closeErr
}
func qualifySource(ctx context.Context, o qualificationOptions, self string) (testcampaign.HubnotSource, error) {
	var s testcampaign.HubnotSource
	head, tree, err := gitIdentity(ctx, o.Repository)
	if err != nil {
		return s, err
	}
	if err = verifyGo(ctx, o.GoBinary); err != nil {
		return s, err
	}
	hn, err := fileArtifact(o.HN)
	if err != nil || hn.SHA256 != hubnot.QualifiedProviderIdentity().BinarySHA256 {
		return s, errors.New("provider binary does not match admitted tuple")
	}
	product, err := fileArtifact(o.Product)
	if err != nil {
		return s, err
	}
	qualifier, err := fileArtifact(self)
	if err != nil {
		return s, err
	}
	dir, err := os.MkdirTemp(o.EvidenceDir, "build-identity-")
	if err != nil {
		return s, err
	}
	defer os.RemoveAll(dir)
	var builds []testcampaign.HubnotBuild
	for _, target := range []struct {
		pkg      string
		artifact testcampaign.HubnotArtifact
	}{{"./cmd/spec-kitty", product}, {testcampaign.HubnotQualificationPackage, qualifier}} {
		output := filepath.Join(dir, filepath.Base(target.pkg))
		args := []string{"build", "-trimpath", "-buildvcs=false", "-mod=readonly", "-ldflags=-s -w", "-o", output, target.pkg}
		if _, err = toolOutput(ctx, o.Repository, buildEnvironment(), o.GoBinary, args...); err != nil {
			return s, err
		}
		rebuilt, err := fileArtifact(output)
		if err != nil || rebuilt.SHA256 != target.artifact.SHA256 {
			return s, fmt.Errorf("%s executable does not reproduce from clean source", target.pkg)
		}
		builds = append(builds, testcampaign.HubnotBuild{Argv: append([]string{o.GoBinary}, args...), Package: target.pkg, ReproducedSHA256: rebuilt.SHA256})
	}
	after, afterTree, err := gitIdentity(ctx, o.Repository)
	if err != nil || after != head || afterTree != tree {
		return s, errors.New("source changed during qualification preflight")
	}
	goArtifact, err := fileArtifact(o.GoBinary)
	if err != nil {
		return s, err
	}
	host, err := os.Hostname()
	if err != nil {
		return s, err
	}
	s = testcampaign.HubnotSource{ProviderExecutable: hn, ToolchainExecutable: goArtifact, Hostname: host, CPUs: runtime.NumCPU(), Builds: builds, Commit: head, Tree: tree, Clean: true, Consumer: product, Qualifier: qualifier, Toolchain: hubnot.QualifiedProviderIdentity().Toolchain, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Environment: buildEnvironment()}
	return s, nil
}

// runScenarios records every selected row, including prerequisites and failures.
// Handler errors never erase completed command artifacts or earlier failures.
func runScenarios(ctx context.Context, o qualificationOptions, e *testcampaign.HubnotEvidence) {
	selected, _ := testcampaign.HubnotSelection(o.Scope)
	r := newScenarioRunner(o)
	r.sourceCommit, r.sourceTree, r.runChallenge = e.Source.Commit, e.Source.Tree, e.Isolation.Challenge
	defer r.close()
	e.Verdict = "pass"
	for _, scenario := range selected {
		row := testcampaign.HubnotScenarioResult{Scenario: scenario, StartedAt: time.Now().UTC(), Verdict: "pass", Measurements: map[string]json.RawMessage{}}
		r.row = &row
		ns, _, err := currentIsolation()
		if err == nil && ns != e.Isolation.ChildNamespace {
			err = errors.New("scenario network namespace changed")
		}
		if err == nil {
			err = r.run(ctx, scenario)
		}
		if err != nil {
			row.Verdict = "fail"
			var missing *prerequisiteError
			if errors.As(err, &missing) || ctx.Err() != nil {
				row.Verdict = "non_admissible"
			}
			row.Reason = err.Error()
			if e.Verdict != "fail" {
				e.Verdict = row.Verdict
			}
			if row.Verdict == "fail" {
				e.Verdict = "fail"
			}
		}
		row.FinishedAt = time.Now().UTC()
		e.Results = append(e.Results, row)
		raw, _ := json.MarshalIndent(row, "", "  ")
		if err = writeNew(filepath.Join(o.EvidenceDir, scenario.ID+".json"), raw); err != nil {
			e.Verdict = "fail"
			e.Reason = err.Error()
		}
	}
	head, tree, err := gitIdentity(ctx, o.Repository)
	if err != nil || head != e.Source.Commit || tree != e.Source.Tree {
		e.Verdict = "non_admissible"
		e.Reason = "source identity changed"
	}
	e.FullAdmissible = e.Verdict == "pass" && o.Scope == "full-local"
}

type prerequisiteError struct{ reason string }

func (e *prerequisiteError) Error() string { return e.reason }
func prerequisite(format string, args ...any) error {
	return &prerequisiteError{fmt.Sprintf(format, args...)}
}

func rawMeasurement(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func shaBytes(raw []byte) string               { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
func cleanFixtureEnvironment(home string) []string {
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	for _, v := range buildEnvironment() {
		if strings.HasPrefix(v, "GO") || strings.HasPrefix(v, "CGO_") {
			env = append(env, v)
		}
	}
	return env
}
