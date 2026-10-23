package rich

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type renderCase struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Box         *string
	ShowLines   bool   `json:"show_lines"`
	ShowHeader  bool   `json:"show_header"`
	ShowEdge    bool   `json:"show_edge"`
	Expand      bool   `json:"expand"`
	HeaderStyle string `json:"header_style"`
	BorderStyle string `json:"border_style"`
	Padding     []int  `json:"padding"`
	PadEdge     bool   `json:"pad_edge"`
	Highlight   bool   `json:"highlight"`
	RowStyle    string `json:"row_style"`
	Width       int    `json:"width"`
	Columns     []struct {
		Header   string `json:"header"`
		Style    string `json:"style"`
		Justify  string `json:"justify"`
		NoWrap   bool   `json:"no_wrap"`
		Width    *int   `json:"width"`
		MinWidth *int   `json:"min_width"`
		Ratio    *int   `json:"ratio"`
	} `json:"columns"`
	Rows       [][]string `json:"rows"`
	Content    string     `json:"content"`
	TitleAlign string     `json:"title_align"`
	Fit        bool       `json:"fit"`
	Root       *treeSpec  `json:"root"`
	GuideStyle string     `json:"guide_style"`
	Pad        []int      `json:"pad"`
	Style      string     `json:"style"`
	Items      []string   `json:"items"`
}

type treeSpec struct {
	Label    string      `json:"label"`
	Children []*treeSpec `json:"children"`
}

var boxByName = map[string]*Box{"HEAVY_HEAD": BoxHeavyHead, "ROUNDED": BoxRounded, "SQUARE": BoxSquare, "SIMPLE": BoxSimple}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (c *renderCase) build() any {
	switch c.Kind {
	case "table":
		t := NewTable()
		if c.Title != "" {
			t.Title = c.Title
		}
		t.Box = nil
		if c.Box != nil {
			t.Box = boxByName[*c.Box]
		}
		t.ShowLines, t.ShowHeader, t.ShowEdge, t.Expand = c.ShowLines, c.ShowHeader, c.ShowEdge, c.Expand
		t.HeaderStyle, t.BorderStyle = c.HeaderStyle, nilIfEmpty(c.BorderStyle)
		t.SetPadding(c.Padding...)
		t.PadEdge, t.Highlight = c.PadEdge, c.Highlight
		for _, col := range c.Columns {
			var st any
			if col.Style != "" {
				st = col.Style
			}
			t.AddColumn(col.Header, Column{Style: st, Justify: col.Justify, NoWrap: col.NoWrap, Width: col.Width, MinWidth: col.MinWidth, Ratio: col.Ratio})
		}
		for i, r := range c.Rows {
			cells := make([]any, len(r))
			for k, x := range r {
				cells[k] = x
			}
			var rs any
			if i%2 == 1 {
				rs = nilIfEmpty(c.RowStyle)
			}
			t.AddRowStyled(rs, false, cells...)
		}
		return t
	case "panel":
		p := &RichPanel{R: c.Content, TitleAlign: c.TitleAlign, Fit: c.Fit, Padding: c.Padding}
		if c.Title != "" {
			p.Title = c.Title
		}
		if c.BorderStyle != "" {
			p.BorderStyle = c.BorderStyle
		}
		return p
	case "tree":
		gs := any("tree.line")
		if c.GuideStyle != "" {
			gs = c.GuideStyle
		}
		root := &Tree{Label: c.Root.Label, GuideStyle: gs}
		var add func(t *Tree, s *treeSpec)
		add = func(t *Tree, s *treeSpec) {
			for _, ch := range s.Children {
				add(t.Add(ch.Label), ch)
			}
		}
		add(root, c.Root)
		return root
	case "padding":
		p := Pad(c.Content, c.Pad...)
		if c.Style != "" {
			p.Style = c.Style
		}
		return p
	}
	var items []any
	for _, x := range c.Items {
		items = append(items, x)
	}
	return &Columns{Items: items}
}

// TestRenderCorpus: tables, panels, trees, padding and columns rendered
// like rich for each colour system (testdata/render.json.gz from
// testdata/rendergen.py, one process per colour system).
func TestRenderCorpus(t *testing.T) {
	f, err := os.Open("testdata/render.json.gz")
	if err != nil {
		t.Skip("corpus not present")
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Case renderCase        `json:"case"`
		Out  map[string]string `json:"out"`
	}
	if err := json.NewDecoder(gz).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	systems := map[string]ColorSystem{"standard": Standard, "256": EightBit, "truecolor": TrueColor, "none": NoColor}
	fails := 0
	for i, c := range cases {
		for name, want := range c.Out {
			if strings.HasPrefix(want, "ERR ") {
				continue
			}
			var b strings.Builder
			con := &Console{Width: c.Case.Width, Color: systems[name], Highlighter: ReprHighlighter{}}
			con.Print(&b, c.Case.build())
			if got := b.String(); got != want {
				fails++
				if fails <= 8 {
					t.Errorf("case %d (%s, %s): %+v\nwant %q\ngot  %q", i, c.Case.Kind, name, c.Case, want, got)
				}
			}
		}
	}
	t.Logf("%d cases x %d systems", len(cases), len(systems))
	if fails > 0 {
		t.Errorf("%d mismatches", fails)
	}
}
