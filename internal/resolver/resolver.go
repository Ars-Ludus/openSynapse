// Package resolver implements Phase 2: Namespace Resolution.
// It matches symbols referenced in one snippet to their definitions in another
// and creates Edges to represent those cross-file relationships.
//
// Two passes are performed:
//   A. Internal — wikilinks matched against snippets in imported repo files.
//   B. External — selector_expression refs matched against import qualifiers to
//      create edges toward synthetic library snippets (lib:<import-path>).
package resolver

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/Ars-Ludus/openSynapse/internal/db"
	"github.com/Ars-Ludus/openSynapse/internal/models"
	"github.com/Ars-Ludus/openSynapse/internal/parser"
)

// Resolver builds edges between snippets based on import & symbol analysis.
type Resolver struct {
	db *db.DB
}

func New(database *db.DB) *Resolver {
	return &Resolver{db: database}
}

// ResolveFile creates edges for a newly indexed file.
//
// Pass A (internal): for each import that resolves to an indexed CodeFile,
// check whether any snippet wikilink matches a snippet name in that file.
//
// Pass B (external): for each ExternalRef (selector expression) in a snippet,
// look up its qualifier in importSpecs. If the import doesn't resolve to an
// internal file, create a synthetic lib snippet and an edge to it.
func (r *Resolver) ResolveFile(
	ctx context.Context,
	file *models.CodeFile,
	snippets []*models.Snippet,
	snippetRefs map[uuid.UUID][]parser.ExternalRef, // snippetID → external refs
	importSpecs []parser.ImportSpec,
) error {
	// Build qualifier → import path map from the specs returned by the parser.
	qualifierToPath := make(map[string]string, len(importSpecs))
	for _, spec := range importSpecs {
		qualifierToPath[spec.Qualifier] = spec.Path
	}

	// Track which import paths resolved to internal (indexed) files.
	internalPaths := make(map[string]bool)

	// ── Pass A: internal resolution ──────────────────────────────────────────
	for _, imp := range file.Dependencies {
		targetFile, err := r.findFileByImport(ctx, imp, file.Path)
		if err != nil || targetFile == nil {
			continue
		}
		internalPaths[imp] = true

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
					slog.Error("resolver: insert internal edge", "err", err)
				}
			}
		}
	}

	// ── Pass B: external/lib resolution ──────────────────────────────────────
	for _, src := range snippets {
		refs, ok := snippetRefs[src.SnippetID]
		if !ok || len(refs) == 0 {
			continue
		}
		for _, ref := range refs {
			importPath, known := qualifierToPath[ref.Qualifier]
			if !known {
				continue // qualifier is not a package import (e.g. a local var)
			}
			if internalPaths[importPath] {
				continue // already handled via internal resolution
			}
			libSnippet, err := r.db.UpsertLibSnippet(ctx, importPath, ref.Symbol)
			if err != nil {
				slog.Error("resolver: upsert lib snippet", "import", importPath, "symbol", ref.Symbol, "err", err)
				continue
			}
			exists, err := r.db.EdgeExists(ctx, src.SnippetID, libSnippet.SnippetID, models.EdgeImportCall)
			if err != nil || exists {
				continue
			}
			edge := &models.Edge{
				EdgeID:          uuid.New(),
				SourceSnippetID: src.SnippetID,
				TargetSnippetID: libSnippet.SnippetID,
				EdgeType:        models.EdgeImportCall,
			}
			if err := r.db.InsertEdge(ctx, edge); err != nil {
				slog.Error("resolver: insert lib edge", "import", importPath, "symbol", ref.Symbol, "err", err)
			}
		}
	}

	return nil
}

// ResolveInterfaces finds Go interfaces and matches them to concrete struct
// types whose method sets satisfy the interface. Creates EdgeTypeDefinition
// edges from the struct snippet to the interface snippet.
func (r *Resolver) ResolveInterfaces(ctx context.Context) error {
	// Get all interface snippets.
	interfaces, err := r.db.GetSnippetsByType(ctx, models.SnippetInterface)
	if err != nil {
		return err
	}
	if len(interfaces) == 0 {
		return nil
	}

	// Get all method snippets and group by receiver type.
	methods, err := r.db.GetSnippetsByType(ctx, models.SnippetMethod)
	if err != nil {
		return err
	}

	// receiverMethods: receiver type name → set of method names
	receiverMethods := make(map[string]map[string]bool)
	// receiverSnippets: receiver type name → struct snippet (looked up by name)
	for _, m := range methods {
		recv := m.Metadata.Receiver
		if recv == "" {
			continue
		}
		if receiverMethods[recv] == nil {
			receiverMethods[recv] = make(map[string]bool)
		}
		receiverMethods[recv][m.Name] = true
	}

	// For each interface, check which receiver types satisfy its method set.
	for _, iface := range interfaces {
		requiredMethods := iface.Metadata.InterfaceMethods
		if len(requiredMethods) == 0 {
			continue
		}

		for recvType, methodSet := range receiverMethods {
			// Check if methodSet is a superset of requiredMethods.
			satisfies := true
			for _, req := range requiredMethods {
				if !methodSet[req] {
					satisfies = false
					break
				}
			}
			if !satisfies {
				continue
			}

			// Find the struct snippet for this receiver type.
			structSnippets, err := r.db.FindSnippetByName(ctx, recvType, nil)
			if err != nil || len(structSnippets) == 0 {
				continue
			}
			for _, structSnip := range structSnippets {
				if structSnip.SnippetType != models.SnippetStruct {
					continue
				}
				exists, err := r.db.EdgeExists(ctx, structSnip.SnippetID, iface.SnippetID, models.EdgeTypeDefinition)
				if err != nil || exists {
					continue
				}
				edge := &models.Edge{
					EdgeID:          uuid.New(),
					SourceSnippetID: structSnip.SnippetID,
					TargetSnippetID: iface.SnippetID,
					EdgeType:        models.EdgeTypeDefinition,
				}
				if err := r.db.InsertEdge(ctx, edge); err != nil {
					slog.Error("resolver: insert interface impl edge",
						"struct", recvType, "interface", iface.Name, "err", err)
				} else {
					slog.Info("resolver: interface implementation",
						"struct", recvType, "implements", iface.Name)
				}
			}
		}
	}

	return nil
}

// findFileByImport looks up a code_file whose path best matches the import string.
func (r *Resolver) findFileByImport(ctx context.Context, imp, callerPath string) (*models.CodeFile, error) {
	callerDir := filepath.Dir(callerPath)
	candidates := []string{
		imp,
		filepath.Join(callerDir, imp),
	}
	// For Go module imports ("modulename/pkg/sub"), strip the module name so
	// "github.com/Ars-Ludus/openSynapse/internal/crawler" also tries "internal/crawler". This lets
	// the resolver match indexed files whose paths are relative to the repo root.
	if idx := strings.Index(imp, "/"); idx >= 0 {
		candidates = append(candidates, imp[idx+1:])
	}

	all, err := r.db.ListFiles(ctx)
	if err != nil {
		return nil, err
	}

	for _, candidate := range candidates {
		candidate = filepath.ToSlash(candidate)
		for _, f := range all {
			// Never match synthetic lib: stubs — only real indexed source files.
			if f.Path == callerPath || strings.HasPrefix(f.Path, "lib:") {
				continue
			}
			fPath := filepath.ToSlash(f.Path)
			// Use path-segment–aware matching to avoid "internal/crawler" matching
			// "internal/crawler_utils". A file belongs to the candidate package if
			// its directory path equals candidate (with a leading or trailing slash
			// separator as context).
			if strings.HasPrefix(fPath, candidate+"/") ||
				strings.Contains(fPath, "/"+candidate+"/") ||
				strings.HasSuffix(fPath, "/"+candidate) {
					return f, nil
			}
		}
	}
	return nil, nil
}
