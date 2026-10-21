// Package staleassert is the post-merge stale-assertion analyzer
// (specify_cli.post_merge.stale_assertions): it compares two git refs,
// extracts the function/class names and string literals removed from
// changed Python source files, and reports the test assertions (Assert
// statements and assert* calls, found with an AST walk of every tracked
// tests/ file) that still reference them.
package staleassert

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/pyast"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// FPCeiling is the NFR-002 findings-per-100-LOC ceiling.
const FPCeiling = 5.0

// Finding is one likely-stale assertion.
type Finding struct {
	TestFile      string
	TestLine      int
	SourceFile    string
	SourceLine    int
	ChangedSymbol string
	Confidence    string // high | medium | low | info
	Hint          string
	Label         string
}

// Report is StaleAssertionReport.
type Report struct {
	BaseRef, HeadRef, RepoRoot string
	Findings                   []Finding
	ElapsedSeconds             float64
	FilesScanned               int
	FindingsPer100LOC          float64
}

type symbol struct {
	name, kind, file string
	line             int
}

var genericTokens = map[string]bool{}

func init() {
	for _, t := range []string{"ok", "true", "false", "none", "null", "yes", "no",
		"error", "warning", "warn", "info", "debug", "trace",
		"test", "tests", "value", "values", "name", "names",
		"type", "types", "data", "result", "results", "status",
		"success", "failure", "fail", "failed", "pass", "passed",
		"id", "key", "keys", "path", "paths", "file", "files",
		"message", "msg", "text", "label", "title", "description",
		"default", "unknown", "empty", "count", "total", "start", "end",
		"{}", "{0}", "{1}", "%s", "%d", "%r", "\n", "\t"} {
		genericTokens[t] = true
	}
}

var (
	fileModeRe  = regexp.MustCompile(`^[arwbt+]{1,4}$`)
	lowerWordRe = regexp.MustCompile(`^[a-z]+$`)
)

func isGenericLiteral(v string) bool {
	if v == "" {
		return true
	}
	alnum := false
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsNumber(r) {
			alnum = true
			break
		}
	}
	if !alnum {
		return true
	}
	return genericTokens[strings.ToLower(strings.TrimSpace(v))]
}

func isNoiseLiteral(v string) bool {
	if isGenericLiteral(v) {
		return true
	}
	if len([]rune(v)) < 2 {
		return true
	}
	return fileModeRe.MatchString(v) || lowerWordRe.MatchString(v)
}

func headStillExports(head *pyast.Node, name string) bool {
	if head == nil {
		return false
	}
	for _, n := range head.Body {
		if (n.Kind == "Import" || n.Kind == "ImportFrom") && pyast.ImportBindsName(n, name) {
			return true
		}
		if n.Kind == "Assign" && pyast.AssignmentListsDunderAll(n, name) {
			return true
		}
	}
	return false
}

func gitShow(root, ref, rel string) string {
	out, err := gitx.Run(root, "show", ref+":"+rel)
	if err != nil {
		return ""
	}
	return out
}

func parseOrNil(src string) *pyast.Node {
	if src == "" {
		return nil
	}
	m, err := pyast.Parse(src)
	if err != nil {
		return nil
	}
	return m
}

// changedPaths is kernel.git.changed_paths(base, head, renames=True,
// pathspecs=("*.py",), glob=True).
func changedPaths(root, base, head string) ([]string, error) {
	res, err := gitx.Exec(root, nil, nil, "-c", "diff.relative=false", "diff", "--name-only", "-z", "-M", base, head, "--", "*.py")
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("git diff failed (exit %d):\n%s", res.Code, res.Stderr)
	}
	var out []string
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func extractChangedSymbols(root, base, head string) ([]symbol, error) {
	paths, err := changedPaths(root, base, head)
	if err != nil {
		return nil, err
	}
	var syms []symbol
	for _, rel := range paths {
		if strings.SplitN(rel, "/", 2)[0] == "tests" {
			continue
		}
		abs := filepath.Join(root, rel)
		bt := parseOrNil(gitShow(root, base, rel))
		ht := parseOrNil(gitShow(root, head, rel))
		if bt == nil {
			continue
		}
		headIDs := map[string]bool{}
		if ht != nil {
			for _, d := range pyast.Defs(ht) {
				headIDs[d.Name] = true
			}
		}
		seen := map[[2]any]bool{}
		var ids []pyast.Def
		for _, d := range pyast.Defs(bt) {
			k := [2]any{d.Name, d.Line}
			if !seen[k] {
				seen[k] = true
				ids = append(ids, d)
			}
		}
		sort.SliceStable(ids, func(i, j int) bool { return ids[i].Line < ids[j].Line })
		for _, d := range ids {
			if !headIDs[d.Name] && !headStillExports(ht, d.Name) {
				syms = append(syms, symbol{d.Name, "identifier", abs, d.Line})
			}
		}
		headLits := map[string]bool{}
		if ht != nil {
			for _, c := range pyast.StrConstants(ht) {
				headLits[c.Value] = true
			}
		}
		litSeen := map[[2]any]bool{}
		var lits []pyast.StrConst
		for _, c := range pyast.StrConstants(bt) {
			k := [2]any{c.Value, c.Line}
			if !litSeen[k] {
				litSeen[k] = true
				lits = append(lits, c)
			}
		}
		sort.SliceStable(lits, func(i, j int) bool { return lits[i].Line < lits[j].Line })
		for _, c := range lits {
			if !headLits[c.Value] && !isNoiseLiteral(c.Value) {
				syms = append(syms, symbol{c.Value, "literal", abs, c.Line})
			}
		}
	}
	return syms, nil
}

func containsLiteral(n *pyast.Node, lit string) bool {
	found := false
	pyast.Walk(n, func(x *pyast.Node) {
		if x.Kind == "Constant" && x.IsStr && x.Str == lit {
			found = true
		}
	})
	return found
}

func checksLiteralAbsence(a *pyast.Node, lit string) bool {
	if a.Kind == "Assert" {
		res := false
		pyast.Walk(a.Test, func(x *pyast.Node) {
			if x.Kind != "Compare" || res {
				return
			}
			for i, op := range x.Ops {
				if op == "NotIn" && (containsLiteral(x.Left, lit) || containsLiteral(x.Comparators[i], lit)) {
					res = true
				}
			}
		})
		return res
	}
	if a.Kind == "Call" && a.Func != nil && a.Func.Kind == "Attribute" && a.Func.Name == "assertNotIn" {
		for _, arg := range a.Args {
			if containsLiteral(arg, lit) {
				return true
			}
		}
	}
	return false
}

func isMessageCapture(n *pyast.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == "Call" && n.Func != nil && n.Func.Kind == "Name" && (n.Func.Name == "str" || n.Func.Name == "repr") {
		return true
	}
	if n.Kind == "Attribute" {
		switch n.Name {
		case "message", "stderr", "stdout", "output", "value":
			return true
		}
		if v := n.Value; v != nil && v.Kind == "Call" && v.Func != nil && v.Func.Kind == "Attribute" && v.Func.Name == "readouterr" {
			return true
		}
	}
	return false
}

func checksLiteralInMessage(a *pyast.Node, lit string) bool {
	res := false
	pyast.Walk(a, func(x *pyast.Node) {
		if x.Kind != "Compare" || res {
			return
		}
		for i, op := range x.Ops {
			if op != "In" && op != "NotIn" {
				continue
			}
			c := x.Comparators[i]
			if containsLiteral(x.Left, lit) && isMessageCapture(c) || containsLiteral(c, lit) && isMessageCapture(x.Left) {
				res = true
			}
		}
	})
	return res
}

func collapsedHint(lit string, sites []symbol) string {
	p := sites[0]
	hint := fmt.Sprintf("Assertion contains string literal %s which was removed from %s:%d", pyjson.ReprString(lit), filepath.Base(p.file), p.line)
	extras := sites[1:]
	if len(extras) == 0 {
		return hint
	}
	named := extras
	if len(named) > 3 {
		named = named[:3]
	}
	var parts []string
	for _, s := range named {
		parts = append(parts, fmt.Sprintf("%s:%d", filepath.Base(s.file), s.line))
	}
	text := strings.Join(parts, ", ")
	if hidden := len(extras) - len(named); hidden > 0 {
		text += fmt.Sprintf(", +%d more", hidden)
	}
	return fmt.Sprintf("%s (+%d more removal site(s): %s)", hint, len(extras), text)
}

func scanTestFile(path string, syms []symbol) []Finding {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	tree := parseOrNil(strings.ToValidUTF8(string(b), "�"))
	if tree == nil {
		return nil
	}
	var idOrder []string
	ids := map[string]symbol{}
	var litOrder []string
	lits := map[string][]symbol{}
	for _, s := range syms {
		if s.kind == "identifier" {
			if _, ok := ids[s.name]; !ok {
				idOrder = append(idOrder, s.name)
			}
			ids[s.name] = s
		} else {
			if _, ok := lits[s.name]; !ok {
				litOrder = append(litOrder, s.name)
			}
			lits[s.name] = append(lits[s.name], s)
		}
	}
	var out []Finding
	pyast.Walk(tree, func(a *pyast.Node) {
		if !pyast.IsAssertionBearing(a) {
			return
		}
		names := pyast.NamesIn(a)
		for _, name := range idOrder {
			if !names[name] {
				continue
			}
			s := ids[name]
			conf := "medium"
			if a.Kind == "Assert" {
				if pyast.NamesIn(a.Test)[name] {
					conf = "high"
				}
			} else {
				for _, arg := range a.Args {
					if pyast.NamesIn(arg)[name] {
						conf = "high"
						break
					}
				}
				if conf != "high" {
					for _, kw := range a.Keywords {
						if pyast.NamesIn(kw.Value)[name] {
							conf = "high"
							break
						}
					}
				}
			}
			out = append(out, Finding{TestFile: path, TestLine: a.Line, SourceFile: s.file, SourceLine: s.line, ChangedSymbol: name,
				Confidence: conf, Hint: fmt.Sprintf("Assertion references '%s' which was renamed/removed in %s:%d", name, filepath.Base(s.file), s.line)})
		}
		consts := pyast.StrValuesIn(a)
		for _, lit := range litOrder {
			if checksLiteralAbsence(a, lit) || !consts[lit] {
				continue
			}
			grade, label := "low", ""
			if checksLiteralInMessage(a, lit) {
				grade, label = "info", "message-content-check"
			}
			sites := append([]symbol{}, lits[lit]...)
			sort.SliceStable(sites, func(i, j int) bool {
				bi, bj := filepath.Base(sites[i].file), filepath.Base(sites[j].file)
				if bi != bj {
					return bi < bj
				}
				return sites[i].line < sites[j].line
			})
			out = append(out, Finding{TestFile: path, TestLine: a.Line, SourceFile: sites[0].file, SourceLine: sites[0].line,
				ChangedSymbol: lit, Confidence: grade, Hint: collapsedHint(lit, sites), Label: label})
		}
	})
	return out
}

func diffLOC(root, base, head string) int {
	out, err := gitx.Run(root, "diff", "--shortstat", base, head, "--", "*.py")
	if err != nil {
		return 0
	}
	ins, del := 0, 0
	for _, tok := range strings.Split(out, ",") {
		tok = strings.TrimSpace(tok)
		f := strings.Fields(tok)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		if strings.Contains(tok, "insertion") {
			ins = n
		} else if strings.Contains(tok, "deletion") {
			del = n
		}
	}
	return ins + del
}

// RunCheck is run_check(base_ref, head_ref, repo_root).
func RunCheck(base, head, root string) (*Report, error) {
	start := time.Now()
	syms, err := extractChangedSymbols(root, base, head)
	if err != nil {
		return nil, err
	}
	var testFiles []string
	if top, err := gitx.Toplevel(root); err == nil {
		res, _ := gitx.Exec(root, nil, nil, "--literal-pathspecs", "ls-files", "--full-name", "-z", "--", "tests/")
		if res.Code == 0 {
			for _, p := range strings.Split(res.Stdout, "\x00") {
				if strings.HasSuffix(p, ".py") {
					testFiles = append(testFiles, filepath.Join(top, p))
				}
			}
		}
	}
	rep := &Report{BaseRef: base, HeadRef: head, RepoRoot: root}
	if len(syms) > 0 {
		for _, tf := range testFiles {
			rep.Findings = append(rep.Findings, scanTestFile(tf, syms)...)
			rep.FilesScanned++
		}
	} else {
		rep.FilesScanned = len(testFiles)
	}
	rep.ElapsedSeconds = time.Since(start).Seconds()
	if loc := diffLOC(root, base, head); loc > 0 {
		rep.FindingsPer100LOC = float64(len(rep.Findings)) / float64(loc) * 100.0
	}
	return rep, nil
}
