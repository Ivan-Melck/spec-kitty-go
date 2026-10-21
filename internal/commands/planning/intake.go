package planning

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

func init() {
	addCommands(func(root, _, _, _ *cli.Command) {
		root.Add(&cli.Command{
			Name:  "intake",
			Short: "Ingest a plan document as a mission brief for /spec-kitty.specify.",
			Args:  []*cli.Arg{{Name: "path", Metavar: "[path]", Help: "Path to plan document, or '-' to read from stdin. Omit when using --show or --auto."}},
			Flags: []*cli.Flag{
				{Name: "force", Kind: cli.Bool, Help: "Overwrite existing brief."},
				{Name: "show", Kind: cli.Bool, Help: "Print current brief and provenance; no writes."},
				{Name: "auto", Kind: cli.Bool, Help: "Scan known harness plan locations and ingest automatically."},
			},
			Run: runIntake,
		})
	})
}

const defaultMaxBrief = 5 * 1024 * 1024

func intakeConfig(root string) *jsonx.Object {
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "config.yaml"))
	if err != nil {
		return nil
	}
	v, err := pyyaml.SafeLoad(string(b))
	if err != nil {
		return nil
	}
	o := jsonx.ToObject(v)
	if o == nil {
		return nil
	}
	return o.GetObject("intake")
}

func maxBriefBytes(root string) int {
	in := intakeConfig(root)
	if in == nil {
		return defaultMaxBrief
	}
	raw, _ := in.Get("max_brief_bytes")
	var n int
	switch x := raw.(type) {
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		n = int(x)
	case string:
		v, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return defaultMaxBrief
		}
		n = v
	default:
		return defaultMaxBrief
	}
	if n <= 0 {
		return defaultMaxBrief
	}
	return n
}

// escapeForComment is intake.provenance.escape_for_comment.
func escapeForComment(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\t' || (r >= 0x20 && r != 0x7f) {
			b.WriteRune(r)
		}
	}
	c := strings.ReplaceAll(strings.ReplaceAll(b.String(), "-->", "--&gt;"), "*/", "*&#47;")
	if strings.HasPrefix(c, "#") {
		c = `\` + c
	}
	if len(c) <= 256 {
		return c
	}
	cut := 256
	for cut > 0 && !utf8.RuneStart(c[cut]) {
		cut--
	}
	return c[:cut]
}

var packetFront = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---(?:\r?\n(.*))?\z`)

// handoffPacketFields is parse_handoff_packet(...).sidecar_fields() (nil when
// not a packet) plus its source_tool.
func handoffPacketFields(content string) (*jsonx.Object, string) {
	m := packetFront.FindStringSubmatch(strings.TrimPrefix(content, "\ufeff"))
	if m == nil {
		return nil, ""
	}
	v, err := pyyaml.SafeLoad(m[1])
	if err != nil {
		return nil, ""
	}
	o := jsonx.ToObject(v)
	if o == nil {
		return nil, ""
	}
	ver, _ := o.Get("handoff_packet")
	switch ver.(type) {
	case int, int64:
		if fmt.Sprint(ver) != "1" {
			return nil, ""
		}
	default:
		return nil, ""
	}
	reqs, ok := o.GetList("requirements"), true
	if rv, has := o.Get("requirements"); !has {
		ok = false
	} else if _, isList := rv.([]any); !isList {
		ok = false
	}
	if !ok {
		return nil, ""
	}
	nonEmpty := func(x any) (string, bool) {
		s, ok := x.(string)
		return strings.TrimSpace(s), ok && strings.TrimSpace(s) != ""
	}
	var ids []string
	for _, it := range reqs {
		io := jsonx.ToObject(it)
		if io == nil {
			return nil, ""
		}
		idv, _ := io.Get("id")
		st, _ := io.Get("statement")
		id, ok1 := nonEmpty(idv)
		_, ok2 := nonEmpty(st)
		if !ok1 || !ok2 {
			return nil, ""
		}
		if ac, has := io.Get("acceptance_criteria"); has && ac != nil {
			l, ok := ac.([]any)
			if !ok {
				return nil, ""
			}
			for _, a := range l {
				ao := jsonx.ToObject(a)
				if ao == nil {
					return nil, ""
				}
				x, _ := ao.Get("id")
				y, _ := ao.Get("statement")
				if _, ok := nonEmpty(x); !ok {
					return nil, ""
				}
				if _, ok := nonEmpty(y); !ok {
					return nil, ""
				}
			}
		}
		ids = append(ids, id)
	}
	cc := 0
	if cv, has := o.Get("constraints"); has && cv != nil {
		l, ok := cv.([]any)
		if !ok {
			return nil, ""
		}
		for _, c := range l {
			co := jsonx.ToObject(c)
			if co == nil {
				return nil, ""
			}
			x, _ := co.Get("id")
			if _, ok := nonEmpty(x); !ok {
				return nil, ""
			}
		}
		cc = len(l)
	}
	f := jsonx.NewObject("packet_version", 1, "requirement_count", len(ids), "constraint_count", cc)
	opt := func(k string) string {
		x, _ := o.Get(k)
		s, _ := nonEmpty(x)
		return s
	}
	tool := opt("source_tool")
	if tool != "" {
		f.Set("source_tool", escapeForComment(tool))
	}
	if s := opt("source_mission"); s != "" {
		f.Set("source_mission", escapeForComment(s))
	}
	if s := opt("source_ref"); s != "" {
		f.Set("source_ref", escapeForComment(s))
	}
	if len(ids) > 0 {
		var l []any
		for _, id := range ids {
			l = append(l, escapeForComment(id))
		}
		f.Set("requirement_ids", l)
	}
	return f, tool
}

func tooLargeMsg(size, limit int) string {
	capMB := limit / 1024 / 1024
	if capMB < 1 {
		capMB = 1
	}
	return fmt.Sprintf("[red]File is too large to ingest (%.1f MB). Maximum allowed size is %d MB.[/red]", float64(size)/1024/1024, capMB)
}

func runIntake(ctx *cli.Context) error {
	errOut := ctx.Stderr
	root := project.Locate("")
	if root == "" {
		root = cwd()
	}
	brief := filepath.Join(root, ".kittify", "mission-brief.md")
	source := filepath.Join(root, ".kittify", "brief-source.yaml")
	if ctx.Bool("show") {
		b, berr := os.ReadFile(brief)
		var src *jsonx.Object
		if sb, err := os.ReadFile(source); err == nil && berr == nil {
			if v, err := pyyaml.SafeLoad(string(sb)); err == nil {
				src = jsonx.ToObject(v)
			}
		}
		if berr != nil && src == nil {
			say(errOut, "[red]No brief found at .kittify/mission-brief.md[/red]")
			return cli.Exit(1)
		}
		if src != nil {
			get := func(k string) string {
				v, _ := src.Get(k)
				if v == nil {
					return ""
				}
				return fmt.Sprint(v)
			}
			say(ctx.Stdout, fmt.Sprintf("[bold]Source:[/bold] %s   [bold]Ingested:[/bold] %s   [bold]Hash:[/bold] %s", get("source_file"), get("ingested_at"), get("brief_hash")))
		}
		if berr == nil {
			say(ctx.Stdout, string(b))
		}
		return nil
	}
	path := ctx.Arg("path")
	hasPath := ctx.HasArg("path")
	if hasPath && ctx.Bool("auto") {
		say(errOut, "[red]--auto cannot be combined with a positional path argument.[/red]")
		return cli.Exit(1)
	}
	limit := maxBriefBytes(root)
	var content, sourceFile, agent string
	if ctx.Bool("auto") {
		type cand struct{ path, key, agent string }
		var cands []cand
		for _, h := range []struct{ key, agent, rel string }{{"opencode", "opencode", ".opencode/plans"}, {"kiro", "amazon-q", ".kiro/specs"},
			{"cursor", "cursor", ".cursor/plans"}, {"gemini", "gemini", ".gemini/plans"}, {"handoff", "", ".handoff"}} {
			p := filepath.Join(root, h.rel)
			st, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if st.Mode().IsRegular() {
				cands = append(cands, cand{p, h.key, h.agent})
			} else if st.IsDir() {
				entries, _ := os.ReadDir(p)
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				sort.Strings(names)
				for _, n := range names {
					cp := filepath.Join(p, n)
					if cs, err := os.Lstat(cp); err == nil && cs.Mode().IsRegular() && filepath.Ext(n) == ".md" {
						cands = append(cands, cand{cp, h.key, h.agent})
					}
				}
			}
		}
		if len(cands) == 0 {
			say(errOut, "No plan document detected in known harness locations.\nPass a path explicitly: spec-kitty intake <path>")
			return cli.Exit(1)
		}
		if len(cands) > 1 {
			say(errOut, "Found multiple plan documents. Which should I use?")
			for i, c := range cands {
				say(errOut, fmt.Sprintf("  %d. %s  (%s)", i+1, c.path, c.key))
			}
			say(errOut, "\nNon-interactive — pass a path explicitly: spec-kitty intake <path>")
			return cli.Exit(1)
		}
		c := cands[0]
		say(ctx.Stdout, fmt.Sprintf("BRIEF DETECTED: %s (source: %s)", c.path, c.key))
		st, _ := os.Stat(c.path)
		if int(st.Size()) > limit {
			say(errOut, tooLargeMsg(int(st.Size()), limit))
			return cli.Exit(1)
		}
		b, err := os.ReadFile(c.path)
		if err != nil {
			say(errOut, fmt.Sprintf("[red]Could not read file: %v[/red]", err))
			return cli.Exit(1)
		}
		content, sourceFile, agent = string(b), c.path, c.agent
	} else {
		if !hasPath {
			say(errOut, "[red]Provide a file path, '-' for stdin, --show, or --auto[/red]")
			return cli.Exit(1)
		}
		if path == "-" {
			b, err := io.ReadAll(io.LimitReader(ctx.Stdin, int64(limit)+1))
			if err != nil {
				say(errOut, fmt.Sprintf("[red]Could not read stdin: %v[/red]", err))
				return cli.Exit(1)
			}
			if len(b) > limit {
				say(errOut, tooLargeMsg(len(b), limit))
				return cli.Exit(1)
			}
			content, sourceFile = string(b), "stdin"
		} else {
			st, err := os.Stat(path)
			if err != nil {
				say(errOut, "[red]File not found: "+path+"[/red]")
				return cli.Exit(1)
			}
			if int(st.Size()) > limit {
				say(errOut, tooLargeMsg(int(st.Size()), limit))
				return cli.Exit(1)
			}
			b, err := os.ReadFile(path)
			if err != nil || !utf8.Valid(b) {
				say(errOut, fmt.Sprintf("[red]Could not read file: %v[/red]", err))
				return cli.Exit(1)
			}
			content, sourceFile = string(b), path
		}
	}
	packet, tool := handoffPacketFields(content)
	if agent == "" && tool != "" {
		agent = tool
	}
	if fileExists(brief) && !ctx.Bool("force") {
		// TODO: guard_destructive_overwrite ownership proof is not ported:
		// an existing brief is always protected without --force.
		say(errOut, "Brief already exists at .kittify/mission-brief.md. Use --force to overwrite.")
		return cli.Exit(1)
	}
	if fileExists(brief) != fileExists(source) {
		os.Remove(brief)
		os.Remove(source)
	}
	os.MkdirAll(filepath.Join(root, ".kittify"), 0o755)
	hash := sha256Hex([]byte(content))
	at := clock.NowUTCISO()
	safeSrc := escapeForComment(sourceFile)
	text := fmt.Sprintf("<!-- spec-kitty intake: ingested from %s at %s -->\n<!-- brief_hash: %s -->\n\n%s", safeSrc, at, hash, content)
	data := jsonx.NewObject("source_file", safeSrc, "ingested_at", at, "brief_hash", hash)
	if agent != "" {
		data.Set("source_agent", escapeForComment(agent))
	}
	if packet != nil {
		for _, kv := range packet.Pairs() {
			data.Set(kv.Key, kv.Value)
		}
	}
	if err := os.WriteFile(source, []byte(pyyaml.Dump(data, pyyaml.Options{SortKeys: true})), 0o644); err != nil {
		say(errOut, "[red]"+err.Error()+"[/red]")
		return cli.Exit(1)
	}
	if err := os.WriteFile(brief, []byte(text), 0o644); err != nil {
		say(errOut, "[red]"+err.Error()+"[/red]")
		return cli.Exit(1)
	}
	say(ctx.Stdout, "[green]✓[/green] Brief written to .kittify/mission-brief.md")
	say(ctx.Stdout, "[green]✓[/green] Provenance written to .kittify/brief-source.yaml")
	return nil
}
