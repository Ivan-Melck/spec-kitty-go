package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
)

type claimProcess struct {
	command *exec.Cmd
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

func inspectAuthority(ctx context.Context, helper, database string) (sqlite.AuthoritySnapshot, error) {
	command := exec.CommandContext(ctx, helper, "inspect", "--db", database, "--work", "work-1", "--revision", "revision-1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return sqlite.AuthoritySnapshot{}, fmt.Errorf("inspect helper exit: %w; stderr=%s; stdout=%s", err, stderr.String(), stdout.String())
	}
	var response helpercmd.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return sqlite.AuthoritySnapshot{}, fmt.Errorf("decode inspect response %q: %w", stdout.String(), err)
	}
	if !response.OK || response.Snapshot == nil {
		return sqlite.AuthoritySnapshot{}, fmt.Errorf("inspect response: %+v", response)
	}
	return *response.Snapshot, nil
}

type claimInput struct {
	Database  string
	Barrier   string
	Operation string
	Actor     string
	Process   string
}

func startClaim(ctx context.Context, helper string, input claimInput) (*claimProcess, error) {
	command := exec.CommandContext(ctx, helper,
		"claim", "--db", input.Database, "--barrier", input.Barrier,
		"--operation", input.Operation, "--project", "project-1", "--mission", "mission-1",
		"--revision", "revision-1", "--work", "work-1", "--actor", input.Actor,
		"--process", input.Process, "--ttl", "1m",
	)
	process := &claimProcess{command: command}
	command.Stdout, command.Stderr = &process.stdout, &process.stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	return process, nil
}

func (p *claimProcess) wait() (helpercmd.Response, error) {
	if err := p.command.Wait(); err != nil {
		return helpercmd.Response{}, fmt.Errorf("helper exit: %w; stderr=%s; stdout=%s", err, p.stderr.String(), p.stdout.String())
	}
	var response helpercmd.Response
	if err := json.Unmarshal(p.stdout.Bytes(), &response); err != nil {
		return response, fmt.Errorf("decode helper response %q: %w", p.stdout.String(), err)
	}
	return response, nil
}

func waitForReached(ctx context.Context, directory string, count int) error {
	return waitForBoundaryReached(ctx, directory, "SR-00", count)
}

func waitForBoundaryReached(ctx context.Context, directory, boundary string, count int) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		matches, err := filepath.Glob(filepath.Join(directory, boundary+".*.reached"))
		if err != nil {
			return err
		}
		if len(matches) == count {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %d claimants, observed %d: %w", count, len(matches), ctx.Err())
		case <-ticker.C:
		}
	}
}
