Technical Blueprint: openSynapse (oSyn)
1. System Overview
A Knowledge Graph-based static analysis system that maps codebases into discrete, semantically enriched units (Snippets) and their inter-relationships (Edges). The system uses Tree-sitter for AST parsing and LLMs for summarization and vector embeddings.
2. Data Schema (The Three-Table Architecture)
2.1 Table: Code Files
file_id: Primary Key (UUID or hashed file path).
path: Full directory path and filename.
dependencies: List of imports (internal/external).
file_summary: LLM-generated high-level description of the file's purpose.
metadata: File size, last modified, language.
2.2 Table: Snippets
snippet_id: Primary Key (Unique ID).
file_id: Foreign Key (References Code Files).
line_range: [start_line, end_line].
wikilinks: List of extracted symbols/references (variables, functions).
raw_content: The raw source code of the snippet.
description: LLM-generated explanation of the snippet's logic.
embedding: 768-dimensional vector (Model: coderankembed via ONNX).
2.3 Table: Edges (The Reference Graph)
edge_id: Primary Key.
source_snippet_id: Reference to the origin snippet.
target_snippet_id: Reference to the destination snippet.
edge_type: (e.g., import_call, variable_ref, type_definition).
merged_context: LLM-generated description of the relationship/flow between these snippets.
3. Processing Pipeline (Implementation Logic)
Phase 1: Ingestion & Parsing (The Crawler)
Walk Directory: Recursively scan the repository.
AST Generation: Use Tree-sitter to parse source files into Abstract Syntax Trees.
Atomic Segmentation: Break the AST into logical nodes (functions, classes, variable declarations) to create the Snippets table.
Phase 2: Namespace Resolution
Import Mapping: Resolve relative and aliased imports (e.g., @/utils -> ./src/utils/index.ts).
Edge Creation: Match symbols in Snippet A that are defined in Snippet B across different files to populate the Edges table.
Phase 3: Semantic Enrichment
LLM Summarization: Pass snippets and edge-pairs to an LLM to generate natural language descriptions.
Vectorization: Generate 768-dim embeddings for each snippet to enable semantic search and "code similarity" evaluations.
4. Maintenance & Synchronicity (The Watcher)
Event Trigger: Utilize fswatch or chokidar to listen for file system changes.
Incremental Update Logic:
On Change: Identify affected file_id.
Purge & Replace: Delete existing snippets and edges associated with the file.
Re-index: Rerun the pipeline for that specific file.
Re-resolve: Check if external dependencies of other files are affected by this change (re-resolving edges).
5. Technical Requirements
Parser: Tree-sitter (multi-language support).
Inference Engine: ONNX Runtime (for local embedding generation).
Database: A relational database (PostgreSQL) or a Graph Database (Neo4j) for the Edges table.
LLM Integration: OpenAI/Anthropic API or local Llama via Ollama.
