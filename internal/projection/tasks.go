package projection

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	workgraphreadmodel "github.com/spec-kitty/go-kitty3/internal/workgraph/readmodel"
)

// RenderTasks produces a pure, deterministic Markdown projection. It does not
// read a clock, filesystem, tracker, provider, or an earlier projection.
func RenderTasks(view TasksView) ([]byte, error) {
	if err := view.Validate(); err != nil {
		return nil, err
	}
	mission, graph := view.Mission(), view.Workgraph()
	var output bytes.Buffer
	line(&output, "# GENERATED, NON-AUTHORITATIVE PROJECTION")
	line(&output, "")
	line(&output, "> **DO NOT USE THIS FILE AS TASK AUTHORITY.** Edits, checkbox changes, deletion, and prose changes have no effect.")
	line(&output, "> The tracker reports what work exists. Spec Kitty query surfaces report what may run and how it is accepted.")
	line(&output, "")
	line(&output, "## Canonical source")
	line(&output, "")
	field(&output, "Mission ID", mission.MissionID().String())
	field(&output, "Mission state version", strconv.FormatUint(mission.StateVersion(), 10))
	field(&output, "Current step", mission.CurrentStep())
	field(&output, "WorkRevision ID", graph.RevisionID().String())
	field(&output, "Generation", strconv.FormatUint(graph.Generation(), 10))
	field(&output, "Graph digest", graph.GraphDigest())
	field(&output, "Drift observation", string(graph.DriftStatus()))
	field(&output, "Rendered at", view.RenderedAt().Format(time.RFC3339Nano))
	line(&output, "- Regeneration query: `projection tasks --mission "+inline(mission.MissionID().String())+"` (supply any output destination externally)")
	line(&output, "")
	line(&output, "## Execution lanes")
	line(&output, "")
	for _, lane := range graph.Lanes() {
		line(&output, "### Lane `"+inline(lane.LaneID().String())+"`")
		field(&output, "Work", joinWork(lane.WorkIDs()))
		field(&output, "Depends on lanes", joinLane(lane.DependencyLaneIDs()))
		line(&output, "")
	}

	laneByWork := make(map[ids.WorkID]ids.LaneID)
	for _, lane := range graph.Lanes() {
		for _, workID := range lane.WorkIDs() {
			laneByWork[workID] = lane.LaneID()
		}
	}
	line(&output, "## Work items")
	line(&output, "")
	for _, work := range graph.WorkItems() {
		line(&output, "### Work `"+inline(work.WorkID().String())+"`")
		field(&output, "Title", work.Title())
		field(&output, "Lane", laneByWork[work.WorkID()].String())
		field(&output, "Dependencies", joinWork(work.Dependencies()))
		block(&output, "Description", work.Description())
		block(&output, "Design context", work.DesignContext())
		line(&output, "#### Acceptance criteria")
		if len(work.Criteria()) == 0 {
			line(&output, "")
			line(&output, "None.")
		} else {
			for _, criterion := range work.Criteria() {
				line(&output, "")
				line(&output, "- Criterion `"+inline(criterion.ID())+"`")
				field(&output, "  Text", criterion.Text())
				field(&output, "  Required", strconv.FormatBool(criterion.Required()))
				field(&output, "  Verifier", criterion.VerifierKind())
				field(&output, "  Metadata JSON", string(criterion.MetadataJSON()))
			}
		}
		line(&output, "")
		line(&output, "#### Write scopes")
		if len(work.WriteScopes()) == 0 {
			line(&output, "")
			line(&output, "None.")
		} else {
			for _, scope := range work.WriteScopes() {
				line(&output, "")
				line(&output, "- Path `"+inline(scope.Path())+"`")
				field(&output, "  Domain", scope.Domain())
				field(&output, "  Access", scope.Access())
				field(&output, "  Serialization key", empty(scope.SerializationKey()))
			}
		}
		line(&output, "")
	}

	line(&output, "## Reconciliation facts")
	line(&output, "")
	if len(graph.Reconciliations()) == 0 {
		line(&output, "None.")
	} else {
		for _, fact := range graph.Reconciliations() {
			line(&output, "### Previous work `"+inline(fact.PreviousWorkID().String())+"`")
			field(&output, "Disposition", string(fact.Disposition()))
			field(&output, "Current work", joinWork(fact.CurrentWorkIDs()))
			block(&output, "Rationale", fact.Rationale())
			line(&output, "")
		}
	}
	canonical := bytes.TrimRight(output.Bytes(), "\n")
	canonical = append(canonical, '\n')
	return canonical, nil
}

func line(output *bytes.Buffer, value string) {
	output.WriteString(strings.TrimRight(value, " \t") + "\n")
}
func field(output *bytes.Buffer, name, value string) {
	line(output, "- **"+name+":** "+inline(empty(value)))
}
func block(output *bytes.Buffer, name, value string) {
	line(output, "")
	line(output, "**"+name+":**")
	line(output, "")
	if value == "" {
		value = "Not provided."
	}
	for _, part := range strings.Split(normalize(value, true), "\n") {
		line(output, "> "+part)
	}
}
func empty(value string) string {
	if value == "" {
		return "None"
	}
	return value
}
func joinWork(values []ids.WorkID) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = "`" + inline(v.String()) + "`"
	}
	if len(parts) == 0 {
		return "None"
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
func joinLane(values []ids.LaneID) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = "`" + inline(v.String()) + "`"
	}
	if len(parts) == 0 {
		return "None"
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func inline(value string) string { return normalize(value, false) }

// normalize visibly escapes Markdown structure, HTML, controls, bidi controls,
// and invalid bytes. Newlines survive only for controlled blockquote rendering.
func normalize(value string, multiline bool) string {
	var output strings.Builder
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if r == utf8.RuneError && size == 1 {
			output.WriteString(`\uFFFD`)
			value = value[1:]
			continue
		}
		value = value[size:]
		if r == '\n' && multiline {
			output.WriteByte('\n')
			continue
		}
		if r == '\r' {
			if multiline {
				output.WriteString(`\r`)
			} else {
				output.WriteString(`\r`)
			}
			continue
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control, unicode.Cf) {
			visible(&output, r)
			continue
		}
		switch r {
		case '\\', '`', '*', '_', '{', '}', '[', ']', '(', ')', '#', '+', '-', '.', '!', '|', '>', '<', '~':
			output.WriteByte('\\')
			output.WriteRune(r)
		default:
			output.WriteRune(r)
		}
	}
	return output.String()
}

func visible(output *strings.Builder, value rune) {
	if value <= 0xffff {
		fmt.Fprintf(output, `\u%04X`, value)
	} else {
		fmt.Fprintf(output, `\U%08X`, value)
	}
}

var _ workgraphreadmodel.DriftStatus = workgraphreadmodel.DriftNotChecked
