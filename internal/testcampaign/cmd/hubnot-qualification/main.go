// Command hubnot-qualification executes the pinned public consumer in disposable
// isolated fixtures. It never installs or initializes a provider implicitly.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
)

type installOptions struct{ Source, Destination, GoBinary string }
type installReceipt struct {
	Schema           string                      `json:"schema"`
	Provider         hubnot.ProviderIdentity     `json:"provider"`
	Source           string                      `json:"source_directory"`
	SourceTree       string                      `json:"source_tree"`
	Binary           testcampaign.HubnotArtifact `json:"binary"`
	Argv             []string                    `json:"argv"`
	Environment      []string                    `json:"environment"`
	AlreadyInstalled bool                        `json:"already_installed"`
}

func main() { os.Exit(commandMain(os.Args[1:], os.Stdout, os.Stderr)) }
func commandMain(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(args) > 0 && args[0] == "manifest" {
		if len(args) != 1 {
			return 2
		}
		if json.NewEncoder(stdout).Encode(testcampaign.HubnotManifest()) != nil {
			return 1
		}
		return 0
	}
	if len(args) > 0 && args[0] == "install" {
		fs := flag.NewFlagSet("install", flag.ContinueOnError)
		fs.SetOutput(stderr)
		var o installOptions
		fs.StringVar(&o.Source, "source", "", "explicit clean Git checkout of the accepted Hubnot commit")
		fs.StringVar(&o.Destination, "destination", "", "new absolute executable path (matching existing bytes are retained)")
		fs.StringVar(&o.GoBinary, "go-bin", "", "explicit absolute Go 1.26.7 executable")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 {
			return 2
		}
		receipt, err := installProvider(ctx, o)
		if err != nil {
			fmt.Fprintln(stderr, "Hubnot install:", err)
			return 1
		}
		if json.NewEncoder(stdout).Encode(receipt) != nil {
			return 1
		}
		return 0
	}
	return qualificationMain(ctx, args, stdout, stderr)
}

func buildEnvironment() []string {
	return []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "LANG=C.UTF-8", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOEXPERIMENT=", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOOS=linux", "GOARCH=amd64", "GOAMD64=v1", "CGO_ENABLED=0"}
}
func canonicalRegular(path string, executable bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("explicit clean absolute path required")
	}
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("regular file required")
	}
	if executable && i.Mode()&0111 == 0 {
		return errors.New("executable mode required")
	}
	return nil
}
func fileArtifact(path string) (testcampaign.HubnotArtifact, error) {
	if err := canonicalRegular(path, false); err != nil {
		return testcampaign.HubnotArtifact{}, err
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return testcampaign.HubnotArtifact{}, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > (256<<20)+1 {
		return testcampaign.HubnotArtifact{}, errors.New("artifact must be bounded regular file")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, info.Size()+1))
	if err != nil || n != info.Size() {
		return testcampaign.HubnotArtifact{}, errors.New("artifact changed during read")
	}
	return testcampaign.HubnotArtifact{Path: path, SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n}, nil
}
func toolOutput(ctx context.Context, dir string, env []string, program string, args ...string) ([]byte, error) {
	// Bootstrap/build commands are bounded too; they cannot retain arbitrary output.
	c := exec.CommandContext(ctx, program, args...)
	c.Dir = dir
	c.Env = env
	c.WaitDelay = time.Second
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	output := &limitedOutput{limit: 1 << 20, overflow: func() { _ = c.Cancel() }}
	c.Stdout = output
	c.Stderr = output
	err := c.Run()
	if c.Process != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	if err != nil {
		return output.bytes(), fmt.Errorf("%s failed: %w", filepath.Base(program), err)
	}
	if output.exceeded {
		return nil, errors.New("tool output exceeded bound")
	}
	return output.bytes(), nil
}
func gitIdentity(ctx context.Context, source string) (string, string, error) {
	if !filepath.IsAbs(source) || filepath.Clean(source) != source {
		return "", "", errors.New("explicit source directory required")
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || resolved != source {
		return "", "", errors.New("source path must be canonical")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return "", "", err
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return "", "", err
	}
	run := func(args ...string) (string, error) {
		out, e := toolOutput(ctx, source, buildEnvironment(), git, args...)
		return strings.TrimSpace(string(out)), e
	}
	top, err := run("rev-parse", "--show-toplevel")
	if err != nil || top != source {
		return "", "", errors.New("source must be the Git checkout root")
	}
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	tree, err := run("rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", "", err
	}
	dirty, err := run("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || dirty != "" {
		return "", "", errors.New("source must have no tracked or untracked changes")
	}
	return head, tree, nil
}
func verifyGo(ctx context.Context, path string) error {
	if err := canonicalRegular(path, true); err != nil {
		return err
	}
	out, err := toolOutput(ctx, "", buildEnvironment(), path, "version")
	want := "go version " + hubnot.QualifiedProviderIdentity().Toolchain + " linux/amd64"
	if err != nil || strings.TrimSpace(string(out)) != want {
		return errors.New("Go toolchain is not the qualified version/host")
	}
	return nil
}
func installProvider(ctx context.Context, o installOptions) (installReceipt, error) {
	p := hubnot.QualifiedProviderIdentity()
	r := installReceipt{Schema: "go-kitty.hubnot-install/1", Provider: p, Source: o.Source, Environment: buildEnvironment()}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if runtime.GOOS != p.GOOS || runtime.GOARCH != p.GOARCH {
		return r, errors.New("host target is not qualified")
	}
	if !filepath.IsAbs(o.Destination) || filepath.Clean(o.Destination) != o.Destination {
		return r, errors.New("explicit destination required")
	}
	if err := verifyGo(ctx, o.GoBinary); err != nil {
		return r, err
	}
	head, tree, err := gitIdentity(ctx, o.Source)
	if err != nil || head != p.SourceCommit {
		return r, errors.New("source must be clean at the exact accepted commit")
	}
	r.SourceTree = tree
	relative, err := filepath.Rel(o.Source, o.Destination)
	if err != nil || relative == "." || (!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && relative != "..") {
		return r, errors.New("destination must be outside source checkout")
	}
	if _, err = os.Lstat(o.Destination); err == nil {
		a, e := fileArtifact(o.Destination)
		if e != nil || a.SHA256 != p.BinarySHA256 {
			return r, errors.New("existing destination differs; preserved")
		}
		if err = canonicalRegular(o.Destination, true); err != nil {
			return r, err
		}
		r.Binary = a
		r.AlreadyInstalled = true
		return r, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(o.Destination))
	if err != nil || parent != filepath.Dir(o.Destination) {
		return r, errors.New("destination parent must exist and be canonical")
	}
	temp, err := os.MkdirTemp(parent, ".hubnot-build-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(temp)
	candidate := filepath.Join(temp, "hn")
	r.Argv = []string{o.GoBinary, "build", "-trimpath", "-buildvcs=false", "-mod=readonly", "-ldflags=-s -w", "-o", candidate, "."}
	if _, err = toolOutput(ctx, o.Source, r.Environment, r.Argv[0], r.Argv[1:]...); err != nil {
		return r, err
	}
	after, afterTree, err := gitIdentity(ctx, o.Source)
	if err != nil || after != head || afterTree != tree {
		return r, errors.New("source changed during build")
	}
	a, err := fileArtifact(candidate)
	if err != nil || a.SHA256 != p.BinarySHA256 {
		return r, errors.New("built executable digest is not admitted")
	}
	// Hard-link publishes complete verified bytes atomically and refuses overwrite.
	if err = os.Link(candidate, o.Destination); err != nil {
		return r, err
	}
	r.Binary, err = fileArtifact(o.Destination)
	return r, err
}
