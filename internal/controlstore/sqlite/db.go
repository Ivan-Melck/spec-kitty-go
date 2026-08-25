package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	_ "modernc.org/sqlite"
)

type Store struct {
	db       *sql.DB
	tokenKey []byte
}

func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("canonicalize control database: %w", err)
	}
	query := url.Values{}
	query.Add("_pragma", "journal_mode=WAL")
	query.Add("_pragma", "foreign_keys=1")
	query.Add("_pragma", "busy_timeout=5000")
	query.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: query.Encode()}).String()

	db, err := openDatabase(ctx, dsn)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	version, legacy, claims, err := inspectSchemaState(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if version > currentSchemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("control database schema version %d is newer than supported version %d", version, currentSchemaVersion)
	}
	store.tokenKey, err = loadOrCreateTokenKey(ctx, abs, version == 0 || legacy || claims == 0)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(ctx, db, store.tokenKey); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate control database: %w", err)
	}
	settings, err := store.Settings(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if settings.JournalMode != "wal" || !settings.ForeignKeys || settings.BusyTimeout != 5000 {
		_ = db.Close()
		return nil, fmt.Errorf("control database settings mismatch: %+v", settings)
	}
	return store, nil
}

func openDatabase(ctx context.Context, dsn string) (*sql.DB, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		db, err := sql.Open("sqlite", dsn)
		if err == nil {
			db.SetMaxOpenConns(8)
			err = db.PingContext(ctx)
		}
		if err == nil {
			return db, nil
		}
		if db != nil {
			_ = db.Close()
		}
		if !isSQLiteBusy(err) {
			return nil, fmt.Errorf("open control database: %w", err)
		}
		if time.Now().After(deadline) {
			diagnostic := execution.NewDiagnostic(execution.KindControlStoreBusy, "control-store startup remained busy after bounded wait", execution.RetrySafeSameOperation, "", "", nil, map[string]string{"control_store": "busy"}, map[string]string{"control_store": "open"}, time.Now().UTC())
			diagnostic.Cause = err
			return nil, diagnostic
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func inspectSchemaState(ctx context.Context, db *sql.DB) (version int, legacy bool, claims int, err error) {
	if err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, false, 0, fmt.Errorf("read control database schema version: %w", err)
	}
	if version == 1 {
		legacy, err = tableHasColumn(ctx, db, "leases", "token")
		if err != nil {
			return 0, false, 0, err
		}
	}
	hasOperations, err := tableHasColumn(ctx, db, "operations", "operation_id")
	if err != nil {
		return 0, false, 0, err
	}
	if hasOperations {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations`).Scan(&claims); err != nil {
			return 0, false, 0, fmt.Errorf("inspect control-store token state: %w", err)
		}
	}
	return version, legacy, claims, nil
}

func loadOrCreateTokenKey(ctx context.Context, databasePath string, allowCreate bool) ([]byte, error) {
	keyPath := databasePath + ".token-key"
	key, err := os.ReadFile(keyPath)
	if err == nil {
		if len(key) != 32 {
			return waitForTokenKey(ctx, keyPath)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read control-store token key: %w", err)
	}
	if !allowCreate {
		return nil, fmt.Errorf("control-store token key is missing for a database containing authority state")
	}
	key = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate control-store token key: %w", err)
	}
	file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return waitForTokenKey(ctx, keyPath)
		}
		return nil, fmt.Errorf("create control-store token key: %w", err)
	}
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write control-store token key: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sync control-store token key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close control-store token key: %w", err)
	}
	directory, err := os.Open(filepath.Dir(keyPath))
	if err != nil {
		return nil, fmt.Errorf("open control-store token key directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return nil, fmt.Errorf("sync control-store token key directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return nil, fmt.Errorf("close control-store token key directory: %w", err)
	}
	return key, nil
}

func waitForTokenKey(ctx context.Context, keyPath string) ([]byte, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		key, err := os.ReadFile(keyPath)
		if err == nil && len(key) == 32 {
			return key, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read concurrently created control-store token key: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("concurrently created control-store token key did not become readable")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Settings(ctx context.Context) (controlstore.Settings, error) {
	var settings controlstore.Settings
	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&settings.JournalMode); err != nil {
		return settings, fmt.Errorf("read journal_mode: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return settings, fmt.Errorf("read foreign_keys: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&settings.BusyTimeout); err != nil {
		return settings, fmt.Errorf("read busy_timeout: %w", err)
	}
	settings.JournalMode = strings.ToLower(settings.JournalMode)
	settings.ForeignKeys = foreignKeys == 1
	return settings, nil
}
