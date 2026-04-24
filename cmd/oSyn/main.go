package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Ars-Ludus/providertron/capability"
	"github.com/Ars-Ludus/providertron/providers/gemini"
	"github.com/spf13/cobra"
	"github.com/Ars-Ludus/openSynapse/internal/api"
	"github.com/Ars-Ludus/openSynapse/internal/config"
	"github.com/Ars-Ludus/openSynapse/internal/db"
	"github.com/Ars-Ludus/openSynapse/internal/enrichment"
	osymcp "github.com/Ars-Ludus/openSynapse/internal/mcp"
	"github.com/Ars-Ludus/openSynapse/internal/pipeline"
	"github.com/Ars-Ludus/openSynapse/internal/registry"
	"github.com/Ars-Ludus/openSynapse/internal/service"
	"github.com/Ars-Ludus/openSynapse/internal/watcher"
)

// repoFlag is the global --repo override. Empty means auto-detect.
var repoFlag string

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

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
	root.PersistentFlags().StringVar(&repoFlag, "repo", "", "Name of a registered repo (overrides auto-detection)")
	root.AddCommand(
		initCmd(),
		reposCmd(),
		configCmd(),
		indexCmd(),
		watchCmd(),
		searchCmd(),
		enrichCmd(),
		enrichChainsCmd(),
		detectPatternsCmd(),
		migrateCmd(),
		serveCmd(),
		serveMcpCmd(),
		queryCmd(),
	)
	return root
}

// ── init ─────────────────────────────────────────────────────────────────────

func initCmd() *cobra.Command {
	var name, path string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Register the current directory as a tracked repo",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := config.EnsureConfigDir()
			if err != nil {
				return err
			}

			if path == "" {
				path, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return err
			}

			if name == "" {
				name = filepath.Base(path)
			}

			reg, err := registry.Load(dir)
			if err != nil {
				return err
			}

			// Check if already registered.
			if existing, entry := reg.FindByRoot(path); entry != nil {
				fmt.Printf("Already registered as %q\n", existing)
				fmt.Printf("  Root: %s\n", entry.Root)
				fmt.Printf("  DB:   %s\n", filepath.Join(dir, "repos", entry.DB))
				return nil
			}

			entry := reg.Add(name, path)
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}

			dbPath := filepath.Join(dir, "repos", entry.DB)
			fmt.Printf("Registered %q at %s\n", name, path)
			fmt.Printf("Database: %s\n", dbPath)

			// Create and migrate the database.
			cfg := config.Load()
			database, err := db.New(cmd.Context(), dbPath, cfg.EmbedDimension)
			if err != nil {
				return fmt.Errorf("create db: %w", err)
			}
			if err := database.Migrate(cmd.Context()); err != nil {
				database.Close()
				return fmt.Errorf("migrate: %w", err)
			}
			database.Close()

			fmt.Println("Run `oSyn index` to build the knowledge graph.")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Short name for the repo (default: directory name)")
	cmd.Flags().StringVarP(&path, "path", "p", "", "Path to the repo root (default: current directory)")
	return cmd
}

// ── repos ────────────────────────────────────────────────────────────────────

func reposCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "List all tracked repos",
		RunE: func(_ *cobra.Command, _ []string) error {
			dir := config.ConfigDir()
			reg, err := registry.Load(dir)
			if err != nil {
				return err
			}
			if len(reg.Repos) == 0 {
				fmt.Println("No repos registered. Run `oSyn init` in a repo directory.")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "NAME\tROOT\tLAST INDEXED\tDB SIZE")
			for name, entry := range reg.Repos {
				lastIdx := "never"
				if !entry.LastIndexed.IsZero() {
					lastIdx = entry.LastIndexed.Format("2006-01-02 15:04")
				}
				dbPath := filepath.Join(dir, "repos", entry.DB)
				dbSize := "—"
				if info, err := os.Stat(dbPath); err == nil {
					dbSize = formatSize(info.Size())
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, entry.Root, lastIdx, dbSize)
			}
			tw.Flush()
			return nil
		},
	}
	cmd.AddCommand(reposRemoveCmd())
	return cmd
}

func reposRemoveCmd() *cobra.Command {
	var deleteDB bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir := config.ConfigDir()
			reg, err := registry.Load(dir)
			if err != nil {
				return err
			}
			entry := reg.Remove(args[0])
			if entry == nil {
				return fmt.Errorf("repo %q not found", args[0])
			}
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
			fmt.Printf("Unregistered %q\n", args[0])

			if deleteDB {
				dbPath := filepath.Join(dir, "repos", entry.DB)
				if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("delete db: %w", err)
				}
				fmt.Printf("Deleted %s\n", dbPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&deleteDB, "delete-db", false, "Also delete the SQLite database file")
	return cmd
}

// ── config ───────────────────────────────────────────────────────────────────

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage global configuration",
	}
	cmd.AddCommand(configShowCmd(), configSetCmd())
	return cmd
}

func configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print resolved configuration (file + env overrides)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg := config.Load()
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"config_dir": config.ConfigDir(),
				"llm": map[string]string{
					"provider": cfg.LLMProvider,
					"base_url": cfg.LLMBaseURL,
					"model":    cfg.LLMModel,
				},
				"embedding": map[string]any{
					"provider":  cfg.EmbedProvider,
					"dimension": cfg.EmbedDimension,
					"local_url": cfg.LocalEmbedURL,
				},
				"max_concurrency": cfg.MaxConcurrency,
			})
		},
	}
}

func configSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value (e.g. oSyn config set llm.provider gemini)",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := config.SetConfigValue(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Set %s = %s\n", args[0], args[1])
			return nil
		},
	}
}

// ── migrate ──────────────────────────────────────────────────────────────────

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Create or update the database schema",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			if err := config.ResolveRepo(cfg, repoFlag); err != nil {
				return err
			}
			database, err := db.New(cmd.Context(), cfg.DatabasePath, cfg.EmbedDimension)
			if err != nil {
				return fmt.Errorf("connect db: %w", err)
			}
			defer database.Close()
			if err := database.Migrate(cmd.Context()); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			slog.Info("migrate: schema up to date")
			return nil
		},
	}
}

// ── index ────────────────────────────────────────────────────────────────────

func indexCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Crawl and index a repository into the knowledge graph",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cfg, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()

			root := path
			if root == "" || root == "." {
				root = cfg.RepoRoot
			}
			if root == "" {
				root = "."
			}

			if err := svc.Pipeline.IndexDir(cmd.Context(), root); err != nil {
				return err
			}

			// Update last_indexed timestamp in registry.
			updateLastIndexed(cfg.RepoName)
			return nil
		},
	}
	cmd.Flags().StringVarP(&path, "path", "p", "", "Root directory to index (default: repo root)")
	return cmd
}

// ── enrich ───────────────────────────────────────────────────────────────────

func enrichCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "enrich",
		Short: "Generate LLM descriptions for files and snippets that are missing them",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.Pipeline.Enrich(cmd.Context(), force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Re-enrich even if descriptions already exist")
	return cmd
}

// ── enrich-chains ───────────────────────────────────────────────────────────

func enrichChainsCmd() *cobra.Command {
	var maxDepth int
	cmd := &cobra.Command{
		Use:   "enrich-chains",
		Short: "Generate LLM call-chain summaries for functions and methods",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.Pipeline.EnrichCallChains(cmd.Context(), maxDepth)
		},
	}
	cmd.Flags().IntVar(&maxDepth, "depth", 3, "Maximum call chain depth to follow")
	return cmd
}

// ── detect-patterns ─────────────────────────────────────────────────────────

func detectPatternsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect-patterns",
		Short: "Detect architectural patterns across the indexed codebase",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.DetectPatterns(cmd.Context())
		},
	}
}

// ── watch ────────────────────────────────────────────────────────────────────

func watchCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch a repository for changes and keep the index up to date",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			svc, cfg, cleanup, err := buildService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			root := path
			if root == "" || root == "." {
				root = cfg.RepoRoot
			}
			if root == "" {
				root = "."
			}

			slog.Info("watch: running initial index", "path", root)
			if err := svc.Pipeline.IndexDir(ctx, root); err != nil {
				slog.Error("watch: initial index", "err", err)
			}

			updateLastIndexed(cfg.RepoName)
			return watcher.Watch(ctx, root, svc.Pipeline)
		},
	}
	cmd.Flags().StringVarP(&path, "path", "p", "", "Root directory to watch (default: repo root)")
	return cmd
}

// ── search ───────────────────────────────────────────────────────────────────

func searchCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Semantic search over indexed snippets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, cleanup, err := buildService(cmd.Context())
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

// ── serve ────────────────────────────────────────────────────────────────────

func serveCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP REST API server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()

			addr := fmt.Sprintf(":%d", port)
			slog.Info("serve: listening", "addr", addr)
			return api.New(svc).ListenAndServe(addr)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8080, "TCP port to listen on")
	return cmd
}

// ── serve-mcp ────────────────────────────────────────────────────────────────

func serveMcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve-mcp",
		Short: "Start the MCP server (stdio JSON-RPC for AI agent integration)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()

			return osymcp.New(svc).Serve()
		},
	}
}

// ── query ────────────────────────────────────────────────────────────────────

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
		queryPatternsCmd(),
	)
	return cmd
}

func queryFilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "files",
		Short: "List all indexed files",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, cleanup, err := buildService(cmd.Context())
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
			svc, _, cleanup, err := buildService(cmd.Context())
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
			svc, _, cleanup, err := buildService(cmd.Context())
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
			svc, _, cleanup, err := buildService(cmd.Context())
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
			svc, _, cleanup, err := buildService(cmd.Context())
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

func queryPatternsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "patterns",
		Short: "List detected architectural patterns",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, cleanup, err := buildService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			patterns, err := svc.ListPatterns(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(map[string]any{"patterns": patterns})
		},
	}
}

// ── shared setup ─────────────────────────────────────────────────────────────

// buildService constructs the full runtime stack. It resolves the repo
// (via --repo flag, auto-detection, or DATABASE_PATH env var), loads config,
// and returns a Service, the resolved Config, and a cleanup function.
func buildService(ctx context.Context) (*service.Service, *config.Config, func(), error) {
	cfg := config.Load()

	if err := config.ResolveRepo(cfg, repoFlag); err != nil {
		return nil, nil, nil, err
	}

	database, err := db.New(ctx, cfg.DatabasePath, cfg.EmbedDimension)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect db: %w", err)
	}

	if err := database.Migrate(ctx); err != nil {
		database.Close()
		return nil, nil, nil, fmt.Errorf("migrate: %w", err)
	}

	gen := buildGenerator(cfg)
	llm := enrichment.NewLLM(gen)
	embedder := enrichment.NewEmbedder(cfg.EmbedProvider, cfg.VoyageAPIKey, cfg.LocalEmbedURL, cfg.EmbedDimension)

	root := cfg.RepoRoot
	if root == "" {
		root = "."
	}
	pl := pipeline.New(database, llm, embedder, cfg.MaxConcurrency, root)

	svc := service.New(database, pl)
	cleanup := func() { database.Close() }
	return svc, cfg, cleanup, nil
}

// buildGenerator selects a capability.Generator based on LLM_PROVIDER config.
func buildGenerator(cfg *config.Config) capability.Generator {
	switch cfg.LLMProvider {
	case "gemini":
		backend, err := gemini.New(&gemini.Config{
			APIKey: cfg.LLMAPIKey,
			Model:  cfg.LLMModel,
		})
		if err != nil {
			slog.Error("llm: failed to create gemini backend", "err", err)
			return nil
		}
		slog.Info("llm: using gemini provider", "model", cfg.LLMModel)
		return backend
	case "openai-compat":
		if cfg.LLMBaseURL == "" {
			slog.Warn("LLM_PROVIDER=openai-compat but LLM_BASE_URL is not set; enrichment disabled")
			return nil
		}
		slog.Info("llm: using openai-compat provider", "base_url", cfg.LLMBaseURL, "model", cfg.LLMModel)
		return enrichment.NewOpenAICompatGenerator(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	case "":
		return nil
	default:
		slog.Warn("unknown LLM_PROVIDER; enrichment disabled", "provider", cfg.LLMProvider)
		return nil
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// updateLastIndexed updates the registry timestamp for a repo (if known).
func updateLastIndexed(repoName string) {
	if repoName == "" {
		return
	}
	dir := config.ConfigDir()
	reg, err := registry.Load(dir)
	if err != nil {
		return
	}
	reg.UpdateLastIndexed(repoName, time.Now().UTC())
	_ = reg.Save()
}
