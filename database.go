package hnslop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const cacheSchema = `
CREATE TABLE IF NOT EXISTS proxy_cache (
    url TEXT PRIMARY KEY,
    status_code INTEGER NOT NULL,
    content_type TEXT NOT NULL,
    body BLOB NOT NULL,
    fetched_at TEXT NOT NULL
);
`

// CachedResponse is an upstream response stored without inspecting or
// transforming its body.
type CachedResponse struct {
	URL         string
	StatusCode  int
	ContentType string
	Body        []byte
	FetchedAt   string
}

// Database owns the SQLite connection used by the permanent response cache.
type Database struct {
	connection *sql.DB
	path       string
}

// OpenDatabase creates the parent directory, opens SQLite, and initializes the
// cache schema.
func OpenDatabase(path string) (*Database, error) {
	if path == "" {
		return nil, errors.New("database path must not be empty")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		parent := filepath.Dir(path)
		if parent != "." {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return nil, fmt.Errorf("create database directory %q: %w", parent, err)
			}
		}
	}

	dsn := path
	if path == ":memory:" {
		// A named shared in-memory database remains available across the one
		// connection in this pool and makes the database useful in tests.
		dsn = "file:hnslop-memory?mode=memory&cache=shared"
	}
	connection, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// SQLite permits many readers but only one writer. A single pooled
	// connection keeps schema initialization and short cache transactions
	// predictable while upstream requests happen outside the database.
	connection.SetMaxOpenConns(1)
	connection.SetMaxIdleConns(1)

	database := &Database{connection: connection, path: path}
	if err := database.initialize(context.Background()); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return database, nil
}

func (database *Database) initialize(ctx context.Context) error {
	if _, err := database.connection.ExecContext(ctx, "PRAGMA busy_timeout = 30000"); err != nil {
		return fmt.Errorf("configure sqlite busy timeout: %w", err)
	}
	if database.path != ":memory:" {
		if _, err := database.connection.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
			return fmt.Errorf("configure sqlite journal mode: %w", err)
		}
	}
	if _, err := database.connection.ExecContext(ctx, cacheSchema); err != nil {
		return fmt.Errorf("initialize sqlite schema: %w", err)
	}
	return nil
}

// Close releases the SQLite connection.
func (database *Database) Close() error {
	if database == nil || database.connection == nil {
		return nil
	}
	return database.connection.Close()
}

// GetCachedResponse returns the response for url, if it has been seen before.
func (database *Database) GetCachedResponse(ctx context.Context, url string) (*CachedResponse, error) {
	var response CachedResponse
	if err := database.connection.QueryRowContext(
		ctx,
		`SELECT url, status_code, content_type, body, fetched_at
         FROM proxy_cache WHERE url = ?`,
		url,
	).Scan(&response.URL, &response.StatusCode, &response.ContentType, &response.Body, &response.FetchedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cached response: %w", err)
	}
	response.Body = append([]byte(nil), response.Body...)
	return &response, nil
}

// DeleteCachedResponse removes the response for url, if one exists.
func (database *Database) DeleteCachedResponse(ctx context.Context, url string) error {
	_, err := database.connection.ExecContext(
		ctx,
		`DELETE FROM proxy_cache WHERE url = ?`,
		url,
	)
	if err != nil {
		return fmt.Errorf("delete cached response: %w", err)
	}
	return nil
}

// SaveCachedResponse stores or replaces a response. The body is kept as a
// BLOB so the proxy never has to interpret upstream content.
func (database *Database) SaveCachedResponse(ctx context.Context, response CachedResponse) error {
	_, err := database.connection.ExecContext(
		ctx,
		`INSERT INTO proxy_cache (url, status_code, content_type, body, fetched_at)
         VALUES (?, ?, ?, ?, ?)
         ON CONFLICT(url) DO UPDATE SET
             status_code = excluded.status_code,
             content_type = excluded.content_type,
             body = excluded.body,
             fetched_at = excluded.fetched_at`,
		response.URL,
		response.StatusCode,
		response.ContentType,
		response.Body,
		response.FetchedAt,
	)
	if err != nil {
		return fmt.Errorf("save cached response: %w", err)
	}
	return nil
}
