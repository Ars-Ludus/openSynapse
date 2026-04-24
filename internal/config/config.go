package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Ars-Ludus/openSynapse/internal/registry"
)

// Config holds all runtime configuration.
type Config struct {
	// Database
	DatabasePath string // resolved path to the SQLite file

	// LLM provider
	LLMProvider string
	LLMBaseURL  string
	LLMModel    string
	LLMAPIKey   string

	// Embedding
	EmbedProvider  string // "local", "voyage", "null"
	EmbedDimension int
	VoyageAPIKey   string
	LocalEmbedURL  string

	// Pipeline
	MaxConcurrency int

	// Repo context (set by auto-detection or --repo flag)
	RepoName string // name in registry, empty if using DATABASE_PATH directly
	RepoRoot string // absolute path to repo root
}

// configFile mirrors the JSON structure of ~/.osyn/config.json.
type configFile struct {
	LLM       llmConfig       `json:"llm"`
	Embedding embeddingConfig `json:"embedding"`
	MaxConc   int             `json:"max_concurrency"`
}

type llmConfig struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
}

type embeddingConfig struct {
	Provider  string `json:"provider"`
	Dimension int    `json:"dimension"`
	LocalURL  string `json:"local_url"`
	VoyageKey string `json:"voyage_api_key"`
}

// ConfigDir returns the path to ~/.osyn/, creating it if needed.
func ConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".osyn")
	}
	return filepath.Join(home, ".osyn")
}

// EnsureConfigDir creates ~/.osyn/ and ~/.osyn/repos/ if they don't exist.
// Returns the config dir path.
func EnsureConfigDir() (string, error) {
	dir := ConfigDir()
	if err := os.MkdirAll(filepath.Join(dir, "repos"), 0755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// Load reads configuration from ~/.osyn/config.json (if it exists), then
// overlays environment variables. Env vars always win.
func Load() *Config {
	cfg := &Config{
		// Defaults
		LLMModel:       "local-model",
		EmbedProvider:  "null",
		EmbedDimension: 768,
		LocalEmbedURL:  "http://127.0.0.1:8765",
		MaxConcurrency: 4,
	}

	// Read config file (errors are non-fatal — defaults are fine).
	if data, err := os.ReadFile(filepath.Join(ConfigDir(), "config.json")); err == nil {
		var fc configFile
		if err := json.Unmarshal(data, &fc); err == nil {
			applyFileConfig(cfg, &fc)
		}
	}

	// Env vars override everything.
	applyEnvOverrides(cfg)

	return cfg
}

func applyFileConfig(cfg *Config, fc *configFile) {
	if fc.LLM.Provider != "" {
		cfg.LLMProvider = fc.LLM.Provider
	}
	if fc.LLM.BaseURL != "" {
		cfg.LLMBaseURL = fc.LLM.BaseURL
	}
	if fc.LLM.Model != "" {
		cfg.LLMModel = fc.LLM.Model
	}
	if fc.LLM.APIKey != "" {
		cfg.LLMAPIKey = fc.LLM.APIKey
	}
	if fc.Embedding.Provider != "" {
		cfg.EmbedProvider = fc.Embedding.Provider
	}
	if fc.Embedding.Dimension > 0 {
		cfg.EmbedDimension = fc.Embedding.Dimension
	}
	if fc.Embedding.LocalURL != "" {
		cfg.LocalEmbedURL = fc.Embedding.LocalURL
	}
	if fc.Embedding.VoyageKey != "" {
		cfg.VoyageAPIKey = fc.Embedding.VoyageKey
	}
	if fc.MaxConc > 0 {
		cfg.MaxConcurrency = fc.MaxConc
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("DATABASE_PATH"); v != "" {
		cfg.DatabasePath = v
	}
	if v := os.Getenv("LLM_PROVIDER"); v != "" {
		cfg.LLMProvider = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		cfg.LLMBaseURL = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		cfg.LLMModel = v
	}
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		cfg.LLMAPIKey = v
	}
	if v := os.Getenv("EMBED_PROVIDER"); v != "" {
		cfg.EmbedProvider = v
	}
	if v := os.Getenv("EMBED_DIMENSION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.EmbedDimension = n
		}
	}
	if v := os.Getenv("VOYAGE_API_KEY"); v != "" {
		cfg.VoyageAPIKey = v
	}
	if v := os.Getenv("LOCAL_EMBED_URL"); v != "" {
		cfg.LocalEmbedURL = v
	}
	if v := os.Getenv("MAX_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxConcurrency = n
		}
	}
}

// ResolveRepo determines the database path. Priority:
//  1. DATABASE_PATH env var (hard override)
//  2. --repo flag (looked up in registry)
//  3. Auto-detection: walk up from cwd looking for .git/, match in registry
//
// Returns the resolved Config with DatabasePath, RepoName, and RepoRoot set.
func ResolveRepo(cfg *Config, repoFlag string) error {
	// 1. DATABASE_PATH env var takes absolute precedence.
	if cfg.DatabasePath != "" {
		return nil
	}

	dir := ConfigDir()
	reg, err := registry.Load(dir)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	// 2. Explicit --repo flag.
	if repoFlag != "" {
		entry := reg.Get(repoFlag)
		if entry == nil {
			return fmt.Errorf("repo %q not found in registry — run `oSyn init` first", repoFlag)
		}
		cfg.DatabasePath = reg.DBPath(dir, repoFlag)
		cfg.RepoName = repoFlag
		cfg.RepoRoot = entry.Root
		return nil
	}

	// 3. Auto-detect from cwd.
	gitRoot, err := findGitRoot()
	if err != nil {
		return fmt.Errorf("not in a git repo and no --repo flag or DATABASE_PATH set")
	}

	name, entry := reg.FindByRoot(gitRoot)
	if entry == nil {
		return fmt.Errorf("repo at %s is not registered — run `oSyn init`", gitRoot)
	}

	cfg.DatabasePath = reg.DBPath(dir, name)
	cfg.RepoName = name
	cfg.RepoRoot = entry.Root
	return nil
}

// findGitRoot walks up from the current working directory looking for .git/.
func findGitRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .git found")
		}
		dir = parent
	}
}

// WriteConfigFile writes the given Config's LLM/embedding/concurrency settings
// to ~/.osyn/config.json.
func WriteConfigFile(cfg *Config) error {
	dir, err := EnsureConfigDir()
	if err != nil {
		return err
	}

	fc := configFile{
		LLM: llmConfig{
			Provider: cfg.LLMProvider,
			BaseURL:  cfg.LLMBaseURL,
			Model:    cfg.LLMModel,
			APIKey:   cfg.LLMAPIKey,
		},
		Embedding: embeddingConfig{
			Provider:  cfg.EmbedProvider,
			Dimension: cfg.EmbedDimension,
			LocalURL:  cfg.LocalEmbedURL,
			VoyageKey: cfg.VoyageAPIKey,
		},
		MaxConc: cfg.MaxConcurrency,
	}

	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}

// ReadConfigFile reads the raw config file and returns it as the configFile
// struct. Returns nil if the file doesn't exist.
func ReadConfigFile() (*configFile, error) {
	data, err := os.ReadFile(filepath.Join(ConfigDir(), "config.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var fc configFile
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, err
	}
	return &fc, nil
}

// SetConfigValue sets a dot-notation key in the config file.
// Supported keys: llm.provider, llm.base_url, llm.model, llm.api_key,
// embedding.provider, embedding.dimension, embedding.local_url, embedding.voyage_api_key,
// max_concurrency.
func SetConfigValue(key, value string) error {
	dir, err := EnsureConfigDir()
	if err != nil {
		return err
	}

	p := filepath.Join(dir, "config.json")
	var fc configFile

	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &fc)
	}

	switch key {
	case "llm.provider":
		fc.LLM.Provider = value
	case "llm.base_url":
		fc.LLM.BaseURL = value
	case "llm.model":
		fc.LLM.Model = value
	case "llm.api_key":
		fc.LLM.APIKey = value
	case "embedding.provider":
		fc.Embedding.Provider = value
	case "embedding.dimension":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("dimension must be an integer: %w", err)
		}
		fc.Embedding.Dimension = n
	case "embedding.local_url":
		fc.Embedding.LocalURL = value
	case "embedding.voyage_api_key":
		fc.Embedding.VoyageKey = value
	case "max_concurrency":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("max_concurrency must be an integer: %w", err)
		}
		fc.MaxConc = n
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}
