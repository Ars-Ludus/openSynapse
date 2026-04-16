package models

import (
	"time"

	"github.com/google/uuid"
)

// Language identifies the programming language of a source file.
type Language string

const (
	LangGo         Language = "go"
	LangPython     Language = "python"
	LangJavaScript Language = "javascript"
	LangTypeScript Language = "typescript"
	LangRust       Language = "rust"
	LangUnknown    Language = "unknown"
)

// EdgeType describes the semantic relationship between two snippets.
type EdgeType string

const (
	EdgeImportCall     EdgeType = "import_call"
	EdgeVariableRef    EdgeType = "variable_ref"
	EdgeTypeDefinition EdgeType = "type_definition"
	EdgeFunctionCall   EdgeType = "function_call"
	EdgeInheritance    EdgeType = "inheritance"
)

// SnippetType categorises an AST-level code unit.
type SnippetType string

const (
	SnippetFunction  SnippetType = "function"
	SnippetMethod    SnippetType = "method"
	SnippetClass     SnippetType = "class"
	SnippetStruct    SnippetType = "struct"
	SnippetInterface SnippetType = "interface"
	SnippetVariable  SnippetType = "variable"
	SnippetConstant  SnippetType = "constant"
	SnippetUnknown   SnippetType = "unknown"
)

// CodeFile represents a source file in the repository (Table 1).
type CodeFile struct {
	FileID       uuid.UUID `db:"file_id"`
	Path         string    `db:"path"`
	Dependencies []string  `db:"dependencies"` // resolved import paths
	FileSummary  string    `db:"file_summary"`
	Language     Language  `db:"language"`
	FileSize     int64     `db:"file_size"`
	LastModified time.Time `db:"last_modified"`
}

// Snippet is an atomic, semantically coherent code unit extracted from an AST (Table 2).
type Snippet struct {
	SnippetID   uuid.UUID   `db:"snippet_id"`
	FileID      uuid.UUID   `db:"file_id"`
	LineStart   int         `db:"line_start"`
	LineEnd     int         `db:"line_end"`
	Wikilinks   []string    `db:"wikilinks"` // symbol names referenced
	RawContent  string      `db:"raw_content"`
	Description string      `db:"description"`
	Embedding   []float32   `db:"embedding"`
	SnippetType SnippetType `db:"snippet_type"`
	Name        string      `db:"name"`
}

// Edge encodes a directed reference relationship between two snippets (Table 3).
type Edge struct {
	EdgeID          uuid.UUID `db:"edge_id"`
	SourceSnippetID uuid.UUID `db:"source_snippet_id"`
	TargetSnippetID uuid.UUID `db:"target_snippet_id"`
	EdgeType        EdgeType  `db:"edge_type"`
	MergedContext   string    `db:"merged_context"`
}
