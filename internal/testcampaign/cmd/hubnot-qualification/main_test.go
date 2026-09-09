package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInstallerRefusesBeforeDestinationMutation(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "hn")
	original := []byte("unrelated executable")
	if err := os.WriteFile(destination, original, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := installProvider(context.Background(), installOptions{Source: filepath.Join(root, "missing"), Destination: destination, GoBinary: "/missing/go"})
	if err == nil {
		t.Fatal("missing source/toolchain accepted")
	}
	after, _ := os.ReadFile(destination)
	if string(after) != string(original) {
		t.Fatal("unrelated executable changed")
	}
}
func TestInstallerRequiresExplicitToolchain(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "hn")
	_, err := installProvider(context.Background(), installOptions{Source: root, Destination: destination, GoBinary: "go"})
	if err == nil {
		t.Fatal("PATH toolchain accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("preflight wrote destination")
	}
}

func TestInstallerQualifiedBuildAndDestinationPreservation(t *testing.T) {
	origin := os.Getenv("GO_KITTY_TEST_HUBNOT_SOURCE")
	if origin == "" {
		t.Skip("explicit local Hubnot Git source required")
	}
	root := t.TempDir()
	source := filepath.Join(root, "accepted source with spaces")
	git := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("/usr/bin/git", args...)
		c.Dir = dir
		c.Env = buildEnvironment()
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git(root, "clone", "--quiet", "--no-checkout", origin, source)
	git(source, "checkout", "--quiet", "--detach", hubnot.QualifiedProviderIdentity().SourceCommit)
	options := installOptions{Source: source, Destination: filepath.Join(root, "hn"), GoBinary: qualificationTestGo(t)}
	t.Setenv("GOFLAGS", "-this-ambient-setting-must-not-apply")
	t.Setenv("GOWORK", "/missing/ambient/work")
	t.Setenv("GOEXPERIMENT", "invalid-ambient-experiment")
	t.Setenv("GOTOOLCHAIN", "invalid-ambient-toolchain")
	t.Setenv("GOROOT", "/missing/ambient/goroot")
	t.Setenv("GOPROXY", "https://invalid.example")
	poison := filepath.Join(root, "poison")
	if err := os.Mkdir(poison, 0700); err != nil {
		t.Fatal(err)
	}
	gitMarker := filepath.Join(root, "ambient-git-executed")
	if err := os.WriteFile(filepath.Join(poison, "git"), []byte("#!/bin/sh\nprintf invoked > '"+gitMarker+"'\nexit 91\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", poison)
	receipt, err := installProvider(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gitMarker); !os.IsNotExist(err) {
		t.Fatal("ambient Git executed for valid source")
	}
	if receipt.AlreadyInstalled || receipt.Binary.SHA256 != hubnot.QualifiedProviderIdentity().BinarySHA256 || len(receipt.Argv) == 0 {
		t.Fatal("exact accepted recipe not executed")
	}
	before, err := os.ReadFile(options.Destination)
	if err != nil {
		t.Fatal(err)
	}
	again, err := installProvider(context.Background(), options)
	if err != nil || !again.AlreadyInstalled {
		t.Fatalf("matching existing artifact: %v", err)
	}
	after, err := os.ReadFile(options.Destination)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("matching executable changed")
	}
	unrelated := filepath.Join(root, "unrelated")
	if err = os.WriteFile(unrelated, []byte("recovery material"), 0700); err != nil {
		t.Fatal(err)
	}
	options.Destination = unrelated
	if _, err = installProvider(context.Background(), options); err == nil {
		t.Fatal("unrelated destination admitted")
	}
	after, err = os.ReadFile(unrelated)
	if err != nil || string(after) != "recovery material" {
		t.Fatal("recovery bytes overwritten")
	}
	link := filepath.Join(root, "link")
	if err = os.Symlink(receipt.Binary.Path, link); err != nil {
		t.Fatal(err)
	}
	options.Destination = link
	if _, err = installProvider(context.Background(), options); err == nil {
		t.Fatal("symlink destination admitted")
	}
	fakeGo := filepath.Join(root, "fake-compiler")
	script := `#!/bin/sh
if test "$1" = version; then echo 'go version go1.26.7 linux/amd64'; exit 0; fi
while test "$#" -gt 0; do
 if test "$1" = -o; then shift; printf 'unqualified output' > "$1"; /usr/bin/chmod 700 "$1"; exit 0; fi
 shift
done
exit 1
`
	if err = os.WriteFile(fakeGo, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	badBuild := options
	badBuild.GoBinary = fakeGo
	badBuild.Destination = filepath.Join(root, "wrong-binary")
	if _, err = installProvider(context.Background(), badBuild); err == nil || !strings.Contains(err.Error(), "digest is not admitted") {
		t.Fatalf("wrong compiled bytes not refused at admission: %v", err)
	}
	if _, err = os.Lstat(badBuild.Destination); !os.IsNotExist(err) {
		t.Fatal("unqualified build published destination")
	}
	options.Destination = filepath.Join(root, "refused-new")
	if err = os.WriteFile(filepath.Join(source, "dirty-fixture"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = installProvider(context.Background(), options); err == nil {
		t.Fatal("dirty source admitted")
	}
	if _, err = os.Lstat(options.Destination); !os.IsNotExist(err) {
		t.Fatal("dirty source created destination")
	}
	if err = os.Remove(filepath.Join(source, "dirty-fixture")); err != nil {
		t.Fatal(err)
	}
	git(source, "checkout", "--quiet", "--detach", "HEAD~1")
	if _, err = installProvider(context.Background(), options); err == nil {
		t.Fatal("wrong clean source admitted")
	}
	if _, err = os.Lstat(options.Destination); !os.IsNotExist(err) {
		t.Fatal("wrong source created destination")
	}
}
func TestInstallerToolOverflowCancelsPromptlyAndArtifactRejectsFIFO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	out, err := toolOutput(ctx, "", buildEnvironment(), "/bin/sh", "-c", "yes oversized")
	if err == nil || len(out) > 1<<20 || time.Since(started) > 2*time.Second {
		t.Fatal("tool overflow did not promptly bound/cancel")
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err = syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = fileArtifact(fifo); err == nil {
		t.Fatal("FIFO accepted as artifact")
	}
}

func TestInstallerWrongToolchainAndTargetRefuseWithoutWrite(t *testing.T) {
	for _, version := range []string{"go version go1.26.6 linux/amd64", "go version go1.26.7 linux/arm64", "go version go1.26.7 darwin/amd64"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			compiler := filepath.Join(root, "go")
			destination := filepath.Join(root, "hn")
			if err := os.WriteFile(compiler, []byte("#!/bin/sh\necho '"+version+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := installProvider(context.Background(), installOptions{Source: root, Destination: destination, GoBinary: compiler}); err == nil {
				t.Fatal("unqualified toolchain/target admitted")
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatal("toolchain refusal changed destination")
			}
		})
	}
}

func qualificationTestGo(t *testing.T) string {
	t.Helper()
	compiler := os.Getenv("GO_KITTY_TEST_GO_BINARY")
	if compiler == "" {
		var err error
		compiler, err = exec.LookPath("go")
		if err != nil {
			t.Fatal(err)
		}
	}
	compiler, err := filepath.Abs(compiler)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err = filepath.EvalSymlinks(compiler)
	if err != nil {
		t.Fatal(err)
	}
	return compiler
}

func TestInstallerAmbientGitCannotAdmitUntrackedSource(t *testing.T) {
	hn := os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY")
	if hn == "" {
		t.Skip("explicit admitted binary required for existing-destination adversary")
	}
	admitted, err := os.ReadFile(hn)
	if err != nil {
		t.Fatal(err)
	}
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "not-a-git-source")
			poison := filepath.Join(root, "poison")
			for _, dir := range []string{source, poison} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			gitMarker := filepath.Join(root, "git-executed")
			buildMarker := filepath.Join(root, "build-executed")
			stub := fmt.Sprintf("#!/bin/sh\nprintf invoked > '%s'\ncase \"$*\" in\n 'rev-parse --show-toplevel') printf '%%s\\n' \"$PWD\";;\n 'rev-parse HEAD') printf '%%s\\n' '%s';;\n 'rev-parse HEAD^{tree}') printf '%%s\\n' '61c2f2e87f7828e71e82bc422f98c172e17539c9';;\n 'status --porcelain=v1 --untracked-files=all') exit 0;;\n *) exit 94;;\nesac\n", gitMarker, hubnot.QualifiedProviderIdentity().SourceCommit)
			if err := os.WriteFile(filepath.Join(poison, "git"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			compiler := filepath.Join(root, "compiler-spy")
			spy := "#!/bin/sh\nif test \"$1\" = version; then echo 'go version go1.26.7 linux/amd64'; exit 0; fi\nprintf build > '" + buildMarker + "'\nexit 93\n"
			if err := os.WriteFile(compiler, []byte(spy), 0700); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, "hn")
			if existing {
				if err := os.WriteFile(destination, admitted, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", poison)
			var stdout, stderr bytes.Buffer
			code := commandMain([]string{"install", "--source", source, "--destination", destination, "--go-bin", compiler}, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("forged source admitted: %s", stdout.String())
			}
			for _, marker := range []string{gitMarker, buildMarker} {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Errorf("untrusted source reached external effect %s", marker)
				}
			}
			if existing {
				after, err := os.ReadFile(destination)
				if err != nil || !bytes.Equal(after, admitted) {
					t.Fatal("existing binary changed")
				}
			} else if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("absent destination created")
			}
		})
	}
}
