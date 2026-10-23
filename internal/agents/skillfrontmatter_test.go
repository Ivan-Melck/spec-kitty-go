package agents

import (
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
	"strings"
	"testing"
)

func TestCommandInvocationBoundaries(t *testing.T) {
	input := "/spec-kitty.plan $spec-kitty.tasks-outline /skill:spec-kitty.review. commands/spec-kitty.plan.md https://x/spec-kitty.plan foo$spec-kitty.plan /spec-kitty.plan.md /spec-kitty.ai /spec-kitty.analysis-report /spec-kitty.tasks-outline-extra"
	want := "/spec-kitty-plan $spec-kitty-tasks-outline /skill:spec-kitty-review. commands/spec-kitty.plan.md https://x/spec-kitty.plan foo$spec-kitty.plan /spec-kitty.plan.md /spec-kitty.ai /spec-kitty.analysis-report /spec-kitty.tasks-outline-extra"
	if got := RewriteCommandInvocations(input); got != want {
		t.Fatalf("rewrite=%q", got)
	}
	if ParseCommandSkillPath(".agents/skills/spec-kitty-implement-review/SKILL.md", false) != "" {
		t.Fatal("charter skill parsed as command")
	}
	if ParseCommandSkillPath(CommandSkillPath("review"), false) != "review" || ParseCommandSkillPath(LegacyCommandSkillPath("review"), true) != "review" {
		t.Fatal("exact paths not recognized")
	}
}

func TestSkillProjectionByRoot(t *testing.T) {
	source := "---\nname: example\ndescription: Example skill\n# caller comment\nuser-invocable: true\nargument-hint: '<id>'\ncustom:\n- one\nmetadata:\n  owner: team\n  user-invocable: false\n---\n# Body\n\nText.\n"
	shared, err := ProjectSkillForRoot(source, ".agents/skills", "example")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shared, "\nuser-invocable:") || !strings.Contains(shared, "  user-invocable: \"false\"") || !strings.Contains(shared, "  custom: \"[\\\"one\\\"]\"") {
		t.Fatalf("shared projection:\n%s", shared)
	}
	for _, root := range []string{".agents/skills", ".github/skills", "unknown/skills"} {
		got, e := ProjectSkillForRoot(shared, root, "example")
		if e != nil || got != shared {
			t.Fatalf("projection not idempotent root=%s %v\n%s", root, e, got)
		}
	}
	native, err := ProjectSkillForRoot(shared, ".claude/skills", "example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(native, "\nuser-invocable: false\n") || !strings.Contains(native, "\nargument-hint: \"<id>\"\n") || !strings.HasSuffix(native, "# Body\n\nText.\n") {
		t.Fatalf("native projection:\n%s", native)
	}
	if got, e := ProjectSkillForRoot(native, ".claude/skills", "example"); e != nil || got != native {
		t.Fatalf("native idempotence=%v\n%s", e, got)
	}
}

func TestSkillScalarControlRoundTrip(t *testing.T) {
	for _, value := range []string{"line\nnext", "tab\tvalue", "\x00control", "\u0085NEL", "\u2028separator", "true", "a: b"} {
		scalar := skillYAMLScalar(value)
		obj, err := yamlx.ParseObject([]byte("value: " + scalar + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if obj.GetString("value") != value {
			t.Fatalf("%q -> %q -> %q", value, scalar, obj.GetString("value"))
		}
	}
}
