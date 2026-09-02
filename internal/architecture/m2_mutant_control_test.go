package architecture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	m2MutantCensusCount  = 50
	m2MutantCensusSHA256 = "513225fad1c00d23bdba621819ac7d679d6c8765201cd2b44b3d2ac91d671786"
)

type m2MutantCase struct {
	ID, Rule string
	Run      func(*testing.T) string
}

type m2ImportObservation struct{ Source, Target string }

func TestM2MutantsAreKilledByOneExecutableCensus(t *testing.T) {
	cases := m2ExecutableMutants()
	if err := validateM2ExecutableMutants(cases); err != nil {
		t.Fatal(err)
	}
	for _, mutant := range cases {
		mutant := mutant
		t.Run(mutant.ID, func(t *testing.T) {
			if got := mutant.Run(t); got != mutant.Rule {
				t.Fatalf("mutant=%s observed-rule=%q expected-rule=%q", mutant.ID, got, mutant.Rule)
			}
		})
	}
}

func TestM2MutantCensusDeletionControlsExecutedTable(t *testing.T) {
	cases := m2ExecutableMutants()
	if err := validateM2ExecutableMutants(cases); err != nil {
		t.Fatal(err)
	}
	mutations := [][]m2MutantCase{
		cases[1:],
		append(slices.Clone(cases), cases[0]),
		func() []m2MutantCase { v := slices.Clone(cases); v[0].Run = nil; return v }(),
		func() []m2MutantCase { v := slices.Clone(cases); v[0].Rule = ""; return v }(),
	}
	for index, mutant := range mutations {
		if err := validateM2ExecutableMutants(mutant); err == nil {
			t.Fatalf("evidence=M2-MUTANT-CENSUS deletion-control=%d observed=accepted", index)
		}
	}
}

func TestM2SemanticPositiveControls(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module example.test/positive\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/mission/mission.go", "package mission\n// modernc.org/sqlite is prose.\nconst Note = `github.com/spec-kitty/gapdb is not a call`\n")
	got, err := scanM2AllGoImports(root, "example.test/positive")
	if err != nil || len(got) != 0 {
		t.Fatalf("evidence=SEMANTIC-DECOYS observed=%v error=%v", got, err)
	}
}

func m2ExecutableMutants() []m2MutantCase {
	cases := make([]m2MutantCase, 0, m2MutantCensusCount)
	imports := map[string]string{
		"DIRECT": `import "modernc.org/sqlite"`, "ALIAS": `import driver "modernc.org/sqlite/lib"`,
		"BLANK": `import _ "github.com/mattn/go-sqlite3"`, "DOT": `import . "github.com/ncruces/go-sqlite3"`,
		"GENERATED": "// Code generated. DO NOT EDIT.\nimport _ \"zombiezen.com/go/sqlite\"",
		"BUILD-TAG": "//go:build never_enabled\n\nimport _ \"github.com/glebarez/sqlite\"",
	}
	for _, name := range sortedM2StringKeys(imports) {
		source := "package mission\n" + imports[name] + "\n"
		cases = append(cases, m2ImportMutant("M-IMPORT-"+name, source))
	}
	reach := map[string]string{
		"RENAME":          "type Harmless = provider.Provider\nfunc New() any { return provider.Open() }",
		"EMBED":           "type Holder struct{ provider.Provider }\nfunc New() any { return Holder{} }",
		"INTERFACE":       "type Port interface{ Identity() string }\nfunc New() Port { return provider.Open() }",
		"CONSTRUCTOR-VAR": "var New = provider.Open", "METHOD-VALUE": "var New = provider.Provider.Identity",
		"GENERIC": "func hold[T any](v T) T { return v }\nfunc New() any { return hold(provider.Open()) }",
	}
	for _, name := range sortedM2StringKeys(reach) {
		expression := reach[name]
		cases = append(cases, m2ReachMutant("M-REACH-"+name, expression))
	}
	policyMutants := []struct {
		id string
		fn func(*Policy)
	}{
		{"CLASS", func(p *Policy) { removeM2Pattern(p, m2ModulePath+"/internal/controljournal/v2") }},
		{"DIRECTION", func(p *Policy) {
			for i := range p.Tiers {
				if p.Tiers[i].Name == TierAuthority {
					p.Tiers[i].AllowedLocalTargets = append(p.Tiers[i].AllowedLocalTargets, TierAdapter)
				}
			}
		}},
		{"PROVIDER", func(p *Policy) {
			p.ForbiddenImportPrefixes = removeM2String(p.ForbiddenImportPrefixes, "modernc.org/sqlite")
		}},
		{"GAPDB-CLASS", func(p *Policy) { removeM2Pattern(p, m2ModulePath+"/internal/controlstore/gapdb") }},
	}
	for _, spec := range policyMutants {
		cases = append(cases, m2PolicyShapeMutant("M-GUARD-DELETE-"+spec.id, spec.fn))
	}
	keyMutants := []struct {
		id string
		fn func([]m2KeyEntry) []m2KeyEntry
	}{
		{"MISSION-OPERATION", func(v []m2KeyEntry) []m2KeyEntry {
			v[3].Template = "spk/v2/missions/<mission-id>/operations/<operation-id>/binding"
			return v
		}},
		{"VARIABLE-VERSION", func(v []m2KeyEntry) []m2KeyEntry {
			v[6].Template = strings.Replace(v[6].Template, "hex16", "decimal", 1)
			return v
		}},
		{"AMBIGUOUS", func(v []m2KeyEntry) []m2KeyEntry {
			v[5].Template = strings.Replace(v[5].Template, "<scope>", "<raw-scope>", 1)
			return v
		}},
		{"RAW", func(v []m2KeyEntry) []m2KeyEntry { return append(v, m2KeyEntry{ID: "K-RAW", Template: "<caller-key>"}) }},
		{"MIXED-V1", func(v []m2KeyEntry) []m2KeyEntry { v[0].Template = "spk/v1/authority/binding"; return v }},
		{"GLOBAL-HEAD", func(v []m2KeyEntry) []m2KeyEntry {
			return append(v, m2KeyEntry{ID: "K-GLOBAL", Template: "spk/v2/journal/head"})
		}},
	}
	for _, spec := range keyMutants {
		cases = append(cases, m2KeyMutant("M-KEY-"+spec.id, spec.fn))
	}
	eligibility := map[string]map[string]string{
		"VALUE":    {"internal/workgraph/e.go": m2EligibilitySource("policy", 128, false)},
		"RENAMED":  {"internal/workgraph/e.go": m2EligibilitySource("arbitrary", 128, false)},
		"COMPOSED": {"internal/workgraph/e.go": m2EligibilitySource("policy", 128, true)},
		"ADAPTER":  {"internal/controlstore/gapdb/e.go": strings.Replace(m2EligibilitySource("policy", 128, true), "package workgraph", "package gapdb", 1)},
		"WRAPPER":  {"internal/workgraph/e.go": "package workgraph\nimport k \"example.test/eligibility/internal/keybuilder\"\nvar _ = k.EligibilityKey\n", "internal/keybuilder/k.go": strings.Replace(m2EligibilitySource("renamed", 128, true), "package workgraph", "package keybuilder", 1)},
	}
	for _, name := range sortedM2EligibilityKeys(eligibility) {
		cases = append(cases, m2EligibilityCase("M-ELIGIBILITY-"+name, eligibility[name]))
	}
	for _, target := range []string{"os", "time", "math/rand", "crypto/rand", "net", "os/exec", "path/filepath"} {
		cases = append(cases, m2EdgeMutant("M-PURE-"+strings.NewReplacer("/", "-").Replace(strings.ToUpper(target)), "PURE", m2ModulePath+"/internal/controljournal/v2", target, ImportStandardLibrary))
	}
	cases = append(cases, m2EdgeMutant("M-GAPDB-SCENARIO-CALLER", "GAPDB", m2ModulePath+"/internal/scenario", "github.com/spec-kitty/gapdb/gapdb", ImportExternal))
	for _, target := range []string{"artifact", "mission", "execution", "gitrepo", "workspace", "handoff", "review", "acceptance", "integration"} {
		cases = append(cases, m2EdgeMutant("M-TRACKER-"+strings.ToUpper(target), "TRACKER", m2ModulePath+"/internal/tracker/beads", m2ModulePath+"/internal/"+target, ImportModuleLocal))
	}
	events := map[string]func(*m2EventRegistry){
		"WRONG-OWNER": func(v *m2EventRegistry) { v.Families[0].Owner = "internal/mission" },
		"RENAMED":     func(v *m2EventRegistry) { v.Families[0].Family = "runtime" },
		"MISSING":     func(v *m2EventRegistry) { v.Families = v.Families[1:] },
		"UNEXPECTED":  func(v *m2EventRegistry) { v.Families[len(v.Families)-1].Family = "unexpected" },
		"VERSION":     func(v *m2EventRegistry) { v.Families[0].EventSchema = 2 },
	}
	for _, name := range sortedM2EventKeys(events) {
		cases = append(cases, m2EventCase("M-EVENT-"+name, events[name]))
	}
	cases = append(cases, m2LegacyWorkgraphMutant())
	return cases
}

func m2ImportMutant(id, source string) m2MutantCase {
	return m2MutantCase{ID: id, Rule: "SQL", Run: func(t *testing.T) string {
		root := t.TempDir()
		writeGoFixture(t, root, "go.mod", "module example.test/import\n\ngo 1.26\n")
		writeGoFixture(t, root, "internal/mission/m.go", source)
		imports, err := scanM2AllGoImports(root, "example.test/import")
		if err != nil {
			return "PARSE"
		}
		for _, edge := range imports {
			if matchesPrefix(edge.Target, m2SQLProviderPrefixes) {
				return "SQL"
			}
		}
		return ""
	}}
}

func m2ReachMutant(id, body string) m2MutantCase {
	return m2MutantCase{ID: id, Rule: "REACH", Run: func(t *testing.T) string {
		const module = "example.test/reach"
		root := t.TempDir()
		writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
		writeGoFixture(t, root, "internal/controlstore/gapdb/p.go", "package gapdb\ntype Provider struct{}\nfunc Open() Provider{return Provider{}}\nfunc(Provider) Identity()string{return \"x\"}\n")
		writeGoFixture(t, root, "internal/wrapper/w.go", "package wrapper\nimport provider \""+module+"/internal/controlstore/gapdb\"\n"+body+"\n")
		writeGoFixture(t, root, "internal/mission/m.go", "package mission\nimport w \""+module+"/internal/wrapper\"\nvar _=w.New\n")
		setIsolatedGoPaths(t)
		t.Setenv("GOPROXY", "off")
		graph, err := LoadGraph(t.Context(), root)
		if err != nil {
			return "LOAD"
		}
		if len(m2ForbiddenPaths(graph, module+"/internal/mission", []string{module + "/internal/controlstore/gapdb"})) > 0 {
			return "REACH"
		}
		return ""
	}}
}

func m2PolicyShapeMutant(id string, mutate func(*Policy)) m2MutantCase {
	return m2MutantCase{ID: id, Rule: "POLICY", Run: func(t *testing.T) string {
		p := m2ClosedPolicy(t, m2ModulePath)
		mutate(&p)
		if validateM2PolicyShape(p) != nil {
			return "POLICY"
		}
		return ""
	}}
}

func m2KeyMutant(id string, mutate func([]m2KeyEntry) []m2KeyEntry) m2MutantCase {
	return m2MutantCase{ID: id, Rule: "KEY", Run: func(*testing.T) string {
		if validateM2KeyspaceRegistry(mutate(slices.Clone(m2KeyspaceRegistry()))) != nil {
			return "KEY"
		}
		return ""
	}}
}

func m2EligibilityCase(id string, files map[string]string) m2MutantCase {
	return m2MutantCase{ID: id, Rule: "ELIGIBILITY", Run: func(t *testing.T) string {
		const module = "example.test/eligibility"
		root := t.TempDir()
		writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
		for path, source := range files {
			writeGoFixture(t, root, path, source)
		}
		setIsolatedGoPaths(t)
		t.Setenv("GOPROXY", "off")
		contract, err := loadM2EligibilityContract()
		if err != nil {
			return "CONTRACT"
		}
		err = inspectM2EligibilityModule(t.Context(), root, module, contract)
		if err != nil && strings.Contains(err.Error(), "evidence=ELIGIBILITY-POLICY") {
			return "ELIGIBILITY"
		}
		return ""
	}}
}

func m2EdgeMutant(id, rule, source, target string, kind ImportKind) m2MutantCase {
	return m2MutantCase{ID: id, Rule: rule, Run: func(t *testing.T) string {
		p := m2ClosedPolicy(t, m2ModulePath)
		packages := []string{source}
		if kind == ImportModuleLocal {
			packages = append(packages, target)
		}
		g := fixtureGraph(packages, ImportEdge{SourcePackage: source, TargetPackage: target, Kind: kind})
		g.ModulePath = m2ModulePath
		for _, violation := range m2EvaluateClosedGraph(p, g) {
			if violation.SourcePackage == source && violation.TargetPackage == target {
				return rule
			}
		}
		return ""
	}}
}

func m2EventCase(id string, mutate func(*m2EventRegistry)) m2MutantCase {
	return m2MutantCase{ID: id, Rule: "EVENT", Run: func(t *testing.T) string {
		v := m2EventRegistry{SchemaVersion: 2, RegistryID: "spec-kitty.authority-event-families.v2", PublicationRule: "one_transition_event_per_aggregate_per_publication", Ordering: []string{"gapdb_record_revision", "event_index", "event_id"}, Families: m2ExpectedEventFamilies()}
		mutate(&v)
		payload, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := validateM2EventRegistry(payload); err != nil {
			return "EVENT"
		}
		return ""
	}}
}

func m2LegacyWorkgraphMutant() m2MutantCase {
	return m2MutantCase{ID: "M-LEGACY-CANNOT-HIDE-WORKGRAPH", Rule: "SQL", Run: func(t *testing.T) string {
		p := m2ClosedPolicy(t, m2ModulePath)
		g := fixtureGraph([]string{m2ModulePath + "/internal/controlstore/sqlite", m2ModulePath + "/internal/workgraph"}, ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/sqlite", TargetPackage: "modernc.org/sqlite", Kind: ImportExternal}, ImportEdge{SourcePackage: m2ModulePath + "/internal/workgraph", TargetPackage: "modernc.org/sqlite", Kind: ImportExternal})
		g.ModulePath = m2ModulePath
		for _, v := range m2EvaluateIncrementalRepository(p, g) {
			if v.SourcePackage == m2ModulePath+"/internal/workgraph" {
				return "SQL"
			}
		}
		return ""
	}}
}

func validateM2ExecutableMutants(cases []m2MutantCase) error {
	if len(cases) != m2MutantCensusCount {
		return fmt.Errorf("evidence=M2-MUTANT-CENSUS observed-count=%d expected=%d", len(cases), m2MutantCensusCount)
	}
	identities := make([]string, 0, len(cases))
	seen := make(map[string]bool, len(cases))
	for _, c := range cases {
		if c.ID == "" || c.Rule == "" || c.Run == nil || seen[c.ID] {
			return fmt.Errorf("evidence=M2-MUTANT-CENSUS invalid=%s", c.ID)
		}
		seen[c.ID] = true
		identities = append(identities, c.ID+"|"+c.Rule)
	}
	slices.Sort(identities)
	digest := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	got := hex.EncodeToString(digest[:])
	if got != m2MutantCensusSHA256 {
		return fmt.Errorf("evidence=M2-MUTANT-CENSUS observed-sha256=%s expected=%s", got, m2MutantCensusSHA256)
	}
	return nil
}

func scanM2AllGoImports(root, module string) ([]m2ImportObservation, error) {
	result := make([]m2ImportObservation, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		source := module
		if rel != "." {
			source += "/" + filepath.ToSlash(rel)
		}
		for _, declaration := range file.Imports {
			target, err := strconv.Unquote(declaration.Path.Value)
			if err != nil {
				return err
			}
			result = append(result, m2ImportObservation{source, target})
		}
		return nil
	})
	return result, err
}

func m2ForbiddenPaths(graph Graph, source string, targets []string) []string {
	adjacent := make(map[string][]string)
	for _, edge := range graph.Edges {
		if edge.Kind != ImportTestOnly {
			adjacent[edge.SourcePackage] = append(adjacent[edge.SourcePackage], edge.TargetPackage)
		}
	}
	queue := []string{source}
	seen := map[string]bool{source: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacent[current] {
			if seen[next] {
				continue
			}
			seen[next] = true
			if matchesPrefix(next, targets) {
				return []string{source, next}
			}
			queue = append(queue, next)
		}
	}
	return nil
}

func sortedM2StringKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}
func sortedM2EventKeys(values map[string]func(*m2EventRegistry)) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}

func sortedM2EligibilityKeys(values map[string]map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}

func TestM2MalformedSourceFailsClosedAndBounded(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module example.test/malformed\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/mission/broken.go", "package mission\nimport (")
	started := time.Now()
	_, err := scanM2AllGoImports(root, "example.test/malformed")
	if err == nil {
		t.Fatal("malformed source accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("malformed source check exceeded one second")
	}
	if len(err.Error()) > 2048 {
		t.Fatal("malformed source diagnostic exceeded bound")
	}
}
