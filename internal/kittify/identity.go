package kittify

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Identity is the config.yaml project: block (identity/project.ProjectIdentity).
type Identity struct {
	UUID     string
	Slug     string
	NodeID   string
	BuildID  string
	RepoSlug string // optional user override
}

// Complete reports whether uuid, slug, node_id and build_id are all set.
func (i Identity) Complete() bool {
	return i.UUID != "" && i.Slug != "" && i.NodeID != "" && i.BuildID != ""
}

// Object renders the project block in upstream key order.
func (i Identity) Object() *jsonx.Object {
	o := jsonx.NewObject("uuid", nullable(i.UUID), "slug", nullable(i.Slug), "node_id", nullable(i.NodeID), "build_id", nullable(i.BuildID))
	if i.RepoSlug != "" {
		o.Set("repo_slug", i.RepoSlug)
	}
	return o
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func textValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case *jsonx.Object, []any:
		return ""
	}
	return strings.TrimSpace(yamlx.FormatScalar(v))
}

// LoadIdentity reads the project block (never fails; empty when unusable).
func LoadIdentity(root string) Identity {
	cfg, err := LoadConfig(root)
	if err != nil {
		return Identity{}
	}
	p := cfg.GetObject("project")
	if p == nil {
		return Identity{}
	}
	get := func(k string) string { v, _ := p.Get(k); return textValue(v) }
	id := Identity{UUID: get("uuid"), Slug: get("slug"), NodeID: get("node_id"), BuildID: get("build_id"), RepoSlug: get("repo_slug")}
	if id.UUID != "" {
		if u, ok := parseUUID(id.UUID); ok {
			id.UUID = u
		} else {
			return Identity{}
		}
	}
	return id
}

// NewUUID4 returns a random RFC 4122 version-4 UUID string.
func NewUUID4() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return formatUUID(b[:])
}

func formatUUID(b []byte) string {
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func parseUUID(s string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(t, "{"), "}"), "urn:uuid:")
	t = strings.ReplaceAll(t, "-", "")
	if len(t) != 32 {
		return "", false
	}
	b, err := hex.DecodeString(t)
	if err != nil {
		return "", false
	}
	return formatUUID(b), true
}

// NamespaceURL is uuid.NAMESPACE_URL.
var NamespaceURL = []byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// UUID5 is uuid.uuid5(namespace, name).
func UUID5(namespace []byte, name string) string {
	h := sha1.New()
	h.Write(namespace)
	h.Write([]byte(name))
	s := h.Sum(nil)[:16]
	s[6] = (s[6] & 0x0f) | 0x50
	s[8] = (s[8] & 0x3f) | 0x80
	return formatUUID(s)
}

func uuidBytes(u string) []byte {
	b, _ := hex.DecodeString(strings.ReplaceAll(u, "-", ""))
	return b
}

// DeriveBuildID is uuid5(uuid5(NAMESPACE_URL, "spec-kitty:identity:build_id"), f"{uuid}:{node_id}").
func DeriveBuildID(projectUUID, nodeID string) string {
	ns := uuidBytes(UUID5(NamespaceURL, "spec-kitty:identity:build_id"))
	return UUID5(ns, projectUUID+":"+nodeID)
}

// GenerateNodeID is sha256(f"{hostname}:{username}")[:12].
func GenerateNodeID() string {
	host, _ := os.Hostname()
	raw := host + ":" + pyGetUser()
	s := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(s[:])[:12]
}

// pyGetUser mirrors getpass.getuser(): LOGNAME, USER, LNAME, USERNAME, then
// the password database.
func pyGetUser() string {
	for _, k := range []string{"LOGNAME", "USER", "LNAME", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// NormalizeSlug lowercases and maps '_' and ' ' to '-'.
func NormalizeSlug(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(name), "_", "-"), " ", "-")
}

// DeriveProjectSlug uses the origin remote's repo name, else the dir name.
func DeriveProjectSlug(root string) string {
	if out, err := gitx.Run(root, "remote", "get-url", "origin"); err == nil {
		url := strings.TrimSpace(out)
		url = strings.TrimSuffix(url, ".git")
		if strings.Contains(url, ":") && strings.Contains(url, "@") {
			parts := strings.Split(url, ":")
			url = parts[len(parts)-1]
		}
		parts := strings.Split(url, "/")
		return NormalizeSlug(parts[len(parts)-1])
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return NormalizeSlug(filepath.Base(abs))
}

// WithDefaults fills missing fields (ProjectIdentity.with_defaults).
func (i Identity) WithDefaults(root string) Identity {
	out := i
	if out.UUID == "" {
		out.UUID = NewUUID4()
	}
	if out.NodeID == "" {
		out.NodeID = GenerateNodeID()
	}
	if out.Slug == "" {
		out.Slug = DeriveProjectSlug(root)
	}
	if out.BuildID == "" {
		out.BuildID = DeriveBuildID(out.UUID, out.NodeID)
	}
	return out
}

// EnsureIdentity loads or mints the project identity and persists it
// atomically (mode 0600 like upstream's mkstemp+replace). Returns the
// identity and whether it could be persisted.
func EnsureIdentity(root string) (Identity, error) {
	id := LoadIdentity(root)
	if id.Complete() {
		return id, nil
	}
	id = id.WithDefaults(root)
	cfg := &jsonx.Object{}
	if ConfigExists(root) {
		var err error
		cfg, err = LoadConfig(root)
		if err != nil {
			return id, fmt.Errorf("%v; refusing to overwrite it", err)
		}
	}
	cfg.Set("project", id.Object())
	return id, writeAtomic(ConfigPath(root), []byte(RenderConfig(cfg)), 0o600)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
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
