// Package watcher implements Phase 4: Incremental Synchronisation.
// It uses fsnotify to detect file-system changes and triggers re-indexing
// for modified files only.
package watcher

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"opensynapse/internal/crawler"
	"opensynapse/internal/pipeline"
)

const debounceDelay = 500 * time.Millisecond

// Watch monitors root for file changes and re-indexes affected files.
// It blocks until ctx is cancelled.
func Watch(ctx context.Context, root string, pl *pipeline.Pipeline) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	// Add the root and every sub-directory to the watcher.
	if err := addDirs(w, root); err != nil {
		return err
	}

	log.Printf("watcher: monitoring %s", root)

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
			handleEvent(ctx, event, w, pl, pending)

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher: error: %v", err)
		}
	}
}

func handleEvent(ctx context.Context, event fsnotify.Event, w *fsnotify.Watcher, pl *pipeline.Pipeline, pending map[string]*time.Timer) {
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

	// Debounce: reset the timer each time the same path fires.
	if t, ok := pending[path]; ok {
		t.Reset(debounceDelay)
		return
	}

	pending[path] = time.AfterFunc(debounceDelay, func() {
		delete(pending, path)
		reindex(ctx, pl, event, path)
	})
}

func reindex(ctx context.Context, pl *pipeline.Pipeline, event fsnotify.Event, path string) {
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		// File removed — the DB entry (and cascaded snippets/edges) will be
		// cleaned up automatically if the file is re-indexed and found missing.
		log.Printf("watcher: file removed %s", path)
		return
	}

	if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
		log.Printf("watcher: re-indexing %s", path)
		if err := pl.IndexFile(ctx, path); err != nil {
			log.Printf("watcher: index %s: %v", path, err)
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
		dir := filepath.Dir(f.Path)
		if !seen[dir] {
			seen[dir] = true
			_ = w.Add(dir)
		}
	}
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
