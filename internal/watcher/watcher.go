// Package watcher implements Phase 4: Incremental Synchronisation.
// It uses fsnotify to detect file-system changes and triggers re-indexing
// for modified files only.
package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/Ars-Ludus/openSynapse/internal/crawler"
	"github.com/Ars-Ludus/openSynapse/internal/pipeline"
)

const debounceDelay = 500 * time.Millisecond

// Watch monitors root for file changes and re-indexes affected files.
// It blocks until ctx is cancelled. File paths are converted to repo-relative
// before passing to the pipeline.
func Watch(ctx context.Context, root string, pl *pipeline.Pipeline) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	// Add the root and every sub-directory to the watcher.
	if err := addDirs(w, absRoot); err != nil {
		return err
	}

	slog.Info("watcher: monitoring", "root", absRoot)

	// pending maps file path → timer for debouncing rapid writes.
	pending := make(map[string]*time.Timer)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			handleEvent(ctx, event, w, pl, pending, absRoot)

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.Error("watcher: fsnotify error", "err", err)
		}
	}
}

func handleEvent(ctx context.Context, event fsnotify.Event, w *fsnotify.Watcher, pl *pipeline.Pipeline, pending map[string]*time.Timer, root string) {
	path := event.Name

	// Handle new directories: add them to the watcher.
	if event.Has(fsnotify.Create) && isDir(path) {
		_ = w.Add(path)
		return
	}

	// Only react to source files we understand.
	if crawler.DetectLanguage(path) == "unknown" {
		return
	}

	// Convert absolute fsnotify path to repo-relative.
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		slog.Error("watcher: relative path", "path", path, "err", err)
		return
	}
	relPath = filepath.ToSlash(relPath)

	// Debounce: reset the timer each time the same path fires.
	if t, ok := pending[relPath]; ok {
		t.Reset(debounceDelay)
		return
	}

	pending[relPath] = time.AfterFunc(debounceDelay, func() {
		delete(pending, relPath)
		reindex(ctx, pl, event, relPath)
	})
}

func reindex(ctx context.Context, pl *pipeline.Pipeline, event fsnotify.Event, relPath string) {
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		slog.Info("watcher: file removed, deleting from DB", "path", relPath)
		if err := pl.DeleteFile(ctx, relPath); err != nil {
			slog.Error("watcher: delete", "path", relPath, "err", err)
		}
		return
	}

	if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
		slog.Info("watcher: re-indexing", "path", relPath)
		if err := pl.IndexFile(ctx, relPath); err != nil {
			slog.Error("watcher: index", "path", relPath, "err", err)
		}
	}
}

func addDirs(w *fsnotify.Watcher, root string) error {
	files, err := crawler.Walk(root)
	if err != nil {
		return err
	}

	seen := make(map[string]bool)
	seen[root] = true
	_ = w.Add(root)

	for _, f := range files {
		// f.Path is repo-relative; resolve to absolute for the watcher.
		absDir := filepath.Dir(filepath.Join(root, filepath.FromSlash(f.Path)))
		if !seen[absDir] {
			seen[absDir] = true
			_ = w.Add(absDir)
		}
	}
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
