package beads

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const workspaceIdentityEncodingVersion uint16 = 1

// BindingConfig is explicit project configuration. In particular, neither the
// executable nor provider workspace is discovered from PATH or the caller's
// current directory.
type BindingConfig struct {
	ProjectID      ids.ProjectID
	ExecutablePath string
	WorkspacePath  string
	// ProviderDatabaseIdentity is the absolute existing database directory
	// reported by `bd where --json` for embedded mode. In server mode it is the
	// exact logical database name reported by the server inspection commands;
	// external databases intentionally have no local data directory to resolve.
	ProviderDatabaseIdentity string
	ProtocolSchema           uint16
	ConcurrencyMode          tracker.ConcurrencyMode
	ReadOnly                 bool
	ExpectedExecutableDigest string
	ServerIdentity           string
	ServerEndpoint           string
	Credentials              map[string]string
}

type providerConnection struct {
	serverIdentity string
	serverEndpoint string
	credentials    map[string]string
}

// Open is the production composition seam for the Beads adapter. It resolves
// an explicit binding and connects it to WP03's bounded process runner.
func Open(config BindingConfig) (*BoundAdapter, error) {
	binding, err := ResolveBinding(config)
	if err != nil {
		return nil, err
	}
	connection, err := connectionFromConfig(config)
	if err != nil {
		return nil, err
	}
	return newConfiguredBoundAdapter(binding, newAdapter(nil), nil, connection)
}

// InspectProvider is the complete one-shot production entry for provider
// preflight and inspection. It ensures callers cannot accidentally construct a
// protocol runner without the canonical binding boundary.
func InspectProvider(ctx context.Context, config BindingConfig) (tracker.Inspection, error) {
	bound, err := Open(config)
	if err != nil {
		return tracker.Inspection{}, err
	}
	return bound.Inspect(ctx, tracker.InspectRequest{ProjectID: config.ProjectID})
}

// ResolveBinding canonicalizes an existing executable and provider workspace,
// and pins their identities in the provider-neutral binding value.
func ResolveBinding(config BindingConfig) (tracker.TrackerBinding, error) {
	if _, err := ids.ParseProjectID(config.ProjectID.String()); err != nil {
		return tracker.TrackerBinding{}, invalidBinding("project identity is invalid", err)
	}
	if config.ProtocolSchema == 0 {
		return tracker.TrackerBinding{}, invalidBinding("protocol schema is required", nil)
	}
	if config.ConcurrencyMode != tracker.ConcurrencyEmbeddedSerialized && config.ConcurrencyMode != tracker.ConcurrencyServerMultiwriter {
		return tracker.TrackerBinding{}, invalidBinding("concurrency mode is unknown", nil)
	}
	connection, err := connectionFromConfig(config)
	if err != nil {
		return tracker.TrackerBinding{}, err
	}
	executable, executableInfo, err := canonicalExisting(config.ExecutablePath, false)
	if err != nil {
		return tracker.TrackerBinding{}, invalidBinding("executable path cannot be resolved", err)
	}
	if executableInfo.Mode().IsDir() || executableInfo.Mode()&0111 == 0 {
		return tracker.TrackerBinding{}, invalidBinding("executable path is not an executable file", nil)
	}
	workspace, workspaceInfo, err := canonicalExisting(config.WorkspacePath, true)
	if err != nil {
		return tracker.TrackerBinding{}, invalidBinding("workspace path cannot be resolved", err)
	}
	if !workspaceInfo.IsDir() {
		return tracker.TrackerBinding{}, invalidBinding("workspace path is not a directory", nil)
	}
	digest, err := digestFile(executable)
	if err != nil {
		return tracker.TrackerBinding{}, invalidBinding("executable bytes cannot be read", err)
	}
	if config.ExpectedExecutableDigest != "" && !validDigest(config.ExpectedExecutableDigest) {
		return tracker.TrackerBinding{}, invalidBinding("expected executable digest is not lowercase SHA-256", nil)
	}
	if config.ExpectedExecutableDigest != "" && config.ExpectedExecutableDigest != digest {
		return tracker.TrackerBinding{}, &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "install_verified_provider", Message: "configured executable digest does not match its bytes"}
	}
	databaseIdentity := config.ProviderDatabaseIdentity
	var identity string
	if config.ConcurrencyMode == tracker.ConcurrencyEmbeddedSerialized {
		databaseIdentity, _, err = canonicalExisting(databaseIdentity, true)
		if err != nil {
			return tracker.TrackerBinding{}, invalidBinding("provider database path cannot be resolved", err)
		}
		identity = deriveWorkspaceIdentity(workspace, databaseIdentity)
	} else {
		if !validServerDatabase(databaseIdentity) {
			return tracker.TrackerBinding{}, invalidBinding("provider server database identity is invalid", nil)
		}
		host := connection.credentials["DOLT_SERVER_HOST"]
		port := connection.credentials["DOLT_SERVER_PORT"]
		identity = deriveServerWorkspaceIdentity(host, port, databaseIdentity)
	}
	binding := tracker.TrackerBinding{
		ProjectID:         config.ProjectID,
		Provider:          "beads",
		ExecutablePath:    executable,
		ExecutableVersion: SupportedBeadsVersion,
		ExecutableDigest:  digest,
		ProtocolSchema:    config.ProtocolSchema,
		WorkspacePath:     workspace,
		WorkspaceIdentity: identity,
		ConcurrencyMode:   config.ConcurrencyMode,
		ReadOnly:          config.ReadOnly,
		ObservedAt:        time.Now().UTC(),
	}
	if err := binding.Validate(); err != nil {
		return tracker.TrackerBinding{}, invalidBinding("resolved binding is invalid", err)
	}
	return binding, nil
}

func connectionFromConfig(config BindingConfig) (providerConnection, error) {
	connection := providerConnection{
		serverIdentity: config.ServerIdentity,
		serverEndpoint: config.ServerEndpoint,
		credentials:    make(map[string]string, len(config.Credentials)),
	}
	for key, value := range config.Credentials {
		if !allowedCredential(key) || strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return providerConnection{}, invalidBinding("server credential binding is invalid", nil)
		}
		connection.credentials[key] = value
	}
	if config.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		if strings.TrimSpace(connection.serverIdentity) == "" || strings.TrimSpace(connection.serverEndpoint) == "" {
			return providerConnection{}, invalidBinding("server mode requires pinned server identity and endpoint", nil)
		}
		host, hostPresent := connection.credentials["DOLT_SERVER_HOST"]
		port, portPresent := connection.credentials["DOLT_SERVER_PORT"]
		if !hostPresent || !portPresent || host == "" || port == "" || host+":"+port != connection.serverEndpoint {
			return providerConnection{}, invalidBinding("server endpoint must match allowlisted host and port binding", nil)
		}
	} else if connection.serverIdentity != "" || connection.serverEndpoint != "" || len(connection.credentials) != 0 {
		return providerConnection{}, invalidBinding("embedded mode cannot carry server connection facts", nil)
	}
	return connection, nil
}

func canonicalExisting(path string, directory bool) (string, os.FileInfo, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", nil, errors.New("path must be explicit and absolute")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	cleaned := filepath.Clean(absolute)
	canonical, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", nil, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", nil, err
	}
	canonical = filepath.Clean(canonical)
	info, err := os.Stat(canonical)
	if err != nil {
		return "", nil, err
	}
	if directory && !info.IsDir() {
		return "", nil, errors.New("path is not a directory")
	}
	return canonical, info, nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.CopyBuffer(hash, file, make([]byte, 32*1024)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func deriveWorkspaceIdentity(canonicalPath, databaseIdentity string) string {
	hash := sha256.New()
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], workspaceIdentityEncodingVersion)
	_, _ = hash.Write(version[:])
	writeIdentityField(hash, "provider", "beads")
	writeIdentityField(hash, "path", canonicalPath)
	writeIdentityField(hash, "database", databaseIdentity)
	return "ws1:" + hex.EncodeToString(hash.Sum(nil))
}

func deriveServerWorkspaceIdentity(host, port, database string) string {
	hash := sha256.New()
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], workspaceIdentityEncodingVersion)
	_, _ = hash.Write(version[:])
	writeIdentityField(hash, "provider", "beads")
	writeIdentityField(hash, "server_host", host)
	writeIdentityField(hash, "server_port", port)
	writeIdentityField(hash, "server_database", database)
	writeIdentityField(hash, "shared_server", "true")
	return "ws1:" + hex.EncodeToString(hash.Sum(nil))
}

func validServerDatabase(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character != '_' && character != '-' && (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func writeIdentityField(destination io.Writer, name, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(name)))
	_, _ = destination.Write(size[:])
	_, _ = io.WriteString(destination, name)
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = destination.Write(size[:])
	_, _ = io.WriteString(destination, value)
}

func invalidBinding(message string, cause error) error {
	return &tracker.Error{Kind: tracker.ErrorInvalidRequest, RecoveryCode: "repair_provider_binding", Message: message, Cause: cause}
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func validatePinnedBinding(binding tracker.TrackerBinding) error {
	if err := binding.Validate(); err != nil {
		return invalidBinding("binding is invalid", err)
	}
	if binding.Provider != "beads" {
		return invalidBinding("binding selects another provider", nil)
	}
	if binding.ExecutableVersion != SupportedBeadsVersion {
		return &tracker.Error{Kind: tracker.ErrorVersionSkew, RecoveryCode: "install_supported_provider", Message: "binding version is unsupported"}
	}
	if binding.ProtocolSchema != SupportedProtocolSchema {
		return &tracker.Error{Kind: tracker.ErrorSchemaSkew, RecoveryCode: "install_supported_provider", Message: "binding protocol schema is unsupported"}
	}
	if !validDigest(binding.ExecutableDigest) {
		return invalidBinding("binding must pin a lowercase SHA-256 executable digest", nil)
	}
	return nil
}

func sameBinding(left, right tracker.TrackerBinding) bool {
	return left.ProjectID == right.ProjectID && left.Provider == right.Provider &&
		left.ExecutablePath == right.ExecutablePath && left.ExecutableVersion == right.ExecutableVersion &&
		left.ExecutableDigest == right.ExecutableDigest && left.ProtocolSchema == right.ProtocolSchema &&
		left.WorkspacePath == right.WorkspacePath && left.WorkspaceIdentity == right.WorkspaceIdentity &&
		left.ConcurrencyMode == right.ConcurrencyMode && left.ReadOnly == right.ReadOnly
}

func workspaceMismatch(expected, observed string) error {
	return &tracker.Error{Kind: tracker.ErrorWorkspaceMismatch, RecoveryCode: "repair_provider_binding", Message: fmt.Sprintf("provider workspace identity differs from binding (%s)", observedIdentityLabel(expected, observed))}
}

func observedIdentityLabel(expected, observed string) string {
	if expected == observed {
		return "equal"
	}
	return "mismatch"
}
