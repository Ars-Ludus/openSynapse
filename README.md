# openSynapse

Unlike traditional documentation tools that produce static text or RAG systems that rely on "fuzzy" searching, openSynapse treats a codebase as a living, physical circuit. It maps the invisible "Synapses" — the intentional relationships between files — translating complex AST data into a relational knowledge graph that both humans and AI agents can navigate with precision.

## Overview

openSynapse (`oSyn`) crawls source files, extracts top-level code units (functions, methods, structs, classes) as **Snippets** using Tree-sitter, resolves cross-file import relationships as **Edges**, enriches each snippet with an LLM-generated description, and stores 768-dimensional semantic embeddings for vector search — all in a self-contained SQLite database.

```
Source files
    │
    ▼
[Tree-sitter AST]  →  Snippets + Edges
    │
    ▼
[Local LLM]        →  Descriptions
    │
    ▼
[CodeRankEmbed]    →  768-dim vectors
    │
    ▼
SQLite + vec_distance_cosine  →  Semantic search
```

## Stack

| Component | Choice | Notes |
|-----------|--------|-------|
| Language | Go 1.26 | CGO required (Tree-sitter) |
| Database | SQLite | `go-sqlite3`, WAL mode, foreign keys |
| Vector search | `vec_distance_cosine` | Registered Go callback — no extension .so needed |
| AST parsing | Tree-sitter | `smacker/go-tree-sitter`; Go and Python grammars built in |
| LLM enrichment | Any OpenAI-compatible endpoint | Tested with llama.cpp |
| Embeddings | CodeRankEmbed (ONNX) | 768-dim, NomicBERT, top-20 MTEB, CPU-friendly |
| File watching | fsnotify | 500 ms debounce for incremental re-indexing |
| CLI | Cobra | `index`, `watch`, `search`, `migrate` |

## Quick Start

### 1. Start the embedding sidecar

```bash
cd internal/vect-embed
pip install -r requirements.txt
python embedder.py --serve 8765
```

### 2. Set environment variables

```bash
export DATABASE_PATH=./opensynapse.db
export EMBED_PROVIDER=local
export LOCAL_EMBED_URL=http://127.0.0.1:8765
export EMBED_DIMENSION=768

# Optional — LLM enrichment (any OpenAI-compatible endpoint)
export LOCAL_LLM_URL=http://192.168.1.1:8080/v1
export LOCAL_LLM_MODEL=local-model
```

### 3. Build and run

```bash
go build -o oSyn ./cmd/oSyn/

./oSyn index --path /path/to/your/repo
./oSyn search "how does authentication work"
./oSyn watch --path /path/to/your/repo   # live, incremental updates
```

## Supported Languages

Go, Python, JavaScript, TypeScript, Rust.

Additional languages can be added by registering their Tree-sitter grammar in `internal/parser/parser.go`.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_PATH` | `./opensynapse.db` | Path to the SQLite database file |
| `EMBED_PROVIDER` | `null` | `local`, `voyage`, or `null` |
| `EMBED_DIMENSION` | `768` | Must match the model's output dimension |
| `LOCAL_EMBED_URL` | `http://127.0.0.1:8765` | URL of the embedding sidecar |
| `VOYAGE_API_KEY` | — | Required when `EMBED_PROVIDER=voyage` |
| `LOCAL_LLM_URL` | — | OpenAI-compatible chat completions base URL |
| `LOCAL_LLM_MODEL` | `local-model` | Model name sent in LLM requests |
| `MAX_CONCURRENCY` | `4` | Parallel file indexing workers |

## Architecture

See [DOCUMENTATION.md](DOCUMENTATION.md) for full command reference, architecture details, and extension guide.
