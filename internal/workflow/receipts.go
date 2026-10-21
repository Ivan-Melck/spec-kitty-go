package workflow

import (
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Receipt is one workflow commit receipt (_WORKFLOW_COMMIT_RECEIPTS).
type Receipt struct {
	DestinationRef string
	Message        string
	Outcome        string // committed | refused
	SHA            string
	WPID           string
}

// Receipts accumulates the commit receipts of one command invocation.
type Receipts struct{ list []*Receipt }

// Record appends a receipt.
func (r *Receipts) Record(ref, message, outcome, sha, wpID string) {
	r.list = append(r.list, &Receipt{DestinationRef: ref, Message: message, Outcome: outcome, SHA: sha, WPID: wpID})
}

// MarkRefused flips the receipt carrying sha to refused.
func (r *Receipts) MarkRefused(sha string) {
	for i := len(r.list) - 1; i >= 0; i-- {
		if r.list[i].SHA == sha {
			r.list[i].Outcome = "refused"
			return
		}
	}
}

// Len returns the number of receipts.
func (r *Receipts) Len() int { return len(r.list) }

// PrintSummary is _print_commit_summary (human format).
func (r *Receipts) PrintSummary(o *Out, command string) {
	if len(r.list) == 0 {
		return
	}
	o.Printf("[%s] Commits recorded:", command)
	for _, rc := range r.list {
		glyph := "[refused]"
		if rc.Outcome == "committed" {
			glyph = "[ok]"
		}
		sha := "-------"
		if rc.SHA != "" {
			sha = rc.SHA
			if len(sha) > 7 {
				sha = sha[:7]
			}
		}
		o.Printf("  - %s  %s  %s  %s", rc.DestinationRef, sha, rc.Message, glyph)
	}
}

// JSON renders {"commits": [...]}.
func (r *Receipts) JSON() *jsonx.Object {
	var items []any
	for _, rc := range r.list {
		var sha, wp any
		if rc.SHA != "" {
			sha = rc.SHA
		}
		if rc.WPID != "" {
			wp = rc.WPID
		}
		items = append(items, jsonx.NewObject("destination_ref", rc.DestinationRef, "message", rc.Message,
			"outcome", rc.Outcome, "sha", sha, "wp_id", wp))
	}
	if items == nil {
		items = []any{}
	}
	return jsonx.NewObject("commits", items)
}
