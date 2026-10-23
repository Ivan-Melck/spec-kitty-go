package rich

import (
	"io"
	"os"
	"regexp"
	"strings"
)

// typer.rich_utils (typer 0.27) console, theme and highlighters: the help
// renderer and the usage-error panels print through TyperConsole.

// Typer styles (typer.rich_utils STYLE_*).
const (
	TyperStyleOption           = "bold cyan"
	TyperStyleSwitch           = "bold green"
	TyperStyleNegativeOption   = "bold magenta"
	TyperStyleNegativeSwitch   = "bold red"
	TyperStyleTypes            = "bold yellow"
	TyperStyleTypesSeparator   = "dim"
	TyperStyleUsage            = "yellow"
	TyperStyleUsageCommand     = "bold"
	TyperStyleDeprecated       = "red"
	TyperStyleDeprecatedCmd    = "dim"
	TyperStyleHelpText         = "dim"
	TyperStyleOptionDefault    = "dim"
	TyperStyleOptionEnvvar     = "dim yellow"
	TyperStyleRequiredShort    = "red"
	TyperStyleRequiredLong     = "dim red"
	TyperStyleOptionsBorder    = "dim"
	TyperStyleCommandsBorder   = "dim"
	TyperStyleCommandsFirstCol = "bold cyan"
	TyperStyleErrorsBorder     = "red"
	TyperStyleErrorsSuggestion = "dim"
	TyperStyleAborted          = "red"
)

var typerTheme = func() map[string]*Style {
	m := map[string]*Style{}
	for k, v := range map[string]string{
		"option": TyperStyleOption, "switch": TyperStyleSwitch,
		"negative_option": TyperStyleNegativeOption, "negative_switch": TyperStyleNegativeSwitch,
		"types": TyperStyleTypes, "types_sep": TyperStyleTypesSeparator, "usage": TyperStyleUsage,
	} {
		st, _ := ParseStyle(v)
		m[k] = st
	}
	return m
}()

// typerForceTerminal is typer.rich_utils.FORCE_TERMINAL (nil = None).
func typerForceTerminal() *bool {
	var f *bool
	if os.Getenv("GITHUB_ACTIONS") != "" || os.Getenv("FORCE_COLOR") != "" || os.Getenv("PY_COLORS") != "" {
		f = boolp(true)
	}
	if os.Getenv("_TYPER_FORCE_DISABLE_TERMINAL") != "" {
		f = boolp(false)
	}
	return f
}

// TyperConsole is typer.rich_utils._get_rich_console(stderr): the typer
// theme, the OptionHighlighter, FORCE_TERMINAL and TERMINAL_WIDTH.
func TyperConsole(stderr bool) *Console {
	fd := 1
	if stderr {
		fd = 2
	}
	term := IsTerminal(fd)
	if f := typerForceTerminal(); f != nil {
		term = *f
	}
	c := &Console{
		Width:       TyperWidth(stderr),
		Color:       colorSystemFor(term),
		NoColor:     noColorEnv(),
		Terminal:    term,
		Theme:       typerTheme,
		Highlighter: OptionHighlighter{},
	}
	return c
}

// colorSystemFor is Console._detect_color_system given is_terminal.
func colorSystemFor(term bool) ColorSystem {
	t := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if !term || t == "dumb" || t == "unknown" {
		return NoColor
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COLORTERM"))) {
	case "truecolor", "24bit":
		return TrueColor
	}
	colors := t
	if i := strings.LastIndex(t, "-"); i >= 0 {
		colors = t[i+1:]
	}
	switch colors {
	case "kitty", "256color":
		return EightBit
	}
	return Standard
}

const pyW = `\p{L}\p{N}_`

var (
	reTyperSwitch  = regexp.MustCompile(`(?:^|[^` + pyW + `])(?P<switch>-[` + pyW + `]+)`)
	reTyperOption  = regexp.MustCompile(`(?:^|[^` + pyW + `])(?P<option>--[` + pyW + `\-]+)`)
	reTyperTypes   = regexp.MustCompile(`(?P<types><[^>]+>)`)
	reTyperUsage   = regexp.MustCompile(`(?P<usage>Usage: )`)
	reTyperNSwitch = regexp.MustCompile(`(?:^|[^` + pyW + `])(?P<negative_switch>-[` + pyW + `]+)`)
	reTyperNOption = regexp.MustCompile(`(?:^|[^` + pyW + `])(?P<negative_option>--[` + pyW + `\-]+)`)
	reTypesSep1    = regexp.MustCompile(`^(?P<types_sep>(?:\[|<))`)
	reTypesSep2    = regexp.MustCompile(`(?P<types_sep>\|)`)
	reTypesSep3    = regexp.MustCompile(`(?P<types_sep>(?:\]|>))(?:\.\.\.)?$`)
)

// OptionHighlighter is typer.rich_utils.OptionHighlighter.
type OptionHighlighter struct{}

// Highlight applies the switch / option / types / usage patterns.
func (OptionHighlighter) Highlight(t *Text) {
	for _, re := range []*regexp.Regexp{reTyperSwitch, reTyperOption, reTyperTypes, reTyperUsage} {
		t.highlightRegex(re, "")
	}
}

// NegativeOptionHighlighter is typer.rich_utils.NegativeOptionHighlighter.
type NegativeOptionHighlighter struct{}

// Highlight applies the negative switch / option patterns.
func (NegativeOptionHighlighter) Highlight(t *Text) {
	t.highlightRegex(reTyperNSwitch, "")
	t.highlightRegex(reTyperNOption, "")
}

// TypesHighlighter is typer.rich_utils.TypesHighlighter.
type TypesHighlighter struct{}

// Highlight dims the brackets and separators of a type column.
func (TypesHighlighter) Highlight(t *Text) {
	for _, re := range []*regexp.Regexp{reTypesSep1, reTypesSep2, reTypesSep3} {
		t.highlightRegex(re, "")
	}
}

// TyperAbort is typer.rich_utils.rich_abort_error: "Aborted." in red on
// the stderr console.
func TyperAbort(w io.Writer) {
	TyperConsole(true).PrintWith(w, PrintOptions{Style: TyperStyleAborted}, "Aborted.")
}
