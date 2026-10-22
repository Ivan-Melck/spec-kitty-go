package doctrine

import (
	"io"

	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// renderEntityMarkdown renders a glossary entity page the way
// console.print(rich.markdown.Markdown(text)) does on the glossary command's
// CliConsole(width=120): the closed set of constructs the page generator
// emits (headings, paragraphs, bullets, a simple table, a rule).
func renderEntityMarkdown(w io.Writer, md string) {
	rich.CliConsoleFor(w, glossaryWidth).Print(w, &rich.Markdown{Source: md})
}
