package agents

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Orientation section markers.
const (
	SectionOpen  = "<!-- spec-kitty:orientation -->"
	SectionClose = "<!-- /spec-kitty:orientation -->"
)

// Presence is SessionPresenceContent.
type Presence struct {
	Version          string
	ProjectSlug      string
	Health           string // healthy, upgrade-available, migration-required
	AvailableVersion string
}

// Render is SessionPresenceContent.render.
func (c Presence) Render() string {
	extra := ""
	if c.Health == "upgrade-available" {
		extra = "\n⚠ Upgrade available: " + c.AvailableVersion + " — run `spec-kitty upgrade --cli` to update."
	} else if c.Health == "migration-required" {
		extra = "\n⚠ Project migration required — run `spec-kitty upgrade` before using missions."
	}
	return SectionOpen + "\n" +
		"**Spec Kitty v" + c.Version + "** — project: " + c.ProjectSlug + " (" + c.Health + ")" + extra + "\n\n" +
		"Two usage patterns:\n" +
		"- **Full mission** (spec → plan → tasks → implement → review → merge):\n" +
		"  trigger: \"spec out\", \"create a mission\", \"write a spec\", \"plan this\"\n" +
		"  → run `/spec-kitty.specify`\n" +
		"- **Lightweight dispatch** (ad-hoc fix, question, or advice — no mission created):\n" +
		"  trigger: \"hey spec kitty\", \"use spec kitty to\", \"spec kitty <anything>\"\n" +
		"  → **ALWAYS run `spec-kitty dispatch \"<request verbatim>\"` — do NOT answer directly.**\n" +
		"  If you know the right profile, pass it to skip routing:\n" +
		"  `spec-kitty dispatch \"<request verbatim>\" --profile <profile-id>`\n" +
		"  Reason: `spec-kitty dispatch` loads governance context, routes the request,\n" +
		"  and opens the Op. Skipping it produces ungoverned, untracked responses.\n" +
		"  After finishing the work, close the Op with the command printed in the capsule\n" +
		"  (`spec-kitty profile-invocation complete --invocation-id <id> --outcome <done|failed|abandoned>`).\n" +
		SectionClose + "\n"
}

// PresenceWriter is one harness's orientation target.
type PresenceWriter struct {
	Key        string
	RulesPath  string // "" = NullWriter
	AppendMode bool
	CheckDir   string
	Claude     bool // also manages .claude/settings.json hooks
}

// PresenceWriters is session_presence.writers.registry.WRITER_REGISTRY.
var PresenceWriters = map[string]PresenceWriter{
	"claude":      {Key: "claude", RulesPath: ".claude/CLAUDE.md", AppendMode: true, Claude: true},
	"cursor":      {Key: "cursor", RulesPath: ".cursor/rules/spec-kitty.mdc", CheckDir: ".cursor"},
	"windsurf":    {Key: "windsurf", RulesPath: ".windsurf/rules/spec-kitty.md", CheckDir: ".windsurf"},
	"copilot":     {Key: "copilot", RulesPath: ".github/copilot-instructions.md", AppendMode: true, CheckDir: ".github"},
	"kiro":        {Key: "kiro", RulesPath: ".kiro/steering/spec-kitty.md", CheckDir: ".kiro"},
	"gemini":      {Key: "gemini", RulesPath: "GEMINI.md", AppendMode: true},
	"llxprt":      {Key: "llxprt", RulesPath: "LLXPRT.md", AppendMode: true},
	"codex":       {Key: "codex", RulesPath: "AGENTS.md", AppendMode: true},
	"opencode":    {Key: "opencode", RulesPath: "AGENTS.md", AppendMode: true},
	"antigravity": {Key: "antigravity", RulesPath: "AGENTS.md", AppendMode: true},
	"pi":          {Key: "pi", RulesPath: "AGENTS.md", AppendMode: true},
	"vibe":        {Key: "vibe", RulesPath: "AGENTS.md", AppendMode: true},
	"letta":       {Key: "letta", RulesPath: "AGENTS.md", AppendMode: true},
}

// WriterFor returns the writer for key (NullWriter when unregistered).
func WriterFor(key string) PresenceWriter {
	if w, ok := PresenceWriters[key]; ok {
		return w
	}
	return PresenceWriter{Key: key}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// CanWrite is MarkdownRulesWriter.can_write.
func (w PresenceWriter) CanWrite(root string) bool {
	if w.RulesPath == "" {
		return false
	}
	if w.CheckDir != "" {
		return exists(filepath.Join(root, w.CheckDir))
	}
	return exists(filepath.Dir(filepath.Join(root, filepath.FromSlash(w.RulesPath))))
}

// HasPresence reports whether the orientation marker (and, for claude, both
// hooks) are present.
func (w PresenceWriter) HasPresence(root string) bool {
	if w.RulesPath == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(w.RulesPath)))
	if err != nil || !strings.Contains(string(b), SectionOpen) {
		return false
	}
	if w.Claude {
		return HookRegistered(root, "SessionStart", "spec-kitty session-start") && HookRegistered(root, "Stop", "spec-kitty session-stop")
	}
	return true
}

var reHeader = regexp.MustCompile(`(?m)^\*\*Spec Kitty v([^\n]+?)\*\* — project: ([^\n]+?) \((healthy|upgrade-available|migration-required)\)$`)
var reUpgradeWarn = regexp.MustCompile("(?m)^⚠ Upgrade available: ([^\\n]+?) — run `spec-kitty upgrade --cli` to update\\.$")

func canonicalSection(block string, c Presence) bool {
	n := strings.ReplaceAll(block, "\r\n", "\n")
	m := reHeader.FindStringSubmatch(n)
	if m == nil {
		return false
	}
	old := Presence{Version: m[1], ProjectSlug: m[2], Health: m[3]}
	if m[3] == "upgrade-available" {
		w := reUpgradeWarn.FindStringSubmatch(n)
		if w == nil {
			return false
		}
		old.AvailableVersion = w[1]
	}
	return n == strings.TrimRight(old.Render(), "\n")
}

// PrepareOrientation returns the desired file content (nil = leave alone)
// and a reason when preserved.
func (w PresenceWriter) PrepareOrientation(root string, c Presence) ([]byte, string, error) {
	target := filepath.Join(root, filepath.FromSlash(w.RulesPath))
	original, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}
	existing := string(original)
	nl := "\n"
	if strings.Contains(existing, "\r\n") {
		nl = "\r\n"
	}
	rendered := strings.ReplaceAll(c.Render(), "\n", nl)
	var desired string
	if strings.Contains(existing, SectionOpen) || strings.Contains(existing, SectionClose) {
		if strings.Count(existing, SectionOpen) != 1 || strings.Count(existing, SectionClose) != 1 {
			return nil, "", fmt.Errorf("Duplicate or unbalanced orientation markers")
		}
		start := strings.Index(existing, SectionOpen)
		end := strings.Index(existing, SectionClose)
		if end < start {
			return nil, "", fmt.Errorf("Reversed orientation markers")
		}
		end += len(SectionClose)
		block := existing[start:end]
		if !canonicalSection(block, c) {
			return nil, "Edited managed orientation", nil
		}
		if strings.Contains(block, "**Spec Kitty v"+c.Version+"**") {
			return nil, "", nil
		}
		desired = existing[:start] + strings.TrimRight(rendered, "\r\n") + existing[end:]
	} else if existing != "" && !w.AppendMode {
		return nil, "Unowned whole-file rules", nil
	} else {
		sep := ""
		if existing != "" {
			sep = nl + nl
		}
		desired = existing + sep + rendered
	}
	if desired == existing {
		return nil, "", nil
	}
	return []byte(desired), "", nil
}

// WriteOrientation installs/updates the orientation (and claude hooks).
func (w PresenceWriter) WriteOrientation(root string, c Presence) error {
	if w.RulesPath == "" {
		return nil
	}
	content, _, err := w.PrepareOrientation(root, c)
	if err != nil {
		return err
	}
	var settings []byte
	if w.Claude {
		if settings, err = PrepareClaudeHooks(root); err != nil {
			return err
		}
	}
	if content != nil {
		if err := writePresenceFile(filepath.Join(root, filepath.FromSlash(w.RulesPath)), content); err != nil {
			return err
		}
	}
	if settings != nil {
		if err := writePresenceFile(filepath.Join(root, ".claude", "settings.json"), settings); err != nil {
			return err
		}
	}
	return nil
}

func writePresenceFile(target string, data []byte) error {
	if b, err := os.ReadFile(target); err == nil && bytes.Equal(b, data) {
		return nil
	}
	mode := os.FileMode(0o644)
	if st, err := os.Lstat(target); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return writeAtomic(target, data, mode)
}

// ---------------------------------------------------------------------------
// .claude/settings.json hooks
// ---------------------------------------------------------------------------

func loadClaudeSettings(root string) (*jsonx.Object, []byte, error) {
	p := filepath.Join(root, ".claude", "settings.json")
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &jsonx.Object{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	v, err := jsonx.Decode(raw)
	if err != nil {
		return nil, raw, err
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, raw, fmt.Errorf("Expected Claude settings JSON object")
	}
	return o, raw, nil
}

func commandHooks(entries []any) []*jsonx.Object {
	var out []*jsonx.Object
	for _, e := range entries {
		eo, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		for _, h := range eo.GetList("hooks") {
			if ho, ok := h.(*jsonx.Object); ok {
				out = append(out, ho)
			}
		}
	}
	return out
}

// HookRegistered reports whether command is registered for event.
func HookRegistered(root, event, command string) bool {
	s, _, err := loadClaudeSettings(root)
	if err != nil || s == nil {
		return false
	}
	hooks := s.GetObject("hooks")
	if hooks == nil {
		return false
	}
	for _, h := range commandHooks(hooks.GetList(event)) {
		if h.GetString("type") == "command" && h.GetString("command") == command {
			return true
		}
	}
	return false
}

// PrepareClaudeHooks returns updated settings.json bytes registering the
// SessionStart/Stop hooks, or nil when already present.
func PrepareClaudeHooks(root string) ([]byte, error) {
	data, _, err := loadClaudeSettings(root)
	if err != nil {
		return nil, err
	}
	hv, ok := data.Get("hooks")
	if !ok {
		hv = &jsonx.Object{}
		data.Set("hooks", hv)
	}
	hooks, isObj := hv.(*jsonx.Object)
	if !isObj {
		return nil, fmt.Errorf("Expected Claude hooks object")
	}
	changed := false
	for _, ec := range [][2]string{{"SessionStart", "spec-kitty session-start"}, {"Stop", "spec-kitty session-stop"}} {
		ev, ok := hooks.Get(ec[0])
		if !ok {
			ev = []any{}
		}
		entries, isList := ev.([]any)
		if !isList {
			return nil, fmt.Errorf("Expected Claude %s hook list", ec[0])
		}
		present := false
		for _, h := range commandHooks(entries) {
			if h.GetString("type") == "command" && h.GetString("command") == ec[1] {
				present = true
			}
		}
		if !present {
			entries = append(entries, jsonx.NewObject("hooks", []any{jsonx.NewObject("type", "command", "command", ec[1])}))
			changed = true
		}
		hooks.Set(ec[0], entries)
	}
	if !changed {
		return nil, nil
	}
	return []byte(jsonx.Dumps(data, jsonx.Pretty) + "\n"), nil
}
