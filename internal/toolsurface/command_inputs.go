package toolsurface

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

type commandInput struct {
	Path     string
	State    FileState
	Children []string
}

func (b *commandBatch) observeInput(abs string, children bool) (FileState, error) {
	state, err := observeNode(abs)
	if err != nil {
		return state, err
	}
	previous, exists := b.inputs[abs]
	if exists && previous.State != state {
		return state, installerError("precondition_changed", jsonx.NewObject("path", abs))
	}
	input := commandInput{Path: abs, State: state}
	if children && state.Kind == "directory" {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return state, err
		}
		input.Children = make([]string, 0, len(entries))
		for _, entry := range entries {
			input.Children = append(input.Children, entry.Name())
		}
	} else if exists {
		input.Children = previous.Children
	}
	if b.inputs == nil {
		b.inputs = map[string]commandInput{}
	}
	b.inputs[abs] = input
	return state, nil
}

func recheckCommandInputs(a *Assessment, payload *PreparedCommands) []Diagnostic {
	changed := func(abs string) []Diagnostic {
		return []Diagnostic{Diag("precondition_changed", "command_skills", "error", installerError("precondition_changed", jsonx.NewObject("path", abs)).Error())}
	}
	paths := make([]string, 0, len(payload.Inputs))
	for abs := range payload.Inputs {
		paths = append(paths, abs)
	}
	sort.Strings(paths)
	for _, abs := range paths {
		input := payload.Inputs[abs]
		state, err := observeNode(abs)
		if err != nil {
			return changed(abs)
		}
		expected := input.State
		if p := payload.Provisioning; p != nil && p.Changed() {
			if abs == p.Target && state.Kind == "file" && state.SHA256 == Digest(p.Desired) {
				expected = FileOf(Digest(p.Desired), p.Mode)
			}
			for _, parent := range p.AbsentParents {
				if abs == filepath.Join(a.Root.Path, filepath.FromSlash(parent)) && state.Kind == "directory" {
					expected = DirOf(0755)
				}
			}
		}
		if state != expected {
			return changed(abs)
		}
		if input.Children != nil {
			entries, err := os.ReadDir(abs)
			if err != nil {
				return changed(abs)
			}
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			if !reflect.DeepEqual(names, input.Children) {
				return changed(abs)
			}
		}
	}
	for command, digest := range payload.Rendered {
		content, err := agents.RenderCommandSkill(a.Root.Path, command)
		if err != nil || Digest(content) != digest {
			return changed(a.Root.Path)
		}
	}
	return nil
}
