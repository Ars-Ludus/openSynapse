// Package mcp exposes oSyn's service layer as an MCP (Model Context Protocol)
// server over stdio. All tool logic lives in service.Service — this file
// contains only MCP schema definitions and thin dispatch wrappers.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/Ars-Ludus/openSynapse/internal/service"
)

// Server wraps a service.Service and exposes its operations as MCP tools.
type Server struct {
	svc *service.Service
	srv *server.MCPServer
}

// New creates an MCP Server and registers all six tools.
func New(svc *service.Service) *Server {
	s := &Server{
		svc: svc,
		srv: server.NewMCPServer("openSynapse", "1.0.0"),
	}
	s.registerTools()
	return s
}

// Serve runs the MCP server over stdio (blocking). The caller must redirect
// log output to stderr before calling this so stdout stays clean.
func (s *Server) Serve() error {
	return server.ServeStdio(s.srv)
}

// ── tool registration ─────────────────────────────────────────────────────────

func (s *Server) registerTools() {
	s.srv.AddTool(
		mcp.NewTool("list_files",
			mcp.WithDescription("Lists all source files currently indexed in the knowledge graph, with their language, size, and LLM-generated summary. Use this to orient yourself in an unfamiliar codebase before drilling into specific files."),
		),
		s.handleListFiles,
	)

	s.srv.AddTool(
		mcp.NewTool("describe_file",
			mcp.WithDescription("Returns a file's metadata, LLM-generated summary, and the full list of its snippets (functions, structs, types, etc.) with their descriptions and line ranges — but without raw source. Use this before editing any file to understand its structure and responsibilities. Follow up with get_snippet for full source of individual symbols."),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description("Repository-relative or absolute path to the source file."),
			),
		),
		s.handleDescribeFile,
	)

	s.srv.AddTool(
		mcp.NewTool("get_snippet",
			mcp.WithDescription("Returns the complete source code, description, type, and line range for a single snippet by its ID. Use the snippet_id values returned by describe_file, search, or get_blast_radius."),
			mcp.WithString("snippet_id",
				mcp.Required(),
				mcp.Description("UUID of the snippet."),
			),
		),
		s.handleGetSnippet,
	)

	s.srv.AddTool(
		mcp.NewTool("get_blast_radius",
			mcp.WithDescription("Pre-edit safety analysis: given a snippet ID, returns (1) the snippet itself, (2) every snippet that directly calls or references it ('dependents' — these break if the signature changes), and (3) every snippet it calls ('dependencies'). The blast_radius_count field is len(dependents) — a quick signal for how cautious to be. Use this before modifying any function, type, or variable."),
			mcp.WithString("snippet_id",
				mcp.Required(),
				mcp.Description("UUID of the snippet to analyse."),
			),
		),
		s.handleGetBlastRadius,
	)

	s.srv.AddTool(
		mcp.NewTool("search",
			mcp.WithDescription("Semantic (vector) search over all indexed snippets. Returns the top N snippets most similar to the query. Use this to find existing implementations, discover patterns, or locate relevant code when you do not know the exact file or function name."),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("Natural language or code description of what to find."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Number of results to return (default 5, max 20)."),
			),
		),
		s.handleSearch,
	)

	s.srv.AddTool(
		mcp.NewTool("get_patterns",
			mcp.WithDescription("Returns all detected architectural patterns and conventions in the codebase. Each pattern includes a name, description, and the IDs of participating snippets. Use this to understand 'how things are done here' before writing new code that should follow existing conventions."),
		),
		s.handleGetPatterns,
	)

	s.srv.AddTool(
		mcp.NewTool("get_implementations",
			mcp.WithDescription("Given an interface snippet ID, returns all concrete struct types that implement it. Use this to understand what types satisfy an interface when tracing runtime dispatch paths."),
			mcp.WithString("snippet_id",
				mcp.Required(),
				mcp.Description("UUID of the interface snippet."),
			),
		),
		s.handleGetImplementations,
	)

	s.srv.AddTool(
		mcp.NewTool("get_dependencies",
			mcp.WithDescription("Returns a snippet and all snippets it directly calls or references (outgoing edges). Useful for tracing execution paths or generating accurate documentation. For the full bi-directional impact analysis including who calls this snippet, use get_blast_radius instead."),
			mcp.WithString("snippet_id",
				mcp.Required(),
				mcp.Description("UUID of the snippet."),
			),
		),
		s.handleGetDependencies,
	)
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (s *Server) handleListFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	files, err := s.svc.ListFiles(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"files": files})
}

func (s *Server) handleDescribeFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	detail, err := s.svc.DescribeFile(ctx, path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if detail == nil {
		return mcp.NewToolResultError(fmt.Sprintf("file not indexed: %s", path)), nil
	}
	return jsonResult(detail)
}

func (s *Server) handleGetSnippet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("snippet_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sn, err := s.svc.GetSnippet(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if sn == nil {
		return mcp.NewToolResultError(fmt.Sprintf("snippet not found: %s", id)), nil
	}
	return jsonResult(sn)
}

func (s *Server) handleGetBlastRadius(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("snippet_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	br, err := s.svc.GetBlastRadius(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if br == nil {
		return mcp.NewToolResultError(fmt.Sprintf("snippet not found: %s", id)), nil
	}
	return jsonResult(br)
}

func (s *Server) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", 5)
	if limit > 20 {
		limit = 20
	}
	results, err := s.svc.Search(ctx, query, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"results": results})
}

func (s *Server) handleGetImplementations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("snippet_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	impls, err := s.svc.GetImplementations(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if impls == nil {
		return mcp.NewToolResultError(fmt.Sprintf("not an interface or not found: %s", id)), nil
	}
	return jsonResult(map[string]any{"implementations": impls})
}

func (s *Server) handleGetPatterns(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	patterns, err := s.svc.ListPatterns(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"patterns": patterns})
}

func (s *Server) handleGetDependencies(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("snippet_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := s.svc.GetDependencies(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if result == nil {
		return mcp.NewToolResultError(fmt.Sprintf("snippet not found: %s", id)), nil
	}
	return jsonResult(result)
}

// jsonResult marshals v to a JSON string and returns it as an MCP text result.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("failed to marshal result: " + err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
