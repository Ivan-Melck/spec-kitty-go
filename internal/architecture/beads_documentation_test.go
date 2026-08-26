package architecture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
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

	makefile := phase2Read(t, root, "Makefile")
	for _, target := range []string{"build", "beads-contract", "quality", "beads-qualification", "full-campaign"} {
		matched, err := regexp.MatchString("(?m)^"+regexp.QuoteMeta(target)+":", makefile)
		if err != nil || !matched {
			t.Errorf("documented Make target %q is absent", target)
		}
	}
	for _, path := range []string{"cmd/spk-phase1-helper", "scripts/install-qualified-beads.sh"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("documented local command path %s: %v", path, err)
		}
	}
	contract := phase2Read(t, root, "internal/tracker/beads/command.go")
	for _, argv := range []string{
		`[]string{"version", "--json"}`,
		`[]string{"where", "--json"}`,
		`[]string{"export", "--json"}`,
		`[]string{"backup", "sync"}`,
		`[]string{"backup", "status"}`,
	} {
		if !strings.Contains(contract, argv) {
			t.Errorf("documented provider argv is absent from typed contract: %s", argv)
		}
	}
}

func TestPhase2EvidenceIntegrity(t *testing.T) {
	root := phase2RepositoryRoot(t)
	evidenceDir := filepath.Join(root, "docs", "phase2-evidence")
	var qualification phase2Qualification
	phase2Decode(t, filepath.Join(evidenceDir, "qualification-evidence.json"), &qualification)
	if qualification.SchemaVersion != 1 || qualification.ProviderVersion != qualifiedBeadsVersion || qualification.ProviderDigestSHA256 != qualifiedBeadsDigest || qualification.ProtocolSchema != 1 {
		t.Fatalf("qualification tuple is not the exact approved v1.2.2/schema-1/digest tuple: %+v", qualification)
	}
	if !phase2GitObjectID(qualification.SourceCommit) || !phase2GitObjectID(qualification.SourceTree) {
		t.Fatal("qualification evidence lacks full source commit/tree identities")
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
		"public API":           "production uses a published Beads Go consumer API",
		"authority":            "Beads assignment authorizes execution",
		"export backup":        "JSONL export is a complete restorable backup",
		"loose version":        "any supported Beads version is accepted",
		"read write is CAS":    "read-before-write is compare-and-swap",
		"tracker accepts work": "tracker status advances the mission",
	} {
		t.Run(name, func(t *testing.T) {
			if err := phase2RejectLivingClaims(fixture); err == nil {
				t.Fatalf("mutant was accepted: %q", fixture)
			}
		})
	}
	for name, values := range map[string][5]int{
		"missing source":   {11, 0, 0, 0, 0},
		"missing terminal": {11, 11, 10, 10, 0},
		"hidden failure":   {11, 11, 11, 11, 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := phase2EqualCensus(values[0], values[1], values[2], values[3], values[4]); err == nil {
				t.Fatalf("census mutant was accepted: %v", values)
			}
		})
	}
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
