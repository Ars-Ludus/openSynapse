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
	"github.com/Ars-Ludus/openSynapse/internal/models"
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
INSERT INTO code_files (file_id, path, language, dependencies, file_summary, file_size, last_modified, content_hash)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    language      = excluded.language,
    dependencies  = excluded.dependencies,
    file_summary  = excluded.file_summary,
    file_size     = excluded.file_size,
    last_modified = excluded.last_modified,
    content_hash  = excluded.content_hash`,
		f.FileID.String(), f.Path, string(f.Language),
		marshalStrings(f.Dependencies), f.FileSummary,
		f.FileSize, f.LastModified.Unix(), f.ContentHash,
	)
	return err
}

func (d *DB) UpdateFileSummary(ctx context.Context, fileID uuid.UUID, summary string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE code_files SET file_summary = ? WHERE file_id = ?`,
		summary, fileID.String())
	return err
}

func (d *DB) GetFileByID(ctx context.Context, fileID uuid.UUID) (*models.CodeFile, error) {
	row := d.sql.QueryRowContext(ctx, `
SELECT file_id, path, language, dependencies, file_summary, file_size, last_modified, content_hash
FROM code_files WHERE file_id = ?`, fileID.String())

	f := &models.CodeFile{}
	var fileIDStr, lang, deps string
	var lastMod int64
	err := row.Scan(&fileIDStr, &f.Path, &lang, &deps, &f.FileSummary, &f.FileSize, &lastMod, &f.ContentHash)
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

func (d *DB) GetFileByPath(ctx context.Context, path string) (*models.CodeFile, error) {
	row := d.sql.QueryRowContext(ctx, `
SELECT file_id, path, language, dependencies, file_summary, file_size, last_modified, content_hash
FROM code_files WHERE path = ?`, path)

	f := &models.CodeFile{}
	var fileIDStr, lang, deps string
	var lastMod int64
	err := row.Scan(&fileIDStr, &f.Path, &lang, &deps, &f.FileSummary, &f.FileSize, &lastMod, &f.ContentHash)
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

// GetFilesWithEdgesInto returns the file IDs of files that have outgoing edges
// targeting snippets belonging to the given file. This identifies files whose
// edges will break when the target file is re-indexed (snippets deleted + recreated).
func (d *DB) GetFilesWithEdgesInto(ctx context.Context, fileID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT DISTINCT src_s.file_id
FROM edges e
JOIN snippets src_s ON src_s.snippet_id = e.source_snippet_id
JOIN snippets dst_s ON dst_s.snippet_id = e.target_snippet_id
WHERE dst_s.file_id = ? AND src_s.file_id != ?`,
		fileID.String(), fileID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var idStr string
		if err := rows.Scan(&idStr); err != nil {
			return nil, err
		}
		id, _ := uuid.Parse(idStr)
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetContentHash returns the stored content hash for a file path, or "" if not found.
func (d *DB) GetContentHash(ctx context.Context, path string) (string, error) {
	var hash string
	err := d.sql.QueryRowContext(ctx,
		`SELECT content_hash FROM code_files WHERE path = ?`, path,
	).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return hash, err
}

func (d *DB) DeleteFileByPath(ctx context.Context, path string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM code_files WHERE path = ?`, path)
	return err
}

func (d *DB) ListFiles(ctx context.Context) ([]*models.CodeFile, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT file_id, path, language, dependencies, file_summary, file_size, last_modified, content_hash
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
		if err := rows.Scan(&fileIDStr, &f.Path, &lang, &deps, &f.FileSummary, &f.FileSize, &lastMod, &f.ContentHash); err != nil {
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
	metaJSON, _ := json.Marshal(s.Metadata)
	_, err := d.sql.ExecContext(ctx, `
INSERT INTO snippets
    (snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks, metadata, call_chain_summary, call_chain_summary)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(snippet_id) DO NOTHING`,
		s.SnippetID.String(), s.FileID.String(),
		string(s.SnippetType), s.Name,
		s.LineStart, s.LineEnd,
		s.RawContent, s.Description,
		marshalStrings(s.Wikilinks),
		string(metaJSON),
		s.CallChainSummary,
	)
	return err
}

func (d *DB) UpdateSnippetDescription(ctx context.Context, id uuid.UUID, desc string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE snippets SET description = ? WHERE snippet_id = ?`,
		desc, id.String())
	return err
}

func (d *DB) UpdateCallChainSummary(ctx context.Context, id uuid.UUID, summary string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE snippets SET call_chain_summary = ? WHERE snippet_id = ?`,
		summary, id.String())
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
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks, metadata, call_chain_summary
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
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks, metadata, call_chain_summary
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
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks, metadata, call_chain_summary
FROM snippets WHERE name = ? AND file_id != ?`, name, fileID.String())
	} else {
		rows, err = d.sql.QueryContext(ctx, `
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks, metadata, call_chain_summary
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
		var sidStr, fidStr, st, wikilinks, metaJSON string
		if err := rows.Scan(&sidStr, &fidStr, &st, &s.Name,
			&s.LineStart, &s.LineEnd, &s.RawContent, &s.Description, &wikilinks, &metaJSON, &s.CallChainSummary); err != nil {
			return nil, err
		}
		s.SnippetID, _ = uuid.Parse(sidStr)
		s.FileID, _ = uuid.Parse(fidStr)
		s.SnippetType = models.SnippetType(st)
		s.Wikilinks = unmarshalStrings(wikilinks)
		_ = json.Unmarshal([]byte(metaJSON), &s.Metadata)
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
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks, metadata, call_chain_summary
FROM snippets WHERE snippet_id = ?`, id.String())

	s := &models.Snippet{}
	var sidStr, fidStr, st, wikilinks, metaJSON string
	err := row.Scan(&sidStr, &fidStr, &st, &s.Name,
		&s.LineStart, &s.LineEnd, &s.RawContent, &s.Description, &wikilinks, &metaJSON, &s.CallChainSummary)
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
	_ = json.Unmarshal([]byte(metaJSON), &s.Metadata)
	return s, nil
}

// GetSnippetsByType returns all snippets of the given type.
func (d *DB) GetSnippetsByType(ctx context.Context, st models.SnippetType) ([]*models.Snippet, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT snippet_id, file_id, snippet_type, name, line_start, line_end, raw_content, description, wikilinks, metadata, call_chain_summary
FROM snippets WHERE snippet_type = ?`, string(st))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnippets(rows)
}

// ── Wikilink Edges ────────────────────────────────────────────────────────────

// WikilinkEdgeTarget holds the resolved snippet info for one edge endpoint.
type WikilinkEdgeTarget struct {
	SnippetID   string
	Name        string
	SnippetType string
	LineStart   int
	LineEnd     int
	FilePath    string
	FileID      string
}

// GetWikilinkEdgeTargets returns snippets that share an edge with snippetID
// where the connected snippet's name matches wikilink. Both directions of the
// edge are checked — outgoing (this snippet references wikilink) and incoming
// (wikilink references this snippet).
func (d *DB) GetWikilinkEdgeTargets(ctx context.Context, snippetID uuid.UUID, wikilink string) ([]WikilinkEdgeTarget, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT s.snippet_id, s.name, s.snippet_type, s.line_start, s.line_end, cf.path, cf.file_id
FROM edges e
JOIN snippets s    ON s.snippet_id   = e.target_snippet_id
JOIN code_files cf ON cf.file_id     = s.file_id
WHERE e.source_snippet_id = ? AND s.name = ?
UNION
SELECT s.snippet_id, s.name, s.snippet_type, s.line_start, s.line_end, cf.path, cf.file_id
FROM edges e
JOIN snippets s    ON s.snippet_id   = e.source_snippet_id
JOIN code_files cf ON cf.file_id     = s.file_id
WHERE e.target_snippet_id = ? AND s.name = ?`,
		snippetID.String(), wikilink,
		snippetID.String(), wikilink,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []WikilinkEdgeTarget
	for rows.Next() {
		var t WikilinkEdgeTarget
		if err := rows.Scan(&t.SnippetID, &t.Name, &t.SnippetType, &t.LineStart, &t.LineEnd, &t.FilePath, &t.FileID); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// ── Wikilink Colors ───────────────────────────────────────────────────────────

// GetWikilinkColor returns the persisted color for a (snippet_id, wikilink) pair.
// Returns "" if no color has been set (caller should default to "#ffffff").
func (d *DB) GetWikilinkColor(ctx context.Context, snippetID uuid.UUID, wikilink string) (string, error) {
	var color string
	err := d.sql.QueryRowContext(ctx,
		`SELECT color FROM wikilink_colors WHERE snippet_id = ? AND wikilink = ?`,
		snippetID.String(), wikilink,
	).Scan(&color)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return color, err
}

// SetWikilinkColor upserts a color for a (snippet_id, wikilink) pair.
func (d *DB) SetWikilinkColor(ctx context.Context, snippetID uuid.UUID, wikilink, color string) error {
	id := uuid.New()
	_, err := d.sql.ExecContext(ctx, `
INSERT INTO wikilink_colors (id, snippet_id, wikilink, color)
VALUES (?, ?, ?, ?)
ON CONFLICT(snippet_id, wikilink) DO UPDATE SET color = excluded.color`,
		id.String(), snippetID.String(), wikilink, color,
	)
	return err
}

// GetConnectedSnippetIDs returns all snippet IDs connected to snippetID via
// any edge in either direction. The source snippet itself is not included.
func (d *DB) GetConnectedSnippetIDs(ctx context.Context, snippetID uuid.UUID) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT target_snippet_id AS connected_id FROM edges WHERE source_snippet_id = ?
UNION
SELECT source_snippet_id AS connected_id FROM edges WHERE target_snippet_id = ?`,
		snippetID.String(), snippetID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetOutgoingEdgeTargetNames returns the distinct snippet names that snippetID
// points to via any outgoing edge. Used to prune wikilinks post-resolution so
// the column only holds symbols that are provably defined elsewhere.
func (d *DB) GetOutgoingEdgeTargetNames(ctx context.Context, snippetID uuid.UUID) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT DISTINCT s.name
FROM edges e
JOIN snippets s ON s.snippet_id = e.target_snippet_id
WHERE e.source_snippet_id = ?`,
		snippetID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// UpdateSnippetWikilinks replaces the wikilinks JSON array for a snippet.
func (d *DB) UpdateSnippetWikilinks(ctx context.Context, snippetID uuid.UUID, wikilinks []string) error {
	if wikilinks == nil {
		wikilinks = []string{}
	}
	_, err := d.sql.ExecContext(ctx,
		`UPDATE snippets SET wikilinks = ? WHERE snippet_id = ?`,
		marshalStrings(wikilinks), snippetID.String(),
	)
	return err
}

// UpsertLibSnippet finds or creates a synthetic CodeFile + Snippet representing
// a symbol exported by an external library (stdlib or third-party). These entries
// are used as edge targets so lib dependencies appear in the knowledge graph.
// The synthetic file path is prefixed with "lib:" to distinguish it from real files.
func (d *DB) UpsertLibSnippet(ctx context.Context, importPath, symbolName string) (*models.Snippet, error) {
	libPath := "lib:" + importPath

	// ── Find or create the synthetic CodeFile ────────────────────────────────
	var fileIDStr string
	err := d.sql.QueryRowContext(ctx,
		`SELECT file_id FROM code_files WHERE path = ?`, libPath,
	).Scan(&fileIDStr)

	var codeFileID uuid.UUID
	if errors.Is(err, sql.ErrNoRows) {
		codeFileID = uuid.New()
		_, insertErr := d.sql.ExecContext(ctx, `
INSERT INTO code_files (file_id, path, language, dependencies, file_size, last_modified, file_summary)
VALUES (?, ?, ?, '[]', 0, strftime('%s', 'now'), '')
ON CONFLICT(path) DO NOTHING`,
			codeFileID.String(), libPath, string(models.LangExternal),
		)
		if insertErr != nil {
			return nil, insertErr
		}
		// Re-fetch in case another goroutine won the ON CONFLICT race.
		_ = d.sql.QueryRowContext(ctx,
			`SELECT file_id FROM code_files WHERE path = ?`, libPath,
		).Scan(&fileIDStr)
		codeFileID, _ = uuid.Parse(fileIDStr)
	} else if err != nil {
		return nil, err
	} else {
		codeFileID, _ = uuid.Parse(fileIDStr)
	}

	// ── Find or create the synthetic Snippet ─────────────────────────────────
	var snippetIDStr string
	err = d.sql.QueryRowContext(ctx,
		`SELECT snippet_id FROM snippets WHERE file_id = ? AND name = ?`,
		codeFileID.String(), symbolName,
	).Scan(&snippetIDStr)

	if errors.Is(err, sql.ErrNoRows) {
		s := &models.Snippet{
			SnippetID:   uuid.New(),
			FileID:      codeFileID,
			Name:        symbolName,
			SnippetType: models.SnippetExternal,
			Wikilinks:   []string{},
		}
		if err := d.InsertSnippet(ctx, s); err != nil {
			// Another goroutine may have inserted it; re-fetch below.
			_ = d.sql.QueryRowContext(ctx,
				`SELECT snippet_id FROM snippets WHERE file_id = ? AND name = ?`,
				codeFileID.String(), symbolName,
			).Scan(&snippetIDStr)
			s.SnippetID, _ = uuid.Parse(snippetIDStr)
		}
		return s, nil
	} else if err != nil {
		return nil, err
	}

	sid, _ := uuid.Parse(snippetIDStr)
	return &models.Snippet{
		SnippetID:   sid,
		FileID:      codeFileID,
		Name:        symbolName,
		SnippetType: models.SnippetExternal,
		Wikilinks:   []string{},
	}, nil
}

// ── Patterns ─────────────────────────────────────────────────────────────────

// InsertPattern inserts a pattern and its snippet associations.
func (d *DB) InsertPattern(ctx context.Context, p *models.Pattern) error {
	if p.PatternID == uuid.Nil {
		p.PatternID = uuid.New()
	}
	var embBlob []byte
	if len(p.Embedding) > 0 {
		embBlob = encodeVec(p.Embedding)
	}
	_, err := d.sql.ExecContext(ctx, `
INSERT INTO patterns (pattern_id, name, description, pattern_type, embedding)
VALUES (?, ?, ?, ?, ?)`,
		p.PatternID.String(), p.Name, p.Description, p.PatternType, embBlob)
	if err != nil {
		return err
	}
	for _, sid := range p.SnippetIDs {
		_, err := d.sql.ExecContext(ctx, `
INSERT INTO pattern_snippets (pattern_id, snippet_id) VALUES (?, ?)
ON CONFLICT DO NOTHING`,
			p.PatternID.String(), sid.String())
		if err != nil {
			return err
		}
	}
	return nil
}

// ListPatterns returns all patterns with their snippet IDs populated.
func (d *DB) ListPatterns(ctx context.Context) ([]*models.Pattern, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT pattern_id, name, description, pattern_type FROM patterns ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var patterns []*models.Pattern
	for rows.Next() {
		p := &models.Pattern{}
		var pidStr string
		if err := rows.Scan(&pidStr, &p.Name, &p.Description, &p.PatternType); err != nil {
			return nil, err
		}
		p.PatternID, _ = uuid.Parse(pidStr)
		patterns = append(patterns, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Populate snippet IDs.
	for _, p := range patterns {
		sids, err := d.getPatternSnippetIDs(ctx, p.PatternID)
		if err != nil {
			return nil, err
		}
		p.SnippetIDs = sids
	}
	return patterns, nil
}

func (d *DB) getPatternSnippetIDs(ctx context.Context, patternID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT snippet_id FROM pattern_snippets WHERE pattern_id = ?`,
		patternID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var idStr string
		if err := rows.Scan(&idStr); err != nil {
			return nil, err
		}
		id, _ := uuid.Parse(idStr)
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteAllPatterns removes all patterns (for re-detection).
func (d *DB) DeleteAllPatterns(ctx context.Context) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM patterns`)
	return err
}

// ── Edges ─────────────────────────────────────────────────────────────────────

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
