// Package pipeline orchestrates the three-phase ingestion pipeline:
//   1. Crawl & Parse  (AST → Snippets)
//   2. Resolve        (cross-file Edges)
//   3. Enrich         (LLM summaries + embeddings)
package pipeline

import (
	"context"
	"log"
	"os"
	"sync"

	"opensynapse/internal/crawler"
	"opensynapse/internal/db"
	"opensynapse/internal/enrichment"
	"opensynapse/internal/models"
	"opensynapse/internal/parser"
	"opensynapse/internal/resolver"
)

// Pipeline wires the crawl → parse → resolve → enrich stages together.
type Pipeline struct {
	db       *db.DB
	llm      *enrichment.LLM
	embedder enrichment.Embedder
	resolver *resolver.Resolver
	sem      chan struct{} // limits concurrent file processing
}

// New creates a Pipeline.
func New(database *db.DB, llm *enrichment.LLM, embedder enrichment.Embedder, maxConcurrency int) *Pipeline {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	sem := make(chan struct{}, maxConcurrency)
	for i := 0; i < maxConcurrency; i++ {
		sem <- struct{}{}
	}
	return &Pipeline{
		db:       database,
		llm:      llm,
		embedder: embedder,
		resolver: resolver.New(database),
		sem:      sem,
	}
}

// IndexDir walks root and indexes every source file found.
func (p *Pipeline) IndexDir(ctx context.Context, root string) error {
	files, err := crawler.Walk(root)
	if err != nil {
		return err
	}
	log.Printf("pipeline: found %d source files in %s", len(files), root)

	var wg sync.WaitGroup
	for _, fi := range files {
		fi := fi
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-p.sem
			defer func() { p.sem <- struct{}{} }()

			if err := p.IndexFile(ctx, fi.Path); err != nil {
				log.Printf("pipeline: index %s: %v", fi.Path, err)
			}
		}()
	}
	wg.Wait()
	return nil
}

// IndexFile runs the full pipeline for a single file.
// Re-indexing a file purges its old snippets and edges first (idempotent).
func (p *Pipeline) IndexFile(ctx context.Context, path string) error {
	content, err := crawler.ReadFile(path)
	if err != nil {
		return err
	}
	if content == nil {
		return nil // file skipped (too large)
	}

	lang := crawler.DetectLanguage(path)

	// ── Phase 1: Parse ─────────────────────────────────────────────────────
	parsed, err := parser.Parse(ctx, lang, content)
	if err != nil {
		return err
	}

	stat, err := os.Stat(path)
	if err != nil {
		return err
	}

	// Purge old data so re-indexing is idempotent (CASCADE handles snippets/edges).
	if err := p.db.DeleteFileByPath(ctx, path); err != nil {
		return err
	}

	codeFile := &models.CodeFile{
		Path:         path,
		Language:     lang,
		Dependencies: parsed.Imports,
		FileSize:     stat.Size(),
		LastModified: stat.ModTime().UTC(),
	}
	if err := p.db.UpsertFile(ctx, codeFile); err != nil {
		return err
	}

	// Insert parsed snippets.
	snippets := make([]*models.Snippet, 0, len(parsed.Snippets))
	for _, ps := range parsed.Snippets {
		s := &models.Snippet{
			FileID:      codeFile.FileID,
			LineStart:   ps.LineStart,
			LineEnd:     ps.LineEnd,
			Wikilinks:   ps.Wikilinks,
			RawContent:  ps.RawContent,
			SnippetType: ps.SnippetType,
			Name:        ps.Name,
		}
		if err := p.db.InsertSnippet(ctx, s); err != nil {
			log.Printf("pipeline: insert snippet %q: %v", ps.Name, err)
			continue
		}
		snippets = append(snippets, s)
	}

	// ── Phase 2: Resolve ────────────────────────────────────────────────────
	if err := p.resolver.ResolveFile(ctx, codeFile, snippets); err != nil {
		log.Printf("pipeline: resolve %s: %v", path, err)
	}

	// ── Phase 3: Enrich ─────────────────────────────────────────────────────
	p.enrich(ctx, codeFile, snippets)

	log.Printf("pipeline: indexed %s (%d snippets)", path, len(snippets))
	return nil
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
		log.Printf("pipeline: llm file summary %s: %v", codeFile.Path, err)
	} else if summary != "" {
		codeFile.FileSummary = summary
		_ = p.db.UpdateFileSummary(ctx, codeFile.FileID, summary)
	}

	// Per-snippet description + embedding.
	for _, s := range snippets {
		desc, err := p.llm.SummariseSnippet(ctx, s, codeFile.Path)
		if err != nil {
			log.Printf("pipeline: llm snippet %q: %v", s.Name, err)
		} else if desc != "" {
			s.Description = desc
			if err := p.db.UpdateSnippetDescription(ctx, s.SnippetID, desc); err != nil {
				log.Printf("pipeline: update description: %v", err)
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
			log.Printf("pipeline: embed snippet %q: %v", s.Name, err)
		} else if len(emb) > 0 {
			if err := p.db.UpdateSnippetEmbedding(ctx, s.SnippetID, emb); err != nil {
				log.Printf("pipeline: update embedding: %v", err)
			}
		}
	}
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

