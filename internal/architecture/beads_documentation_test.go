package architecture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	qualifiedBeadsVersion = "1.2.2"
	qualifiedBeadsDigest  = "0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6"
	qualifiedBeadsSource  = "6c124203e771433a3550c348771a5b5e27fd3c21"
)

type phase2Qualification struct {
	SchemaVersion          int    `json:"schema_version"`
	SourceCommit           string `json:"source_commit"`
	SourceTree             string `json:"source_tree"`
	ProviderVersion        string `json:"provider_version"`
	ProviderDigestSHA256   string `json:"provider_digest_sha256"`
	ProtocolSchema         int    `json:"protocol_schema"`
	StorageMode            string `json:"storage_mode"`
	StartedAt              string `json:"started_at"`
	FinishedAt             string `json:"finished_at"`
	AuthorityAudits        int    `json:"authority_audits"`
	AuthorityInvariantFail int    `json:"authority_invariant_failures"`
	Verdict                string `json:"verdict"`
	Scenarios              struct {
		Expected int `json:"expected"`
		Started  int `json:"started"`
		Terminal int `json:"terminal"`
		Passed   int `json:"passed"`
		Failed   int `json:"failed"`
	} `json:"scenarios"`
	Contention struct {
		Attempted  int `json:"attempted"`
		Terminal   int `json:"terminal"`
		Passed     int `json:"passed"`
		Collisions int `json:"collisions"`
		Duplicates int `json:"duplicates"`
	} `json:"contention"`
}

type phase2Census struct {
	SchemaVersion int    `json:"schema_version"`
	Verdict       string `json:"verdict"`
	Portable      struct {
		Expected int `json:"expected"`
		Started  int `json:"started"`
		Terminal int `json:"terminal"`
		Passed   int `json:"passed"`
		Failed   int `json:"failed"`
	} `json:"portable"`
	Adversarial struct {
		Expected int `json:"expected"`
		Started  int `json:"started"`
		Terminal int `json:"terminal"`
		Passed   int `json:"passed"`
		Failed   int `json:"failed"`
	} `json:"adversarial"`
	RealProvider struct {
		Expected int `json:"expected"`
		Started  int `json:"started"`
		Terminal int `json:"terminal"`
		Passed   int `json:"passed"`
		Failed   int `json:"failed"`
	} `json:"real_provider"`
	HostedBaseline struct {
		Expected int    `json:"expected"`
		Started  int    `json:"started"`
		Terminal int    `json:"terminal"`
		Passed   int    `json:"passed"`
		Failed   int    `json:"failed"`
		Verdict  string `json:"verdict"`
	} `json:"hosted_baseline"`
}

func TestBeadsDocumentationDeliveryClaims(t *testing.T) {
	root := phase2RepositoryRoot(t)
	documents := map[string]string{
		"README.md":                      phase2Read(t, root, "README.md"),
		"docs/TECHNICAL_PROJECT_PLAN.md": phase2Read(t, root, "docs/TECHNICAL_PROJECT_PLAN.md"),
	}
	for path, document := range documents {
		flat := phase2Flat(document)
		for _, required := range []string{
			"Beads executable: 1.2.2",
			"source tag commit: 6c124203e771433a3550c348771a5b5e27fd3c21",
			"BD_JSON_ENVELOPE=1",
			"schema_version: 1",
			"argv-only documented JSON CLI",
			"canonical Beads workspace",
			"guarded arbitrary update",
			"safe arbitrary compensation",
			"one machine and one filesystem authority",
			"SQLite",
			"Gapdb",
		} {
			if !strings.Contains(flat, required) {
				t.Errorf("%s lacks required Phase 2 truth %q", path, required)
			}
		}
		if err := phase2RejectLivingClaims(flat); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}

	readme := phase2Flat(documents["README.md"])
	for _, required := range []string{
		"inspection is read-only",
		"--stealth --skip-agents --skip-hooks --non-interactive",
		"JSONL interchange",
		"Dolt-native restorable backup",
		"assignment is not an execution lease",
		"readiness is not permission to run",
		"comments are not review verdicts",
		"tracker closure or status is not mission acceptance",
		"make beads-contract",
		"make beads-qualification",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README.md lacks operational statement %q", required)
		}
	}
}

func TestBeadsDocumentationLinksMermaidAndCommands(t *testing.T) {
	root := phase2RepositoryRoot(t)
	readme := phase2Read(t, root, "README.md")
	makefile := phase2Read(t, root, "Makefile")
	runner := phase2Read(t, root, "internal/testcampaign/cmd/beads-qualification/runner.go")
	evidenceIndex := phase2Read(t, root, "docs/phase2-evidence/README.md")
	const checksumCommand = "(cd docs/phase2-evidence && sha256sum --check SHA256SUMS)"
	for path, document := range map[string]string{
		"README.md":                      readme,
		"docs/phase2-evidence/README.md": evidenceIndex,
	} {
		if !strings.Contains(document, checksumCommand) {
			t.Errorf("%s lacks the repository-root-safe checksum command", path)
		}
		if strings.Contains(document, "sha256sum --check docs/phase2-evidence/SHA256SUMS") {
			t.Errorf("%s contains a checksum command whose manifest paths resolve from the wrong directory", path)
		}
	}
	paths := []string{
		"README.md",
		"docs/TECHNICAL_PROJECT_PLAN.md",
		"docs/phase2-evidence/README.md",
		"docs/phase2-evidence/results.md",
		"docs/phase2-evidence/limitations.md",
	}
	for _, path := range paths {
		document := phase2Read(t, root, path)
		if strings.Count(document, "```")%2 != 0 {
			t.Errorf("%s has an unbalanced fenced code block", path)
		}
		phase2CheckMermaid(t, path, document)
		phase2CheckRelativeLinks(t, root, path, document)
	}

	targets := []string{"build", "beads-contract", "quality", "beads-qualification", "full-campaign"}
	for _, target := range targets {
		matched, err := regexp.MatchString("(?m)^"+regexp.QuoteMeta(target)+":", makefile)
		if err != nil || !matched {
			t.Errorf("documented Make target %q is absent", target)
		}
	}
	phase2SmokeMakeTargets(t, root, targets)
	for _, path := range []string{"cmd/spk-phase1-helper", "scripts/install-qualified-beads.sh"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("documented local command path %s: %v", path, err)
		}
	}
	storageKeys, err := phase2QualificationStorageModeContract(runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := phase2ValidateStorageModeSemantics(storageKeys); err != nil {
		t.Fatal(err)
	}
	if err := phase2ValidateQualificationCommand(readme, makefile, storageKeys); err != nil {
		t.Error(err)
	}
	binary, help := phase2SmokeHelperAndInstaller(t, root)
	if err := phase2ValidateHelperCommand(readme, help); err != nil {
		t.Error(err)
	}
	phase2SmokeDocumentedHelper(t, root, binary, readme)
	if err := phase2ValidateInstallerCommand(readme); err != nil {
		t.Error(err)
	}
	providerArgv, err := phase2TypedProviderArgv(root)
	if err != nil {
		t.Fatal(err)
	}
	initArgv, err := phase2AcceptedDisposableInitArgv(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := phase2ValidateProviderCommands(readme, providerArgv, initArgv); err != nil {
		t.Error(err)
	}
	phase2RunCommand(t, root, 0, "go", "test", "./internal/tracker/beads", "-run", "^TestCommandArgvVocabulary$", "-count=1")
}

func TestPhase2EvidenceIntegrity(t *testing.T) {
	root := phase2RepositoryRoot(t)
	evidenceDir := filepath.Join(root, "docs", "phase2-evidence")
	var qualification phase2Qualification
	phase2Decode(t, filepath.Join(evidenceDir, "qualification-evidence.json"), &qualification)
	if qualification.SchemaVersion != 1 || qualification.ProviderVersion != qualifiedBeadsVersion || qualification.ProviderDigestSHA256 != qualifiedBeadsDigest || qualification.ProtocolSchema != 1 {
		t.Fatalf("qualification tuple is not the exact approved v1.2.2/schema-1/digest tuple: %+v", qualification)
	}
	if err := phase2ValidateSourceIdentity(qualification.SourceCommit, qualification.SourceTree); err != nil {
		t.Fatal(err)
	}
	if qualification.StorageMode != "server_multiwriter" || qualification.Verdict != "pass" || qualification.AuthorityInvariantFail != 0 || qualification.AuthorityAudits == 0 {
		t.Fatalf("qualification result is not admissible: %+v", qualification)
	}
	if err := phase2EqualCensus(qualification.Scenarios.Expected, qualification.Scenarios.Started, qualification.Scenarios.Terminal, qualification.Scenarios.Passed, qualification.Scenarios.Failed); err != nil {
		t.Fatal(err)
	}
	if qualification.Contention.Attempted != 1000 || qualification.Contention.Terminal != 1000 || qualification.Contention.Passed != 1000 || qualification.Contention.Collisions != 0 || qualification.Contention.Duplicates != 0 {
		t.Fatalf("real contention census changed: %+v", qualification.Contention)
	}
	if _, err := time.Parse(time.RFC3339Nano, qualification.StartedAt); err != nil {
		t.Fatalf("qualification start time: %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, qualification.FinishedAt); err != nil {
		t.Fatalf("qualification finish time: %v", err)
	}

	var census phase2Census
	phase2Decode(t, filepath.Join(evidenceDir, "campaign-census.json"), &census)
	if census.SchemaVersion != 1 || census.Verdict != "pass" {
		t.Fatalf("campaign census verdict=%q schema=%d", census.Verdict, census.SchemaVersion)
	}
	for name, counts := range map[string][5]int{
		"portable":      {census.Portable.Expected, census.Portable.Started, census.Portable.Terminal, census.Portable.Passed, census.Portable.Failed},
		"adversarial":   {census.Adversarial.Expected, census.Adversarial.Started, census.Adversarial.Terminal, census.Adversarial.Passed, census.Adversarial.Failed},
		"real_provider": {census.RealProvider.Expected, census.RealProvider.Started, census.RealProvider.Terminal, census.RealProvider.Passed, census.RealProvider.Failed},
	} {
		if err := phase2EqualCensus(counts[0], counts[1], counts[2], counts[3], counts[4]); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if census.Portable.Expected != 2 || census.Adversarial.Expected != 20 || census.RealProvider.Expected != 11 {
		t.Fatalf("unexpected campaign census: %+v", census)
	}
	if census.HostedBaseline.Expected != 10 || census.HostedBaseline.Started != 0 || census.HostedBaseline.Terminal != 0 || census.HostedBaseline.Passed != 0 || census.HostedBaseline.Failed != 0 || census.HostedBaseline.Verdict != "baseline_pending" {
		t.Fatalf("hosted baseline was laundered or changed: %+v", census.HostedBaseline)
	}
	phase2VerifyChecksums(t, evidenceDir)
}

func TestPhase2EvidenceDocumentationMutants(t *testing.T) {
	for name, fixture := range map[string]string{
		"public API":            "production uses a published Beads Go consumer API",
		"authority":             "Beads assignment authorizes execution",
		"export backup":         "JSONL export is a complete restorable backup",
		"loose version":         "any supported Beads version is accepted",
		"read write is CAS":     "read-before-write is compare-and-swap",
		"tracker accepts work":  "tracker status advances the mission",
		"provisional phase one": "SQLite in WAL mode is the leading implementation choice",
	} {
		t.Run(name, func(t *testing.T) {
			if err := phase2RejectLivingClaims(fixture); err == nil {
				t.Fatalf("mutant was accepted: %q", fixture)
			}
		})
	}
	for name, values := range map[string][5]int{
		"no scenarios started": {11, 0, 0, 0, 0},
		"missing terminal":     {11, 11, 10, 10, 0},
		"hidden failure":       {11, 11, 11, 11, 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := phase2EqualCensus(values[0], values[1], values[2], values[3], values[4]); err == nil {
				t.Fatalf("census mutant was accepted: %v", values)
			}
		})
	}
	t.Run("missing source identity", func(t *testing.T) {
		if err := phase2ValidateSourceIdentity("", strings.Repeat("a", 40)); err == nil {
			t.Fatal("missing source commit mutant was accepted")
		}
	})
	t.Run("corrupt source tree identity", func(t *testing.T) {
		if err := phase2ValidateSourceIdentity(strings.Repeat("a", 40), "not-a-tree"); err == nil {
			t.Fatal("corrupt source tree mutant was accepted")
		}
	})

	root := phase2RepositoryRoot(t)
	readme := phase2Read(t, root, "README.md")
	makefile := phase2Read(t, root, "Makefile")
	runner := phase2Read(t, root, "internal/testcampaign/cmd/beads-qualification/runner.go")
	storageKeys, err := phase2QualificationStorageModeContract(runner)
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{
		"BEADS_BIN", "BEADS_INSTALL_RESULT", "BEADS_SERVER_ID", "BEADS_SERVER_ENDPOINT",
		"DOLT_SERVER_HOST", "DOLT_SERVER_PORT", "OFFLINE_ENFORCED",
	} {
		t.Run("qualification variable "+variable, func(t *testing.T) {
			mutant := strings.Replace(readme, variable+"=", variable+"_BOGUS=", 1)
			if err := phase2ValidateQualificationCommand(mutant, makefile, storageKeys); err == nil {
				t.Fatalf("qualification variable typo %s was accepted", variable)
			}
		})
	}
	t.Run("storage mode required tuple", func(t *testing.T) {
		mutant := strings.Replace(runner,
			`[]string{"BEADS_SERVER_ID", "BEADS_SERVER_ENDPOINT", "DOLT_SERVER_HOST", "DOLT_SERVER_PORT"}`,
			`[]string{"BEADS_SERVER_ID", "BEADS_SERVER_ENDPOINT", "DOLT_SERVER_HOST"}`, 1)
		mutantKeys, contractErr := phase2QualificationStorageModeContract(mutant)
		if contractErr == nil {
			contractErr = phase2ValidateStorageModeSemantics(mutantKeys)
		}
		if contractErr == nil || !strings.Contains(contractErr.Error(), "required keys") {
			t.Fatalf("qualificationStorageMode three-key mutant error=%v; want required-key rejection", contractErr)
		}
		if !strings.Contains(mutant, `"DOLT_SERVER_PORT"`) {
			t.Fatal("storage-mode mutant accidentally removed every DOLT_SERVER_PORT occurrence")
		}
	})
	t.Run("qualification endpoint drift", func(t *testing.T) {
		mutant := strings.Replace(readme, "BEADS_SERVER_ENDPOINT=127.0.0.1:3307", "BEADS_SERVER_ENDPOINT=127.0.0.1:3308", 1)
		if err := phase2ValidateQualificationCommand(mutant, makefile, storageKeys); err == nil {
			t.Fatal("server endpoint mismatch was accepted")
		}
	})
	t.Run("qualification target typo", func(t *testing.T) {
		mutant := strings.Replace(readme, "make beads-qualification", "make beads-qualifiction", 1)
		if err := phase2ValidateQualificationCommand(mutant, makefile, storageKeys); err == nil {
			t.Fatal("qualification target typo was accepted")
		}
	})
	t.Run("provider argv typo", func(t *testing.T) {
		providerArgv, err := phase2TypedProviderArgv(root)
		if err != nil {
			t.Fatal(err)
		}
		mutant := strings.Replace(readme, `"$beads_bin" export --json`, `"$beads_bin" export --jsoon`, 1)
		initArgv, initErr := phase2AcceptedDisposableInitArgv(root)
		if initErr != nil {
			t.Fatal(initErr)
		}
		if err := phase2ValidateProviderCommands(mutant, providerArgv, initArgv); err == nil {
			t.Fatal("provider argv typo was accepted")
		}
	})
	t.Run("backup init verb", func(t *testing.T) {
		providerArgv, contractErr := phase2TypedProviderArgv(root)
		if contractErr != nil {
			t.Fatal(contractErr)
		}
		initArgv, initErr := phase2AcceptedDisposableInitArgv(root)
		if initErr != nil {
			t.Fatal(initErr)
		}
		mutant := strings.Replace(readme, `"$beads_bin" backup init "$backup_root"`, `"$beads_bin" backup begin "$backup_root"`, 1)
		if err := phase2ValidateProviderCommands(mutant, providerArgv, initArgv); err == nil || !strings.Contains(err.Error(), "backup initialization") {
			t.Fatalf("backup init verb mutant error=%v; want backup initialization rejection", err)
		}
	})
	t.Run("disposable init safety flags", func(t *testing.T) {
		providerArgv, contractErr := phase2TypedProviderArgv(root)
		if contractErr != nil {
			t.Fatal(contractErr)
		}
		initArgv, initErr := phase2AcceptedDisposableInitArgv(root)
		if initErr != nil {
			t.Fatal(initErr)
		}
		mutant := strings.Replace(readme, "--stealth --skip-agents --skip-hooks --non-interactive", "--stealth --skip-agents --non-interactive", 1)
		if err := phase2ValidateProviderCommands(mutant, providerArgv, initArgv); err == nil || !strings.Contains(err.Error(), "disposable initialization") {
			t.Fatalf("disposable init safety mutant error=%v; want disposable initialization rejection", err)
		}
	})
	t.Run("helper flag typo", func(t *testing.T) {
		mutant := strings.Replace(readme, "--protocol-schema 1", "--protocol-scheme 1", 1)
		if err := phase2ValidateHelperCommand(mutant, phase2ExpectedInspectHelp()); err == nil {
			t.Fatal("helper flag typo was accepted")
		}
	})
	t.Run("helper read-only false", func(t *testing.T) {
		mutant := strings.Replace(readme, "--read-only=true", "--read-only=false", 1)
		if err := phase2ValidateHelperCommand(mutant, phase2ExpectedInspectHelp()); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("writable inspection mutant error=%v; want read-only rejection", err)
		}
	})
	t.Run("helper unknown mode", func(t *testing.T) {
		mutant := strings.Replace(readme, "--mode embedded_serialized", "--mode nonsense", 1)
		if err := phase2ValidateHelperCommand(mutant, phase2ExpectedInspectHelp()); err == nil || !strings.Contains(err.Error(), "mode") {
			t.Fatalf("unknown helper mode mutant error=%v; want mode rejection", err)
		}
	})
	t.Run("installer flag typo", func(t *testing.T) {
		mutant := strings.Replace(readme, "--dest \"$tool_root\"", "--destination \"$tool_root\"", 1)
		if err := phase2ValidateInstallerCommand(mutant); err == nil {
			t.Fatal("installer flag typo was accepted")
		}
	})
}

func phase2RejectLivingClaims(document string) error {
	lower := strings.ToLower(phase2Flat(document))
	for _, stale := range []string{
		"beads is only a candidate",
		"beads is the leading candidate",
		"no tracker adapter exists",
		"production uses a published beads go consumer api",
		"use the published go consumer api",
		"any supported beads version",
		"read-before-write is compare-and-swap",
		"jsonl export is a complete restorable backup",
		"beads assignment authorizes execution",
		"beads readiness authorizes execution",
		"tracker status advances the mission",
		"tracker closure advances the mission",
		"mechanism adrs remain provisional through the phase 1 proving experiment",
		"sqlite in wal mode is the leading implementation choice",
		"foundational adr ratification and selected golden python fixtures: pending",
		"define the two-agent concurrency proving experiment in phase 1",
		"decide the control-store driver after a focused sqlite wal spike",
		"after phase 0 behavioral characterization, the project should ratify",
		"pin a version, use published apis only",
	} {
		if strings.Contains(lower, stale) {
			return fmt.Errorf("stale or unsafe claim %q", stale)
		}
	}
	return nil
}

func phase2EqualCensus(expected, started, terminal, passed, failed int) error {
	if expected <= 0 || expected != started || started != terminal || terminal != passed+failed || failed != 0 || passed != expected {
		return fmt.Errorf("non-passing exact census expected=%d started=%d terminal=%d passed=%d failed=%d", expected, started, terminal, passed, failed)
	}
	return nil
}

func phase2ValidateSourceIdentity(commit, tree string) error {
	if !phase2GitObjectID(commit) || !phase2GitObjectID(tree) {
		return errors.New("qualification evidence lacks full source commit/tree identities")
	}
	return nil
}

func phase2ValidateQualificationCommand(document, makefile string, storageKeys []string) error {
	block, err := phase2FencedBlockContaining(document, "make beads-qualification")
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`(?m)(^|\\\n)\s*make beads-qualification(?:\s|\\|$)`).MatchString(block) {
		return errors.New("README lacks the exact make beads-qualification invocation")
	}
	if !regexp.MustCompile(`(?m)^beads-qualification:`).MatchString(makefile) {
		return errors.New("Makefile lacks the beads-qualification target")
	}
	assignments := map[string]string{}
	for _, match := range regexp.MustCompile(`\b([A-Z][A-Z0-9_]*)=([^\s\\]+)`).FindAllStringSubmatch(block, -1) {
		assignments[match[1]] = strings.Trim(match[2], `"'`)
	}
	required := map[string]string{
		"BEADS_BIN":             "/absolute/path/to/verified/bd",
		"BEADS_INSTALL_RESULT":  "/absolute/path/to/installer-identity.json",
		"BEADS_SERVER_ID":       "dolt-v0.40.5-1bf533220ab0",
		"BEADS_SERVER_ENDPOINT": "127.0.0.1:3307",
		"DOLT_SERVER_HOST":      "127.0.0.1",
		"DOLT_SERVER_PORT":      "3307",
		"OFFLINE_ENFORCED":      "linux-network-namespace",
	}
	for name, value := range required {
		if assignments[name] != value {
			return fmt.Errorf("qualification command %s=%q, want %q", name, assignments[name], value)
		}
	}
	if assignments["BEADS_SERVER_ENDPOINT"] != assignments["DOLT_SERVER_HOST"]+":"+assignments["DOLT_SERVER_PORT"] {
		return errors.New("qualification endpoint must equal the documented Dolt host/port binding")
	}
	if err := phase2ValidateStorageModeSemantics(storageKeys); err != nil {
		return err
	}
	return nil
}

func phase2QualificationStorageModeContract(source string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "runner.go", source, 0)
	if err != nil {
		return nil, fmt.Errorf("parse qualification runner: %w", err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "qualificationStorageMode" || function.Body == nil {
			continue
		}
		if len(function.Body.List) != 2 {
			return nil, errors.New("qualificationStorageMode must be one required-key range followed by the server result")
		}
		rangeStatement, ok := function.Body.List[0].(*ast.RangeStmt)
		if !ok {
			return nil, errors.New("qualificationStorageMode no longer ranges over its required server tuple")
		}
		literal, ok := rangeStatement.X.(*ast.CompositeLit)
		if !ok {
			return nil, errors.New("qualificationStorageMode required tuple is not a static literal")
		}
		keys, err := phase2StringLiteralElements(literal)
		if err != nil {
			return nil, fmt.Errorf("qualificationStorageMode required tuple: %w", err)
		}
		if len(rangeStatement.Body.List) != 1 {
			return nil, errors.New("qualificationStorageMode range must contain exactly the missing-key decision")
		}
		missing, ok := rangeStatement.Body.List[0].(*ast.IfStmt)
		if !ok || !phase2IsMissingEnvironmentKey(missing.Cond, rangeStatement.Value) || !phase2ReturnsString(missing.Body.List, "embedded_serialized") {
			return nil, errors.New("qualificationStorageMode no longer maps a missing required key to embedded_serialized")
		}
		if !phase2ReturnsString(function.Body.List[1:], "server_multiwriter") {
			return nil, errors.New("qualificationStorageMode no longer maps the complete tuple to server_multiwriter")
		}
		return keys, nil
	}
	return nil, errors.New("qualificationStorageMode is absent")
}

func phase2ValidateStorageModeSemantics(keys []string) error {
	want := []string{"BEADS_SERVER_ID", "BEADS_SERVER_ENDPOINT", "DOLT_SERVER_HOST", "DOLT_SERVER_PORT"}
	if strings.Join(keys, "\x00") != strings.Join(want, "\x00") {
		return fmt.Errorf("qualificationStorageMode required keys=%q want=%q", keys, want)
	}
	complete := map[string]string{}
	for _, key := range want {
		complete[key] = "present"
	}
	if phase2ClassifyStorageMode(keys, complete) != "server_multiwriter" {
		return errors.New("complete four-key server tuple did not select server_multiwriter")
	}
	for _, missing := range want {
		fixture := make(map[string]string, len(complete)-1)
		for key, value := range complete {
			if key != missing {
				fixture[key] = value
			}
		}
		if phase2ClassifyStorageMode(keys, fixture) != "embedded_serialized" {
			return fmt.Errorf("missing %s did not select embedded_serialized", missing)
		}
	}
	return nil
}

func phase2ClassifyStorageMode(keys []string, environment map[string]string) string {
	for _, key := range keys {
		if environment[key] == "" {
			return "embedded_serialized"
		}
	}
	return "server_multiwriter"
}

func phase2IsMissingEnvironmentKey(expression ast.Expr, rangeValue ast.Expr) bool {
	condition, ok := expression.(*ast.BinaryExpr)
	if !ok || condition.Op != token.EQL {
		return false
	}
	empty, ok := condition.Y.(*ast.BasicLit)
	if !ok || empty.Kind != token.STRING || empty.Value != `""` {
		return false
	}
	call, ok := condition.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Getenv" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "os" {
		return false
	}
	argument, argumentOK := call.Args[0].(*ast.Ident)
	value, valueOK := rangeValue.(*ast.Ident)
	return argumentOK && valueOK && argument.Name == value.Name
}

func phase2ReturnsString(statements []ast.Stmt, value string) bool {
	for _, statement := range statements {
		result, ok := statement.(*ast.ReturnStmt)
		if !ok || len(result.Results) != 1 {
			continue
		}
		literal, ok := result.Results[0].(*ast.BasicLit)
		if ok && literal.Kind == token.STRING && literal.Value == strconv.Quote(value) {
			return true
		}
	}
	return false
}

func phase2ValidateHelperCommand(document, help string) error {
	block, err := phase2FencedBlockContaining(document, "tracker inspect-beads")
	if err != nil {
		return err
	}
	options := phase2DocumentedOptions(block)
	for _, flag := range []string{
		"--project", "--executable", "--workspace", "--database-identity", "--protocol-schema",
		"--mode", "--read-only", "--expected-digest",
	} {
		if !strings.Contains(block, flag) {
			return fmt.Errorf("documented inspect-beads command lacks %s", flag)
		}
		if !strings.Contains(help, flag) && !strings.Contains(help, strings.TrimPrefix(flag, "-")) {
			return fmt.Errorf("inspect-beads help lacks documented flag %s", flag)
		}
	}
	if options["--project"] != "example-project" {
		return fmt.Errorf("documented inspect-beads project=%q", options["--project"])
	}
	if options["--executable"] != "$beads_bin" {
		return fmt.Errorf("documented inspect-beads executable=%q; want the verified absolute-path variable", options["--executable"])
	}
	for _, name := range []string{"--workspace", "--database-identity"} {
		if !filepath.IsAbs(options[name]) {
			return fmt.Errorf("documented inspect-beads %s=%q is not absolute", name, options[name])
		}
	}
	if options["--protocol-schema"] != "1" {
		return fmt.Errorf("documented inspect-beads protocol schema=%q", options["--protocol-schema"])
	}
	if options["--mode"] != "embedded_serialized" {
		return fmt.Errorf("documented inspect-beads mode=%q", options["--mode"])
	}
	if options["--read-only"] != "true" {
		return fmt.Errorf("documented inspect-beads read-only=%q; inspection must be read-only", options["--read-only"])
	}
	if options["--expected-digest"] != qualifiedBeadsDigest || !phase2FullSHA(options["--expected-digest"]) {
		return fmt.Errorf("documented inspect-beads digest is not the qualified lowercase SHA-256")
	}
	return nil
}

func phase2DocumentedOptions(block string) map[string]string {
	normalized := strings.ReplaceAll(block, "\\\n", " ")
	result := map[string]string{}
	for _, match := range regexp.MustCompile(`(--[a-z][a-z-]*)(?:=|\s+)([^\s\\]+)`).FindAllStringSubmatch(normalized, -1) {
		result[match[1]] = strings.Trim(match[2], `"'`)
	}
	return result
}

func phase2ValidateInstallerCommand(document string) error {
	block, err := phase2FencedBlockContaining(document, "scripts/install-qualified-beads.sh")
	if err != nil {
		return err
	}
	if !strings.Contains(block, `scripts/install-qualified-beads.sh --dest "$tool_root"`) {
		return errors.New("installer example lacks the accepted --dest argument")
	}
	return nil
}

func phase2ValidateProviderCommands(document string, argv map[string][]string, disposableInit []string) error {
	for function, prefix := range map[string]string{
		"newVersionCommand":      `"$beads_bin"`,
		"newWhereCommand":        `"$beads_bin"`,
		"newDoltShowCommand":     `"$beads_bin"`,
		"newDoltStatusCommand":   `"$beads_bin"`,
		"newExportCommand":       `"$beads_bin"`,
		"newBackupSyncCommand":   `"$beads_bin"`,
		"newBackupStatusCommand": `"$beads_bin"`,
	} {
		arguments, ok := argv[function]
		if !ok {
			return fmt.Errorf("typed provider command %s is absent", function)
		}
		command := prefix + " " + strings.Join(arguments, " ")
		if !strings.Contains(document, command) {
			return fmt.Errorf("README lacks typed provider command %q from %s", command, function)
		}
	}
	backupInit, ok := argv["newBackupInitCommand"]
	if !ok || strings.Join(backupInit, " ") != "backup init <dynamic>" {
		return fmt.Errorf("typed backup-init contract=%q", backupInit)
	}
	if !strings.Contains(document, `"$beads_bin" backup init "$backup_root"`) {
		return errors.New("README backup initialization does not match newBackupInitCommand")
	}
	initBlock, err := phase2FencedBlockContaining(document, `"$beads_bin" init`)
	if err != nil {
		return err
	}
	normalizedInit := strings.Join(strings.Fields(strings.ReplaceAll(initBlock, "\\\n", " ")), " ")
	wantInit := `BD_NON_INTERACTIVE=1 "$beads_bin" ` + strings.Join(disposableInit, " ")
	if !strings.Contains(normalizedInit, wantInit) {
		return fmt.Errorf("disposable initialization must match accepted safe argv %q", disposableInit)
	}
	return nil
}

func phase2TypedProviderArgv(root string) (map[string][]string, error) {
	path := filepath.Join(root, "internal", "tracker", "beads", "command.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse typed provider commands: %w", err)
	}
	wanted := map[string]bool{
		"newVersionCommand": true, "newWhereCommand": true, "newDoltShowCommand": true,
		"newDoltStatusCommand": true, "newExportCommand": true,
		"newBackupInitCommand": true, "newBackupSyncCommand": true, "newBackupStatusCommand": true,
	}
	result := map[string][]string{}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !wanted[function.Name.Name] {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 3 {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "buildCommand" {
				return true
			}
			literal, ok := call.Args[2].(*ast.CompositeLit)
			if !ok {
				return true
			}
			var arguments []string
			for _, element := range literal.Elts {
				value, ok := element.(*ast.BasicLit)
				if !ok || value.Kind != token.STRING {
					arguments = append(arguments, "<dynamic>")
					continue
				}
				decoded, decodeErr := strconv.Unquote(value.Value)
				if decodeErr != nil {
					arguments = nil
					return false
				}
				arguments = append(arguments, decoded)
			}
			result[function.Name.Name] = arguments
			return false
		})
	}
	for function := range wanted {
		if len(result[function]) == 0 {
			return nil, fmt.Errorf("typed provider builder %s has no static argv contract", function)
		}
	}
	return result, nil
}

func phase2AcceptedDisposableInitArgv(root string) ([]string, error) {
	path := filepath.Join(root, "internal", "testcampaign", "cmd", "beads-qualification", "fixture.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse accepted disposable setup contract: %w", err)
	}
	want := []string{"init", "--stealth", "--skip-agents", "--skip-hooks", "--non-interactive", "--prefix", "skt"}
	var matches int
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		values, literalErr := phase2StringLiteralElements(literal)
		if literalErr == nil && strings.Join(values, "\x00") == strings.Join(want, "\x00") {
			matches++
		}
		return true
	})
	if matches != 1 {
		return nil, fmt.Errorf("accepted disposable setup contract matches=%d want=1", matches)
	}
	return want, nil
}

func phase2StringLiteralElements(literal *ast.CompositeLit) ([]string, error) {
	values := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		value, ok := element.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return nil, errors.New("contains a non-string-literal element")
		}
		decoded, err := strconv.Unquote(value.Value)
		if err != nil {
			return nil, err
		}
		values = append(values, decoded)
	}
	return values, nil
}

func phase2FencedBlockContaining(document, needle string) (string, error) {
	parts := strings.Split(document, "```")
	for index := 1; index < len(parts); index += 2 {
		block := parts[index]
		if newline := strings.IndexByte(block, '\n'); newline >= 0 {
			block = block[newline+1:]
		}
		if strings.Contains(block, needle) {
			return block, nil
		}
	}
	return "", fmt.Errorf("documentation lacks fenced command containing %q", needle)
}

func phase2SmokeMakeTargets(t *testing.T, root string, targets []string) {
	t.Helper()
	for _, target := range targets {
		t.Run("make -n "+target, func(t *testing.T) {
			phase2RunCommand(t, root, 0, "make", "-n", target,
				"ARTIFACT_DIR=/tmp/spec-kitty-doc-smoke-artifacts",
				"BUILD_DIR=/tmp/spec-kitty-doc-smoke-build",
				"PROVIDER_TMP_DIR=/tmp/spec-kitty-doc-smoke-provider",
				"BEADS_BIN=/absolute/path/to/verified/bd",
				"BEADS_INSTALL_RESULT=/absolute/path/to/installer-identity.json",
				"OFFLINE_ENFORCED=linux-network-namespace")
		})
	}
}

func phase2SmokeHelperAndInstaller(t *testing.T, root string) (string, string) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "spk-phase1-helper")
	phase2RunCommand(t, root, 0, "go", "build", "-trimpath", "-o", binary, "./cmd/spk-phase1-helper")
	help := phase2RunCommand(t, root, 2, binary, "tracker", "inspect-beads", "--help")
	if !strings.Contains(help, "Usage of tracker inspect-beads") {
		t.Fatalf("inspect-beads help seam returned unexpected output: %q", help)
	}
	installer := filepath.Join(root, "scripts", "install-qualified-beads.sh")
	relative := phase2RunCommand(t, root, 2, "sh", installer, "--dest", "relative")
	if !strings.Contains(relative, "destination must be absolute") {
		t.Fatalf("installer --dest parser seam returned unexpected output: %q", relative)
	}
	offlineDestination := filepath.Join(t.TempDir(), "verified-provider")
	offline := phase2RunCommand(t, root, 1, "sh", installer, "--dest", offlineDestination, "--offline")
	if !strings.Contains(offline, "offline installation requires an already verified target") {
		t.Fatalf("installer --offline parser seam returned unexpected output: %q", offline)
	}
	return binary, help
}

func phase2SmokeDocumentedHelper(t *testing.T, root, binary, document string) {
	t.Helper()
	block, err := phase2FencedBlockContaining(document, "tracker inspect-beads")
	if err != nil {
		t.Fatal(err)
	}
	options := phase2DocumentedOptions(block)
	fixture := t.TempDir()
	marker := filepath.Join(fixture, "provider-started")
	executable := filepath.Join(fixture, "bd")
	script := "#!/bin/sh\nprintf started > " + strconv.Quote(marker) + "\nexit 97\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	workspace := filepath.Join(fixture, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	missingDatabase := filepath.Join(workspace, "missing-database")
	output := phase2RunCommand(t, root, 1, binary,
		"tracker", "inspect-beads",
		"--project", options["--project"],
		"--executable", executable,
		"--workspace", workspace,
		"--database-identity", missingDatabase,
		"--protocol-schema", options["--protocol-schema"],
		"--mode", options["--mode"],
		"--read-only="+options["--read-only"],
		"--expected-digest", hex.EncodeToString(digest[:]))
	if !strings.Contains(output, `"ok":false`) || !strings.Contains(output, `"kind":"invalid_request"`) || !strings.Contains(output, "provider database path cannot be resolved") {
		t.Fatalf("documented inspect-beads invocation did not reach the expected pre-provider validation boundary: %q", output)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("documented helper smoke reached provider state: %v", err)
	}
}

func phase2ExpectedInspectHelp() string {
	return strings.Join([]string{
		"--project", "--executable", "--workspace", "--database-identity", "--protocol-schema",
		"--mode", "--read-only", "--expected-digest",
	}, " ")
}

func phase2RunCommand(t *testing.T, directory string, expectedExit int, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	output, err := command.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("run %s: %v", name, err)
		}
		exitCode = exitError.ExitCode()
	}
	if ctx.Err() != nil {
		t.Fatalf("run %s timed out: %v", name, ctx.Err())
	}
	if exitCode != expectedExit {
		t.Fatalf("run %s %q exit=%d want=%d output=%q", name, args, exitCode, expectedExit, output)
	}
	return string(output)
}

func phase2VerifyChecksums(t *testing.T, evidenceDir string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(evidenceDir, "SHA256SUMS"))
	if err != nil {
		t.Fatalf("read SHA256SUMS: %v", err)
	}
	seen := map[string]bool{}
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !phase2FullSHA(fields[0]) {
			t.Fatalf("SHA256SUMS line %d is malformed", lineNumber+1)
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "SHA256SUMS" || filepath.Base(name) != name || seen[name] {
			t.Fatalf("SHA256SUMS line %d has unsafe or duplicate name %q", lineNumber+1, name)
		}
		seen[name] = true
		payload, err := os.ReadFile(filepath.Join(evidenceDir, name))
		if err != nil {
			t.Fatalf("checksum payload %s: %v", name, err)
		}
		digest := sha256.Sum256(payload)
		if hex.EncodeToString(digest[:]) != fields[0] {
			t.Fatalf("checksum mismatch for %s", name)
		}
	}
	for _, name := range []string{"qualification-evidence.json", "campaign-census.json", "results.md", "limitations.md"} {
		if !seen[name] {
			t.Errorf("SHA256SUMS omits immutable payload %s", name)
		}
	}
}

func phase2CheckRelativeLinks(t *testing.T, root, sourcePath, document string) {
	t.Helper()
	re := regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	for _, match := range re.FindAllStringSubmatch(document, -1) {
		target := strings.TrimSpace(match[1])
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
			phase2CheckExternalURL(t, sourcePath, target)
			continue
		}
		target = strings.Trim(target, "<>")
		pathPart, fragment, _ := strings.Cut(target, "#")
		if pathPart == "" {
			if fragment != "" && !phase2HasAnchor(document, fragment) {
				t.Errorf("%s has missing local anchor #%s", sourcePath, fragment)
			}
			continue
		}
		pathPart, err := strconv.Unquote(`"` + strings.ReplaceAll(pathPart, `"`, `\"`) + `"`)
		if err != nil {
			t.Errorf("%s invalid link %q", sourcePath, target)
			continue
		}
		if pathPart == "" {
			continue
		}
		resolved := filepath.Clean(filepath.Join(root, filepath.Dir(sourcePath), filepath.FromSlash(pathPart)))
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("%s link escapes repository: %q", sourcePath, target)
			continue
		}
		if _, err := os.Stat(resolved); err != nil {
			t.Errorf("%s broken relative link %q: %v", sourcePath, target, err)
			continue
		}
		if fragment != "" && strings.HasSuffix(strings.ToLower(resolved), ".md") {
			payload, err := os.ReadFile(resolved)
			if err != nil || !phase2HasAnchor(string(payload), fragment) {
				t.Errorf("%s has missing target anchor %q", sourcePath, target)
			}
		}
	}
	for _, raw := range regexp.MustCompile(`<https?://[^>]+>`).FindAllString(document, -1) {
		phase2CheckExternalURL(t, sourcePath, strings.Trim(raw, "<>"))
	}
}

func phase2CheckExternalURL(t *testing.T, sourcePath, target string) {
	t.Helper()
	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		t.Errorf("%s has malformed external URL %q", sourcePath, target)
	}
}

func phase2HasAnchor(document, wanted string) bool {
	wanted, err := url.PathUnescape(strings.ToLower(wanted))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(document, "\n") {
		heading := strings.TrimSpace(line)
		if !strings.HasPrefix(heading, "#") {
			continue
		}
		heading = strings.TrimSpace(strings.TrimLeft(heading, "#"))
		heading = strings.ToLower(heading)
		heading = regexp.MustCompile(`[^a-z0-9 -]`).ReplaceAllString(heading, "")
		heading = strings.Trim(strings.ReplaceAll(strings.Join(strings.Fields(heading), "-"), "--", "-"), "-")
		if heading == wanted {
			return true
		}
	}
	return false
}

func phase2CheckMermaid(t *testing.T, sourcePath, document string) {
	t.Helper()
	blocks := regexp.MustCompile("(?s)```mermaid\\s*\\n(.*?)```").FindAllStringSubmatch(document, -1)
	for index, block := range blocks {
		body := strings.TrimSpace(block[1])
		if body == "" {
			t.Errorf("%s Mermaid block %d is empty", sourcePath, index+1)
			continue
		}
		first, _, _ := strings.Cut(body, "\n")
		if !strings.HasPrefix(first, "flowchart ") && first != "sequenceDiagram" && first != "stateDiagram-v2" {
			t.Errorf("%s Mermaid block %d uses unsupported declaration %q", sourcePath, index+1, first)
		}
		if first == "sequenceDiagram" {
			for _, line := range strings.Split(body, "\n") {
				if strings.Contains(line, "->>") && strings.Contains(line, ";") {
					t.Errorf("%s Mermaid sequence block %d contains a semicolon in an arrow label", sourcePath, index+1)
				}
			}
		}
	}
}

func phase2Decode(t *testing.T, path string, target any) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func phase2RepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Phase 2 documentation test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func phase2Read(t *testing.T, root, path string) string {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(payload)
}

func phase2Flat(value string) string { return strings.Join(strings.Fields(value), " ") }

func phase2FullSHA(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func phase2GitObjectID(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
