package agents

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
)

// SkillsAssetRoot is the doctrine skill catalog (charter/offering/skills).
const SkillsAssetRoot = "charter/offering/skills"

// RetiredSkillNames are retired canonical skill names removed from global roots.
var RetiredSkillNames = map[string]bool{
	"debugger-debbie": true, "paula-patterns": true, "spk-team-upsun-cli-sync": true,
	"spk-admin-dashboard": true, "spk-team-auth": true, "spk-team-connectors": true,
	"spk-team-sync": true, "spk-team-tracker": true, "ad-hoc-profile-load": true,
	"spec-kitty-bulk-edit-classification": true, "spec-kitty-charter-doctrine": true,
	"spec-kitty-constitution-doctrine": true, "spec-kitty-glossary-context": true,
	"spec-kitty-spdd-reasons": true, "spk-doctrine-bulk-edit": true, "spk-doctrine-charter": true,
	"spk-doctrine-glossary": true, "spk-doctrine-profile-load": true,
	"spk-doctrine-semantic-compression": true, "spk-doctrine-show-me": true,
	"spk-doctrine-spdd-reasons": true, "spec-kitty.advise": true,
}

// Skill is one canonical doctrine skill.
type Skill struct {
	Name string
	// Dir is the asset path of the skill directory.
	Dir string
	// Files are installable files relative to Dir: SKILL.md, then sorted
	// non-dot files of references/, scripts/, assets/.
	Files []string
}

// Skills discovers the canonical skills (sorted by name).
func Skills() []Skill {
	ents, err := assets.List(SkillsAssetRoot)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := SkillsAssetRoot + "/" + e.Name()
		if !assets.Exists(dir + "/SKILL.md") {
			continue
		}
		s := Skill{Name: e.Name(), Dir: dir, Files: []string{"SKILL.md"}}
		for _, sub := range []string{"references", "scripts", "assets"} {
			sents, err := assets.List(dir + "/" + sub)
			if err != nil {
				continue
			}
			var names []string
			for _, se := range sents {
				if se.IsDir() || strings.HasPrefix(se.Name(), ".") {
					continue
				}
				names = append(names, sub+"/"+se.Name())
			}
			sort.Strings(names)
			s.Files = append(s.Files, names...)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SkillTreeNode is one node of a skill's full source tree (for global installs).
type SkillTreeNode struct {
	Rel   string // relative to the skill dir ("" = the dir itself)
	IsDir bool
}

// SkillTree walks the full skill directory (including dotfiles), depth first
// in sorted name order, directories before their children.
func SkillTree(s Skill) []SkillTreeNode {
	var out []SkillTreeNode
	var walk func(rel string)
	walk = func(rel string) {
		out = append(out, SkillTreeNode{Rel: rel, IsDir: true})
		p := s.Dir
		if rel != "" {
			p += "/" + rel
		}
		ents, _ := fs.ReadDir(assets.Dir(p), ".")
		names := make([]fs.DirEntry, len(ents))
		copy(names, ents)
		sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })
		for _, e := range names {
			child := e.Name()
			if rel != "" {
				child = rel + "/" + e.Name()
			}
			if e.IsDir() {
				walk(child)
			} else {
				out = append(out, SkillTreeNode{Rel: child})
			}
		}
	}
	walk("")
	return out
}

// ReadSkillFile reads one skill file from the assets.
func ReadSkillFile(s Skill, rel string) ([]byte, error) {
	return assets.ReadFile(path.Join(s.Dir, rel))
}

var reLeadingFrontmatter = regexp.MustCompile(`(?s)\A---\n.*?\n---\n?`)

// EnsureSkillFrontmatter is command_renderer.ensure_skill_frontmatter.
func EnsureSkillFrontmatter(content, skillName string) string {
	content = NormalizeNewlines(content)
	if reLeadingFrontmatter.MatchString(content) {
		return content
	}
	desc := firstParagraphDescription(content)
	if desc == "" {
		desc = "Spec Kitty skill"
	}
	return "---\nname: " + skillYAMLScalar(skillName) + "\ndescription: " + skillYAMLScalar(desc) + "\n---\n" + content
}

var (
	reDecorator  = regexp.MustCompile(`^[-*_]{3,}$`)
	reLeadMarker = regexp.MustCompile(`^\*+|^_+`)
	reUnsafe     = regexp.MustCompile("[:#\\[\\]{},&*?|<>=!%@`\"'\n\r\t]")
)

var yamlKeywords = map[string]bool{}

func init() {
	for _, k := range []string{"true", "false", "yes", "no", "on", "off", "null", "~"} {
		yamlKeywords[k] = true
		yamlKeywords[strings.ToUpper(k[:1])+k[1:]] = true
		yamlKeywords[strings.ToUpper(k)] = true
	}
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func firstParagraphDescription(body string) string {
	inFence := false
	for _, line := range SplitLines(body) {
		st := strings.TrimSpace(line)
		if strings.HasPrefix(st, "```") {
			inFence = !inFence
			continue
		}
		if inFence || st == "" || strings.HasPrefix(st, "#") || reDecorator.MatchString(st) {
			continue
		}
		cleaned := strings.TrimSpace(reLeadMarker.ReplaceAllString(st, ""))
		if cleaned != "" {
			return truncRunes(cleaned, 140)
		}
	}
	return ""
}

func skillYAMLScalar(text string) string {
	if text != "" && text == strings.TrimSpace(text) && !yamlKeywords[text] && !reUnsafe.MatchString(text) && !strings.ContainsAny(text[:1], "-+.0123456789") {
		return text
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`) + `"`
}
