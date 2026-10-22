package ownedcheckout

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// Help is OWNED_CHECKOUT_HELP.
const Help = "Run against an owned checkout: a linked checkout that owns this mission. Refuses the repository root checkout, lane worktrees and coordination worktrees."

// Envelope builds a command's JSON refusal payload from (code, message).
type Envelope func(code, message string) *jsonx.Object

// SuccessFalseEnvelope is success_false_envelope (agent context resolve).
func SuccessFalseEnvelope(code, message string) *jsonx.Object {
	return jsonx.NewObject("success", false, "error_code", code, "error", message)
}

// JSONErrorEnvelope is json_error_envelope (agent tasks status):
// {"ok": false, "error": {"code", "message"}, "error_code"}.
func JSONErrorEnvelope(code, message string) *jsonx.Object {
	return jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", code, "message", message), "error_code", code)
}

// FlatErrorEnvelope is flat_error_envelope (move-task, check-prerequisites).
func FlatErrorEnvelope(code, message string) *jsonx.Object {
	return jsonx.NewObject("error", message, "error_code", code)
}

// ResultErrorEnvelope is result_error_envelope (setup-plan).
func ResultErrorEnvelope(code, message string) *jsonx.Object {
	return jsonx.NewObject("result", "error", "phase_complete", false, "error_code", code, "error", message)
}

// Payload is emit_owned_refusal's JSON payload (error_code added when the
// envelope omits it).
func Payload(e *Error, envelope Envelope) *jsonx.Object {
	p := envelope(e.Code, e.Message)
	if _, ok := p.Get("error_code"); !ok {
		p.Set("error_code", e.Code)
	}
	return p
}

// Emit is emit_owned_refusal: JSON mode prints the envelope (indent=2) on
// stdout; human mode prints `Error: [<code>] <message>` on stderr (rich
// console, wrapped). The caller exits 1.
func Emit(stdout, stderr io.Writer, e *Error, jsonMode bool, envelope Envelope) {
	if jsonMode {
		fmt.Fprintln(stdout, jsonx.Dumps(Payload(e, envelope), jsonx.Options{Indent: 2}))
		return
	}
	rich.Print(stderr, "Error: ["+e.Code+"] "+e.Message)
}

// StaleCopy is stale_repository_root_copy: R's copy of the owned mission
// ({"path", "mission_id"}), or nil when R holds none with the same id.
func StaleCopy(f *Fact) *jsonx.Object {
	dir := project.Specs(f.RepositoryRoot, mission.CoordMissionDirName(f.MissionSlug, mission.Mid8FromSlug(f.MissionSlug)))
	if mission.Mid8FromSlug(f.MissionSlug) == "" {
		dir = project.Specs(f.RepositoryRoot, f.MissionSlug)
	}
	rid := mission.ReadMetaOrEmpty(dir).Get
	pid := mission.ReadMetaOrEmpty(f.MissionDir).Get
	rv, _ := rid("mission_id")
	pv, _ := pid("mission_id")
	rs, ok1 := rv.(string)
	ps, ok2 := pv.(string)
	if !ok1 || !ok2 || rs == "" || ps == "" || rs != ps {
		return nil
	}
	return jsonx.NewObject("path", dir, "mission_id", rs)
}

// StaleCopyWarningText is STALE_COPY_WARNING.
func StaleCopyWarningText(f *Fact, v *jsonx.Object) string {
	return fmt.Sprintf("The repository root checkout holds a stale copy of mission %s at %s; the owned checkout %s is authoritative.",
		f.MissionSlug, v.GetString("path"), f.OwnedRoot)
}

// StaleCopyValue returns the stale_repository_root_copy JSON value (nil →
// null) and appends the warning text to warnings when both apply.
func StaleCopyValue(f *Fact, warnings *[]string) any {
	v := StaleCopy(f)
	if v == nil {
		return nil
	}
	if warnings != nil {
		*warnings = append(*warnings, StaleCopyWarningText(f, v))
	}
	return v
}

// EchoStaleCopyWarning prints the stale-copy text on stderr when R holds a
// copy.
func EchoStaleCopyWarning(stderr io.Writer, f *Fact) {
	if v := StaleCopy(f); v != nil {
		rich.Print(stderr, StaleCopyWarningText(f, v))
	}
}

// MissionRel is the mission dir relative to the owned root
// (kitty-specs/<slug>).
func (f *Fact) MissionRel() string {
	r, err := filepath.Rel(f.OwnedRoot, f.MissionDir)
	if err != nil {
		return filepath.Join("kitty-specs", f.MissionSlug)
	}
	return r
}
