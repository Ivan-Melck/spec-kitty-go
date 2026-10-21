package charter

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// CharterExtensionAdvisory is language_advisory.CHARTER_EXTENSION_ADVISORY.
const CharterExtensionAdvisory = "No specialist guidance exists for this project's language; add tech-specific guidelines to your local charter (see 'Extend your charter for an unsupported language')."

const unknownLanguage = "unknown"

var (
	reservedLanguageTokens = map[string]bool{unknownLanguage: true}
	sentinelLanguageTokens = map[string]bool{"any": true, "all": true}
	placeholderAnswers     = map[string]bool{"": true, "[]": true, "-": true, "n/a": true, "na": true, "none": true,
		"null": true, "tbd": true, "language-agnostic": true, "language agnostic": true, "any": true, "all": true, "unknown": true}
)

// languagePatterns is language_scope._LANGUAGE_PATTERNS (each word is
// matched as \bword\b, guarded against a preceding "\w-").
var languagePatterns = []struct {
	lang string
	res  []*regexp.Regexp
}{
	{"python", words(`python`, `pytest`, `mypy`, `ruff`)},
	{"typescript", words(`typescript`, `tsc`)},
	{"javascript", words(`javascript`, `jest`, `node(?:\.js)?`, `npm`, `pnpm`, `yarn`)},
	{"rust", words(`rust`, `cargo`, `rustc`)},
	{"java", words(`java`, `junit`, `gradle`, `maven`)},
	{"swift", words(`swift`, `xctest`)},
	{"ruby", words(`ruby`, `rspec`, `rails`)},
	{"php", words(`php`, `phpunit`)},
}

func words(ps ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(ps))
	for i, p := range ps {
		out[i] = regexp.MustCompile(`\b` + p + `\b`)
	}
	return out
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// guarded is the (?<!\w-) compound-prefix guard.
func guarded(s string, start int) bool {
	rs := []rune(s[:start])
	n := len(rs)
	return !(n >= 2 && rs[n-1] == '-' && isWordRune(rs[n-2]))
}

func searchGuarded(re *regexp.Regexp, s string) bool {
	for _, m := range re.FindAllStringIndex(s, -1) {
		if guarded(s, m[0]) {
			return true
		}
	}
	return false
}

// NormalizeLanguages is scoping.normalize_languages.
func NormalizeLanguages(vals []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range vals {
		item := strings.ToLower(strings.TrimSpace(v))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

// ExtractDeclaredLanguages is language_scope.extract_declared_languages.
func ExtractDeclaredLanguages(text string) []string {
	h := strings.ToLower(text)
	var out []string
	for _, lp := range languagePatterns {
		for _, re := range lp.res {
			if searchGuarded(re, h) {
				out = append(out, lp.lang)
				break
			}
		}
	}
	return NormalizeLanguages(out)
}

func normalizeAnswer(v string) string { return strings.Join(strings.Fields(strings.ToLower(v)), " ") }

func isPlaceholderLanguageAnswer(v string) bool {
	n := normalizeAnswer(v)
	return placeholderAnswers[n] || strings.HasPrefix(n, "[needs clarification") || n == normalizeAnswer(PackagedDefaultAnswer("languages_frameworks"))
}

// pyListLiteral parses a Python list-of-strings literal ("['a', \"b\"]").
func pyListLiteral(text string) ([]string, bool) {
	inner := strings.TrimSpace(text[1 : len(text)-1])
	var out []string
	for inner != "" {
		q := inner[0]
		if q != '\'' && q != '"' {
			return nil, false
		}
		end := strings.IndexByte(inner[1:], q)
		if end < 0 {
			return nil, false
		}
		out = append(out, inner[1:1+end])
		inner = strings.TrimSpace(inner[end+2:])
		if inner == "" {
			break
		}
		if inner[0] != ',' {
			return nil, false
		}
		inner = strings.TrimSpace(inner[1:])
	}
	return out, true
}

func answerItems(raw string) []string {
	t := strings.TrimSpace(raw)
	if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") && len(t) >= 2 {
		if l, ok := pyListLiteral(t); ok {
			return l
		}
	}
	return []string{t}
}

var listSepRe = regexp.MustCompile(`[,;]|\s/\s`)

func realParts(item string) []string {
	if isPlaceholderLanguageAnswer(item) {
		return nil
	}
	var parts []string
	for _, piece := range listSepRe.Split(item, -1) {
		if isPlaceholderLanguageAnswer(piece) {
			continue
		}
		for _, sub := range strings.Split(piece, "/") {
			if !isPlaceholderLanguageAnswer(sub) {
				parts = append(parts, strings.TrimSpace(sub))
			}
		}
	}
	return parts
}

// wordTokens is _WORD_TOKEN.findall: maximal word runs not followed by
// '#'/'+' and not preceded by "\w-".
func wordTokens(s string) []string {
	rs := []rune(s)
	var out []string
	for i := 0; i < len(rs); {
		if !isWordRune(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isWordRune(rs[j]) {
			j++
		}
		ok := !(j < len(rs) && (rs[j] == '#' || rs[j] == '+'))
		if i >= 2 && rs[i-1] == '-' && isWordRune(rs[i-2]) {
			ok = false
		}
		if ok {
			out = append(out, string(rs[i:j]))
		}
		i = j
	}
	return out
}

// ScopedLanguageVocabulary is language_vocabulary.scoped_language_vocabulary.
func ScopedLanguageVocabulary(root string) map[string]bool {
	tokens := map[string]bool{}
	wordRe := regexp.MustCompile(`^\w+$`)
	add := func(b []byte) {
		if !strings.Contains(string(b), "applies_to_languages") {
			return
		}
		o, err := yamlx.ParseObject(b)
		if err != nil || o == nil {
			return
		}
		var items []string
		for _, x := range o.GetList("applies_to_languages") {
			if s, ok := x.(string); ok && wordRe.MatchString(strings.TrimSpace(s)) {
				items = append(items, s)
			}
		}
		for _, t := range NormalizeLanguages(items) {
			tokens[t] = true
		}
	}
	for _, k := range []dct.Kind{dct.Tactic, dct.Styleguide, dct.Toolguide, dct.Procedure, dct.AgentProfile} {
		bi := dct.BuiltIn()
		for _, p := range bi.Walk(k.Plural(), k.Suffix(), true) {
			if b, err := bi.ReadFile(p); err == nil {
				add(b)
			}
		}
		if root == "" {
			continue
		}
		var dirs []string
		for _, c := range []string{filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot)), filepath.Join(root, "src", "charter", "offering")} {
			if st, err := os.Stat(c); err == nil && st.IsDir() {
				dirs = append(dirs, filepath.Join(c, dct.ProjectKindDir(k)))
				break
			}
		}
		for _, org := range dct.OrgRoots(root) {
			dirs = append(dirs, filepath.Join(org, k.Plural()))
			if dct.ProjectKindDir(k) != k.Plural() {
				dirs = append(dirs, filepath.Join(org, dct.ProjectKindDir(k)))
			}
		}
		for _, d := range dirs {
			for _, p := range dct.DiskPack(d).Walk(".", k.Suffix(), true) {
				if b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(p))); err == nil {
					add(b)
				}
			}
		}
	}
	for t := range reservedLanguageTokens {
		delete(tokens, t)
	}
	for t := range sentinelLanguageTokens {
		delete(tokens, t)
	}
	return tokens
}

func languagesFromInterview(root string, iv *Interview) []string {
	var real []string
	raw, ok := iv.Answers.Get("languages_frameworks")
	if ok && raw != nil {
		for _, item := range answerItems(pyStrOf(raw)) {
			real = append(real, realParts(item)...)
		}
	}
	if answer := strings.Join(real, ", "); answer != "" {
		rec := map[string]bool{}
		for _, l := range ExtractDeclaredLanguages(answer) {
			rec[l] = true
		}
		vocab := ScopedLanguageVocabulary(root)
		for _, t := range wordTokens(strings.ToLower(answer)) {
			if vocab[t] {
				rec[t] = true
			}
		}
		if len(rec) == 0 {
			return []string{unknownLanguage}
		}
		out := make([]string, 0, len(rec))
		for l := range rec {
			out = append(out, l)
		}
		sort.Strings(out)
		return out
	}
	var vals []string
	for _, kv := range iv.Answers.Pairs() {
		vals = append(vals, pyStrOf(kv.Value))
	}
	if l := ExtractDeclaredLanguages(strings.Join(vals, "\n")); len(l) > 0 {
		return l
	}
	return nil
}

func isPackagedDefaultInterview(iv *Interview) bool {
	for _, kv := range iv.Answers.Pairs() {
		if pyStrOf(kv.Value) != PackagedDefaultAnswer(kv.Key) {
			return false
		}
	}
	return true
}

// readCompiledLanguages reads charter.yaml catalog.languages (nil = absent).
func readCompiledLanguages(root string) ([]string, bool) {
	b := LoadBundle(root)
	if b == nil {
		return nil, false
	}
	c := b.GetObject("catalog")
	if c == nil {
		return nil, false
	}
	v, ok := c.Get("languages")
	l, isList := v.([]any)
	if !ok || !isList {
		return nil, false
	}
	var vals []string
	for _, x := range l {
		vals = append(vals, pyStrOf(x))
	}
	out := NormalizeLanguages(vals)
	if out == nil {
		out = []string{}
	}
	return out, true
}

// InferRepoLanguages is language_scope.infer_repo_languages. The second
// result is false for "no signal" (None).
func InferRepoLanguages(root string, iv *Interview, preferInterview bool) ([]string, bool) {
	resolved := iv
	if preferInterview && resolved == nil && root != "" {
		resolved = ReadInterview(InterviewPath(root))
	}
	if !(preferInterview && resolved != nil) {
		if root != "" {
			if l, ok := readCompiledLanguages(root); ok {
				return l, true
			}
		}
		if resolved == nil || isPackagedDefaultInterview(resolved) {
			if root != "" {
				if d := ReadInterview(InterviewPath(root)); d != nil {
					resolved = d
				}
			}
		}
	}
	if resolved == nil {
		return nil, false
	}
	l := languagesFromInterview(root, resolved)
	return l, l != nil
}

// LacksSpecialistGuidance is language_scope.lacks_specialist_guidance.
func LacksSpecialistGuidance(langs []string, known bool, root string) bool {
	if !known {
		return false
	}
	norm := NormalizeLanguages(langs)
	if len(norm) == 0 {
		return false
	}
	vocab := ScopedLanguageVocabulary(root)
	for _, l := range norm {
		if !reservedLanguageTokens[l] && vocab[l] {
			return false
		}
	}
	return true
}
