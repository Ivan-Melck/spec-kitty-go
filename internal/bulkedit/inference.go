package bulkedit

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type Phrase struct {
	Text   string
	Weight int
}
type Inference struct {
	Score, Threshold int
	Triggered        bool
	Matched          []Phrase
}

func wordMatch(keyword, text string) bool {
	if strings.ContainsAny(keyword, " -") {
		return strings.Contains(text, keyword)
	}
	word := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) }
	for offset := 0; offset <= len(text); {
		i := strings.Index(text[offset:], keyword)
		if i < 0 {
			return false
		}
		i += offset
		left, right := true, true
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:i])
			left = !word(r)
		}
		end := i + len(keyword)
		if end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			right = !word(r)
		}
		if left && right {
			return true
		}
		offset = i + 1
	}
	return false
}

func Infer(text string) Inference {
	r := Inference{Threshold: 4, Matched: []Phrase{}}
	lowered := strings.ToLower(text)
	groups := [][]string{
		{"rename across", "bulk edit", "codemod", "find-and-replace", "find and replace", "replace everywhere", "terminology migration", "rename all occurrences"},
		{"rename", "migrate", "replace all", "across the codebase", "globally", "sed", "search and replace"},
		{"update", "change", "modify", "refactor"},
	}
	for i, keywords := range groups {
		for _, keyword := range keywords {
			consumed := false
			for _, p := range r.Matched {
				consumed = consumed || strings.Contains(p.Text, keyword)
			}
			if i > 0 && consumed {
				continue
			}
			match := strings.Contains(lowered, keyword)
			if i > 0 {
				match = wordMatch(keyword, lowered)
			}
			if !match {
				continue
			}
			weight := 3 - i
			r.Matched = append(r.Matched, Phrase{keyword, weight})
			if weight > 1 {
				r.Score += weight
			}
			r.Triggered = r.Triggered || weight == 3 || keyword == "replace all" || keyword == "across the codebase"
		}
	}
	r.Triggered = r.Triggered || r.Score >= r.Threshold
	return r
}

func PlanningOwnedFile(value, slug string) bool {
	p := strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	for strings.HasPrefix(p, "./") {
		p = strings.TrimPrefix(p, "./")
	}
	return p == "occurrence_map.yaml" || strings.HasSuffix(p, "/occurrence_map.yaml") || p == "kitty-specs/"+slug || strings.HasPrefix(p, "kitty-specs/"+slug+"/")
}
