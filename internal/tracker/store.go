package tracker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/tracker/sqlitedb"
)

// Schema of the tracker cache, byte-identical to what upstream's
// TrackerSqliteStore._init_db stores in sqlite_master.
var storeSchema = []sqlitedb.TableSpec{
	{Name: "tracker_issues", SQL: "CREATE TABLE tracker_issues (\n                    identity TEXT PRIMARY KEY,\n                    system TEXT NOT NULL,\n                    workspace TEXT NOT NULL,\n                    issue_id TEXT NOT NULL,\n                    payload TEXT NOT NULL,\n                    updated_at TEXT\n                )", Indexes: [][]int{{0}}},
	{Name: "tracker_checkpoints", SQL: "CREATE TABLE tracker_checkpoints (\n                    checkpoint_key TEXT PRIMARY KEY,\n                    cursor TEXT,\n                    updated_since TEXT\n                )", Indexes: [][]int{{0}}},
	{Name: "tracker_mappings", SQL: "CREATE TABLE tracker_mappings (\n                    wp_id TEXT NOT NULL,\n                    system TEXT NOT NULL,\n                    workspace TEXT NOT NULL,\n                    external_id TEXT NOT NULL,\n                    external_key TEXT,\n                    external_url TEXT,\n                    created_at TEXT NOT NULL,\n                    updated_at TEXT NOT NULL,\n                    PRIMARY KEY (wp_id, system),\n                    UNIQUE (system, workspace, external_id)\n                )", Indexes: [][]int{{0, 1}, {1, 2, 3}}},
}

// TrackersDir is <runtime root>/trackers.
func TrackersDir() string { return filepath.Join(RuntimeRoot(), "trackers") }

// BuildScope mirrors build_tracker_scope.
func BuildScope(provider, workspace, serverURL, username, teamSlug string) string {
	or := func(s, d string) string {
		if s == "" {
			return d
		}
		return s
	}
	server := strings.ToLower(strings.TrimSpace(or(serverURL, "local")))
	user := strings.ToLower(strings.TrimSpace(or(username, "anonymous")))
	team := strings.ToLower(strings.TrimSpace(or(teamSlug, "no-team")))
	id := strings.ToLower(strings.TrimSpace(provider)) + "|" + strings.ToLower(strings.TrimSpace(workspace)) + "|" + server + "|" + user + "|" + team
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:16]
}

// DefaultDBPath mirrors default_tracker_db_path.
func DefaultDBPath(provider, workspace, serverURL, username, teamSlug string) string {
	return filepath.Join(TrackersDir(), BuildScope(provider, workspace, serverURL, username, teamSlug)+".db")
}

// Store is TrackerSqliteStore. Every operation reads the database file and
// every mutation rewrites the tracker tables in place (sqlitedb.Update:
// other tables, header fields and the freelist are kept).
type Store struct{ Path string }

// OpenStore creates the parent directory and any missing tables.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return nil, err
	}
	s := &Store{Path: path}
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	created := uint32(0)
	for _, spec := range storeSchema {
		if !f.HasTable(spec.Name) {
			created++
		}
	}
	if created > 0 {
		if err := s.write(f, created); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) read() (*sqlitedb.File, error) {
	f, err := sqlitedb.Read(s.Path)
	if err != nil {
		return nil, &Crash{Type: "sqlite3.DatabaseError", Msg: err.Error()}
	}
	return f, nil
}

func (s *Store) write(f *sqlitedb.File, schemaBump uint32) error {
	data := map[string][]sqlitedb.Row{}
	for _, spec := range storeSchema {
		data[spec.Name] = f.Rows(spec.Name)
	}
	counter := f.ChangeCounter + 1
	if f.ChangeCounter == 0 && f.SchemaCookie == 0 {
		counter = schemaBump
	}
	return sqlitedb.Update(s.Path, storeSchema, data, counter, f.SchemaCookie+schemaBump)
}

func (s *Store) mutate(table string, fn func(rows []sqlitedb.Row) ([]sqlitedb.Row, error)) error {
	f, err := s.read()
	if err != nil {
		return err
	}
	rows, err := fn(append([]sqlitedb.Row(nil), f.Rows(table)...))
	if err != nil {
		return err
	}
	data := map[string][]sqlitedb.Row{}
	for _, spec := range storeSchema {
		data[spec.Name] = f.Rows(spec.Name)
	}
	data[table] = rows
	return sqlitedb.Update(s.Path, storeSchema, data, f.ChangeCounter+1, f.SchemaCookie)
}

func nextRowID(rows []sqlitedb.Row) int64 {
	var m int64
	for _, r := range rows {
		if r.RowID > m {
			m = r.RowID
		}
	}
	return m + 1
}

func col(r sqlitedb.Row, i int) any {
	if i < len(r.Values) {
		return r.Values[i]
	}
	return nil
}

func colStr(r sqlitedb.Row, i int) string {
	switch v := col(r, i).(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return pyjson.Str(v)
	}
}

// ListIssues returns cached issues (optionally for one system) by identity.
func (s *Store) ListIssues(system string, all bool) ([]*Issue, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	rows := append([]sqlitedb.Row(nil), f.Rows("tracker_issues")...)
	sort.SliceStable(rows, func(i, j int) bool { return sqlitedb.Compare(col(rows[i], 0), col(rows[j], 0)) < 0 })
	var out []*Issue
	for _, r := range rows {
		if !all && sqlitedb.Compare(col(r, 1), system) != 0 {
			continue
		}
		issue, err := decodeIssuePayload(colStr(r, 4))
		if err != nil {
			return nil, err
		}
		out = append(out, issue)
	}
	return out, nil
}

// CountIssues counts cached issues for system.
func (s *Store) CountIssues(system string) (int, error) {
	issues, err := s.ListIssues(system, false)
	return len(issues), err
}

func decodeIssuePayload(text string) (*Issue, error) {
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil, &Crash{Type: "json.decoder.JSONDecodeError", Msg: err.Error()}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &Crash{Type: "AttributeError", Msg: "payload is not an object"}
	}
	return deserializeIssue(o)
}

// GetIssue returns the cached issue with ref's identity (nil if absent).
func (s *Store) GetIssue(ref ExternalRef) (*Issue, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	for _, r := range f.Rows("tracker_issues") {
		if col(r, 0) == ref.Identity() {
			return decodeIssuePayload(colStr(r, 4))
		}
	}
	return nil, nil
}

// UpsertIssue stores issue keyed by its identity.
func (s *Store) UpsertIssue(issue *Issue) error {
	now := clock.NowUTCISO()
	payload := jsonx.Dumps(serializeIssue(issue), jsonx.Options{Indent: -1, Compact: true, EnsureASCII: true})
	id := issue.Ref.Identity()
	vals := []any{id, issue.Ref.System, issue.Ref.Workspace, issue.Ref.ID, payload, now}
	return s.mutate("tracker_issues", func(rows []sqlitedb.Row) ([]sqlitedb.Row, error) {
		for i, r := range rows {
			if col(r, 0) == id {
				rows[i].Values = vals
				return rows, nil
			}
		}
		return append(rows, sqlitedb.Row{RowID: nextRowID(rows), Values: vals}), nil
	})
}

// Checkpoint is SyncCheckpoint.
type Checkpoint struct {
	Cursor       *string
	UpdatedSince *PyTime
}

// GetCheckpoint returns the stored checkpoint (nil when absent).
func (s *Store) GetCheckpoint(key string) (*Checkpoint, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	for _, r := range f.Rows("tracker_checkpoints") {
		if col(r, 0) != key {
			continue
		}
		cp := &Checkpoint{}
		if c := col(r, 1); c != nil {
			s := pyjson.Str(c)
			cp.Cursor = &s
		}
		cp.UpdatedSince = parseDT(col(r, 2))
		return cp, nil
	}
	return nil, nil
}

// SetCheckpoint upserts a checkpoint.
func (s *Store) SetCheckpoint(cp Checkpoint, key string) error {
	var cursor, since any
	if cp.Cursor != nil {
		cursor = *cp.Cursor
	}
	if cp.UpdatedSince != nil {
		since = cp.UpdatedSince.ISO()
	}
	vals := []any{key, cursor, since}
	return s.mutate("tracker_checkpoints", func(rows []sqlitedb.Row) ([]sqlitedb.Row, error) {
		for i, r := range rows {
			if col(r, 0) == key {
				rows[i].Values = vals
				return rows, nil
			}
		}
		return append(rows, sqlitedb.Row{RowID: nextRowID(rows), Values: vals}), nil
	})
}

// Mapping is a tracker_mappings row.
type Mapping struct {
	WPID, System, Workspace, ExternalID string
	ExternalKey, ExternalURL            *string
	CreatedAt, UpdatedAt                string
}

// JSON renders the row like list_mappings' dicts.
func (m Mapping) JSON() *jsonx.Object {
	return jsonx.NewObject("wp_id", m.WPID, "system", m.System, "workspace", m.Workspace, "external_id", m.ExternalID,
		"external_key", optPtr(m.ExternalKey), "external_url", optPtr(m.ExternalURL), "created_at", m.CreatedAt, "updated_at", m.UpdatedAt)
}

// UpsertMapping mirrors upsert_mapping (ON CONFLICT(wp_id, system) DO
// UPDATE; the UNIQUE(system, workspace, external_id) constraint raises
// sqlite3.IntegrityError).
func (s *Store) UpsertMapping(wpID string, ref ExternalRef) error {
	now := clock.NowUTCISO()
	return s.mutate("tracker_mappings", func(rows []sqlitedb.Row) ([]sqlitedb.Row, error) {
		target := -1
		for i, r := range rows {
			if col(r, 0) == wpID && col(r, 1) == ref.System {
				target = i
			}
		}
		var newVals []any
		if target >= 0 {
			old := rows[target].Values
			newVals = []any{col(rows[target], 0), col(rows[target], 1), ref.Workspace, ref.ID, optPtr(ref.Key), optPtr(ref.URL), old[6], now}
		} else {
			newVals = []any{wpID, ref.System, ref.Workspace, ref.ID, optPtr(ref.Key), optPtr(ref.URL), now, now}
		}
		for i, r := range rows {
			if i == target {
				continue
			}
			if col(r, 1) == newVals[1] && col(r, 2) == newVals[2] && col(r, 3) == newVals[3] {
				return nil, &Crash{Type: "IntegrityError", Msg: "UNIQUE constraint failed: tracker_mappings.system, tracker_mappings.workspace, tracker_mappings.external_id"}
			}
		}
		if target >= 0 {
			rows[target].Values = newVals
			return rows, nil
		}
		return append(rows, sqlitedb.Row{RowID: nextRowID(rows), Values: newVals}), nil
	})
}

// ListMappings returns all mappings ordered by (wp_id, system).
func (s *Store) ListMappings() ([]Mapping, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	rows := append([]sqlitedb.Row(nil), f.Rows("tracker_mappings")...)
	sort.SliceStable(rows, func(i, j int) bool {
		if c := sqlitedb.Compare(col(rows[i], 0), col(rows[j], 0)); c != 0 {
			return c < 0
		}
		return sqlitedb.Compare(col(rows[i], 1), col(rows[j], 1)) < 0
	})
	out := make([]Mapping, 0, len(rows))
	for _, r := range rows {
		m := Mapping{WPID: colStr(r, 0), System: colStr(r, 1), Workspace: colStr(r, 2), ExternalID: colStr(r, 3), CreatedAt: colStr(r, 6), UpdatedAt: colStr(r, 7)}
		if v := col(r, 4); v != nil {
			s := colStr(r, 4)
			m.ExternalKey = &s
		}
		if v := col(r, 5); v != nil {
			s := colStr(r, 5)
			m.ExternalURL = &s
		}
		out = append(out, m)
	}
	return out, nil
}
