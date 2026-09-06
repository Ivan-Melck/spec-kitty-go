package architecture

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestM2AbsenceMutants(t *testing.T) {
	mutants := []struct {
		name, path, content, kind string
	}{
		{"direct-import", "internal/x/x.go", "package x\nimport _ \"" + m2ModerncDriver + "\"\n", "go-import"},
		{"aliased-import", "internal/x/x.go", "package x\nimport driver \"" + m2MattnDriver + "\"\nvar _ = driver.ErrNo\n", "go-import"},
		{"test-only-import", "internal/x/x_test.go", "package x\nimport _ \"" + m2ZombiezenDriver + "\"\n", "go-import"},
		{"generated-import", "internal/x/generated.go", "// Code generated. DO NOT EDIT.\npackage x\nimport _ \"" + m2NcrucesDriver + "\"\n", "go-import"},
		{"tagged-import", "internal/x/tagged.go", "//go:build never\npackage x\nimport _ \"" + m2GlebarezDriver + "\"\n", "go-import"},
		{"platform-import", "internal/x/x_plan9.go", "package x\nimport _ \"" + m2ModerncDriver + "\"\n", "go-import"},
		{"indirect-wrapper", "internal/adapter/wrapper.go", "package adapter\nimport _ \"" + m2ModerncDriver + "/lib\"\n", "go-import"},
		{"reflection", "internal/x/x.go", "package x\nimport \"plugin\"\nfunc load(){ _, _ = plugin" + ".Open(\"authority.so\") }\n", "dynamic-load"},
		{"subprocess", "internal/x/x.go", "package x\nimport \"os/exec\"\nfunc run(){ _ = exec.Command(\"" + m2LegacyWord + "3\") }\n", "subprocess"},
		{"linkname", "internal/x/x.go", "package x\n//go:" + "linkname Open " + m2LegacyWord + ".Open\nfunc Open()\n", "linkname"},
		{"cgo", "internal/x/x.go", "package x\n// #cgo LDFLAGS: -l" + m2LegacyWord + "3\nimport \"C\"\n", "cgo"},
		{"embedded-schema", "assets/authority.sql", "CREATE " + "TABLE authority(id text);", "copied-schema"},
		{"copied-driver", "internal/archive/store.go", "package archive\nconst header = \"" + m2LegacyTitle + " format 3\\x00\"\n", "copied-schema"},
		{"renamed-driver", "internal/cache/provider.go", "package cache\nconst catalog = \"" + m2LegacyWord + "_master\"\n", "copied-schema"},
		{"direct-module", "go.mod", "module example.test/x\nrequire " + m2ModerncDriver + " v1.57.0\n", "driver"},
		{"tool-module", "go.mod", "module example.test/x\ntool " + m2ModerncDriver + "\n", "driver"},
		{"exclusive-module", "go.mod", "module example.test/x\nrequire modernc.org/libc v1.74.4\n", "module"},
		{"sbom-component", "artifacts/sbom.json", `{"components":[{"name":"` + m2ModerncDriver + `"}]}`, "driver"},
		{"binary-string", "dist/spec-kitty", "ELF\\x00" + m2ModerncDriver + "\\x00", "driver"},
		{"installed-driver", "install/lib/provider.bin", m2ModerncDriver, "driver"},
		{"help-selector", "cmd/spec-kitty/help.go", "package main\nconst help = \"--backend " + m2LegacyWord + "\"\n", "selector"},
		{"fallback", "internal/x/x.go", "package x\nvar " + m2LegacyTitle + "Fallback bool\n", "fallback"},
		{"comparison-read", "internal/x/x.go", "package x\nfunc compare" + m2LegacyTitle + "(){}\n", "comparison-read"},
		{"rescue-read", "internal/x/x.go", "package x\nfunc rescue" + m2LegacyTitle + "(){}\n", "rescue-read"},
		{"dual-write", "internal/x/x.go", "package x\nfunc dualWrite" + m2LegacyTitle + "(){}\n", "dual-write"},
		{"shadow-write", "internal/x/x.go", "package x\nfunc shadowWrite" + m2LegacyTitle + "(){}\n", "shadow-write"},
		{"automatic-import", "internal/x/x.go", "package x\nfunc import" + m2LegacyTitle + "(){}\n", "automatic-import"},
		{"migration", "internal/x/x.go", "package x\nfunc migrate" + m2LegacyTitle + "(){}\n", "migration"},
		{"runtime-open", "internal/x/x.go", "package x\nfunc open" + m2LegacyTitle + "(){}\n", "runtime-open"},
		{"ignored-source", ".cache/generated/provider.go", "package generated\nimport _ \"" + m2ModerncDriver + "\"\n", "go-import"},
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
		writeM2Mutant(t, root, "kitty-specs/beads-tracker-integration-01M0YG7E/plan.md", []byte("The retired driver was "+m2ModerncDriver+"."))
		if evidence := inspectM2Absence(t, root, false); len(evidence.Findings) != 0 {
			t.Fatalf("historical documentation rejected:\n%s", formatM2Findings(evidence.Findings))
		}
	})
	t.Run("executable-beneath-history", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "kitty-specs/beads-tracker-integration-01M0YG7E/provider.go", []byte("package history\nimport _ \""+m2ModerncDriver+"\"\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "go-import", "kitty-specs/beads-tracker-integration-01M0YG7E/provider.go")
	})
}

func TestM2GateSourcesDoNotShelterExecutableBehavior(t *testing.T) {
	attacks := []struct {
		path, content, kind string
	}{
		{"scripts/m2-absence/inspect.sh", "#!/bin/sh\n" + m2LegacyWord + "3 authority.db\n", "subprocess"},
		{"internal/architecture/m1_crown_test.go", "package architecture\nfunc " + m2LegacyTitle + "Fallback() bool { return true }\n", "fallback"},
	}
	for _, attack := range attacks {
		t.Run(filepath.Base(attack.path), func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, attack.path, []byte(attack.content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, attack.kind, attack.path)
		})
	}
}

func TestM2SplitExecutableTokensFailClosedWithoutOverNormalization(t *testing.T) {
	longGap := strings.Repeat("# intervening line\n", 65)
	tests := []struct {
		name, path, content string
		want                bool
	}{
		{"go-concatenation", "internal/x/x.go", "package x\nimport \"os/exec\"\nfunc run(){ _ = exec.Command(\"sql\"+\"ite3\", \"authority.db\") }\n", true},
		{"go-alias-flow", "internal/x/x.go", "package x\nimport command \"os/exec\"\nfunc run(){ retired := \"sql\"+\"ite3\"; _ = command.Command(retired) }\n", true},
		{"shell-adjacent-quotes", "scripts/x.sh", "#!/bin/sh\nretired_cli='sql''ite3'; \"$retired_cli\" authority.db\n", true},
		{"shell-multiline-adjacent-quotes", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli='sql''ite3'\n\"$review_retired_cli\" /tmp/x\n", true},
		{"go-unrelated-concatenation", "internal/x/x.go", "package x\nimport \"os/exec\"\nfunc run(){ _ = exec.Command(\"sql\"+\"-formatter\") }\n", false},
		{"shell-uninvoked-data", "scripts/x.sh", "#!/bin/sh\nretired_cli='sql''ite3'; printf '%s' native\n", false},
		{"shell-multiline-uninvoked-data", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli='sql''ite3'\nprintf '%s' \"$review_retired_cli\"\n", false},
		{"shell-command-before-assignment", "scripts/x.sh", "#!/bin/sh\n\"$review_retired_cli\" /tmp/x; review_retired_cli='sql''ite3'\n", false},
		{"shell-beyond-former-line-horizon", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli='sql''ite3'\n" + longGap + "\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-long-uninvoked-data", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli='sql''ite3'\n" + longGap + "printf '%s' native\n", false},
		{"shell-adjacent-benign-assignment", "scripts/x.sh", "#!/bin/sh\nreview_benign=native; review_retired_cli='sql''ite3'; \"$review_retired_cli\" /tmp/x\n", true},
		{"shell-newline-separated-assignments", "scripts/x.sh", "#!/bin/sh\nreview_benign=native\nreview_retired_cli='sql''ite3'\n\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-mixed-whitespace-assignments", "scripts/x.sh", "#!/bin/sh\n review_benign=native ;\treview_retired_cli='sql''ite3' ; \t\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-apostrophe-in-comment", "scripts/x.sh", "#!/bin/sh\n# don't merge comment quoting into executable parsing\nreview_retired_cli='sql''ite3'\n\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-comment-after-statement", "scripts/x.sh", "#!/bin/sh\nreview_benign=native # comment's quote is inert\nreview_retired_cli='sql''ite3'; \"$review_retired_cli\" /tmp/x\n", true},
		{"shell-indented-comment", "scripts/x.sh", "#!/bin/sh\n  # comment's quote is inert\nreview_retired_cli='sql''ite3'; \"$review_retired_cli\" /tmp/x\n", true},
		{"shell-quoted-hash", "scripts/x.sh", "#!/bin/sh\nreview_benign='# literal; not a boundary'; review_retired_cli='sql''ite3'; \"$review_retired_cli\" /tmp/x\n", true},
		{"shell-escaped-hash", "scripts/x.sh", "#!/bin/sh\nprintf '%s' \\#; review_retired_cli='sql''ite3'; \"$review_retired_cli\" /tmp/x\n", true},
		{"shell-comment-between-assignment-and-command", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli='sql''ite3'\n# don't disturb active assignments\n\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-backslash-newline", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli='sql''ite3'; \\\n\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-unquoted-backslash-word", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli=sql\\ite3\n\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-interpreter-option-arity", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli=sql\\ite3; /bin/sh -o errexit -c \"$review_retired_cli\"\n", true},
		{"shell-env-option-arity", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli=sql\\ite3; /usr/bin/env -C . \"$review_retired_cli\" /tmp/x\n", true},
		{"shell-single-quoted-word", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli='sql''ite3'\n\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-double-quoted-word", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli=\"sql\"\"ite3\"\n\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-concatenated-word", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli=sql'ite'3\n\"$review_retired_cli\" /tmp/x\n", true},
		{"shell-unquoted-backslash-uninvoked", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli=sql\\ite3\nprintf '%s' native\n", false},
		{"shell-unquoted-backslash-non-command", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli=sql\\ite3\nprintf '%s' \"$review_retired_cli\"\n", false},
		{"shell-case-pattern-data", "scripts/x.sh", "#!/bin/sh\nreview_retired_cli=sql\\ite3\ncase \"$review_retired_cli\" in \"$review_retired_cli\") printf '%s' native ;; esac\n", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, test.path, []byte(test.content))
			evidence := inspectM2Absence(t, root, false)
			if test.want {
				assertM2Finding(t, evidence.Findings, "subprocess", test.path)
			} else if len(evidence.Findings) != 0 {
				t.Fatalf("clean split-token control rejected: %s", formatM2Findings(evidence.Findings))
			}
		})
	}

	t.Run("supported-command-positions", func(t *testing.T) {
		assignment := "#!/bin/sh\nreview_retired_cli=sql\\ite3\n"
		for name, invocation := range map[string]string{
			"if":                  "if \"$review_retired_cli\" /tmp/x; then :; fi\n",
			"then":                "if true; then \"$review_retired_cli\" /tmp/x; fi\n",
			"elif":                "if false; then :; elif \"$review_retired_cli\" /tmp/x; then :; fi\n",
			"else":                "if false; then :; else \"$review_retired_cli\" /tmp/x; fi\n",
			"while":               "while \"$review_retired_cli\" /tmp/x; do :; done\n",
			"until":               "until \"$review_retired_cli\" /tmp/x; do :; done\n",
			"do":                  "while true; do \"$review_retired_cli\" /tmp/x; done\n",
			"negation":            "! \"$review_retired_cli\" /tmp/x\n",
			"and-list":            "true && \"$review_retired_cli\" /tmp/x\n",
			"or-list":             "false || \"$review_retired_cli\" /tmp/x\n",
			"pipeline":            "true | \"$review_retired_cli\" /tmp/x\n",
			"semicolon":           "true; \"$review_retired_cli\" /tmp/x\n",
			"newline":             "true\n\"$review_retired_cli\" /tmp/x\n",
			"subshell":            "( \"$review_retired_cli\" /tmp/x )\n",
			"group":               "{ \"$review_retired_cli\" /tmp/x; }\n",
			"case-same-line":      "case native in native) \"$review_retired_cli\" /tmp/x ;; esac\n",
			"case-multiline":      "case native in\n  native)\n    \"$review_retired_cli\" /tmp/x\n    ;;\nesac\n",
			"command":             "command \"$review_retired_cli\" /tmp/x\n",
			"command-option":      "command -p -- \"$review_retired_cli\" /tmp/x\n",
			"exec":                "exec \"$review_retired_cli\" /tmp/x\n",
			"env":                 "env \"$review_retired_cli\" /tmp/x\n",
			"env-prefixes":        "env -i -u OTHER REVIEW=native \"$review_retired_cli\" /tmp/x\n",
			"nohup":               "nohup \"$review_retired_cli\" /tmp/x\n",
			"nice":                "nice -n 5 \"$review_retired_cli\" /tmp/x\n",
			"time":                "time -p \"$review_retired_cli\" /tmp/x\n",
			"nested-wrapper":      "env REVIEW=native command -- \"$review_retired_cli\" /tmp/x\n",
			"path-env":            "/usr/bin/env \"$review_retired_cli\" /tmp/x\n",
			"path-nested":         "/usr/bin/env -- REVIEW=native command -- \"$review_retired_cli\" /tmp/x\n",
			"path-nohup":          "/usr/bin/nohup \"$review_retired_cli\" /tmp/x\n",
			"path-nice":           "/usr/bin/nice -n 5 \"$review_retired_cli\" /tmp/x\n",
			"path-time":           "/usr/bin/time -p \"$review_retired_cli\" /tmp/x\n",
			"sh-command":          "sh -c \"$review_retired_cli\"\n",
			"dash-command":        "dash -c \"$review_retired_cli\"\n",
			"bash-command":        "bash --noprofile -c \"$review_retired_cli\"\n",
			"ksh-command":         "ksh -c \"$review_retired_cli\"\n",
			"zsh-command":         "zsh -c \"$review_retired_cli\"\n",
			"path-sh":             "/bin/sh -c \"$review_retired_cli\"\n",
			"path-sh-options":     "/bin/sh -o errexit -c \"$review_retired_cli\"\n",
			"sh-unknown-options":  "sh --review-unknown native \"$review_retired_cli\"\n",
			"eval":                "eval \"$review_retired_cli\"\n",
			"xargs":               "printf '%s' native | xargs \"$review_retired_cli\"\n",
			"env-directory":       "/usr/bin/env -C . \"$review_retired_cli\" /tmp/x\n",
			"env-unknown-options": "env --review-unknown native \"$review_retired_cli\" /tmp/x\n",
		} {
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				writeM2Mutant(t, root, "scripts/x.sh", []byte(assignment+invocation))
				evidence := inspectM2Absence(t, root, false)
				assertM2Finding(t, evidence.Findings, "subprocess", "scripts/x.sh")
			})
		}
	})

	t.Run("unknown-path-dispatch-with-tracked-argument-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\nreview_retired_cli=sql\\ite3\n/usr/bin/custom-wrapper \"$review_retired_cli\"\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
	})

	t.Run("known-path-data-command-keeps-tracked-value-as-data", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\nreview_retired_cli=sql\\ite3\n/usr/bin/printf '%s' \"$review_retired_cli\"\n"))
		evidence := inspectM2Absence(t, root, false)
		if len(evidence.Findings) != 0 {
			t.Fatalf("known data command rejected: %s", formatM2Findings(evidence.Findings))
		}
	})

	t.Run("malformed-go-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "internal/x/x.go", []byte("package x\nfunc broken("))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "malformed-go", "internal/x/x.go")
	})

	t.Run("supported-static-shell-words", func(t *testing.T) {
		for name, word := range map[string]string{
			"unquoted":      m2LegacyCLI,
			"single-quoted": "'sql''ite3'",
			"double-quoted": `"sql""ite3"`,
			"concatenated":  "sql'ite'3",
			"escaped":       `sql\ite3`,
		} {
			t.Run(name, func(t *testing.T) {
				value, resolved := m2StaticShellWord(word)
				if !resolved || value != m2LegacyCLI {
					t.Fatalf("static shell word resolved=(%q,%v), want admitted forbidden executable", value, resolved)
				}
			})
		}
	})

	t.Run("unsupported-invoked-assignment-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\nreview_retired_cli=$(printf '%s%s' sql ite3)\n\"$review_retired_cli\" /tmp/x\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
	})

	t.Run("resolved-indirect-command-is-inspected", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\nreview_retired_cli=sql\\ite3\neval \"$review_retired_cli\"\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "subprocess", "scripts/x.sh")
	})

	t.Run("unsupported-indirect-command-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\nreview_retired_cli=${review_source}\neval \"$review_retired_cli\"\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
	})

	t.Run("unsupported-wrapper-value-fails-closed-after-options", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\nreview_retired_cli=${review_source}\n/usr/bin/env -C . \"$review_retired_cli\" /tmp/x\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
	})

	t.Run("unsupported-uninvoked-assignment-is-data", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\nreview_retired_cli=$(printf '%s%s' sql ite3)\nprintf '%s' native\n"))
		evidence := inspectM2Absence(t, root, false)
		if len(evidence.Findings) != 0 {
			t.Fatalf("uninvoked unsupported assignment rejected: %s", formatM2Findings(evidence.Findings))
		}
	})

	for name, content := range map[string]string{
		"unterminated-single-quote": "#!/bin/sh\nvalue='unterminated\n",
		"unterminated-double-quote": "#!/bin/sh\nvalue=\"unterminated\n",
		"unterminated-escape":       "#!/bin/sh\nvalue=native\\",
		"unsupported-backtick":      "#!/bin/sh\nvalue=`command`\n",
	} {
		t.Run(name+"-fails-closed", func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
		})
	}

	t.Run("oversize-shell-is-explicit-budget-refusal", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte(strings.Repeat("#", (8<<20)+1)))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "budget", "scripts/x.sh")
	})
}

func TestM2AbsenceLayerDetectors(t *testing.T) {
	command := exec.Command(filepath.Join(m2RepositoryRoot(t), "scripts", "m2-absence", "mutants.sh"), "--layers-only")
	command.Dir = m2RepositoryRoot(t)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("layer detector mutants: %v\n%s", err, output)
	}
}

func TestM2ShellProbe(t *testing.T) {
	path := os.Getenv("M2_SHELL_PROBE")
	if path == "" {
		t.Skip("shell probe path is supplied only by the qualification wrapper")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var findings []m2AbsenceFinding
	inspectM2Candidate(filepath.ToSlash(path), data, func(kind, candidate, detail string) {
		findings = append(findings, m2AbsenceFinding{Kind: kind, Path: candidate, Detail: detail})
	})
	if len(findings) != 0 {
		t.Fatalf("shell candidate rejected: %s", formatM2Findings(findings))
	}
}

func TestM2QualificationRequiresAdmittedSidecar(t *testing.T) {
	root := t.TempDir()
	wrong := filepath.Join(root, "wrong-gapdbd")
	writeM2Mutant(t, root, "wrong-gapdbd", []byte("#!/bin/sh\nexit 0\n"))
	if err := os.Chmod(wrong, 0o700); err != nil {
		t.Fatal(err)
	}
	unexecutable := filepath.Join(root, "unexecutable-gapdbd")
	writeM2Mutant(t, root, "unexecutable-gapdbd", []byte("not executable"))

	for name, sidecar := range map[string]string{
		"missing":      "",
		"relative":     "gapdbd",
		"nonexistent":  filepath.Join(root, "absent-gapdbd"),
		"unexecutable": unexecutable,
		"wrong-digest": wrong,
	} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(filepath.Join(m2RepositoryRoot(t), "scripts", "m2-absence", "inspect.sh"), "--check-sidecar")
			command.Dir = m2RepositoryRoot(t)
			command.Env = m2EnvironmentWithSidecar(sidecar)
			err := command.Run()
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) || exitError.ExitCode() != 64 {
				t.Fatalf("inadmissible sidecar did not fail closed with status 64: %v", err)
			}
		})
	}
}

func m2EnvironmentWithSidecar(sidecar string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SPEC_KITTY_WP15_GAPDBD=") {
			environment = append(environment, entry)
		}
	}
	return append(environment, "SPEC_KITTY_WP15_GAPDBD="+sidecar)
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
