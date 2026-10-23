package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/migrationledger"
)

func TestMigrationSelectionUsesPartitionAndSuccess(t *testing.T) {
	root := t.TempDir()
	m, ok := migrationByID("4.0.0rc7_exclude_spec_kitty")
	if !ok || !m.CloneLocal {
		t.Fatal("missing clone-local migration")
	}
	ledger := migrationledger.Ledger{Root: root}
	wrong := migrationRecord(m, "success", "")
	wrong.CloneLocal = false
	if err := ledger.Append(wrong); err != nil {
		t.Fatal(err)
	}
	if migrationSettled(root, m) {
		t.Fatal("tracked record settled clone-local effect")
	}
	for _, result := range []string{"skipped", "failed", "success"} {
		if err := ledger.Append(migrationRecord(m, result, "")); err != nil {
			t.Fatal(err)
		}
		if got := migrationSettled(root, m); got != (result == "success") {
			t.Fatalf("%s settled=%t", result, got)
		}
	}
	selected, err := applicableMigrations("0.0.0", "4.0.0rc8", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range selected {
		if step.ID == m.ID {
			t.Fatal("settled clone-local migration was selected again")
		}
	}
}

func TestMigrationDriftIsAuditOnlyAndReportDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	m, _ := migrationByID("4.0.0rc7_exclude_spec_kitty")
	record := migrationRecord(m, "success", "")
	record.ContentHash = "sha256:previous-source"
	ledger := migrationledger.Ledger{Root: root}
	if err := ledger.Append(record); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ledger.Path(true))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := migrationLedgerPayload(root)
	if err != nil {
		t.Fatal(err)
	}
	var row *jsonx.Object
	for _, raw := range payload.GetList("steps") {
		step := raw.(*jsonx.Object)
		if step.GetString("id") == m.ID {
			row = step
		}
	}
	if row == nil || !row.GetBool("applied") || !row.GetBool("drift") || row.GetBool("pending") {
		t.Fatalf("drift row=%+v", row)
	}
	after, err := os.ReadFile(ledger.Path(true))
	if err != nil || string(before) != string(after) {
		t.Fatal("read-only report changed ledger")
	}
	if _, err := os.Stat(ledger.Path(false)); !os.IsNotExist(err) {
		t.Fatal("read-only report created tracked ledger")
	}
}

func TestMigrationAdoptionHonorsVersionFloorAndTargetCap(t *testing.T) {
	root := t.TempDir()
	md := kittify.NewMetadata("4.0.0rc8")
	if _, err := md.Save(root); err != nil {
		t.Fatal(err)
	}
	selected, err := applicableMigrations("4.0.0rc8", "3.2.7", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) == 0 || selected[0].ID != migrationLedgerBackfillID {
		t.Fatalf("adoption/cap selected %v", selected)
	}
	for _, m := range selected[1:] {
		cmp, valid := versionCmp(m.TargetVersion, "3.2.7")
		if !m.CloneLocal || !valid || cmp > 0 {
			t.Fatalf("floor/cap did not constrain selection: %+v", m)
		}
	}
	backfill, _ := migrationByID(migrationLedgerBackfillID)
	if result, err := backfill.applyErr(root, false); err != nil || !result.Success {
		t.Fatalf("backfill: %+v %v", result, err)
	}
	if err := (migrationledger.Ledger{Root: root}).Append(migrationRecord(backfill, "success", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := md.Save(root); err != nil {
		t.Fatal(err)
	}
	if doc, err := kittify.LoadMetadataDoc(root); err != nil || doc.Has("migrations") {
		t.Fatalf("legacy migration mirror survived: %+v %v", doc, err)
	}
}

func TestInitLedgerLeavesCloneLocalWorkPendingOutsideGit(t *testing.T) {
	root := t.TempDir()
	if err := seedMigrationLedger(root); err != nil {
		t.Fatal(err)
	}
	ledger := migrationledger.Ledger{Root: root}
	if _, err := os.Stat(ledger.Path(true)); !os.IsNotExist(err) {
		t.Fatalf("clone-local ledger must not be seeded without git: %v", err)
	}
	tracked, err := ledger.Read(false)
	if err != nil || len(tracked) == 0 {
		t.Fatalf("tracked seed=%v %v", tracked, err)
	}
	before, err := os.ReadFile(ledger.Path(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := seedMigrationLedger(root); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(migrationledger.TrackedRelative)))
	if err != nil || string(before) != string(after) {
		t.Fatal("init seed was not idempotent")
	}
}
