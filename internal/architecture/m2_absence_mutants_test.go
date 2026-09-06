package architecture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestM2AbsenceMutants(t *testing.T) {
	mutants := []struct {
		name, path, content, kind string
	}{
		{"direct-import", "internal/x/x.go", "package x\nimport _ \"modernc.org/sqlite\"\n", "go-import"},
		{"aliased-import", "internal/x/x.go", "package x\nimport driver \"github.com/mattn/go-sqlite3\"\nvar _ = driver.ErrNo\n", "go-import"},
		{"test-only-import", "internal/x/x_test.go", "package x\nimport _ \"zombiezen.com/go/sqlite\"\n", "go-import"},
		{"generated-import", "internal/x/generated.go", "// Code generated. DO NOT EDIT.\npackage x\nimport _ \"github.com/ncruces/go-sqlite3\"\n", "go-import"},
		{"tagged-import", "internal/x/tagged.go", "//go:build never\npackage x\nimport _ \"github.com/glebarez/sqlite\"\n", "go-import"},
		{"platform-import", "internal/x/x_plan9.go", "package x\nimport _ \"modernc.org/sqlite\"\n", "go-import"},
		{"indirect-wrapper", "internal/adapter/wrapper.go", "package adapter\nimport _ \"modernc.org/sqlite/lib\"\n", "go-import"},
		{"reflection", "internal/x/x.go", "package x\nimport \"plugin\"\nfunc load(){ _, _ = plugin.Open(\"authority.so\") }\n", "dynamic-load"},
		{"subprocess", "internal/x/x.go", "package x\nimport \"os/exec\"\nfunc run(){ _ = exec.Command(\"sqlite3\") }\n", "subprocess"},
		{"linkname", "internal/x/x.go", "package x\n//go:linkname Open sqlite.Open\nfunc Open()\n", "linkname"},
		{"cgo", "internal/x/x.go", "package x\n// #cgo LDFLAGS: -lsqlite3\nimport \"C\"\n", "cgo"},
		{"embedded-schema", "assets/authority.sql", "CREATE TABLE authority(id text);", "copied-schema"},
		{"copied-driver", "internal/archive/store.go", "package archive\nconst header = \"SQLite format 3\\x00\"\n", "copied-schema"},
		{"renamed-driver", "internal/cache/provider.go", "package cache\nconst catalog = \"sqlite_master\"\n", "copied-schema"},
		{"direct-module", "go.mod", "module example.test/x\nrequire modernc.org/sqlite v1.57.0\n", "driver"},
		{"tool-module", "go.mod", "module example.test/x\ntool modernc.org/sqlite\n", "driver"},
		{"exclusive-module", "go.mod", "module example.test/x\nrequire modernc.org/libc v1.74.4\n", "module"},
		{"sbom-component", "artifacts/sbom.json", `{"components":[{"name":"modernc.org/sqlite"}]}`, "driver"},
		{"binary-string", "dist/spec-kitty", "ELF\\x00modernc.org/sqlite\\x00", "driver"},
		{"installed-driver", "install/lib/provider.bin", "modernc.org/sqlite", "driver"},
		{"help-selector", "cmd/spec-kitty/help.go", "package main\nconst help = \"--backend sqlite\"\n", "selector"},
		{"fallback", "internal/x/x.go", "package x\nvar SQLiteFallback bool\n", "fallback"},
		{"comparison-read", "internal/x/x.go", "package x\nfunc compareSQLite(){}\n", "comparison-read"},
		{"rescue-read", "internal/x/x.go", "package x\nfunc rescueSQLite(){}\n", "rescue-read"},
		{"dual-write", "internal/x/x.go", "package x\nfunc dualWriteSQLite(){}\n", "dual-write"},
		{"shadow-write", "internal/x/x.go", "package x\nfunc shadowWriteSQLite(){}\n", "shadow-write"},
		{"automatic-import", "internal/x/x.go", "package x\nfunc importSQLite(){}\n", "automatic-import"},
		{"migration", "internal/x/x.go", "package x\nfunc migrateSQLite(){}\n", "migration"},
		{"runtime-open", "internal/x/x.go", "package x\nfunc openSQLite(){}\n", "runtime-open"},
		{"ignored-source", ".cache/generated/provider.go", "package generated\nimport _ \"modernc.org/sqlite\"\n", "go-import"},
	}
	if len(mutants) != 30 {
		t.Fatalf("absence mutant census=%d want=30", len(mutants))
	}
	seen := make(map[string]bool, len(mutants))
	for _, mutant := range mutants {
		if seen[mutant.name] {
			t.Fatalf("duplicate absence mutant %q", mutant.name)
		}
		seen[mutant.name] = true
		t.Run(mutant.name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, mutant.path, []byte(mutant.content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, mutant.kind, mutant.path)
		})
	}
}

func TestM2AbsenceSymlinkAndHistoricalDiscrimination(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "payload/provider.go", []byte("package payload\n"))
		if err := os.Symlink(filepath.Join(root, "payload", "provider.go"), filepath.Join(root, "alias.go")); err != nil {
			t.Fatal(err)
		}
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "symlink", "alias.go")
	})
	t.Run("clean", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "internal/x/x.go", []byte("package x\nfunc Native() string { return \"gapdb\" }\n"))
		if evidence := inspectM2Absence(t, root, false); len(evidence.Findings) != 0 {
			t.Fatalf("clean native fixture rejected:\n%s", formatM2Findings(evidence.Findings))
		}
	})
	t.Run("historical-document", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "kitty-specs/beads-tracker-integration-01M0YG7E/plan.md", []byte("The retired driver was modernc.org/sqlite."))
		if evidence := inspectM2Absence(t, root, false); len(evidence.Findings) != 0 {
			t.Fatalf("historical documentation rejected:\n%s", formatM2Findings(evidence.Findings))
		}
	})
	t.Run("executable-beneath-history", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "kitty-specs/beads-tracker-integration-01M0YG7E/provider.go", []byte("package history\nimport _ \"modernc.org/sqlite\"\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "go-import", "kitty-specs/beads-tracker-integration-01M0YG7E/provider.go")
	})
}

func TestM2RetiredBlobCensusIsClosed(t *testing.T) {
	const expected = "479dab6c7d1240643ff21adc7b259b547e4aa78d2c76edd26c288775acb68281"
	if got := m2RetiredBlobDigest(); got != expected {
		t.Fatalf("retired SQLite blob census digest=%s want=%s", got, expected)
	}
	mutant := make(map[string]string, len(retiredSQLiteBlobs))
	for name, digest := range retiredSQLiteBlobs {
		mutant[name] = digest
	}
	delete(mutant, "db.go")
	if len(mutant) == len(retiredSQLiteBlobs) {
		t.Fatal("retired blob deletion mutant survived")
	}
}

func writeM2Mutant(t *testing.T, root, relative string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertM2Finding(t *testing.T, findings []m2AbsenceFinding, kind, path string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Kind == kind && finding.Path == filepath.ToSlash(path) {
			return
		}
	}
	t.Fatalf("mutant survived kind=%s path=%s findings=%v", kind, path, findings)
}
