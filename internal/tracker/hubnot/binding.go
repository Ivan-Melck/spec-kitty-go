// Package hubnot translates the pinned public CLI into descriptive tracker facts.
// It does not import provider implementation code or acquire execution authority.
package hubnot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const bindingSchema = "go-kitty.hubnot-binding/1"
const identitySchema = "hn.identity/1"
const issueSchema = "hn.issue/1"

// ProviderIdentity is review evidence identifying one admitted artifact, not a
// caller-controlled admission policy. Every field is compared at Open.
type ProviderIdentity struct {
	SourceCommit   string `json:"source_commit"`
	Toolchain      string `json:"toolchain"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	GOAMD64        string `json:"goamd64"`
	CGOEnabled     bool   `json:"cgo_enabled"`
	BuildRecipe    string `json:"build_recipe"`
	BinarySHA256   string `json:"binary_sha256"`
	IdentitySchema string `json:"identity_schema"`
	IssueSchema    string `json:"issue_schema"`
	EventProtocol  string `json:"event_protocol"`
}

// QualifiedProviderIdentity returns a value copy of the independently admitted
// tuple. Installers and qualification manifests must derive or mirror-check it.
func QualifiedProviderIdentity() ProviderIdentity {
	return ProviderIdentity{
		SourceCommit: "baf350d12accd01d4d7e7221cf859c81b2c9622b", Toolchain: "go1.26.7", GOOS: "linux", GOARCH: "amd64", GOAMD64: "v1",
		BuildRecipe:    "go1.26.7-linux-amd64-cgo0-trimpath-buildvcsfalse-s-w-v1",
		BinarySHA256:   "077058c8be07cfcd28994210ab483f739fb194b6db417f70b455d6c3a3524cf3",
		IdentitySchema: identitySchema, IssueSchema: issueSchema, EventProtocol: "hn/0",
	}
}

// BindingConfig names existing resources explicitly. Decoding and Open both
// validate it; constructing a Go value does not bypass the admitted tuple.
type BindingConfig struct {
	Schema         string           `json:"schema"`
	Provider       string           `json:"provider"`
	ProjectID      ids.ProjectID    `json:"project_id"`
	WorkspacePath  string           `json:"workspace_path"`
	ExecutablePath string           `json:"executable_path"`
	ExpectedActor  string           `json:"expected_actor"`
	ReadOnly       bool             `json:"read_only"`
	Identity       ProviderIdentity `json:"identity"`
}

func DecodeBinding(input io.Reader) (BindingConfig, error) {
	var config BindingConfig
	if input == nil {
		return config, invalid("binding input is missing", nil)
	}
	data, err := io.ReadAll(io.LimitReader(input, (64<<10)+1))
	if err != nil {
		return config, invalid("binding cannot be read", err)
	}
	if len(data) > 64<<10 {
		return config, invalid("binding exceeds 64 KiB", nil)
	}
	if err := decodeStrict(data, &config); err != nil {
		return config, invalid("binding JSON is invalid", err)
	}
	return config, validateConfig(config)
}
func validateConfig(c BindingConfig) error {
	if c.Schema != bindingSchema || c.Provider != "hubnot" {
		return invalid("binding must select go-kitty.hubnot-binding/1 and hubnot", nil)
	}
	if _, err := ids.ParseProjectID(c.ProjectID.String()); err != nil {
		return invalid("binding project is invalid", err)
	}
	for _, path := range []string{c.WorkspacePath, c.ExecutablePath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return invalid("binding paths must be explicit clean absolute paths", nil)
		}
	}
	if !validHex(c.ExpectedActor, 32) {
		return invalid("binding requires full lowercase public actor fingerprint", nil)
	}
	if c.Identity != QualifiedProviderIdentity() {
		return failure(tracker.ErrorVersionSkew, "install_verified_provider", "binding tuple is not independently admitted", nil)
	}
	if runtime.GOOS != c.Identity.GOOS || runtime.GOARCH != c.Identity.GOARCH {
		return failure(tracker.ErrorVersionSkew, "install_verified_provider", "host target is not qualified", nil)
	}
	return nil
}

// PublicIdentity contains no private key or private storage location.
type PublicIdentity struct {
	Actor     string `json:"actor"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

// Open binds existing resources and performs only read-only public discovery.
// Future tracker port implementations share this transport; no port is implied.
func Open(ctx context.Context, config BindingConfig) (*BoundAdapter, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	executable, ei, err := canonicalPath(config.ExecutablePath, false)
	if err != nil {
		return nil, invalid("executable unavailable", err)
	}
	workspace, wi, err := canonicalPath(config.WorkspacePath, true)
	if err != nil {
		return nil, invalid("workspace unavailable", err)
	}
	digest, err := fileDigest(executable)
	if err != nil {
		return nil, invalid("executable cannot be verified", err)
	}
	if digest != config.Identity.BinarySHA256 {
		return nil, failure(tracker.ErrorVersionSkew, "install_verified_provider", "executable digest differs from admitted bytes", nil)
	}
	sum := sha256.Sum256([]byte("go-kitty.hubnot-workspace/1\x00" + config.ProjectID.String() + "\x00" + workspace + "\x00" + config.ExpectedActor))
	bound := &BoundAdapter{config: config, executableInfo: ei, workspaceInfo: wi, binding: tracker.TrackerBinding{ProjectID: config.ProjectID, Provider: "hubnot", ExecutablePath: executable, ExecutableVersion: config.Identity.SourceCommit, ExecutableDigest: digest, ProtocolSchema: 1, WorkspacePath: workspace, WorkspaceIdentity: "ws1:" + hex.EncodeToString(sum[:]), ConcurrencyMode: tracker.ConcurrencyGitNative, ReadOnly: config.ReadOnly, ObservedAt: time.Now().UTC()}}
	budget := newObservationBudget()
	defer budget.close()
	response, err := bound.execute(ctx, budget, command{argv: []string{"identity", "show", "--json"}, schema: identitySchema})
	if err != nil {
		return nil, err
	}
	if err := decodeStrict(response.envelope.Data, &bound.identity); err != nil {
		return nil, protocolFailure(err)
	}
	key, err := base64.RawStdEncoding.DecodeString(bound.identity.PublicKey)
	if err != nil || len(key) != 32 {
		return nil, protocolFailure(errors.New("identity public key is invalid"))
	}
	actor := sha256.Sum256(key)
	if bound.identity.Actor != hex.EncodeToString(actor[:]) || bound.identity.Actor != config.ExpectedActor {
		return nil, failure(tracker.ErrorWorkspaceMismatch, "repair_provider_binding", "public actor differs from binding", nil)
	}
	if len(bound.identity.Name) > 1024 {
		return nil, protocolFailure(errors.New("identity name exceeds limit"))
	}
	return bound, nil
}
func canonicalPath(path string, directory bool) (string, os.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	if directory != info.IsDir() || (!directory && (!info.Mode().IsRegular() || info.Mode()&0111 == 0)) {
		return "", nil, errors.New("wrong resource type or permissions")
	}
	return resolved, info, nil
}
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (64<<20)+1))
	if n > 64<<20 {
		return "", errors.New("executable exceeds admitted binary size bound")
	}
	return hex.EncodeToString(h.Sum(nil)), err
}
func validHex(value string, bytes int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == bytes && hex.EncodeToString(decoded) == value
}
func validEventID(value string) bool {
	return len(value) == 71 && value[:7] == "sha256:" && validHex(value[7:], 32)
}
func (b *BoundAdapter) verifyDispatch() error {
	if b == nil {
		return invalid("bound transport is missing", nil)
	}
	for _, r := range []struct {
		original, resolved string
		info               os.FileInfo
		directory          bool
	}{{b.config.ExecutablePath, b.binding.ExecutablePath, b.executableInfo, false}, {b.config.WorkspacePath, b.binding.WorkspacePath, b.workspaceInfo, true}} {
		path, info, err := canonicalPath(r.original, r.directory)
		if err != nil || path != r.resolved || !os.SameFile(info, r.info) {
			return failure(tracker.ErrorWorkspaceMismatch, "repair_provider_binding", "bound resource identity changed", err)
		}
	}
	digest, err := fileDigest(b.binding.ExecutablePath)
	if err != nil || digest != b.binding.ExecutableDigest {
		return failure(tracker.ErrorVersionSkew, "install_verified_provider", "executable changed before dispatch", err)
	}
	return nil
}
func (b *BoundAdapter) checkProject(project ids.ProjectID) error {
	if b == nil || project != b.binding.ProjectID {
		return failure(tracker.ErrorWorkspaceMismatch, "repair_provider_binding", "project differs from bound project", nil)
	}
	return nil
}
func (b *BoundAdapter) Binding() tracker.TrackerBinding {
	if b == nil {
		return tracker.TrackerBinding{}
	}
	return b.binding
}
func (b *BoundAdapter) PublicIdentity() PublicIdentity {
	if b == nil {
		return PublicIdentity{}
	}
	return b.identity
}
func invalid(message string, cause error) error {
	return failure(tracker.ErrorInvalidRequest, "repair_provider_binding", message, cause)
}
func failure(kind tracker.ErrorKind, recovery, message string, cause error) error {
	return &tracker.Error{Kind: kind, RecoveryCode: recovery, Message: message, Cause: cause}
}
func protocolFailure(cause error) error {
	return failure(tracker.ErrorMalformedProtocol, "repair_provider_binding", "provider response violates the admitted protocol", cause)
}
