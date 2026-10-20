package kittify

import (
	"bufio"
	"bytes"
	"os"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// EventsPath is <root>/.kittify/canonical-events.jsonl.
func EventsPath(root string) string { return Path(root, EventsFile) }

// pydanticUTC renders a UTC datetime the way pydantic's JSON mode does
// ("...Z", microseconds only when non-zero).
func pydanticUTC(t time.Time) string {
	t = t.UTC()
	s := t.Format("2006-01-02T15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		s += "." + pad6(us)
	}
	return s + "Z"
}

func pad6(n int) string {
	b := []byte("000000")
	for i := 5; i >= 0 && n > 0; i-- {
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b)
}

// EmitProjectInitialized appends the ProjectInitialized lifecycle envelope to
// canonical-events.jsonl unless one for projectUUID already exists
// (status.emit_project_initialized). Returns true when written.
func EmitProjectInitialized(root, projectUUID, projectSlug, actor, runtimeVersion string) (bool, error) {
	path := EventsPath(root)
	existing, _ := os.ReadFile(path)
	sc := bufio.NewScanner(bytes.NewReader(existing))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		o, err := jsonx.DecodeObject(line)
		if err != nil {
			continue
		}
		if o.GetString("event_type") == "ProjectInitialized" {
			if p := o.GetObject("payload"); p != nil && p.GetString("project_uuid") == projectUUID {
				return false, nil
			}
		}
	}
	now := clock.Now()
	payload := jsonx.NewObject(
		"project_uuid", projectUUID,
		"project_slug", nullable(projectSlug),
		"actor", actor,
		"runtime_version", nullable(runtimeVersion),
		"initialized_at", pydanticUTC(now),
	)
	env := jsonx.NewObject(
		"event_id", ulid.New(),
		"event_type", "ProjectInitialized",
		"aggregate_id", projectUUID,
		"aggregate_type", "Project",
		"schema_version", "5.0.0",
		"timestamp", clock.NowUTCISO(),
		"payload", payload,
		"project_uuid", projectUUID,
		"project_slug", nullable(projectSlug),
	)
	line := jsonx.Dumps(env, jsonx.Line) + "\n"
	data := append(append([]byte(nil), existing...), line...)
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		data = append(append(append([]byte(nil), existing...), '\n'), line...)
	}
	return true, writeAtomic(path, data, 0o600)
}
