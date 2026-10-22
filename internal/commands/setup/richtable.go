package setup

import (
	"fmt"
	"io"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// The rich table renderers live in internal/rich (HeavyTable, TableX).

// alignText pads s to n runes ('l', 'c', 'r').
func alignText(s string, n int, j byte) string { return rich.AlignText(s, n, j) }

// printJSONRaw is console.emit_json (indent 2, ensure_ascii=False).
func printJSONRaw(w io.Writer, v any) {
	fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Options{Indent: 2}))
}
