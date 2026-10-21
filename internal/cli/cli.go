// Package cli is a small click/typer-compatible command framework.
//
// It reproduces the observable parsing behaviour of the upstream Python CLI
// (typer on click): interspersed options, --flag/--no-flag pairs, repeated
// options, variadic arguments, exit code 2 with a boxed error for usage
// problems, and -h/--help on every command.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Kind is the value type of a flag.
type Kind int

const (
	String Kind = iota
	Bool
	Int
	Float
	StringList // repeatable option collecting every occurrence
)

// Flag describes an option.
type Flag struct {
	Name     string   // primary long name without dashes, e.g. "review-feedback-file"
	Aliases  []string // extra long names without dashes
	Short    string   // single letter without dash
	Kind     Kind
	Default  any
	Help     string
	Metavar  string
	Required bool
	Hidden   bool
	Choices  []string
	// Negatable bools render as --name/--no-name. NoName overrides the
	// negative spelling (without dashes).
	Negatable bool
	NoName    string
	// Envvar, when set, supplies the value if the flag was not given.
	Envvar string
	// Help rendering: Panel names a rich_help_panel ("" = Options);
	// HideDefault suppresses "[default: ...]" (show_default=False);
	// DefaultText overrides the rendered default value.
	Panel       string
	HideDefault bool
	DefaultText string
	// Min/Max bound an Int flag (click IntRange): out-of-range values are
	// usage errors and help shows "<int range> [lo<=x<=hi]".
	Min, Max *int
}

// Arg describes a positional argument.
type Arg struct {
	Name     string
	Help     string
	Required bool
	Variadic bool // nargs=-1; collects remaining positionals
	Default  string
	Metavar  string
	Choices  []string
	// ErrorHint, when set, names the argument in "Missing argument" errors
	// instead of the metavar (typer shows "{name}" in the usage line but the
	// bare name in the error).
	ErrorHint string
	// Help rendering: Type is the Arguments panel type column (default
	// "<str>"); Panel names a rich_help_panel; Hidden omits it from help.
	Type   string
	Panel  string
	Hidden bool
}

// Command is a node in the command tree.
type Command struct {
	Name    string
	Aliases []string
	Short   string // one-line help shown in parent listings
	Long    string // full help text; defaults to Short
	Epilog  string
	Hidden  bool
	// Deprecated marks the command "(deprecated)" in help listings.
	Deprecated bool
	// ShortHelp, when set, replaces the help's first paragraph in the
	// parent's Commands panel (click short_help).
	ShortHelp string
	// GroupUsage renders the usage line like a group's ("COMMAND
	// [ARGS]...") for a leaf that upstream implements as a typer sub-app
	// with only a callback.
	GroupUsage bool
	// NoShortHelp drops the -h alias of --help (upstream registers a few
	// commands after the -h alias is applied, e.g. upgrade).
	NoShortHelp bool
	// ParseUsagePath, when set, replaces the command path in usage errors
	// raised while parsing its arguments (upstream's upgrade re-parses its
	// arguments under a context named "upgrade").
	ParseUsagePath string
	Flags       []*Flag
	Args        []*Arg
	Run         func(*Context) error

	// Group behaviour.
	Subcommands []*Command
	// InvokeWithoutCommand runs Run when a group gets no subcommand.
	InvokeWithoutCommand bool
	// NoArgsIsError makes a bare group invocation a usage error ("Missing command.").
	NoArgsIsError bool
	// Callback runs after a group's own options are parsed, before the
	// subcommand (typer callback). Return Stop() to end successfully.
	Callback func(*Context) error
	// AllowExtraArgs passes unknown positionals to Run via ctx.Extra.
	AllowExtraArgs bool
	// IgnoreUnknownOptions keeps unknown options as extra args.
	IgnoreUnknownOptions bool

	parent *Command
}

// Add appends subcommands and returns the receiver.
func (c *Command) Add(cmds ...*Command) *Command {
	for _, s := range cmds {
		s.parent = c
		c.Subcommands = append(c.Subcommands, s)
	}
	return c
}

// Find returns the direct subcommand named name (or alias).
func (c *Command) Find(name string) *Command {
	for _, s := range c.Subcommands {
		if s.Name == name {
			return s
		}
		for _, a := range s.Aliases {
			if a == name {
				return s
			}
		}
	}
	return nil
}

// Lookup resolves a path of subcommand names.
func (c *Command) Lookup(path ...string) *Command {
	cur := c
	for _, p := range path {
		if cur = cur.Find(p); cur == nil {
			return nil
		}
	}
	return cur
}

// Path returns the full invocation path, e.g. "spec-kitty agent tasks".
func (c *Command) Path() string {
	if c.parent == nil {
		return c.Name
	}
	return c.parent.Path() + " " + c.Name
}

func (c *Command) isGroup() bool { return len(c.Subcommands) > 0 }

func (c *Command) flag(name string) *Flag {
	for _, f := range c.Flags {
		if f.Name == name {
			return f
		}
		for _, a := range f.Aliases {
			if a == name {
				return f
			}
		}
		if f.Kind == Bool && f.Negatable && f.negName() == name {
			return f
		}
	}
	return nil
}

func (f *Flag) negName() string {
	if f.NoName != "" {
		return f.NoName
	}
	return "no-" + f.Name
}

func (c *Command) shortFlag(s string) *Flag {
	for _, f := range c.Flags {
		if f.Short == s {
			return f
		}
	}
	return nil
}

// Context carries parsed values to a command's Run function.
type Context struct {
	Cmd    *Command
	Parent *Context
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Extra holds unconsumed positionals when AllowExtraArgs is set.
	Extra []string
	// InvokedSubcommand is the subcommand name a group's Callback is about
	// to dispatch to ("" when none; click's ctx.invoked_subcommand).
	InvokedSubcommand string

	values map[string]any
	set    map[string]bool
	args   map[string][]string
	// pendingExtra holds unexpected positionals, reported after the
	// missing-parameter checks (click order).
	pendingExtra []string
}

func newContext(cmd *Command, parent *Context, io_ IO) *Context {
	return &Context{Cmd: cmd, Parent: parent, Stdin: io_.Stdin, Stdout: io_.Stdout, Stderr: io_.Stderr,
		values: map[string]any{}, set: map[string]bool{}, args: map[string][]string{}}
}

func (c *Context) value(name string) (any, bool) {
	for x := c; x != nil; x = x.Parent {
		if v, ok := x.values[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// String returns a string flag value (or the default).
func (c *Context) String(name string) string {
	v, _ := c.value(name)
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

// Bool returns a bool flag value.
func (c *Context) Bool(name string) bool {
	v, _ := c.value(name)
	b, _ := v.(bool)
	return b
}

// Int returns an int flag value.
func (c *Context) Int(name string) int {
	v, _ := c.value(name)
	i, _ := v.(int)
	return i
}

// Float returns a float flag value.
func (c *Context) Float(name string) float64 {
	v, _ := c.value(name)
	f, _ := v.(float64)
	return f
}

// Strings returns a repeated flag's values.
func (c *Context) Strings(name string) []string {
	v, _ := c.value(name)
	s, _ := v.([]string)
	return s
}

// IsSet reports whether the flag was given explicitly on the command line
// (or via its environment variable).
func (c *Context) IsSet(name string) bool {
	for x := c; x != nil; x = x.Parent {
		if x.set[name] {
			return true
		}
	}
	return false
}

// OptString returns a pointer to the value when set, else nil (Optional[str]).
func (c *Context) OptString(name string) *string {
	if !c.IsSet(name) {
		if v, ok := c.value(name); ok && v != nil {
			if s, ok := v.(string); ok && s != "" {
				return &s
			}
		}
		return nil
	}
	s := c.String(name)
	return &s
}

// Arg returns the first value of a positional argument.
func (c *Context) Arg(name string) string {
	if v := c.args[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// HasArg reports whether a positional argument was supplied.
func (c *Context) HasArg(name string) bool { return len(c.args[name]) > 0 }

// ArgList returns all values of a (variadic) positional argument.
func (c *Context) ArgList(name string) []string { return c.args[name] }

// Printf writes to stdout.
func (c *Context) Printf(format string, a ...any) { fmt.Fprintf(c.Stdout, format, a...) }

// Println writes to stdout.
func (c *Context) Println(a ...any) { fmt.Fprintln(c.Stdout, a...) }

// Eprintf writes to stderr.
func (c *Context) Eprintf(format string, a ...any) { fmt.Fprintf(c.Stderr, format, a...) }

// IO bundles the process streams.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExitError terminates with Code after Run has already reported the failure.
type ExitError struct {
	Code int
	Msg  string // optional; printed to stderr when non-empty
}

func (e *ExitError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return "exit status " + strconv.Itoa(e.Code)
}

// Exit returns an *ExitError.
func Exit(code int) error { return &ExitError{Code: code} }

// Fail prints msg to stderr and exits 1.
func Fail(msg string) error { return &ExitError{Code: 1, Msg: msg} }

// UsageError is a click-style usage error (exit 2, boxed message).
type UsageError struct {
	Cmd *Command
	Msg string
	// NoContext marks parser errors click raises without a context
	// (BadOptionUsage): only the Error panel is printed.
	NoContext bool
	parsing   bool
}

func (e *UsageError) Error() string { return e.Msg }

// Usage builds a usage error for cmd.
func Usage(cmd *Command, format string, a ...any) error {
	return &UsageError{Cmd: cmd, Msg: fmt.Sprintf(format, a...)}
}

// BadParameter mirrors click.BadParameter for an option.
func BadParameter(cmd *Command, flag, msg string) error {
	return &UsageError{Cmd: cmd, Msg: fmt.Sprintf("Invalid value for '%s': %s", flag, msg)}
}

// Execute parses argv against root and runs the selected command, returning
// the process exit code.
func Execute(root *Command, argv []string, io_ IO) int {
	err := execute(root, argv, io_)
	return handleErr(err, io_)
}

func handleErr(err error, io_ IO) int {
	if err == nil {
		return 0
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		writeUsageError(io_.Stderr, ue)
		return 2
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		if ee.Msg != "" {
			msg := ee.Msg
			if !strings.HasSuffix(msg, "\n") {
				msg += "\n"
			}
			io.WriteString(io_.Stderr, msg)
		}
		return ee.Code
	}
	var he *helpShown
	if errors.As(err, &he) {
		return 0
	}
	fmt.Fprintf(io_.Stderr, "Error: %v\n", err)
	return 1
}

// Stop ends execution successfully (exit 0) without running further.
func Stop() error { return &helpShown{} }

type helpShown struct{}

func (*helpShown) Error() string { return "help" }

// Hook runs before a leaf command's Run (e.g. startup checks). It receives
// the leaf context.
var BeforeRun func(*Context) error

func execute(root *Command, argv []string, io_ IO) error {
	ctx := newContext(root, nil, io_)
	cmd := root
	rest := argv
	for {
		var err error
		var sub string
		rest, sub, err = parseInto(ctx, cmd, rest)
		if err == nil && !cmd.isGroup() {
			err = finalize(ctx, cmd)
		}
		if err != nil {
			if ue, ok := err.(*UsageError); ok {
				ue.parsing = true
			}
			return err
		}
		if cmd.isGroup() {
			if sub != "" && cmd.Find(sub) == nil {
				return noSuchCommand(cmd, sub)
			}
			if cmd.Callback != nil {
				if err := finalize(ctx, cmd); err != nil {
					return err
				}
				if sub != "" && cmd.Find(sub) != nil {
					ctx.InvokedSubcommand = sub
				}
				if err := cmd.Callback(ctx); err != nil {
					return err
				}
			}
			if sub == "" {
				if cmd.InvokeWithoutCommand && cmd.Run != nil {
					if err := finalize(ctx, cmd); err != nil {
						return err
					}
					return cmd.Run(ctx)
				}
				if cmd.NoArgsIsError {
					return Usage(cmd, "Missing command.")
				}
				WriteHelp(io_.Stdout, cmd)
				return nil
			}
			next := cmd.Find(sub)
			if err := finalize(ctx, cmd); err != nil {
				return err
			}
			ctx = newContext(next, ctx, io_)
			cmd = next
			continue
		}
		if cmd.Callback != nil {
			if err := cmd.Callback(ctx); err != nil {
				return err
			}
		}
		if BeforeRun != nil {
			if err := BeforeRun(ctx); err != nil {
				return err
			}
		}
		if cmd.Run == nil {
			WriteHelp(io_.Stdout, cmd)
			return nil
		}
		return cmd.Run(ctx)
	}
}


// noSuchCommand is typer's resolve_command failure with close-match
// suggestions among all registered (also hidden) subcommands.
func noSuchCommand(cmd *Command, name string) error {
	msg := fmt.Sprintf("No such command %s.", pyjson.ReprString(name))
	var names []string
	for _, s := range cmd.Subcommands {
		names = append(names, s.Name)
	}
	if hits := closeMatches(name, names); len(hits) > 0 {
		q := make([]string, len(hits))
		for i, h := range hits {
			q[i] = pyjson.ReprString(h)
		}
		msg = strings.TrimSuffix(msg, ".") + ". Did you mean " + strings.Join(q, ", ") + "?"
	}
	return &UsageError{Cmd: cmd, Msg: msg}
}
