package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// docsIndexPath is DEFAULT_INDEX_PATH (resolved against the cwd).
const docsIndexPath = "docs/development/docs-retrieval-index.yaml"

var divioTypes = []string{"tutorial", "how-to", "reference", "explanation", "none"}

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "docs", "Common Docs retrieval commands")
		g.Add(&cli.Command{
			Name:  "query",
			Short: "Query the Common Docs retrieval index for pages matching TERM.",
			Flags: []*cli.Flag{
				{Name: "json", Kind: cli.Bool, Help: "Emit machine-readable JSON via plain print (no Rich markup)."},
				{Name: "divio-type", Kind: cli.String, Help: "Restrict to pages of this Divio type (tutorial|how-to|reference|explanation|none)."},
				{Name: "section", Kind: cli.String, Help: "Restrict to pages containing an anchor with this slug."},
			},
			Args: []*cli.Arg{{Name: "term", Help: "Case-insensitive substring matched against title, anchor text/slug, and abstract.", Required: true, Metavar: "{term}"}},
			Run:  runDocsQuery,
		})
	})
}

type docAnchor struct {
	slug, text string
	level      int
}

type docEntry struct {
	path, title, divio, abstract string
	anchors                      []docAnchor
}

func anyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return jsonx.FormatFloat(t)
	}
	return fmt.Sprint(v)
}

func getStr(o *jsonx.Object, k string) string {
	v, ok := o.Get(k)
	if !ok {
		return ""
	}
	return anyStr(v)
}

func parseDocsIndex(b []byte) ([]docEntry, error) {
	if strings.TrimSpace(string(b)) == "" {
		return nil, nil
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, nil
	}
	pages, ok := mustGet(o, "pages").([]any)
	if !ok {
		return nil, nil
	}
	var out []docEntry
	for _, p := range pages {
		po, ok := p.(*jsonx.Object)
		if !ok {
			continue
		}
		e := docEntry{path: getStr(po, "path"), title: getStr(po, "title"), divio: getStr(po, "divio_type"), abstract: getStr(po, "abstract")}
		as, _ := mustGet(po, "anchors").([]any)
		for _, a := range as {
			ao, ok := a.(*jsonx.Object)
			if !ok {
				continue
			}
			lvl := 2
			if n, ok := ao.GetInt("level"); ok {
				lvl = n
			}
			e.anchors = append(e.anchors, docAnchor{getStr(ao, "slug"), getStr(ao, "text"), lvl})
		}
		out = append(out, e)
	}
	return out, nil
}

func matchingAnchors(e docEntry, term string, section *string) []docAnchor {
	var out []docAnchor
	for _, a := range e.anchors {
		hit := strings.Contains(strings.ToLower(a.text), term) || strings.Contains(strings.ToLower(a.slug), term)
		if hit || (section != nil && a.slug == *section) {
			out = append(out, a)
		}
	}
	return out
}

func runDocsQuery(ctx *cli.Context) error {
	term := strings.TrimSpace(ctx.Arg("term"))
	if term == "" {
		return cli.Usage(ctx.Cmd, "Invalid value: TERM must not be empty.")
	}
	divio := ctx.OptString("divio-type")
	if divio != nil {
		ok := false
		for _, d := range divioTypes {
			ok = ok || d == *divio
		}
		if !ok {
			return cli.Usage(ctx.Cmd, "Invalid value: Invalid --divio-type '%s'. Valid values: %s.", *divio, strings.Join(divioTypes, ", "))
		}
	}
	section := ctx.OptString("section")
	b, err := os.ReadFile(docsIndexPath)
	if err != nil {
		if _, serr := os.Stat(docsIndexPath); serr != nil {
			if _, derr := os.Stat("docs"); derr != nil {
				uconsole.Print(ctx.Stderr, "[red]Error: no docs/ tree found in the current directory.[/red]")
			} else {
				uconsole.Print(ctx.Stderr, "[red]Error: no Common Docs retrieval index at "+filepath.Clean(docsIndexPath)+".[/red] `docs query` requires a committed Common Docs retrieval index, which this project does not have (it is an opt-in artifact; in the Spec Kitty repository itself, `python scripts/docs/docs_index.py --write` generates it).")
			}
			return cli.Exit(1)
		}
		uconsole.Print(ctx.Stderr, fmt.Sprintf("[red]Error: could not read or parse the docs retrieval index at %s: %v[/red]", docsIndexPath, err))
		return cli.Exit(1)
	}
	entries, err := parseDocsIndex(b)
	if err != nil {
		uconsole.Print(ctx.Stderr, fmt.Sprintf("[red]Error: could not read or parse the docs retrieval index at %s: %v[/red]", docsIndexPath, err))
		return cli.Exit(1)
	}
	norm := strings.ToLower(term)
	var matches []docEntry
	for _, e := range entries {
		if divio != nil && e.divio != *divio {
			continue
		}
		if section != nil {
			has := false
			for _, a := range e.anchors {
				has = has || a.slug == *section
			}
			if !has {
				continue
			}
		}
		hit := strings.Contains(strings.ToLower(e.title), norm) || strings.Contains(strings.ToLower(e.abstract), norm)
		for _, a := range e.anchors {
			hit = hit || strings.Contains(strings.ToLower(a.text), norm) || strings.Contains(strings.ToLower(a.slug), norm)
		}
		if hit {
			matches = append(matches, e)
		}
	}
	if ctx.Bool("json") {
		out := []any{}
		for _, e := range matches {
			as := []any{}
			for _, a := range matchingAnchors(e, norm, section) {
				as = append(as, jsonx.NewObject("slug", a.slug, "text", a.text, "level", a.level))
			}
			out = append(out, jsonx.NewObject("path", e.path, "title", e.title, "divio_type", e.divio, "abstract", e.abstract, "anchors", as))
		}
		jsonOut(ctx.Stdout, out, jsonx.Pretty)
		return nil
	}
	raw := ctx.Arg("term")
	if len(matches) == 0 {
		gprint(ctx.Stdout, "[dim]No docs pages match '"+raw+"'.[/dim]")
		return nil
	}
	t := &rtable{Title: "Docs matching '" + raw + "'", Headers: []string{"Path", "Title", "Type", "Matching anchors"}}
	for _, e := range matches {
		var slugs []string
		for _, a := range matchingAnchors(e, norm, section) {
			slugs = append(slugs, a.slug)
		}
		at := "-"
		if len(slugs) > 0 {
			at = strings.Join(slugs, ", ")
		}
		t.Rows = append(t.Rows, []string{e.path, e.title, e.divio, at})
	}
	t.render(ctx.Stdout, glossaryWidth)
	return nil
}
