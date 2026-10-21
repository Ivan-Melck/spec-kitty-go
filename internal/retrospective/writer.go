package retrospective

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Width is CANONICAL_YAML_WIDTH.
const Width = 120

// Serialize renders a mapping like kernel.yaml_io.serialize_mapping
// (ruamel round-trip, block style, sequences at offset 0, width 120).
func Serialize(o *jsonx.Object) string {
	var b strings.Builder
	writeMapping(&b, o, 0)
	return b.String()
}

func writeMapping(b *strings.Builder, o *jsonx.Object, indent int) {
	pad := strings.Repeat(" ", indent)
	for i, kv := range o.Pairs() {
		prefix := pad
		if i == 0 && indent > 0 && b.Len() > 0 && strings.HasSuffix(b.String(), "- ") {
			prefix = ""
		}
		writeKey(b, prefix, kv.Key, kv.Value, indent)
	}
}

func writeKey(b *strings.Builder, prefix, key string, v any, indent int) {
	switch x := v.(type) {
	case *jsonx.Object:
		if x.Len() == 0 {
			fmt.Fprintf(b, "%s%s: {}\n", prefix, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", prefix, key)
		writeMapping(b, x, indent+2)
	case []any:
		if len(x) == 0 {
			fmt.Fprintf(b, "%s%s: []\n", prefix, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", prefix, key)
		writeSequence(b, x, indent)
	case nil:
		fmt.Fprintf(b, "%s%s:\n", prefix, key)
	default:
		head := prefix + key + ":"
		writeScalar(b, head, v, indent+2)
	}
}

func writeSequence(b *strings.Builder, l []any, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, item := range l {
		switch x := item.(type) {
		case *jsonx.Object:
			b.WriteString(pad + "- ")
			first := true
			for _, kv := range x.Pairs() {
				p := pad + "  "
				if first {
					p = ""
					first = false
				}
				writeKey(b, p, kv.Key, kv.Value, indent+2)
			}
		default:
			writeScalar(b, pad+"-", item, indent+2)
		}
	}
}

// writeScalar writes " value" after head, folding plain strings at Width.
func writeScalar(b *strings.Builder, head string, v any, contIndent int) {
	text := yamlx.FormatScalar(v)
	s, isStr := v.(string)
	if !isStr || text != s || !strings.Contains(s, " ") {
		b.WriteString(head + " " + text + "\n")
		return
	}
	line := head
	col := utf8.RuneCountInString(head)
	pad := strings.Repeat(" ", contIndent)
	atStart := false
	for _, word := range strings.Split(s, " ") {
		wl := utf8.RuneCountInString(word)
		if !atStart && col+1+wl > Width {
			b.WriteString(line + "\n")
			line, col = pad+word, contIndent+wl
			continue
		}
		if atStart {
			line += word
			col += wl
			atStart = false
			continue
		}
		line += " " + word
		col += 1 + wl
	}
	b.WriteString(line + "\n")
}

// Capture generates and writes kitty-specs/<slug>/retrospective.yaml and
// appends the RetrospectiveCaptured event (no-op when the record exists).
// Returns the record path ("" when nothing was written).
func Capture(root, slug string) (string, error) {
	if exists(filepath.Join(project.Specs(root, slug), FileName)) {
		return "", nil
	}
	_, path, err := Author(root, slug, "", "")
	return path, err
}

// Author generates and writes the record (overwriting) with the given
// provenance kind/command ("" = runtime terminus capture) and appends the
// RetrospectiveCaptured event. Returns the record and its path.
func Author(root, slug, kind, command string) (*Record, string, error) {
	dir := project.Specs(root, slug)
	path := filepath.Join(dir, FileName)
	now := Now()
	rec, err := Generate(root, slug, now)
	if err != nil {
		return nil, "", err
	}
	rec.ProvenanceKind, rec.Command, rec.Human = kind, command, kind != ""
	if err := os.WriteFile(path, []byte(Serialize(rec.ToObject())), 0o644); err != nil {
		return nil, "", err
	}
	actor := jsonx.NewObject("kind", "runtime", "id", "spec-kitty-generator", "display", nil)
	if rec.Human {
		actor = jsonx.NewObject("kind", "human", "id", "cli", "display", "spec-kitty retrospect")
	}
	pk := kind
	if pk == "" {
		pk = "runtime_post_completion"
	}
	ev := jsonx.NewObject("type", "RetrospectiveCaptured", "schema_version", 1, "event_id", ulid.New(), "lamport", nextLamport(dir),
		"at", Now(), "actor", actor,
		"mission_id", rec.MissionID, "mission_slug", rec.Slug, "wp_id", nil, "force", false, "execution_mode", "main",
		"findings_status", rec.FindingsStatus, "record_path", "kitty-specs/"+slug+"/"+FileName, "generator_version", GeneratorVersion,
		"policy_source", rec.PolicySource.Clone(), "provenance_kind", pk, "proposal_count", 0,
		"evidence_ref_count", len(rec.Evidence))
	if err := status.AppendRows(status.EventsPath(dir), ev); err != nil {
		return rec, path, err
	}
	return rec, path, nil
}

func nextLamport(dir string) int {
	last := 0
	for _, e := range loadEvents(dir) {
		if n, ok := e.GetInt("lamport"); ok && n > last {
			last = n
		}
	}
	return last + 1
}
