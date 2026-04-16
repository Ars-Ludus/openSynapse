// Package service is the canonical implementation of all oSyn tool operations.
// HTTP handlers, MCP tools, and CLI query commands are all thin wrappers around
// the functions defined here. New surfaces should consume this package directly
// rather than calling db or pipeline packages themselves.
package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"opensynapse/internal/db"
	"opensynapse/internal/models"
	"opensynapse/internal/pipeline"
)

// Service holds the two runtime dependencies shared by all tool operations.
// DB and Pipeline are exported so CLI commands that need low-level access
// (e.g. IndexDir for the watch loop) can reach them without another wrapper.
type Service struct {
	DB       *db.DB
	Pipeline *pipeline.Pipeline
}

func New(database *db.DB, pl *pipeline.Pipeline) *Service {
	return &Service{DB: database, Pipeline: pl}
}

// ── Result types ──────────────────────────────────────────────────────────────

// FileDetail is the response type for DescribeFile.
// Snippets omit raw_content and embedding to keep the payload compact;
// callers that need full source should follow up with GetSnippet.
type FileDetail struct {
	File     *models.CodeFile   `json:"file"`
	Snippets []*SnippetSummary  `json:"snippets"`
}

// SnippetSummary is a lightweight view of a snippet without its raw source blob.
type SnippetSummary struct {
	SnippetID   string             `json:"snippet_id"`
	FileID      string             `json:"file_id"`
	Name        string             `json:"name"`
	SnippetType models.SnippetType `json:"snippet_type"`
	LineStart   int                `json:"line_start"`
	LineEnd     int                `json:"line_end"`
	Description string             `json:"description"`
	Wikilinks   []string           `json:"wikilinks,omitempty"`
}

// ResolvedEdge pairs an edge with the resolved partner snippet (summary only).
type ResolvedEdge struct {
	EdgeType      models.EdgeType `json:"edge_type"`
	MergedContext string          `json:"merged_context,omitempty"`
	Snippet       *SnippetSummary `json:"snippet"`
}

// BlastRadius is the response type for GetBlastRadius.
type BlastRadius struct {
	Snippet          *models.Snippet  `json:"snippet"`
	Dependents       []*ResolvedEdge  `json:"dependents"`
	Dependencies     []*ResolvedEdge  `json:"dependencies"`
	BlastRadiusCount int              `json:"blast_radius_count"`
}

// DependencyResult is the response type for GetDependencies.
type DependencyResult struct {
	Snippet      *models.Snippet `json:"snippet"`
	Dependencies []*ResolvedEdge `json:"dependencies"`
}

// ── Tool implementations ──────────────────────────────────────────────────────

// DescribeFile returns a file's metadata and a compact listing of all its
// snippets (no raw source). Use GetSnippet to fetch full source for individual
// snippets. Returns nil, nil when the path is not in the index.
func (s *Service) DescribeFile(ctx context.Context, path string) (*FileDetail, error) {
	f, err := s.DB.GetFileByPath(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("describe_file: %w", err)
	}
	if f == nil {
		return nil, nil
	}

	snippets, err := s.DB.GetSnippetsByFile(ctx, f.FileID)
	if err != nil {
		return nil, fmt.Errorf("describe_file snippets: %w", err)
	}

	summaries := make([]*SnippetSummary, len(snippets))
	for i, sn := range snippets {
		summaries[i] = toSummary(sn)
	}

	return &FileDetail{File: f, Snippets: summaries}, nil
}

// GetSnippet returns the full snippet (including raw source) for a given UUID
// string. Returns nil, nil when not found.
func (s *Service) GetSnippet(ctx context.Context, snippetID string) (*models.Snippet, error) {
	id, err := uuid.Parse(snippetID)
	if err != nil {
		return nil, fmt.Errorf("invalid snippet_id %q: %w", snippetID, err)
	}
	sn, err := s.DB.GetSnippetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get_snippet: %w", err)
	}
	return sn, nil
}

// GetBlastRadius returns a snippet, every snippet that directly depends on it
// (callers), and every snippet it directly depends on (callees). The
// BlastRadiusCount field is len(Dependents) — a quick signal for how
// cautiously to treat a change to this symbol.
func (s *Service) GetBlastRadius(ctx context.Context, snippetID string) (*BlastRadius, error) {
	id, err := uuid.Parse(snippetID)
	if err != nil {
		return nil, fmt.Errorf("invalid snippet_id %q: %w", snippetID, err)
	}

	sn, err := s.DB.GetSnippetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get_blast_radius: %w", err)
	}
	if sn == nil {
		return nil, nil
	}

	rawDeps, err := s.DB.GetDependencies(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get_blast_radius dependencies: %w", err)
	}
	rawCallers, err := s.DB.GetDependents(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get_blast_radius dependents: %w", err)
	}

	deps, err := s.resolveEdges(ctx, rawDeps, func(e *models.Edge) uuid.UUID { return e.TargetSnippetID })
	if err != nil {
		return nil, err
	}
	callers, err := s.resolveEdges(ctx, rawCallers, func(e *models.Edge) uuid.UUID { return e.SourceSnippetID })
	if err != nil {
		return nil, err
	}

	return &BlastRadius{
		Snippet:          sn,
		Dependents:       callers,
		Dependencies:     deps,
		BlastRadiusCount: len(callers),
	}, nil
}

// Search performs semantic (vector) search over all indexed snippets.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]*models.Snippet, error) {
	if limit <= 0 {
		limit = 5
	}
	results, err := s.Pipeline.Search(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return results, nil
}

// ListFiles returns all source files currently in the index.
func (s *Service) ListFiles(ctx context.Context) ([]*models.CodeFile, error) {
	files, err := s.DB.ListFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list_files: %w", err)
	}
	return files, nil
}

// GetDependencies returns a snippet and all snippets it directly calls or
// references (outgoing edges). For the full bi-directional impact analysis
// including callers, use GetBlastRadius.
func (s *Service) GetDependencies(ctx context.Context, snippetID string) (*DependencyResult, error) {
	id, err := uuid.Parse(snippetID)
	if err != nil {
		return nil, fmt.Errorf("invalid snippet_id %q: %w", snippetID, err)
	}

	sn, err := s.DB.GetSnippetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get_dependencies: %w", err)
	}
	if sn == nil {
		return nil, nil
	}

	raw, err := s.DB.GetDependencies(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get_dependencies edges: %w", err)
	}

	resolved, err := s.resolveEdges(ctx, raw, func(e *models.Edge) uuid.UUID { return e.TargetSnippetID })
	if err != nil {
		return nil, err
	}

	return &DependencyResult{Snippet: sn, Dependencies: resolved}, nil
}

// ReindexFile triggers re-indexing of a single file path.
func (s *Service) ReindexFile(ctx context.Context, path string) error {
	if err := s.Pipeline.IndexFile(ctx, path); err != nil {
		return fmt.Errorf("reindex: %w", err)
	}
	return nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

func toSummary(s *models.Snippet) *SnippetSummary {
	return &SnippetSummary{
		SnippetID:   s.SnippetID.String(),
		FileID:      s.FileID.String(),
		Name:        s.Name,
		SnippetType: s.SnippetType,
		LineStart:   s.LineStart,
		LineEnd:     s.LineEnd,
		Description: s.Description,
		Wikilinks:   s.Wikilinks,
	}
}

// resolveEdges looks up the partner snippet for each edge and returns
// ResolvedEdge values. partnerID extracts the correct UUID from each edge.
func (s *Service) resolveEdges(ctx context.Context, edges []*models.Edge, partnerID func(*models.Edge) uuid.UUID) ([]*ResolvedEdge, error) {
	result := make([]*ResolvedEdge, 0, len(edges))
	for _, e := range edges {
		partner, err := s.DB.GetSnippetByID(ctx, partnerID(e))
		if err != nil {
			return nil, fmt.Errorf("resolve edge %s: %w", e.EdgeID, err)
		}
		var summary *SnippetSummary
		if partner != nil {
			summary = toSummary(partner)
		}
		result = append(result, &ResolvedEdge{
			EdgeType:      e.EdgeType,
			MergedContext: e.MergedContext,
			Snippet:       summary,
		})
	}
	return result, nil
}
