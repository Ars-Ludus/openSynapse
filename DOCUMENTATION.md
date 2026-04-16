# openSynapse Documentation

## Table of Contents

1. [Commands](#commands)
2. [Environment Variables](#environment-variables)
3. [Embedding Sidecar](#embedding-sidecar)
4. [Architecture](#architecture)
5. [Data Model](#data-model)
6. [Extending the System](#extending-the-system)

---

## Commands

All commands are run via the `oSyn` binary built from `./cmd/oSyn/`.

```
oSyn <command> [flags]
```

### `oSyn migrate`

Creates or updates the database schema. Run this once before using a new database file. Every other command also auto-migrates on startup, so explicit use is optional.

```bash
oSyn migrate
```

No flags.

---

### `oSyn index`

Crawls a directory tree, parses source files with Tree-sitter, resolves cross-file import edges, and enriches each snippet with an LLM description and a semantic embedding. Re-indexing a file that already exists in the database is safe and idempotent — old snippets and edges are purged via `DELETE CASCADE` before the new ones are inserted.

```bash
oSyn index [--path <dir>]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--path` | `-p` | `.` | Root directory to crawl |

**What it does per file:**

1. Detect language from file extension
2. Parse AST with Tree-sitter → extract top-level Snippets and import paths
3. Purge the file's old DB row (cascades to its snippets and edges)
4. Insert the `code_files` row and all `snippets` rows
5. Resolve cross-file edges (import → exported symbol matching)
6. For each snippet: call the LLM for a one-sentence description, then embed the description (or raw content if the LLM is disabled) via the embedding sidecar

Files larger than 2 MB are skipped. Hidden directories (`.git`, `.venv`, etc.) and `vendor`/`node_modules` trees are excluded automatically.

**Concurrency:** up to `MAX_CONCURRENCY` files are processed in parallel (default: 4). Each file is fully processed (parse → resolve → enrich) in its own goroutine before the semaphore slot is released.

---

### `oSyn watch`

Runs an initial full index of the target directory, then enters a persistent watch loop. Any write or creation event on a recognised source file triggers a re-index of that file after a 500 ms debounce window. New subdirectories are added to the watcher automatically.

```bash
oSyn watch [--path <dir>]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--path` | `-p` | `.` | Root directory to watch |

Stop with `Ctrl-C` (SIGINT) or SIGTERM. The watcher exits cleanly, flushing any in-progress index.

---

### `oSyn search`

Embeds the query string using the configured embedding provider, then performs a cosine-distance search over all embedded snippets in the database.

```bash
oSyn search <query> [--limit <n>]
```

| Argument / Flag | Short | Default | Description |
|-----------------|-------|---------|-------------|
| `<query>` | — | required | Natural language or code query |
| `--limit` | `-n` | `5` | Number of results to return |

When `EMBED_PROVIDER=local` (CodeRankEmbed), the query is sent with `is_query: true`, which prepends `"Query: "` before embedding — this aligns the query vector with the document vectors as CodeRankEmbed expects.

Results are printed to stdout ordered by cosine distance (closest first), showing snippet type, name, line range, LLM description (if present), and the raw source text.

**Note:** Search requires a real embedding provider (`local` or `voyage`). With the default `null` provider all vectors are zero and results are unordered.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_PATH` | `./opensynapse.db` | Path to the SQLite file. Created automatically if it does not exist. |
| `EMBED_PROVIDER` | `null` | Embedding backend. Options: `local` (CodeRankEmbed sidecar), `voyage` (Voyage AI API), `null` (zero vectors, indexing only). |
| `EMBED_DIMENSION` | `768` | Vector dimension. Must match the model output. CodeRankEmbed = 768, Voyage code-2 = 1024. |
| `LOCAL_EMBED_URL` | `http://127.0.0.1:8765` | Base URL of the embedding sidecar. Used when `EMBED_PROVIDER=local`. |
| `VOYAGE_API_KEY` | — | Voyage AI API key. Required when `EMBED_PROVIDER=voyage`. |
| `LOCAL_LLM_URL` | — | Base URL of an OpenAI-compatible chat completions server (e.g. `http://host:8080/v1`). LLM enrichment is disabled if this is unset. |
| `LOCAL_LLM_MODEL` | `local-model` | Model name sent in the `"model"` field of LLM requests. Ignored by llama.cpp but required by some servers. |
| `MAX_CONCURRENCY` | `4` | Maximum number of files processed simultaneously during `index` and `watch`. |

---

## Embedding Sidecar

The sidecar runs CodeRankEmbed (a 160M-parameter NomicBERT model) locally via ONNX Runtime, exposing a minimal HTTP API over localhost. No GPU required — runs efficiently on CPU.

### Starting the sidecar

```bash
cd internal/vect-embed
pip install -r requirements.txt
python embedder.py --serve [PORT]
```

`PORT` defaults to `8765`.

### HTTP API

**`POST /embed`**

Request:

```json
{
  "texts": ["def hello(): ..."],
  "is_query": false
}
```

- `texts` — list of one or more strings to embed
- `is_query` — when `true`, prepends `"Query: "` to each text before tokenisation (used for search queries, not for indexing)

Response:

```json
{
  "embeddings": [
    [0.91, -0.66, ...]
  ]
}
```

Each inner array is a 768-element `float32` vector. Errors return HTTP 4xx/5xx with `{"error": "..."}`.

### Smoke test

```bash
python embedder.py --model-path ./model
```

Runs a quick demo: embeds two code snippets and a query, prints shapes and a similarity score.

### Model files

```
internal/vect-embed/
├── embedder.py
├── requirements.txt
└── model/
    ├── config.json
    ├── tokenizer.json
    ├── tokenizer_config.json
    ├── vocab.txt
    ├── special_tokens_map.json
    └── onnx/
        └── model.onnx
```

The model is NomicBERT (`nomic-bert`) architecture, `n_embd=768`, CLS pooling, BertTokenizer (lowercase, WordPiece, vocab 30528).

---

## Architecture

### Pipeline phases

Each file goes through three sequential phases:

```
Phase 1 — Parse
    crawler.Walk()       discovers source files
    crawler.ReadFile()   reads up to 2 MB
    parser.Parse()       Tree-sitter AST → Snippets + import paths

Phase 2 — Resolve
    resolver.ResolveFile()   matches import paths to files already in the DB,
                             creates EdgeImportCall edges where a wikilink in
                             the importing file matches an exported symbol name
                             in the imported file

Phase 3 — Enrich
    llm.SummariseFile()      one-paragraph file description
    llm.SummariseSnippet()   one-sentence snippet description (per snippet)
    embedder.Embed()         768-dim vector of description (or raw content)
```

### Vector storage and search

Embeddings are stored as raw little-endian IEEE 754 float32 blobs in the `snippets.embedding BLOB` column. The `vec_distance_cosine(a, b)` SQL function is registered as a Go callback via `go-sqlite3`'s `ConnectHook`, so no shared extension library is needed at runtime.

Search is a linear scan:

```sql
SELECT ... FROM snippets
WHERE embedding IS NOT NULL
ORDER BY vec_distance_cosine(embedding, ?) ASC
LIMIT ?
```

For a typical codebase (thousands of snippets) this is fast. For very large repos an ANN index (e.g. sqlite-vec `vec0` virtual table) could replace it without changing the interface.

### Key packages

| Package | Path | Responsibility |
|---------|------|----------------|
| `crawler` | `internal/crawler/` | Walk directory tree, read files, detect language |
| `parser` | `internal/parser/` | Tree-sitter AST → `ParsedFile{Imports, Snippets}` |
| `resolver` | `internal/resolver/` | Heuristic cross-file edge creation |
| `enrichment` | `internal/enrichment/` | LLM (`llm.go`) and embedding (`embedder.go`) |
| `pipeline` | `internal/pipeline/` | Orchestrates the three phases; concurrency semaphore |
| `watcher` | `internal/watcher/` | fsnotify loop with debounce |
| `db` | `internal/db/` | SQLite connection, schema migration, all queries |
| `models` | `internal/models/` | Shared struct types (`CodeFile`, `Snippet`, `Edge`) |
| `config` | `internal/config/` | Env-var loading |

---

## Data Model

Three tables, two of which cascade on delete:

```
code_files
    file_id       TEXT  PRIMARY KEY   (UUID)
    path          TEXT  UNIQUE
    language      TEXT
    dependencies  TEXT               (JSON array of import paths)
    file_summary  TEXT               (LLM-generated)
    file_size     INTEGER
    last_modified INTEGER            (Unix timestamp)

snippets
    snippet_id    TEXT  PRIMARY KEY   (UUID)
    file_id       TEXT  → code_files  (CASCADE DELETE)
    snippet_type  TEXT               ("function", "method", "struct", …)
    name          TEXT
    line_start    INTEGER
    line_end      INTEGER
    raw_content   TEXT
    description   TEXT               (LLM-generated)
    wikilinks     TEXT               (JSON array of referenced symbol names)
    embedding     BLOB               (768 × float32, little-endian)

edges
    edge_id           TEXT  PRIMARY KEY   (UUID)
    source_snippet_id TEXT  → snippets    (CASCADE DELETE)
    target_snippet_id TEXT  → snippets    (CASCADE DELETE)
    edge_type         TEXT               ("import_call", "function_call", …)
    merged_context    TEXT               (LLM-generated, optional)
    UNIQUE(source, target, edge_type)
```

---

## Extending the System

### Add a language

1. In `internal/parser/parser.go`, import the Tree-sitter grammar package.
2. Add a case to `getLanguage(lang)` returning its `*sitter.Language`.
3. Add entries to `topLevelNodeTypes` for the node types you want to extract as snippets.
4. Add the file extension in `internal/crawler/crawler.go`'s `extToLang` map.

### Add an embedding provider

Implement the `enrichment.Embedder` interface:

```go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}
```

Add a case to `NewEmbedder` in `internal/enrichment/embedder.go` and a corresponding env-var to `internal/config/config.go`.

If the provider distinguishes query vs document embeddings (like CodeRankEmbed's `"Query: "` prefix), implement the optional `EmbedQuery(ctx, text) ([]float32, error)` method — the pipeline's `Search` function detects and uses it via interface assertion.

### Add an edge type

1. Add a constant to `models.EdgeType` in `internal/models/models.go`.
2. Add detection logic in `internal/resolver/resolver.go`'s `ResolveFile` method (or create a new resolver phase).
