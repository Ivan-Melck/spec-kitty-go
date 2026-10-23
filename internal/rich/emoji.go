package rich

import (
	"regexp"
	"strings"
)

// reEmoji is rich._emoji_replace's pattern (\S as Python's Unicode class).
var reEmoji = regexp.MustCompile(`(:([^\s\v\x1c-\x1f\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]*?)(?:-(emoji|text))?:)`)

// emojiReplace is rich._emoji_replace (no default variant): ":name:"
// codes become the emoji, ":name-text:" / ":name-emoji:" add the variant
// selector; unknown names stay as they are.
func emojiReplace(s string) string {
	if !strings.Contains(s, ":") {
		return s
	}
	return reEmoji.ReplaceAllStringFunc(s, func(m string) string {
		g := reEmoji.FindStringSubmatch(m)
		e, ok := emojiCodes[strings.ToLower(g[2])]
		if !ok {
			return m
		}
		switch g[3] {
		case "text":
			e += "︎"
		case "emoji":
			e += "️"
		}
		return e
	})
}
