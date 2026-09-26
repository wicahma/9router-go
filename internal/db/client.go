package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

var (
	dbInstance *sql.DB
	dbOnce     sync.Once
	initErr    error
)

// OpenDatabase opens a SQLite database and configures it with WAL mode, normal synchronous mode,
// and other safe concurrency / performance defaults matching the Node/Bun implementation.
func OpenDatabase(path string) (*sql.DB, error) {
	// Ensure the parent directory of the database file exists
	dbDir := filepath.Dir(path)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("create db dir %s: %w", dbDir, err)
	}
	// The DB stores provider API keys/tokens in plaintext, so keep the file
	// and its directory private to the owning user.
	_ = os.Chmod(dbDir, 0700)

	// PRAGMAs that must hold on EVERY pooled connection (busy_timeout,
	// foreign_keys, journal_mode, synchronous) go in the DSN: database/sql
	// pools connections, and db.Exec() only ever reaches one of them, leaving
	// the rest without busy_timeout — those return SQLITE_BUSY immediately
	// under concurrent writes (usage inserts) instead of waiting.
	// temp_store/mmap_size/cache_size are read-only after connect, so they
	// stay here.
	dsn := path + "?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open(%s): %w", path, err)
	}

	if _, err = db.Exec(`
PRAGMA temp_store = MEMORY;
PRAGMA mmap_size = 30000000;
PRAGMA cache_size = -64000;
`); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma exec: %w", err)
	}

	// Restrict the DB file to the owning user (it stores plaintext keys).
	// The file is created by the driver on first open; chmod it now and
	// again on every open to re-assert the permission.
	if err := os.Chmod(path, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, fmt.Errorf("chmod db file %s: %w", path, err)
	}

	// Pool limits: readers must not queue behind writers. Each chat request
	// reads connections/settings; usage logging writes request details. With a
	// 4-connection cap a 7-way parallel burst starved callers into unbounded
	// waits inside database/sql.
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	db.SetConnMaxLifetime(0)

	return db, nil
}

// InitGlobalDatabase initializes the global database connection instance.
func InitGlobalDatabase(path string) error {
	dbOnce.Do(func() {
		dbInstance, initErr = OpenDatabase(path)
	})
	return initErr
}

// GetConnection returns the global database connection.
func GetConnection() (*sql.DB, error) {
	if dbInstance == nil {
		return nil, errors.New("database not initialized, call InitGlobalDatabase first")
	}
	return dbInstance, nil
}
