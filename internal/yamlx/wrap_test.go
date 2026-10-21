package yamlx

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Expected output produced by ruamel.yaml 0.19.1 (YAML() round-trip,
// default_flow_style=False) for the same document.
func TestWidthFoldsLikeRuamel(t *testing.T) {
	doc := jsonx.NewObject(
		"answers", jsonx.NewObject(
			"project_intent", "Deliver predictable changes with clear reviewability.",
			"languages_frameworks", "Use only the languages, frameworks, and tools explicitly declared by this project.",
			"testing_requirements", "Use the project's declared test approach. If none is declared, mark it as NEEDS CLARIFICATION.",
			"quality_gates", "Only require the quality gates explicitly declared by this project before merge.",
			"performance_targets", "CLI operations should complete quickly (typically under 2 seconds).",
			"a_very_long_word", "x Supercalifragilisticexpialidocious-and-a-very-long-token-that-exceeds-the-line-width-entirely ok",
			"colon", "key: value pairs inside a long string that must be quoted because of the colon space sequence here",
			"trailing", "ends with a colon and is long enough to be folded over multiple lines of output text here:",
		),
		"list", []any{"a list item that is quite long and should be folded by the emitter when it passes the width", "short"},
		"nested", []any{jsonx.NewObject("name", "a mapping inside a sequence with a long value that needs to be folded at the width limit", "x", 1)},
	)
	want := "answers:\n" +
		"  project_intent: Deliver predictable changes with clear reviewability.\n" +
		"  languages_frameworks: Use only the languages, frameworks, and tools explicitly\n" +
		"    declared by this project.\n" +
		"  testing_requirements: Use the project's declared test approach. If none is \n" +
		"    declared, mark it as NEEDS CLARIFICATION.\n" +
		"  quality_gates: Only require the quality gates explicitly declared by this \n" +
		"    project before merge.\n" +
		"  performance_targets: CLI operations should complete quickly (typically under 2\n" +
		"    seconds).\n" +
		"  a_very_long_word: x \n" +
		"    Supercalifragilisticexpialidocious-and-a-very-long-token-that-exceeds-the-line-width-entirely\n" +
		"    ok\n" +
		"  colon: 'key: value pairs inside a long string that must be quoted because of the\n" +
		"    colon space sequence here'\n" +
		"  trailing: 'ends with a colon and is long enough to be folded over multiple lines\n" +
		"    of output text here:'\n" +
		"list:\n" +
		"- a list item that is quite long and should be folded by the emitter when it \n" +
		"  passes the width\n" +
		"- short\n" +
		"nested:\n" +
		"- name: a mapping inside a sequence with a long value that needs to be folded at\n" +
		"    the width limit\n" +
		"  x: 1\n"
	if got := EmitWith(doc, Options{Width: 80}); got != want {
		t.Errorf("mismatch:\n--- got\n%s--- want\n%s", got, want)
	}
	// Width 0 keeps the historical unfolded output.
	if got := Emit(jsonx.NewObject("k", "Use only the languages, frameworks, and tools explicitly declared by this project.")); got != "k: Use only the languages, frameworks, and tools explicitly declared by this project.\n" {
		t.Errorf("unfolded: %q", got)
	}
}
