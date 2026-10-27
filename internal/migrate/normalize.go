package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// NormalizeResult is NormalizeMissionLifecycleResult.
type NormalizeResult struct {
	Slug           string
	Status         string // normalized | skipped | error
	LifecycleState *string
	Actions        []string
	Warnings       []string
	Error          *string
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func needsEventLog(dir string) bool {
	if fileExists(filepath.Join(dir, "status.events.jsonl")) {
		return false
	}
	return fileExists(filepath.Join(dir, "status.json")) || isDirFollow(filepath.Join(dir, "tasks"))
}

func needsDerivedRefresh(dir, derived string) bool {
	fd := filepath.Join(derived, filepath.Base(dir))
	paths := []string{filepath.Join(fd, "status.json"), filepath.Join(fd, "progress.json"), filepath.Join(fd, "lifecycle.json")}
	for _, p := range paths {
		if !fileExists(p) {
			return true
		}
	}
	ev, err := os.Stat(filepath.Join(dir, "status.events.jsonl"))
	if err != nil {
		return false
	}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && ev.ModTime().After(st.ModTime()) {
			return true
		}
	}
	return false
}

// NormalizeLifecycle is normalize_mission_lifecycle.normalize_repo.
func NormalizeLifecycle(root string, dryRun bool, missionSlug *string) []*NormalizeResult {
	var out []*NormalizeResult
	names := MissionDirs(root)
	if missionSlug != nil {
		var one []string
		for _, n := range names {
			if n == *missionSlug {
				one = append(one, n)
			}
		}
		names = one
	}
	if len(names) == 0 {
		return out
	}
	derived := project.Kittify(root, "derived")
	tracked := ProbeTrackedMissionPaths(root)
	for _, name := range names {
		dir := project.Specs(root, name)
		r := &NormalizeResult{Slug: name, Status: "skipped", Actions: []string{}, Warnings: []string{}}
		out = append(out, r)
		if IsKittySpecsResidue(dir, root, tracked) {
			// #6627: residue (e.g. an untracked decisions/ leftover) is reported, never given an identity.
			r.Warnings = append(r.Warnings, ResidueNotice(project.SpecsDir+"/"+name))
			continue
		}
		fail := func(msg string) { r.Status = "error"; r.Error = &msg }
		meta, err := LoadMetaFailClosed(dir)
		if err != nil {
			fail("Could not read meta.json: " + err.Error())
			continue
		}
		if meta == nil {
			r.Warnings = append(r.Warnings, "meta.json missing; mission skipped")
			continue
		}
		refresh := false
		bf, err := BackfillMissionIdentity(dir, dryRun)
		if err != nil {
			fail("Identity backfill failed: " + err.Error())
			continue
		}
		if bf.Action == "wrote" {
			r.Actions = append(r.Actions, "Backfilled mission identity metadata")
			refresh = true
		}
		if bf.NumberCoerced {
			r.Actions = append(r.Actions, "Normalized legacy mission_number type")
			refresh = true
		}
		if !dryRun && (bf.Action == "wrote" || bf.NumberCoerced) {
			if m, err := LoadMetaFailClosed(dir); err == nil && m != nil {
				meta = m
			}
		}
		eventRefresh := false
		if needsEventLog(dir) {
			mid, _ := meta.Get("mission_id")
			if pyjson.Truthy(mid) && isDirFollow(filepath.Join(dir, "tasks")) {
				if dryRun {
					r.Actions = append(r.Actions, "Would backfill missing WP identity fields before rebuilding the event log")
				} else {
					BackfillWPIDs(dir, pyjson.Str(mid))
					r.Actions = append(r.Actions, "Backfilled missing WP identity fields")
				}
			}
			if dryRun {
				r.Actions = append(r.Actions, "Would rebuild status.events.jsonl from legacy mission state")
			} else {
				rb := RebuildEventLog(dir, name, map[string]string{})
				if len(rb.Errors) > 0 {
					fail(strings.Join(rb.Errors, "; "))
					r.Warnings = append(r.Warnings, rb.Warnings...)
					continue
				}
				r.Actions = append(r.Actions, fmt.Sprintf("Rebuilt status.events.jsonl (%d synthetic events, %d corrected)", rb.Generated, rb.Corrected))
				r.Warnings = append(r.Warnings, rb.Warnings...)
			}
			eventRefresh = true
		}
		refresh = refresh || eventRefresh || needsDerivedRefresh(dir, derived)
		state := func() {
			s := LifecycleState(dir)
			r.LifecycleState = &s
		}
		switch {
		case dryRun && refresh:
			r.Actions = append(r.Actions, "Would regenerate canonical status/progress/lifecycle views")
			state()
		case !dryRun && refresh:
			if _, err := WriteDerivedViews(dir, derived); err != nil {
				fail("Lifecycle materialization failed: " + err.Error())
				continue
			}
			state()
			r.Actions = append(r.Actions, "Regenerated canonical status/progress/lifecycle views")
		default:
			state()
		}
		if len(r.Actions) > 0 {
			r.Status = "normalized"
		}
	}
	return out
}
