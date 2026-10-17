package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Width is the rendering width for help and error panels (rich defaults to
// the terminal width, falling back to 80; COLUMNS overrides).
func Width() int {
	if v, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && v > 20 {
		return v
	}
	return 80
}

func usageLine(c *Command) string {
	var b strings.Builder
	b.WriteString("Usage: ")
	b.WriteString(c.Path())
	b.WriteString(" [OPTIONS]")
	if c.isGroup() {
		b.WriteString(" COMMAND [ARGS]...")
		return b.String()
	}
	for _, a := range c.Args {
		m := argMeta(a)
		if a.Metavar == "" {
			m = strings.ToUpper(strings.ReplaceAll(a.Name, "-", "_"))
		}
		if a.Variadic {
			m += "..."
		}
		if !a.Required {
			m = "[" + m + "]"
		}
		b.WriteString(" " + m)
	}
	return b.String()
}

func writeUsageError(w io.Writer, e *UsageError) {
	c := e.Cmd
	fmt.Fprintln(w, usageLine(c))
	fmt.Fprintf(w, "Try '%s --help' for help.\n", c.Path())
	panel(w, "Error", []string{e.Msg})
}

// panel draws a rich-style rounded box.
func panel(w io.Writer, title string, lines []string) {
	width := Width()
	inner := width - 4
	head := "╭─ " + title + " "
	fmt.Fprintln(w, head+strings.Repeat("─", max(0, width-utf8.RuneCountInString(head)-1))+"╮")
	for _, l := range lines {
		for _, wl := range wrap(l, inner) {
			fmt.Fprintf(w, "│ %s%s │\n", wl, strings.Repeat(" ", max(0, inner-utf8.RuneCountInString(wl))))
		}
	}
	fmt.Fprintln(w, "╰"+strings.Repeat("─", width-2)+"╯")
}

// tablePanel draws a panel whose rows are columns; the last column wraps.
func tablePanel(w io.Writer, title string, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	width := Width()
	inner := width - 4
	ncol := 0
	for _, r := range rows {
		if len(r) > ncol {
			ncol = len(r)
		}
	}
	colw := make([]int, ncol)
	for _, r := range rows {
		for i := 0; i < len(r)-1; i++ {
			if n := utf8.RuneCountInString(r[i]); n > colw[i] {
				colw[i] = n
			}
		}
	}
	var lines []string
	for _, r := range rows {
		prefix := ""
		for i := 0; i < len(r)-1; i++ {
			if colw[i] == 0 {
				continue
			}
			prefix += r[i] + strings.Repeat(" ", colw[i]-utf8.RuneCountInString(r[i])) + " "
		}
		indent := 0
		for i := 0; i < ncol-1; i++ {
			if colw[i] > 0 {
				indent += colw[i] + 1
			}
		}
		last := ""
		if len(r) > 0 {
			last = r[len(r)-1]
		}
		avail := inner - indent
		if avail < 20 {
			avail = 20
		}
		wl := wrap(last, avail)
		for j, l := range wl {
			if j == 0 {
				lines = append(lines, strings.TrimRight(prefix+strings.Repeat(" ", max(0, indent-utf8.RuneCountInString(prefix)))+l, " "))
			} else {
				lines = append(lines, strings.Repeat(" ", indent)+l)
			}
		}
	}
	head := "╭─ " + title + " "
	fmt.Fprintln(w, head+strings.Repeat("─", max(0, width-utf8.RuneCountInString(head)-1))+"╮")
	for _, l := range lines {
		if utf8.RuneCountInString(l) > inner {
			l = string([]rune(l)[:inner])
		}
		fmt.Fprintf(w, "│ %s%s │\n", l, strings.Repeat(" ", max(0, inner-utf8.RuneCountInString(l))))
	}
	fmt.Fprintln(w, "╰"+strings.Repeat("─", width-2)+"╯")
}

func wrap(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, wd := range words {
			if line == "" {
				line = wd
			} else if utf8.RuneCountInString(line)+1+utf8.RuneCountInString(wd) <= width {
				line += " " + wd
			} else {
				out = append(out, line)
				line = wd
			}
			for utf8.RuneCountInString(line) > width {
				r := []rune(line)
				out = append(out, string(r[:width]))
				line = string(r[width:])
			}
		}
		out = append(out, line)
	}
	return out
}

// WriteHelp renders typer-style help for c.
func WriteHelp(w io.Writer, c *Command) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, " "+usageLine(c))
	fmt.Fprintln(w)
	long := c.Long
	if long == "" {
		long = c.Short
	}
	if long != "" {
		for _, l := range strings.Split(strings.TrimRight(long, "\n"), "\n") {
			fmt.Fprintln(w, " "+l)
		}
		fmt.Fprintln(w)
	}
	var argRows [][]string
	anyReq := false
	for _, a := range c.Args {
		if a.Required {
			anyReq = true
		}
	}
	for _, f := range c.Flags {
		if f.Required && !f.Hidden {
			anyReq = true
		}
	}
	for _, a := range c.Args {
		req := ""
		if a.Required {
			req = "*"
		}
		help := a.Help
		if a.Required {
			help += " [required]"
		}
		row := []string{req, strings.ReplaceAll(a.Name, "-", "_"), "TEXT", strings.TrimSpace(help)}
		if !anyReq {
			row = row[1:]
		}
		argRows = append(argRows, row)
	}
	if len(argRows) > 0 {
		tablePanel(w, "Arguments", argRows)
	}
	var optRows [][]string
	for _, f := range c.Flags {
		if f.Hidden {
			continue
		}
		name := "--" + f.Name
		if f.Kind == Bool && f.Negatable {
			name += "/--" + f.negName()
		}
		for _, a := range f.Aliases {
			name += ", --" + a
		}
		if f.Short != "" {
			name += " -" + f.Short
		}
		meta := ""
		switch f.Kind {
		case String, StringList:
			meta = "TEXT"
			if f.Metavar != "" {
				meta = f.Metavar
			}
		case Int:
			meta = "INTEGER"
		case Float:
			meta = "FLOAT"
		}
		help := f.Help
		if len(f.Choices) > 0 {
			help += " [" + strings.Join(f.Choices, "|") + "]"
		}
		if f.Default != nil {
			switch d := f.Default.(type) {
			case string:
				if d != "" {
					help += " [default: " + d + "]"
				}
			case int:
				help += " [default: " + strconv.Itoa(d) + "]"
			case bool:
				if f.Negatable {
					if d {
						help += " [default: " + f.Name + "]"
					} else {
						help += " [default: " + f.negName() + "]"
					}
				}
			}
		}
		if f.Required {
			help += " [required]"
		}
		req := ""
		if f.Required {
			req = "*"
		}
		row := []string{req, name, meta, strings.TrimSpace(help)}
		if !anyReq {
			row = row[1:]
		}
		optRows = append(optRows, row)
	}
	helpRow := []string{"", "--help -h", "", "Show this message and exit."}
	if !anyReq {
		helpRow = helpRow[1:]
	}
	optRows = append(optRows, helpRow)
	tablePanel(w, "Options", optRows)
	if c.isGroup() {
		var cmdRows [][]string
		for _, s := range c.Subcommands {
			if s.Hidden {
				continue
			}
			cmdRows = append(cmdRows, []string{s.Name, firstLine(s.Short)})
		}
		tablePanel(w, "Commands", cmdRows)
	}
	if c.Epilog != "" {
		fmt.Fprintln(w, " "+c.Epilog)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
