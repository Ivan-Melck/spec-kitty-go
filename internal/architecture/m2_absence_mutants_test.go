package architecture

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	t.Run("exact-review-feedback-document", func(t *testing.T) {
		root := t.TempDir()
		path := "kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP16-structural-sqlite-eradication-and-absence-proof/review-feedback-27.md"
		writeM2Mutant(t, root, path, []byte("The retired executable was "+m2LegacyCLI+"."))
		if evidence := inspectM2Absence(t, root, false); len(evidence.Findings) != 0 {
			t.Fatalf("exact historical review feedback rejected:\n%s", formatM2Findings(evidence.Findings))
		}
	})
	t.Run("review-feedback-other-token", func(t *testing.T) {
		root := t.TempDir()
		path := "kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP16-structural-sqlite-eradication-and-absence-proof/review-feedback-27.md"
		writeM2Mutant(t, root, path, []byte("An unapproved driver reference: "+m2ModerncDriver))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "driver", path)
	})
	t.Run("review-feedback-near-path", func(t *testing.T) {
		root := t.TempDir()
		path := "kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP16-structural-sqlite-eradication-and-absence-proof/review-feedback-27-copy.md"
		writeM2Mutant(t, root, path, []byte("The retired executable was "+m2LegacyCLI+"."))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "subprocess", path)
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
	t.Run("special-builtin-registry-is-closed", func(t *testing.T) {
		want := []string{".", ":", "break", "continue", "eval", "exec", "exit", "export", "readonly", "return", "set", "shift", "trap", "unset"}
		got := make([]string, 0, len(m2ShellSpecialBuiltins))
		for name := range m2ShellSpecialBuiltins {
			got = append(got, name)
		}
		sort.Strings(got)
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("special builtin registry=%v want=%v", got, want)
		}
	})

	t.Run("command-target-resolution-contract-is-closed", func(t *testing.T) {
		values := map[string]string{"KNOWN": "native", "EMPTY": ""}
		lookup := func(name string) (string, bool) {
			value, exists := values[name]
			return value, exists
		}
		for name, test := range map[string]struct {
			token               string
			expanded, quoted    bool
			resolved, ambiguous bool
			value               string
		}{
			"literal":        {token: "native", resolved: true, value: "native"},
			"unquoted-known": {token: "$KNOWN", expanded: true, ambiguous: true},
			"quoted-known":   {token: `"$KNOWN"`, expanded: true, quoted: true, resolved: true, value: "native"},
			"quoted-empty":   {token: `"${EMPTY}"`, expanded: true, quoted: true, resolved: true},
			"quoted-unknown": {token: `"$UNKNOWN"`, expanded: true, quoted: true, ambiguous: true},
			"parameter":      {token: `"${UNKNOWN:-native}"`, expanded: true, ambiguous: true},
			"command-subst":  {token: `"$(printf native)"`, expanded: true, ambiguous: true},
			"arithmetic":     {token: `"$((1 + 2))"`, expanded: true, ambiguous: true},
			"single-quoted":  {token: `'${UNKNOWN:-native}'`, resolved: true, value: "${UNKNOWN:-native}"},
			"escaped-dollar": {token: `\$UNKNOWN`, resolved: true, value: "$UNKNOWN"},
		} {
			t.Run(name, func(t *testing.T) {
				got := m2ResolveShellCommandTarget(test.token, lookup)
				if got.expanded != test.expanded || got.quoted != test.quoted || got.resolved != test.resolved ||
					(got.ambiguity != "") != test.ambiguous || got.value != test.value {
					t.Fatalf("resolution=%+v", got)
				}
			})
		}
	})

	t.Run("command-substitution-scan-contract-is-closed", func(t *testing.T) {
		for name, test := range map[string]struct {
			source string
			want   bool
		}{
			"dollar-paren":           {source: "printf '%s' $(printf native)", want: true},
			"double-quoted-dollar":   {source: `printf '%s' "$(printf native)"`, want: true},
			"backtick":               {source: "printf '%s' `printf native`", want: true},
			"double-quoted-backtick": {source: "printf '%s' \"`printf native`\"", want: true},
			"single-quoted-dollar":   {source: `printf '%s' '$(printf native)'`},
			"single-quoted-backtick": {source: "printf '%s' '`printf native`'"},
			"escaped-dollar":         {source: `printf '%s' "\$(printf native)"`},
			"escaped-backtick":       {source: "printf '%s' \"\\`printf native\\`\""},
			"comment":                {source: "# $(printf native) `printf native`\nprintf '%s' native\n"},
			"arithmetic":             {source: `printf '%s' "$((1 + 2))"`},
		} {
			t.Run(name, func(t *testing.T) {
				got := m2ShellExecutableSubstitutionAmbiguity(test.source) != ""
				if got != test.want {
					t.Fatalf("command substitution ambiguity=%t want=%t", got, test.want)
				}
			})
		}
	})

	t.Run("heredoc-body-metadata-is-closed", func(t *testing.T) {
		source := "cat <<EOF <<-'LITERAL'\n$(printf native)\nEOF\n\t$(printf literal)\n\tLITERAL\n"
		sanitized, bodies, ambiguity := m2ShellWithoutHeredocBodies(source)
		if ambiguity != "" {
			t.Fatalf("sanitize heredocs: %s", ambiguity)
		}
		if len(bodies) != 2 {
			t.Fatalf("heredoc bodies=%d want=2", len(bodies))
		}
		if bodies[0].delimiter != "EOF" || bodies[0].stripTabs || !bodies[0].expansionEnabled ||
			bodies[0].content != "$(printf native)\n" {
			t.Fatalf("unquoted heredoc metadata=%+v", bodies[0])
		}
		if bodies[1].delimiter != "LITERAL" || !bodies[1].stripTabs || bodies[1].expansionEnabled ||
			bodies[1].content != "$(printf literal)\n" {
			t.Fatalf("quoted heredoc metadata=%+v", bodies[1])
		}
		if strings.Contains(sanitized, "printf native") || strings.Contains(sanitized, "printf literal") {
			t.Fatalf("sanitized command stream retained heredoc body: %q", sanitized)
		}
		for label, continued := range map[string]string{
			"lf":   "$\\\n(printf native)\n",
			"crlf": "$\\\r\n(printf native)\r\n",
		} {
			if got := m2ShellHeredocSubstitutionAmbiguity(continued); got != "heredoc-line-continuation" {
				t.Fatalf("%s continuation ambiguity=%q", label, got)
			}
		}
		for label, declaration := range map[string]string{
			"after-declaration":  "cat <<EOF \\\n; printf native\ndata\nEOF\n",
			"before-declaration": "cat \\\n<<EOF\ndata\nEOF\n",
			"inside-delimiter":   "cat <<EO\\\nF\ndata\nEOF\n",
			"crlf":               "cat <<EOF \\\r\n; printf native\r\ndata\r\nEOF\r\n",
		} {
			if _, _, got := m2ShellWithoutHeredocBodies(declaration); got != "heredoc-declaration-line-continuation" {
				t.Fatalf("%s declaration ambiguity=%q", label, got)
			}
		}
		for label, splitOperator := range map[string]string{
			"lf":                 "cat <\\\n<EOF\ndata\nEOF\n",
			"crlf":               "cat <\\\r\n<EOF\r\ndata\r\nEOF\r\n",
			"tab-stripped":       "cat <\\\n<-EOF\n\tdata\n\tEOF\n",
			"quoted-delimiter":   "cat <\\\n<'EOF'\ndata\nEOF\n",
			"multiple-operators": "cat <\\\n<ONE <\\\n<TWO\none\nONE\ntwo\nTWO\n",
		} {
			if _, _, got := m2ShellWithoutHeredocBodies(splitOperator); got != "heredoc-declaration-line-continuation" {
				t.Fatalf("%s split-operator ambiguity=%q", label, got)
			}
		}
		for label, literal := range map[string]string{
			"single-quoted":         "printf '%s' '<\\\n<EOF'\n",
			"double-quoted":         "printf '%s' \"<\\\n<EOF\"\n",
			"comment":               "# <\\\n<EOF\nprintf '%s' native\n",
			"redirection-near-miss": "cat <\\\ninput.txt\n",
			"comparison-near-miss":  "[ 1 <\\\n= 2 ]\n",
		} {
			if _, _, got := m2ShellWithoutHeredocBodies(literal); got != "" {
				t.Fatalf("%s literal split-operator control ambiguity=%q", label, got)
			}
		}
		oversize := "cat <<EOF\n" + strings.Repeat("x", (8<<20)+1) + "\nEOF\n"
		if _, _, budgetAmbiguity := m2ShellWithoutHeredocBodies(oversize); budgetAmbiguity != "heredoc-body-budget" {
			t.Fatalf("oversize heredoc ambiguity=%q", budgetAmbiguity)
		}
	})

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

	t.Run("shell-command-before-assignment", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\n\"$review_retired_cli\" /tmp/x; review_retired_cli='sql''ite3'\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
	})

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

	for name, content := range map[string]string{
		"direct-prefix-uses-pre-command-value":  "#!/bin/sh\nreview_retired_cli=sql\\ite3; review_retired_cli=native \"$review_retired_cli\" /tmp/x\n",
		"wrapped-prefix-uses-pre-command-value": "#!/bin/sh\nreview_retired_cli=sql\\ite3; env review_retired_cli=native \"$review_retired_cli\" /tmp/x\n",
		"prefix-does-not-persist-after-command": "#!/bin/sh\nreview_retired_cli=sql\\ite3\nreview_retired_cli=native true\n\"$review_retired_cli\" /tmp/x\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "subprocess", "scripts/x.sh")
		})
	}

	for name, content := range map[string]string{
		"pipeline-sibling-uses-parent-value":           "#!/bin/sh\nreview_retired_cli=sql\\ite3; review_retired_cli=native | \"$review_retired_cli\" /tmp/x\n",
		"pipeline-assignment-does-not-persist":         "#!/bin/sh\nreview_retired_cli=sql\\ite3\nreview_retired_cli=native | true\n\"$review_retired_cli\" /tmp/x\n",
		"subshell-assignment-does-not-escape":          "#!/bin/sh\nreview_retired_cli=sql\\ite3; ( review_retired_cli=native ); \"$review_retired_cli\" /tmp/x\n",
		"nested-subshell-assignment-does-not-escape":   "#!/bin/sh\nreview_retired_cli=sql\\ite3\n( ( review_retired_cli=native ) )\n\"$review_retired_cli\" /tmp/x\n",
		"current-shell-group-persists":                 "#!/bin/sh\nreview_retired_cli=native\n{ review_retired_cli=sql\\ite3; }\n\"$review_retired_cli\" /tmp/x\n",
		"background-assignment-does-not-persist":       "#!/bin/sh\nreview_retired_cli=sql\\ite3\nreview_retired_cli=native & true\n\"$review_retired_cli\" /tmp/x\n",
		"group-pipeline-assignment-is-isolated":        "#!/bin/sh\nreview_retired_cli=sql\\ite3; { review_retired_cli=native; } | \"$review_retired_cli\" /tmp/x\n",
		"group-background-assignment-is-isolated":      "#!/bin/sh\nreview_retired_cli=sql\\ite3; { review_retired_cli=native; } & \"$review_retired_cli\" /tmp/x\n",
		"nested-group-pipeline-is-isolated":            "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ { review_retired_cli=native; }; } | \"$review_retired_cli\" /tmp/x\n",
		"pipeline-inside-group-does-not-persist":       "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native | true; }\n\"$review_retired_cli\" /tmp/x\n",
		"subshell-inside-group-remains-isolated":       "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ ( review_retired_cli=native ); }\n\"$review_retired_cli\" /tmp/x\n",
		"redirected-group-pipeline-is-isolated":        "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } >/dev/null | \"$review_retired_cli\" /tmp/x\n",
		"redirected-group-background-is-isolated":      "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } 2>/dev/null & \"$review_retired_cli\" /tmp/x\n",
		"fd-dup-group-pipeline-is-isolated":            "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } 2>&1 | \"$review_retired_cli\" /tmp/x\n",
		"fd-dup-group-background-is-isolated":          "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } 2>&1 & \"$review_retired_cli\" /tmp/x\n",
		"repeated-redirect-group-pipeline-is-isolated": "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } >/dev/null 2>&1 | \"$review_retired_cli\" /tmp/x\n",
		"fd-close-group-pipeline-is-isolated":          "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } 2>&- | \"$review_retired_cli\" /tmp/x\n",
		"input-fd-dup-group-pipeline-is-isolated":      "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } 0<&1 | \"$review_retired_cli\" /tmp/x\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "subprocess", "scripts/x.sh")
		})
	}

	for name, content := range map[string]string{
		"pipeline-left-does-not-reach-sibling":         "#!/bin/sh\nreview_retired_cli=native; review_retired_cli=sql\\ite3 | \"$review_retired_cli\" /tmp/x\n",
		"subshell-assignment-is-isolated":              "#!/bin/sh\nreview_retired_cli=native\n( review_retired_cli=sql\\ite3 )\n\"$review_retired_cli\" /tmp/x\n",
		"current-shell-group-clean-update":             "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; }\n\"$review_retired_cli\" /tmp/x\n",
		"background-left-is-isolated":                  "#!/bin/sh\nreview_retired_cli=native\nreview_retired_cli=sql\\ite3 & \"$review_retired_cli\" /tmp/x\n",
		"group-pipeline-left-is-isolated":              "#!/bin/sh\nreview_retired_cli=native; { review_retired_cli=sql\\ite3; } | \"$review_retired_cli\" /tmp/x\n",
		"group-background-left-is-isolated":            "#!/bin/sh\nreview_retired_cli=native; { review_retired_cli=sql\\ite3; } & \"$review_retired_cli\" /tmp/x\n",
		"nested-current-shell-groups-persist":          "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ { review_retired_cli=native; }; }\n\"$review_retired_cli\" /tmp/x\n",
		"group-inside-subshell-remains-isolated":       "#!/bin/sh\nreview_retired_cli=native\n( { review_retired_cli=sql\\ite3; } )\n\"$review_retired_cli\" /tmp/x\n",
		"redirected-group-left-does-not-reach-sibling": "#!/bin/sh\nreview_retired_cli=native\n{ review_retired_cli=sql\\ite3; } >/dev/null | \"$review_retired_cli\" /tmp/x\n",
		"fd-dup-group-left-does-not-reach-sibling":     "#!/bin/sh\nreview_retired_cli=native\n{ review_retired_cli=sql\\ite3; } 2>&1 | \"$review_retired_cli\" /tmp/x\n",
		"redirected-current-shell-group-persists":      "#!/bin/sh\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } 2>&1; \"$review_retired_cli\" /tmp/x\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			if len(evidence.Findings) != 0 {
				t.Fatalf("clean shell-scope control rejected: %s", formatM2Findings(evidence.Findings))
			}
		})
	}

	for name, content := range map[string]string{
		"single-line-function-direct-execution":         "#!/bin/sh\nhelper() { sql\\ite3 /tmp/x; }\n",
		"single-line-function-variable-execution":       "#!/bin/sh\nhelper() { review_retired_cli=sql\\ite3; \"$review_retired_cli\" /tmp/x; }\n",
		"multiline-function-variable-execution":         "#!/bin/sh\nhelper()\n{\nreview_retired_cli=sql\\ite3\n\"$review_retired_cli\" /tmp/x\n}\n",
		"function-followed-by-parent-command":           "#!/bin/sh\nreview_retired_cli=sql\\ite3\nhelper()\n{ review_retired_cli=native; }\n\"$review_retired_cli\" /tmp/x\n",
		"invoked-function-applies-direct-effect":        "#!/bin/sh\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"reviewer-exact-invoked-function-effect":        "#!/bin/sh\nreview_retired_cli=native\nselect(){ review_retired_cli=sql\\ite3; }\nselect\n\"$review_retired_cli\" /tmp/x\n",
		"invoked-multiline-function-effect":             "#!/bin/sh\nreview_retired_cli=native\nselect_retired_cli()\n{\nreview_retired_cli=sql\\ite3\n}\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"invoked-nested-function-effect":                "#!/bin/sh\nreview_retired_cli=native\ninner(){ review_retired_cli=sql\\ite3; }\nouter(){ inner; }\nouter\n\"$review_retired_cli\" /tmp/x\n",
		"forward-nested-function-call":                  "#!/bin/sh\nreview_retired_cli=native\nouter(){ inner; }\ninner(){ review_retired_cli=sql\\ite3; }\nouter\n\"$review_retired_cli\" /tmp/x\n",
		"nested-effect-order-is-preserved":              "#!/bin/sh\nreview_retired_cli=native\ninner(){ review_retired_cli=native; }\nouter(){ inner; review_retired_cli=sql\\ite3; }\nouter\n\"$review_retired_cli\" /tmp/x\n",
		"subshell-function-effect-is-isolated":          "#!/bin/sh\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\n( helper )\n\"$review_retired_cli\" /tmp/x\n",
		"early-return-retains-prior-value":              "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ return 0; review_retired_cli=native; }\nselect_native_cli\n\"$review_retired_cli\" /tmp/x\n",
		"return-after-effect-stops-summary":             "#!/bin/sh\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; return 0; review_retired_cli=native; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-bypasses-function-lookup":              "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\ncommand select_native_cli || :\n\"$review_retired_cli\" /tmp/x\n",
		"env-bypasses-function-lookup":                  "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\nenv select_native_cli || :\n\"$review_retired_cli\" /tmp/x\n",
		"nested-command-bypasses-function-lookup":       "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\nouter(){ command select_native_cli || :; }\nouter\n\"$review_retired_cli\" /tmp/x\n",
		"subshell-return-does-not-stop-function":        "#!/bin/sh\nreview_retired_cli=native\nselect_retired_cli(){ ( return 0 ); review_retired_cli=sql\\ite3; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"nested-subshell-return-does-not-stop-function": "#!/bin/sh\nreview_retired_cli=native\nselect_retired_cli(){ ( ( return 0 ) ); review_retired_cli=sql\\ite3; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"pipeline-group-return-does-not-stop-function":  "#!/bin/sh\nreview_retired_cli=native\nselect_retired_cli(){ { return 0; } | true; review_retired_cli=sql\\ite3; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"bare-time-invokes-function":                    "#!/bin/bash\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; }\ntime select_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"bare-time-posix-option-invokes-function":       "#!/bin/bash\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; }\ntime -p select_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"nested-bare-time-invokes-function":             "#!/bin/bash\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; }\nouter(){ time -p select_retired_cli; }\nouter\n\"$review_retired_cli\" /tmp/x\n",
		"dash-time-leaves-prior-retired":                "#!/bin/dash\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\ntime select_native_cli\n\"$review_retired_cli\" /tmp/x\n",
		"env-dash-time-leaves-prior-retired":            "#!/usr/bin/env dash\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\ntime select_native_cli\n\"$review_retired_cli\" /tmp/x\n",
		"bash-option-time-invokes-function":             "#!/usr/bin/bash -eu\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; }\ntime select_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"env-bash-time-invokes-function":                "#!/usr/bin/env bash\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; }\ntime select_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"env-s-bash-time-invokes-function":              "#!/usr/bin/env -S bash -eu\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; }\ntime select_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"env-option-bash-time-invokes-function":         "#!/usr/bin/env -i bash\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3; }\ntime select_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"dash-special-colon-prefix-persists":            "#!/bin/dash\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"dash-special-return-prefix-persists":           "#!/bin/dash\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 return 0; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"bash-special-colon-prefix-is-command-scoped":   "#!/bin/bash\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native :; }\nselect_native_cli\n\"$review_retired_cli\" /tmp/x\n",
		"bash-posix-shebang-prefix-persists":            "#!/bin/bash --posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"bash-o-posix-shebang-prefix-persists":          "#!/usr/bin/bash -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"env-s-bash-posix-prefix-persists":              "#!/usr/bin/env -S bash --posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"env-posixly-correct-prefix-persists":           "#!/usr/bin/env -S POSIXLY_CORRECT=1 bash\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"runtime-set-posix-prefix-persists":             "#!/bin/bash\nset -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"function-set-posix-prefix-persists":            "#!/bin/bash\nreview_retired_cli=native\nselect_retired_cli(){ set -o posix; review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"current-group-set-posix-persists":              "#!/bin/bash\n{ set -o posix; }\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"called-function-set-posix-persists":            "#!/bin/bash\nenable_posix(){ set -o posix; }\nenable_posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-set-posix-persists":                    "#!/bin/bash\ncommand set -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-p-set-posix-persists":                  "#!/bin/bash\ncommand -p set -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"set-terminator-preserves-posix-mode":           "#!/bin/bash --posix\nset -- +o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"ordered-set-options-end-in-posix-mode":         "#!/bin/bash\nset -o posix +o posix -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"function-command-set-posix-persists":           "#!/bin/bash\nenable_posix(){ command set -o posix; }\nenable_posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"current-group-command-set-posix-persists":      "#!/bin/bash\n{ command set -o posix; }\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-dash-p-target-keeps-posix":             "#!/bin/bash --posix\ncommand -- -p set +o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-double-dash-target-keeps-posix":        "#!/bin/bash --posix\ncommand -- -- set +o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-p-terminator-enables-posix":            "#!/bin/bash\ncommand -p -- set -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-variable-set-enables-posix":            "#!/bin/bash\nreview_command=set\ncommand \"$review_command\" -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-quoted-p-option-enables-posix":         "#!/bin/bash\ncommand \"-p\" set -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-variable-p-option-enables-posix":       "#!/bin/bash\nreview_command=-p\ncommand \"$review_command\" set -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-variable-target-keeps-posix":           "#!/bin/bash --posix\nreview_command=-p\ncommand -- \"$review_command\" set +o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"command-quoted-set-with-changed-ifs":           "#!/bin/bash\nIFS=:\nreview_command=set\ncommand \"$review_command\" -o posix\nreview_retired_cli=native\nselect_retired_cli(){ review_retired_cli=sql\\ite3 :; }\nselect_retired_cli\n\"$review_retired_cli\" /tmp/x\n",
		"local-quoted-retired-direct":                   "#!/bin/sh\nREVIEW_COMMAND=sql\\ite3\n\"$REVIEW_COMMAND\" /tmp/x\n",
		"local-quoted-retired-command-wrapper":          "#!/bin/sh\nREVIEW_COMMAND=sql\\ite3\ncommand \"$REVIEW_COMMAND\" /tmp/x\n",
		"local-quoted-retired-nested-wrapper":           "#!/bin/sh\nREVIEW_COMMAND=sql\\ite3\nenv command -- \"$REVIEW_COMMAND\" /tmp/x\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "subprocess", "scripts/x.sh")
		})
	}

	for name, content := range map[string]string{
		"safe-single-line-function":                        "#!/bin/sh\nhelper() { printf '%s' native; }\n",
		"safe-multiline-spaced-function":                   "#!/bin/sh\nhelper ()\n{\nprintf '%s' native\n}\n",
		"function-definition-does-not-persist":             "#!/bin/sh\nreview_retired_cli=native\nhelper() { review_retired_cli=sql\\ite3; }\n\"$review_retired_cli\" /tmp/x\n",
		"safe-invoked-function-no-effect":                  "#!/bin/sh\nreview_retired_cli=native\nhelper(){ printf '%s' native; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"function-called-before-definition":                "#!/bin/sh\nreview_retired_cli=native\nhelper\nhelper(){ review_retired_cli=sql\\ite3; }\n\"$review_retired_cli\" /tmp/x\n",
		"invoked-function-clean-effect":                    "#!/bin/sh\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"nested-clean-effect-order":                        "#!/bin/sh\nreview_retired_cli=sql\\ite3\ninner(){ review_retired_cli=sql\\ite3; }\nouter(){ inner; review_retired_cli=native; }\nouter\n\"$review_retired_cli\" /tmp/x\n",
		"return-before-effect-is-clean":                    "#!/bin/sh\nreview_retired_cli=native\nhelper(){ return 0; review_retired_cli=sql\\ite3; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-bypass-keeps-clean-prior":                 "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\ncommand helper || :\n\"$review_retired_cli\" /tmp/x\n",
		"sequential-direct-call-is-certain":                "#!/bin/sh\nreview_retired_cli=sql\\ite3\ntrue\nhelper(){ review_retired_cli=native; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"short-circuit-same-effect-is-stable":              "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=native; }\nfalse && helper\n\"$review_retired_cli\" /tmp/x\n",
		"conditional-same-effect-is-stable":                "#!/bin/sh\nreview_retired_cli=native\nhelper(){ if true; then review_retired_cli=native; fi; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"conditional-effect-used-as-data":                  "#!/bin/sh\nreview_retired_cli=sql\\ite3\nhelper(){ if true; then review_retired_cli=native; fi; }\nhelper\nprintf '%s' \"$review_retired_cli\"\n",
		"current-shell-group-return-stops-function":        "#!/bin/sh\nreview_retired_cli=native\nhelper(){ { return 0; }; review_retired_cli=sql\\ite3; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"nested-current-shell-group-return-stops-function": "#!/bin/sh\nreview_retired_cli=native\nhelper(){ { { return 0; }; }; review_retired_cli=sql\\ite3; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"path-time-bypasses-function":                      "#!/bin/bash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\n/usr/bin/time -p helper || :\n\"$review_retired_cli\" /tmp/x\n",
		"command-time-bypasses-function":                   "#!/bin/bash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\ncommand time -p helper || :\n\"$review_retired_cli\" /tmp/x\n",
		"env-time-bypasses-function":                       "#!/bin/bash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\nenv time -p helper || :\n\"$review_retired_cli\" /tmp/x\n",
		"nested-path-time-bypasses-function":               "#!/bin/bash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\n/usr/bin/env -i /usr/bin/time -p helper || :\n\"$review_retired_cli\" /tmp/x\n",
		"quoted-time-bypasses-function":                    "#!/bin/bash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\n\"time\" -p helper || :\n\"$review_retired_cli\" /tmp/x\n",
		"dash-time-bypasses-function":                      "#!/usr/bin/dash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\ntime -p helper || :\n\"$review_retired_cli\" /tmp/x\n",
		"env-dash-time-bypasses-function":                  "#!/usr/bin/env -S dash -e\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\ntime helper || :\n\"$review_retired_cli\" /tmp/x\n",
		"harmless-generic-time-helper":                     "#!/bin/sh\nshow(){ printf '%s\\n' native; }\ntime show\n",
		"bash-special-colon-prefix-discards-retired":       "#!/bin/bash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"dash-regular-printf-prefix-is-command-scoped":     "#!/bin/dash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 printf '%s' native; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"dash-external-prefix-is-command-scoped":           "#!/bin/dash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 /bin/true; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"generic-special-prefix-same-value-is-stable":      "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=native :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"generic-special-prefix-irrelevant-data":           "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_other_cli=sql\\ite3 :; }\nhelper\nprintf '%s' \"$review_other_cli\"\n\"$review_retired_cli\" /tmp/x\n",
		"generic-time-same-effect-is-stable":               "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"generic-time-irrelevant-effect-is-data":           "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_other_cli=sql\\ite3; }\ntime helper\nprintf '%s' \"$review_other_cli\"\n\"$review_retired_cli\" /tmp/x\n",
		"runtime-posix-toggle-off-restores-bash-default":   "#!/bin/bash\nset -o posix\nset +o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"shebang-posix-toggle-off-restores-bash-default":   "#!/bin/bash --posix\nset +o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"subshell-set-posix-does-not-escape":               "#!/bin/bash\n( set -o posix )\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"pipeline-set-posix-does-not-escape":               "#!/bin/bash\nset -o posix | true\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"background-set-posix-does-not-escape":             "#!/bin/bash\nset -o posix & true\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"function-disable-posix-persists-to-caller":        "#!/bin/bash --posix\ndisable_posix(){ set +o posix; }\ndisable_posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"irrelevant-posix-mode-change":                     "#!/bin/bash\nset -o posix\nprintf '%s' native\n",
		"set-terminator-leaves-default-bash-mode":          "#!/bin/bash\nset -- -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"set-first-positional-stops-option-parsing":        "#!/bin/bash\nset review-data -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"set-no-arguments-does-not-change-mode":            "#!/bin/bash\nset\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"set-option-listing-does-not-change-mode":          "#!/bin/bash\nset -o\nset +o\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"set-known-option-does-not-change-posix-mode":      "#!/bin/bash\nset -o pipefail\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"ordered-set-options-end-in-default-mode":          "#!/bin/bash\nset -o posix +o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-query-does-not-invoke-set":                "#!/bin/bash\ncommand -v set -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-terminator-can-disable-posix-mode":        "#!/bin/bash --posix\ncommand -- set +o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"subshell-command-set-posix-does-not-escape":       "#!/bin/bash\n( command set -o posix )\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"pipeline-command-set-posix-does-not-escape":       "#!/bin/bash\ncommand set -o posix | true\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"background-command-set-posix-does-not-escape":     "#!/bin/bash\ncommand set -o posix & true\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"env-command-set-does-not-invoke-shell-builtin":    "#!/bin/bash\nenv command set -o posix || :\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-vv-query-keeps-mode":                      "#!/bin/bash\ncommand -VV set -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-missing-name-keeps-mode":                  "#!/bin/bash\ncommand\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-p-missing-name-keeps-mode":                "#!/bin/bash\ncommand -p\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-terminator-missing-keeps-mode":            "#!/bin/bash\ncommand --\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-dash-p-target-keeps-default":              "#!/bin/bash\ncommand -- -p set -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-quoted-target-keeps-default":              "#!/bin/bash\ncommand -- \"-p\" set -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-quoted-multiword-is-scalar":               "#!/bin/bash\nreview_command='set -o posix'\ncommand \"$review_command\" || :\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-query-multiword-is-data":                  "#!/bin/bash\nreview_command='set -o posix'\ncommand -v $review_command >/dev/null || :\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-quoted-multiword-ignores-changed-ifs":     "#!/bin/bash\nIFS=:\nreview_command='set:-o:posix'\ncommand \"$review_command\" || :\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"ordinary-unquoted-expansion-is-data":              "#!/bin/bash\nreview_command='set -o posix'\nprintf '%s' $review_command\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"local-quoted-native-direct":                       "#!/bin/sh\nREVIEW_COMMAND=native\n\"$REVIEW_COMMAND\" /tmp/x\n",
		"local-quoted-empty-direct":                        "#!/bin/sh\nREVIEW_COMMAND=''\n\"$REVIEW_COMMAND\" /tmp/x\n",
		"local-quoted-native-command-wrapper":              "#!/bin/sh\nREVIEW_COMMAND=native\ncommand \"$REVIEW_COMMAND\" /tmp/x\n",
		"unset-quoted-query-is-data":                       "#!/bin/sh\ncommand -v \"$REVIEW_COMMAND\" >/dev/null || :\n",
		"unset-quoted-ordinary-argument-is-data":           "#!/bin/sh\nprintf '%s' \"$REVIEW_COMMAND\"\n",
		"single-quoted-parameter-syntax-is-literal":        "#!/bin/sh\n'${REVIEW_COMMAND:-native}' /tmp/x\n",
		"escaped-dollar-simple-variable-is-literal":        "#!/bin/sh\n\\$REVIEW_COMMAND /tmp/x\n",
		"single-quoted-command-substitution-is-literal":    "#!/bin/sh\n'$(printf native)' /tmp/x\n",
		"single-quoted-backtick-substitution-is-literal":   "#!/bin/sh\nprintf '%s' '`printf native`'\n",
		"escaped-dollar-command-substitution-is-literal":   "#!/bin/sh\nprintf '%s' \"\\$(printf native)\"\n",
		"escaped-backtick-substitution-is-literal":         "#!/bin/sh\nprintf '%s' \"\\`printf native\\`\"\n",
		"commented-command-substitution-is-inert":          "#!/bin/sh\n# $(printf native) `printf native`\nprintf '%s' native\n",
		"runtime-expansion-query-is-data":                  "#!/bin/sh\ncommand -v \"${REVIEW_COMMAND:-native}\" >/dev/null || :\n",
		"runtime-expansion-ordinary-argument-is-data":      "#!/bin/sh\nprintf '%s' \"${REVIEW_COMMAND:-native}\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			if len(evidence.Findings) != 0 {
				t.Fatalf("clean function-definition control rejected: %s", formatM2Findings(evidence.Findings))
			}
		})
	}

	for name, content := range map[string]string{
		"conditional-function-effect":                 "#!/bin/sh\nreview_retired_cli=native\nhelper(){ if true; then review_retired_cli=sql\\ite3; fi; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"unresolved-function-effect":                  "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=${review_source}; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"recursive-function-effect":                   "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; helper; }\nhelper\n",
		"indirect-function-call":                      "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; }\nselected=helper\n\"$selected\"\n",
		"function-read-effect":                        "#!/bin/sh\nreview_retired_cli=native\nhelper(){ read review_retired_cli; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"opaque-function-effect":                      "#!/bin/sh\nreview_retired_cli=native\nhelper(){ eval \"$review_payload\"; }\nhelper\n",
		"conditional-case-effect":                     "#!/bin/sh\nreview_retired_cli=sql\\ite3\nhelper(){ case native in other) review_retired_cli=native ;; esac; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"false-and-function-effect":                   "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\nfalse && select_native_cli\n\"$review_retired_cli\" /tmp/x\n",
		"or-function-effect":                          "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\ntrue || select_native_cli\n\"$review_retired_cli\" /tmp/x\n",
		"if-branch-function-effect":                   "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\nif false; then select_native_cli; fi\n\"$review_retired_cli\" /tmp/x\n",
		"loop-function-effect":                        "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\nwhile false; do select_native_cli; done\n\"$review_retired_cli\" /tmp/x\n",
		"pipeline-function-effect-is-isolated":        "#!/bin/sh\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\nhelper | true\n\"$review_retired_cli\" /tmp/x\n",
		"background-function-effect-join":             "#!/bin/sh\nreview_retired_cli=sql\\ite3\nselect_native_cli(){ review_retired_cli=native; }\nselect_native_cli & true\n\"$review_retired_cli\" /tmp/x\n",
		"short-circuit-return-keeps-reachability":     "#!/bin/sh\nreview_retired_cli=native\nhelper(){ false && return 0; review_retired_cli=sql\\ite3; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"conditional-overwrite-joins-prior-effect":    "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3; false && review_retired_cli=native; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"generic-sh-time-function-lookup":             "#!/bin/sh\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"missing-shebang-time-function-lookup":        "review_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"unknown-shebang-time-function-lookup":        "#!/opt/review-shell\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"dynamic-env-time-function-lookup":            "#!/usr/bin/env $SHELL\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"env-s-sh-time-function-lookup":               "#!/usr/bin/env -S sh -eu\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"relative-bash-time-function-lookup":          "#!bash\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"malformed-shebang-time-function-lookup":      "#!\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"overbudget-shebang-time-function-lookup":     "#!/bin/bash " + strings.Repeat("x", 4096) + "\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"unsupported-env-option-time-function-lookup": "#!/usr/bin/env --review-unknown bash\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"quoted-env-interpreter-time-function-lookup": "#!/usr/bin/env \"bash\"\nreview_retired_cli=sql\\ite3\nhelper(){ review_retired_cli=native; }\ntime helper\n\"$review_retired_cli\" /tmp/x\n",
		"generic-special-colon-prefix-joins":          "#!/bin/sh\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"missing-shell-special-colon-prefix-joins":    "review_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"dash-opaque-export-prefix":                   "#!/bin/dash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 export REVIEW_OTHER; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"dash-opaque-dot-prefix":                      "#!/bin/dash\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 . ./review-env; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"conditional-set-posix-mode":                  "#!/bin/bash\nfalse && set -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"unknown-runtime-set-option":                  "#!/bin/bash\nset -o review_unknown\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"unknown-shebang-set-option":                  "#!/bin/bash -o review_unknown\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"generic-shell-runtime-set-posix":             "#!/bin/sh\nset -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"unknown-command-set-option":                  "#!/bin/bash\ncommand --review-unknown set -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"dynamic-set-option":                          "#!/bin/bash\nreview_set_option=-o\nset \"$review_set_option\" posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"dynamic-command-target":                      "#!/bin/bash\ncommand \"$review_unknown_command\" set -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-multiword":                  "#!/bin/bash\nreview_command='set -o posix'\ncommand $review_command\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-tab":                        "#!/bin/bash\nreview_command='set\t-o\tposix'\ncommand $review_command\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-newline":                    "#!/bin/bash\nreview_command='set\n-o\nposix'\ncommand $review_command\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-glob":                       "#!/bin/bash\nreview_command='review-*'\ncommand $review_command\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-changed-ifs":                "#!/bin/bash\nIFS=:\nreview_command='set:-o:posix'\ncommand $review_command\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-exported-ifs":               "#!/bin/bash\nexport IFS=:\nreview_command='set:-o:posix'\ncommand $review_command\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-alternate-ifs":              "#!/bin/bash\nexport IFS=,\nreview_command='set,-o,posix'\ncommand $review_command\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-one-word-looking":           "#!/bin/bash\nreview_command=set\ncommand $review_command -o posix\nreview_retired_cli=native\nhelper(){ review_retired_cli=sql\\ite3 :; }\nhelper\n\"$review_retired_cli\" /tmp/x\n",
		"command-unquoted-native-looking":             "#!/bin/bash\nreview_command=native\ncommand $review_command\n",
		"command-unquoted-option-looking":             "#!/bin/bash\nreview_command=-p\ncommand $review_command set -o posix\n",
		"direct-unquoted-one-word-looking":            "#!/bin/sh\nreview_command=native\n$review_command /tmp/x\n",
		"direct-unquoted-glob-looking":                "#!/bin/sh\nreview_command='review-*'\n$review_command /tmp/x\n",
		"direct-unquoted-unresolved":                  "#!/bin/sh\n$review_command /tmp/x\n",
		"env-unquoted-one-word-looking":               "#!/bin/sh\nreview_command=native\nenv $review_command /tmp/x\n",
		"direct-quoted-unset":                         "#!/bin/sh\n\"$REVIEW_COMMAND\" /tmp/x\n",
		"command-quoted-unset":                        "#!/bin/sh\ncommand \"$REVIEW_COMMAND\" /tmp/x\n",
		"env-quoted-unset":                            "#!/bin/sh\nenv \"$REVIEW_COMMAND\" /tmp/x\n",
		"nested-wrapper-quoted-unset":                 "#!/bin/sh\nenv command -- \"$REVIEW_COMMAND\" /tmp/x\n",
		"direct-quoted-locally-unresolved":            "#!/bin/sh\nREVIEW_COMMAND=${REVIEW_SOURCE}\n\"$REVIEW_COMMAND\" /tmp/x\n",
		"parameter-default-direct":                    "#!/bin/sh\n\"${REVIEW_COMMAND:-native}\" /tmp/x\n",
		"parameter-alternate-direct":                  "#!/bin/sh\n\"${REVIEW_COMMAND:+native}\" /tmp/x\n",
		"parameter-assignment-direct":                 "#!/bin/sh\n\"${REVIEW_COMMAND:=native}\" /tmp/x\n",
		"parameter-error-direct":                      "#!/bin/sh\n\"${REVIEW_COMMAND:?required}\" /tmp/x\n",
		"parameter-concatenated-direct":               "#!/bin/sh\n\"${REVIEW_COMMAND_PREFIX}3\" /tmp/x\n",
		"parameter-substring-direct":                  "#!/bin/bash\n\"${REVIEW_COMMAND:0:3}\" /tmp/x\n",
		"parameter-transformation-direct":             "#!/bin/bash\n\"${REVIEW_COMMAND^^}\" /tmp/x\n",
		"parameter-nested-direct":                     "#!/bin/sh\n\"${REVIEW_COMMAND:-${REVIEW_DEFAULT}}\" /tmp/x\n",
		"command-substitution-direct":                 "#!/bin/sh\n\"$(printf native)\" /tmp/x\n",
		"backtick-substitution-direct":                "#!/bin/sh\n\"`printf native`\" /tmp/x\n",
		"command-substitution-argument":               "#!/bin/sh\nREVIEW_COMMAND=sql\\ite3\nprintf '%s\\n' \"$(\"$REVIEW_COMMAND\" /tmp/data.db)\"\n",
		"backtick-substitution-argument":              "#!/bin/sh\nprintf '%s\\n' \"`printf native`\"\n",
		"command-substitution-assignment":             "#!/bin/sh\nreview_value=$(printf native)\nprintf '%s' native\n",
		"backtick-substitution-assignment":            "#!/bin/sh\nreview_value=`printf native`\nprintf '%s' native\n",
		"command-substitution-redirection":            "#!/bin/sh\nprintf '%s' native >\"$(printf native)\"\n",
		"backtick-substitution-redirection":           "#!/bin/sh\nprintf '%s' native >\"`printf native`\"\n",
		"command-substitution-query":                  "#!/bin/sh\nREVIEW_COMMAND=sql\\ite3\ncommand -v \"$(\"$REVIEW_COMMAND\" /tmp/query.db)\"\n",
		"backtick-substitution-query":                 "#!/bin/sh\ncommand -v \"`printf native`\"\n",
		"command-substitution-nested":                 "#!/bin/sh\nprintf '%s' \"$(printf '%s' \"$(printf native)\")\"\n",
		"backtick-substitution-nested":                "#!/bin/sh\nprintf '%s' \"`printf '%s' \\`printf native\\``\"\n",
		"command-substitution-group-scope":            "#!/bin/sh\n{ printf '%s' \"$(printf native)\"; }\n",
		"backtick-substitution-subshell-scope":        "#!/bin/sh\n( printf '%s' \"`printf native`\" )\n",
		"unterminated-command-substitution":           "#!/bin/sh\nprintf '%s' \"$(printf native\"\n",
		"unterminated-backtick-substitution":          "#!/bin/sh\nprintf '%s' \"`printf native\"\n",
		"arithmetic-expansion-direct":                 "#!/bin/sh\n\"$((1 + 2))\" /tmp/x\n",
		"parameter-default-command-wrapper":           "#!/bin/sh\ncommand \"${REVIEW_COMMAND:-native}\" /tmp/x\n",
		"parameter-default-env-wrapper":               "#!/bin/sh\nenv \"${REVIEW_COMMAND:-native}\" /tmp/x\n",
		"parameter-default-nested-wrapper":            "#!/bin/sh\nenv command -- \"${REVIEW_COMMAND:-native}\" /tmp/x\n",
	} {
		t.Run(name+"-fails-closed", func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
		})
	}
	for name, content := range map[string]string{
		"heredoc-command-substitution":                       "#!/bin/bash\nREVIEW_COMMAND=sql\\ite3\ncat <<EOF\n$(\"$REVIEW_COMMAND\" /tmp/heredoc.db)\nEOF\n",
		"heredoc-backtick-substitution":                      "#!/bin/sh\ncat <<EOF\n`printf native`\nEOF\n",
		"heredoc-tab-stripped-command-substitution":          "#!/bin/sh\ncat <<-EOF\n\t$(printf native)\n\tEOF\n",
		"heredoc-multiple-second-expands":                    "#!/bin/sh\ncat <<'FIRST' <<SECOND\n$(printf literal)\nFIRST\n$(printf native)\nSECOND\n",
		"heredoc-nested-command-substitution":                "#!/bin/sh\ncat <<EOF\n$(printf '%s' \"$(printf native)\")\nEOF\n",
		"heredoc-single-quotes-do-not-disable-expansion":     "#!/bin/sh\ncat <<EOF\n'$(printf native)'\nEOF\n",
		"heredoc-comment-text-does-not-disable-expansion":    "#!/bin/sh\ncat <<EOF\n# $(printf native)\nEOF\n",
		"heredoc-substitution-precedes-query-classification": "#!/bin/sh\ncommand -v native <<EOF\n$(printf native)\nEOF\n",
		"heredoc-unterminated-command-substitution":          "#!/bin/sh\ncat <<EOF\n$(printf native\nEOF\n",
		"heredoc-unterminated-backtick-substitution":         "#!/bin/sh\ncat <<EOF\n`printf native\nEOF\n",
		"heredoc-lf-continuation":                            "#!/bin/bash\nREVIEW_COMMAND=sql\\ite3\ncat <<EOF\n$\\\n(\"$REVIEW_COMMAND\" /tmp/continued.db)\nEOF\n",
		"heredoc-crlf-continuation":                          "#!/bin/sh\r\ncat <<EOF\r\n$\\\r\n(printf native)\r\nEOF\r\n",
		"heredoc-tab-stripped-continuation":                  "#!/bin/sh\ncat <<-EOF\n\t$\\\n\t(printf native)\n\tEOF\n",
		"heredoc-multiple-continuations":                     "#!/bin/sh\ncat <<EOF\nfirst\\\nsecond\\\nthird\nEOF\n",
		"heredoc-continuation-before-dollar-substitution":    "#!/bin/sh\ncat <<EOF\nprefix\\\n$(printf native)\nEOF\n",
		"heredoc-continuation-before-backtick-substitution":  "#!/bin/sh\ncat <<EOF\nprefix\\\n`printf native`\nEOF\n",
		"heredoc-continuation-in-ordinary-data":              "#!/bin/sh\ncat <<EOF\nnative\\\ndata\nEOF\n",
		"heredoc-second-body-continuation":                   "#!/bin/sh\ncat <<'FIRST' <<SECOND\nliteral\\\ndata\nFIRST\nactive\\\ndata\nSECOND\n",
		"heredoc-declaration-continuation-after":             "#!/bin/sh\ncat <<EOF \\\n; sql\\ite3 /tmp/continued-command.db\ndata\nEOF\n",
		"heredoc-declaration-continuation-before":            "#!/bin/sh\ncat \\\n<<EOF\ndata\nEOF\n",
		"heredoc-declaration-continuation-inside-delimiter":  "#!/bin/sh\ncat <<EO\\\nF\ndata\nEOF\n",
		"heredoc-multiple-declaration-continuation":          "#!/bin/sh\ncat <<FIRST <<SECOND \\\n; sql\\ite3 /tmp/continued-command.db\none\nFIRST\ntwo\nSECOND\n",
		"heredoc-between-multiple-declaration-continuation":  "#!/bin/sh\ncat <<FIRST \\\n<<SECOND\none\nFIRST\ntwo\nSECOND\n",
		"heredoc-tab-stripped-declaration-continuation":      "#!/bin/sh\ncat <<-EOF \\\n; sql\\ite3 /tmp/continued-command.db\n\tdata\n\tEOF\n",
		"heredoc-crlf-declaration-continuation":              "#!/bin/sh\r\ncat <<EOF \\\r\n; sql\\ite3 /tmp/continued-command.db\r\ndata\r\nEOF\r\n",
		"heredoc-quoted-declaration-continuation":            "#!/bin/sh\ncat <<'EOF' \\\n; sql\\ite3 /tmp/continued-command.db\ndata\nEOF\n",
		"heredoc-escaped-delimiter-declaration-continuation": "#!/bin/sh\ncat <<\\EOF \\\n; sql\\ite3 /tmp/continued-command.db\ndata\nEOF\n",
		"heredoc-split-operator-lf":                          "#!/bin/sh\ncat <\\\n<EOF\nsql\\ite3 --not-executed data\nEOF\nprintf '%s\\n' native\n",
		"heredoc-split-operator-crlf":                        "#!/bin/sh\r\ncat <\\\r\n<EOF\r\nsql\\ite3 --not-executed data\r\nEOF\r\nprintf '%s\\n' native\r\n",
		"heredoc-split-operator-tab-stripped":                "#!/bin/sh\ncat <\\\n<-EOF\n\tsql\\ite3 --not-executed data\n\tEOF\nprintf '%s\\n' native\n",
		"heredoc-split-operator-quoted-delimiter":            "#!/bin/sh\ncat <\\\n<'EOF'\nsql\\ite3 --not-executed data\nEOF\nprintf '%s\\n' native\n",
		"heredoc-split-operator-multiple":                    "#!/bin/sh\ncat <\\\n<ONE <\\\n<TWO\none\nONE\nsql\\ite3 --not-executed data\nTWO\nprintf '%s\\n' native\n",
		"heredoc-split-second-operator":                      "#!/bin/sh\ncat <<ONE <\\\n<TWO\none\nONE\nsql\\ite3 --not-executed data\nTWO\nprintf '%s\\n' native\n",
		"heredoc-split-operator-leading-whitespace":          "#!/bin/sh\ncat   <\\\n<EOF\nsql\\ite3 --not-executed data\nEOF\nprintf '%s\\n' native\n",
	} {
		t.Run(name+"-fails-closed", func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
		})
	}

	t.Run("environment-provided-direct-quoted-target-fails-closed", func(t *testing.T) {
		t.Setenv("REVIEW_COMMAND", m2LegacyCLI)
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/bash\n\"$REVIEW_COMMAND\" /tmp/x\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
	})

	for name, test := range map[string]struct {
		environmentName, environmentValue, source string
	}{
		"environment-default-parameter-fails-closed":   {"REVIEW_COMMAND", m2LegacyCLI, "#!/bin/bash\n\"${REVIEW_COMMAND:-native}\" /tmp/x\n"},
		"environment-concatenated-prefix-fails-closed": {"REVIEW_COMMAND_PREFIX", m2LegacyWord, "#!/bin/bash\n\"${REVIEW_COMMAND_PREFIX}3\" /tmp/x\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(test.environmentName, test.environmentValue)
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(test.source))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
		})
	}

	for name, content := range map[string]string{
		"bash-current-shell-output-redirect":    "#!/bin/bash\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } &>/tmp/x\n\"$review_retired_cli\" /tmp/x\n",
		"bash-current-shell-append-redirect":    "#!/bin/bash\nreview_retired_cli=sql\\ite3\n{ review_retired_cli=native; } &>>/tmp/x\n\"$review_retired_cli\" /tmp/x\n",
		"bash-background-after-output-redirect": "#!/bin/bash\nreview_retired_cli=native\n{ review_retired_cli=sql\\ite3; } &>/tmp/x & \"$review_retired_cli\" /tmp/x\n",
		"bash-background-after-append-redirect": "#!/bin/bash\nreview_retired_cli=native\n{ review_retired_cli=sql\\ite3; } &>>/tmp/x & \"$review_retired_cli\" /tmp/x\n",
		"heredoc-unquoted-body-is-data":         "#!/bin/sh\ncat <<EOF\n" + m2LegacyCLI + " --not-executed; '$unterminated\nEOF\nprintf '%s' native\n",
		"heredoc-quoted-body-is-data":           "#!/bin/sh\ncat <<'EOF'\n" + m2ModerncDriver + "\n$(" + m2LegacyCLI + ")\nEOF\nprintf '%s' native\n",
		"heredoc-tab-stripped-body-is-data":     "#!/bin/sh\ncat <<-'EOF'\n\t" + m2LegacyCLI + " | unexpected && punctuation\n\tEOF\nprintf '%s' native\n",
		"multiple-heredoc-bodies-are-data":      "#!/bin/sh\ncat <<FIRST <<'SECOND'\n" + m2LegacyCLI + "\nFIRST\n" + m2ModerncDriver + "\nSECOND\nprintf '%s' native\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			if len(evidence.Findings) != 0 {
				t.Fatalf("clean function/redirection/heredoc control rejected: %s", formatM2Findings(evidence.Findings))
			}
		})
	}
	for name, content := range map[string]string{
		"heredoc-double-quoted-body-is-data":                   "#!/bin/sh\ncat <<\"EOF\"\n$(printf native) `printf native`\nEOF\nprintf '%s' native\n",
		"heredoc-escaped-delimiter-body-is-data":               "#!/bin/sh\ncat <<\\EOF\n$(printf native) `printf native`\nEOF\nprintf '%s' native\n",
		"heredoc-unquoted-escaped-substitution-is-data":        "#!/bin/sh\ncat <<EOF\n\\$(printf native) \\`printf native\\`\nEOF\nprintf '%s' native\n",
		"heredoc-quoted-delimiter-continuation-is-data":        "#!/bin/sh\ncat <<'EOF'\n$\\\n(printf native)\nEOF\n",
		"heredoc-escaped-delimiter-crlf-continuation-is-data":  "#!/bin/sh\r\ncat <<\\EOF\r\n$\\\r\n(printf native)\r\nEOF\r\n",
		"heredoc-terminated-declaration-is-clean":              "#!/bin/sh\ncat <<EOF\ndata\nEOF\nprintf '%s' native\n",
		"heredoc-terminated-multiple-declarations-are-clean":   "#!/bin/sh\ncat <<FIRST <<'SECOND'\none\nFIRST\ntwo\nSECOND\nprintf '%s' native\n",
		"heredoc-terminated-tab-stripped-declaration-is-clean": "#!/bin/sh\ncat <<-EOF\n\tdata\n\tEOF\nprintf '%s' native\n",
		"heredoc-terminated-quoted-declaration-is-clean":       "#!/bin/sh\ncat <<'EOF'\n$\\\n(printf native)\nEOF\nprintf '%s' native\n",
		"heredoc-split-marker-single-quoted-is-data":           "#!/bin/sh\nprintf '%s' '<\\\n<EOF'\n",
		"heredoc-split-marker-double-quoted-is-data":           "#!/bin/sh\nprintf '%s' \"<\\\n<EOF\"\n",
		"heredoc-split-marker-comment-is-data":                 "#!/bin/sh\n# <\\\n<EOF\nprintf '%s' native\n",
		"heredoc-split-marker-escaped-is-data":                 "#!/bin/sh\nprintf '%s' \\<\\\\\n\\<EOF\n",
		"heredoc-split-operator-near-miss-is-redirection":      "#!/bin/sh\ncat <\\\ninput.txt\nprintf '%s' native\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			if len(evidence.Findings) != 0 {
				t.Fatalf("clean heredoc expansion control rejected: %s", formatM2Findings(evidence.Findings))
			}
		})
	}

	for name, content := range map[string]string{
		"forbidden-command-before-heredoc": "#!/bin/sh\nsql\\ite3 /tmp/x; cat <<EOF\ndata\nEOF\n",
		"forbidden-command-after-heredoc":  "#!/bin/sh\ncat <<EOF\ndata\nEOF\nsql\\ite3 /tmp/x\n",
		"quoted-heredoc-marker-is-data":    "#!/bin/sh\nprintf '%s' '<<EOF'\nsql\\ite3 /tmp/x\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "subprocess", "scripts/x.sh")
		})
	}

	for name, content := range map[string]string{
		"unterminated-heredoc": "#!/bin/sh\ncat <<EOF\ndata\n",
		"dynamic-heredoc":      "#!/bin/sh\nmarker=EOF\ncat <<\"$marker\"\ndata\nEOF\n",
	} {
		t.Run(name+"-fails-closed", func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
		})
	}

	for name, content := range map[string]string{
		"assignment-only-persists-at-boundary": "#!/bin/sh\nreview_retired_cli=sql\\ite3\nreview_retired_cli=native\n\"$review_retired_cli\" /tmp/x\n",
		"direct-prefix-expands-clean-prior":    "#!/bin/sh\nreview_retired_cli=native\nreview_retired_cli=sql\\ite3 \"$review_retired_cli\" /tmp/x\n",
		"wrapped-prefix-expands-clean-prior":   "#!/bin/sh\nreview_retired_cli=native\nenv review_retired_cli=sql\\ite3 \"$review_retired_cli\" /tmp/x\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeM2Mutant(t, root, "scripts/x.sh", []byte(content))
			evidence := inspectM2Absence(t, root, false)
			if len(evidence.Findings) != 0 {
				t.Fatalf("clean assignment-expansion control rejected: %s", formatM2Findings(evidence.Findings))
			}
		})
	}

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

	t.Run("command-substitution-in-uninvoked-assignment-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		writeM2Mutant(t, root, "scripts/x.sh", []byte("#!/bin/sh\nreview_retired_cli=$(printf '%s%s' sql ite3)\nprintf '%s' native\n"))
		evidence := inspectM2Absence(t, root, false)
		assertM2Finding(t, evidence.Findings, "ambiguous-shell", "scripts/x.sh")
	})

	for name, content := range map[string]string{
		"unterminated-single-quote":     "#!/bin/sh\nvalue='unterminated\n",
		"unterminated-double-quote":     "#!/bin/sh\nvalue=\"unterminated\n",
		"unterminated-escape":           "#!/bin/sh\nvalue=native\\",
		"unsupported-backtick":          "#!/bin/sh\nvalue=`command`\n",
		"unsupported-compound-redirect": "#!/bin/sh\n{ value=native; } 2>< /tmp/x | true\n",
		"dynamic-compound-fd-redirect":  "#!/bin/sh\nfd=1\n{ value=native; } 2>&\"$fd\" | true\n",
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
	root := m2RepositoryRoot(t)
	command := exec.Command(filepath.Join(root, "scripts", "m2-absence", "mutants.sh"), "--layers-json")
	command.Dir = m2RepositoryRoot(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("layer detector mutants: %v\n%s", err, output)
	}
	result := m2VerifyObservedReceipts(t, filepath.Join(root, "scripts", "m2-absence", "expected-layer-mutants.txt"), output)
	if result.Verdict != "pass" || result.Counts.Expected != 8 || result.Counts.Passed != 8 {
		t.Fatalf("layer receipt census rejected: %+v", result)
	}
}

func TestM2ObservedReceiptVerifier(t *testing.T) {
	expected := filepath.Join(t.TempDir(), "expected.txt")
	if err := os.WriteFile(expected, []byte("Receipt/A\nReceipt/B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, events := range map[string]string{
		"missing":       `{"Action":"run","Test":"Receipt/A"}` + "\n" + `{"Action":"pass","Test":"Receipt/A"}` + "\n",
		"renamed":       `{"Action":"run","Test":"Receipt/A"}` + "\n" + `{"Action":"pass","Test":"Receipt/A"}` + "\n" + `{"Action":"run","Test":"Receipt/C"}` + "\n" + `{"Action":"pass","Test":"Receipt/C"}` + "\n",
		"skipped":       `{"Action":"run","Test":"Receipt/A"}` + "\n" + `{"Action":"pass","Test":"Receipt/A"}` + "\n" + `{"Action":"run","Test":"Receipt/B"}` + "\n" + `{"Action":"skip","Test":"Receipt/B"}` + "\n",
		"extra":         `{"Action":"run","Test":"Receipt/A"}` + "\n" + `{"Action":"pass","Test":"Receipt/A"}` + "\n" + `{"Action":"run","Test":"Receipt/B"}` + "\n" + `{"Action":"pass","Test":"Receipt/B"}` + "\n" + `{"Action":"run","Test":"Receipt/C"}` + "\n" + `{"Action":"pass","Test":"Receipt/C"}` + "\n",
		"duplicate":     `{"Action":"run","Test":"Receipt/A"}` + "\n" + `{"Action":"run","Test":"Receipt/A"}` + "\n" + `{"Action":"pass","Test":"Receipt/A"}` + "\n" + `{"Action":"run","Test":"Receipt/B"}` + "\n" + `{"Action":"pass","Test":"Receipt/B"}` + "\n",
		"claimed-count": `{"Action":"run","Test":"Receipt/A"}` + "\n" + `{"Action":"pass","Test":"Receipt/A"}` + "\n" + `{"mutants":2,"status":"pass"}` + "\n",
	} {
		t.Run(name+"-fails", func(t *testing.T) {
			if result := m2VerifyObservedReceipts(t, expected, []byte(events)); result.Verdict != "fail" {
				t.Fatalf("receipt substitution unexpectedly passed: %+v", result)
			}
		})
	}
	passing := `{"Action":"run","Test":"Receipt/A"}` + "\n" + `{"Action":"pass","Test":"Receipt/A"}` + "\n" + `{"Action":"run","Test":"Receipt/B"}` + "\n" + `{"Action":"pass","Test":"Receipt/B"}` + "\n"
	if result := m2VerifyObservedReceipts(t, expected, []byte(passing)); result.Verdict != "pass" || result.Counts.Expected != 2 || result.Counts.Passed != 2 {
		t.Fatalf("complete observed receipt census rejected: %+v", result)
	}
}

type m2ReceiptResult struct {
	Verdict string `json:"verdict"`
	Counts  struct {
		Expected int `json:"expected"`
		Passed   int `json:"passed"`
	} `json:"counts"`
}

func m2VerifyObservedReceipts(t *testing.T, expectedPath string, events []byte) m2ReceiptResult {
	t.Helper()
	rawPath := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(rawPath, events, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("jq", "-s", "-S", "--rawfile", "expected_text", expectedPath, "--arg", "receipt_kind", "test", "--arg", "raw_sha", "fixture", "-f", filepath.Join(m2RepositoryRoot(t), "scripts", "m2-absence", "receipt-result.jq"), rawPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("derive observed receipts: %v\n%s", err, output)
	}
	var result m2ReceiptResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode observed receipt result: %v\n%s", err, output)
	}
	return result
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
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SPEC_KITTY_WP15_GAPDBD=") && !strings.HasPrefix(entry, "SPEC_KITTY_M2_GO_VERSION=") {
			environment = append(environment, entry)
		}
	}
	return append(environment, "SPEC_KITTY_WP15_GAPDBD="+sidecar, "SPEC_KITTY_M2_GO_VERSION=go1.26.7")
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
