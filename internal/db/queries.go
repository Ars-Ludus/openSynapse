package db

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"opensynapse/internal/models"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func marshalStrings(ss []string) string {
	if ss == nil {
		return "[]"
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

func unmarshalStrings(s string) []string {
	var ss []string
	_ = json.Unmarshal([]byte(s), &ss)
	if ss == nil {
		ss = []string{}
	}
	return ss
}

// encodeVec serialises a float32 slice to the raw little-endian IEEE 754 blob
// format that sqlite-vec's scalar functions (vec_distance_cosine, etc.) accept.
func encodeVec(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// ── Code Files ────────────────────────────────────────────────────────────────

func (d *DB) UpsertFile(ctx context.Context, f *models.CodeFile) error {
	if f.FileID == uuid.Nil {
		f.FileID = uuid.New()
	}
	_, err := d.sql.ExecContext(ctx, `
INSERT INTO code_files (file_id, path, language, dependencies, file_summary, file_size, last_modified)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    language      = excluded.language,
    dependencies  = excluded.dependencies,
    file_summary  = excluded.file_summary,
    file_size     = excluded.file_size,
    last_modified = excluded.last_modified`,
		f.FileID.String(), f.Path, string(f.Language),
		marshalStrings(f.Dependencies), f.FileSummary,
		f.FileSize, f.LastModified.Unix(),
	)
	return err
}

func (d *DB) UpdateFileSummary(ctx context.Context, fileID uuid.UUID, summary string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE code_files SET file_summary = ? WHERE file_id = ?`,
		summary, fileID.String())
	return err
}

func (d *DB) GetFileByPath(ctx context.Context, path string) (*models.CodeFile, error) {
	row := d.sql.QueryRowContext(ctx, `
SELECT file_id, path, language, dependencies, file_summary, file_size, last_modified
FROM code_files WHERE path = ?`, path)

	f := &models.CodeFile{}
	var fileIDStr, lang, deps string
	var lastMod int64
	err := row.Scan(&fileIDStr, &f.Path, &lang, &deps, &f.FileSummary, &f.FileSize, &lastMod)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.FileID, _ = uuid.Parse(fileIDStr)
	f.Language = models.Language(lang)
	f.Dependencies = unmarshalStrings(deps)
	f.LastModified = time.Unix(lastMod, 0).UTC()
	return f, nil
}

func (d *DB) DeleteFileByPath(ctx context.Context, path string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM code_files WHERE path = ?`, path)
	return err
}

func (d *DB) ListFiles(ctx context.Context) ([]*models.CodeFile, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT file_id, path, language, dependencies, file_summary, file_size, last_modified
FROM code_files ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*models.CodeFile
	for rows.Next() {
		f := &models.CodeFile{}
		var fileIDStr, lang, deps string
		var lastMod int64
		if err := rows.Scan(&fileIDStr, &f.Path, &lang, &deps, &f.FileSummary, &f.FileSize, &lastMod); err != nil {
			return nil, err
		}
		f.FileID, _ = uuid.Parse(fileIDStr)
		f.Language = models.Language(lang)
		f.Dependencies = unmarshalStrings(deps)
		f.LastModified = time.Unix(lastMod, 0).UTC()
		files = append(files, f)
	}
	return files, rows.Err()
}

// ── Snippets ──────────────────────────────────────────────────────────────────

func (d *DB) InsertSnippet(ctx context.Context, s *models.Snippet) error {
	if s.SnippetID == uuid.Nil {
		s.SnippetID = uuid.New()
	}
	_, err := d.sql.ExecContext(ctx, `
INSERT INTO snippets
    (snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks)
VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(snippet_id) DO NOTHING`,
		s.SnippetID.String(), s.FileID.String(),
		string(s.SnippetType), s.Name,
		s.LineStart, s.LineEnd,
		s.RawContent, s.Description,
		marshalStrings(s.Wikilinks),
	)
	return err
}

func (d *DB) UpdateSnippetDescription(ctx context.Context, id uuid.UUID, desc string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE snippets SET description = ? WHERE snippet_id = ?`,
		desc, id.String())
	return err
}

func (d *DB) UpdateSnippetEmbedding(ctx context.Context, id uuid.UUID, emb []float32) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE snippets SET embedding = ? WHERE snippet_id = ?`,
		encodeVec(emb), id.String())
	return err
}

func (d *DB) GetSnippetsByFile(ctx context.Context, fileID uuid.UUID) ([]*models.Snippet, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks
FROM snippets WHERE file_id = ? ORDER BY line_start`, fileID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnippets(rows)
}

// SearchByEmbedding performs cosine-distance search using sqlite-vec's scalar
// vec_distance_cosine function (linear scan; fast enough for typical codebases).
func (d *DB) SearchByEmbedding(ctx context.Context, vec []float32, limit int) ([]*models.Snippet, error) {
	if len(vec) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := d.sql.QueryContext(ctx, fmt.Sprintf(`
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks
FROM snippets
WHERE embedding IS NOT NULL
ORDER BY vec_distance_cosine(embedding, ?) ASC
LIMIT %d`, limit), encodeVec(vec))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnippets(rows)
}

func (d *DB) FindSnippetByName(ctx context.Context, name string, fileID *uuid.UUID) ([]*models.Snippet, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if fileID != nil {
		rows, err = d.sql.QueryContext(ctx, `
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks
FROM snippets WHERE name = ? AND file_id != ?`, name, fileID.String())
	} else {
		rows, err = d.sql.QueryContext(ctx, `
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks
FROM snippets WHERE name = ?`, name)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnippets(rows)
}

func scanSnippets(rows *sql.Rows) ([]*models.Snippet, error) {
	var snippets []*models.Snippet
	for rows.Next() {
		s := &models.Snippet{}
		var sidStr, fidStr, st, wikilinks string
		if err := rows.Scan(&sidStr, &fidStr, &st, &s.Name,
			&s.LineStart, &s.LineEnd, &s.RawContent, &s.Description, &wikilinks); err != nil {
			return nil, err
		}
		s.SnippetID, _ = uuid.Parse(sidStr)
		s.FileID, _ = uuid.Parse(fidStr)
		s.SnippetType = models.SnippetType(st)
		s.Wikilinks = unmarshalStrings(wikilinks)
		snippets = append(snippets, s)
	}
	return snippets, rows.Err()
}

// ── Edges ─────────────────────────────────────────────────────────────────────

func (d *DB) InsertEdge(ctx context.Context, e *models.Edge) error {
	if e.EdgeID == uuid.Nil {
		e.EdgeID = uuid.New()
	}
	_, err := d.sql.ExecContext(ctx, `
INSERT INTO edges (edge_id, source_snippet_id, target_snippet_id, edge_type, merged_context)
VALUES (?,?,?,?,?)
ON CONFLICT(source_snippet_id, target_snippet_id, edge_type) DO NOTHING`,
		e.EdgeID.String(), e.SourceSnippetID.String(), e.TargetSnippetID.String(),
		string(e.EdgeType), e.MergedContext)
	return err
}

func (d *DB) EdgeExists(ctx context.Context, src, dst uuid.UUID, edgeType models.EdgeType) (bool, error) {
	var exists bool
	err := d.sql.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM edges
    WHERE source_snippet_id = ? AND target_snippet_id = ? AND edge_type = ?
)`, src.String(), dst.String(), string(edgeType)).Scan(&exists)
	return exists, err
}

func (d *DB) GetEdgesForSnippet(ctx context.Context, snippetID uuid.UUID) ([]*models.Edge, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT edge_id, source_snippet_id, target_snippet_id, edge_type, merged_context
FROM edges WHERE source_snippet_id = ? OR target_snippet_id = ?`,
		snippetID.String(), snippetID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// GetDependencies returns outgoing edges — what this snippet calls or references.
func (d *DB) GetDependencies(ctx context.Context, snippetID uuid.UUID) ([]*models.Edge, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT edge_id, source_snippet_id, target_snippet_id, edge_type, merged_context
FROM edges WHERE source_snippet_id = ?`, snippetID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// GetDependents returns incoming edges — what calls or references this snippet.
func (d *DB) GetDependents(ctx context.Context, snippetID uuid.UUID) ([]*models.Edge, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT edge_id, source_snippet_id, target_snippet_id, edge_type, merged_context
FROM edges WHERE target_snippet_id = ?`, snippetID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// GetSnippetByID returns a single snippet by primary key, or nil if not found.
func (d *DB) GetSnippetByID(ctx context.Context, id uuid.UUID) (*models.Snippet, error) {
	row := d.sql.QueryRowContext(ctx, `
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks
FROM snippets WHERE snippet_id = ?`, id.String())

	s := &models.Snippet{}
	var sidStr, fidStr, st, wikilinks string
	err := row.Scan(&sidStr, &fidStr, &st, &s.Name,
		&s.LineStart, &s.LineEnd, &s.RawContent, &s.Description, &wikilinks)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.SnippetID, _ = uuid.Parse(sidStr)
	s.FileID, _ = uuid.Parse(fidStr)
	s.SnippetType = models.SnippetType(st)
	s.Wikilinks = unmarshalStrings(wikilinks)
	return s, nil
}

func scanEdges(rows *sql.Rows) ([]*models.Edge, error) {
	var edges []*models.Edge
	for rows.Next() {
		e := &models.Edge{}
		var eidStr, srcStr, dstStr, et string
		if err := rows.Scan(&eidStr, &srcStr, &dstStr, &et, &e.MergedContext); err != nil {
			return nil, err
		}
		e.EdgeID, _ = uuid.Parse(eidStr)
		e.SourceSnippetID, _ = uuid.Parse(srcStr)
		e.TargetSnippetID, _ = uuid.Parse(dstStr)
		e.EdgeType = models.EdgeType(et)
		edges = append(edges, e)
	}
	return edges, rows.Err()
}
