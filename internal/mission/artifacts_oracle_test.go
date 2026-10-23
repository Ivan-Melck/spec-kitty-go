package mission

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestArtifactKindsMatchRC8(t *testing.T) {
	f, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	python := strings.TrimSpace(strings.TrimPrefix(first, "#!"))
	s := testkit.NewSandbox(t)
	oracle := s.Exec("", python, "-c", `import json
from mission_runtime.artifacts import kind_for_mission_file, _MISSION_FILE_KIND_BY_BASENAME, _COORD_RESIDUE_DIRS
paths=[]
for prefix in ("kitty-specs/demo/", "/tmp/project/kitty-specs/demo/"):
 paths.extend(prefix+name for name in (*_MISSION_FILE_KIND_BY_BASENAME, *_COORD_RESIDUE_DIRS, "unknown.md"))
 paths.extend(prefix+directory+"/nested/file.md" for directory in _COORD_RESIDUE_DIRS)
 paths.extend(prefix+name for name in ("tasks/review-cycle-WP01.md", "tasks/WP01/review-cycle-1.md", "research/meta.json", "outside/report.md"))
print(json.dumps({path:kind.value if (kind:=kind_for_mission_file(path)) else "" for path in paths}))`)
	if oracle.Code != 0 {
		t.Fatalf("oracle: %s%s", oracle.Stdout, oracle.Stderr)
	}
	var expected map[string]string
	if err := json.Unmarshal([]byte(oracle.Stdout), &expected); err != nil {
		t.Fatal(err)
	}
	for path, kind := range expected {
		if got := ArtifactKindForPath(path); got != kind {
			t.Errorf("%s: kind=%q, want %q", path, got, kind)
		}
	}
}
