package tracker

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const leaveRemedy = "Run `spec-kitty tracker unbind`, or `spec-kitty tracker bind --provider beads|fp`."

// ParseKVPairs mirrors service.parse_kv_pairs (the error always names
// --credential, as upstream does for --field-owner too).
func ParseKVPairs(entries []string) (*jsonx.Object, error) {
	out := &jsonx.Object{}
	for _, e := range entries {
		i := strings.Index(e, "=")
		if i < 0 {
			return nil, errorf("Invalid --credential value '%s'. Expected key=value.", e)
		}
		k, v := strings.TrimSpace(e[:i]), strings.TrimSpace(e[i+1:])
		if k == "" || v == "" {
			return nil, errorf("Invalid --credential value '%s'. Expected key=value.", e)
		}
		out.Set(k, v)
	}
	return out, nil
}

// Service is the TrackerService facade over the local (beads/fp) backend.
type Service struct {
	Root  string
	Creds *CredentialStore
}

// NewService returns a facade for the project at root.
func NewService(root string) *Service { return &Service{Root: root, Creds: NewCredentialStore()} }

func (s *Service) resolveBackend() (ProjectConfig, error) {
	c, err := LoadConfig(s.Root)
	if err != nil {
		return c, err
	}
	switch {
	case c.Provider == "":
		return c, errorf("No tracker bound. Run `spec-kitty tracker bind` first.")
	case IsLocal(c.Provider):
		return c, nil
	case IsRemoved(c.Provider):
		return c, errorf("Provider '%s' is no longer supported. %s", c.Provider, leaveRemedy)
	}
	return c, errorf("Unknown provider: %s. %s", c.Provider, leaveRemedy)
}

// Bind mirrors TrackerService.bind -> LocalTrackerService.bind.
func (s *Service) Bind(provider, workspace, mode string, fieldOwners, creds *jsonx.Object) (ProjectConfig, error) {
	if !IsLocal(provider) {
		if IsRemoved(provider) {
			return ProjectConfig{}, errorf("Provider '%s' is no longer supported.", provider)
		}
		return ProjectConfig{}, errorf("Unknown provider: %s", provider)
	}
	if _, err := LoadConfig(s.Root); err != nil {
		return ProjectConfig{}, err
	}
	committed, err := LoadConfig(s.Root)
	if err != nil {
		return ProjectConfig{}, err
	}
	c := NewProjectConfig()
	c.Provider = NormalizeProvider(provider)
	c.Workspace = workspace
	c.OwnershipMode = mode
	c.FieldOwners = fieldOwners.Clone()
	c.Egress = committed.Egress
	c.EgressStyle = committed.EgressStyle
	c.loaded, c.rtBlock = committed.loaded, committed.rtBlock
	c.Extra = committed.Extra.Clone()
	if err := SaveConfig(s.Root, c); err != nil {
		return c, err
	}
	if creds.Len() > 0 {
		if err := s.Creds.SetProvider(c.Provider, creds); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Unbind clears the binding (and the provider's stored credentials).
func (s *Service) Unbind() error {
	c, err := LoadConfig(s.Root)
	if err != nil {
		return err
	}
	if c.Provider != "" && !IsLocal(c.Provider) {
		return ClearConfig(s.Root)
	}
	if _, err := s.resolveBackend(); err != nil {
		return err
	}
	c, err = LoadConfig(s.Root)
	if err != nil {
		return err
	}
	if c.Provider != "" {
		if err := s.Creds.ClearProvider(c.Provider); err != nil {
			return err
		}
	}
	return ClearConfig(s.Root)
}

func credStr(creds *jsonx.Object, keys ...string) string {
	for _, k := range keys {
		if v, ok := creds.Get(k); ok && pyjson.Truthy(v) {
			return pyjson.Str(v)
		}
	}
	return ""
}

// DBPath resolves the cache path for a binding (_resolve_db_path).
func DBPath(c ProjectConfig, creds *jsonx.Object) string {
	return DefaultDBPath(c.Provider, c.Workspace, credStr(creds, "server_url", "base_url"), credStr(creds, "username", "email"), credStr(creds, "team_slug"))
}

// Status mirrors TrackerService.status.
func (s *Service) Status() (*jsonx.Object, error) {
	c, err := LoadConfig(s.Root)
	if err != nil {
		return nil, err
	}
	if c.Provider != "" && !IsLocal(c.Provider) {
		return jsonx.NewObject("configured", false, "provider", c.Provider, "inert", true,
			"message", "bound to removed provider '"+c.Provider+"' (inert) -- run `spec-kitty tracker unbind`, or `tracker bind --provider beads|fp`"), nil
	}
	if _, err := s.resolveBackend(); err != nil {
		return nil, err
	}
	c, err = LoadConfig(s.Root)
	if err != nil {
		return nil, err
	}
	if !c.IsConfigured() {
		return jsonx.NewObject("configured", false, "provider", nil, "workspace", nil, "db_path", nil, "issue_count", 0, "mapping_count", 0), nil
	}
	creds, err := s.Creds.GetProvider(c.Provider)
	if err != nil {
		return nil, err
	}
	path := DBPath(c, creds)
	store, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	n, err := store.CountIssues(c.Provider)
	if err != nil {
		return nil, err
	}
	maps, err := store.ListMappings()
	if err != nil {
		return nil, err
	}
	return jsonx.NewObject("configured", true, "provider", c.Provider, "workspace", c.Workspace,
		"ownership_mode", c.OwnershipMode, "field_owners", c.FieldOwners, "db_path", path,
		"issue_count", n, "mapping_count", len(maps), "credentials_present", creds.Len() > 0), nil
}

type runtimeState struct {
	cfg   ProjectConfig
	creds *jsonx.Object
	store *Store
}

func (s *Service) loadRuntime() (*runtimeState, error) {
	c, err := LoadConfig(s.Root)
	if err != nil {
		return nil, err
	}
	if !c.IsConfigured() {
		return nil, errorf("Tracker is not configured. Run 'spec-kitty tracker bind' first.")
	}
	creds, err := s.Creds.GetProvider(c.Provider)
	if err != nil {
		return nil, err
	}
	store, err := OpenStore(DBPath(c, creds))
	if err != nil {
		return nil, err
	}
	return &runtimeState{cfg: c, creds: creds, store: store}, nil
}

// MapAdd mirrors map_add.
func (s *Service) MapAdd(wpID, externalID string, key, url *string) error {
	if _, err := s.resolveBackend(); err != nil {
		return err
	}
	rt, err := s.loadRuntime()
	if err != nil {
		return err
	}
	ref, err := NewRef(rt.cfg.Provider, rt.cfg.Workspace, externalID, key, url)
	if err != nil {
		return err
	}
	return rt.store.UpsertMapping(wpID, ref)
}

// MapList mirrors map_list.
func (s *Service) MapList() ([]Mapping, error) {
	if _, err := s.resolveBackend(); err != nil {
		return nil, err
	}
	rt, err := s.loadRuntime()
	if err != nil {
		return nil, err
	}
	return rt.store.ListMappings()
}

// SyncPublish mirrors sync_publish (always refused for local providers).
func (s *Service) SyncPublish() (*jsonx.Object, error) {
	if _, err := s.resolveBackend(); err != nil {
		return nil, err
	}
	return nil, errorf("Snapshot publish is not supported for local providers (beads/fp). Use 'spec-kitty tracker sync push' instead.")
}

// Sync runs one of "pull", "push", "run" via the direct connector.
func (s *Service) Sync(op string, limit int) (*jsonx.Object, error) {
	if _, err := s.resolveBackend(); err != nil {
		return nil, err
	}
	if v := Verdict(s.Root); v.Refused {
		return nil, errorf("%s", v.Render())
	}
	rt, err := s.loadRuntime()
	if err != nil {
		return nil, err
	}
	connector, err := s.buildConnector(rt)
	if err != nil {
		return nil, err
	}
	engine := &Engine{Connector: connector, Store: rt.store, Policy: PolicyFor(rt.cfg)}
	key := rt.cfg.Provider + ":" + rt.cfg.Workspace
	var res *SyncResult
	switch op {
	case "pull", "run":
		cp, err := rt.store.GetCheckpoint(key)
		if err != nil {
			return nil, err
		}
		if cp != nil {
			engine.Checkpoint = *cp
		}
		if op == "pull" {
			res, err = engine.Pull(limit)
		} else {
			res, err = engine.Sync(limit)
		}
		if err != nil {
			return nil, err
		}
		if err := rt.store.SetCheckpoint(engine.Checkpoint, key); err != nil {
			return nil, err
		}
	default:
		res, err = engine.Push(limit)
		if err != nil {
			return nil, err
		}
	}
	return res.JSON(connector.Name()), nil
}
