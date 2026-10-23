// Package languagecatalog is the shared language vocabulary for detection,
// declarations, configured values, and family-aware doctrine matching.
package languagecatalog

import "strings"

type Spec struct {
	Token                                     string
	Extensions, Manifests, Patterns, Families []string
	DeclaredRank                              int
}

var jvmManifests = []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradlew", "mvnw"}
var Languages = []Spec{
	{"python", []string{".py"}, []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt"}, []string{`\bpython\b`, `\bpytest\b`, `\bmypy\b`, `\bruff\b`}, nil, 0},
	{"go", []string{".go"}, []string{"go.mod"}, nil, nil, 9},
	{"rust", []string{".rs"}, []string{"Cargo.toml"}, []string{`\brust\b`, `\bcargo\b`, `\brustc\b`}, nil, 3},
	{"java", []string{".java"}, jvmManifests, []string{`\bjava\b`}, []string{"jvm"}, 4},
	{"kotlin", []string{".kt"}, jvmManifests, []string{`\bkotlin\b`}, []string{"jvm"}, 5},
	{"ruby", []string{".rb"}, []string{"Gemfile"}, []string{`\bruby\b`, `\brspec\b`, `\brails\b`}, nil, 7},
	{"php", []string{".php"}, []string{"composer.json"}, []string{`\bphp\b`, `\bphpunit\b`}, nil, 8},
	{"javascript", []string{".js", ".jsx", ".mjs"}, []string{"package.json"}, []string{`\bjavascript\b`, `\bjest\b`, `\bnode(?:\.js)?\b`, `\bnpm\b`, `\bpnpm\b`, `\byarn\b`}, nil, 2},
	{"typescript", []string{".ts", ".tsx"}, nil, []string{`\btypescript\b`, `\btsc\b`}, nil, 1},
	{"swift", []string{".swift"}, nil, []string{`\bswift\b`, `\bxctest\b`}, nil, 6},
}

func Tokens() []string {
	out := make([]string, len(Languages))
	for i, s := range Languages {
		out[i] = s.Token
	}
	return out
}
func IsLanguage(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	for _, s := range Languages {
		if s.Token == token {
			return true
		}
	}
	return false
}
func IsFamily(token string) bool { return strings.ToLower(strings.TrimSpace(token)) == "jvm" }
func Expand(tokens []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range tokens {
		token := strings.ToLower(strings.TrimSpace(raw))
		items := []string{token}
		for _, spec := range Languages {
			if spec.Token == token {
				items = append(items, spec.Families...)
			}
		}
		for _, item := range items {
			if item != "" && !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
	}
	return out
}
func ForExtension(extension string) string {
	for _, s := range Languages {
		for _, e := range s.Extensions {
			if e == extension {
				return s.Token
			}
		}
	}
	return ""
}
func ForManifest(name string) []string {
	var out []string
	for _, s := range Languages {
		for _, n := range s.Manifests {
			if n == name {
				out = append(out, s.Token)
				break
			}
		}
	}
	return out
}
