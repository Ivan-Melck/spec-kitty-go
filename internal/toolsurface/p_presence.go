package toolsurface

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

const (
	presenceRepairHint = "spec-kitty doctor tool-surfaces --kind context_file --fix"
	hookRepairHint     = "spec-kitty doctor tool-surfaces --kind hook --fix"
)

func presenceDefinition(kind string) Definition {
	act, hint := "always", presenceRepairHint
	switch kind {
	case KindHook:
		act, hint = "event", hookRepairHint
	case KindRule:
		act = "glob"
	}
	return Definition{Kind: kind, SourceKind: "generated", Scope: "project", PathPattern: "<session-presence>", Required: "repairable_required",
		Activation: act, Provider: "session_presence", RepairHint: hint}
}

func markdownKind(rulesPath string) string {
	l := strings.ToLower(rulesPath)
	for _, m := range []string{"/rules/", "/steering/", ".mdc"} {
		if strings.Contains(l, m) {
			return KindRule
		}
	}
	return KindContextFile
}

type managedSurface struct{ path, kind string }

func managedSurfaces(w agents.PresenceWriter, root string) []managedSurface {
	if w.RulesPath == "" {
		return nil
	}
	if w.Claude {
		settings := filepath.Join(root, ".claude", "settings.json")
		return []managedSurface{{filepath.Join(root, filepath.FromSlash(w.RulesPath)), KindContextFile}, {settings, KindHook}, {settings, KindHook}}
	}
	return []managedSurface{{filepath.Join(root, filepath.FromSlash(w.RulesPath)), markdownKind(w.RulesPath)}}
}

type sessionPresenceProvider struct{}

func (sessionPresenceProvider) key() string { return "session_presence" }
func (sessionPresenceProvider) handles(kind string) bool {
	return kind == KindContextFile || kind == KindHook || kind == KindRule
}
func (sessionPresenceProvider) definitions() []Definition {
	return []Definition{presenceDefinition(KindContextFile), presenceDefinition(KindHook), presenceDefinition(KindRule)}
}

func (sessionPresenceProvider) expand(def Definition, tool, root string) []Instance {
	w := agents.WriterFor(tool)
	if w.RulesPath == "" {
		return []Instance{{Def: presenceDefinition(KindContextFile), Path: "<unsupported>", Owner: tool}}
	}
	var out []Instance
	for _, m := range managedSurfaces(w, root) {
		if m.kind != def.Kind {
			continue
		}
		out = append(out, Instance{Def: presenceDefinition(m.kind), Path: m.path, Owner: tool})
	}
	return out
}

func orientationSectionPresent(target string) bool {
	b, err := os.ReadFile(target)
	return err == nil && strings.Contains(string(b), agents.SectionOpen)
}

func onDiskOrientationVersion(target string) string {
	b, err := os.ReadFile(target)
	if err != nil {
		return ""
	}
	text := string(b)
	open := strings.Index(text, agents.SectionOpen)
	if open < 0 {
		return ""
	}
	section := text[open:]
	if c := strings.Index(section, agents.SectionClose); c >= 0 {
		section = section[:c]
	}
	start := strings.Index(section, "**Spec Kitty v")
	if start < 0 {
		return ""
	}
	start += len("**Spec Kitty v")
	end := strings.Index(section[start:], "**")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(section[start : start+end])
}

func (p sessionPresenceProvider) probe(c *collectCtx, inst Instance) Status {
	if inst.Path == "<unsupported>" {
		f := Finding{Code: "research-gap-surface", Severity: "info", Message: "No known session-presence mechanism for " + inst.Owner + ".",
			Tool: inst.Owner, HasTool: true, SurfaceID: inst.ID(), HasSurface: true}
		return Status{inst, StateNotApplicable, []Finding{f}}
	}
	w := agents.WriterFor(inst.Owner)
	kind := inst.Def.Kind
	present := false
	switch {
	case kind == KindHook && w.Claude:
		root := strings.TrimSuffix(inst.Path, string(filepath.Separator)+filepath.Join(".claude", "settings.json"))
		present = agents.HookRegistered(root, "SessionStart", "spec-kitty session-start") && agents.HookRegistered(root, "Stop", "spec-kitty session-stop")
	case w.RulesPath != "":
		present = orientationSectionPresent(inst.Path)
	default:
		_, err := os.Stat(inst.Path)
		present = err == nil
	}
	if present {
		if (kind == KindContextFile || kind == KindRule) && w.RulesPath != "" {
			if v := onDiskOrientationVersion(inst.Path); v != "" && v != app.Version {
				return Status{inst, StateStale, []Finding{instFinding("stale-generated-surface", "warning",
					fmt.Sprintf("Orientation version stale for %s: %s", inst.Owner, inst.Path), inst, true, presenceRepairHint)}}
			}
		}
		return Status{inst, StatePresent, nil}
	}
	if kind == KindContextFile {
		return Status{inst, StateMissing, []Finding{instFinding("context-file-missing", "error",
			fmt.Sprintf("Always-on context file missing for %s: %s", inst.Owner, inst.Path), inst, true, presenceRepairHint)}}
	}
	label, hint := "rule file", presenceRepairHint
	if kind == KindHook {
		label, hint = "hook entry", hookRepairHint
	}
	return Status{inst, StateMissing, []Finding{instFinding("session-presence-incomplete", "error",
		fmt.Sprintf("Session-presence %s missing for %s: %s", label, inst.Owner, inst.Path), inst, true, hint)}}
}

// presenceMember is markdown_rules.PreparedPresenceFile.
type presenceMember struct {
	Path        string // root-relative POSIX
	Before      FileState
	Content     []byte
	Disposition string
	Reason      string
}

func (m presenceMember) changed() bool {
	return m.Disposition != "preserve" && m.Before.SHA256 != Digest(m.Content)
}

// PreparedSessionBatch is session_presence.PreparedSessionBatch.
type PreparedSessionBatch struct {
	Files []struct {
		Tool   string
		Member presenceMember
	}
}

// observePresencePath is observe_presence_path: every node from the root
// to rel; a symlink anywhere refuses. Returns (rel name, state) pairs.
func observePresencePath(root, rel string) ([][2]any, error) {
	if filepath.IsAbs(rel) {
		return nil, errors.New("Unconfined presence path")
	}
	parts := []string{}
	if rel != "." && rel != "" {
		parts = strings.Split(rel, "/")
	}
	for _, p := range parts {
		if p == ".." {
			return nil, errors.New("Unconfined presence path")
		}
	}
	names := []string{"."}
	for i := 1; i <= len(parts); i++ {
		names = append(names, strings.Join(parts[:i], "/"))
	}
	var out [][2]any
	for i, n := range names {
		abs := root
		if n != "." {
			abs = filepath.Join(root, filepath.FromSlash(n))
		}
		info, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			out = append(out, [2]any{n, Absent()})
			continue
		}
		if err != nil {
			return nil, err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return nil, fmt.Errorf("Refusing unowned symlink: %s", n)
		case info.IsDir():
			out = append(out, [2]any{n, DirOf(int(info.Mode().Perm()))})
		case info.Mode().IsRegular() && i == len(names)-1:
			b, err := os.ReadFile(abs)
			if err != nil {
				return nil, err
			}
			out = append(out, [2]any{n, FileOf(Digest(b), int(info.Mode().Perm()))})
		default:
			return nil, fmt.Errorf("Unsupported presence node: %s", n)
		}
	}
	return out, nil
}

func prepareOrientationMember(w agents.PresenceWriter, root string, c agents.Presence) (presenceMember, error) {
	obs, err := observePresencePath(root, w.RulesPath)
	if err != nil {
		return presenceMember{}, err
	}
	before := obs[len(obs)-1][1].(FileState)
	if before.Kind != "file" && before.Kind != "absent" {
		return presenceMember{}, errors.New("Orientation destination is not a regular file")
	}
	original, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(w.RulesPath)))
	m := presenceMember{Path: w.RulesPath, Before: before, Content: original, Disposition: "unchanged", Reason: "Session presence is current"}
	desired, reason, err := w.PrepareOrientation(root, c)
	if err != nil {
		return m, err
	}
	switch {
	case reason != "":
		m.Disposition, m.Reason = "preserve", reason
	case desired != nil:
		m.Content, m.Reason = desired, "Prepare managed orientation"
	}
	return m, nil
}

func prepareHooksMember(root string) (presenceMember, error) {
	obs, err := observePresencePath(root, ".claude/settings.json")
	if err != nil {
		return presenceMember{}, err
	}
	before := obs[len(obs)-1][1].(FileState)
	if before.Kind != "file" && before.Kind != "absent" {
		return presenceMember{}, errors.New("Claude settings destination is not a regular file")
	}
	raw, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	desired, err := agents.PrepareClaudeHooks(root)
	if err != nil {
		return presenceMember{}, err
	}
	content := raw
	if desired != nil {
		content = desired
	}
	return presenceMember{Path: ".claude/settings.json", Before: before, Content: content, Disposition: "unchanged", Reason: "Prepare lifecycle hooks"}, nil
}

// LocalPresenceContent is manager.local_presence_content.
func LocalPresenceContent(root string) agents.Presence {
	return agents.Presence{Version: app.Version, ProjectSlug: filepath.Base(root), Health: "healthy"}
}

func (p sessionPresenceProvider) assess(c *collectCtx, in AssessInputs, statuses []Status, sels []Selection) *Assessment {
	a, err := p.prepare(in, statuses, sels)
	if err != nil {
		return Incomplete("session_presence", in.Root, "session_input_invalid", err.Error())
	}
	return a
}

func (p sessionPresenceProvider) prepare(in AssessInputs, statuses []Status, sels []Selection) (*Assessment, error) {
	root := in.Root.Path
	var observations [][2]any
	obs, err := observePresencePath(root, ".kittify/config.yaml")
	if err != nil {
		return nil, err
	}
	observations = append(observations, obs...)
	cfgState := obs[len(obs)-1][1].(FileState)
	if cfgState.Kind != "file" && cfgState.Kind != "absent" {
		return nil, errors.New("Agent config is not a regular file")
	}
	var configured map[string]bool
	if cfgState.Kind == "file" {
		ac, err := kittify.LoadAgentConfig(root)
		if err != nil {
			return nil, err
		}
		configured = map[string]bool{}
		for _, t := range ac.Available {
			configured[t] = true
		}
	}
	a := &Assessment{OwnerKey: "session_presence", Root: in.Root, Complete: true}
	batch := &PreparedSessionBatch{}
	var eligible []Selection
	for _, s := range sels {
		if s.Def.enabled() && s.Def.Required == "repairable_required" {
			eligible = append(eligible, s)
		}
	}
	var tools []string
	for _, s := range eligible {
		tools = append(tools, s.Tool)
	}
	tools = uniqueSorted(tools)
	if len(tools) == 0 {
		a.Dispositions = append(a.Dispositions, NewDisposition("session_presence", in.Root.ID, "", false, "not_applicable", "No automatically repairable presence selection"))
	}
	var content *agents.Presence
	if pc, ok := in.Projected.(*agents.Presence); ok {
		content = pc
	}
	var effects []Effect
	var artifacts []Artifact
	for _, tool := range tools {
		w := agents.WriterFor(tool)
		kinds := map[string]bool{}
		for _, s := range eligible {
			if s.Tool == tool {
				kinds[s.Def.Kind] = true
			}
		}
		managed := managedSurfaces(w, root)
		applicable := w.RulesPath != "" && (configured == nil || configured[tool])
		if applicable {
			any := false
			for _, m := range managed {
				any = any || kinds[m.kind]
			}
			applicable = any
		}
		if applicable {
			check := w.CheckDir
			if check == "" {
				check = path.Dir(w.RulesPath)
			}
			o, err := observePresencePath(root, check)
			if err != nil {
				return nil, err
			}
			observations = append(observations, o...)
			applicable = w.CanWrite(root)
		}
		if !applicable {
			a.Dispositions = append(a.Dispositions, NewDisposition("session_presence", in.Root.ID, "", false, "not_applicable", "No enabled selected writable presence for "+tool))
			continue
		}
		if content == nil {
			pc := LocalPresenceContent(root)
			content = &pc
		}
		for _, m := range managed {
			o, err := observePresencePath(root, relPosix(root, m.path))
			if err != nil {
				return nil, err
			}
			observations = append(observations, o...)
		}
		var members []presenceMember
		om, err := prepareOrientationMember(w, root, *content)
		if err != nil {
			return nil, err
		}
		members = append(members, om)
		if w.Claude {
			hm, err := prepareHooksMember(root)
			if err != nil {
				return nil, err
			}
			members = append(members, hm)
		}
		var ids []string
		for _, s := range statuses {
			if s.Inst.Owner == tool {
				ids = append(ids, s.Inst.ID())
			}
		}
		for _, m := range members {
			batch.Files = append(batch.Files, struct {
				Tool   string
				Member presenceMember
			}{tool, m})
			if m.changed() {
				effects = append(effects, sessionEffects(in.Root, tool, m, observations, ids)...)
				dir := path.Dir(m.Path)
				art := Artifact{dir, "." + path.Base(m.Path) + "." + strings.Repeat("[0-9a-f]", 32) + ".tmp", "atomic replacement; removed before return"}
				dup := false
				for _, x := range artifacts {
					dup = dup || x == art
				}
				if !dup {
					artifacts = append(artifacts, art)
				}
			} else {
				a.Dispositions = append(a.Dispositions, NewDisposition("session_presence", in.Root.ID, m.Path, true, m.Disposition, m.Reason))
			}
		}
	}
	co, err := Coalesce(effects)
	if err != nil {
		return nil, err
	}
	a.Effects, a.Artifacts, a.Prepared = co, artifacts, batch
	a.Recheck = presenceRecheck(root, observations, "Session input changed: %s", false)
	return a, nil
}

func sessionEffects(root Root, tool string, m presenceMember, observations [][2]any, ids []string) []Effect {
	proof := []Proof{{"managed_path", "session-presence:" + m.Path + ":managed-region"}}
	var out []Effect
	for _, o := range observations {
		name := o[0].(string)
		st := o[1].(FileState)
		if name == "." || st.Kind != "absent" || !strings.HasPrefix(m.Path, name+"/") {
			continue
		}
		out = append(out, NewEffect("session_presence", "surface_repair", root, name, "create", st, DirOf(0o755), "Session writer supporting directory", proof, []string{tool}, ids))
	}
	action := "update"
	if m.Before.Kind == "absent" {
		action = "create"
	}
	mode := 0o644
	if m.Before.Mode >= 0 {
		mode = m.Before.Mode
	}
	return append(out, NewEffect("session_presence", "surface_repair", root, m.Path, action, m.Before, FileOf(Digest(m.Content), mode), m.Reason, proof, []string{tool}, ids))
}

// applySession writes the prepared session members (directories first).
func applySession(a *Assessment) ApplyResult {
	batch, _ := a.Prepared.(*PreparedSessionBatch)
	effects := append([]Effect(nil), a.Effects...)
	sort.SliceStable(effects, func(i, j int) bool {
		di, dj := effects[i].After.Kind != "directory", effects[j].After.Kind != "directory"
		if di != dj {
			return !di
		}
		li, lj := len(strings.Split(effects[i].Path, "/")), len(strings.Split(effects[j].Path, "/"))
		if li != lj {
			return li < lj
		}
		return effects[i].Path < effects[j].Path
	})
	var succeeded []string
	for idx, e := range effects {
		var err error
		if e.After.Kind == "directory" {
			if err = os.Mkdir(e.Destination(), 0o755); err == nil {
				err = os.Chmod(e.Destination(), 0o755)
			}
		} else {
			err = fmt.Errorf("Selected writer is no longer available")
			if batch != nil {
				for _, f := range batch.Files {
					if f.Member.Path == e.Path {
						mode := os.FileMode(0o644)
						if f.Member.Before.Mode >= 0 {
							mode = os.FileMode(f.Member.Before.Mode)
						}
						err = writeFileAtomic(e.Destination(), f.Member.Content, mode)
						break
					}
				}
			}
		}
		if err != nil {
			var rest []string
			for _, x := range effects[idx+1:] {
				rest = append(rest, x.ID())
			}
			out := "failed"
			if len(succeeded) > 0 {
				out = "partial"
			}
			return ApplyResult{Owner: "session_presence", Succeeded: succeeded, Failed: []string{e.ID()}, Skipped: rest, Outcome: out,
				Diagnostics: []Diagnostic{Diag("session_apply_failed", "session_presence", "error", err.Error())}}
		}
		succeeded = append(succeeded, e.ID())
	}
	return ApplyResult{Owner: "session_presence", Succeeded: succeeded, Outcome: "applied"}
}

func (p sessionPresenceProvider) repair(c *collectCtx, root string, statuses []Status) RepairResult {
	actionable := statusesOf(statuses, StateMissing, StateStale)
	skipped := idsOf(statusesOf(statuses, StateNotApplicable))
	if len(actionable) == 0 {
		return RepairResult{Skipped: skipped}
	}
	var sels []Selection
	for _, s := range actionable {
		sels = append(sels, Selection{s.Inst.Owner, s.Inst.Def})
	}
	a := p.assess(c, AssessInputs{Root: Root{"project", "project", root}, Automatic: true}, actionable, sels)
	if !a.Complete {
		var msgs []string
		for _, d := range a.Diagnostics {
			msgs = append(msgs, d.Message)
		}
		return RepairResult{Skipped: skipped, Failed: msgs}
	}
	var failed []string
	if len(a.Effects) > 0 {
		res := applySession(a)
		for _, d := range res.Diagnostics {
			failed = append(failed, d.Message)
		}
	}
	preserved := map[string]bool{}
	var pres []string
	for _, d := range a.Dispositions {
		if d.State == "preserve" {
			preserved[d.Path] = true
			if d.Path != "" {
				pres = append(pres, d.Path)
			}
		}
	}
	eligible := map[string]bool{}
	if b, ok := a.Prepared.(*PreparedSessionBatch); ok {
		for _, f := range b.Files {
			eligible[f.Tool] = true
		}
	}
	var repaired []string
	for _, s := range actionable {
		if eligible[s.Inst.Owner] && len(failed) == 0 && !preserved[relPosix(root, s.Inst.Path)] {
			repaired = append(repaired, s.Inst.ID())
		}
	}
	for _, s := range actionable {
		if !eligible[s.Inst.Owner] {
			skipped = append(skipped, s.Inst.ID())
		}
	}
	sort.Strings(pres)
	pres = uniqueSorted(pres)
	for _, p := range pres {
		failed = append(failed, "Preserved drift: "+p)
	}
	return RepairResult{Repaired: repaired, Skipped: skipped, Failed: failed}
}

// presenceRecheck snapshots the observed presence nodes (files keep their
// mtime, directories their identity) and returns the owner recheck.
func presenceRecheck(root string, observations [][2]any, msg string, fixed bool) func() []Diagnostic {
	var names []string
	seen := map[string]bool{}
	for _, o := range observations {
		n := o[0].(string)
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	abs := func(n string) string {
		if n == "." {
			return root
		}
		return filepath.Join(root, filepath.FromSlash(n))
	}
	before := make([]nodeSig, len(names))
	for i, n := range names {
		before[i] = observeSig(abs(n), false, true)
	}
	owner := "session_presence"
	if fixed {
		owner = "native_config"
	}
	return func() []Diagnostic {
		for i, n := range names {
			if _, err := observePresencePath(root, n); err != nil {
				return []Diagnostic{Diag("precondition_changed", owner, "error", err.Error())}
			}
			if observeSig(abs(n), false, true) != before[i] {
				m := msg
				if !fixed {
					m = fmt.Sprintf(msg, n)
				}
				return []Diagnostic{Diag("precondition_changed", owner, "error", m)}
			}
		}
		return nil
	}
}
