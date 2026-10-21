package sqlitedb

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var testSpecs = []TableSpec{
	{Name: "items", SQL: "CREATE TABLE items (\n  k TEXT PRIMARY KEY,\n  a TEXT NOT NULL,\n  b TEXT\n)", Indexes: [][]int{{0}}},
	{Name: "pairs", SQL: "CREATE TABLE pairs (\n  x TEXT NOT NULL,\n  y TEXT NOT NULL,\n  z TEXT NOT NULL,\n  PRIMARY KEY (x, y),\n  UNIQUE (y, z)\n)", Indexes: [][]int{{0, 1}, {1, 2}}},
}

func sqlite3(t *testing.T, db string, sql string) string {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	out, err := exec.Command(bin, db, sql).CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 %q: %v\n%s", sql, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestReadSQLiteWrittenFile(t *testing.T) {
	f, err := Read("testdata/empty-tracker.db")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Master) != 7 || f.SchemaCookie != 3 {
		t.Fatalf("master=%d cookie=%d", len(f.Master), f.SchemaCookie)
	}
	for _, n := range []string{"tracker_issues", "tracker_checkpoints", "tracker_mappings"} {
		if !f.HasTable(n) || len(f.Rows(n)) != 0 {
			t.Fatalf("table %s", n)
		}
	}
}

func TestReadSQLiteMultiLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	sqlite3(t, path, "CREATE TABLE items (k TEXT PRIMARY KEY, a TEXT NOT NULL, b TEXT); WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i<5000) INSERT INTO items SELECT 'k'||i, printf('%.*c', (i%7)*1500+1, 'x'), NULL FROM c; DELETE FROM items WHERE rowid % 3 = 0")
	f, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := f.Rows("items")
	if len(rows) != 5000-1666 {
		t.Fatalf("rows=%d", len(rows))
	}
	for _, r := range rows {
		if len(r.Values[1].(string)) != int(r.RowID%7)*1500+1 || r.Values[0] != fmt.Sprintf("k%d", r.RowID) {
			t.Fatalf("row %d bad", r.RowID)
		}
	}
}

func TestRecordRoundTrip(t *testing.T) {
	vals := []any{nil, int64(0), int64(1), int64(-5), int64(300), int64(1 << 40), int64(-1 << 62), 2.5, "héllo", []byte{1, 2}, strings.Repeat("x", 500)}
	got, err := DecodeRecord(EncodeRecord(vals))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, vals) {
		t.Fatalf("got %#v", got)
	}
}

func build(n int, payload int) map[string][]Row {
	data := map[string][]Row{}
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key-%05d", (i*7919)%n)
		data["items"] = append(data["items"], Row{RowID: int64(i + 1), Values: []any{k, strings.Repeat("p", payload), nil}})
		data["pairs"] = append(data["pairs"], Row{RowID: int64(i + 1), Values: []any{fmt.Sprintf("WP%04d", i), "beads", fmt.Sprintf("ext-%d-%s", i, strings.Repeat("e", i%300))}})
	}
	return data
}

func TestWriteIntegrity(t *testing.T) {
	for _, tc := range []struct{ n, payload int }{{0, 0}, {1, 10}, {50, 100}, {400, 50}, {3000, 20}, {30, 9000}, {200, 3000}, {20000, 5}} {
		t.Run(fmt.Sprintf("%d-%d", tc.n, tc.payload), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.db")
			data := build(tc.n, tc.payload)
			if err := Write(path, testSpecs, data, 7, 2); err != nil {
				t.Fatal(err)
			}
			f, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(f.Rows("items")) != tc.n || len(f.Rows("pairs")) != tc.n {
				t.Fatalf("rows: %d %d", len(f.Rows("items")), len(f.Rows("pairs")))
			}
			for i, r := range f.Rows("items") {
				if !reflect.DeepEqual(r, data["items"][i]) {
					t.Fatalf("row %d: %#v", i, r)
				}
			}
			if got := sqlite3(t, path, "PRAGMA integrity_check"); got != "ok" {
				t.Fatalf("integrity: %s", got)
			}
			if tc.n > 0 {
				want := fmt.Sprintf("%d", tc.n)
				if got := sqlite3(t, path, "SELECT count(*) FROM items WHERE k >= 'key-'"); got != want {
					t.Fatalf("index scan count %s", got)
				}
			}
		})
	}
}

func TestSQLiteCanUpdateOurFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	if err := Write(path, testSpecs, build(10, 10), 1, 2); err != nil {
		t.Fatal(err)
	}
	sqlite3(t, path, "INSERT INTO pairs(x,y,z) VALUES('WP0003','beads','n') ON CONFLICT(x,y) DO UPDATE SET z=excluded.z; INSERT INTO items(k,a) VALUES('zz','q')")
	if got := sqlite3(t, path, "PRAGMA integrity_check"); got != "ok" {
		t.Fatalf("integrity: %s", got)
	}
	f, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Rows("items")) != 11 {
		t.Fatalf("items=%d", len(f.Rows("items")))
	}
	found := false
	for _, r := range f.Rows("pairs") {
		if r.Values[0] == "WP0003" && r.Values[2] == "n" {
			found = true
		}
	}
	if !found {
		t.Fatal("upsert not visible")
	}
}
