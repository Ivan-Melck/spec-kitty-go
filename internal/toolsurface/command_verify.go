package toolsurface

import (
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

type CommandVerify struct {
	Drift, Gaps, Orphans, Stale, Unsafe, Legacy, LegacyOwned, LegacyRecorded, Collisions []string
}

type CommandVerifyReport = CommandVerify

func VerifyCommandSkills(root string) (*CommandVerify, error) {
	v := &CommandVerify{}
	entries, err := LoadCommandManifest(root)
	if err != nil {
		return v, err
	}
	recorded := map[string]bool{}
	for _, entry := range entries {
		rel := entry.Path
		recorded[rel] = true
		if agents.ParseCommandSkillPath(rel, true) != "" {
			v.Legacy = append(v.Legacy, rel)
			v.LegacyRecorded = append(v.LegacyRecorded, rel)
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if !confined(root, abs) {
			v.Unsafe = append(v.Unsafe, rel)
			continue
		}
		if agents.ParseCommandSkillPath(rel, false) == "" {
			v.Stale = append(v.Stale, rel)
		}
		b, err := os.ReadFile(abs)
		if os.IsNotExist(err) {
			v.Gaps = append(v.Gaps, rel)
			continue
		}
		if err != nil {
			return v, err
		}
		if Digest(b) != entry.ContentHash {
			v.Drift = append(v.Drift, rel)
		}
	}
	config, configErr := kittify.LoadAgentConfig(root)
	canRender := false
	if configErr == nil {
		for _, key := range config.Available {
			canRender = canRender || agents.IsCommandSkillAgent(key)
		}
	}
	for _, command := range agents.CommandSkillCommands {
		legacyRel := agents.LegacyCommandSkillPath(command)
		abs := filepath.Join(root, filepath.FromSlash(legacyRel))
		if !containsStr(v.Legacy, legacyRel) {
			if _, err := os.Lstat(abs); err == nil {
				v.Legacy = append(v.Legacy, legacyRel)
				if !recorded[legacyRel] && canRender && plainCommandPath(root, legacyRel) {
					canonical, err := agents.RenderCommandSkill(root, command)
					if err == nil {
						legacy, err := agents.LegacyCommandSkillRendering(canonical, command)
						if err == nil {
							if b, err := os.ReadFile(abs); err == nil && Digest(b) == Digest(legacy) {
								v.LegacyOwned = append(v.LegacyOwned, legacyRel)
							}
						}
					}
				}
			} else if _, err := os.Lstat(filepath.Dir(abs)); err == nil {
				v.Legacy = append(v.Legacy, path.Dir(legacyRel)+"/")
			}
		}
		rel := agents.CommandSkillPath(command)
		abs = filepath.Join(root, filepath.FromSlash(rel))
		folder := filepath.Dir(abs)
		st, err := os.Stat(folder)
		if err != nil || !st.IsDir() {
			continue
		}
		if !recorded[rel] {
			folderInfo, _ := os.Lstat(folder)
			if folderInfo.Mode()&os.ModeSymlink == 0 {
				collision := false
				info, e := os.Lstat(abs)
				if os.IsNotExist(e) {
					children, _ := os.ReadDir(folder)
					collision = len(children) > 0
				} else if e == nil && info.Mode().IsRegular() {
					collision = true
					if canRender {
						if canonical, err := agents.RenderCommandSkill(root, command); err == nil {
							if b, err := os.ReadFile(abs); err == nil {
								collision = Digest(b) != Digest(canonical)
							}
						}
					}
				}
				if collision {
					v.Collisions = append(v.Collisions, rel)
					continue
				}
			}
		}
		filepath.Walk(folder, func(file string, info os.FileInfo, e error) error {
			if e != nil || info.IsDir() {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				if followed, err := os.Stat(file); err != nil || !followed.Mode().IsRegular() {
					return nil
				}
			}
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			rel, _ := filepath.Rel(root, file)
			rel = filepath.ToSlash(rel)
			if !confined(root, file) {
				v.Unsafe = append(v.Unsafe, rel)
			} else if !recorded[rel] {
				v.Orphans = append(v.Orphans, rel)
			}
			return nil
		})
	}
	for _, values := range [][]string{v.Drift, v.Gaps, v.Orphans, v.Stale, v.Unsafe, v.Legacy, v.LegacyOwned, v.LegacyRecorded, v.Collisions} {
		sort.Strings(values)
	}
	return v, nil
}

func plainCommandPath(root, rel string) bool {
	current := root

	// Check each component without following a link, including the file itself.
	parts := []string{}
	for rel != "." && rel != "" {
		parts = append(parts, path.Base(rel))
		rel = path.Dir(rel)
	}
	for i := len(parts) - 1; i >= 0; i-- {
		current = filepath.Join(current, parts[i])
		st, err := os.Lstat(current)
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if i == 0 {
			return st.Mode().IsRegular()
		}
		if !st.IsDir() {
			return false
		}
	}
	return false
}
