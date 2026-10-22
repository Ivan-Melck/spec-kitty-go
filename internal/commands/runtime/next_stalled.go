package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// printStalledInterventions is next_cmd._print_stalled_wp_interventions:
// after an advancing step (human mode), every in_review WP whose last
// status event is older than review.stall_threshold_minutes (default 30)
// gets the two intervention commands. Failures are swallowed.
func printStalledInterventions(ctx *cli.Context, root, slug string) {
	statusDir := coord.Resolve(root, slug).StatusDir()
	if _, err := os.Stat(statusDir); err != nil {
		return
	}
	tasksDir := filepath.Join(project.Specs(root, slug), "tasks")
	if st, err := os.Stat(tasksDir); err != nil || !st.IsDir() {
		return
	}
	threshold := stallThresholdMinutes(root)
	stream, err := status.ReadStream(statusDir, "")
	if err != nil {
		return
	}
	snap := status.Reduce(stream.Transitions, stream.Annotations, true)
	files, _ := filepath.Glob(filepath.Join(tasksDir, "WP*.md"))
	sort.Strings(files)
	var ids []string
	for _, f := range files {
		w, err := wp.Read(f)
		if err != nil {
			return
		}
		ids = append(ids, strings.TrimSpace(w.Doc.Meta.GetString("work_package_id")))
	}
	// Stale-verdict detection: event_sourced_review_result per approved /
	// done WP (one read each).
	for _, id := range ids {
		if l := snap.Lane(id); id != "" && (l == status.Approved || l == status.Done) {
			status.ExtraReadPasses(statusDir, 1)
		}
	}
	now := clock.Now().UTC()
	for _, id := range ids {
		if id == "" || string(snap.Lane(id)) != string(status.InReview) {
			continue
		}
		var lastAt string
		for _, e := range stream.Transitions {
			if e.WPID == id && e.At > lastAt {
				lastAt = e.At
			}
		}
		if lastAt == "" {
			continue
		}
		t, err := clock.ParseISO(lastAt)
		if err != nil {
			continue
		}
		age := int(now.Sub(t.UTC()) / time.Minute)
		if float64(now.Sub(t.UTC()))/float64(time.Minute) <= float64(threshold) {
			continue
		}
		fmt.Fprintf(ctx.Stdout, "\n⚠  %s has been in_review for %dm — reviewer may be stalled.\n   Intervention options:\n"+
			"     spec-kitty agent tasks move-task %s --to approved --force --note 'Approved after %dm stall' --mission %s\n"+
			"     spec-kitty agent tasks move-task %s --to planned --review-feedback-file <path> --mission %s\n",
			id, age, id, age, slug, id, slug)
	}
}

// stallThresholdMinutes is int(config.review.stall_threshold_minutes) (30).
func stallThresholdMinutes(root string) int {
	rv := mission.ProjectConfig(root).GetObject("review")
	if rv == nil {
		return 30
	}
	v, ok := rv.Get("stall_threshold_minutes")
	if !ok {
		return 30
	}
	switch t := v.(type) {
	case bool:
		if t {
			return 1
		}
		return 0
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return 30
}
