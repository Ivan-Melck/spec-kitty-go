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
	"os"
	"sort"
	"strconv"
	"strings"
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
}

// Command is a node in the command tree.
type Command struct {
	Name    string
	Aliases []string
	Short   string // one-line help shown in parent listings
	Long    string // full help text; defaults to Short
	Epilog  string
	Hidden  bool
	Flags   []*Flag
	Args    []*Arg
	Run     func(*Context) error

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

	values map[string]any
	set    map[string]bool
	args   map[string][]string
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
		if err != nil {
			return err
		}
		if cmd.isGroup() {
			if cmd.Callback != nil {
				if err := finalize(ctx, cmd); err != nil {
					return err
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
			if next == nil {
				return Usage(cmd, "No such command '%s'.", sub)
			}
			if err := finalize(ctx, cmd); err != nil {
				return err
			}
			ctx = newContext(next, ctx, io_)
			cmd = next
			continue
		}
		if err := finalize(ctx, cmd); err != nil {
			return err
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

// parseInto consumes argv for cmd. For groups it stops at the first
// positional (the subcommand name) and returns it.
func parseInto(ctx *Context, cmd *Command, argv []string) ([]string, string, error) {
	var positionals []string
	i := 0
	onlyPositional := false
	for i < len(argv) {
		a := argv[i]
		i++
		if onlyPositional || a == "-" || !strings.HasPrefix(a, "-") {
			if cmd.isGroup() {
				return argv[i:], a, nil
			}
			positionals = append(positionals, a)
			continue
		}
		if a == "--" {
			onlyPositional = true
			continue
		}
		if a == "-h" || a == "--help" {
			WriteHelp(ctx.Stdout, cmd)
			return nil, "", &helpShown{}
		}
		var f *Flag
		var name, val string
		hasVal := false
		negated := false
		if strings.HasPrefix(a, "--") {
			name = a[2:]
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				val, hasVal = name[eq+1:], true
				name = name[:eq]
			}
			f = cmd.flag(name)
			if f == nil && name == "mission" && !cmd.isGroup() {
				// Every leaf accepts (and ignores) --mission.
				f = &Flag{Name: "mission", Kind: String}
			}
			if f != nil && f.Kind == Bool && f.Negatable && name == f.negName() {
				negated = true
			}
		} else {
			name = a[1:2]
			f = cmd.shortFlag(name)
			if len(a) > 2 {
				val, hasVal = a[2:], true
			}
		}
		if f == nil {
			if cmd.IgnoreUnknownOptions {
				positionals = append(positionals, a)
				continue
			}
			if len(a) > 2 && !strings.HasPrefix(a, "--") {
				return nil, "", Usage(cmd, "No such option: %s", a[:2])
			}
			opt := a
			if eq := strings.IndexByte(opt, '='); eq >= 0 {
				opt = opt[:eq]
			}
			if s := suggest(cmd, opt); s != "" {
				return nil, "", Usage(cmd, "No such option: %s (Possible options: %s)", opt, s)
			}
			return nil, "", Usage(cmd, "No such option: %s", opt)
		}
		disp := displayName(a, hasVal)
		switch f.Kind {
		case Bool:
			if hasVal {
				return nil, "", Usage(cmd, "Option '%s' does not take a value.", disp)
			}
			ctx.values[f.Name] = !negated
			ctx.set[f.Name] = true
		default:
			if !hasVal {
				if i >= len(argv) {
					return nil, "", Usage(cmd, "Option '%s' requires an argument.", disp)
				}
				val = argv[i]
				i++
			}
			if err := assign(ctx, cmd, f, disp, val); err != nil {
				return nil, "", err
			}
		}
	}
	if cmd.isGroup() {
		return nil, "", nil
	}
	// Bind positionals.
	pi := 0
	for _, arg := range cmd.Args {
		if arg.Variadic {
			n := len(positionals) - pi
			// leave room for trailing required args
			after := 0
			for _, a2 := range cmd.Args[indexOf(cmd.Args, arg)+1:] {
				if a2.Required {
					after++
				}
			}
			n -= after
			if n < 0 {
				n = 0
			}
			ctx.args[arg.Name] = append([]string(nil), positionals[pi:pi+n]...)
			pi += n
			continue
		}
		if pi < len(positionals) {
			v := positionals[pi]
			if len(arg.Choices) > 0 && !contains(arg.Choices, v) {
				return nil, "", Usage(cmd, "Invalid value for '%s': '%s' is not one of %s.", argMeta(arg), v, quoteList(arg.Choices))
			}
			ctx.args[arg.Name] = []string{v}
			pi++
		} else if arg.Default != "" {
			ctx.args[arg.Name] = []string{arg.Default}
		}
	}
	if pi < len(positionals) {
		if cmd.AllowExtraArgs {
			ctx.Extra = positionals[pi:]
		} else {
			extra := positionals[pi:]
			if len(extra) == 1 {
				return nil, "", Usage(cmd, "Got unexpected extra argument (%s)", extra[0])
			}
			return nil, "", Usage(cmd, "Got unexpected extra arguments (%s)", strings.Join(extra, " "))
		}
	}
	return nil, "", nil
}

func indexOf(args []*Arg, a *Arg) int {
	for i, x := range args {
		if x == a {
			return i
		}
	}
	return -1
}

func displayName(a string, hasVal bool) string {
	if hasVal && strings.HasPrefix(a, "--") {
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			return a[:eq]
		}
	}
	if !strings.HasPrefix(a, "--") && len(a) > 2 {
		return a[:2]
	}
	return a
}

func assign(ctx *Context, cmd *Command, f *Flag, disp, val string) error {
	if len(f.Choices) > 0 && !contains(f.Choices, val) {
		return Usage(cmd, "Invalid value for '%s': '%s' is not one of %s.", flagDisplay(f, disp), val, quoteList(f.Choices))
	}
	switch f.Kind {
	case String:
		ctx.values[f.Name] = val
	case Int:
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return Usage(cmd, "Invalid value for '%s': '%s' is not a valid integer.", flagDisplay(f, disp), val)
		}
		ctx.values[f.Name] = n
	case Float:
		n, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			return Usage(cmd, "Invalid value for '%s': '%s' is not a valid float.", flagDisplay(f, disp), val)
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

func flagDisplay(f *Flag, disp string) string {
	if disp != "" {
		return disp
	}
	return "--" + f.Name
}

// finalize applies defaults, env vars and required checks.
func finalize(ctx *Context, cmd *Command) error {
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
				} else if err := assign(ctx, cmd, f, "--"+f.Name, v); err != nil {
					return err
				}
				continue
			}
		}
		if f.Required {
			return Usage(cmd, "Missing option '%s'.", flagLabel(f))
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
			if a.Variadic {
				return Usage(cmd, "Missing argument '%s...'.", argMeta(a))
			}
			return Usage(cmd, "Missing argument '%s'.", argMeta(a))
		}
	}
	return nil
}

func flagLabel(f *Flag) string {
	if f.Short != "" {
		return "--" + f.Name + "' / '-" + f.Short
	}
	return "--" + f.Name
}

func argMeta(a *Arg) string {
	if a.Metavar != "" {
		return a.Metavar
	}
	return strings.ToUpper(strings.ReplaceAll(a.Name, "-", "_"))
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func quoteList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = "'" + x + "'"
	}
	if len(q) == 1 {
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}

func suggest(cmd *Command, opt string) string {
	name := strings.TrimLeft(opt, "-")
	var hits []string
	for _, f := range cmd.Flags {
		if f.Hidden {
			continue
		}
		for _, n := range append([]string{f.Name}, f.Aliases...) {
			if similar(name, n) {
				hits = append(hits, "--"+n)
			}
		}
	}
	sort.Strings(hits)
	return strings.Join(hits, ", ")
}

// similar approximates difflib.get_close_matches(cutoff=0.6).
func similar(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	m := lcs(a, b)
	return 2*float64(m)/float64(len(a)+len(b)) >= 0.6
}

func lcs(a, b string) int {
	prev := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] > cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev = cur
	}
	return prev[len(b)]
}
