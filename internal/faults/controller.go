package faults

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrCrashRequested = errors.New("fault controller requested process crash")

type FileController struct {
	Directory   string
	Participant string
}

func (c FileController) Reach(ctx context.Context, boundary string) error {
	if c.Directory == "" {
		return nil
	}
	if !safePathComponent(boundary) || !safePathComponent(c.Participant) {
		return fmt.Errorf("fault boundary and participant must be portable path components")
	}
	action, err := os.ReadFile(filepath.Join(c.Directory, boundary+".action"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read fault action: %w", err)
	}
	if err := os.WriteFile(filepath.Join(c.Directory, boundary+"."+c.Participant+".reached"), []byte("reached\n"), 0o600); err != nil {
		return fmt.Errorf("publish fault boundary: %w", err)
	}
	switch strings.TrimSpace(string(action)) {
	case "continue", "":
		return nil
	case "crash":
		return ErrCrashRequested
	case "block":
		return waitForRelease(ctx, filepath.Join(c.Directory, boundary+".release"))
	default:
		return fmt.Errorf("unknown fault action %q", strings.TrimSpace(string(action)))
	}
}

func safePathComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, `/\\`)
}

func waitForRelease(ctx context.Context, path string) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect fault release: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
