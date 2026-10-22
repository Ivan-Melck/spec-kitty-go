package charsetnorm

import (
	"sort"
	"strings"
	"sync"
)

// Port of charset_normalizer/cd.py (coherence detection).

// CoherenceMatch is one (language, ratio) pair.
type CoherenceMatch struct {
	Language string
	Ratio    float64
}

type langData struct {
	lang        string
	chars       []rune
	rank        map[rune]int
	set         map[rune]bool
	haveAccents bool
	pureLatin   bool
}

var (
	langOnce  sync.Once
	langs     []*langData
	langIndex map[string]*langData
)

func loadLangs() {
	langIndex = map[string]*langData{}
	for _, f := range frequencies {
		ld := &langData{lang: f.lang, chars: f.chars, rank: map[rune]int{}, set: map[rune]bool{}, pureLatin: true}
		for i, c := range f.chars {
			ld.rank[c] = i
			ld.set[c] = true
		}
		// get_target_features
		for _, c := range f.chars {
			ci := getInfo(c)
			if !ld.haveAccents && ci.accentuated {
				ld.haveAccents = true
			}
			if ld.pureLatin && !ci.latin {
				ld.pureLatin = false
			}
		}
		langs = append(langs, ld)
		langIndex[f.lang] = ld
	}
}

// encodingLanguages is cd.encoding_languages / mb_encoding_languages for
// a codec name.
func encodingLanguages(name string) []string {
	return encodingLanguagesTable[name]
}

// alphabetLanguages is cd.alphabet_languages.
func alphabetLanguages(characters []rune, ignoreNonLatin bool) []string {
	langOnce.Do(loadLangs)
	set := make(map[rune]bool, len(characters))
	for _, c := range characters {
		set[c] = true
	}
	sourceAccents := false
	for _, c := range characters {
		if getInfo(c).accentuated {
			sourceAccents = true
			break
		}
	}
	type lr struct {
		lang  string
		ratio float64
	}
	var res []lr
	for _, ld := range langs {
		if ignoreNonLatin && !ld.pureLatin {
			continue
		}
		if !ld.haveAccents && sourceAccents {
			continue
		}
		match := 0
		for c := range ld.set {
			if set[c] {
				match++
			}
		}
		ratio := float64(match) / float64(len(ld.chars))
		if ratio >= 0.2 {
			res = append(res, lr{ld.lang, ratio})
		}
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].ratio > res[j].ratio })
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.lang
	}
	return out
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// charactersPopularityCompare is cd.characters_popularity_compare.
func charactersPopularityCompare(language string, ordered []rune) float64 {
	langOnce.Do(loadLangs)
	ld := langIndex[language]
	approved := 0
	orderedCount := len(ordered)
	targetCount := len(ld.chars)
	largeAlphabet := targetCount > 26
	largeThreshold := float64(targetCount) / 3
	projection := float64(targetCount) / float64(orderedCount)
	var commonLR, commonORR []int
	for pr, c := range ordered {
		if lr, ok := ld.rank[c]; ok {
			commonLR = append(commonLR, lr)
			commonORR = append(commonORR, pr)
		}
	}
	for k, rankInLang := range commonLR {
		rank := commonORR[k]
		proj := int(float64(rank) * projection)
		if !largeAlphabet && absInt(proj-rankInLang) > 4 {
			continue
		}
		if largeAlphabet && float64(absInt(proj-rankInLang)) < largeThreshold {
			approved++
			continue
		}
		if rankInLang == 0 {
			approved++
			continue
		}
		afterLen := targetCount - rankInLang
		before, after := 0, 0
		for m, lri := range commonLR {
			orri := commonORR[m]
			if lri < rankInLang {
				if orri < rank {
					before++
					if 5*before >= 2*rankInLang {
						approved++
						break
					}
				}
			} else if orri >= rank {
				after++
				if 5*after >= 2*afterLen {
					approved++
					break
				}
			}
		}
	}
	return float64(approved) / float64(len(ordered))
}

// alphaUnicodeSplit is cd.alpha_unicode_split.
func alphaUnicodeSplit(seq []rune) [][]rune {
	var order []string
	layers := map[string][]rune{}
	single := ""
	hasSingle := false
	multi := false
	prevRange := ""
	prevHas := false
	prevTarget := ""
	prevTargetOK := false
	for _, c := range seq {
		ci := getInfo(c)
		if !ci.alpha || !ci.hasRange {
			continue
		}
		r := ci.rng
		if prevHas && r == prevRange {
			if prevTargetOK {
				layers[prevTarget] = append(layers[prevTarget], c)
			}
			continue
		}
		target := ""
		found := false
		if multi {
			for _, d := range order {
				if !isSuspiciouslySuccessiveRange(d, true, r, true) {
					target, found = d, true
					break
				}
			}
		} else if hasSingle {
			if !isSuspiciouslySuccessiveRange(single, true, r, true) {
				target, found = single, true
			}
		}
		if !found {
			target = r
		}
		if _, ok := layers[target]; !ok {
			layers[target] = nil
			order = append(order, target)
			if !hasSingle {
				single, hasSingle = target, true
			} else {
				multi = true
			}
		}
		layers[target] = append(layers[target], c)
		prevRange, prevHas = r, true
		prevTarget, prevTargetOK = target, true
	}
	out := make([][]rune, 0, len(order))
	for _, k := range order {
		out = append(out, pyLower(layers[k]))
	}
	return out
}

// mergeCoherenceRatios is cd.merge_coherence_ratios.
func mergeCoherenceRatios(results [][]CoherenceMatch) []CoherenceMatch {
	var order []string
	per := map[string][]float64{}
	for _, r := range results {
		for _, m := range r {
			if _, ok := per[m.Language]; !ok {
				order = append(order, m.Language)
			}
			per[m.Language] = append(per[m.Language], m.Ratio)
		}
	}
	merged := make([]CoherenceMatch, 0, len(order))
	for _, l := range order {
		v := per[l]
		merged = append(merged, CoherenceMatch{l, pyRound(pySum(v)/float64(len(v)), 4)})
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Ratio > merged[j].Ratio })
	return merged
}

// filterAltCoherenceMatches is cd.filter_alt_coherence_matches.
func filterAltCoherenceMatches(results []CoherenceMatch) []CoherenceMatch {
	var order []string
	index := map[string][]float64{}
	for _, r := range results {
		name := strings.ReplaceAll(r.Language, "—", "")
		if _, ok := index[name]; !ok {
			order = append(order, name)
		}
		index[name] = append(index[name], r.Ratio)
	}
	anyMulti := false
	for _, k := range order {
		if len(index[k]) > 1 {
			anyMulti = true
			break
		}
	}
	if !anyMulti {
		return results
	}
	out := make([]CoherenceMatch, 0, len(order))
	for _, k := range order {
		mx := index[k][0]
		for _, v := range index[k][1:] {
			if v > mx {
				mx = v
			}
		}
		out = append(out, CoherenceMatch{k, mx})
	}
	return out
}

// coherenceRatio is cd.coherence_ratio. lgInclusion is the comma-joined
// language list, hasInclusion false for None.
func coherenceRatio(seq []rune, threshold float64, lgInclusion string, hasInclusion bool) []CoherenceMatch {
	var results []CoherenceMatch
	ignoreNonLatin := false
	sufficient := 0
	var inclusion []string
	if hasInclusion {
		inclusion = strings.Split(lgInclusion, ",")
	}
	for i, l := range inclusion {
		if l == "Latin Based" {
			ignoreNonLatin = true
			inclusion = append(append([]string(nil), inclusion[:i]...), inclusion[i+1:]...)
			break
		}
	}
	for _, layer := range alphaUnicodeSplit(seq) {
		var order []rune
		counts := map[rune]int{}
		for _, c := range layer {
			if _, ok := counts[c]; !ok {
				order = append(order, c)
			}
			counts[c]++
		}
		if len(layer) <= 32 { // TOO_SMALL_SEQUENCE
			continue
		}
		sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
		candidates := inclusion
		if len(candidates) == 0 {
			candidates = alphabetLanguages(order, ignoreNonLatin)
		}
		for _, lang := range candidates {
			ratio := charactersPopularityCompare(lang, order)
			if ratio < threshold {
				continue
			} else if ratio >= 0.8 {
				sufficient++
			}
			results = append(results, CoherenceMatch{lang, pyRound(ratio, 4)})
			if sufficient >= 3 {
				break
			}
		}
	}
	filtered := filterAltCoherenceMatches(results)
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Ratio > filtered[j].Ratio })
	return filtered
}
