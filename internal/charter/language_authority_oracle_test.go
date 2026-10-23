package charter

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func languageOraclePython(t *testing.T) string {
	t.Helper()
	file, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	line, _ := bufio.NewReader(file).ReadString('\n')
	return strings.TrimSpace(strings.TrimPrefix(line, "#!"))
}
func TestLanguageAuthorityMatchesOracle(t *testing.T) {
	python := languageOraclePython(t)
	for _, scenario := range []string{"polyglot", "nested", "kotlin", "configured", "declared"} {
		t.Run(scenario, func(t *testing.T) {
			pair := testkit.NewPair(t)
			pair.Both(func(s *testkit.Sandbox) {
				switch scenario {
				case "polyglot":
					s.WriteFile("pyproject.toml", "")
					s.WriteFile("package.json", "{}")
					for _, name := range []string{"one.py", "two.py", "three.py", "ui.ts", "api.ts"} {
						s.WriteFile("src/"+name, "")
					}
				case "nested":
					s.WriteFile("go.mod", "module demo\n")
					s.WriteFile("web/package.json", "{}")
					s.WriteFile("web/tsconfig.json", "{}")
					s.WriteFile("main.go", "")
					s.WriteFile("web/src/ui.ts", "")
				case "kotlin":
					s.WriteFile("build.gradle.kts", "")
					s.WriteFile("src/main/kotlin/app/Main.kt", "")
					s.WriteFile("src/main/java/app/Old.java", "")
					s.WriteFile("src/main/kotlin/app/Other.kt", "")
				case "configured":
					s.WriteFile("pyproject.toml", "")
					s.WriteFile(".kittify/config.yaml", "languages: [KOTLIN, java, kotlin, invalid, 3]\n")
				}
				s.WriteFile(".kittify/missions/python_helpers.py", "")
			})
			declarations := []string{"Gradle", "Kotlin, Gradle", "Java-based, JUnit", "librespot-java", "Python 3.12 and Rust", "go", "TypeScript, Node.js", "Maven, Swift", "Kotlin and Java"}
			var declared []any
			for _, text := range declarations {
				declared = append(declared, strs(ExtractDeclaredLanguages(text)))
			}
			code := CollectCodeSignals(pair.Go.Dir)
			got := jsonx.Dumps(jsonx.NewObject("primary", code.PrimaryLanguage, "languages", strs(code.Languages), "declarations", declared), jsonx.SortedPretty) + "\n"
			script := "import json\nfrom pathlib import Path\nfrom charter.activation.evidence.code_reader import CodeReadingCollector\nfrom charter.activation.language_authority import declared_languages\nc=CodeReadingCollector(Path.cwd()).collect()\nprint(json.dumps(dict(primary=c.primary_language,languages=c.languages,declarations=[declared_languages(s) for s in " + jsonx.Dumps(strs(declarations), jsonx.Line) + "]),indent=2,sort_keys=True))\n"
			oracle := pair.Oracle.Exec("", python, "-c", script)
			if oracle.Code != 0 {
				t.Fatalf("oracle: %s", oracle.Stderr)
			}
			if got != oracle.Stdout {
				t.Errorf("%s", testkit.Diff(oracle.Stdout, got))
			}
		})
	}
}
