// Code generated from upstream ae97982033 bulk_edit/diff_check.py tables. DO NOT EDIT.
package bulkedit

import "regexp"

var pathRules = []struct {
	category string
	pattern  *regexp.Regexp
}{
	{"tests_fixtures", regexp.MustCompile("(^|/)tests?/|(^|/)testing/|(^|/)fixtures?/|(^|/)__snapshots__/|(^|/)__tests__/|(^|/)conftest\\.py$|_test\\.py$|\\.test\\.(ts|tsx|js|jsx)$|(^|/)test_[^/]+\\.py$")},
	{"cli_commands", regexp.MustCompile("(^|/)cli/commands/|(^|/)commands/[^/]+\\.py$|(^|/)bin/[^/]+$")},
	{"user_facing_strings", regexp.MustCompile("\\.md$|(^|/)docs?/|(^|/)README(\\.[^/]+)?$|(^|/)CHANGELOG(\\.[^/]+)?$|\\.rst$|\\.txt$|(^|/)LICENSE(\\.[^/]+)?$")},
	{"filesystem_paths", regexp.MustCompile("(^|/)Dockerfile$|(^|/)Dockerfile\\.[^/]+$|(^|/)Containerfile$|(^|/)docker-compose(\\.[^/]+)?\\.ya?ml$|(^|/)compose(\\.[^/]+)?\\.ya?ml$")},
	{"code_symbols", regexp.MustCompile("\\.erb$|\\.haml$|\\.slim$|\\.jbuilder$|\\.heex$|\\.eex$|\\.blade\\.php$|\\.vue$|\\.svelte$|\\.astro$|\\.css$|\\.scss$|\\.sass$|\\.less$|\\.rake$|\\.gemspec$|(^|/)Rakefile$|(^|/)Gemfile$")},
	{"serialized_keys", regexp.MustCompile("\\.ya?ml$|\\.json$|\\.toml$|\\.ini$|\\.cfg$")},
	{"code_symbols", regexp.MustCompile("\\.py$|\\.pyi$|\\.ts$|\\.tsx$|\\.js$|\\.jsx$|\\.mjs$|\\.cjs$|\\.go$|\\.rs$|\\.java$|\\.rb$|\\.kt$|\\.swift$|\\.c$|\\.cc$|\\.cpp$|\\.h$|\\.hpp$")},
}
var opaqueSuffixes = map[string]bool{
	".avif":  true,
	".bmp":   true,
	".class": true,
	".dll":   true,
	".dylib": true,
	".eot":   true,
	".flac":  true,
	".gif":   true,
	".ico":   true,
	".jar":   true,
	".jpeg":  true,
	".jpg":   true,
	".mov":   true,
	".mp3":   true,
	".mp4":   true,
	".ogg":   true,
	".otf":   true,
	".png":   true,
	".pyc":   true,
	".pyo":   true,
	".so":    true,
	".ttf":   true,
	".wasm":  true,
	".wav":   true,
	".webm":  true,
	".webp":  true,
	".woff":  true,
	".woff2": true,
}
var lockfileNames = map[string]bool{
	"Cargo.lock":        true,
	"Gemfile.lock":      true,
	"Pipfile.lock":      true,
	"bun.lockb":         true,
	"composer.lock":     true,
	"flake.lock":        true,
	"go.sum":            true,
	"mix.lock":          true,
	"package-lock.json": true,
	"pnpm-lock.yaml":    true,
	"poetry.lock":       true,
	"uv.lock":           true,
	"yarn.lock":         true,
}
var placeholderNames = map[string]bool{
	".gitkeep": true,
	".keep":    true,
}
