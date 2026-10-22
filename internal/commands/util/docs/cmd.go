// Package docs implements `spec-kitty docs query`: a case-insensitive
// substring query over the generated Common Docs retrieval index
// (docs/development/docs-retrieval-index.yaml, read from the current
// directory).
package docs

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"errors"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/encoding"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// DefaultIndexPath is the index location relative to the current directory.
const DefaultIndexPath = "docs/development/docs-retrieval-index.yaml"

// DivioTypes are the valid --divio-type values (DivioType enum order).
var DivioTypes = []string{"tutorial", "how-to", "reference", "explanation", "none"}

const generatorHint = "`docs query` requires a committed Common Docs retrieval index, which this " +
	"project does not have (it is an opt-in artifact; in the Spec Kitty " +
	"repository itself, `python scripts/docs/docs_index.py --write` generates it)"

// tableWidth is the fixed width of the human-output console.
const tableWidth = 120

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "docs", "Common Docs retrieval commands")
		g.Add(&cli.Command{
			Name:  "query",
			Short: "Query the Common Docs retrieval index for pages matching TERM.",
			Args: []*cli.Arg{{Name: "term", Metavar: "{term}", ErrorHint: "term", Required: true,
				Help: "Case-insensitive substring matched against title, anchor text/slug, and abstract."}},
			Flags: []*cli.Flag{
				{Name: "json", Kind: cli.Bool, Help: "Emit machine-readable JSON via plain print (no Rich markup)."},
				{Name: "divio-type", Kind: cli.String, Metavar: "<str>", Help: "Restrict to pages of this Divio type (tutorial|how-to|reference|explanation|none)."},
				{Name: "section", Kind: cli.String, Metavar: "<str>", Help: "Restrict to pages containing an anchor with this slug."},
			},
			Run: runQuery,
		})
	})
}

// Anchor is one heading anchor of a page.
type Anchor struct {
	Slug, Text string
	Level      int
}

// Entry is one page of the index.
type Entry struct {
	Path, Title, DivioType, Abstract string
	Anchors                          []Anchor
}

// pyInt is int(value) with the TypeError/ValueError fallback to def.
func pyInt(v any, def int) int {
	switch t := v.(type) {
	case bool:
		if t {
			return 1
		}
		return 0
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		s := strings.ReplaceAll(strings.TrimSpace(t), "_", "")
		if n, err := strconv.Atoi(strings.TrimPrefix(s, "+")); err == nil && s != "" {
			return n
		}
	}
	return def
}

func get(o *jsonx.Object, key string, def any) any {
	if v, ok := o.Get(key); ok {
		return v
	}
	return def
}

// ParseIndex is parse_index: degenerate documents yield no entries; a YAML
// syntax error is returned.
func ParseIndex(text string) ([]Entry, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	loaded, err := yamlx.ParseWith([]byte(text), yamlx.ParseOptions{Safe: true}) // YAML(typ="safe").load(text)
	if err != nil {
		return nil, err
	}
	root, ok := loaded.(*jsonx.Object)
	if !ok {
		return nil, nil
	}
	pages, ok := get(root, "pages", nil).([]any)
	if !ok {
		return nil, nil
	}
	var out []Entry
	for _, p := range pages {
		po, ok := p.(*jsonx.Object)
		if !ok {
			continue
		}
		e := Entry{
			Path:      pyjson.Str(get(po, "path", "")),
			Title:     pyjson.Str(get(po, "title", "")),
			DivioType: pyjson.Str(get(po, "divio_type", "")),
			Abstract:  pyjson.Str(get(po, "abstract", "")),
		}
		rawAnchors := get(po, "anchors", nil)
		if !pyjson.Truthy(rawAnchors) {
			rawAnchors = []any{}
		}
		if list, ok := rawAnchors.([]any); ok {
			for _, ra := range list {
				ao, ok := ra.(*jsonx.Object)
				if !ok {
					continue
				}
				e.Anchors = append(e.Anchors, Anchor{
					Slug:  pyjson.Str(get(ao, "slug", "")),
					Text:  pyjson.Str(get(ao, "text", "")),
					Level: pyInt(get(ao, "level", int64(2)), 2),
				})
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func matchesTerm(e Entry, term string) bool {
	if strings.Contains(strings.ToLower(e.Title), term) || strings.Contains(strings.ToLower(e.Abstract), term) {
		return true
	}
	for _, a := range e.Anchors {
		if strings.Contains(strings.ToLower(a.Text), term) || strings.Contains(strings.ToLower(a.Slug), term) {
			return true
		}
	}
	return false
}

// Query is DocsIndexStore.query (divioType/section nil = no filter).
func Query(entries []Entry, term string, divioType, section *string) []Entry {
	normalized := strings.ToLower(strings.TrimSpace(term))
	var out []Entry
	for _, e := range entries {
		if divioType != nil && e.DivioType != *divioType {
			continue
		}
		if section != nil {
			hit := false
			for _, a := range e.Anchors {
				if a.Slug == *section {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		if matchesTerm(e, normalized) {
			out = append(out, e)
		}
	}
	return out
}

func matchingAnchors(e Entry, term string, section *string) []Anchor {
	var out []Anchor
	for _, a := range e.Anchors {
		termHit := strings.Contains(strings.ToLower(a.Text), term) || strings.Contains(strings.ToLower(a.Slug), term)
		if termHit || (section != nil && a.Slug == *section) {
			out = append(out, a)
		}
	}
	return out
}

func errPrint(w io.Writer, s string) { rich.Print(w, s) }

func runQuery(ctx *cli.Context) error {
	term := ctx.Arg("term")
	normalized := strings.TrimSpace(term)
	if normalized == "" {
		return cli.Usage(ctx.Cmd, "Invalid value: TERM must not be empty.")
	}
	divio := ctx.OptString("divio-type")
	if divio != nil {
		valid := false
		for _, d := range DivioTypes {
			valid = valid || d == *divio
		}
		if !valid {
			return cli.Usage(ctx.Cmd, "Invalid value: Invalid --divio-type '%s'. Valid values: %s.", *divio, strings.Join(DivioTypes, ", "))
		}
	}
	section := ctx.OptString("section")

	if _, err := os.Stat(DefaultIndexPath); err != nil {
		if _, err := os.Stat("docs"); err != nil {
			errPrint(ctx.Stderr, "[red]Error: no docs/ tree found in the current directory.[/red]")
		} else {
			errPrint(ctx.Stderr, fmt.Sprintf("[red]Error: no Common Docs retrieval index at %s.[/red] %s.", DefaultIndexPath, generatorHint))
		}
		return cli.Exit(1)
	}
	readFail := func(msg string) error {
		errPrint(ctx.Stderr, fmt.Sprintf("[red]Error: could not read or parse the docs retrieval index at %s: %s[/red]", DefaultIndexPath, msg))
		return cli.Exit(1)
	}
	b, err := os.ReadFile(DefaultIndexPath)
	if err != nil {
		return readFail(encoding.PyOSError(err))
	}
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		rich.Traceback(ctx.Stderr, "UnicodeDecodeError", err.Error(), rich.Frame{Name: "query_command", Line: 202}, rich.Frame{Name: "_load_store", Line: 92})
		return cli.Exit(1)
	}
	entries, err := ParseIndex(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	if err != nil {
		var pe *pyyaml.PyError
		if errors.As(err, &pe) {
			// not a YAMLError: uncaught
			rich.Traceback(ctx.Stderr, pe.Type, pe.Msg, rich.Frame{Name: "query_command", Line: 202}, rich.Frame{Name: "_load_store", Line: 92})
			return cli.Exit(1)
		}
		return readFail(err.Error())
	}
	matches := Query(entries, normalized, divio, section)

	if ctx.Bool("json") {
		lower := strings.ToLower(normalized)
		out := make([]any, 0, len(matches))
		for _, e := range matches {
			anchors := []any{}
			for _, a := range matchingAnchors(e, lower, section) {
				anchors = append(anchors, jsonx.NewObject("slug", a.Slug, "text", a.Text, "level", a.Level))
			}
			out = append(out, jsonx.NewObject("path", e.Path, "title", e.Title, "divio_type", e.DivioType, "abstract", e.Abstract, "anchors", anchors))
		}
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(out, jsonx.Pretty))
		return nil
	}

	if len(matches) == 0 {
		rich.CliConsoleFor(ctx.Stdout, tableWidth).Print(ctx.Stdout, fmt.Sprintf("[dim]No docs pages match '%s'.[/dim]", term))
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(term))
	tbl := rich.NewTable()
	tbl.Title = fmt.Sprintf("Docs matching '%s'", term)
	tbl.AddColumn("Path", rich.Column{Style: "cyan"})
	tbl.AddColumn("Title", rich.Column{Style: "bold"})
	tbl.AddColumn("Type", rich.Column{Style: "yellow"})
	tbl.AddColumn("Matching anchors", rich.Column{})
	for _, e := range matches {
		var slugs []string
		for _, a := range matchingAnchors(e, lower, section) {
			slugs = append(slugs, a.Slug)
		}
		anchorText := "-"
		if len(slugs) > 0 {
			anchorText = strings.Join(slugs, ", ")
		}
		tbl.AddRow(e.Path, e.Title, e.DivioType, anchorText)
	}
	rich.CliConsoleFor(ctx.Stdout, tableWidth).Print(ctx.Stdout, tbl)
	return nil
}
