package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "session-start",
			Short: "Emit spec-kitty orientation for the Claude Code SessionStart hook.",
			Run:   runSessionStart,
		}, &cli.Command{
			Name:  "session-stop",
			Short: "Emit the open-Ops reminder for the Claude Code Stop hook.",
			Run:   runSessionStop,
		})
	})
}

// findKittifyRoot is session_start._find_project_root: the first directory
// at or above cwd containing a .kittify/ directory.
func findKittifyRoot() string {
	cur, err := os.Getwd()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(cur); err == nil {
		cur = r
	}
	for {
		if st, err := os.Stat(filepath.Join(cur, ".kittify")); err == nil && st.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// SessionPresence is SessionPresenceManager._build_content for root: the
// health is migration-required for a legacy/stale project schema.
func SessionPresence(root string) agents.Presence {
	c := presenceContent(root)
	if kittify.ScanProjectStatus(root).NeedsMigration() {
		c.Health = "migration-required"
	}
	return c
}

func runSessionStart(ctx *cli.Context) error {
	defer func() { recover() }()
	root := findKittifyRoot()
	if root == "" {
		return nil
	}
	// load_agent_config (its failure is swallowed with everything else).
	if _, err := kittify.LoadAgentConfig(root); err != nil {
		return nil
	}
	fmt.Fprintln(ctx.Stdout, SessionPresence(root).Render())
	if s := RenderOpenOpsSection(root, time.Time{}); s != "" {
		fmt.Fprintln(ctx.Stdout, s)
	}
	return nil
}

func runSessionStop(ctx *cli.Context) error {
	defer func() { recover() }()
	root := findKittifyRoot()
	if root == "" {
		return nil
	}
	if s := RenderOpenOpsSection(root, time.Time{}); s != "" {
		fmt.Fprintln(ctx.Stdout, "spec-kitty: this session is ending with open Ops — close each with the\nreal outcome before moving on.\n"+s)
	}
	return nil
}

var nonOpJSONL = map[string]bool{"ops-index.jsonl": true, "lifecycle.jsonl": true, "propagation-errors.jsonl": true, "op-closures.jsonl": true}

func readJSONLines(path string, fn func(map[string]any) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if !fn(m) {
			return
		}
	}
}

// ListOrphanOps is doctor.ops.list_orphan_ops: kitty-ops/*.jsonl records
// with no "completed" event and no closure on the op-closures spine.
func ListOrphanOps(root string) []string {
	dir := filepath.Join(root, "kitty-ops")
	paths, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	closed := map[string]bool{}
	readJSONLines(filepath.Join(dir, "op-closures.jsonl"), func(m map[string]any) bool {
		if m["event"] == "completed" {
			if id, ok := m["invocation_id"].(string); ok {
				closed[id] = true
			}
		}
		return true
	})
	var out []string
	for _, p := range paths {
		name := filepath.Base(p)
		stem := strings.TrimSuffix(name, ".jsonl")
		if nonOpJSONL[name] || closed[stem] {
			continue
		}
		done := false
		readJSONLines(p, func(m map[string]any) bool {
			if m["event"] == "completed" {
				done = true
				return false
			}
			return true
		})
		if !done {
			out = append(out, p)
		}
	}
	return out
}

// RenderOpenOpsSection is session_presence.open_ops.render_open_ops_section.
func RenderOpenOpsSection(root string, now time.Time) string {
	orphans := ListOrphanOps(root)
	if len(orphans) == 0 {
		return ""
	}
	if now.IsZero() {
		now = clock.Now()
	}
	lines := []string{fmt.Sprintf("⚠ Open Ops (%d): work that was dispatched but never closed", len(orphans))}
	for _, p := range orphans {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		profile, started := "", ""
		if f, err := os.Open(p); err == nil {
			first, _ := bufio.NewReader(f).ReadString('\n')
			f.Close()
			var m map[string]any
			if json.Unmarshal([]byte(first), &m) == nil {
				profile, started = pyStrOrEmpty(m["profile_id"]), pyStrOrEmpty(m["started_at"])
			}
		}
		var meta []string
		if profile != "" {
			meta = append(meta, profile)
		}
		if age := formatOpAge(started, now); age != "" {
			meta = append(meta, age)
		}
		metaText := ""
		if len(meta) > 0 {
			metaText = " (" + strings.Join(meta, ", ") + ")"
		}
		lines = append(lines, fmt.Sprintf("  %s%s — close: spec-kitty profile-invocation complete --invocation-id %s --outcome <done|failed|abandoned>", id, metaText, id))
	}
	lines = append(lines, "Sweep stale ones: spec-kitty doctor ops --close-stale")
	return strings.Join(lines, "\n")
}

func pyStrOrEmpty(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return ""
	case float64:
		if x == 0 {
			return ""
		}
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	}
	return fmt.Sprintf("%v", v)
}

func formatOpAge(startedAt string, now time.Time) string {
	if startedAt == "" {
		return ""
	}
	t, err := clock.ParseISO(startedAt)
	if err != nil {
		return ""
	}
	h := now.Sub(t).Hours()
	if h < 0 {
		h = 0
	}
	return fmt.Sprintf("%dh old", int(h))
}
