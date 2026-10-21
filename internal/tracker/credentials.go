package tracker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// RuntimeRoot is get_runtime_root().base (~/.spec-kitty or SPEC_KITTY_HOME).
func RuntimeRoot() string { return project.GlobalSpecKitty() }

// DefaultCredentialsPath is <runtime root>/credentials (POSIX layout).
func DefaultCredentialsPath() string { return filepath.Join(RuntimeRoot(), "credentials") }

// CredentialStore is TrackerCredentialStore.
type CredentialStore struct{ Path string }

// NewCredentialStore returns the store at the default location.
func NewCredentialStore() *CredentialStore { return &CredentialStore{Path: DefaultCredentialsPath()} }

func (s *CredentialStore) lockPath() string { return s.Path + ".lock" }

// Load parses the credential file (missing -> empty).
func (s *CredentialStore) Load() (*jsonx.Object, error) {
	if _, err := os.Stat(s.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &jsonx.Object{}, nil
		}
	}
	unlock, err := lockFile(s.lockPath())
	if err != nil {
		return nil, errorf("Failed to load credentials: %v", err)
	}
	raw, err := os.ReadFile(s.Path)
	unlock()
	if err != nil {
		return nil, errorf("Failed to load credentials: %v", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return &jsonx.Object{}, nil
	}
	o, err := ParseTOML(string(raw))
	if err != nil {
		return nil, errorf("Failed to load credentials: %v", err)
	}
	return o, nil
}

// Save writes the payload as TOML, owner-only.
func (s *CredentialStore) Save(payload *jsonx.Object) error {
	fail := func(err error) error { return errorf("Failed to save credentials: %v", err) }
	if s.Path == DefaultCredentialsPath() {
		root := RuntimeRoot()
		if err := os.MkdirAll(root, 0o700); err != nil {
			return fail(err)
		}
		if err := os.Chmod(root, 0o700); err != nil {
			return fail(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o777); err != nil {
		return fail(err)
	}
	content, err := DumpTOML(payload)
	if err != nil {
		return fail(err)
	}
	unlock, err := lockFile(s.lockPath())
	if err != nil {
		return fail(err)
	}
	defer unlock()
	tmp := fmt.Sprintf("%s.tmp.%d.%s", s.Path, os.Getpid(), randHex(4))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fail(err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(tmp)
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fail(err)
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		os.Remove(tmp)
		return fail(err)
	}
	return nil
}

func providersOf(payload *jsonx.Object) *jsonx.Object {
	t, _ := payload.Get("tracker")
	to, _ := t.(*jsonx.Object)
	if to == nil {
		return nil
	}
	p, _ := to.Get("providers")
	po, _ := p.(*jsonx.Object)
	return po
}

// GetProvider returns a copy of the provider's credentials (empty if none).
func (s *CredentialStore) GetProvider(provider string) (*jsonx.Object, error) {
	payload, err := s.Load()
	if err != nil {
		return nil, err
	}
	if po := providersOf(payload); po != nil {
		if v, ok := po.Get(provider); ok {
			if o, ok := v.(*jsonx.Object); ok {
				return o.Clone(), nil
			}
		}
	}
	return &jsonx.Object{}, nil
}

// SetProvider stores credentials for provider (blank keys/values dropped).
func (s *CredentialStore) SetProvider(provider string, values *jsonx.Object) error {
	payload, err := s.Load()
	if err != nil {
		return err
	}
	tv, ok := payload.Get("tracker")
	t, isObj := tv.(*jsonx.Object)
	if !ok || !isObj {
		t = &jsonx.Object{}
		payload.Set("tracker", t)
	}
	pv, ok := t.Get("providers")
	p, isObj := pv.(*jsonx.Object)
	if !ok || !isObj {
		p = &jsonx.Object{}
		t.Set("providers", p)
	}
	clean := &jsonx.Object{}
	for _, kv := range values.Pairs() {
		if strings.TrimSpace(kv.Key) == "" || kv.Value == nil || strings.TrimSpace(pyjson.Str(kv.Value)) == "" {
			continue
		}
		clean.Set(kv.Key, kv.Value)
	}
	p.Set(provider, clean)
	return s.Save(payload)
}

// ClearProvider removes the provider's credentials when present.
func (s *CredentialStore) ClearProvider(provider string) error {
	payload, err := s.Load()
	if err != nil {
		return err
	}
	po := providersOf(payload)
	if po == nil || !po.Has(provider) {
		return nil
	}
	po.Delete(provider)
	return s.Save(payload)
}
