package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/bulkedit"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

func bulkReady(o *Out, dir string) (bool, error) {
	r, err := bulkedit.Ready(dir)
	if err != nil {
		return false, err
	}
	if !r.Passed {
		o.ConsoleR(bulkedit.FailurePanel(r))
		return false, ExitErr(1)
	}
	return r.BulkEdit, nil
}

func bulkReviewGate(o *Out, dir, root, slug, target string, ws *lanes.Workspace) error {
	bulk, err := bulkReady(o, dir)
	if err != nil || !bulk {
		return err
	}
	base, head := target, ws.BranchName
	if manifest, err := lanes.Read(dir); err == nil && manifest != nil {
		base = manifest.MissionBranch
	}
	if head == "" {
		head = "HEAD"
	}
	r, err := bulkedit.ReviewDiff(dir, root, base, head)
	if err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	if !r.Passed {
		bulkedit.RenderDiffFailure(*r, o.ConsoleR)
		return ExitErr(1)
	}
	if len(r.Warnings) > 0 {
		bulkedit.RenderDiffWarnings(*r, o.ConsoleR)
	}
	return nil
}

func bulkImplementGate(o *Out, dir, slug, wpID string, w *wp.WP, acknowledged bool) error {
	bulk, err := bulkReady(o, dir)
	if err != nil || bulk {
		return err
	}
	spec, err := os.ReadFile(filepath.Join(dir, "spec.md"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	inference := bulkedit.Infer(string(spec))
	if !inference.Triggered {
		return nil
	}
	planning := false
	for _, owned := range w.List("owned_files") {
		planning = planning || bulkedit.PlanningOwnedFile(owned, slug)
	}
	if !planning && acknowledged {
		return nil
	}
	var matched []string
	for _, p := range inference.Matched {
		matched = append(matched, fmt.Sprintf("'%s' (%dpt)", p.Text, p.Weight))
	}
	text := fmt.Sprintf("This mission's spec contains language suggesting a bulk edit (score: %d/%d)", inference.Score, inference.Threshold)
	title := "[bold yellow]Bulk Edit Inference Warning[/]"
	if planning {
		text += fmt.Sprintf(", but %s owns the occurrence-map planning artifact.\n  Matched: %s\n\nContinuing without --acknowledge-not-bulk-edit for this planning WP.", wpID, strings.Join(matched, ", "))
		title = "[bold yellow]Bulk Edit Inference Informational[/]"
	} else {
		text += fmt.Sprintf(":\n  Matched: %s\n\nIf this IS a bulk edit, set change_mode to 'bulk_edit' in meta.json.\nIf it is NOT, re-run with --acknowledge-not-bulk-edit to suppress.", strings.Join(matched, ", "))
	}
	o.ConsoleR(&rich.RichPanel{R: text, Title: title, BorderStyle: "yellow"})
	if planning {
		return nil
	}
	return ExitErr(1)
}
