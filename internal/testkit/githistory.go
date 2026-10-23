package testkit

import (
	"sort"
	"strings"
)

// GitHistory renders every commit reachable from the refs (and HEAD) of the
// repository at dir (relative to the sandbox project, "" for the project
// itself) in an order that depends only on the commit graph, never on commit
// timestamps: refs in name order, each followed by the commits first
// reachable from it, walked depth-first from the tip (first parent before
// the other parents). `git log --all --topo-order` instead interleaves
// independent branches by commit date, so two sandboxes whose commits fell
// into different seconds render the same graph differently.
//
// format is a git log --format string (without %H/%P, which this adds);
// extra args (e.g. "--name-status") go to git log. The output is not
// normalized.
func (s *Sandbox) GitHistory(dir, format string, extra ...string) string {
	s.T.Helper()
	const sep = "\x1e"
	args := append([]string{"log", "--all", "--format=" + sep + "%H %P%n" + format}, extra...)
	r := s.Exec(dir, "git", args...)
	type commit struct {
		parents []string
		text    string
	}
	commits := map[string]*commit{}
	for _, block := range strings.Split(r.Stdout, sep) {
		if block == "" {
			continue
		}
		head, text, _ := strings.Cut(block, "\n")
		ids := strings.Fields(head)
		if len(ids) == 0 {
			continue
		}
		commits[ids[0]] = &commit{parents: ids[1:], text: text}
	}
	var refs []string
	for _, ref := range strings.Fields(s.Exec(dir, "git", "for-each-ref", "--format=%(refname)").Stdout) {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	refs = append(refs, "HEAD")
	seen := map[string]bool{}
	var b strings.Builder
	for _, ref := range refs {
		tip := strings.TrimSpace(s.Exec(dir, "git", "rev-parse", "--verify", "--quiet", ref+"^{commit}").Stdout)
		b.WriteString("@ " + ref + "\n")
		stack := []string{tip}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			c, ok := commits[id]
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			b.WriteString(c.text)
			if !strings.HasSuffix(c.text, "\n") {
				b.WriteString("\n")
			}
			for i := len(c.parents) - 1; i >= 0; i-- {
				stack = append(stack, c.parents[i])
			}
		}
	}
	return b.String()
}
