package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/languagecatalog"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func readLanguageConfig(root string) (*jsonx.Object, error) {
	content, err := os.ReadFile(kittify.ConfigPath(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := pyjson.DecodeUTF8(content); err != nil {
		return nil, err
	}
	value, err := yamlx.Parse(content)
	if err != nil {
		return nil, err
	}
	data, _ := value.(*jsonx.Object)
	return data, nil
}
func LanguagesKeyIsSet(root string) bool {
	info, err := os.Stat(kittify.ConfigPath(root))
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := readLanguageConfig(root)
	if err != nil {
		return true
	}
	if data == nil {
		return false
	}
	value, present := data.Get("languages")
	if !present || value == nil {
		return false
	}
	if list, ok := value.([]any); ok && len(list) == 0 {
		return false
	}
	return true
}
func ConfiguredLanguages(root string) []string {
	data, err := readLanguageConfig(root)
	p := kittify.ConfigPath(root)
	if err != nil {
		warnLog("Ignoring %s: it cannot be read (%s); detected languages apply.", p, err)
		return nil
	}
	if data == nil {
		return nil
	}
	value, present := data.Get("languages")
	if !present || value == nil {
		return nil
	}
	raw, ok := value.([]any)
	if !ok {
		warnLog("Ignoring 'languages' in %s: it must be a list of language tokens; detected languages apply.", p)
		return nil
	}
	var out []string
	for _, entry := range raw {
		token := ""
		if text, ok := entry.(string); ok {
			token = strings.ToLower(strings.TrimSpace(text))
		}
		reason := ""
		if token == "" {
			reason = "not a language token"
		} else if !languagecatalog.IsLanguage(token) && !languagecatalog.IsFamily(token) {
			reason = "not a catalog language"
		}
		if reason != "" {
			warnLog("Dropping %s from 'languages' in %s: %s.", pyjson.Repr(entry), p, reason)
		} else if !contains(out, token) {
			out = append(out, token)
		}
	}
	return out
}
func WriteConfiguredLanguages(root string, values []string) error {
	tokens := NormalizeLanguages(values)
	if len(tokens) == 0 {
		return fmt.Errorf("languages must name at least one language")
	}
	for _, token := range tokens {
		if !languagecatalog.IsLanguage(token) && !languagecatalog.IsFamily(token) {
			return fmt.Errorf("%s cannot be stored in 'languages': not a catalog language.", pyjson.Repr(token))
		}
	}
	return kittify.UpdateConfig(root, func(config *jsonx.Object) error { config.Set("languages", strs(tokens)); return nil })
}

type languageObservation struct {
	Depths map[string]int
	TS, JS int
}

func (o *languageObservation) Observe(name string, depth int) {
	if old, ok := o.Depths[name]; !ok || depth < old {
		o.Depths[name] = depth
	}
	switch languagecatalog.ForExtension(filepath.Ext(name)) {
	case "typescript":
		o.TS++
	case "javascript":
		o.JS++
	}
}
func languageSourceCounts(root string) map[string]int {
	counts := map[string]int{}
	var walk func(string)
	walk = func(dir string) {
		entries, err := readDirUnsorted(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			full := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.IsDir() {
				if !evidenceExcludedDirs[entry.Name()] {
					walk(full)
				}
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Stat(full); err == nil && target.IsDir() {
					continue
				}
			}
			if token := languagecatalog.ForExtension(filepath.Ext(entry.Name())); token != "" {
				counts[token]++
			}
		}
	}
	walk(root)
	return counts
}
func observedLanguages(root string, o languageObservation) []string {
	top := -1
	for name, depth := range o.Depths {
		if len(languagecatalog.ForManifest(name)) > 0 && (top < 0 || depth < top) {
			top = depth
		}
	}
	if top < 0 {
		return nil
	}
	candidatesAt := func(at int) ([]string, []string) {
		var candidates, shared []string
		for _, spec := range languagecatalog.Languages {
			for _, name := range spec.Manifests {
				depth, ok := o.Depths[name]
				if !ok || at >= 0 && depth != at {
					continue
				}
				owners := languagecatalog.ForManifest(name)
				token := owners[0]
				if name == "package.json" {
					if tsDepth, ok := o.Depths["tsconfig.json"]; ok && tsDepth == depth || o.TS+o.JS > 0 && float64(o.TS)/float64(o.TS+o.JS) > 0.5 {
						token = "typescript"
					}
				}
				if !contains(candidates, token) {
					candidates = append(candidates, token)
				}
				if len(owners) > 1 {
					shared = owners
				}
			}
		}
		return candidates, shared
	}
	candidates, shared := candidatesAt(top)
	if len(candidates) == 1 && len(shared) == 0 {
		all, anyShared := candidatesAt(-1)
		if len(all) == 1 && len(anyShared) == 0 {
			return candidates
		}
	}
	counts := languageSourceCounts(root)
	best := func(tokens []string) string {
		chosen := tokens[0]
		for _, token := range tokens[1:] {
			if counts[token] > counts[chosen] {
				chosen = token
			}
		}
		return chosen
	}
	if len(shared) > 0 {
		for i, token := range candidates {
			if token == shared[0] {
				candidates[i] = best(shared)
			}
		}
	}
	primary := best(candidates)
	total := 0
	for _, count := range counts {
		total += count
	}
	var rest []string
	for _, spec := range languagecatalog.Languages {
		if spec.Token != primary && total > 0 && float64(counts[spec.Token])/float64(total) >= 0.2 {
			rest = append(rest, spec.Token)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return counts[rest[i]] > counts[rest[j]] })
	return append([]string{primary}, rest...)
}
