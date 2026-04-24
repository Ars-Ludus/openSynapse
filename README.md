# openSynapse

A knowledge graph for codebases. openSynapse parses source files into an AST, extracts every function, method, struct, and class as a discrete **snippet**, resolves the cross-file relationships between them as **edges**, then enriches the graph with LLM descriptions, semantic embeddings, control-flow metadata, interface resolution, and architectural pattern detection.

The result is a self-contained SQLite database that a human or AI agent can query to answer structural questions about a codebase with precision rather than fuzzy search.

## How it works

```
Source files
    |
    v
 Tree-sitter AST ---> Snippets (functions, methods, structs, ...)
    |                  Edges    (import_call, type_definition, ...)
    v                  Metadata (error paths, concurrency, branching)
 LLM enrichment  ---> Descriptions, call-chain summaries, patterns
    |
    v
 CodeRankEmbed   ---> 768-dim semantic vectors
    |
    v
 SQLite database ---> CLI, HTTP API, MCP tools, Desktop GUI
```

**Four surfaces** consume the same service layer:

| Surface | Use case |
|---------|----------|
| CLI | `oSyn search "auth middleware"`, `oSyn query blast-radius --id <uuid>` |
| HTTP API | Programmatic access, integrations |
| MCP server | Direct AI agent integration (Claude Code, Claude Desktop) |
| Desktop GUI | Visual exploration (Wails v2 + Svelte 5) |

## Installation

### From source

```bash
go install github.com/Ars-Ludus/openSynapse/cmd/oSyn@latest
```

Requires CGO (Tree-sitter and go-sqlite3 are C libraries). You need a C compiler (`gcc`, `clang`, or equivalent).

### From release binaries

Download from [GitHub Releases](https://github.com/Ars-Ludus/openSynapse/releases). Archives are available for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.

### Build from checkout

```bash
git clone https://github.com/Ars-Ludus/openSynapse.git
cd openSynapse
go build -o oSyn ./cmd/oSyn/
```

## Quick start

### 1. First-time setup

```bash
# Configure LLM enrichment (optional, any OpenAI-compatible endpoint)
oSyn config set llm.provider openai-compat
oSyn config set llm.base_url http://192.168.1.1:8080/v1
oSyn config set llm.model local-model

# Configure embeddings (required for semantic search)
oSyn config set embedding.provider local
oSyn config set embedding.local_url http://127.0.0.1:8765
```

Settings are stored in `~/.osyn/config.json`. Environment variables (`LLM_PROVIDER`, `EMBED_PROVIDER`, etc.) still override for CI/scripting.

### 2. Start the embedding sidecar

```bash
cd internal/vect-embed
pip install -r requirements.txt
python embedder.py --serve 8765
```

### 3. Register and index a repo

```bash
cd /path/to/your/repo
oSyn init                                # registers the repo in ~/.osyn/
oSyn index                               # builds the knowledge graph
```

Each repo gets its own SQLite database in `~/.osyn/repos/`. After `init`, all commands auto-detect which repo you're in based on your working directory.

### 4. Query

```bash
oSyn search "how does authentication work"
oSyn query blast-radius --id <uuid>      # what breaks if I change this?
oSyn query patterns                      # detected architectural conventions
```

### 5. Live incremental updates

```bash
oSyn watch
```

The watcher monitors the filesystem, skips files whose content hasn't changed (SHA-256), re-indexes modified files, cleans up deleted files, and cascades edge updates to dependent files automatically.

### 6. Connect to Claude via MCP

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

No environment variables needed — `oSyn` reads `~/.osyn/config.json` for LLM/embedding settings and the registry for the database path.

Nine MCP tools: `list_files`, `describe_file`, `get_snippet`, `get_blast_radius`, `search`, `get_dependencies`, `get_patterns`, `get_implementations`, `get_call_chain`.

## Multi-repo management

```bash
# Register repos
cd ~/projects/backend && oSyn init
cd ~/projects/frontend && oSyn init --name ui

# List all tracked repos
oSyn repos

# Query a specific repo from anywhere
oSyn search "auth middleware" --repo backend

# Remove a repo
oSyn repos remove ui --delete-db
```

## What the graph captures

| Layer | What it knows | How |
|-------|--------------|-----|
| **Structure** | Every function, method, struct, interface, class, constant, variable | Tree-sitter AST extraction |
| **Relationships** | Which snippets call, import, or reference each other | Cross-file edge resolution |
| **Types** | Which structs implement which interfaces | Method-set matching |
| **Behavior** | Error returns, branching complexity, goroutine spawns, mutex usage, panic/recover, defer | AST control-flow analysis |
| **Semantics** | What each snippet does, in one sentence | LLM description |
| **Execution paths** | What happens when a function is called, 3 levels deep | LLM call-chain summaries |
| **Conventions** | Recurring structural patterns across the codebase | Structural grouping + LLM synthesis |
| **Similarity** | Which snippets are semantically related to a query | 768-dim cosine vector search |

## Environment variables

Environment variables override `~/.osyn/config.json` settings. Useful for CI or one-off runs.

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_PATH` | (auto from registry) | SQLite database path — bypasses registry when set |
| `EMBED_PROVIDER` | `null` | `local`, `voyage`, or `null` (no vectors) |
| `EMBED_DIMENSION` | `768` | Must match model output dimension |
| `LOCAL_EMBED_URL` | `http://127.0.0.1:8765` | Embedding sidecar URL |
| `VOYAGE_API_KEY` | -- | Required when `EMBED_PROVIDER=voyage` |
| `LLM_PROVIDER` | -- | `openai-compat`, `gemini`, or empty (disabled) |
| `LLM_BASE_URL` | -- | OpenAI-compatible `/v1` base URL |
| `LLM_MODEL` | `local-model` | Model name in LLM requests |
| `LLM_API_KEY` | -- | Bearer token (any string for local servers) |
| `MAX_CONCURRENCY` | `4` | Parallel file indexing workers |

## Supported languages

Go and Python have full Tree-sitter grammar support. JavaScript, TypeScript, and Rust have file detection but grammar integration is in progress.

Adding a language: register the Tree-sitter grammar in `internal/parser/parser.go`, add top-level node types, and add the file extension in `internal/crawler/`.

## Further reading

See [DOCUMENTATION.md](DOCUMENTATION.md) for the full command reference, HTTP API endpoints, MCP tool definitions, data model, GUI internals, and extension guide.
