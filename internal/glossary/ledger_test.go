package glossary

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func TestConcurrentTornTailRecoveryPreservesEveryAppend(t *testing.T) {
	root := t.TempDir()
	path := EventLogPath(root, "m")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("{}\nunfinished fragment"), 0o644)
	start := make(chan struct{})
	failures := make(chan error, 6)
	var workers sync.WaitGroup
	for i := 0; i < 6; i++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			<-start
			event := jsonx.NewObject("event_type", "TermCandidateObserved", "mission_id", "m", "run_id", "r", "term", "workspace", "source_step", "plan", "actor_id", "a", "confidence", jsonx.Float(1), "extraction_method", "request_text", "context", fmt.Sprint(id))
			failures <- AppendEvent(root, "m", event)
		}(i)
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(rows) != 7 || rows[0] != "{}" {
		t.Fatalf("lost ledger rows: %s", data)
	}
	seen := map[string]bool{}
	for _, row := range rows[1:] {
		event, err := jsonx.DecodeObject([]byte(row))
		if err != nil {
			t.Fatal(err)
		}
		seen[event.GetString("context")] = true
	}
	if len(seen) != 6 {
		t.Fatalf("lost or duplicated append: %s", data)
	}
	if torn, err := os.ReadFile(path + ".torn"); err != nil || string(torn) != "unfinished fragment\n" {
		t.Fatalf("tail not preserved exactly once: %q %v", torn, err)
	}
	if lock, err := os.ReadFile(path + ".lock"); err != nil || len(lock) != 0 {
		t.Fatalf("repair lock not released: %q %v", lock, err)
	}
}
