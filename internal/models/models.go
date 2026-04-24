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
	LangExternal   Language = "external" // synthetic entries for stdlib/third-party libs
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
	SnippetExternal  SnippetType = "external" // synthetic entry for a library symbol
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
	ContentHash  string    `db:"content_hash"` // SHA-256 of file content
}

// SnippetMetadata holds control-flow and structural metadata extracted from the AST.
type SnippetMetadata struct {
	ReturnsError    bool   `json:"returns_error,omitempty"`
	EarlyReturns    int    `json:"early_returns,omitempty"`
	BranchCount     int    `json:"branch_count,omitempty"`     // if/switch/select statements
	GoroutineSpawns int    `json:"goroutine_spawns,omitempty"` // go keyword
	ChannelOps      int    `json:"channel_ops,omitempty"`      // <- operator
	UsesMutex       bool   `json:"uses_mutex,omitempty"`
	HasPanic        bool   `json:"has_panic,omitempty"`
	HasRecover      bool   `json:"has_recover,omitempty"`
	HasDefer        bool   `json:"has_defer,omitempty"`
	Receiver        string `json:"receiver,omitempty"`          // Go method receiver type name
	InterfaceMethods []string `json:"interface_methods,omitempty"` // method names for interface types
}

// Snippet is an atomic, semantically coherent code unit extracted from an AST (Table 2).
type Snippet struct {
	SnippetID        uuid.UUID       `db:"snippet_id"`
	FileID           uuid.UUID       `db:"file_id"`
	LineStart        int             `db:"line_start"`
	LineEnd          int             `db:"line_end"`
	Wikilinks        []string        `db:"wikilinks"` // symbol names referenced
	RawContent       string          `db:"raw_content"`
	Description      string          `db:"description"`
	Embedding        []float32       `db:"embedding"`
	SnippetType      SnippetType     `db:"snippet_type"`
	Name             string          `db:"name"`
	Metadata         SnippetMetadata `db:"metadata"`
	CallChainSummary string          `db:"call_chain_summary"`
}

// Pattern represents a detected structural or behavioral pattern across multiple snippets.
type Pattern struct {
	PatternID   uuid.UUID   `db:"pattern_id"`
	Name        string      `db:"name"`
	Description string      `db:"description"`
	PatternType string      `db:"pattern_type"` // "fan_out", "naming", "signature"
	SnippetIDs  []uuid.UUID `db:"-"`            // populated from join table
	Embedding   []float32   `db:"embedding"`
}

// Edge encodes a directed reference relationship between two snippets (Table 3).
type Edge struct {
	EdgeID          uuid.UUID `db:"edge_id"`
	SourceSnippetID uuid.UUID `db:"source_snippet_id"`
	TargetSnippetID uuid.UUID `db:"target_snippet_id"`
	EdgeType        EdgeType  `db:"edge_type"`
	MergedContext   string    `db:"merged_context"`
}
