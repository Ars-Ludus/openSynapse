# openSynapse Documentation

## Table of Contents

1. [Commands](#commands)
2. [Environment Variables](#environment-variables)
3. [HTTP API](#http-api)
4. [MCP Server](#mcp-server)
5. [Embedding Sidecar](#embedding-sidecar)
6. [Architecture](#architecture)
7. [Data Model](#data-model)
8. [Extending the System](#extending-the-system)

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

**Concurrency:** up to `MAX_CONCURRENCY` files are processed in parallel (default: 4).

---

### `oSyn watch`

Runs an initial full index of the target directory, then enters a persistent watch loop. Any write or creation event on a recognised source file triggers a re-index of that file after a 500 ms debounce window.

```bash
oSyn watch [--path <dir>]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--path` | `-p` | `.` | Root directory to watch |

Stop with `Ctrl-C` (SIGINT) or SIGTERM.

---

### `oSyn search`

Embeds the query string and performs a cosine-distance search over all indexed snippets. Prints human-readable results to stdout.

```bash
oSyn search <query> [--limit <n>]
```

| Argument / Flag | Short | Default | Description |
|-----------------|-------|---------|-------------|
| `<query>` | — | required | Natural language or code query |
| `--limit` | `-n` | `5` | Number of results to return |

**Note:** Search requires a real embedding provider (`local` or `voyage`). With the default `null` provider all vectors are zero and results are unordered.

---

### `oSyn serve`

Starts the HTTP REST API server. All endpoints are documented in the [HTTP API](#http-api) section.

```bash
oSyn serve [--port <n>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | TCP port to listen on |

---

### `oSyn serve-mcp`

Starts the MCP (Model Context Protocol) server over stdio. Designed for direct integration with Claude Code and Claude Desktop. All tools are documented in the [MCP Server](#mcp-server) section.

```bash
oSyn serve-mcp
```

No flags. All configuration is via environment variables (same as other commands). Log output is redirected to stderr to keep stdout clean for JSON-RPC.

---

### `oSyn query`

Direct CLI access to the same tool operations used by the HTTP API and MCP server. All output is JSON — pipe to `jq` for filtering or use in scripts.

```bash
oSyn query <subcommand> [flags]
```

#### `oSyn query files`

Lists all indexed files with path, language, file size, and LLM-generated summary.

```bash
oSyn query files
```

#### `oSyn query file`

Returns a file's full metadata plus a compact listing of all its snippets (name, type, line range, description — no raw source).

```bash
oSyn query file --path <path>
```

| Flag | Short | Required | Description |
|------|-------|----------|-------------|
| `--path` | `-p` | yes | File path to describe |

#### `oSyn query snippet`

Returns the complete source code and metadata for a single snippet by UUID.

```bash
oSyn query snippet --id <uuid>
```

#### `oSyn query blast-radius`

Returns a snippet, every snippet that calls/references it (dependents), and every snippet it calls (dependencies). `blast_radius_count` is the number of dependents — a quick signal for how cautiously to treat a change.

```bash
oSyn query blast-radius --id <uuid>
```

#### `oSyn query deps`

Returns a snippet and the snippets it directly calls or references (outgoing edges only). Use `blast-radius` for the full bi-directional picture.

```bash
oSyn query deps --id <uuid>
```

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
| `LOCAL_LLM_MODEL` | `local-model` | Model name sent in the `"model"` field of LLM requests. |
| `MAX_CONCURRENCY` | `4` | Maximum number of files processed simultaneously during `index` and `watch`. |

---

## HTTP API

Start with `oSyn serve [--port 8080]`. All responses are JSON.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Returns `{"status":"ok"}` |
| `GET` | `/files` | List all indexed files |
| `GET` | `/files/{path}` | File metadata + snippet listing (no raw source) |
| `GET` | `/files/{path}/snippets` | Snippet listing only |
| `POST` | `/search` | Semantic search |
| `GET` | `/snippets/{id}` | Full snippet with raw source |
| `GET` | `/snippets/{id}/dependencies` | Outgoing edges (what this snippet calls) |
| `GET` | `/snippets/{id}/dependents` | Incoming edges (what calls this snippet) |
| `POST` | `/reindex` | Re-index a single file |

### Request / Response Examples

**`POST /search`**

```bash
curl -X POST http://localhost:8080/search \
     -H 'Content-Type: application/json' \
     -d '{"query": "cosine distance vector search", "limit": 3}'
```

```json
{
  "results": [
    {
      "snippet_id": "...",
      "name": "vecDistanceCosine",
      "snippet_type": "function",
      "line_start": 28,
      "line_end": 46,
      "description": "Computes cosine distance between two float32 blobs...",
      "raw_content": "func vecDistanceCosine(a, b []byte) float64 { ... }"
    }
  ]
}
```

**`GET /files/internal/db/queries.go`**

Returns `FileDetail` with the file record and a `snippets` array (no `raw_content`). Use `GET /snippets/{id}` for full source of individual snippets.

**`GET /snippets/{id}/dependents`**

```json
{
  "snippet": { "snippet_id": "...", "name": "IndexFile", ... },
  "dependents": [
    {
      "edge_type": "function_call",
      "snippet": { "snippet_id": "...", "name": "IndexDir", "snippet_type": "function", ... }
    }
  ]
}
```

**`POST /reindex`**

```bash
curl -X POST http://localhost:8080/reindex \
     -H 'Content-Type: application/json' \
     -d '{"path": "internal/db/queries.go"}'
```

---

## MCP Server

Start with `oSyn serve-mcp`. The server communicates over stdio using the Model Context Protocol (JSON-RPC 2.0) and is compatible with Claude Code and Claude Desktop.

### Configuration

Add to your Claude Code MCP settings (`.claude.json`):

```json
{
  "mcpServers": {
    "openSynapse": {
      "command": "/absolute/path/to/oSyn",
      "args": ["serve-mcp"],
      "env": {
        "DATABASE_PATH": "/absolute/path/to/opensynapse.db",
        "EMBED_PROVIDER": "local",
        "LOCAL_EMBED_URL": "http://127.0.0.1:8765",
        "EMBED_DIMENSION": "768"
      }
    }
  }
}
```

### Tools

All six tools are backed by the same `internal/service` implementations used by the HTTP API and CLI query commands.

---

#### `list_files`

Lists all indexed files with path, language, size, and LLM-generated summary.

**Inputs:** none

**Use when:** orienting yourself in an unfamiliar codebase before drilling into specific files.

---

#### `describe_file`

Returns a file's metadata and a compact listing of all its snippets (name, type, line range, description — no raw source).

**Inputs:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `path` | string | yes | Repository-relative or absolute path to the source file |

**Use when:** about to edit a file — get its full structure and responsibilities in one call.

---

#### `get_snippet`

Returns the complete source code and metadata for a single snippet.

**Inputs:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `snippet_id` | string | yes | UUID from `describe_file` or `search` |

---

#### `get_blast_radius`

Pre-edit safety analysis. Returns the snippet, every snippet that directly calls or references it (dependents), and every snippet it calls (dependencies). Includes `blast_radius_count` — the number of dependents — as a quick signal for how cautiously to treat a change.

**Inputs:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `snippet_id` | string | yes | UUID of the snippet to analyse |

**Use when:** before modifying any function, type, or variable — see what breaks.

**Response shape:**

```json
{
  "snippet": { "snippet_id": "...", "name": "...", "raw_content": "..." },
  "dependents": [
    { "edge_type": "function_call", "snippet": { "name": "...", "file_id": "..." } }
  ],
  "dependencies": [
    { "edge_type": "import_call", "snippet": { "name": "...", "file_id": "..." } }
  ],
  "blast_radius_count": 3
}
```

---

#### `search`

Semantic (vector) search over all indexed snippets. Returns the top N most similar results.

**Inputs:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `query` | string | yes | Natural language or code description |
| `limit` | integer | no | Results to return (default 5, max 20) |

**Use when:** you don't know the exact file or function name — find relevant code by meaning.

---

#### `get_dependencies`

Returns a snippet and the snippets it directly calls or references (outgoing edges only).

**Inputs:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `snippet_id` | string | yes | UUID of the snippet |

**Use when:** tracing an execution path or generating accurate documentation. Use `get_blast_radius` for the full bi-directional picture.

---

## Embedding Sidecar

The sidecar runs CodeRankEmbed (a 160M-parameter NomicBERT model) locally via ONNX Runtime, exposing a minimal HTTP API over localhost. No GPU required.

### Starting the sidecar

```bash
cd internal/vect-embed
pip install -r requirements.txt
python embedder.py --serve [PORT]
```

`PORT` defaults to `8765`.

### HTTP API

**`POST /embed`**

```json
{
  "texts": ["def hello(): ..."],
  "is_query": false
}
```

- `is_query` — when `true`, prepends `"Query: "` before tokenisation (for search queries, not indexing)

Response: `{"embeddings": [[0.91, -0.66, ...]]}` — 768-element `float32` vectors.

### Model files

```
internal/vect-embed/
├── embedder.py
├── requirements.txt
└── model/
    ├── config.json
    ├── tokenizer.json
    └── onnx/
        └── model.onnx
```

The `vect-embed/` directory is gitignored — model files stay local.

---

## Architecture

### Service layer

All tool operations are implemented once in `internal/service/service.go` and consumed by three surfaces:

```
internal/service/service.go   ← single source of tool logic
        ↑              ↑              ↑
internal/api/    internal/mcp/   cmd/oSyn/main.go
(HTTP handlers)  (MCP tools)     (query subcommands)
```

Adding a new operation means implementing it in `service.go` once, then adding thin wrappers in whichever surfaces need it.

### Pipeline phases

Each file goes through three sequential phases during indexing:

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

Embeddings are stored as raw little-endian IEEE 754 float32 blobs in `snippets.embedding BLOB`. The `vec_distance_cosine(a, b)` SQL function is registered as a Go callback via `go-sqlite3`'s `ConnectHook` — no shared extension library needed at runtime.

```sql
SELECT ... FROM snippets
WHERE embedding IS NOT NULL
ORDER BY vec_distance_cosine(embedding, ?) ASC
LIMIT ?
```

### Key packages

| Package | Path | Responsibility |
|---------|------|----------------|
| `service` | `internal/service/` | All tool operations — single source consumed by API, MCP, and CLI |
| `api` | `internal/api/` | HTTP REST server; thin wrappers over `service` |
| `mcp` | `internal/mcp/` | MCP stdio server; thin wrappers over `service` |
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
    UNIQUE(source_snippet_id, target_snippet_id, edge_type)
```

---

## Extending the System

### Add a tool operation

1. Implement the logic in `internal/service/service.go` with a clear return type.
2. Add an HTTP handler in `internal/api/handlers.go` and register a route in `server.go`.
3. Add an MCP tool in `internal/mcp/server.go` (`registerTools` + a handler method).
4. Add a CLI subcommand in `cmd/oSyn/main.go` under `queryCmd`.

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

If the provider distinguishes query vs document embeddings, implement the optional `EmbedQuery(ctx, text) ([]float32, error)` method — the pipeline detects and uses it via interface assertion.

### Add an edge type

1. Add a constant to `models.EdgeType` in `internal/models/models.go`.
2. Add detection logic in `internal/resolver/resolver.go`'s `ResolveFile` method.
