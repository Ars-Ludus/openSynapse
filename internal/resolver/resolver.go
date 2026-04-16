// Package resolver implements Phase 2: Namespace Resolution.
// It matches symbols referenced in one snippet to their definitions in another
// and creates Edges to represent those cross-file relationships.
package resolver

import (
	"context"
	"log"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"opensynapse/internal/db"
	"opensynapse/internal/models"
)

// Resolver builds edges between snippets based on import & symbol analysis.
type Resolver struct {
	db *db.DB
}

func New(database *db.DB) *Resolver {
	return &Resolver{db: database}
}

// ResolveFile creates import-call edges for a newly indexed file.
// For each import the file declares, it looks for snippets in the imported
// file and creates an edge from every snippet in the current file that
// references the imported symbol.
func (r *Resolver) ResolveFile(ctx context.Context, file *models.CodeFile, snippets []*models.Snippet) error {
	for _, imp := range file.Dependencies {
		// Try to find a code_file whose path ends with the import.
		// This is a best-effort heuristic for same-repo imports.
		targetFile, err := r.findFileByImport(ctx, imp, file.Path)
		if err != nil || targetFile == nil {
			continue
		}

		targetSnippets, err := r.db.GetSnippetsByFile(ctx, targetFile.FileID)
		if err != nil || len(targetSnippets) == 0 {
			continue
		}

		// Build a name→snippet map for the target file.
		targetByName := make(map[string]*models.Snippet, len(targetSnippets))
		for _, ts := range targetSnippets {
			if ts.Name != "" {
				targetByName[ts.Name] = ts
			}
		}

		// For each snippet in the current file, check if its wikilinks contain
		// an exported symbol from the target file.
		for _, src := range snippets {
			for _, link := range src.Wikilinks {
				dst, ok := targetByName[link]
				if !ok {
					continue
				}
				exists, err := r.db.EdgeExists(ctx, src.SnippetID, dst.SnippetID, models.EdgeImportCall)
				if err != nil || exists {
					continue
				}
				edge := &models.Edge{
					EdgeID:          uuid.New(),
					SourceSnippetID: src.SnippetID,
					TargetSnippetID: dst.SnippetID,
					EdgeType:        models.EdgeImportCall,
				}
				if err := r.db.InsertEdge(ctx, edge); err != nil {
					log.Printf("resolver: insert edge: %v", err)
				}
			}
		}
	}
	return nil
}

// findFileByImport looks up a code_file whose path best matches the import string.
func (r *Resolver) findFileByImport(ctx context.Context, imp, callerPath string) (*models.CodeFile, error) {
	// Strip leading "./" and "../" patterns for relative imports.
	callerDir := filepath.Dir(callerPath)
	candidates := []string{
		imp,
		filepath.Join(callerDir, imp),
	}

	all, err := r.db.ListFiles(ctx)
	if err != nil {
		return nil, err
	}

	// Check each candidate against the file list.
	for _, candidate := range candidates {
		// Normalise separators.
		candidate = filepath.ToSlash(candidate)
		for _, f := range all {
			fPath := filepath.ToSlash(f.Path)
			if strings.HasSuffix(fPath, candidate) || strings.Contains(fPath, candidate) {
				if f.Path != callerPath {
					return f, nil
				}
			}
		}
	}
	return nil, nil
}
