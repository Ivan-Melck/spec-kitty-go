package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	qualifiedVersion = "1.2.2"
	qualifiedSchema  = 1
	fixtureMarker    = ".spec-kitty-beads-qualification-root"
	maxCommandOutput = 4 << 20
)

type binaryIdentity struct {
	Path      string `json:"path"`
	Version   string `json:"version"`
	Digest    string `json:"digest_sha256"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	Build     string `json:"build,omitempty"`
	Schema    int    `json:"schema_version"`
	SourceTag string `json:"source_version"`
}

type fixture struct {
	Root       string
	Repository string
	Workspace  string
	Worktrees  string
	Backup     string
	Restore    string
	Evidence   string
	identity   binaryIdentity
	keep       bool
}

func verifyBinary(ctx context.Context, path, expectedDigest string) (binaryIdentity, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return binaryIdentity{}, errors.New("BEADS_BIN must be an absolute clean path")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return binaryIdentity{}, fmt.Errorf("resolve BEADS_BIN: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil || canonical != filepath.Clean(canonical) {
		return binaryIdentity{}, errors.New("BEADS_BIN did not resolve to a canonical path")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return binaryIdentity{}, errors.New("BEADS_BIN must be an executable regular file")
	}
	if len(expectedDigest) != sha256.Size*2 || strings.ToLower(expectedDigest) != expectedDigest {
		return binaryIdentity{}, errors.New("expected digest must be lowercase SHA-256")
	}
	if _, err := hex.DecodeString(expectedDigest); err != nil {
		return binaryIdentity{}, errors.New("expected digest must be lowercase SHA-256")
	}
	file, err := os.Open(canonical)
	if err != nil {
		return binaryIdentity{}, fmt.Errorf("open BEADS_BIN: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(file, 1<<30))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return binaryIdentity{}, errors.New("hash BEADS_BIN")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest != expectedDigest {
		return binaryIdentity{}, errors.New("BEADS_BIN digest is not the reviewed qualified digest")
	}

	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, canonical, "version", "--json")
	command.Env = qualificationEnv("", "")
	stdout, stderr, err := boundedCombined(command)
	if err != nil {
		return binaryIdentity{}, fmt.Errorf("verify BEADS_BIN version: %w", err)
	}
	if len(bytes.TrimSpace(stderr)) != 0 {
		// Warnings are tolerated, but they are deliberately not persisted.
	}
	var envelope struct {
		Schema int `json:"schema_version"`
		Data   struct {
			Version string `json:"version"`
			Build   string `json:"build"`
		} `json:"data"`
	}
	if err := decodeSingleJSON(stdout, &envelope); err != nil {
		return binaryIdentity{}, fmt.Errorf("decode BEADS_BIN identity: %w", err)
	}
	if envelope.Schema != qualifiedSchema || envelope.Data.Version != qualifiedVersion {
		return binaryIdentity{}, errors.New("BEADS_BIN version or protocol schema is outside the qualified tuple")
	}
	return binaryIdentity{
		Path: canonical, Version: envelope.Data.Version, Digest: digest,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Build: envelope.Data.Build,
		Schema: envelope.Schema, SourceTag: "v" + qualifiedVersion,
	}, nil
}

func newFixture(identity binaryIdentity, keep bool) (*fixture, error) {
	root, err := os.MkdirTemp("", "spec-kitty-beads-qualification-")
	if err != nil {
		return nil, fmt.Errorf("create qualification root: %w", err)
	}
	result := &fixture{
		Root: root, Repository: filepath.Join(root, "repository"), Workspace: filepath.Join(root, "beads-workspace"),
		Worktrees: filepath.Join(root, "worktrees"), Backup: filepath.Join(root, "native-backup"),
		Restore: filepath.Join(root, "restore-workspace"), Evidence: filepath.Join(root, "evidence"),
		identity: identity, keep: keep,
	}
	if err := os.WriteFile(filepath.Join(root, fixtureMarker), []byte("owned\n"), 0o600); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	for _, path := range []string{result.Repository, result.Workspace, result.Worktrees, result.Backup, result.Restore, result.Evidence} {
		if err := result.validateChild(path, true); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			_ = os.RemoveAll(root)
			return nil, fmt.Errorf("create fixture directory: %w", err)
		}
	}
	return result, nil
}

func (fixture *fixture) initialize(ctx context.Context) error {
	if fixture == nil {
		return errors.New("fixture is missing")
	}
	if _, _, err := runBounded(ctx, fixture.Repository, qualificationEnv(fixture.Root, fixture.Workspace), "git", "init", "--quiet"); err != nil {
		return fmt.Errorf("initialize disposable git repository: %w", err)
	}
	args := []string{"init", "--stealth", "--skip-agents", "--skip-hooks", "--non-interactive", "--prefix", "skt"}
	if _, _, err := runBounded(ctx, fixture.Repository, qualificationEnv(fixture.Root, fixture.Workspace), fixture.identity.Path, args...); err != nil {
		return fmt.Errorf("initialize disposable Beads workspace: %w", err)
	}
	return nil
}

func (fixture *fixture) validateChild(path string, allowAbsent bool) error {
	if fixture == nil || !filepath.IsAbs(fixture.Root) || !filepath.IsAbs(path) {
		return errors.New("fixture paths must be absolute")
	}
	root := filepath.Clean(fixture.Root)
	candidate := filepath.Clean(path)
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path escapes the test-owned fixture root")
	}
	if info, err := os.Lstat(candidate); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("fixture target must not be a symlink")
		}
	} else if !allowAbsent || !errors.Is(err, os.ErrNotExist) {
		return errors.New("fixture target is unavailable")
	}
	return nil
}

func (fixture *fixture) validateEmptyRestoreTarget() error {
	if err := fixture.validateChild(fixture.Restore, false); err != nil {
		return err
	}
	entries, err := os.ReadDir(fixture.Restore)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("restore target must be an empty fixture-owned directory")
	}
	return nil
}

func (fixture *fixture) cleanup() error {
	if fixture == nil || fixture.keep {
		return nil
	}
	root := filepath.Clean(fixture.Root)
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "spec-kitty-beads-qualification-") {
		return errors.New("refusing cleanup of an unrecognized root")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing cleanup of an invalid fixture root")
	}
	if _, err := os.Stat(filepath.Join(root, fixtureMarker)); err != nil {
		return errors.New("refusing cleanup without fixture ownership marker")
	}
	return os.RemoveAll(root)
}

func qualificationEnv(root, workspace string) []string {
	allowed := map[string]bool{
		"PATH": true, "TMPDIR": true, "LANG": true, "LC_ALL": true, "TZ": true, "SYSTEMROOT": true,
		"HOME": true, "GOCACHE": true, "GOMODCACHE": true, "GOPATH": true, "GOROOT": true, "GOENV": true,
		"GOTOOLCHAIN": true, "CGO_ENABLED": true,
	}
	environment := make([]string, 0, len(allowed)+8)
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && allowed[key] {
			environment = append(environment, item)
		}
	}
	environment = append(environment, "BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1", "GIT_CONFIG_NOSYSTEM=1")
	if root != "" {
		environment = append(environment,
			"XDG_CONFIG_HOME="+filepath.Join(root, "xdg-config"),
			"XDG_DATA_HOME="+filepath.Join(root, "xdg-data"),
			"XDG_CACHE_HOME="+filepath.Join(root, "xdg-cache"),
			"GIT_CONFIG_GLOBAL="+filepath.Join(root, "gitconfig"),
		)
	}
	if workspace != "" {
		environment = append(environment, "BEADS_DIR="+workspace)
	}
	return environment
}

func runBounded(ctx context.Context, directory string, environment []string, executable string, args ...string) ([]byte, []byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, executable, args...)
	command.Dir = directory
	command.Env = environment
	return boundedCombined(command)
}

func boundedCombined(command *exec.Cmd) ([]byte, []byte, error) {
	var stdout, stderr limitedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if stdout.overflow || stderr.overflow {
		return nil, nil, errors.New("command output exceeded qualification bound")
	}
	if err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (buffer *limitedBuffer) Write(payload []byte) (int, error) {
	remaining := maxCommandOutput - buffer.Len()
	if remaining <= 0 {
		buffer.overflow = true
		return len(payload), nil
	}
	write := len(payload)
	if write > remaining {
		write = remaining
		buffer.overflow = true
	}
	_, _ = buffer.Buffer.Write(payload[:write])
	return len(payload), nil
}

func decodeSingleJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values or trailing content")
	}
	return nil
}
