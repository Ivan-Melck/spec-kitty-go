package missionstate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var speedateCases = []string{
	"", "x", "2025", "2025-01-01", "2025-01-01T00:00", "2025-01-01T00:00:00", "2025-01-01T00:00:00Z",
	"2025-01-01t00:00:00z", "2025-01-01_00:00", "2025-01-01 00:00:00+0000", "2025-01-01T00:00:00+00",
	"2025-01-01T00:00:00+00:0", "2025-01-01T00:00:00+24:00", "2025-01-01T00:00:00-23:59", "2025-01-01T00:00:00.",
	"2025-01-01T00:00:00.5", "2025-01-01T00:00:00,123456789", "2025-01-01T00:00:60", "2025-01-01T23:59:59",
	"2025-01-01T24:00:00", "2025-01-01T00:60", "2025-01-01T0:00", "2025-01-01T", "2025-01-01 ", "2025-01-01Z",
	"2024-02-29", "2023-02-29", "1900-02-29", "2000-02-29", "2025-00-10", "2025-12-00", "2025-12-32",
	"2025/01/01", "2025-01/01", "202a-01-01", "2025-1a-01", "2025-01-1b", "20250101", "1700000000.5", "-5",
	"+5", "1e5", "12.3.4", ".", "2025-01-01T00:00:00Zjunk", "2025-01-01T00:00:00 +00:00",
}

// TestPydanticDatetimeAgainstPython compares the lax datetime verdict and
// message with pydantic in the oracle venv, when present.
func TestPydanticDatetimeAgainstPython(t *testing.T) {
	py := filepath.Join(os.Getenv("HOME"), ".cache/spec-kitty-parity/oracle-venv/bin/python")
	if _, err := os.Stat(py); err != nil {
		t.Skip("oracle venv not available")
	}
	in, _ := json.Marshal(speedateCases)
	script := `
import json, sys
from datetime import datetime
from pydantic import BaseModel, ValidationError
class M(BaseModel):
    t: datetime
out = []
for s in json.loads(sys.argv[1]):
    try:
        M(t=s); out.append("")
    except ValidationError as e:
        out.append(e.errors()[0]["msg"])
print(json.dumps(out))
`
	b, err := exec.Command(py, "-I", "-c", script, string(in)).Output()
	if err != nil {
		t.Fatalf("python: %v", err)
	}
	var want []string
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	for i, c := range speedateCases {
		if got := pydanticDatetimeError(c); got != want[i] {
			t.Errorf("%q: got %q want %q", c, got, want[i])
		}
	}
}
