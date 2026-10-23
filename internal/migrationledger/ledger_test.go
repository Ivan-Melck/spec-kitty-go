package migrationledger

import (
	"os"
	"strings"
	"testing"
)

func ledgerRecord(id, result string, cloneLocal bool) Record {
	return Record{ID: id, AppliedAt: "2026-10-23T12:00:00+00:00", Result: result, Identity: id, ContentHash: "sha256:source", CLIVersion: "4.0.0rc8", CloneLocal: cloneLocal}
}

func TestLedgerPartitionsAndAliasOrder(t *testing.T) {
	ledger := Ledger{Root: t.TempDir()}
	legacy := ledgerRecord("0.10.12_constitution_cleanup", "failed", false)
	canonical := ledgerRecord("0.10.12_charter_cleanup", "skipped", false)
	last := legacy
	last.Result = "success"
	if err := ledger.AppendMany([]Record{legacy, ledgerRecord("local", "success", true), canonical, last}); err != nil {
		t.Fatal(err)
	}
	tracked, err := ledger.Read(false)
	if err != nil || len(tracked) != 1 || tracked[0].ID != canonical.ID || tracked[0].Result != "success" {
		t.Fatalf("tracked=%+v err=%v", tracked, err)
	}
	local, err := ledger.Read(true)
	if err != nil || len(local) != 1 || local[0].ID != "local" {
		t.Fatalf("local=%+v err=%v", local, err)
	}
	b, err := os.ReadFile(ledger.Path(false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "clone_local") || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("tracked encoding: %s", b)
	}
	st, err := os.Stat(ledger.Path(false))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode: %v, %v", st, err)
	}
}

func TestLedgerReadToleratesBrokenRowsAndPreservesThemOnAppend(t *testing.T) {
	ledger := Ledger{Root: t.TempDir()}
	if err := ledger.Append(ledgerRecord("first", "success", false)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(ledger.Path(false), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("not json\n{}\n{\"id\": \"bad-date\", \"applied_at\": \"bad\", \"result\": \"success\"}\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Append(ledgerRecord("next", "success", false)); err != nil {
		t.Fatal(err)
	}
	records, err := ledger.Read(false)
	if err != nil || len(records) != 2 || records[1].ID != "next" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	b, err := os.ReadFile(ledger.Path(false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "not json\n{}\n") {
		t.Fatalf("append discarded old raw records: %s", b)
	}
}
