package hubnot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testChild(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "child")
	cmd := exec.Command("go", "build", "-o", path, "./testdata/protocol_child.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build child: %v %s", err, out)
	}
	return path
}
func TestTransportBoundsAndCancellation(t *testing.T) {
	child := testChild(t)
	for _, mode := range []string{"stdout", "stderr", "sleep", "descendant"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			result := runProcess(ctx, child, t.TempDir(), []string{mode}, nil)
			if !result.started || result.err == nil {
				t.Fatalf("unbounded %s: %+v", mode, result)
			}
			if len(result.stdout) > maxStdoutBytes || len(result.stderr) > maxStderrBytes {
				t.Fatal("capture overflow")
			}
			if time.Since(result.start) > 3*time.Second {
				t.Fatal("child not reaped promptly")
			}
		})
	}
}
func TestTransportDropsAmbientEnvironment(t *testing.T) {
	child := testChild(t)
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "HN_SECRET", "DOLT_PASSWORD", "AWS_SECRET_ACCESS_KEY"} {
		t.Setenv(name, "private")
	}
	r := runProcess(context.Background(), child, t.TempDir(), []string{"environment"}, nil)
	if r.err != nil || r.exitCode != 0 || string(r.stdout) != "safe" {
		t.Fatalf("unsafe environment %q %v", r.stdout, r.err)
	}
}
func TestTransportUnstarted(t *testing.T) {
	r := runProcess(context.Background(), filepath.Join(t.TempDir(), "missing"), t.TempDir(), nil, nil)
	if r.started || r.err == nil {
		t.Fatal("missing child started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = runProcess(ctx, os.Args[0], t.TempDir(), nil, nil)
	if r.started {
		t.Fatal("canceled child started")
	}
}
