package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"opensynapse/internal/api"
	"opensynapse/internal/config"
	"opensynapse/internal/db"
	"opensynapse/internal/enrichment"
	osymcp "opensynapse/internal/mcp"
	"opensynapse/internal/pipeline"
	"opensynapse/internal/service"
	"opensynapse/internal/watcher"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "oSyn",
		Short: "openSynapse — knowledge-graph static analysis for codebases",
	}
	root.AddCommand(
		indexCmd(),
		watchCmd(),
		searchCmd(),
		migrateCmd(),
		serveCmd(),
		serveMcpCmd(),
		queryCmd(),
	)
	return root
}

// ── migrate ───────────────────────────────────────────────────────────────────

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Create or update the database schema",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			database, err := db.New(cmd.Context(), cfg.DatabasePath, cfg.EmbedDimension)
			if err != nil {
				return fmt.Errorf("connect db: %w", err)
			}
			defer database.Close()
			if err := database.Migrate(cmd.Context()); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			log.Println("migrate: schema up to date")
			return nil
		},
	}
}

// ── index ─────────────────────────────────────────────────────────────────────

func indexCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Crawl and index a repository into the knowledge graph",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.Pipeline.IndexDir(cmd.Context(), path)
		},
	}
	cmd.Flags().StringVarP(&path, "path", "p", ".", "Root directory to index")
	return cmd
}

// ── watch ─────────────────────────────────────────────────────────────────────

func watchCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch a repository for changes and keep the index up to date",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			svc, cleanup, err := buildService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			log.Printf("watch: running initial index of %s", path)
			if err := svc.Pipeline.IndexDir(ctx, path); err != nil {
				log.Printf("watch: initial index error: %v", err)
			}

			return watcher.Watch(ctx, path, svc.Pipeline)
		},
	}
	cmd.Flags().StringVarP(&path, "path", "p", ".", "Root directory to watch")
	return cmd
}

// ── search ────────────────────────────────────────────────────────────────────

func searchCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Semantic search over indexed snippets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()

			results, err := svc.Search(cmd.Context(), args[0], limit)
			if err != nil {
				return err
			}

			if len(results) == 0 {
				fmt.Println("No results found.")
				return nil
			}
			for i, s := range results {
				fmt.Printf("\n── Result %d ──────────────────────────\n", i+1)
				fmt.Printf("  Type : %s\n", s.SnippetType)
				fmt.Printf("  Name : %s\n", s.Name)
				fmt.Printf("  Lines: %d–%d\n", s.LineStart, s.LineEnd)
				if s.Description != "" {
					fmt.Printf("  Desc : %s\n", s.Description)
				}
				fmt.Printf("\n%s\n", s.RawContent)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 5, "Maximum number of results")
	return cmd
}

// ── serve ─────────────────────────────────────────────────────────────────────

func serveCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP REST API server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()

			addr := fmt.Sprintf(":%d", port)
			log.Printf("serve: listening on %s", addr)
			return api.New(svc).ListenAndServe(addr)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8080, "TCP port to listen on")
	return cmd
}

// ── serve-mcp ─────────────────────────────────────────────────────────────────

func serveMcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve-mcp",
		Short: "Start the MCP server (stdio JSON-RPC for AI agent integration)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Redirect all log output to stderr — stdout is reserved for MCP JSON-RPC.
			log.SetOutput(os.Stderr)

			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()

			return osymcp.New(svc).Serve()
		},
	}
}

// ── query ─────────────────────────────────────────────────────────────────────
//
// The query subcommands are thin CLI wrappers over service.Service — the same
// tool implementations used by the HTTP API and MCP server. All output is JSON
// to stdout so the results can be piped to jq or used in scripts.

func queryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query the knowledge graph directly (JSON output)",
	}
	cmd.AddCommand(
		queryFilesCmd(),
		queryFileCmd(),
		querySnippetCmd(),
		queryBlastRadiusCmd(),
		queryDepsCmd(),
	)
	return cmd
}

func queryFilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "files",
		Short: "List all indexed files",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			files, err := svc.ListFiles(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(map[string]any{"files": files})
		},
	}
}

func queryFileCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Describe a file: metadata and snippet listing",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			detail, err := svc.DescribeFile(cmd.Context(), path)
			if err != nil {
				return err
			}
			if detail == nil {
				return fmt.Errorf("file not indexed: %s", path)
			}
			return printJSON(detail)
		},
	}
	cmd.Flags().StringVarP(&path, "path", "p", "", "File path to describe (required)")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func querySnippetCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "snippet",
		Short: "Get a snippet's full source and metadata",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			sn, err := svc.GetSnippet(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sn == nil {
				return fmt.Errorf("snippet not found: %s", id)
			}
			return printJSON(sn)
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Snippet UUID (required)")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

func queryBlastRadiusCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "blast-radius",
		Short: "Show what depends on a snippet (callers + callees)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			br, err := svc.GetBlastRadius(cmd.Context(), id)
			if err != nil {
				return err
			}
			if br == nil {
				return fmt.Errorf("snippet not found: %s", id)
			}
			return printJSON(br)
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Snippet UUID (required)")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

func queryDepsCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "deps",
		Short: "List what a snippet directly calls or imports",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			result, err := svc.GetDependencies(cmd.Context(), id)
			if err != nil {
				return err
			}
			if result == nil {
				return fmt.Errorf("snippet not found: %s", id)
			}
			return printJSON(result)
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Snippet UUID (required)")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

// ── shared setup ──────────────────────────────────────────────────────────────

// buildService constructs the full runtime stack and returns a Service and a
// cleanup function. All commands (index, watch, search, serve, query, …) use
// this as their single entry point so the wiring never diverges.
func buildService(ctx context.Context) (*service.Service, func(), error) {
	cfg := config.Load()

	database, err := db.New(ctx, cfg.DatabasePath, cfg.EmbedDimension)
	if err != nil {
		return nil, nil, fmt.Errorf("connect db: %w", err)
	}

	if err := database.Migrate(ctx); err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}

	llm := enrichment.NewLLM(cfg.LocalLLMURL, cfg.LocalLLMModel)
	embedder := enrichment.NewEmbedder(cfg.EmbedProvider, cfg.VoyageAPIKey, cfg.LocalEmbedURL, cfg.EmbedDimension)
	pl := pipeline.New(database, llm, embedder, cfg.MaxConcurrency)

	svc := service.New(database, pl)
	cleanup := func() { database.Close() }
	return svc, cleanup, nil
}

// printJSON writes v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
