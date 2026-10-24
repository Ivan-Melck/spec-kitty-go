package bulkedit

import (
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"strings"
)

const FinalizeBlockedMessage = "Bulk edit occurrence-map gate blocked finalize-tasks."

func FailurePanel(r GateResult) *rich.RichPanel {
	var lines []string
	for _, err := range r.Errors {
		lines = append(lines, "  • "+err)
	}
	return &rich.RichPanel{R: strings.Join(lines, "\n"), Title: "[bold red]Bulk Edit Gate: BLOCKED[/]", Subtitle: "Create or fix occurrence_map.yaml before proceeding", BorderStyle: "red"}
}

func RenderDiffFailure(r DiffResult, print func(any)) {
	table := &rich.BoxTable{Title: "Bulk Edit Review: Diff Compliance", Headers: []string{"File", "Category", "Action", "Verdict"}, NoWrap: []bool{false, true, true, true}, Overflow: []string{"fold"}}
	for _, a := range r.Assessments {
		category, action := a.Category, a.Action
		if category == "" {
			category = "(unclassified)"
		}
		if action == "" {
			action = "(none)"
		}
		verdict := "[green]ok[/]"
		if a.Violation {
			verdict = "[bold red]BLOCK[/]"
		} else if a.Action == "manual_review" {
			verdict = "[yellow]review[/]"
		}
		table.Rows = append(table.Rows, []string{a.Path, category, action, verdict})
	}
	print(table.Table())
	if len(r.Errors) > 0 {
		var lines []string
		for _, err := range r.Errors {
			lines = append(lines, "  • "+err)
		}
		print(&rich.RichPanel{R: strings.Join(lines, "\n"), Title: "[bold red]Review Rejected: Diff Compliance Violations[/]", Subtitle: "FR-007/FR-008 — forbidden or unclassified surfaces touched", BorderStyle: "red"})
	}
}

func RenderDiffWarnings(r DiffResult, print func(any)) {
	for _, warning := range r.Warnings {
		print("[yellow]manual_review:[/] " + warning)
	}
}
