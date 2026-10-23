package bootstrap

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

func retiredAntigravityBytes(command string) ([]byte, error) {
	for _, c := range agents.PromptDrivenCommands {
		if command != c {
			continue
		}
		template, err := agents.ReadTemplate(command)
		if err != nil {
			return nil, err
		}
		text, err := agents.RenderCommandTemplate(template, "antigravity", app.Version)
		if err != nil {
			return nil, err
		}
		marker := agents.VersionMarker(app.Version) + "\n\n"
		if index := strings.Index(text, marker); index >= 0 {
			end := index + len(marker)
			text = text[:end] + agents.PrependUpgradeCheck(text[end:])
		}
		return []byte(text), nil
	}
	for _, c := range agents.CLIDrivenCommands {
		if command == c {
			text, err := agents.ShimContent(command, "antigravity", app.Version)
			return []byte(text), err
		}
	}
	return nil, nil
}

func (p *prep) retireAntigravityWorkflows() error {
	current := project.UserHome()
	for _, part := range []string{".agent", "workflows"} {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil
		}
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "spec-kitty.") || !strings.HasSuffix(name, ".md") {
			continue
		}
		path := filepath.Join(current, name)
		rel := p.rel(path)
		if _, owned := p.previous[rel]; owned {
			if _, err := p.retire(path); err != nil {
				return err
			}
			continue
		}
		before, err := p.observe(path)
		if err != nil {
			return err
		}
		if before.Kind != "file" {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !agents.HasCurrentVersionMarker(content, app.Version) {
			continue
		}
		canonical, err := retiredAntigravityBytes(strings.TrimSuffix(strings.TrimPrefix(name, "spec-kitty."), ".md"))
		if err != nil {
			return err
		}
		if canonical == nil || string(content) != string(canonical) {
			continue
		}
		if err := p.effect(path, FileState{Kind: "absent"}, nil, Proof{"canonical_predecessor", "retired-antigravity-render"}); err != nil {
			return err
		}
		delete(p.entries, rel)
	}
	return nil
}
