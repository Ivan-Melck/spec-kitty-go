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
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const commandManifestRel = ".kittify/command-skills-manifest.json"

// CommandEntry is manifest_store.ManifestEntry.
type CommandEntry struct {
	Path             string
	ContentHash      string
	Agents           []string // sorted
	InstalledAt      string
	SpecKittyVersion string
}

func (e CommandEntry) equal(o CommandEntry) bool {
	if e.Path != o.Path || e.ContentHash != o.ContentHash || e.InstalledAt != o.InstalledAt || e.SpecKittyVersion != o.SpecKittyVersion || len(e.Agents) != len(o.Agents) {
		return false
	}
	for i := range e.Agents {
		if e.Agents[i] != o.Agents[i] {
			return false
		}
	}
	return true
}

func installerError(code string, ctx *jsonx.Object) error {
	return &agents.InstallerError{Code: code, Context: ctx}
}

// manifestError renders ManifestError(code, **context) like str(exc).
func manifestError(code string, ctx *jsonx.Object) string {
	return (&agents.InstallerError{Code: code, Context: ctx}).Error()
}

// LoadCommandManifest is manifest_store.load with its ManifestError texts
// (wrapped as InstallerError("manifest_parse_failed")).
func LoadCommandManifest(root string) ([]CommandEntry, error) {
	p := filepath.Join(root, filepath.FromSlash(commandManifestRel))
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, installerError("manifest_parse_failed", jsonx.NewObject("detail", err.Error()))
	}
	fail := func(msg string) error {
		return installerError("manifest_parse_failed", jsonx.NewObject("detail", msg))
	}
	v, err := pyjson.Loads(string(b))
	if err != nil {
		return nil, fail(manifestError("corrupt_json", jsonx.NewObject("path", p, "detail", err.Error())))
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fail(manifestError("schema_validation_failed", jsonx.NewObject("detail", "Manifest root must be an object")))
	}
	sv, _ := o.Get("schema_version")
	if n, isInt := sv.(int); !isInt || n != 1 {
		return nil, fail(manifestError("unsupported_schema_version", jsonx.NewObject("found", sv)))
	}
	var out []CommandEntry
	seen := map[string]bool{}
	for _, x := range o.GetList("entries") {
		eo, ok := x.(*jsonx.Object)
		if !ok {
			return nil, fail(manifestError("schema_validation_failed", jsonx.NewObject("errors", []any{"entries item is not an object"})))
		}
		e := CommandEntry{Path: eo.GetString("path"), ContentHash: eo.GetString("content_hash"), InstalledAt: eo.GetString("installed_at"), SpecKittyVersion: eo.GetString("spec_kitty_version")}
		for _, a := range eo.GetList("agents") {
			if s, ok := a.(string); ok {
				e.Agents = append(e.Agents, s)
			}
		}
		sort.Strings(e.Agents)
		if seen[e.Path] {
			return nil, fail(manifestError("duplicate_path", jsonx.NewObject("path", e.Path)))
		}
		seen[e.Path] = true
		out = append(out, e)
	}
	return out, nil
}

// SerializeCommandManifest is manifest_store.serialize.
func SerializeCommandManifest(entries []CommandEntry) []byte {
	sorted := append([]CommandEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	l := []any{}
	for _, e := range sorted {
		ag := make([]any, len(e.Agents))
		for i, a := range e.Agents {
			ag[i] = a
		}
		l = append(l, jsonx.NewObject("path", e.Path, "content_hash", e.ContentHash, "agents", ag, "installed_at", e.InstalledAt, "spec_kitty_version", e.SpecKittyVersion))
	}
	return []byte(jsonx.Dumps(jsonx.NewObject("schema_version", 1, "entries", l), jsonx.Options{Indent: 2, SortKeys: true}) + "\n")
}

// PreparedCommand is command_installer.PreparedCommand.
type PreparedCommand struct {
	Path       string
	Content    []byte // nil for kept/deleted
	Entry      *CommandEntry
	ReportKind string // added, already_installed, reused_shared, regenerated, kept, deleted
	Proof      *Proof
}

// PreparedCommands is command_installer.PreparedCommands.
type PreparedCommands struct {
	Commands      []PreparedCommand
	ManifestBytes []byte
}

type commandBatch struct {
	root         Root
	agents       []string
	provParents  map[string]bool
	consent      map[string]bool
	manifest     []CommandEntry
	original     []CommandEntry
	effects      map[string]Effect
	order        []string
	commands     []PreparedCommand
	dispositions []Disposition
	artifacts    []Artifact
	regenerated  map[string]bool
	now          string
}

func (b *commandBatch) find(rel string) *CommandEntry {
	for i := range b.manifest {
		if b.manifest[i].Path == rel {
			e := b.manifest[i]
			return &e
		}
	}
	return nil
}

func (b *commandBatch) upsert(e CommandEntry) {
	var out []CommandEntry
	for _, x := range b.manifest {
		if x.Path != e.Path {
			out = append(out, x)
		}
	}
	b.manifest = append(out, e)
}

func (b *commandBatch) removePath(rel string) {
	var out []CommandEntry
	for _, x := range b.manifest {
		if x.Path != rel {
			out = append(out, x)
		}
	}
	b.manifest = out
}

func (b *commandBatch) observeDestination(rel string) (FileState, error) {
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return FileState{}, installerError("unsafe_path", jsonx.NewObject("path", rel))
		}
	}
	for _, parent := range parentsOf(rel) {
		st, err := observeNode(filepath.Join(b.root.Path, filepath.FromSlash(parent)))
		if err != nil {
			return FileState{}, err
		}
		if st.Kind != "directory" && st.Kind != "absent" {
			return FileState{}, installerError("unsafe_path", jsonx.NewObject("path", rel, "detail", "Non-directory parent"))
		}
	}
	return observeNode(filepath.Join(b.root.Path, filepath.FromSlash(rel)))
}

func (b *commandBatch) disposition(rel, state, reason string) {
	b.dispositions = append(b.dispositions, NewDisposition("command_skills", b.root.ID, rel, true, state, reason))
}

func (b *commandBatch) effect(rel string, after FileState, owners []string, proof Proof) error {
	before, err := b.observeDestination(rel)
	if err != nil {
		return err
	}
	var action string
	switch {
	case before.Kind == "absent":
		action = "create"
	case after.Kind == "absent":
		action = "delete"
	case before.Kind != after.Kind:
		action = "replace"
	case before.SHA256 != after.SHA256:
		action = "update"
	case before.Mode != after.Mode:
		action = "chmod"
	default:
		return nil
	}
	e := NewEffect("command_skills", "surface_repair", b.root, rel, action, before, after, "Reconcile command delivery and manifest ownership", []Proof{proof}, owners, nil)
	if prev, ok := b.effects[rel]; ok {
		e.LogicalOwners = append(append([]string(nil), prev.LogicalOwners...), e.LogicalOwners...)
		e.Ownership = append(append([]Proof(nil), prev.Ownership...), e.Ownership...)
		e.normalize()
	} else {
		b.order = append(b.order, rel)
	}
	b.effects[rel] = e
	return nil
}

func (b *commandBatch) parents(rel string, owners []string) error {
	for _, parent := range parentsOf(rel) {
		st, err := b.observeDestination(parent)
		if err != nil {
			return err
		}
		if st.Kind == "absent" {
			if b.provParents[parent] {
				continue
			}
			if err := b.effect(parent, DirOf(0o755), owners, Proof{"managed_path", "parent:" + rel}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *commandBatch) atomicArtifact(rel string) error {
	var tmp string
	if rel == commandManifestRel {
		tmp = strings.TrimSuffix(rel, ".json") + ".tmp"
	} else {
		tmp = rel + ".tmp"
	}
	st, err := b.observeDestination(tmp)
	if err != nil {
		return err
	}
	if st.Kind != "absent" {
		return installerError("unexpected_collision", jsonx.NewObject("path", tmp))
	}
	b.artifacts = append(b.artifacts, Artifact{path.Dir(tmp), path.Base(tmp), "atomic_write"})
	return nil
}

func (b *commandBatch) preserve(rel string, before FileState, existing *CommandEntry, digest string) bool {
	if before.Kind == "directory" || (before.Kind == "symlink" && existing == nil) {
		b.disposition(rel, "preserve", "Unknown node is not command ownership")
		return true
	}
	if before.Kind != "file" {
		return false
	}
	if existing != nil && before.SHA256 != existing.ContentHash && before.SHA256 != digest {
		b.regenerated[rel] = true
		return false
	}
	if existing == nil && before.SHA256 != digest {
		b.disposition(rel, "preserve", "Unknown content does not equal canonical rendered bytes")
		return true
	}
	return false
}

func unionSorted(a, b []string) []string {
	return uniqueSorted(append(append([]string(nil), a...), b...))
}

func (b *commandBatch) installCommand(command string, content []byte) error {
	rel := agents.CommandSkillPath(command)
	existing := b.find(rel)
	parent := path.Dir(rel)
	parentState, err := b.observeDestination(parent)
	if err != nil {
		var ie *agents.InstallerError
		if errors.As(err, &ie) && ie.Code == "unsafe_path" {
			b.disposition(rel, "preserve", "Unsafe command parent retained")
			return nil
		}
		return err
	}
	packageLink := parentState.Kind == "symlink"
	if packageLink && existing == nil {
		b.disposition(rel, "preserve", "Unknown package link is not command ownership")
		return nil
	}
	before := Absent()
	if !packageLink {
		if before, err = b.observeDestination(rel); err != nil {
			return err
		}
	}
	digest := Digest(content)
	if b.preserve(rel, before, existing, digest) {
		return nil
	}
	var prevAgents []string
	installedAt := b.now
	if existing != nil {
		prevAgents, installedAt = existing.Agents, existing.InstalledAt
	}
	owners := unionSorted(prevAgents, b.agents)
	entry := CommandEntry{Path: rel, ContentHash: digest, Agents: owners, InstalledAt: installedAt, SpecKittyVersion: app.Version}
	same := before.Kind == "file" && before.SHA256 == digest
	if same && existing != nil {
		entry = CommandEntry{Path: existing.Path, ContentHash: digest, Agents: unionSorted(existing.Agents, b.agents), InstalledAt: existing.InstalledAt, SpecKittyVersion: existing.SpecKittyVersion}
	}
	kind := "added"
	switch {
	case same && existing != nil && existing.equal(entry):
		kind = "already_installed"
	case same:
		kind = "reused_shared"
	}
	if b.regenerated[rel] {
		kind = "regenerated"
	}
	var proof Proof
	switch {
	case existing != nil:
		proof = Proof{"manifest", commandManifestRel + "#" + rel}
	case same:
		proof = Proof{"canonical_content", fmt.Sprintf("CANONICAL_COMMANDS:%s:%s", command, digest)}
	default:
		proof = Proof{"managed_path", fmt.Sprintf("CANONICAL_COMMANDS:%s:%s", command, digest)}
	}
	pc := proof
	b.commands = append(b.commands, PreparedCommand{Path: rel, Content: content, Entry: &entry, ReportKind: kind, Proof: &pc})
	b.upsert(entry)
	switch {
	case packageLink:
		if err := b.effect(parent, DirOf(0o755), owners, proof); err != nil {
			return err
		}
		if _, ok := b.effects[rel]; !ok {
			b.order = append(b.order, rel)
		}
		b.effects[rel] = NewEffect("command_skills", "surface_repair", b.root, rel, "create", before, FileOf(digest, 0o644), "Copy into the replaced owned package link", []Proof{proof}, owners, nil)
		b.artifacts = append(b.artifacts, Artifact{parent, "SKILL.md.tmp", "atomic_write"})
	case !same:
		if err := b.parents(rel, owners); err != nil {
			return err
		}
		mode := 0o644
		if before.Kind == "file" {
			mode = before.Mode
		}
		if err := b.effect(rel, FileOf(digest, mode), owners, proof); err != nil {
			return err
		}
		if err := b.atomicArtifact(rel); err != nil {
			return err
		}
	default:
		b.disposition(rel, "unchanged", "Canonical bytes retained; manifest owners assessed separately")
	}
	return nil
}

func (b *commandBatch) removeEntry(e CommandEntry, remaining []string, retired bool) error {
	before, err := b.observeDestination(e.Path)
	if err != nil {
		return err
	}
	if before.Kind == "directory" || (before.Kind == "file" && before.SHA256 != e.ContentHash && !retired) {
		b.disposition(e.Path, "consent_required", "Edited owned prune/remove candidate retained")
		return nil
	}
	if len(remaining) > 0 {
		updated := CommandEntry{Path: e.Path, ContentHash: e.ContentHash, Agents: remaining, InstalledAt: e.InstalledAt, SpecKittyVersion: e.SpecKittyVersion}
		b.upsert(updated)
		b.commands = append(b.commands, PreparedCommand{Path: e.Path, Entry: &updated, ReportKind: "kept"})
		return nil
	}
	proof := Proof{"manifest", commandManifestRel + "#" + e.Path}
	if before.Kind != "absent" {
		if err := b.effect(e.Path, Absent(), e.Agents, proof); err != nil {
			return err
		}
	}
	parent := path.Dir(e.Path)
	pst, err := observeNode(filepath.Join(b.root.Path, filepath.FromSlash(parent)))
	if err != nil {
		return err
	}
	if pst.Kind == "directory" {
		ents, _ := os.ReadDir(filepath.Join(b.root.Path, filepath.FromSlash(parent)))
		if len(ents) == 0 || (len(ents) == 1 && ents[0].Name() == "SKILL.md") {
			if err := b.effect(parent, Absent(), e.Agents, proof); err != nil {
				return err
			}
		}
	}
	b.removePath(e.Path)
	b.commands = append(b.commands, PreparedCommand{Path: e.Path, ReportKind: "deleted"})
	return nil
}

func neutralized(entries []CommandEntry) string {
	n := make([]CommandEntry, len(entries))
	for i, e := range entries {
		e.InstalledAt = ""
		n[i] = e
	}
	return string(SerializeCommandManifest(n))
}

func (b *commandBatch) finish() (*Assessment, error) {
	encoded := SerializeCommandManifest(b.manifest)
	var all []string
	all = append(all, b.agents...)
	for _, e := range b.original {
		all = append(all, e.Agents...)
	}
	owners := uniqueSorted(all)
	changed := len(b.manifest) != len(b.original)
	if !changed {
		for i := range b.manifest {
			if !b.manifest[i].equal(b.original[i]) {
				changed = true
				break
			}
		}
	}
	if changed && neutralized(b.manifest) != neutralized(b.original) {
		if err := b.parents(commandManifestRel, owners); err != nil {
			return nil, err
		}
		before, err := b.observeDestination(commandManifestRel)
		if err != nil {
			return nil, err
		}
		mode := 0o644
		if before.Kind == "file" {
			mode = before.Mode
		}
		if err := b.effect(commandManifestRel, FileOf(Digest(encoded), mode), owners, Proof{"managed_path", commandManifestRel}); err != nil {
			return nil, err
		}
		e := b.effects[commandManifestRel]
		for _, c := range b.commands {
			if c.Proof != nil {
				e.Ownership = append(e.Ownership, *c.Proof)
			}
		}
		e.normalize()
		b.effects[commandManifestRel] = e
		if err := b.atomicArtifact(commandManifestRel); err != nil {
			return nil, err
		}
	}
	var effects []Effect
	for _, rel := range b.order {
		effects = append(effects, b.effects[rel])
	}
	return &Assessment{OwnerKey: "command_skills", Root: b.root, Effects: effects, Dispositions: b.dispositions, Complete: true, Artifacts: b.artifacts,
		Prepared: &PreparedCommands{Commands: b.commands, ManifestBytes: encoded}}, nil
}

// CommandOptions narrows PrepareCommands.
type CommandOptions struct {
	Prune        bool
	Overwrite    []string // ApplyConsent.overwrite_paths (consulted only off the DERIVED surface)
	RemoveAgents []string
	Provisioning *Provisioning
	Now          string
}

// PrepareCommands is command_installer.prepare_commands.
func PrepareCommands(root Root, agentKeys []string, opts CommandOptions) *Assessment {
	a, err := prepareCommands(root, agentKeys, opts)
	if err != nil {
		code := "command_input_unreadable"
		var ie *agents.InstallerError
		if errors.As(err, &ie) {
			code = ie.Code
		}
		return Incomplete("command_skills", root, code, err.Error())
	}
	return a
}

func prepareCommands(root Root, agentKeys []string, opts CommandOptions) (*Assessment, error) {
	for _, a := range append(append([]string(nil), agentKeys...), opts.RemoveAgents...) {
		if !agents.IsCommandSkillAgent(a) {
			all := make([]any, 0)
			for _, x := range append(append([]string(nil), agentKeys...), opts.RemoveAgents...) {
				all = append(all, x)
			}
			return nil, installerError("unsupported_agent", jsonx.NewObject("agents", all))
		}
	}
	b := &commandBatch{root: root, agents: agentKeys, effects: map[string]Effect{}, regenerated: map[string]bool{}, provParents: map[string]bool{}, now: opts.Now}
	if opts.Provisioning != nil {
		for _, d := range opts.Provisioning.AbsentParents {
			b.provParents[d] = true
		}
	}
	st, err := b.observeDestination(commandManifestRel)
	if err != nil {
		return nil, err
	}
	if st.Kind != "file" && st.Kind != "absent" {
		return nil, installerError("manifest_parse_failed", jsonx.NewObject("detail", "Manifest must be a regular file"))
	}
	entries, err := LoadCommandManifest(root.Path)
	if err != nil {
		return nil, err
	}
	b.manifest = append([]CommandEntry(nil), entries...)
	b.original = append([]CommandEntry(nil), entries...)
	if len(agentKeys) > 0 {
		for _, c := range agents.CommandSkillCommands {
			content, err := agents.RenderCommandSkill(root.Path, c)
			if err != nil {
				return nil, err
			}
			if err := b.installCommand(c, content); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range b.original {
		retired := opts.Prune && !isCanonicalRelPath(e.Path)
		if len(opts.RemoveAgents) > 0 || retired {
			sel := opts.RemoveAgents
			if len(sel) == 0 {
				sel = agentKeys
			}
			if len(sel) == 0 {
				sel = e.Agents
			}
			selSet := map[string]bool{}
			for _, s := range sel {
				selSet[s] = true
			}
			hit := false
			var remaining []string
			for _, a := range e.Agents {
				if selSet[a] {
					hit = true
				} else {
					remaining = append(remaining, a)
				}
			}
			if hit {
				if err := b.removeEntry(e, remaining, retired); err != nil {
					return nil, err
				}
			}
		}
	}
	return b.finish()
}

func isCanonicalRelPath(rel string) bool {
	const prefix, suffix = ".agents/skills/spec-kitty.", "/SKILL.md"
	if !strings.HasPrefix(rel, prefix) || !strings.HasSuffix(rel, suffix) {
		return false
	}
	c := rel[len(prefix) : len(rel)-len(suffix)]
	for _, x := range agents.CommandSkillCommands {
		if x == c {
			return true
		}
	}
	return false
}

// ApplyCommands is command_installer.apply_commands for a fresh assessment.
func ApplyCommands(a *Assessment) ApplyResult {
	payload, _ := a.Prepared.(*PreparedCommands)
	if !a.Complete || payload == nil {
		return ApplyResult{Owner: "command_skills", Skipped: a.EffectIDs(), Outcome: "precondition_changed",
			Diagnostics: []Diagnostic{Diag("incomplete_assessment", "command_skills", "error", "Command preparation is incomplete")}}
	}
	effects := append([]Effect(nil), a.Effects...)
	rank := func(e Effect) int {
		switch {
		case e.Path == commandManifestRel:
			return 3
		case e.After.Kind == "absent" && e.Before.Kind == "directory":
			return 2
		case e.After.Kind == "directory":
			return 0
		}
		return 1
	}
	sort.SliceStable(effects, func(i, j int) bool {
		ri, rj := rank(effects[i]), rank(effects[j])
		if ri != rj {
			return ri < rj
		}
		di, dj := len(strings.Split(effects[i].Path, "/")), len(strings.Split(effects[j].Path, "/"))
		if di != dj {
			return di < dj
		}
		return effects[i].Path < effects[j].Path
	})
	var succeeded []string
	for idx, e := range effects {
		if err := applyCommandEffect(e, payload); err != nil {
			var rest []string
			for _, x := range effects[idx+1:] {
				rest = append(rest, x.ID())
			}
			out := "failed"
			if len(succeeded) > 0 {
				out = "partial"
			}
			return ApplyResult{Owner: "command_skills", Succeeded: succeeded, Failed: []string{e.ID()}, Skipped: rest, Outcome: out,
				Diagnostics: []Diagnostic{Diag("command_apply_failed", "command_skills", "error", err.Error())}}
		}
		succeeded = append(succeeded, e.ID())
	}
	return ApplyResult{Owner: "command_skills", Succeeded: succeeded, Outcome: "applied"}
}

func applyCommandEffect(e Effect, payload *PreparedCommands) error {
	dest := e.Destination()
	mode := os.FileMode(0o644)
	if e.After.Mode >= 0 {
		mode = os.FileMode(e.After.Mode)
	}
	switch {
	case e.After.Kind == "directory":
		if e.Before.Kind == "symlink" {
			if err := os.Remove(dest); err != nil {
				return err
			}
		}
		if err := os.Mkdir(dest, 0o755); err != nil {
			return err
		}
		return os.Chmod(dest, 0o755)
	case e.After.Kind == "absent":
		return os.Remove(dest)
	case e.Path == commandManifestRel:
		return writeFileAtomic(dest, payload.ManifestBytes, mode)
	}
	for _, c := range payload.Commands {
		if c.Path == e.Path && c.Content != nil {
			return writeFileAtomic(dest, c.Content, mode)
		}
	}
	return fmt.Errorf("no prepared content for %s", e.Path)
}

// writeFileAtomic writes data via a sibling temporary file and rename.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
