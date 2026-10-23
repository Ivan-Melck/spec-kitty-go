package bulkedit

import (
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"sort"
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

var sourceLabels = map[string]string{"added": "created here", "non-occurrence": "no occurrences", "runtime-state": "own bookkeeping", "exception": "map exception", "move": "declared move", "structural-target": "structural target", "path-heuristic": "(unclassified)"}

func sourceLabel(source string) string {
	if s := sourceLabels[source]; s != "" {
		return s
	}
	return source
}

func RenderDiffFailure(r DiffResult, print func(any)) {
	table := &rich.BoxTable{Title: "Bulk Edit Review: Diff Compliance", Headers: []string{"File", "Category", "Action", "Verdict"}, NoWrap: []bool{false, true, true, true}, Overflow: []string{"fold"}}
	for _, blocking := range []bool{true, false} {
		for _, a := range r.Assessments {
			if a.Violation != blocking || (!blocking && a.Action != "manual_review") {
				continue
			}
			category, action := a.Category, a.Action
			if category == "" {
				category = sourceLabel(a.Source)
			}
			if action == "" {
				action = "(none)"
			}
			verdict := "[bold red]BLOCK[/]"
			if !blocking {
				verdict = "[yellow]review[/]"
			}
			table.Rows = append(table.Rows, []string{a.Path, category, action, verdict})
		}
	}
	print(table.Table())
	counts := map[string]int{}
	var order []string
	total := 0
	for _, a := range r.Assessments {
		if !a.Violation && a.Action != "manual_review" {
			if counts[a.Source] == 0 {
				order = append(order, a.Source)
			}
			counts[a.Source]++
			total++
		}
	}
	if total > 0 {
		sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
		var summary []string
		for _, source := range order {
			summary = append(summary, fmt.Sprintf("%d %s", counts[source], sourceLabel(source)))
		}
		print(fmt.Sprintf("[green]%d further files passed[/]: %s", total, strings.Join(summary, ", ")))
	}
	if len(r.Errors) > 0 {
		var lines []string
		for _, err := range r.Errors {
			lines = append(lines, "  • "+err)
		}
		print(&rich.RichPanel{R: strings.Join(lines, "\n"), Title: "[bold red]Review Rejected: Diff Compliance Violations[/]", Subtitle: "FR-007/FR-008 — forbidden or unclassified surfaces touched", BorderStyle: "red"})
	}
}

func RenderDiffWarnings(r DiffResult, print func(any)) {
	print("[yellow]Bulk-edit diff notes[/] (non-blocking):")
	paths := func(label string, values []string, limit int) {
		if len(values) == 0 {
			return
		}
		sort.Strings(values)
		print(fmt.Sprintf("  [yellow]%d[/] %s:", len(values), label))
		shown := values
		if limit > 0 && len(values) > limit {
			shown = values[:limit]
		}
		for _, p := range shown {
			print("    " + p)
		}
		if len(shown) < len(values) {
			print(fmt.Sprintf("    … and %d more", len(values)-len(shown)))
		}
	}
	var review, bypass []string
	attention := map[string]bool{}
	for _, a := range r.Assessments {
		if a.Violation {
			continue
		}
		if a.Action == "manual_review" {
			review = append(review, a.Path)
			attention[a.Path] = true
		}
		if a.Source == "structural-target" {
			bypass = append(bypass, a.Path)
			attention[a.Path] = true
		}
	}
	paths("need manual review", review, 0)
	paths("structural-target exemptions (do_not_change bypass)", bypass, 0)
	for _, info := range [][2]string{{"added", "created by this diff"}, {"non-occurrence", "carry no textual occurrence"}, {"runtime-state", "this mission's own bookkeeping"}, {"path-heuristic", "classified by path, no review needed"}, {"exception", "allowed by a map exception"}, {"move", "declared structural moves"}} {
		var values []string
		for _, a := range r.Assessments {
			if !a.Violation && a.Source == info[0] && !attention[a.Path] {
				values = append(values, a.Path)
			}
		}
		paths(info[1], values, 12)
	}
	for _, warning := range r.Warnings {
		if strings.Contains(warning, "field-path exception pins") {
			print("  [yellow]•[/] " + warning)
		}
	}
}
