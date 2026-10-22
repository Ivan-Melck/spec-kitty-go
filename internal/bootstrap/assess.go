package bootstrap

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
)

// FamilyEffect is one planned global write of a family assessment, relative
// to the family anchor.
type FamilyEffect struct {
	Rel           string
	Action        string
	Before, After FileState
	Proof         Proof
	Logical       []string
}

// Family is one global asset owner's assessment (runtime_bootstrap,
// slash_commands or global_skills): planned effects in plan order and the
// dispositions of every node it left alone. Err is the incomplete() reason.
type Family struct {
	Owner        string
	Anchor       string
	Effects      []FamilyEffect
	Dispositions []Disposition
	Err          error
}

func (p *prep) family(logical func(path string) []string) Family {
	f := Family{Owner: p.owner, Anchor: p.anchor, Dispositions: append([]Disposition(nil), p.disp...)}
	for _, path := range p.order {
		w := p.writes[path]
		f.Effects = append(f.Effects, FamilyEffect{Rel: p.rel(path), Action: w.action, Before: w.before, After: w.after, Proof: w.proof, Logical: logical(path)})
	}
	return f
}

func failedFamily(owner, anchor string, err error) Family {
	return Family{Owner: owner, Anchor: anchor, Err: err}
}

// AssessRuntimeFamily is bootstrap.assess_runtime.
func AssessRuntimeFamily() Family {
	home := KittifyHome()
	p, err := AssessRuntime()
	if err != nil {
		return failedFamily("runtime_bootstrap", anchorFor(home), err)
	}
	return p.family(func(string) []string { return []string{"runtime_bootstrap"} })
}

// AssessCommandsFamily is agent_commands.assess_global_agent_commands;
// keys == nil assesses the full fleet (with the freshness and version
// stamps), otherwise only the named agents (no stamps).
func AssessCommandsFamily(keys []string) Family {
	home := KittifyHome()
	var roots []string
	for _, k := range agents.CommandAgents {
		roots = append(roots, agents.GlobalCommandDir(k))
	}
	anchor := anchorFor(append([]string{home}, roots...)...)
	p, _, err := AssessGlobalCommands(keys)
	if err != nil {
		return failedFamily("slash_commands", anchor, err)
	}
	selected := keys
	if selected == nil {
		selected = agents.SortedCommandAgents()
	}
	sel := uniqueSortedKeys(selected)
	return p.family(func(path string) []string {
		var owners []string
		for _, k := range sel {
			dir := agents.GlobalCommandDir(k)
			if path == dir || filepath.Dir(path) == dir || strings.HasPrefix(dir, path+string(filepath.Separator)) {
				owners = append(owners, k)
			}
		}
		if len(owners) == 0 {
			return sel
		}
		return owners
	})
}

func uniqueSortedKeys(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range in {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// AssessSkillsFamily is agent_skills.assess_global_agent_skills with an
// explicit GlobalSkillSelection: the built-in skills for agentKeys only,
// without the version stamp, and custom or retired entries preserved.
func AssessSkillsFamily(agentKeys []string) Family {
	p, sel, anchor, err := assessSkillsSelection(agentKeys)
	if err != nil {
		return failedFamily("global_skills", anchor, err)
	}
	return p.family(func(path string) []string {
		var owners []string
		for _, a := range sel {
			r := agents.GlobalSkillRoot(a)
			if r == path || strings.HasPrefix(path, r+string(filepath.Separator)) {
				owners = append(owners, a)
			}
		}
		if len(owners) == 0 {
			return sel
		}
		return owners
	})
}

// EnsureSkillsSelection assesses and applies the global skill selection for
// agentKeys (the coordinated global half of a managed-skill repair).
func EnsureSkillsSelection(agentKeys []string) error {
	p, _, _, err := assessSkillsSelection(agentKeys)
	if err != nil {
		return err
	}
	return p.apply()
}

func assessSkillsSelection(agentKeys []string) (*prep, []string, string, error) {
	home := KittifyHome()
	var sel []string
	for _, a := range agentKeys {
		if agents.GlobalSkillRoot(a) != "" {
			sel = append(sel, a)
		}
	}
	seen := map[string]bool{}
	var roots []string
	for _, a := range sel {
		r := agents.GlobalSkillRoot(a)
		if !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	anchor := anchorFor(append([]string{home}, roots...)...)
	p, err := newPrep("global_skills", anchor, filepath.Join(home, "cache"), ".agent-skills.lock")
	if err != nil {
		return nil, sel, anchor, err
	}
	skills := append([]agents.Skill(nil), agents.Skills()...)
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	canonical := map[string]bool{}
	for _, s := range skills {
		canonical[s.Name] = true
	}
	err = func() error {
		for _, root := range roots {
			st, err := p.observe(root)
			if err != nil {
				return err
			}
			if st.Kind != "directory" && st.Kind != "absent" {
				p.preserve(root, "Unproven global skill root replacement", false)
				continue
			}
			for _, s := range skills {
				if err := p.skillTree(s, "", filepath.Join(root, s.Name)); err != nil {
					return err
				}
				if err := p.pruneMissing(filepath.Join(root, s.Name)); err != nil {
					return err
				}
			}
			if st.Kind == "directory" {
				ents, _ := os.ReadDir(root)
				for _, e := range ents {
					if !canonical[e.Name()] {
						p.preserve(filepath.Join(root, e.Name()), "Unproven custom skill; preserve content and links", false)
					}
				}
			}
		}
		return p.finish("", app.Version)
	}()
	if err != nil {
		return nil, sel, anchor, err
	}
	return p, sel, anchor, nil
}

// ApplySelectedFamilies applies the upgrade finalizer's coordinated global
// batch: runtime, the slash commands of commandKeys (no stamps) and the
// built-in skill selection of skillAgents.
func ApplySelectedFamilies(commandKeys, skillAgents []string) error {
	p, err := AssessRuntime()
	if err == nil {
		err = p.apply()
	}
	if err != nil {
		return err
	}
	if commandKeys == nil {
		commandKeys = []string{}
	}
	cp, _, err := AssessGlobalCommands(commandKeys)
	if err == nil {
		err = cp.apply()
	}
	if err != nil {
		return err
	}
	return EnsureSkillsSelection(skillAgents)
}
