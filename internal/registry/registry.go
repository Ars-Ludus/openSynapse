// Package registry manages the mapping of tracked repos to their SQLite
// databases. State is persisted in ~/.osyn/registry.json.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RepoEntry describes a single tracked repository.
type RepoEntry struct {
	Root        string    `json:"root"`
	DB          string    `json:"db"`           // filename relative to ~/.osyn/repos/
	LastIndexed time.Time `json:"last_indexed"` // zero value = never
}

// Registry holds all tracked repos.
type Registry struct {
	Repos map[string]*RepoEntry `json:"repos"`
	path  string                // full path to registry.json
}

// Load reads the registry from disk. Returns an empty registry if the file
// does not exist.
func Load(configDir string) (*Registry, error) {
	p := filepath.Join(configDir, "registry.json")
	r := &Registry{Repos: make(map[string]*RepoEntry), path: p}

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	if r.Repos == nil {
		r.Repos = make(map[string]*RepoEntry)
	}
	r.path = p
	return r, nil
}

// Save writes the registry to disk.
func (r *Registry) Save() error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, data, 0644)
}

// Add registers a repo. The DB filename is <name>.db inside ~/.osyn/repos/.
func (r *Registry) Add(name, root string) *RepoEntry {
	entry := &RepoEntry{
		Root: root,
		DB:   name + ".db",
	}
	r.Repos[name] = entry
	return entry
}

// Remove unregisters a repo. Returns the entry (for cleanup) or nil.
func (r *Registry) Remove(name string) *RepoEntry {
	entry, ok := r.Repos[name]
	if !ok {
		return nil
	}
	delete(r.Repos, name)
	return entry
}

// Get returns a repo entry by name, or nil.
func (r *Registry) Get(name string) *RepoEntry {
	return r.Repos[name]
}

// FindByRoot returns the name and entry for a repo whose root matches absPath.
func (r *Registry) FindByRoot(absPath string) (string, *RepoEntry) {
	for name, entry := range r.Repos {
		if entry.Root == absPath {
			return name, entry
		}
	}
	return "", nil
}

// List returns all repo names sorted by insertion order (map iteration order
// is non-deterministic, caller should sort if needed).
func (r *Registry) List() map[string]*RepoEntry {
	return r.Repos
}

// DBPath returns the full path to a repo's database file.
func (r *Registry) DBPath(configDir, name string) string {
	entry := r.Repos[name]
	if entry == nil {
		return ""
	}
	return filepath.Join(configDir, "repos", entry.DB)
}

// UpdateLastIndexed sets the last_indexed timestamp for a repo.
func (r *Registry) UpdateLastIndexed(name string, t time.Time) {
	if entry := r.Repos[name]; entry != nil {
		entry.LastIndexed = t
	}
}
