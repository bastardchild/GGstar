// Package sessionstore persists Fiber sessions in SQLite.
//
// Fiber's official sqlite3 storage depends on mattn/go-sqlite3, which requires
// cgo and would break the CGO_ENABLED=0 static build. This implementation uses
// modernc.org/sqlite (the same pure-Go driver the rest of ggstar uses) and
// satisfies fiber.Storage directly.
package sessionstore

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	_ "modernc.org/sqlite"
)

// Store is a fiber.Storage backed by a SQLite table.
type Store struct {
	db     *sql.DB
	ownsDB bool
	stop   chan struct{}
	once   sync.Once
}

var _ fiber.Storage = (*Store)(nil)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	session_id TEXT PRIMARY KEY,
	data       BLOB    NOT NULL,
	expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions (expires_at);
`

// New opens (or reuses) a SQLite file for sessions and starts a janitor that
// removes expired rows.
func New(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping session store: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate session store: %w", err)
	}

	s := &Store{db: db, ownsDB: true, stop: make(chan struct{})}
	go s.janitor()
	return s, nil
}

// NewFromDB reuses an existing pool. The caller keeps ownership, so Close only
// stops the janitor.
func NewFromDB(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate session store: %w", err)
	}

	s := &Store{db: db, stop: make(chan struct{})}
	go s.janitor()
	return s, nil
}

func (s *Store) janitor() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			_, _ = s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC())
		}
	}
}

// Get returns nil, nil when the key is absent or expired, matching fiber.Storage
// semantics: Fiber treats a nil slice as a cache miss.
func (s *Store) Get(key string) ([]byte, error) {
	var (
		data      []byte
		expiresAt time.Time
	)

	err := s.db.QueryRow(`SELECT data, expires_at FROM sessions WHERE session_id = ?`, key).
		Scan(&data, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if !expiresAt.After(time.Now().UTC()) {
		_ = s.Delete(key)
		return nil, nil
	}
	return data, nil
}

func (s *Store) Set(key string, val []byte, exp time.Duration) error {
	if key == "" || len(val) == 0 {
		return nil
	}

	// 0 means "no expiration"; store a far-future timestamp so the janitor and
	// the expiry check stay uniform.
	expiresAt := time.Now().UTC().Add(exp)
	if exp <= 0 {
		expiresAt = time.Now().UTC().AddDate(10, 0, 0)
	}

	_, err := s.db.Exec(`
INSERT INTO sessions (session_id, data, expires_at) VALUES (?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET data = excluded.data, expires_at = excluded.expires_at`,
		key, val, expiresAt)
	return err
}

func (s *Store) Delete(key string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE session_id = ?`, key)
	return err
}

func (s *Store) Reset() error {
	_, err := s.db.Exec(`DELETE FROM sessions`)
	return err
}

func (s *Store) Close() error {
	s.once.Do(func() { close(s.stop) })

	if !s.ownsDB {
		return nil
	}
	return s.db.Close()
}

// Len reports the number of live sessions. Used by tests.
func (s *Store) Len() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE expires_at > ?`, time.Now().UTC()).Scan(&n)
	return n, err
}
