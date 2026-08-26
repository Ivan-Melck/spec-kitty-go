package beads

import (
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
	ProjectID                ids.ProjectID
	ExecutablePath           string
	WorkspacePath            string
	ProviderDatabaseIdentity string
	ProtocolSchema           uint16
	ConcurrencyMode          tracker.ConcurrencyMode
	ReadOnly                 bool
	ExpectedExecutableDigest string
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
	identity := deriveWorkspaceIdentity(workspace, config.ProviderDatabaseIdentity)
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
