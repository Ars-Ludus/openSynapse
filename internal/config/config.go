package config

import (
	"os"
	"strconv"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	// Database
	DatabasePath string // path to the SQLite file (created if missing)

	// Local LLM (OpenAI-compatible, e.g. llama.cpp)
	LocalLLMURL   string // e.g. "http://192.168.254.8:8080/v1"
	LocalLLMModel string // model name sent in the request body

	// Embedding
	EmbedProvider  string // "local", "voyage", "null"
	EmbedDimension int
	VoyageAPIKey   string
	LocalEmbedURL  string // used when EmbedProvider="local"

	// Pipeline
	MaxConcurrency int
}

func Load() *Config {
	embedDim := intEnv("EMBED_DIMENSION", 768)
	maxConc := intEnv("MAX_CONCURRENCY", 4)

	return &Config{
		DatabasePath:  getEnv("DATABASE_PATH", "./opensynapse.db"),
		LocalLLMURL:   os.Getenv("LOCAL_LLM_URL"),
		LocalLLMModel: getEnv("LOCAL_LLM_MODEL", "local-model"),
		EmbedProvider:   getEnv("EMBED_PROVIDER", "null"),
		EmbedDimension:  embedDim,
		VoyageAPIKey:    os.Getenv("VOYAGE_API_KEY"),
		LocalEmbedURL:   getEnv("LOCAL_EMBED_URL", "http://127.0.0.1:8765"),
		MaxConcurrency:  maxConc,
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
