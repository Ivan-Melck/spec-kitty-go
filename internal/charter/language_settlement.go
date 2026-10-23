package charter

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/languagecatalog"
)

type LanguageSettlement struct {
	Languages []string
	Source    string
	Detected  []string
	ToStore   []string
	Notice    *string
}

var languageAnswerSeparators = regexp.MustCompile(`[,;\s]+`)

func ParseLanguageAnswer(answer string) ([]string, error) {
	text := strings.ToLower(strings.TrimSpace(answer))
	if text == "" || text == "none" || text == "-" || text == "n/a" {
		return nil, nil
	}
	tokens := NormalizeLanguages(languageAnswerSeparators.Split(text, -1))
	var invalid []string
	for _, token := range tokens {
		if !languagecatalog.IsLanguage(token) && !languagecatalog.IsFamily(token) {
			invalid = append(invalid, token)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("Not a catalog language: %s. Use any of: %s.", strings.Join(invalid, ", "), strings.Join(languagecatalog.Tokens(), ", "))
	}
	return tokens, nil
}
func SettleProjectLanguages(root string, ask func(string) (string, error), onInvalid func(string)) (*LanguageSettlement, error) {
	stored := ConfiguredLanguages(root)
	detected := collectCodeSignals(root, false).Languages
	if ask == nil {
		if len(stored) > 0 {
			return &LanguageSettlement{Languages: stored, Source: "stored", Detected: detected}, nil
		}
		if LanguagesKeyIsSet(root) {
			notice := "'languages:' in .kittify/config.yaml is set but holds no usable language list, so it was left as it is. Set it to a list of catalog language tokens (for example 'languages: [python]') or remove the key, then re-run 'spec-kitty charter interview'."
			return &LanguageSettlement{Source: "none", Detected: detected, Notice: &notice}, nil
		}
		if len(detected) == 0 {
			return &LanguageSettlement{Source: "none", Detected: detected}, nil
		}
		notice := "Project languages stored as DETECTED, not confirmed: " + strings.Join(detected, ", ") + ". To change them, edit 'languages:' in .kittify/config.yaml or re-run 'spec-kitty charter interview' interactively."
		return &LanguageSettlement{Languages: detected, Source: "detected", Detected: detected, ToStore: detected, Notice: &notice}, nil
	}
	proposal := stored
	if len(proposal) == 0 {
		proposal = detected
	}
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := ask(strings.Join(proposal, ", "))
		if err != nil {
			return nil, err
		}
		confirmed, err := ParseLanguageAnswer(answer)
		if err != nil {
			if attempt == 2 {
				return nil, err
			}
			if onInvalid != nil {
				onInvalid(err.Error())
			}
			continue
		}
		if len(confirmed) == 0 {
			source := "none"
			if len(stored) > 0 {
				source = "stored"
			}
			return &LanguageSettlement{Languages: stored, Source: source, Detected: detected}, nil
		}
		if strings.Join(confirmed, "\x00") == strings.Join(stored, "\x00") {
			return &LanguageSettlement{Languages: stored, Source: "stored", Detected: detected}, nil
		}
		return &LanguageSettlement{Languages: confirmed, Source: "confirmed", Detected: detected, ToStore: confirmed}, nil
	}
	panic("unreachable")
}
func StoreLanguageSettlement(root string, settlement *LanguageSettlement) (bool, error) {
	if settlement.ToStore == nil || settlement.Source == "detected" && LanguagesKeyIsSet(root) {
		return false, nil
	}
	if err := WriteConfiguredLanguages(root, settlement.ToStore); err != nil {
		return false, err
	}
	return true, nil
}
