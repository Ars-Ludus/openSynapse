package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"opensynapse/internal/config"
	"opensynapse/internal/db"
	"opensynapse/internal/enrichment"
	"opensynapse/internal/pipeline"
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
	root.AddCommand(indexCmd(), watchCmd(), searchCmd(), migrateCmd())
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
			pl, cleanup, err := buildPipeline(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			return pl.IndexDir(cmd.Context(), path)
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

			pl, cleanup, err := buildPipeline(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			// Perform an initial full index before entering watch mode.
			log.Printf("watch: running initial index of %s", path)
			if err := pl.IndexDir(ctx, path); err != nil {
				log.Printf("watch: initial index error: %v", err)
			}

			return watcher.Watch(ctx, path, pl)
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
			pl, cleanup, err := buildPipeline(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()

			results, err := pl.Search(cmd.Context(), args[0], limit)
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

// ── shared setup ──────────────────────────────────────────────────────────────

func buildPipeline(ctx context.Context) (*pipeline.Pipeline, func(), error) {
	cfg := config.Load()

	database, err := db.New(ctx, cfg.DatabasePath, cfg.EmbedDimension)
	if err != nil {
		return nil, nil, fmt.Errorf("connect db: %w", err)
	}

	// Auto-migrate on every run so the schema is always current.
	if err := database.Migrate(ctx); err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}

	llm := enrichment.NewLLM(cfg.LocalLLMURL, cfg.LocalLLMModel)
	embedder := enrichment.NewEmbedder(cfg.EmbedProvider, cfg.VoyageAPIKey, cfg.LocalEmbedURL, cfg.EmbedDimension)

	pl := pipeline.New(database, llm, embedder, cfg.MaxConcurrency)

	cleanup := func() { database.Close() }
	return pl, cleanup, nil
}
