# openSynapse -- Reference

## Commands

Build with `go build -o oSyn ./cmd/oSyn/` or install with `go install github.com/Ars-Ludus/openSynapse/cmd/oSyn@latest`.

All commands that access a database auto-migrate the schema on startup. The `--repo <name>` flag is available on all commands to target a specific registered repo. Without it, the current repo is auto-detected from the working directory.

### Setup

```bash
oSyn init [--name <name>] [--path <dir>]  # register a repo, create its database
oSyn repos                                 # list all tracked repos
oSyn repos remove <name> [--delete-db]     # unregister a repo
oSyn config show                           # print resolved configuration
oSyn config set <key> <value>              # set a value in ~/.osyn/config.json
```

`init` registers the current directory (or `--path`) as a tracked repo. The name defaults to the directory name. Creates a new SQLite database in `~/.osyn/repos/`.

Config keys: `llm.provider`, `llm.base_url`, `llm.model`, `llm.api_key`, `embedding.provider`, `embedding.dimension`, `embedding.local_url`, `embedding.voyage_api_key`, `max_concurrency`.

### Indexing

```bash
oSyn index  [--path <dir>]           # full index of a directory tree
oSyn watch  [--path <dir>]           # initial index, then live incremental updates
```

`--path` defaults to the repo root from the registry. `index` crawls, parses, resolves edges, and enriches every source file. Re-indexing is idempotent -- old data is purged via CASCADE before new data is inserted. Files > 2 MB are skipped. Hidden directories, `vendor/`, and `node_modules/` are excluded.

All file paths stored in the database are **repo-relative** (e.g. `internal/db/db.go`), making databases portable if the repo moves on disk.

`watch` runs a full index, then monitors the filesystem with fsnotify (500 ms debounce). On file changes:
- **Content hash check** -- SHA-256 of file content is compared to the stored hash. If unchanged, the file is skipped entirely.
- **Deletion handling** -- removed/renamed files are deleted from the DB (CASCADE cleans up snippets and edges).
- **Edge cascade** -- when file A is re-indexed, files that had edges into A's old snippets are automatically re-resolved against A's new snippets without re-enrichment.

### Enrichment

```bash
oSyn enrich [--force]                # fill missing LLM descriptions (--force overwrites existing)
oSyn enrich-chains [--depth <n>]     # generate call-chain summaries (default depth: 3)
oSyn detect-patterns                 # detect architectural patterns across the graph
```

`enrich` generates LLM descriptions for files and snippets that are missing them. `--force` re-generates all descriptions.

`enrich-chains` walks outgoing edges from each function/method up to `--depth` levels, collects the chain of descriptions, and asks the LLM to summarize the execution path. Results are stored in `snippets.call_chain_summary`.

`detect-patterns` runs structural grouping (fan-out analysis, naming conventions) then sends candidate groups to the LLM for filtering and naming. Previous patterns are cleared and re-detected.

### Search

```bash
oSyn search <query> [--limit <n>]    # semantic search over snippets (default limit: 5)
```

Embeds the query and returns snippets by cosine distance. Requires a real embedding provider (`local` or `voyage`).

### Query

```bash
oSyn query files                     # list all indexed files
oSyn query file --path <path>        # file metadata + snippet listing (no raw source)
oSyn query snippet --id <uuid>       # full snippet with source, metadata, call chain
oSyn query blast-radius --id <uuid>  # dependents + dependencies (who breaks if I change this?)
oSyn query deps --id <uuid>          # outgoing edges only (what does this call?)
oSyn query patterns                  # list detected architectural patterns
```

All output is JSON. Pipe to `jq` for filtering.

### Servers

```bash
oSyn serve [--port <n>]              # HTTP REST API (default port: 8080)
oSyn serve-mcp                       # MCP server over stdio (for Claude Code / Claude Desktop)
oSyn migrate                         # explicit schema migration (rarely needed)
```

---

## Configuration

### Config file (`~/.osyn/config.json`)

```json
{
  "llm": {
    "provider": "openai-compat",
    "base_url": "http://192.168.1.1:8080/v1",
    "model": "local-model",
    "api_key": ""
  },
  "embedding": {
    "provider": "local",
    "dimension": 768,
    "local_url": "http://127.0.0.1:8765"
  },
  "max_concurrency": 4
}
```

Environment variables override config file values. `DATABASE_PATH` bypasses the registry entirely when set.

### Home directory layout

```
~/.osyn/
  config.json          # global settings (LLM, embedding, concurrency)
  registry.json        # repo name -> {root, db_path, last_indexed}
  repos/               # one SQLite DB per tracked repo
    my-project.db
    other-repo.db
```

### Repo resolution order

1. `DATABASE_PATH` env var (hard override, bypasses registry)
2. `--repo <name>` flag (looked up in registry)
3. Auto-detection: walk up from cwd looking for `.git/`, match root in registry

---

## Architecture

### Service layer

All operations are implemented once in `internal/service/service.go`. The four surfaces are thin wrappers:

```
internal/service/service.go      <-- single source of truth
      |          |          |          |
  cmd/gui/   internal/  internal/  cmd/oSyn/
  app.go     api/       mcp/       main.go
  (GUI)      (HTTP)     (MCP)      (CLI)
```

### Pipeline phases

```
Phase 1 -- Parse (concurrent, up to MAX_CONCURRENCY workers)
    Tree-sitter AST --> Snippets + imports + control-flow metadata
    Content hash check --> skip unchanged files

Phase 2 -- Resolve (sequential, all files stable in DB)
    Import-path matching --> EdgeImportCall edges
    Wikilink pruning     --> keep only symbols with real edges

Phase 3 -- Enrich (sequential)
    LLM file summary     --> code_files.file_summary
    LLM snippet desc     --> snippets.description
    Embedding            --> snippets.embedding (768-dim float32 blob)

Phase 4 -- Interface resolution (sequential, after all files)
    Method-set matching  --> EdgeTypeDefinition edges (struct implements interface)
```

### Incremental update pipeline (watcher)

```
fsnotify event --> debounce 500ms --> content hash check
    |
    +--> unchanged: skip
    +--> deleted:   DELETE FROM code_files (CASCADE)
    +--> modified:  capture dependent file IDs
                    --> delete + re-insert file
                    --> resolve edges
                    --> enrich (LLM + embedding)
                    --> re-resolve each dependent file's edges
```

### Key packages

| Package | Responsibility |
|---------|----------------|
| `service` | All tool operations -- consumed by GUI, API, MCP, CLI |
| `pipeline` | Orchestrates parse -> resolve -> enrich; concurrency semaphore; stores repo root |
| `parser` | Tree-sitter AST -> snippets, imports, control-flow metadata |
| `resolver` | Cross-file edge creation; interface-to-implementation matching |
| `enrichment` | LLM descriptions (`llm.go`), embeddings (`embedder.go`), pattern detection (`patterns.go`) |
| `db` | SQLite connection, schema migration, all queries |
| `watcher` | fsnotify loop with debounce, deletion handling, edge cascade |
| `crawler` | Directory walk (returns repo-relative paths), file reading, language detection |
| `models` | Shared types: `CodeFile`, `Snippet`, `Edge`, `Pattern`, `SnippetMetadata` |
| `config` | Config file loading, env var overlay, repo resolution |
| `registry` | Repo registry CRUD (`~/.osyn/registry.json`) |
| `api` | HTTP REST server |
| `mcp` | MCP stdio server |
| `cmd/gui` | Wails desktop app |

---

## Data Model

Six tables. Snippet and edge deletions cascade from `code_files`.

All file paths are stored **repo-relative** (e.g. `internal/db/db.go`), not absolute.

### code_files

| Column | Type | Notes |
|--------|------|-------|
| `file_id` | TEXT PK | UUID |
| `path` | TEXT UNIQUE | Repo-relative path; `lib:` prefix for synthetic library entries |
| `language` | TEXT | `go`, `python`, `javascript`, `typescript`, `rust`, `external`, `unknown` |
| `dependencies` | TEXT | JSON array of import paths |
| `file_summary` | TEXT | LLM-generated paragraph |
| `file_size` | INTEGER | Bytes |
| `last_modified` | INTEGER | Unix timestamp |
| `content_hash` | TEXT | SHA-256 hex; used to skip no-op re-indexes |

### snippets

| Column | Type | Notes |
|--------|------|-------|
| `snippet_id` | TEXT PK | UUID |
| `file_id` | TEXT FK | CASCADE DELETE from code_files |
| `snippet_type` | TEXT | `function`, `method`, `struct`, `interface`, `class`, `variable`, `constant`, `external`, `unknown` |
| `name` | TEXT | Symbol identifier |
| `line_start` | INTEGER | 1-based |
| `line_end` | INTEGER | 1-based |
| `raw_content` | TEXT | Full source text of the snippet |
| `description` | TEXT | LLM-generated one-sentence summary |
| `wikilinks` | TEXT | JSON array of referenced symbol names (pruned to real edges) |
| `embedding` | BLOB | 768 x float32, little-endian IEEE 754 |
| `metadata` | TEXT | JSON `SnippetMetadata` (see below) |
| `call_chain_summary` | TEXT | LLM-generated execution path narrative |

#### SnippetMetadata (JSON in `metadata` column)

```json
{
  "returns_error": true,
  "early_returns": 2,
  "branch_count": 3,
  "goroutine_spawns": 1,
  "channel_ops": 0,
  "uses_mutex": false,
  "has_panic": false,
  "has_recover": false,
  "has_defer": true,
  "receiver": "Pipeline",
  "interface_methods": ["Read", "Write", "Close"]
}
```

All fields are omitted when zero/false. `receiver` is set on Go method snippets. `interface_methods` is set on Go interface snippets.

### edges

| Column | Type | Notes |
|--------|------|-------|
| `edge_id` | TEXT PK | UUID |
| `source_snippet_id` | TEXT FK | CASCADE DELETE from snippets |
| `target_snippet_id` | TEXT FK | CASCADE DELETE from snippets |
| `edge_type` | TEXT | `import_call`, `variable_ref`, `type_definition`, `function_call`, `inheritance` |
| `merged_context` | TEXT | Optional LLM-generated description of the relationship |

UNIQUE constraint on `(source_snippet_id, target_snippet_id, edge_type)`.

**Edge type semantics:**
- `import_call` -- snippet A references an exported symbol defined in snippet B (cross-file)
- `type_definition` -- struct A implements interface B (method-set satisfaction)

### patterns

| Column | Type | Notes |
|--------|------|-------|
| `pattern_id` | TEXT PK | UUID |
| `name` | TEXT | LLM-generated pattern name |
| `description` | TEXT | LLM-generated explanation |
| `pattern_type` | TEXT | `fan_out`, `naming` |
| `embedding` | BLOB | For semantic search over patterns |

### pattern_snippets (join table)

| Column | Type | Notes |
|--------|------|-------|
| `pattern_id` | TEXT FK | CASCADE DELETE from patterns |
| `snippet_id` | TEXT FK | CASCADE DELETE from snippets |

### wikilink_colors (GUI persistence)

| Column | Type | Notes |
|--------|------|-------|
| `id` | TEXT PK | UUID |
| `snippet_id` | TEXT FK | CASCADE DELETE from snippets |
| `wikilink` | TEXT | Symbol name |
| `color` | TEXT | Hex color string |

---

## HTTP API

Start with `oSyn serve [--port 8080]`. All responses are JSON.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | `{"status":"ok"}` |
| GET | `/files` | List all indexed files |
| GET | `/files/{path}` | File metadata + snippet listing (no raw source) |
| GET | `/files/{path}/snippets` | Snippet listing only |
| POST | `/search` | Semantic search: `{"query": "...", "limit": 5}` |
| GET | `/snippets/{id}` | Full snippet with source, metadata, call chain summary |
| GET | `/snippets/{id}/dependencies` | Outgoing edges |
| GET | `/snippets/{id}/dependents` | Incoming edges |
| GET | `/patterns` | List detected patterns |
| POST | `/reindex` | Re-index a file: `{"path": "..."}` |

### Examples

```bash
# Semantic search
curl -X POST http://localhost:8080/search \
     -H 'Content-Type: application/json' \
     -d '{"query": "cosine distance", "limit": 3}'

# Blast radius
curl http://localhost:8080/snippets/<uuid>/dependents

# Patterns
curl http://localhost:8080/patterns
```

---

## MCP Server

Start with `oSyn serve-mcp`. Communicates over stdio using JSON-RPC 2.0. Compatible with Claude Code and Claude Desktop.

### Configuration

Add to `.claude.json`:

```json
{
  "mcpServers": {
    "openSynapse": {
      "command": "oSyn",
      "args": ["serve-mcp", "--repo", "my-project"]
    }
  }
}
```

No environment variables needed. For multi-repo setups, add multiple server entries with different `--repo` names.

### Tools (9 total)

| Tool | Inputs | Purpose |
|------|--------|---------|
| `list_files` | -- | Orient in an unfamiliar codebase |
| `describe_file` | `path` | File structure and responsibilities before editing |
| `get_snippet` | `snippet_id` | Full source, metadata, call chain summary |
| `get_blast_radius` | `snippet_id` | Pre-edit safety: who depends on this, what does it depend on |
| `search` | `query`, `limit?` | Find code by meaning when you don't know the name |
| `get_dependencies` | `snippet_id` | Trace outgoing execution paths |
| `get_patterns` | -- | Understand "how things are done here" |
| `get_implementations` | `snippet_id` | Find concrete types implementing an interface |

---

## Embedding Sidecar

Runs CodeRankEmbed (160M-parameter NomicBERT, ONNX) locally. No GPU required.

```bash
cd internal/vect-embed
pip install -r requirements.txt
python embedder.py --serve 8765
```

### API

`POST /embed` with `{"texts": ["..."], "is_query": false}` returns `{"embeddings": [[...]]}` (768-dim float32 vectors). Set `is_query: true` for search queries (prepends "Query: " before tokenization).

### Model files

Place ONNX model files in `internal/vect-embed/model/`:

```
model/
  config.json
  tokenizer.json
  onnx/
    model.onnx
```

The `vect-embed/` directory is gitignored.

---

## Desktop GUI

Native application built with Wails v2 (Go backend) + Svelte 5 (frontend). Reads from the same SQLite database as all other surfaces.

### Building

```bash
make gui          # dev mode with hot reload
make gui-run      # production build + launch
make gui-build    # production build only
```

First-time setup:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
make gui-deps     # npm install in frontend/
# Linux: sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev
```

### Layout

```
+-----------------+------------------------------------------+
|  EXPLORER       |  [Snippet Assembly] [File Info] [Code]   |
|                 |                                          |
|  > cmd/         |  +- myFunction -- function -- 12-45 --+ |
|    v internal/  |  | Description...                      | |
|      > db/      |  | [[Wikilink]] [[AnotherRef]]         | |
|      > parser/  |  +------------------------------------+ |
+-----------------+------------------------------------------+
```

- **Sidebar** -- collapsible directory tree of all indexed files
- **Editor area** -- up to two file panels side by side, each with three tabs:
  - **Snippet Assembly** -- all snippets ordered by line, with type badge, name, description, and wikilink chips
  - **File Info** -- path, language, dependencies, size, last modified, LLM summary
  - **Code** -- assembled raw source with line numbers
- **Wikilink coloring** -- right-click any `[[symbol]]` chip to color it. Colors propagate to all edge-connected snippets referencing the same symbol and persist in the database.

---

## Extending the system

### Add a tool operation

1. Implement in `internal/service/service.go`
2. Add HTTP handler in `internal/api/handlers.go` + route in `server.go`
3. Add MCP tool in `internal/mcp/server.go`
4. Add CLI subcommand in `cmd/oSyn/main.go`
5. Optionally add GUI binding in `cmd/gui/app.go` + regenerate TS bindings with `wails generate module`

### Add a language

1. Import the Tree-sitter grammar in `internal/parser/parser.go`
2. Add a case to `languageFor()` returning the `*sitter.Language`
3. Add entries to `topLevelNodeTypes` for snippet extraction
4. Add import extraction in `extractImportSpecs()`
5. Add file extension mapping in `internal/crawler/`

### Add an embedding provider

Implement `enrichment.Embedder`:

```go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}
```

Optionally implement `EmbedQuery(ctx, text) ([]float32, error)` for providers that distinguish query vs document embeddings.

### Add an edge type

1. Add a constant to `models.EdgeType`
2. Add detection logic in `internal/resolver/resolver.go`
