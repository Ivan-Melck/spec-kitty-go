package helpercmd

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArchitectureCheckCommandExercisesProductionGuard(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"architecture-check", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("architecture check code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "architecture check passed") {
		t.Fatalf("architecture check output=%q", stdout.String())
	}
}
