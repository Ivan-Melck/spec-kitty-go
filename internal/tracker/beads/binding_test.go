package beads

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestBindingWorkspaceIdentityConvergesAcrossAliasesAndWorktrees(t *testing.T) {
	root := t.TempDir()
	executable := testExecutable(t, root)
	workspace := filepath.Join(root, "catalog")
	mustMkdir(t, workspace)
	alias := filepath.Join(root, "catalog-alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}

	config := BindingConfig{ProjectID: ids.ProjectID("project-1"), ExecutablePath: executable, WorkspacePath: workspace, ProviderDatabaseIdentity: "db-1", ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized}
	first, err := ResolveBinding(config)
	if err != nil {
		t.Fatal(err)
	}
	config.WorkspacePath = filepath.Join(alias, ".")
	second, err := ResolveBinding(config)
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkspacePath != second.WorkspacePath || first.WorkspaceIdentity != second.WorkspaceIdentity {
		t.Fatalf("aliases diverged: %#v %#v", first, second)
	}

	for _, lane := range []string{"lane-a", "lane-b"} {
		mustMkdir(t, filepath.Join(root, lane))
		before, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(filepath.Join(root, lane)); err != nil {
			t.Fatal(err)
		}
		resolved, resolveErr := ResolveBinding(config)
		if err := os.Chdir(before); err != nil {
			t.Fatal(err)
		}
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		if resolved.WorkspaceIdentity != first.WorkspaceIdentity {
			t.Fatalf("lane %s changed identity", lane)
		}
	}
}

func TestBindingWorkspaceIdentityDistinguishesWorkspacesAndDatabaseIdentity(t *testing.T) {
	root := t.TempDir()
	executable := testExecutable(t, root)
	config := BindingConfig{ProjectID: ids.ProjectID("project-1"), ExecutablePath: executable, ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized}
	var identities []string
	for _, name := range []string{"one/same", "two/same"} {
		workspace := filepath.Join(root, name)
		mustMkdir(t, workspace)
		config.WorkspacePath = workspace
		binding, err := ResolveBinding(config)
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, binding.WorkspaceIdentity)
	}
	if identities[0] == identities[1] {
		t.Fatal("distinct workspaces share identity")
	}
	config.WorkspacePath = filepath.Join(root, "one/same")
	config.ProviderDatabaseIdentity = "other-db"
	third, err := ResolveBinding(config)
	if err != nil {
		t.Fatal(err)
	}
	if identities[0] == third.WorkspaceIdentity {
		t.Fatal("database identity was omitted from workspace identity")
	}
}

func TestBindingRejectsInvalidExplicitInputs(t *testing.T) {
	root := t.TempDir()
	executable := testExecutable(t, root)
	workspace := filepath.Join(root, "workspace")
	mustMkdir(t, workspace)
	valid := BindingConfig{ProjectID: ids.ProjectID("project-1"), ExecutablePath: executable, WorkspacePath: workspace, ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized}
	tests := []struct {
		name string
		edit func(*BindingConfig)
	}{
		{"empty project", func(c *BindingConfig) { c.ProjectID = "" }},
		{"relative executable", func(c *BindingConfig) { c.ExecutablePath = "bd" }},
		{"relative workspace", func(c *BindingConfig) { c.WorkspacePath = "." }},
		{"missing workspace", func(c *BindingConfig) { c.WorkspacePath = filepath.Join(root, "missing") }},
		{"unknown mode", func(c *BindingConfig) { c.ConcurrencyMode = "surprise" }},
		{"zero schema", func(c *BindingConfig) { c.ProtocolSchema = 0 }},
		{"bad digest", func(c *BindingConfig) { c.ExpectedExecutableDigest = "ABC" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			_, err := ResolveBinding(candidate)
			if !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestBindingPinsExecutableDigest(t *testing.T) {
	root := t.TempDir()
	executable := testExecutable(t, root)
	workspace := filepath.Join(root, "workspace")
	mustMkdir(t, workspace)
	binding, err := ResolveBinding(BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized})
	if err != nil {
		t.Fatal(err)
	}
	if !validDigest(binding.ExecutableDigest) {
		t.Fatalf("digest=%q", binding.ExecutableDigest)
	}
	_, err = ResolveBinding(BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized, ExpectedExecutableDigest: strings.Repeat("0", 64)})
	if !tracker.IsKind(err, tracker.ErrorUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestServerBindingRequiresPinnedConnectionAndCopiesCredentials(t *testing.T) {
	root := t.TempDir()
	executable := testExecutable(t, root)
	workspace := filepath.Join(root, "workspace")
	mustMkdir(t, workspace)
	base := BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyServerMultiwriter, ServerIdentity: "server-1", ServerEndpoint: "127.0.0.1:3306", Credentials: map[string]string{"DOLT_SERVER_HOST": "127.0.0.1", "DOLT_SERVER_PORT": "3306"}}
	tests := []struct {
		name string
		edit func(*BindingConfig)
	}{{"identity", func(c *BindingConfig) { c.ServerIdentity = "" }}, {"endpoint", func(c *BindingConfig) { c.ServerEndpoint = "" }}, {"endpoint mismatch", func(c *BindingConfig) { c.ServerEndpoint = "other:3306" }}, {"unknown credential", func(c *BindingConfig) { c.Credentials["SECRET"] = "value" }}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Credentials = map[string]string{"DOLT_SERVER_HOST": "127.0.0.1", "DOLT_SERVER_PORT": "3306"}
			test.edit(&candidate)
			if _, err := ResolveBinding(candidate); !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	bound, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Credentials["DOLT_SERVER_HOST"] = "attacker"
	command := bound.commandBinding()
	if command.credentials["DOLT_SERVER_HOST"] != "127.0.0.1" {
		t.Fatal("bound credentials changed through caller map")
	}
	command.credentials["DOLT_SERVER_HOST"] = "changed"
	if bound.commandBinding().credentials["DOLT_SERVER_HOST"] != "127.0.0.1" {
		t.Fatal("command received shared credential map")
	}
}

func TestWorkspaceMismatchDiagnosticIsPayloadFree(t *testing.T) {
	err := workspaceMismatch("secret-expected", "secret-observed")
	var portable *tracker.Error
	if !errors.As(err, &portable) || portable.Kind != tracker.ErrorWorkspaceMismatch {
		t.Fatalf("error=%v", err)
	}
	if portable.Message != "provider workspace identity differs from binding (mismatch)" {
		t.Fatalf("message=%q", portable.Message)
	}
}

func testExecutable(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "bd")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
}
