package cli

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Parsing follows click 8.5 (as vendored by typer 0.27): options are parsed
// first (parse errors are raised immediately), then eager --help, then the
// given parameters are converted in command-line order, then missing
// required parameters are reported in declaration order, then extra
// positionals.

// badOptionUsage is click's BadOptionUsage raised by the parser: it carries
// no context, so only the Error panel is printed.
func badOptionUsage(cmd *Command, format string, a ...any) error {
	return &UsageError{Cmd: cmd, Msg: fmt.Sprintf(format, a...), NoContext: true}
}

// parseInto consumes argv for cmd. For groups it stops at the first
// positional (the subcommand name) and returns it.
func parseInto(ctx *Context, cmd *Command, argv []string) ([]string, string, error) {
	var positionals []string
	var convErr error // first conversion error, in command-line order
	helpWanted := false
	note := func(err error) {
		if err != nil && convErr == nil {
			convErr = err
		}
	}
	// done finishes the parse: --help wins over conversion errors.
	done := func() error {
		if helpWanted {
			WriteHelp(ctx.Stdout, cmd)
			return &helpShown{}
		}
		return convErr
	}
	i := 0
	onlyPositional := false
	for i < len(argv) {
		a := argv[i]
		i++
		if onlyPositional || a == "-" || !strings.HasPrefix(a, "-") {
			if cmd.isGroup() {
				if err := done(); err != nil {
					return nil, "", err
				}
				return argv[i:], a, nil
			}
			positionals = append(positionals, a)
			continue
		}
		if a == "--" {
			onlyPositional = true
			continue
		}
		if strings.HasPrefix(a, "--") {
			name, val, hasVal := strings.Cut(a[2:], "=")
			opt := "--" + name
			if name == "help" {
				if hasVal {
					return nil, "", badOptionUsage(cmd, "Option '%s' does not take a value.", opt)
				}
				helpWanted = true
				continue
			}
			f := cmd.flag(name)
			if f == nil && name == "mission" && !cmd.isGroup() {
				// Every leaf accepts (and ignores) --mission.
				f = &Flag{Name: "mission", Kind: String}
			}
			if f == nil {
				if cmd.IgnoreUnknownOptions {
					positionals = append(positionals, a)
					continue
				}
				return nil, "", noSuchOption(cmd, opt)
			}
			if f.Kind == Bool {
				if hasVal {
					return nil, "", badOptionUsage(cmd, "Option '%s' does not take a value.", opt)
				}
				ctx.values[f.Name] = !(f.Negatable && name == f.negName())
				ctx.set[f.Name] = true
				continue
			}
			if !hasVal {
				if i >= len(argv) {
					return nil, "", badOptionUsage(cmd, "Option '%s' requires an argument.", opt)
				}
				val = argv[i]
				i++
			}
			note(assign(ctx, cmd, f, val))
			continue
		}
		// A cluster of short options ("-v", "-vx", "-ovalue").
		var unknown []rune
		rs := []rune(a[1:])
		for j := 0; j < len(rs); j++ {
			opt := "-" + string(rs[j])
			f := cmd.shortFlag(string(rs[j]))
			if f == nil && rs[j] == 'h' && !cmd.NoShortHelp {
				helpWanted = true
				continue
			}
			if f == nil {
				if cmd.IgnoreUnknownOptions {
					unknown = append(unknown, rs[j])
					continue
				}
				return nil, "", &UsageError{Cmd: cmd, Msg: "No such option: " + opt}
			}
			if f.Kind == Bool {
				ctx.values[f.Name] = true
				ctx.set[f.Name] = true
				continue
			}
			val := string(rs[j+1:])
			if val == "" {
				if i >= len(argv) {
					return nil, "", badOptionUsage(cmd, "Option '%s' requires an argument.", opt)
				}
				val = argv[i]
				i++
			}
			note(assign(ctx, cmd, f, val))
			break
		}
		if len(unknown) > 0 {
			positionals = append(positionals, "-"+string(unknown))
		}
	}
	if err := done(); err != nil {
		return nil, "", err
	}
	if cmd.isGroup() {
		return nil, "", nil
	}
	// Bind positionals.
	pi := 0
	for ai, arg := range cmd.Args {
		if arg.Variadic {
			n := len(positionals) - pi
			// leave room for trailing required args
			for _, a2 := range cmd.Args[ai+1:] {
				if a2.Required {
					n--
				}
			}
			n = max(n, 0)
			ctx.args[arg.Name] = append([]string(nil), positionals[pi:pi+n]...)
			pi += n
			continue
		}
		if pi < len(positionals) {
			v := positionals[pi]
			if len(arg.Choices) > 0 && !contains(arg.Choices, v) {
				return nil, "", Usage(cmd, "Invalid value for '%s': %s is not one of %s.", argHint(arg), pyjson.ReprString(v), choiceList(arg.Choices))
			}
			ctx.args[arg.Name] = []string{v}
			pi++
		} else if arg.Default != "" {
			ctx.args[arg.Name] = []string{arg.Default}
		}
	}
	ctx.pendingExtra = nil
	if pi < len(positionals) {
		if cmd.AllowExtraArgs {
			ctx.Extra = positionals[pi:]
		} else {
			ctx.pendingExtra = positionals[pi:]
		}
	}
	return nil, "", nil
}

// noSuchOption is click's NoSuchOption for a long option, with
// difflib-close possibilities among the command's long options.
func noSuchOption(cmd *Command, opt string) error {
	msg := "No such option: " + opt
	if hits := closeMatches(opt, longOptions(cmd)); len(hits) > 0 {
		sort.Strings(hits)
		msg += " (Possible options: " + strings.Join(hits, ", ") + ")"
	}
	return &UsageError{Cmd: cmd, Msg: msg}
}

// longOptions is click's parser._long_opt key set (hidden options, negative
// spellings and --help included).
func longOptions(cmd *Command) []string {
	var out []string
	hasMission := false
	for _, f := range cmd.Flags {
		names := append([]string{f.Name}, f.Aliases...)
		if f.Kind == Bool && f.Negatable {
			names = append(names, f.negName())
		}
		for _, n := range names {
			out = append(out, "--"+n)
			hasMission = hasMission || n == "mission"
		}
	}
	if !hasMission && !cmd.isGroup() {
		out = append(out, "--mission")
	}
	return append(out, "--help")
}

// flagHint is click's Parameter.get_error_hint for an option.
func flagHint(f *Flag) string {
	names := []string{"'--" + f.Name + "'"}
	for _, a := range f.Aliases {
		names = append(names, "'--"+a+"'")
	}
	if f.Short != "" {
		names = append(names, "'-"+f.Short+"'")
	}
	return strings.Join(names, " / ")
}

// choiceList renders choices like typer's invalid-choice message.
func choiceList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = pyjson.ReprString(x)
	}
	return strings.Join(q, ", ")
}

// describeRange is click's _NumberRangeBase._describe_range.
func describeRange(lo, hi *int) string {
	switch {
	case lo == nil && hi == nil:
		return ""
	case lo == nil:
		return fmt.Sprintf("x<=%d", *hi)
	case hi == nil:
		return fmt.Sprintf("x>=%d", *lo)
	}
	return fmt.Sprintf("%d<=x<=%d", *lo, *hi)
}

func assign(ctx *Context, cmd *Command, f *Flag, val string) error {
	invalid := func(msg string) error {
		return Usage(cmd, "Invalid value for %s: %s", flagHint(f), msg)
	}
	if len(f.Choices) > 0 && !contains(f.Choices, val) {
		return invalid(fmt.Sprintf("%s is not one of %s.", pyjson.ReprString(val), choiceList(f.Choices)))
	}
	switch f.Kind {
	case String:
		ctx.values[f.Name] = val
	case Int:
		n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(val), "_", ""))
		if err != nil {
			return invalid(fmt.Sprintf("%s is not a valid int.", pyjson.ReprString(val)))
		}
		if (f.Min != nil && n < *f.Min) || (f.Max != nil && n > *f.Max) {
			return invalid(fmt.Sprintf("%d is not in the range %s.", n, describeRange(f.Min, f.Max)))
		}
		ctx.values[f.Name] = n
	case Float:
		n, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			return invalid(fmt.Sprintf("%s is not a valid float.", pyjson.ReprString(val)))
		}
		ctx.values[f.Name] = n
	case StringList:
		cur, _ := ctx.values[f.Name].([]string)
		if !ctx.set[f.Name] {
			cur = nil
		}
		ctx.values[f.Name] = append(cur, val)
	}
	ctx.set[f.Name] = true
	return nil
}

// finalize applies env vars and defaults, then reports missing required
// parameters (arguments first: upstream never declares a required option
// before a required argument) and unexpected extra positionals.
func finalize(ctx *Context, cmd *Command) error {
	var missing error
	for _, f := range cmd.Flags {
		if ctx.set[f.Name] {
			continue
		}
		if f.Envvar != "" {
			if v, ok := os.LookupEnv(f.Envvar); ok && v != "" {
				if f.Kind == Bool {
					lv := strings.ToLower(v)
					ctx.values[f.Name] = lv == "1" || lv == "true" || lv == "yes" || lv == "y" || lv == "on"
					ctx.set[f.Name] = true
				} else if err := assign(ctx, cmd, f, v); err != nil {
					return err
				}
				continue
			}
		}
		if f.Required && missing == nil {
			missing = Usage(cmd, "Missing option %s.", flagHint(f))
		}
		if f.Default != nil {
			ctx.values[f.Name] = f.Default
		} else {
			switch f.Kind {
			case Bool:
				ctx.values[f.Name] = false
			case Int:
				ctx.values[f.Name] = 0
			case String:
				ctx.values[f.Name] = ""
			case StringList:
				ctx.values[f.Name] = []string(nil)
			}
		}
	}
	for _, a := range cmd.Args {
		if a.Required && len(ctx.args[a.Name]) == 0 {
			return Usage(cmd, "Missing argument '%s'.", argHint(a))
		}
	}
	if missing != nil {
		return missing
	}
	if len(ctx.pendingExtra) > 0 && !cmd.isGroup() {
		extra := ctx.pendingExtra
		ctx.pendingExtra = nil
		return Usage(cmd, "Got unexpected extra argument(s) (%s)", strings.Join(extra, " "))
	}
	return nil
}

// argHint is TyperArgument.get_error_hint: the argument's metavar or name.
func argHint(a *Arg) string {
	if a.ErrorHint != "" {
		return a.ErrorHint
	}
	return argBase(a)
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// Bound returns a pointer to n, for Flag.Min / Flag.Max.
func Bound(n int) *int { return &n }
