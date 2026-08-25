package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/execution"
)

func TestWorkspaceMaintenanceCommandsAreReachableFromProductionRoot(t *testing.T) {
	for _, command := range []string{"workspace-inspect", "workspace-cleanup"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(context.Background(), []string{command, "--operation", "not portable"}, &stdout, &stderr); code != 0 {
				t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
			}
			var response WorkspaceMaintenanceResponse
			if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
				t.Fatalf("decode %q: %v", stdout.String(), err)
			}
			if response.OK || response.Error == nil || response.Error.Kind != execution.KindInvalidCommand {
				t.Fatalf("route response=%+v", response)
			}
		})
	}
}
