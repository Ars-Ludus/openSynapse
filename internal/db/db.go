package db

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func init() {
	// Register a custom driver that exposes vec_distance_cosine as a SQL scalar
	// function. This gives us the same interface as sqlite-vec without needing
	// the shared extension to be present at runtime.
	sql.Register("sqlite3_vec", &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return conn.RegisterFunc("vec_distance_cosine", vecDistanceCosine, true)
		},
	})
}

// vecDistanceCosine computes cosine distance (1 – cosine similarity) between
// two raw little-endian IEEE 754 float32 blobs.
func vecDistanceCosine(a, b []byte) float64 {
	n := len(a) / 4
	if n == 0 || len(a) != len(b) {
		return 1.0
	}
	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		fa := float64(math.Float32frombits(binary.LittleEndian.Uint32(a[i*4:])))
		fb := float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:])))
		dot += fa * fb
		normA += fa * fa
		normB += fb * fb
	}
	if normA == 0 || normB == 0 {
		return 1.0
	}
	return 1.0 - dot/(math.Sqrt(normA)*math.Sqrt(normB))
}

// DB wraps *sql.DB and provides schema migration and typed query access.
type DB struct {
	sql *sql.DB
	Dim int
}

// New opens (or creates) a SQLite database at path with the vec driver.
func New(_ context.Context, path string, embedDim int) (*DB, error) {
	db, err := sql.Open("sqlite3_vec", path+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &DB{sql: db, Dim: embedDim}, nil
}

// Migrate creates all required tables and indexes if they do not exist.
func (d *DB) Migrate(_ context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS code_files (
    file_id       TEXT    PRIMARY KEY,
    path          TEXT    NOT NULL UNIQUE,
    language      TEXT    NOT NULL DEFAULT 'unknown',
    dependencies  TEXT    NOT NULL DEFAULT '[]',
    file_summary  TEXT    NOT NULL DEFAULT '',
    file_size     INTEGER NOT NULL DEFAULT 0,
    last_modified INTEGER NOT NULL DEFAULT 0
)`,
		`CREATE TABLE IF NOT EXISTS snippets (
    snippet_id   TEXT    PRIMARY KEY,
    file_id      TEXT    NOT NULL REFERENCES code_files(file_id) ON DELETE CASCADE,
    snippet_type TEXT    NOT NULL DEFAULT 'unknown',
    name         TEXT    NOT NULL DEFAULT '',
    line_start   INTEGER NOT NULL DEFAULT 0,
    line_end     INTEGER NOT NULL DEFAULT 0,
    raw_content  TEXT    NOT NULL DEFAULT '',
    description  TEXT    NOT NULL DEFAULT '',
    wikilinks    TEXT    NOT NULL DEFAULT '[]',
    embedding    BLOB
)`,
		`CREATE INDEX IF NOT EXISTS idx_snippets_file_id ON snippets(file_id)`,
		`CREATE TABLE IF NOT EXISTS edges (
    edge_id           TEXT PRIMARY KEY,
    source_snippet_id TEXT NOT NULL REFERENCES snippets(snippet_id) ON DELETE CASCADE,
    target_snippet_id TEXT NOT NULL REFERENCES snippets(snippet_id) ON DELETE CASCADE,
    edge_type         TEXT NOT NULL,
    merged_context    TEXT NOT NULL DEFAULT '',
    UNIQUE(source_snippet_id, target_snippet_id, edge_type)
)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_source ON edges(source_snippet_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target_snippet_id)`,
		`CREATE INDEX IF NOT EXISTS idx_code_files_path ON code_files(path)`,
	}
	for _, s := range stmts {
		if _, err := d.sql.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Close releases the database connection.
func (d *DB) Close() {
	d.sql.Close()
}
