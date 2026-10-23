package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RemoteContact executes one bounded, noninteractive remote operation. The
// existing git argument boundary is preserved; no shell parses remote inputs.
func RemoteContact(dir, remote string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	ssh := strings.TrimSpace(os.Getenv("GIT_SSH_COMMAND"))
	if ssh == "" {
		ssh, _ = Run(dir, "config", "--get", "core.sshCommand")
	}
	if ssh == "" && os.Getenv("GIT_SSH") == "" {
		ssh = "ssh"
	}
	if ssh != "" {
		program := strings.Trim(strings.Fields(ssh)[0], "\"'")
		base := strings.ToLower(filepath.Base(program))
		if (base == "ssh" || base == "ssh.exe" || base == "plink" || base == "plink.exe") && !strings.Contains(ssh, "BatchMode") {
			ssh += " -o BatchMode=yes"
		}
		command.Env = append(command.Env, "GIT_SSH_COMMAND="+ssh)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			if ctx.Err() != nil {
				detail = "timed out"
			} else {
				detail = "no error output"
			}
		} else {
			detail = strings.Split(detail, "\n")[0]
		}
		return "", fmt.Errorf("remote %s unreachable: %s", remote, detail)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// FetchCommitSubtree materializes one exact commit through a fresh sparse
// checkout. The destination belongs to the caller's temporary directory.
func FetchCommitSubtree(repository, sha, subtree, destination string) (string, error) {
	if err := os.MkdirAll(destination, 0755); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "core.autocrlf", "false"}, {"remote", "add", "--", "origin", repository}} {
		if _, err := Run(destination, args...); err != nil {
			return "", err
		}
	}
	args := []string{"fetch", "-q", "--depth", "1", "--no-tags", "--no-recurse-submodules", "--no-auto-gc"}
	_, err := RemoteContact(destination, repository, 120*time.Second, append(append([]string{}, args...), "--filter=blob:none", "--", "origin", sha)...)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "filter") {
		_, err = RemoteContact(destination, repository, 120*time.Second, append(args, "--", "origin", sha)...)
	}
	if err != nil {
		return "", err
	}
	if _, err := Run(destination, "sparse-checkout", "set", "--cone", "--", subtree); err != nil {
		return "", err
	}
	if _, err := RemoteContact(destination, repository, 120*time.Second, "read-tree", "-m", "-u", "FETCH_HEAD"); err != nil {
		return "", err
	}
	return filepath.Join(destination, filepath.FromSlash(subtree)), nil
}
