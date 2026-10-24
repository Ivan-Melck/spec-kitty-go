// Code generated from upstream v4.0.0rc7 (1b0fe9fc38) bulk_edit/diff_check.py. DO NOT EDIT.
package bulkedit

import "regexp"

var pathRules = []struct {
	category string
	pattern  *regexp.Regexp
}{
	{"tests_fixtures", regexp.MustCompile("(^|/)tests?/|(^|/)testing/|(^|/)fixtures?/|(^|/)__snapshots__/|(^|/)__tests__/|(^|/)conftest\\.py$|_test\\.py$|\\.test\\.(ts|tsx|js|jsx)$|(^|/)test_[^/]+\\.py$")},
	{"cli_commands", regexp.MustCompile("(^|/)cli/commands/|(^|/)commands/[^/]+\\.py$|(^|/)bin/[^/]+$")},
	{"user_facing_strings", regexp.MustCompile("\\.md$|(^|/)docs?/|(^|/)README(\\.[^/]+)?$|(^|/)CHANGELOG(\\.[^/]+)?$|\\.rst$|\\.txt$|(^|/)LICENSE(\\.[^/]+)?$")},
	{"serialized_keys", regexp.MustCompile("\\.ya?ml$|\\.json$|\\.toml$|\\.ini$|\\.cfg$")},
	{"code_symbols", regexp.MustCompile("\\.py$|\\.pyi$|\\.ts$|\\.tsx$|\\.js$|\\.jsx$|\\.mjs$|\\.cjs$|\\.go$|\\.rs$|\\.java$|\\.rb$|\\.kt$|\\.swift$|\\.c$|\\.cc$|\\.cpp$|\\.h$|\\.hpp$")},
}
