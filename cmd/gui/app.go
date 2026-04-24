package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/Ars-Ludus/openSynapse/internal/config"
	"github.com/Ars-Ludus/openSynapse/internal/db"
	"github.com/Ars-Ludus/openSynapse/internal/enrichment"
	"github.com/Ars-Ludus/openSynapse/internal/models"
	"github.com/Ars-Ludus/openSynapse/internal/pipeline"
	"github.com/Ars-Ludus/openSynapse/internal/service"
)

// ── View types (JSON-serialised over the Wails bridge) ────────────────────────

// FileNode is one entry in the sidebar directory tree.
// Children is non-nil (possibly empty) for directories; nil for files.
type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`    // full path; empty for directory nodes
	FileID   string     `json:"file_id"` // UUID string; empty for directory nodes
	IsDir    bool       `json:"is_dir"`
	Children []FileNode `json:"children,omitempty"`
}

// SnippetView is the per-snippet row shown in the Snippet Assembly tab.
type SnippetView struct {
	SnippetID   string   `json:"snippet_id"`
	Name        string   `json:"name"`
	SnippetType string   `json:"snippet_type"`
	LineStart   int      `json:"line_start"`
	LineEnd     int      `json:"line_end"`
	Description string   `json:"description"`
	Wikilinks   []string `json:"wikilinks"`
}

// WikilinkEdgeInfo is returned by GetWikilinkEdges for each connected snippet.
type WikilinkEdgeInfo struct {
	SnippetID   string `json:"snippet_id"`
	Name        string `json:"name"`
	SnippetType string `json:"snippet_type"`
	LineStart   int    `json:"line_start"`
	LineEnd     int    `json:"line_end"`
	FilePath    string `json:"file_path"`
	FileID      string `json:"file_id"`
}

// FileInfoView is the data shown in the File Info tab.
type FileInfoView struct {
	FileID       string   `json:"file_id"`
	Path         string   `json:"path"`
	Language     string   `json:"language"`
	Dependencies []string `json:"dependencies"`
	FileSize     int64    `json:"file_size"`
	LastModified string   `json:"last_modified"` // RFC3339
	FileSummary  string   `json:"file_summary"`
}

// ── App struct ────────────────────────────────────────────────────────────────

// App is the Wails application struct. All exported methods on App are
// automatically bound to the JavaScript/TypeScript frontend.
type App struct {
	ctx context.Context
	db  *db.DB
	svc *service.Service
}

func NewApp() *App { return &App{} }

// startup is called by the Wails runtime after the window is created.
// It mirrors buildService() from cmd/oSyn/main.go.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	cfg := config.Load()

	database, err := db.New(ctx, cfg.DatabasePath, cfg.EmbedDimension)
	if err != nil {
		runtime.LogFatalf(ctx, "startup: open db: %v", err)
		return
	}
	if err := database.Migrate(ctx); err != nil {
		runtime.LogFatalf(ctx, "startup: migrate: %v", err)
		return
	}

	var gen enrichment.Generator
	if cfg.LLMProvider == "openai-compat" && cfg.LLMBaseURL != "" {
		gen = enrichment.NewOpenAICompatGenerator(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	}
	llm := enrichment.NewLLM(gen)
	embedder := enrichment.NewEmbedder(cfg.EmbedProvider, cfg.VoyageAPIKey, cfg.LocalEmbedURL, cfg.EmbedDimension)
	pl := pipeline.New(database, llm, embedder, cfg.MaxConcurrency, cfg.RepoRoot)

	a.db = database
	a.svc = service.New(database, pl)
}

func (a *App) domready(_ context.Context) {}

func (a *App) shutdown(_ context.Context) {
	if a.db != nil {
		a.db.Close()
	}
}

// ── Bound methods ─────────────────────────────────────────────────────────────

// GetFiles returns the directory tree of all indexed files.
// Synthetic library entries (path prefix "lib:") are excluded — they are not
// real source files and should not appear in the sidebar.
func (a *App) GetFiles() []FileNode {
	files, err := a.svc.ListFiles(a.ctx)
	if err != nil {
		runtime.LogErrorf(a.ctx, "GetFiles: %v", err)
		return nil
	}
	// Filter out synthetic lib: entries created by the resolver.
	real := files[:0]
	for _, f := range files {
		if !strings.HasPrefix(f.Path, "lib:") {
			real = append(real, f)
		}
	}
	return buildTree(real)
}

// GetSnippetAssembly returns all snippets for a file ordered by line_start.
func (a *App) GetSnippetAssembly(fileID string) []SnippetView {
	id, err := uuid.Parse(fileID)
	if err != nil {
		return nil
	}
	snippets, err := a.db.GetSnippetsByFile(a.ctx, id)
	if err != nil {
		runtime.LogErrorf(a.ctx, "GetSnippetAssembly: %v", err)
		return nil
	}
	views := make([]SnippetView, len(snippets))
	for i, s := range snippets {
		wl := s.Wikilinks
		if wl == nil {
			wl = []string{}
		}
		views[i] = SnippetView{
			SnippetID:   s.SnippetID.String(),
			Name:        s.Name,
			SnippetType: string(s.SnippetType),
			LineStart:   s.LineStart,
			LineEnd:     s.LineEnd,
			Description: s.Description,
			Wikilinks:   wl,
		}
	}
	return views
}

// GetFileInfo returns metadata for the File Info tab.
func (a *App) GetFileInfo(fileID string) *FileInfoView {
	files, err := a.svc.ListFiles(a.ctx)
	if err != nil {
		return nil
	}
	for _, f := range files {
		if f.FileID.String() == fileID {
			deps := f.Dependencies
			if deps == nil {
				deps = []string{}
			}
			return &FileInfoView{
				FileID:       f.FileID.String(),
				Path:         f.Path,
				Language:     string(f.Language),
				Dependencies: deps,
				FileSize:     f.FileSize,
				LastModified: f.LastModified.Format(time.RFC3339),
				FileSummary:  f.FileSummary,
			}
		}
	}
	return nil
}

// GetFileCode assembles all snippet raw_content values ordered by line_start.
func (a *App) GetFileCode(fileID string) string {
	id, err := uuid.Parse(fileID)
	if err != nil {
		return ""
	}
	snippets, err := a.db.GetSnippetsByFile(a.ctx, id)
	if err != nil {
		runtime.LogErrorf(a.ctx, "GetFileCode: %v", err)
		return ""
	}
	// Emit each snippet starting at its declared line number by filling gaps
	// between snippets with blank lines. This keeps code positions in the
	// viewer consistent with the snippet line_start/line_end metadata.
	var sb strings.Builder
	currentLine := 1
	for _, s := range snippets {
		for currentLine < s.LineStart {
			sb.WriteByte('\n')
			currentLine++
		}
		sb.WriteString(s.RawContent)
		if !strings.HasSuffix(s.RawContent, "\n") {
			sb.WriteByte('\n')
		}
		currentLine = s.LineEnd + 1
	}
	return sb.String()
}

// GetWikilinkColor returns the persisted color for a (snippetID, wikilink) pair.
// When no color has been explicitly set, a default is derived from edge types:
//   - any internal edge (file in this repo)  → blue  (#4878d0)
//   - all external edges (lib: snippets only) → yellow (#c8a020)
//   - no edges at all                         → white  (#ffffff)
func (a *App) GetWikilinkColor(snippetID, wikilink string) string {
	id, err := uuid.Parse(snippetID)
	if err != nil {
		return "#ffffff"
	}
	color, err := a.db.GetWikilinkColor(a.ctx, id, wikilink)
	if err == nil && color != "" {
		return color
	}
	// Derive default from edge types.
	targets, err := a.db.GetWikilinkEdgeTargets(a.ctx, id, wikilink)
	if err != nil || len(targets) == 0 {
		return "#ffffff"
	}
	for _, t := range targets {
		if !strings.HasPrefix(t.FilePath, "lib:") {
			return "#4878d0" // internal → blue
		}
	}
	return "#c8a020" // all external (lib:) → yellow
}

// SetWikilinkColor sets the color for wikilink on snippetID, then propagates
// the same color to all edge-connected snippets that also contain that wikilink.
func (a *App) SetWikilinkColor(snippetID, wikilink, color string) error {
	id, err := uuid.Parse(snippetID)
	if err != nil {
		return fmt.Errorf("invalid snippet_id: %w", err)
	}

	if err := a.db.SetWikilinkColor(a.ctx, id, wikilink, color); err != nil {
		return fmt.Errorf("set color: %w", err)
	}

	connectedIDs, err := a.db.GetConnectedSnippetIDs(a.ctx, id)
	if err != nil {
		return fmt.Errorf("get connected: %w", err)
	}

	for _, connIDStr := range connectedIDs {
		connID, err := uuid.Parse(connIDStr)
		if err != nil {
			continue
		}
		sn, err := a.db.GetSnippetByID(a.ctx, connID)
		if err != nil || sn == nil {
			continue
		}
		for _, wl := range sn.Wikilinks {
			if wl == wikilink {
				_ = a.db.SetWikilinkColor(a.ctx, connID, wikilink, color)
				break
			}
		}
	}
	return nil
}

// GetWikilinkEdges returns the snippets that share an edge with snippetID where
// the connected snippet's name matches wikilink (bidirectional lookup).
func (a *App) GetWikilinkEdges(snippetID, wikilink string) []WikilinkEdgeInfo {
	id, err := uuid.Parse(snippetID)
	if err != nil {
		return nil
	}
	targets, err := a.db.GetWikilinkEdgeTargets(a.ctx, id, wikilink)
	if err != nil {
		runtime.LogErrorf(a.ctx, "GetWikilinkEdges: %v", err)
		return nil
	}
	if targets == nil {
		return []WikilinkEdgeInfo{}
	}
	views := make([]WikilinkEdgeInfo, len(targets))
	for i, t := range targets {
		views[i] = WikilinkEdgeInfo{
			SnippetID:   t.SnippetID,
			Name:        t.Name,
			SnippetType: t.SnippetType,
			LineStart:   t.LineStart,
			LineEnd:     t.LineEnd,
			FilePath:    t.FilePath,
			FileID:      t.FileID,
		}
	}
	return views
}

// GetConnectedSnippetIDs returns all snippet IDs connected to snippetID via
// any edge in either direction.
func (a *App) GetConnectedSnippetIDs(snippetID string) []string {
	id, err := uuid.Parse(snippetID)
	if err != nil {
		return nil
	}
	ids, err := a.db.GetConnectedSnippetIDs(a.ctx, id)
	if err != nil {
		runtime.LogErrorf(a.ctx, "GetConnectedSnippetIDs: %v", err)
		return nil
	}
	if ids == nil {
		return []string{}
	}
	return ids
}

// ── Tree builder ──────────────────────────────────────────────────────────────

type dirEntry struct {
	children map[string]*dirEntry
	file     *FileNode // non-nil for leaf files
}

// commonDirPrefix returns the longest directory prefix shared by all paths.
// e.g. ["/a/b/c.go", "/a/b/d.go"] → "/a/b/"
func commonDirPrefix(files []*models.CodeFile) string {
	if len(files) == 0 {
		return ""
	}
	prefix := files[0].Path
	for _, f := range files[1:] {
		for !strings.HasPrefix(f.Path, prefix) {
			idx := strings.LastIndex(strings.TrimSuffix(prefix, "/"), "/")
			if idx < 0 {
				return ""
			}
			prefix = prefix[:idx+1]
		}
	}
	// Ensure prefix ends at a directory boundary
	idx := strings.LastIndex(strings.TrimSuffix(prefix, "/"), "/")
	if idx < 0 {
		return ""
	}
	return prefix[:idx+1]
}

// buildTree converts a flat []CodeFile list into a nested FileNode tree.
// Paths are rooted at the longest common directory prefix so the tree shows
// only the project-relative structure, not the full filesystem path.
func buildTree(files []*models.CodeFile) []FileNode {
	prefix := commonDirPrefix(files)
	root := &dirEntry{children: make(map[string]*dirEntry)}

	for _, f := range files {
		rel := strings.TrimPrefix(f.Path, prefix)
		if rel == "" {
			continue
		}
		parts := strings.Split(rel, "/")
		cur := root
		for i, part := range parts {
			if _, ok := cur.children[part]; !ok {
				cur.children[part] = &dirEntry{children: make(map[string]*dirEntry)}
			}
			if i == len(parts)-1 {
				fid := f.FileID.String()
				cur.children[part].file = &FileNode{
					Name:   part,
					Path:   f.Path, // keep full path for display/tooltip
					FileID: fid,
					IsDir:  false,
				}
			}
			cur = cur.children[part]
		}
	}

	var flatten func(name string, entry *dirEntry) FileNode
	flatten = func(name string, entry *dirEntry) FileNode {
		if entry.file != nil && len(entry.children) == 0 {
			return *entry.file
		}
		node := FileNode{Name: name, IsDir: true, Children: []FileNode{}}
		keys := make([]string, 0, len(entry.children))
		for k := range entry.children {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			node.Children = append(node.Children, flatten(k, entry.children[k]))
		}
		return node
	}

	keys := make([]string, 0, len(root.children))
	for k := range root.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := make([]FileNode, 0, len(keys))
	for _, k := range keys {
		result = append(result, flatten(k, root.children[k]))
	}
	return result
}
