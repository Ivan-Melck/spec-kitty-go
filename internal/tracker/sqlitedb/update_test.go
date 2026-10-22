package sqlitedb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PYF-05: Update keeps foreign tables, user indexes, header fields and the
// freelist, and leaves a database SQLite accepts (integrity_check) with
// the rewritten tables' content.
func TestUpdatePreservesForeignObjectsAndHeader(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "x.db")
	var big strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&big, "INSERT INTO foreign_t VALUES (%d, '%s');", i, strings.Repeat("f", 50+i%700))
	}
	sqlite3(t, db, "PRAGMA user_version=42; PRAGMA application_id=1234;"+
		"CREATE TABLE foreign_t (id INTEGER PRIMARY KEY, v TEXT);"+
		"CREATE INDEX foreign_v ON foreign_t(v);"+big.String()+
		strings.Join([]string{testSpecs[0].SQL, testSpecs[1].SQL}, ";")+";"+
		"INSERT INTO items VALUES ('a', 'x', NULL); INSERT INTO pairs VALUES ('p', 'q', 'r');"+
		"CREATE TABLE junk (v TEXT); INSERT INTO junk VALUES (randomblob(9000)); DROP TABLE junk;")
	before := sqlite3(t, db, "SELECT count(*), sum(length(v)) FROM foreign_t")
	f, err := Read(db)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 3; round++ {
		data := map[string][]Row{}
		var items []Row
		for i := 0; i < 200*(round+1); i++ {
			items = append(items, Row{RowID: int64(i + 1), Values: []any{fmt.Sprintf("k%04d", i), strings.Repeat("v", 30+i%900), nil}})
		}
		data["items"] = items
		data["pairs"] = f.Rows("pairs")
		if err := Update(db, testSpecs, data, f.ChangeCounter+1, f.SchemaCookie); err != nil {
			t.Fatal(err)
		}
		if got := sqlite3(t, db, "PRAGMA integrity_check"); got != "ok" {
			t.Fatalf("round %d: integrity_check: %s", round, got)
		}
		if got := sqlite3(t, db, "SELECT count(*), sum(length(v)) FROM foreign_t"); got != before {
			t.Fatalf("foreign table changed: %s != %s", got, before)
		}
		if got := sqlite3(t, db, "PRAGMA user_version; PRAGMA application_id"); got != "42\n1234" {
			t.Fatalf("header fields: %q", got)
		}
		if got := sqlite3(t, db, "SELECT count(*) FROM items; SELECT x||y||z FROM pairs"); got != fmt.Sprintf("%d\npqr", 200*(round+1)) {
			t.Fatalf("rows: %q", got)
		}
		if got := sqlite3(t, db, "SELECT count(*) FROM items INDEXED BY sqlite_autoindex_items_1 WHERE k > ''"); got != fmt.Sprint(200*(round+1)) {
			t.Fatalf("index: %q", got)
		}
		g, err := Read(db)
		if err != nil {
			t.Fatal(err)
		}
		if g.ChangeCounter != f.ChangeCounter+1 {
			t.Fatalf("change counter %d after %d", g.ChangeCounter, f.ChangeCounter)
		}
		f = g
	}
	// SQLite keeps writing to the updated file.
	sqlite3(t, db, "INSERT INTO items VALUES ('zz', 'y', 'b'); INSERT INTO foreign_t VALUES (1000, 'n')")
	if got := sqlite3(t, db, "PRAGMA integrity_check"); got != "ok" {
		t.Fatalf("after sqlite write: %s", got)
	}
}

func TestUpdateEmptyFileBuildsFresh(t *testing.T) {
	db := filepath.Join(t.TempDir(), "n.db")
	if err := os.WriteFile(db, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Update(db, testSpecs, map[string][]Row{"items": {{RowID: 1, Values: []any{"a", "b", nil}}}}, 1, 1); err != nil {
		t.Fatal(err)
	}
	if got := sqlite3(t, db, "PRAGMA integrity_check; SELECT k FROM items"); got != "ok\na" {
		t.Fatalf("%q", got)
	}
}
