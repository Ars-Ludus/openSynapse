// Package pipeline orchestrates the three-phase ingestion pipeline:
//  1. Crawl & Parse  (AST → Snippets)
//  2. Resolve        (cross-file Edges)
//  3. Enrich         (LLM summaries + embeddings)
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/Ars-Ludus/openSynapse/internal/crawler"
	"github.com/Ars-Ludus/openSynapse/internal/db"
	"github.com/Ars-Ludus/openSynapse/internal/enrichment"
	"github.com/Ars-Ludus/openSynapse/internal/models"
	"github.com/Ars-Ludus/openSynapse/internal/parser"
	"github.com/Ars-Ludus/openSynapse/internal/resolver"
)

// Pipeline wires the crawl → parse → resolve → enrich stages together.
type Pipeline struct {
	db       *db.DB
	llm      *enrichment.LLM
	embedder enrichment.Embedder
	resolver *resolver.Resolver
	sem      chan struct{} // limits concurrent file processing
	root     string       // absolute path to the repo root (for resolving relative paths)
}

// New creates a Pipeline. root is the absolute path to the repo directory —
// all file paths stored in the DB are relative to this root.
func New(database *db.DB, llm *enrichment.LLM, embedder enrichment.Embedder, maxConcurrency int, root string) *Pipeline {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	sem := make(chan struct{}, maxConcurrency)
	for i := 0; i < maxConcurrency; i++ {
		sem <- struct{}{}
	}
	absRoot, _ := filepath.Abs(root)
	return &Pipeline{
		db:       database,
		llm:      llm,
		embedder: embedder,
		resolver: resolver.New(database),
		sem:      sem,
		root:     absRoot,
	}
}

// Root returns the absolute repo root path.
func (p *Pipeline) Root() string { return p.root }

// parsedFile holds in-memory parse results from phase 1 of IndexDir.
// Resolution is deferred until all files are stable in the DB.
type parsedFile struct {
	codeFile    *models.CodeFile
	snippets    []*models.Snippet
	snippetRefs map[uuid.UUID][]parser.ExternalRef
	importSpecs []parser.ImportSpec
}

// IndexDir walks root and indexes every source file found.
//
// Two-phase execution prevents a race condition where the resolver for file A
// can't find file B because B is mid-deletion in a concurrent goroutine:
//   Phase 1 (concurrent): parse every file, delete-and-reinsert its DB record.
//   Phase 2 (sequential): resolve cross-file edges once all files are stable.
// absPath resolves a repo-relative path to an absolute path using the pipeline root.
func (p *Pipeline) absPath(relPath string) string {
	return filepath.Join(p.root, filepath.FromSlash(relPath))
}

func (p *Pipeline) IndexDir(ctx context.Context, root string) error {
	// If a root is passed, use it; otherwise use the pipeline's stored root.
	if root == "" || root == "." {
		root = p.root
	}
	files, err := crawler.Walk(root)
	if err != nil {
		return err
	}
	slog.Info("pipeline: found source files", "count", len(files), "root", root)

	// ── Phase 1: concurrent parse + insert ───────────────────────────────────
	results := make([]*parsedFile, len(files))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for i, fi := range files {
		i, fi := i, fi
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-p.sem
			defer func() { p.sem <- struct{}{} }()

			pf, err := p.parseAndInsert(ctx, fi.Path)
			if err != nil {
				slog.Error("pipeline: index file", "path", fi.Path, "err", err)
				return
			}
			if pf != nil {
				mu.Lock()
				results[i] = pf
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// ── Phase 2: sequential resolve + prune + enrich ─────────────────────────
	// All files are now stable in the DB, so cross-file lookups are reliable.
	for _, pf := range results {
		if pf == nil {
			continue
		}
		if err := p.resolver.ResolveFile(ctx, pf.codeFile, pf.snippets, pf.snippetRefs, pf.importSpecs); err != nil {
			slog.Error("pipeline: resolve", "path", pf.codeFile.Path, "err", err)
		}
		for _, s := range pf.snippets {
			resolved, err := p.db.GetOutgoingEdgeTargetNames(ctx, s.SnippetID)
			if err != nil {
				slog.Error("pipeline: prune wikilinks", "name", s.Name, "err", err)
				continue
			}
			if err := p.db.UpdateSnippetWikilinks(ctx, s.SnippetID, resolved); err != nil {
				slog.Error("pipeline: update wikilinks", "name", s.Name, "err", err)
			}
		}
		p.enrich(ctx, pf.codeFile, pf.snippets)
		slog.Info("pipeline: indexed", "path", pf.codeFile.Path, "snippets", len(pf.snippets))
	}

	// ── Phase 3: interface-to-implementation resolution ──────────────────────
	if err := p.resolver.ResolveInterfaces(ctx); err != nil {
		slog.Error("pipeline: resolve interfaces", "err", err)
	}

	return nil
}

// DeleteFile removes a file and all its snippets/edges from the database.
// CASCADE handles snippet and edge cleanup automatically.
func (p *Pipeline) DeleteFile(ctx context.Context, path string) error {
	return p.db.DeleteFileByPath(ctx, path)
}

// IndexFile runs the full pipeline for a single file (repo-relative path).
// Re-indexing a file purges its old snippets and edges first (idempotent).
// After re-indexing, dependent files whose edges pointed into this file's old
// snippets are re-resolved so their edges stay accurate.
func (p *Pipeline) IndexFile(ctx context.Context, path string) error {
	// Capture files that have edges INTO this file BEFORE we delete old data.
	var dependentFileIDs []uuid.UUID
	if existing, err := p.db.GetFileByPath(ctx, path); err == nil && existing != nil {
		dependentFileIDs, _ = p.db.GetFilesWithEdgesInto(ctx, existing.FileID)
	}

	pf, err := p.parseAndInsert(ctx, path)
	if err != nil || pf == nil {
		return err
	}

	if err := p.resolver.ResolveFile(ctx, pf.codeFile, pf.snippets, pf.snippetRefs, pf.importSpecs); err != nil {
		slog.Error("pipeline: resolve", "path", path, "err", err)
	}

	for _, s := range pf.snippets {
		resolved, err := p.db.GetOutgoingEdgeTargetNames(ctx, s.SnippetID)
		if err != nil {
			slog.Error("pipeline: prune wikilinks", "name", s.Name, "err", err)
			continue
		}
		if err := p.db.UpdateSnippetWikilinks(ctx, s.SnippetID, resolved); err != nil {
			slog.Error("pipeline: update wikilinks", "name", s.Name, "err", err)
		}
	}

	p.enrich(ctx, pf.codeFile, pf.snippets)
	slog.Info("pipeline: indexed", "path", path, "snippets", len(pf.snippets))

	// Re-resolve dependent files: their edges into this file's old snippets were
	// CASCADE-deleted. Re-parse (cheap) and re-resolve (no re-enrichment needed).
	for _, depFileID := range dependentFileIDs {
		p.reResolveFile(ctx, depFileID)
	}

	return nil
}

// reResolveFile re-parses a file and re-resolves its edges without re-enriching.
// Used after a dependency target has been re-indexed.
func (p *Pipeline) reResolveFile(ctx context.Context, fileID uuid.UUID) {
	depFile, err := p.db.GetFileByID(ctx, fileID)
	if err != nil || depFile == nil {
		return
	}

	content, err := crawler.ReadFile(p.absPath(depFile.Path))
	if err != nil || content == nil {
		return
	}

	parsed, err := parser.Parse(ctx, depFile.Language, content)
	if err != nil {
		slog.Error("pipeline: re-parse for cascade", "path", depFile.Path, "err", err)
		return
	}

	snippets, err := p.db.GetSnippetsByFile(ctx, fileID)
	if err != nil || len(snippets) == 0 {
		return
	}

	// Build snippetRefs from the fresh parse, matching by name+line to existing snippet IDs.
	snippetRefs := make(map[uuid.UUID][]parser.ExternalRef)
	snippetByKey := make(map[string]*models.Snippet)
	for _, s := range snippets {
		key := fmt.Sprintf("%s:%d", s.Name, s.LineStart)
		snippetByKey[key] = s
	}
	for _, ps := range parsed.Snippets {
		key := fmt.Sprintf("%s:%d", ps.Name, ps.LineStart)
		if s, ok := snippetByKey[key]; ok && len(ps.ExternalRefs) > 0 {
			snippetRefs[s.SnippetID] = ps.ExternalRefs
		}
	}

	if err := p.resolver.ResolveFile(ctx, depFile, snippets, snippetRefs, parsed.ImportSpecs); err != nil {
		slog.Error("pipeline: cascade resolve", "path", depFile.Path, "err", err)
		return
	}

	// Update wikilinks for the re-resolved snippets.
	for _, s := range snippets {
		resolved, err := p.db.GetOutgoingEdgeTargetNames(ctx, s.SnippetID)
		if err != nil {
			continue
		}
		_ = p.db.UpdateSnippetWikilinks(ctx, s.SnippetID, resolved)
	}

	slog.Info("pipeline: cascade re-resolved", "path", depFile.Path)
}

// parseAndInsert is phase 1: parse a file and insert its records into the DB.
// path is repo-relative (e.g. "internal/db/db.go").
func (p *Pipeline) parseAndInsert(ctx context.Context, path string) (*parsedFile, error) {
	absPath := p.absPath(path)
	content, err := crawler.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	if content == nil {
		return nil, nil // file skipped (too large)
	}

	// Content hash — skip re-indexing if file content is unchanged.
	hash := sha256Hex(content)
	if existing, err := p.db.GetContentHash(ctx, path); err == nil && existing != "" && existing == hash {
		slog.Debug("pipeline: content unchanged, skipping", "path", path)
		return nil, nil
	}

	lang := crawler.DetectLanguage(path)
	parsed, err := parser.Parse(ctx, lang, content)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}

	// Purge old data so re-indexing is idempotent (CASCADE handles snippets/edges).
	if err := p.db.DeleteFileByPath(ctx, path); err != nil {
		return nil, err
	}

	codeFile := &models.CodeFile{
		Path:         path, // stored as repo-relative
		Language:     lang,
		Dependencies: parsed.Imports,
		FileSize:     stat.Size(),
		LastModified: stat.ModTime().UTC(),
		ContentHash:  hash,
	}
	if err := p.db.UpsertFile(ctx, codeFile); err != nil {
		return nil, err
	}

	snippets := make([]*models.Snippet, 0, len(parsed.Snippets))
	snippetRefs := make(map[uuid.UUID][]parser.ExternalRef, len(parsed.Snippets))

	for _, ps := range parsed.Snippets {
		s := &models.Snippet{
			FileID:      codeFile.FileID,
			LineStart:   ps.LineStart,
			LineEnd:     ps.LineEnd,
			Wikilinks:   ps.Wikilinks,
			RawContent:  ps.RawContent,
			SnippetType: ps.SnippetType,
			Name:        ps.Name,
			Metadata:    ps.Metadata,
		}
		if err := p.db.InsertSnippet(ctx, s); err != nil {
			slog.Error("pipeline: insert snippet", "name", ps.Name, "err", err)
			continue
		}
		snippets = append(snippets, s)
		if len(ps.ExternalRefs) > 0 {
			snippetRefs[s.SnippetID] = ps.ExternalRefs
		}
	}

	return &parsedFile{
		codeFile:    codeFile,
		snippets:    snippets,
		snippetRefs: snippetRefs,
		importSpecs: parsed.ImportSpecs,
	}, nil
}

// enrich runs LLM summarisation and embedding generation for a file's snippets.
func (p *Pipeline) enrich(ctx context.Context, codeFile *models.CodeFile, snippets []*models.Snippet) {
	// Collect snippet names for the file-level summary.
	names := make([]string, 0, len(snippets))
	for _, s := range snippets {
		if s.Name != "" {
			names = append(names, s.Name)
		}
	}

	// File summary.
	summary, err := p.llm.SummariseFile(ctx, codeFile, names)
	if err != nil {
		slog.Error("pipeline: llm file summary", "path", codeFile.Path, "err", err)
	} else if summary != "" {
		codeFile.FileSummary = summary
		_ = p.db.UpdateFileSummary(ctx, codeFile.FileID, summary)
	}

	// Per-snippet description + embedding.
	for _, s := range snippets {
		desc, err := p.llm.SummariseSnippet(ctx, s, codeFile.Path)
		if err != nil {
			slog.Error("pipeline: llm snippet", "name", s.Name, "err", err)
		} else if desc != "" {
			s.Description = desc
			if err := p.db.UpdateSnippetDescription(ctx, s.SnippetID, desc); err != nil {
				slog.Error("pipeline: update description", "err", err)
			}
		}

		// Use the LLM description for embedding, falling back to raw content.
		text := s.Description
		if text == "" {
			text = s.RawContent
		}
		if len(text) > 2000 {
			text = text[:2000]
		}

		emb, err := p.embedder.Embed(ctx, text)
		if err != nil {
			slog.Error("pipeline: embed snippet", "name", s.Name, "err", err)
		} else if len(emb) > 0 {
			if err := p.db.UpdateSnippetEmbedding(ctx, s.SnippetID, emb); err != nil {
				slog.Error("pipeline: update embedding", "err", err)
			}
		}
	}
}

// Enrich iterates over all already-indexed files and fills in missing LLM
// descriptions without re-parsing or re-embedding. Pass force=true to
// overwrite descriptions that already exist.
func (p *Pipeline) Enrich(ctx context.Context, force bool) error {
	files, err := p.db.ListFiles(ctx)
	if err != nil {
		return err
	}
	slog.Info("enrich: files to process", "count", len(files))

	var (
		wg            sync.WaitGroup
		filesEnriched int
		snipsEnriched int
		mu            sync.Mutex
	)

	for _, f := range files {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-p.sem
			defer func() { p.sem <- struct{}{} }()

			snippets, err := p.db.GetSnippetsByFile(ctx, f.FileID)
			if err != nil {
				slog.Error("enrich: get snippets", "path", f.Path, "err", err)
				return
			}

			// File summary.
			if force || f.FileSummary == "" {
				names := make([]string, 0, len(snippets))
				for _, s := range snippets {
					if s.Name != "" {
						names = append(names, s.Name)
					}
				}
				slog.Info("enrich: requesting file summary", "path", f.Path)
				summary, err := p.llm.SummariseFile(ctx, f, names)
				if err != nil {
					slog.Error("enrich: file summary", "path", f.Path, "err", err)
				} else if summary == "" {
					slog.Warn("enrich: empty file summary", "path", f.Path)
				} else {
					words := len(strings.Fields(summary))
					if err := p.db.UpdateFileSummary(ctx, f.FileID, summary); err != nil {
						slog.Error("enrich: save file summary", "path", f.Path, "err", err)
					} else {
						mu.Lock()
						filesEnriched++
						mu.Unlock()
						slog.Info("enrich: file summary ok", "words", words, "path", f.Path)
					}
				}
			} else {
				slog.Debug("enrich: skip file (already summarised)", "path", f.Path)
			}

			// Per-snippet descriptions.
			for _, s := range snippets {
				if !force && s.Description != "" {
					slog.Debug("enrich: skip snippet (already described)", "path", f.Path, "name", s.Name)
					continue
				}
				slog.Info("enrich: requesting snippet description",
					"path", f.Path, "name", s.Name,
					"line_start", s.LineStart, "line_end", s.LineEnd)
				desc, err := p.llm.SummariseSnippet(ctx, s, f.Path)
				if err != nil {
					slog.Error("enrich: snippet description", "path", f.Path, "name", s.Name, "err", err)
					continue
				}
				if desc == "" {
					slog.Warn("enrich: empty snippet description", "path", f.Path, "name", s.Name)
					continue
				}
				words := len(strings.Fields(desc))
				if err := p.db.UpdateSnippetDescription(ctx, s.SnippetID, desc); err != nil {
					slog.Error("enrich: save snippet description", "path", f.Path, "name", s.Name, "err", err)
				} else {
					mu.Lock()
					snipsEnriched++
					mu.Unlock()
					slog.Info("enrich: snippet description ok", "words", words, "path", f.Path, "name", s.Name)
				}
			}
		}()
	}

	wg.Wait()
	slog.Info("enrich: done", "file_summaries", filesEnriched, "snippet_descriptions", snipsEnriched)
	return nil
}

// EnrichCallChains generates LLM summaries of execution paths for snippets
// that have outgoing edges. Each summary describes what happens when the
// snippet is called, following the call chain up to maxDepth levels.
func (p *Pipeline) EnrichCallChains(ctx context.Context, maxDepth int) error {
	if maxDepth <= 0 {
		maxDepth = 3
	}

	files, err := p.db.ListFiles(ctx)
	if err != nil {
		return err
	}

	var enriched int
	for _, f := range files {
		if strings.HasPrefix(f.Path, "lib:") {
			continue
		}
		snippets, err := p.db.GetSnippetsByFile(ctx, f.FileID)
		if err != nil {
			continue
		}
		for _, s := range snippets {
			if s.SnippetType != models.SnippetFunction && s.SnippetType != models.SnippetMethod {
				continue
			}
			if s.CallChainSummary != "" {
				continue // already has one
			}

			chain := p.walkCallChain(ctx, s.SnippetID, maxDepth, make(map[uuid.UUID]bool))
			if len(chain) == 0 {
				continue
			}

			summary, err := p.llm.SummariseCallChain(ctx, s, chain)
			if err != nil {
				slog.Error("pipeline: call chain summary", "name", s.Name, "err", err)
				continue
			}
			if summary == "" {
				continue
			}

			if err := p.db.UpdateCallChainSummary(ctx, s.SnippetID, summary); err != nil {
				slog.Error("pipeline: save call chain", "name", s.Name, "err", err)
				continue
			}
			enriched++
			slog.Info("pipeline: call chain enriched", "name", s.Name)
		}
	}

	slog.Info("pipeline: call chains done", "enriched", enriched)
	return nil
}

// walkCallChain performs a BFS/DFS walk of outgoing edges up to maxDepth,
// collecting the target snippets in call order.
func (p *Pipeline) walkCallChain(ctx context.Context, rootID uuid.UUID, maxDepth int, visited map[uuid.UUID]bool) []*models.Snippet {
	if maxDepth <= 0 {
		return nil
	}
	visited[rootID] = true

	edges, err := p.db.GetDependencies(ctx, rootID)
	if err != nil || len(edges) == 0 {
		return nil
	}

	var chain []*models.Snippet
	for _, e := range edges {
		if visited[e.TargetSnippetID] {
			continue
		}
		target, err := p.db.GetSnippetByID(ctx, e.TargetSnippetID)
		if err != nil || target == nil {
			continue
		}
		chain = append(chain, target)
		// Recurse into target's dependencies.
		chain = append(chain, p.walkCallChain(ctx, e.TargetSnippetID, maxDepth-1, visited)...)
	}
	return chain
}

// DetectPatterns runs structural grouping over the indexed graph, then uses
// the LLM to filter and name meaningful patterns. Results are stored in the
// patterns table (previous patterns are cleared first).
func (p *Pipeline) DetectPatterns(ctx context.Context) error {
	candidates, err := enrichment.DetectCandidates(ctx, p.db)
	if err != nil {
		return fmt.Errorf("detect candidates: %w", err)
	}
	slog.Info("patterns: candidates found", "count", len(candidates))

	// Clear previous patterns.
	if err := p.db.DeleteAllPatterns(ctx); err != nil {
		return fmt.Errorf("clear patterns: %w", err)
	}

	var patternsCreated int
	for _, c := range candidates {
		name, desc, err := p.llm.SummarisePattern(ctx, enrichment.PatternSummaryInput{
			GroupLabel: c.Label,
			Snippets:  c.Snippets,
		})
		if err != nil {
			slog.Error("patterns: llm", "label", c.Label, "err", err)
			continue
		}
		if name == "" {
			slog.Debug("patterns: llm rejected candidate", "label", c.Label)
			continue
		}

		pattern := &models.Pattern{
			Name:        name,
			Description: desc,
			PatternType: c.Type,
			SnippetIDs:  c.SnippetIDs,
		}

		// Generate embedding for the pattern description.
		if desc != "" {
			emb, err := p.embedder.Embed(ctx, name+": "+desc)
			if err == nil && len(emb) > 0 {
				pattern.Embedding = emb
			}
		}

		if err := p.db.InsertPattern(ctx, pattern); err != nil {
			slog.Error("patterns: insert", "name", name, "err", err)
			continue
		}
		patternsCreated++
		slog.Info("patterns: detected", "name", name, "members", len(c.SnippetIDs))
	}

	slog.Info("patterns: done", "total", patternsCreated)
	return nil
}

// Search performs cosine-similarity search against all embedded snippets.
func (p *Pipeline) Search(ctx context.Context, query string, limit int) ([]*models.Snippet, error) {
	// LocalEmbedder distinguishes query vs document embeddings via a prefix.
	type queryEmbedder interface {
		EmbedQuery(ctx context.Context, text string) ([]float32, error)
	}
	var vec []float32
	var err error
	if qe, ok := p.embedder.(queryEmbedder); ok {
		vec, err = qe.EmbedQuery(ctx, query)
	} else {
		vec, err = p.embedder.Embed(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	return p.db.SearchByEmbedding(ctx, vec, limit)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
