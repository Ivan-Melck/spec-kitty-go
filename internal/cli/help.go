package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// The help renderer reproduces typer's rich_utils.rich_format_help (typer
// 0.27, rich 15) as seen without a terminal: the click usage line, the
// command help text, then Arguments / Options / Commands panels laid out by
// rich's table algorithm, every line padded to the console width.

// Width is the rendering width for help and error panels (rich defaults to
// the terminal width, falling back to 80; COLUMNS overrides).
func Width() int { return rich.TyperWidth(false) }

// usagePieces are click's collect_usage_pieces (after "[OPTIONS]").
func usagePieces(c *Command) []string {
	pieces := []string{"[OPTIONS]"}
	for _, a := range c.Args {
		pieces = append(pieces, argUsage(a))
	}
	if c.isGroup() || c.GroupUsage {
		pieces = append(pieces, "COMMAND [ARGS]...")
	}
	return pieces
}

// usageLine is Command.get_usage: click's HelpFormatter.write_usage at the
// formatter width (min(terminal, 80) - 2, at least 50), without the final
// newline. It may span several lines.
func usageLine(c *Command) string { return usageLineFor(c, c.Path()) }

func usageLineFor(c *Command, path string) string {
	width := max(min(Width(), 80)-2, 50)
	prefix := "Usage: " + path + " "
	args := strings.Join(usagePieces(c), " ")
	if width >= rich.CellLen(prefix)+20 {
		return strings.Join(textWrap(args, width, prefix, strings.Repeat(" ", rich.CellLen(prefix))), "\n")
	}
	indent := strings.Repeat(" ", len("Usage: ")+4)
	return prefix + "\n" + strings.Join(textWrap(args, width, indent, indent), "\n")
}

// argBase is the argument's display name: its metavar without usage
// decoration ("{x}" / "[x]"), else its parameter name.
func argBase(a *Arg) string {
	m := a.Metavar
	if m == "" || strings.HasPrefix(m, "<") {
		// "<...>" metavars are type names (the panel's type column).
		return strings.ReplaceAll(a.Name, "-", "_")
	}
	if (strings.HasPrefix(m, "{") && strings.HasSuffix(m, "}")) || (strings.HasPrefix(m, "[") && strings.HasSuffix(m, "]")) {
		return m[1 : len(m)-1]
	}
	return m
}

// argUsage is TyperArgument.make_metavar(usage=True).
func argUsage(a *Arg) string {
	v := argBase(a)
	if a.Required {
		v = "{" + v + "}"
	} else {
		v = "[" + v + "]"
	}
	if a.Variadic {
		v += "..."
	}
	return v
}

// writeUsageError is typer's rich_format_error on the stderr console.
func writeUsageError(w io.Writer, e *UsageError) {
	c := e.Cmd
	con := rich.TyperConsole(true)
	hl := rich.OptionHighlighter{}
	if !e.NoContext && c != nil {
		path := c.Path()
		if e.parsing && c.ParseUsagePath != "" {
			path = c.ParseUsagePath
		}
		con.PrintWith(w, rich.PrintOptions{Style: rich.TyperStyleUsageCommand}, rich.Highlight(hl, usageLineFor(c, path)))
		help := "--help"
		con.PrintWith(w, rich.PrintOptions{Style: rich.TyperStyleErrorsSuggestion},
			fmt.Sprintf("Try [blue]'%s %s'[/] for help.", rich.EscapeMarkup(path), help))
	}
	con.Print(w, &rich.RichPanel{R: rich.Highlight(hl, e.Msg), BorderStyle: rich.TyperStyleErrorsBorder,
		Title: "Error", TitleAlign: "left"})
}

// printRenderable prints r on typer's stdout help console.
func printRenderable(w io.Writer, con *rich.Console, r any, style string) {
	con.PrintWith(w, rich.PrintOptions{Style: style}, r)
}

// cleandoc is Python's inspect.cleandoc.
func cleandoc(doc string) string {
	lines := strings.Split(expandTabs(doc), "\n")
	margin := -1
	for _, l := range lines[1:] {
		content := len(strings.TrimLeft(l, " "))
		if content > 0 {
			indent := len(l) - content
			if margin < 0 || indent < margin {
				margin = indent
			}
		}
	}
	lines[0] = strings.TrimLeft(lines[0], " ")
	if margin > 0 {
		for i := 1; i < len(lines); i++ {
			if len(lines[i]) >= margin {
				lines[i] = lines[i][margin:]
			} else {
				lines[i] = ""
			}
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		var b strings.Builder
		col := 0
		for _, r := range line {
			if r == '\t' {
				n := 8 - col%8
				b.WriteString(strings.Repeat(" ", n))
				col += n
				continue
			}
			b.WriteRune(r)
			col++
		}
		out = append(out, b.String())
	}
	return strings.Join(out, "\n")
}

// richText is typer's _make_rich_text in "rich" markup mode: the cleaned
// markup in a base style, highlighted by the OptionHighlighter.
func richText(s, style string) *rich.Text {
	return rich.HighlightText(rich.OptionHighlighter{}, rich.FromMarkupStyle(cleandoc(s), style))
}

// helpText returns the command's full help (click's Command.help).
func helpText(c *Command) string {
	if c.Long != "" {
		return c.Long
	}
	return c.Short
}

// listingHelp is the first paragraph shown in a parent's Commands panel.
func listingHelp(c *Command) *rich.Text {
	h := c.ShortHelp
	if h == "" {
		h = helpText(c)
	}
	first, _, _ := strings.Cut(cleandoc(h), "\n\n")
	if strings.HasPrefix(first, "\b") {
		first = strings.ReplaceAll(first, "\b\n", "")
	}
	return richText(strings.TrimSpace(first), "")
}

// helpBlock is typer's _get_help_text.
func helpBlock(c *Command) rich.Group {
	var g rich.Group
	if c.Deprecated {
		g = append(g, rich.NewStyledText("(deprecated) ", rich.TyperStyleDeprecated))
	}
	h := cleandoc(helpText(c))
	if i := strings.Index(h, "\f"); i >= 0 {
		h = h[:i]
	}
	first, rest, hasRest := strings.Cut(h, "\n\n")
	if !strings.HasPrefix(first, "\b") {
		first = strings.ReplaceAll(first, "\n", " ")
	}
	g = append(g, richText(strings.TrimSpace(first), ""))
	if hasRest {
		g = append(g, rich.NewText(""), richText(rest, rich.TyperStyleHelpText))
	}
	return g
}

// paramHelpText is the prose part of _get_parameter_help.
func paramHelpText(help string) string {
	paras := strings.Split(help, "\n\n")
	for i, p := range paras {
		if strings.HasPrefix(p, "\b") {
			paras[i] = strings.Trim(p, "\b\n") + "\n"
		} else {
			paras[i] = strings.TrimSpace(strings.ReplaceAll(p, "\n", " "))
		}
	}
	return strings.TrimSpace(strings.Join(paras, "\n"))
}

// pyFloatStr renders a float like Python str().
func pyFloatStr(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEn") {
		s += ".0"
	}
	return s
}

// flagDefault is typer's _get_default_string for an option ("" = none).
func flagDefault(f *Flag) string {
	if f.HideDefault {
		return ""
	}
	if f.DefaultText != "" {
		return f.DefaultText
	}
	switch d := f.Default.(type) {
	case nil:
		return ""
	case string:
		return d
	case int:
		return strconv.Itoa(d)
	case float64:
		return pyFloatStr(d)
	case bool:
		if f.Kind == Bool && f.Negatable {
			if d {
				return f.Name
			}
			return f.negName()
		}
		if d {
			return "True"
		}
		return ""
	case []string:
		return strings.Join(d, ", ")
	}
	return fmt.Sprint(f.Default)
}

// flagType is the Options panel type column (make_metavar; flags none).
func flagType(f *Flag) string {
	if f.Kind == Bool {
		return ""
	}
	if f.Metavar != "" {
		return f.Metavar
	}
	if f.Kind == Int && (f.Min != nil || f.Max != nil) {
		return "<int range> [" + describeRange(f.Min, f.Max) + "]"
	}
	if len(f.Choices) > 0 {
		return "<" + strings.Join(f.Choices, "|") + ">"
	}
	switch f.Kind {
	case Int:
		return "<int>"
	case Float:
		return "<float>"
	}
	return "<str>"
}

func helpColumns(help, def string, required bool) any {
	var items []any
	if help != "" {
		items = append(items, richText(paramHelpText(help), ""))
	}
	if def != "" {
		items = append(items, rich.NewStyledText("[default: "+def+"]", rich.TyperStyleOptionDefault))
	}
	if required {
		items = append(items, rich.NewStyledText("[required]", rich.TyperStyleRequiredLong))
	}
	return &rich.Columns{Items: items}
}

type paramRow struct {
	required bool
	cells    []any // long, short, secondary long, secondary short, type, help
}

func optionsPanel(title string, rows []paramRow) any {
	anyReq := false
	for _, r := range rows {
		anyReq = anyReq || r.required
	}
	t := rich.NewTable()
	t.Box, t.ShowHeader, t.Expand, t.PadEdge, t.Highlight = nil, false, true, false, true
	for _, r := range rows {
		cells := r.cells
		if anyReq {
			var req any = ""
			if r.required {
				req = rich.NewStyledText("*", rich.TyperStyleRequiredShort)
			}
			cells = append([]any{req}, cells...)
		}
		t.AddRow(cells...)
	}
	return &rich.RichPanel{R: t, BorderStyle: rich.TyperStyleOptionsBorder, Title: title, TitleAlign: "left"}
}

// typesText is typer's types column: Text(style=bold yellow,
// overflow=fold) with the metavar, through the TypesHighlighter.
func typesText(s string) *rich.Text {
	t := rich.NewStyledText(s, rich.TyperStyleTypes)
	t.Overflow = "fold"
	return rich.HighlightText(rich.TypesHighlighter{}, t)
}

func flagRow(f *Flag) paramRow {
	longs := []string{"--" + f.Name}
	for _, a := range f.Aliases {
		longs = append(longs, "--"+a)
	}
	short := ""
	if f.Short != "" {
		short = "-" + f.Short
	}
	secondary := ""
	if f.Kind == Bool && f.Negatable {
		secondary = "--" + f.negName()
	}
	hl, neg := rich.OptionHighlighter{}, rich.NegativeOptionHighlighter{}
	return paramRow{required: f.Required, cells: []any{
		rich.Highlight(hl, strings.Join(longs, ",")),
		rich.Highlight(hl, short),
		rich.Highlight(neg, secondary),
		rich.Highlight(neg, ""),
		typesText(flagType(f)),
		helpColumns(f.Help, flagDefault(f), f.Required),
	}}
}

func argRow(a *Arg) paramRow {
	typ := a.Type
	if typ == "" && strings.HasPrefix(a.Metavar, "<") {
		typ = a.Metavar
	}
	if typ == "" {
		typ = "<str>"
	}
	hl, neg := rich.OptionHighlighter{}, rich.NegativeOptionHighlighter{}
	return paramRow{required: a.Required, cells: []any{
		rich.Highlight(hl, ""),
		rich.Highlight(hl, argBase(a)),
		rich.Highlight(neg, ""),
		rich.Highlight(neg, ""),
		typesText(typ),
		helpColumns(a.Help, a.Default, a.Required),
	}}
}

// visibleSubcommands lists a group's commands in help order (the root
// group sorts them, like upstream's BannerGroup).
func visibleSubcommands(c *Command) []*Command {
	var out []*Command
	for _, s := range c.Subcommands {
		if !s.Hidden {
			out = append(out, s)
		}
	}
	if c.parent == nil {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	} else if order, ok := upstreamOrder[c.Path()]; ok {
		rank := map[string]int{}
		for i, n := range order {
			rank[n] = i
		}
		pos := func(s *Command) int {
			if r, ok := rank[s.Name]; ok {
				return r
			}
			return len(order)
		}
		sort.SliceStable(out, func(i, j int) bool { return pos(out[i]) < pos(out[j]) })
	}
	return out
}

func commandsPanel(cmds []*Command) any {
	cmdLen := 0
	anyDep := false
	for _, s := range cmds {
		cmdLen = max(cmdLen, len(s.Name))
		anyDep = anyDep || s.Deprecated
	}
	t := rich.NewTable()
	t.Box, t.ShowHeader, t.Expand, t.PadEdge = nil, false, true, false
	t.AddColumn("", rich.Column{Style: rich.TyperStyleCommandsFirstCol, NoWrap: true, Width: rich.IntP(cmdLen)})
	t.AddColumn("Description", rich.Column{Justify: "left", Ratio: rich.IntP(10)})
	for _, s := range cmds {
		name := rich.NewText(s.Name)
		if s.Deprecated {
			name = rich.NewStyledText(s.Name, rich.TyperStyleDeprecatedCmd)
		}
		row := []any{name, listingHelp(s)}
		if anyDep {
			var dep any
			if s.Deprecated {
				dep = rich.NewStyledText("(deprecated) ", rich.TyperStyleDeprecated)
			}
			row = append(row, dep)
		}
		t.AddRow(row...)
	}
	return &rich.RichPanel{R: t, BorderStyle: rich.TyperStyleCommandsBorder, Title: "Commands", TitleAlign: "left"}
}

// simpleHelpEnabled is upstream's _should_use_simple_help (root only).
func simpleHelpEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("SPEC_KITTY_SIMPLE_HELP")))
	switch raw {
	case "1", "true", "yes", "on", "y":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return Width() < 100
}

// invocationArgs is the command line Execute received (sys.argv[1:]).
var invocationArgs []string

// RootHelpBanner, when set, runs before the root group's rich help with the
// invocation's arguments (upstream's BannerGroup.format_help).
var RootHelpBanner func(w io.Writer, argv []string)

// WriteHelp renders typer-style help for c.
func WriteHelp(w io.Writer, c *Command) {
	if c.parent == nil && c.isGroup() && simpleHelpEnabled() {
		writeSimpleHelp(w, c)
		return
	}
	if c.parent == nil && RootHelpBanner != nil {
		RootHelpBanner(w, invocationArgs)
	}
	con := rich.TyperConsole(false)
	printRenderable(w, con, rich.Pad(rich.Highlight(rich.OptionHighlighter{}, usageLine(c)), 1), rich.TyperStyleUsageCommand)
	if strings.TrimSpace(helpText(c)) != "" || c.Deprecated {
		printRenderable(w, con, rich.Pad(rich.AlignLeft(helpBlock(c)), 0, 1, 1, 1), "")
	}
	var argPanels, optPanels []string
	argRows := map[string][]paramRow{}
	optRows := map[string][]paramRow{}
	for _, a := range c.Args {
		if a.Hidden {
			continue
		}
		p := a.Panel
		if p == "" {
			p = "Arguments"
		}
		if _, ok := argRows[p]; !ok {
			argPanels = append(argPanels, p)
		}
		argRows[p] = append(argRows[p], argRow(a))
	}
	helpFlag := &Flag{Name: "help", Short: "h", Kind: Bool, Help: "Show this message and exit."}
	if c.NoShortHelp {
		helpFlag.Short = ""
	}
	for _, f := range append(append([]*Flag(nil), c.Flags...), helpFlag) {
		if f.Hidden {
			continue
		}
		p := f.Panel
		if p == "" {
			p = "Options"
		}
		if _, ok := optRows[p]; !ok {
			optPanels = append(optPanels, p)
		}
		optRows[p] = append(optRows[p], flagRow(f))
	}
	emit := func(panels []string, rows map[string][]paramRow, def string) {
		if len(rows[def]) > 0 {
			printRenderable(w, con, optionsPanel(def, rows[def]), "")
		}
		for _, p := range panels {
			if p != def {
				printRenderable(w, con, optionsPanel(p, rows[p]), "")
			}
		}
	}
	emit(argPanels, argRows, "Arguments")
	emit(optPanels, optRows, "Options")
	if c.isGroup() {
		if cmds := visibleSubcommands(c); len(cmds) > 0 {
			printRenderable(w, con, commandsPanel(cmds), "")
		}
	}
	if c.Epilog != "" {
		printRenderable(w, con, rich.Pad(rich.AlignLeft(richText(c.Epilog, "")), 1), "")
	}
	// click's help callback then echoes the (empty) formatter buffer.
	fmt.Fprintln(w)
}

// writeSimpleHelp is upstream's _format_simple_help: click's plain
// HelpFormatter output for the root group.
func writeSimpleHelp(w io.Writer, c *Command) {
	width := max(min(Width(), 80)-2, 50)
	var b strings.Builder
	b.WriteString(usageLine(c) + "\n")
	if h := helpText(c); h != "" {
		b.WriteString("\n")
		b.WriteString(clickWriteText(h, width, 0))
	}
	var opts [][2]string
	for _, f := range c.Flags {
		if f.Hidden {
			continue
		}
		opts = append(opts, simpleFlagRecord(f))
	}
	opts = append(opts, [2]string{"-h, --help", "Show this message and exit."})
	b.WriteString("\nOptions:\n")
	b.WriteString(clickWriteDL(opts, width, 2))
	var cmds [][2]string
	for _, s := range visibleSubcommands(c) {
		cmds = append(cmds, [2]string{s.Name, shortHelpStr(s, 45)})
	}
	if len(cmds) > 0 {
		b.WriteString("\nCommands:\n")
		b.WriteString(clickWriteDL(cmds, width, 2))
	}
	io.WriteString(w, b.String())
}

func simpleFlagRecord(f *Flag) [2]string {
	names := []string{"--" + f.Name}
	for _, a := range f.Aliases {
		names = append(names, "--"+a)
	}
	if f.Short != "" {
		names = append([]string{"-" + f.Short}, names...)
	}
	rv := strings.Join(names, ", ")
	if f.Kind != Bool {
		rv += " " + flagType(f)
	}
	if f.Kind == Bool && f.Negatable {
		rv += " / --" + f.negName()
	}
	help := f.Help
	var extra []string
	if d := flagDefault(f); d != "" {
		extra = append(extra, "default: "+d)
	}
	if f.Required {
		extra = append(extra, "required")
	}
	if len(extra) > 0 {
		x := "[" + strings.Join(extra, "; ") + "]"
		if help != "" {
			help += "  " + x
		} else {
			help = x
		}
	}
	return [2]string{rv, help}
}

// shortHelpStr is click's Command.get_short_help_str(limit).
func shortHelpStr(c *Command, limit int) string {
	text := c.ShortHelp
	if text == "" {
		text = helpText(c)
		text = cleandoc(text)
		if i := strings.Index(text, "\f"); i >= 0 {
			text = text[:i]
		}
		text = makeDefaultShortHelp(text, limit)
	}
	return strings.TrimSpace(text)
}

// makeDefaultShortHelp is click.utils.make_default_short_help.
func makeDefaultShortHelp(help string, maxLength int) string {
	if paragraphEnd := strings.Index(help, "\n\n"); paragraphEnd >= 0 {
		help = help[:paragraphEnd]
	}
	words := strings.Fields(help)
	if len(words) == 0 {
		return ""
	}
	if words[0] == "\b" {
		words = words[1:]
	}
	runes := func(s string) int { return len([]rune(s)) }
	totalLength := 0
	lastIndex := len(words) - 1
	i := 0
	truncated := false
	for i = range words {
		totalLength += runes(words[i]) + boolInt(i > 0)
		if totalLength > maxLength {
			truncated = true
			break
		}
		if strings.HasSuffix(words[i], ".") {
			return strings.Join(words[:i+1], " ")
		}
		if totalLength == maxLength && i != lastIndex {
			truncated = true
			break
		}
	}
	if !truncated {
		return strings.Join(words, " ")
	}
	totalLength += len("...")
	for i > 0 {
		totalLength -= runes(words[i]) + boolInt(i > 0)
		if totalLength <= maxLength {
			break
		}
		i--
	}
	return strings.Join(words[:i], " ") + "..."
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// clickWriteText is HelpFormatter.write_text at the given indent:
// paragraphs rewrapped (click wrap_text with preserve_paragraphs).
func clickWriteText(text string, width, indent int) string {
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	for i, para := range clickParagraphs(text) {
		if i > 0 {
			b.WriteString("\n")
		}
		if para.raw {
			for _, l := range para.lines {
				b.WriteString(pad + l + "\n")
			}
			continue
		}
		ind := strings.Repeat(" ", para.indent)
		for _, l := range textWrap(strings.Join(para.lines, " "), width, pad+ind, pad+ind) {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

type clickPara struct {
	indent int
	raw    bool
	lines  []string
}

// clickParagraphs splits text like click's wrap_text(preserve_paragraphs).
func clickParagraphs(text string) []clickPara {
	var out []clickPara
	var buf []string
	indent := -1
	flush := func() {
		if len(buf) == 0 {
			return
		}
		raw := strings.TrimSpace(buf[0]) == "\b"
		lines := buf
		if raw {
			lines = buf[1:]
		} else {
			for i := range lines {
				lines[i] = strings.TrimSpace(lines[i])
			}
		}
		out = append(out, clickPara{indent: max(indent, 0), raw: raw, lines: append([]string(nil), lines...)})
		buf = nil
		indent = -1
	}
	for _, line := range strings.Split(expandTabs(text), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if indent < 0 {
			orig := len(line)
			indent = orig - len(strings.TrimLeft(line, " "))
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

// clickWriteDL is HelpFormatter.write_dl (col_max 30, col_spacing 2).
func clickWriteDL(rows [][2]string, width, indent int) string {
	firstCol := 0
	for _, r := range rows {
		firstCol = max(firstCol, rich.CellLen(r[0]))
	}
	firstCol = min(firstCol, 30) + 2
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(pad + r[0])
		if r[1] == "" {
			b.WriteString("\n")
			continue
		}
		if rich.CellLen(r[0]) <= firstCol-2 {
			b.WriteString(strings.Repeat(" ", firstCol-rich.CellLen(r[0])))
		} else {
			b.WriteString("\n" + strings.Repeat(" ", firstCol+indent))
		}
		textWidth := max(width-firstCol-2, 10)
		lines := textWrap(r[1], textWidth, "", "")
		b.WriteString(lines[0] + "\n")
		for _, l := range lines[1:] {
			b.WriteString(strings.Repeat(" ", firstCol+indent) + l + "\n")
		}
	}
	return b.String()
}
