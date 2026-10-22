package tracker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func TestBuildScopeMatchesUpstream(t *testing.T) {
	// Observed from the oracle: beads/ws with no server/user/team.
	if got := BuildScope("beads", "ws", "", "", ""); got != "e46273cd70c682bd" {
		t.Fatalf("scope %s", got)
	}
}

func TestParseKVPairs(t *testing.T) {
	o, err := ParseKVPairs([]string{" a = b ", "c=d=e"})
	if err != nil || jsonx.Dumps(o, jsonx.Options{Indent: -1}) != `{"a": "b", "c": "d=e"}` {
		t.Fatalf("%v %v", o, err)
	}
	for _, bad := range []string{"nope", "=v", "k=", " = "} {
		if _, err := ParseKVPairs([]string{bad}); err == nil || !strings.Contains(err.Error(), "Expected key=value") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func newProject(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".kittify"), 0o755)
	if config != "" {
		os.WriteFile(ConfigPath(root), []byte(config), 0o644)
	}
	return root
}

func TestConfigRoundTripAndClear(t *testing.T) {
	root := newProject(t, "vcs:\n  type: git\ntracker:\n  extra: 1\n  provider: ' Beads '\n  workspace: ws\n  egress: 'refused'\n  ownership:\n    mode: split_ownership\n    field_owners:\n      title: local\n      empty: ''\n")
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "Beads" || c.IsConfigured() || c.EgressFault() || c.EgressStyle != '\'' || c.FieldOwners.Len() != 1 {
		t.Fatalf("%+v", c)
	}
	c.Provider = "beads"
	if !c.IsConfigured() {
		t.Fatal("expected configured")
	}
	if err := SaveConfig(root, c); err != nil {
		t.Fatal(err)
	}
	want := "vcs:\n  type: git\ntracker:\n  extra: 1\n  provider: beads\n  binding_ref:\n  project_slug:\n  display_label:\n  provider_context:\n  workspace: ws\n  ownership:\n    mode: split_ownership\n    field_owners:\n      title: local\n  egress: 'refused'\n"
	if b, _ := os.ReadFile(ConfigPath(root)); string(b) != want {
		t.Fatalf("saved:\n%s", b)
	}
	if err := ClearConfig(root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ConfigPath(root)); string(b) != "vcs:\n  type: git\ntracker:\n  egress: 'refused'\n" {
		t.Fatalf("cleared:\n%s", b)
	}
	v := Verdict(root)
	if !v.Refused || v.State != EgressRefused || len(v.Remedies) != 0 {
		t.Fatalf("%+v", v)
	}
}

func TestEgressVerdictStates(t *testing.T) {
	cases := map[string]string{
		"":                       "no tracker.egress key is recorded",
		"  egress: permitted\n":  "is permitted by tracker.egress: 'permitted'",
		"  egress: Refused\n":    "tracker.egress is set to 'Refused', which is not a legal value",
		"  egress:\n":            "tracker.egress is set to None",
		"  egress: [a]\n":        "tracker.egress is set to ['a']",
		"  egress: refused\n":    "tracker.egress is recorded as 'refused'",
		"  egress: {a: true}\n":  "tracker.egress is set to {'a': True}",
		"  egress: \"refuse\"\n": "tracker.egress is set to 'refuse'",
	}
	for block, want := range cases {
		root := newProject(t, "tracker:\n  provider: beads\n"+block)
		v := Verdict(root)
		if !strings.Contains(v.Render(), want) {
			t.Errorf("%q: %s", block, v.Render())
		}
		if (block == "") != (len(v.Remedies) == 1) {
			t.Errorf("%q: remedies %v", block, v.Remedies)
		}
	}
	if v := Verdict(newProject(t, "tracker: [unclosed\n")); !strings.Contains(v.Message, "could not be read") {
		t.Errorf("unreadable: %s", v.Message)
	}
}

func TestCredentialStore(t *testing.T) {
	s := &CredentialStore{Path: filepath.Join(t.TempDir(), "creds")}
	if err := s.SetProvider("beads", jsonx.NewObject("command", "bd", "blank", " ", "actor", "me")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProvider("fp", jsonx.NewObject("cwd", "/x")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.Path)
	if string(b) != "[tracker.providers.beads]\ncommand = \"bd\"\nactor = \"me\"\n\n[tracker.providers.fp]\ncwd = \"/x\"\n" {
		t.Fatalf("got:\n%s", b)
	}
	if st, _ := os.Stat(s.Path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if err := s.ClearProvider("beads"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearProvider("fp"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.Path); string(b) != "[tracker.providers]\n" {
		t.Fatalf("got:\n%s", b)
	}
	got, err := s.GetProvider("fp")
	if err != nil || got.Len() != 0 {
		t.Fatal(got, err)
	}
}

func TestStoreMappings(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "x", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ref := func(id string) ExternalRef { r, _ := NewRef("beads", "ws", id, nil, nil); return r }
	for _, c := range []struct{ wp, id string }{{"WP02", "1"}, {"WP01", "2"}, {"WP02", "3"}} {
		if err := st.UpsertMapping(c.wp, ref(c.id)); err != nil {
			t.Fatal(err)
		}
	}
	err = st.UpsertMapping("WP03", ref("2"))
	var crash *Crash
	if !errors.As(err, &crash) || crash.Type != "IntegrityError" {
		t.Fatalf("want IntegrityError, got %v", err)
	}
	maps, err := st.ListMappings()
	if err != nil || len(maps) != 2 || maps[0].WPID != "WP01" || maps[1].ExternalID != "3" {
		t.Fatalf("%+v %v", maps, err)
	}
	if _, err := NewRef("beads", "ws", "  ", nil, nil); err == nil {
		t.Fatal("expected ValueError")
	}
}

// memConnector is an in-memory Connector for engine tests.
type memConnector struct {
	issues  []*Issue
	created []string
	patches [][]string
}

func (m *memConnector) Name() string               { return "beads" }
func (m *memConnector) Capabilities() Capabilities { return Capabilities{} }
func (m *memConnector) ListIssues(*PyTime, *string, int) (*Page, error) {
	return &Page{Items: m.issues}, nil
}
func (m *memConnector) GetIssue(ref ExternalRef) (*Issue, error) {
	for _, i := range m.issues {
		if i.Ref.Identity() == ref.Identity() {
			return i, nil
		}
	}
	return nil, &TrackerError{Class: "IssueNotFoundError", Msg: "nf"}
}
func (m *memConnector) CreateIssue(i *Issue) (*Issue, error) {
	m.created = append(m.created, i.Title)
	c := i.Clone()
	c.Ref, _ = NewRef("beads", "ws", "new-"+i.Ref.ID, nil, nil)
	return c, nil
}
func (m *memConnector) UpdateIssue(ref ExternalRef, p *Patch) (*Issue, error) {
	m.patches = append(m.patches, append([]string{}, p.Fields...))
	return p.Src, nil
}

func mkIssue(id, title, status, updated string) *Issue {
	r, _ := NewRef("beads", "ws", id, nil, nil)
	u, _ := ParseISO(updated)
	return &Issue{Ref: r, Title: title, Status: status, IssueType: "task", Assignees: []string{}, Labels: []string{}, Links: []Link{}, CustomFields: &jsonx.Object{}, UpdatedAt: u}
}

func TestEnginePullPushSplitOwnership(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	conn := &memConnector{issues: []*Issue{mkIssue("1", "A", "todo", "2026-01-01T00:00:00+00:00"), mkIssue("2", "B", "todo", "2026-01-01T00:00:00+00:00")}}
	cfg := NewProjectConfig()
	cfg.OwnershipMode = "split_ownership"
	cfg.FieldOwners = jsonx.NewObject("title", "local", "status", "bogus")
	e := &Engine{Connector: conn, Store: st, Policy: PolicyFor(cfg)}
	res, err := e.Pull(10)
	if err != nil || res.Stats.PulledCreated != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if e.Checkpoint.UpdatedSince == nil || e.Checkpoint.UpdatedSince.ISO() != "2026-01-01T00:00:00+00:00" {
		t.Fatalf("checkpoint %+v", e.Checkpoint)
	}
	// Remote edits: title is locally owned (kept), status is shared (newer wins).
	conn.issues = []*Issue{mkIssue("1", "A2", "in_progress", "2026-01-02T00:00:00+00:00")}
	res, err = e.Pull(10)
	if err != nil || res.Stats.PulledUpdated != 1 || len(res.Conflicts) != 1 || res.Conflicts[0].Field != "status" {
		t.Fatalf("%+v %v", res, err)
	}
	local, _ := st.GetIssue(conn.issues[0].Ref)
	if local.Title != "A" || local.Status != "in_progress" {
		t.Fatalf("merged %+v", local)
	}
	// Push: issue 1 differs in title (local owner) -> update; issue 2 is
	// missing remotely -> create.
	res, err = e.Push(100)
	if err != nil || res.Stats.PushedUpdated != 1 || res.Stats.PushedCreated != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(conn.patches) != 1 || strings.Join(conn.patches[0], ",") != "title" || strings.Join(conn.created, ",") != "B" {
		t.Fatalf("patches %v created %v", conn.patches, conn.created)
	}
	js := jsonx.Dumps(res.JSON("beads"), jsonx.SortedPretty)
	if !strings.Contains(js, `"pushed_created": 1`) {
		t.Fatal(js)
	}
}

func TestGatewayAllowList(t *testing.T) {
	cases := map[string]string{
		"bd --json list":                    "",
		"bd --json update x --title t":      "",
		"bd --json update x --status open":  "forbidden lifecycle flag '--status' on allow-listed subcommand 'update'",
		"bd --json update x --status=open":  "forbidden lifecycle flag '--status'",
		"bd --json close x":                 "subcommand 'close' is not on the tracker gateway allow-list",
		"bd --db f --json dep add a b":      "",
		"bd --json":                         "subcommand '<none>' is not on the tracker gateway allow-list",
		"bd --json create -a --type task":   "forbidden lifecycle flag '-a' on allow-listed subcommand 'create'",
		"bd --json comments add x hi there": "",
	}
	for cmd, want := range cases {
		got := forbiddenReason(strings.Fields(cmd))
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%s: %q", cmd, got)
		}
	}
}

func TestParseISO(t *testing.T) {
	for in, want := range map[string]string{
		"2025-01-02T03:04:05.123456789-07:00": "2025-01-02T03:04:05.123456-07:00",
		"2025-01-02":                          "2025-01-02T00:00:00",
		"2025-01-02 03:04Z":                   "2025-01-02T03:04:00+00:00",
		"20250102T030405":                     "2025-01-02T03:04:05",
		"2025-01-02T03:04:05+0530":            "2025-01-02T03:04:05+05:30",
		"2025-01-02T03:04:05.1":               "2025-01-02T03:04:05.100000",
	} {
		got, ok := ParseISO(in)
		if !ok || got.ISO() != want {
			t.Errorf("%s: %v %v", in, got, ok)
		}
	}
	if _, ok := ParseISO("bad"); ok {
		t.Error("bad parsed")
	}
}

func TestFPLineParsing(t *testing.T) {
	f := &FPConnector{Workspace: "ws"}
	issues, err := f.parseLines("FP-1 [todo] [high] Build\n├─ FP-2 [In-Review] Child\nnoise\r\n└─ FP-3 [x] [9] Odd\n")
	if err != nil || len(issues) != 3 {
		t.Fatalf("%v %v", issues, err)
	}
	if *issues[0].Priority != 1 || issues[1].Status != "in_review" || issues[2].Status != "todo" || *issues[2].Priority != 4 {
		t.Fatalf("%+v %+v %+v", issues[0], issues[1], issues[2])
	}
}
