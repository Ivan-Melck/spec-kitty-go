package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Runner struct {
	Executable string
}

func (r Runner) Run(ctx context.Context, directory string, args ...string) (string, error) {
	return r.RunEnv(ctx, directory, nil, args...)
}

func (r Runner) RunEnv(ctx context.Context, directory string, environment []string, args ...string) (string, error) {
	return r.run(ctx, directory, environment, nil, args...)
}

// RunInput executes Git with an explicit stdin payload. It is used for Git's
// transactional plumbing protocols, where shell redirection would weaken the
// adapter boundary and make atomic multi-ref updates difficult to test.
func (r Runner) RunInput(ctx context.Context, directory, input string, args ...string) (string, error) {
	return r.run(ctx, directory, nil, []byte(input), args...)
}

func (r Runner) run(ctx context.Context, directory string, environment []string, input []byte, args ...string) (string, error) {
	executable := r.Executable
	if executable == "" {
		executable = "git"
	}
	argv := append([]string{"-C", directory}, args...)
	command := exec.CommandContext(ctx, executable, argv...)
	command.Env = append(os.Environ(), environment...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %q in %q: %w: %s", args, directory, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
