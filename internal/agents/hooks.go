package agents

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func claudeSettingsPath(root string) string { return filepath.Join(root, ".claude", "settings.json") }

// loadSettingsLenient is ClaudeCodeHookRegistrar._load: absent, invalid or
// non-object JSON yields an empty object; with preserveInvalid the invalid
// text is first copied to settings.json.invalid.<hex>.
func loadSettingsLenient(root string, preserveInvalid bool) (*jsonx.Object, error) {
	data, raw, err := loadClaudeSettings(root)
	if err == nil && data != nil {
		return data, nil
	}
	if raw == nil {
		if err != nil {
			return nil, err
		}
		return &jsonx.Object{}, nil
	}
	if preserveInvalid {
		var b [16]byte
		rand.Read(b[:])
		backup := claudeSettingsPath(root) + ".invalid." + hex.EncodeToString(b[:])
		if werr := os.WriteFile(backup, raw, 0o644); werr != nil {
			return nil, werr
		}
	}
	return &jsonx.Object{}, nil
}

func saveSettings(root string, data *jsonx.Object) error {
	return writePresenceFile(claudeSettingsPath(root), []byte(jsonx.Dumps(data, jsonx.Pretty)+"\n"))
}

// RegisterClaudeHook is ClaudeCodeHookRegistrar(event).register: appends
// {"matcher"?, "hooks": [{"type": "command", "command": command}]} unless
// already registered. Returns whether settings.json was written.
func RegisterClaudeHook(root, event, command, matcher string) (bool, error) {
	if HookRegistered(root, event, command) {
		return false, nil
	}
	data, err := loadSettingsLenient(root, true)
	if err != nil {
		return false, err
	}
	hooks, ok := data.GetObject("hooks"), true
	if hooks == nil {
		hooks, ok = &jsonx.Object{}, false
	}
	if !ok {
		data.Set("hooks", hooks)
	}
	entries, isList := func() ([]any, bool) {
		v, ok := hooks.Get(event)
		if !ok {
			return nil, false
		}
		l, ok := v.([]any)
		return l, ok
	}()
	if !isList {
		entries = []any{}
	}
	entry := jsonx.NewObject("hooks", []any{jsonx.NewObject("type", "command", "command", command)})
	if matcher != "" {
		entry = jsonx.NewObject("matcher", matcher, "hooks", []any{jsonx.NewObject("type", "command", "command", command)})
	}
	hooks.Set(event, append(entries, entry))
	return true, saveSettings(root, data)
}

// UnregisterClaudeHook is ClaudeCodeHookRegistrar(event).unregister: removes
// the command hook from every entry (entries are kept, possibly with an
// empty hooks list). Returns whether settings.json was written.
func UnregisterClaudeHook(root, event, command string) (bool, error) {
	data, err := loadSettingsLenient(root, false)
	if err != nil {
		return false, err
	}
	hooks := data.GetObject("hooks")
	if hooks == nil {
		return false, nil
	}
	v, _ := hooks.Get(event)
	entries, ok := v.([]any)
	if !ok {
		return false, nil
	}
	found := false
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		eo, ok := e.(*jsonx.Object)
		if !ok {
			out = append(out, e)
			continue
		}
		hv, _ := eo.Get("hooks")
		hl, ok := hv.([]any)
		if !ok {
			out = append(out, e)
			continue
		}
		filtered := []any{}
		for _, h := range hl {
			if ho, ok := h.(*jsonx.Object); ok && ho.GetString("type") == "command" && ho.GetString("command") == command {
				found = true
				continue
			}
			filtered = append(filtered, h)
		}
		ne := &jsonx.Object{}
		for _, kv := range eo.Pairs() {
			ne.Set(kv.Key, kv.Value)
		}
		ne.Set("hooks", filtered)
		out = append(out, ne)
	}
	if !found {
		return false, nil
	}
	hooks.Set(event, out)
	return true, saveSettings(root, data)
}
