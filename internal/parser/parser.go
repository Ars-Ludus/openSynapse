// Package parser uses Tree-sitter to extract atomic code snippets from source files.
// CGO is required: the tree-sitter C library is linked at compile time.
package parser

import (
	"context"
	"path"
	"strings"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"
	tree_golang "github.com/smacker/go-tree-sitter/golang"
	tree_python "github.com/smacker/go-tree-sitter/python"
	tree_typescript "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/Ars-Ludus/openSynapse/internal/models"
)

// ImportSpec holds a parsed import with its local qualifier (the identifier used
// to reference the package in code, e.g. "http" for "net/http").
type ImportSpec struct {
	Path      string // full import path, e.g. "net/http"
	Qualifier string // local name, e.g. "http" or an explicit alias
}

// ExternalRef is a cross-package symbol reference extracted from selector
// expressions (e.g. http.ListenAndServe → {Qualifier:"http", Symbol:"ListenAndServe"}).
// These are filtered against ImportSpecs in the resolver so false positives
// (e.g. myVar.Field) are discarded.
type ExternalRef struct {
	Qualifier string // local package alias referenced in the snippet
	Symbol    string // exported symbol name
}

// ParsedSnippet is the raw output of the parser before DB insertion.
type ParsedSnippet struct {
	Name         string
	SnippetType  models.SnippetType
	LineStart    int // 1-based
	LineEnd      int // 1-based
	RawContent   string
	Wikilinks    []string              // exported symbols that may cross to other indexed files
	ExternalRefs []ExternalRef         // package.Symbol references for lib edge creation
	Metadata     models.SnippetMetadata // control-flow metadata from AST
}

// ParsedFile is the full parse result for one source file.
type ParsedFile struct {
	Imports     []string     // backward-compat: full import paths only
	ImportSpecs []ImportSpec // richer form with local qualifier names
	Snippets    []*ParsedSnippet
}

// Parse extracts snippets and imports from source content using Tree-sitter.
func Parse(ctx context.Context, lang models.Language, content []byte) (*ParsedFile, error) {
	tsLang := languageFor(lang)
	if tsLang == nil {
		// Language not yet supported — return empty result
		return &ParsedFile{}, nil
	}

	p := sitter.NewParser()
	p.SetLanguage(tsLang)

	tree, err := p.ParseCtx(ctx, nil, content)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	root := tree.RootNode()

	result := &ParsedFile{}
	result.ImportSpecs = extractImportSpecs(root, content, lang)
	for _, spec := range result.ImportSpecs {
		result.Imports = append(result.Imports, spec.Path)
	}
	result.Snippets = extractSnippets(root, content, lang)

	return result, nil
}

// languageFor maps our language enum to the tree-sitter grammar.
func languageFor(lang models.Language) *sitter.Language {
	switch lang {
	case models.LangGo:
		return tree_golang.GetLanguage()
	case models.LangPython:
		return tree_python.GetLanguage()
	case models.LangTypeScript:
		return tree_typescript.GetLanguage()
	default:
		return nil
	}
}

// ── Import extraction ─────────────────────────────────────────────────────────

func extractImportSpecs(root *sitter.Node, content []byte, lang models.Language) []ImportSpec {
	switch lang {
	case models.LangGo:
		return goImportSpecs(root, content)
	case models.LangPython:
		return pythonImportSpecs(root, content)
	case models.LangTypeScript:
		return typescriptImportSpecs(root, content)
	default:
		return nil
	}
}

func goImportSpecs(root *sitter.Node, content []byte) []ImportSpec {
	var specs []ImportSpec
	walkNodes(root, func(n *sitter.Node) bool {
		if n.Type() == "import_spec" {
			pathNode := n.ChildByFieldName("path")
			if pathNode == nil {
				return true
			}
			importPath := strings.Trim(string(content[pathNode.StartByte():pathNode.EndByte()]), `"`)
			if importPath == "" {
				return true
			}

			// Determine qualifier: explicit alias takes priority, otherwise last segment.
			qualifier := path.Base(importPath)
			if aliasNode := n.ChildByFieldName("name"); aliasNode != nil {
				alias := string(content[aliasNode.StartByte():aliasNode.EndByte()])
				if alias != "" && alias != "." && alias != "_" {
					qualifier = alias
				}
			}
			specs = append(specs, ImportSpec{Path: importPath, Qualifier: qualifier})
		}
		return true
	})
	return specs
}

func pythonImportSpecs(root *sitter.Node, content []byte) []ImportSpec {
	var specs []ImportSpec
	walkNodes(root, func(n *sitter.Node) bool {
		switch n.Type() {
		case "import_statement":
			for i := 0; i < int(n.NamedChildCount()); i++ {
				child := n.NamedChild(i)
				if child.Type() == "dotted_name" || child.Type() == "aliased_import" {
					name := string(content[child.StartByte():child.EndByte()])
					specs = append(specs, ImportSpec{Path: name, Qualifier: path.Base(name)})
				}
			}
		case "import_from_statement":
			mod := n.ChildByFieldName("module_name")
			if mod != nil {
				modPath := string(content[mod.StartByte():mod.EndByte()])
				specs = append(specs, ImportSpec{Path: modPath, Qualifier: path.Base(modPath)})
			}
		}
		return true
	})
	return specs
}

func typescriptImportSpecs(root *sitter.Node, content []byte) []ImportSpec {
	var specs []ImportSpec
	walkNodes(root, func(n *sitter.Node) bool {
		if n.Type() != "import_statement" {
			return true
		}
		// In the TypeScript grammar, import_statement named children are:
		//   [0] import_clause (optional, absent for side-effect imports)
		//   [1] string  (module path)
		// Neither is exposed as a named field — iterate by type.
		modulePath := tsImportPath(n, content)
		if modulePath == "" {
			return true
		}
		var importClause *sitter.Node
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if n.NamedChild(i).Type() == "import_clause" {
				importClause = n.NamedChild(i)
				break
			}
		}
		if importClause == nil {
			return true // side-effect import: import './foo'
		}
		for i := 0; i < int(importClause.NamedChildCount()); i++ {
			child := importClause.NamedChild(i)
			switch child.Type() {
			case "identifier":
				// Default import: import Foo from '...'
				specs = append(specs, ImportSpec{
					Path:      modulePath,
					Qualifier: string(content[child.StartByte():child.EndByte()]),
				})
			case "namespace_import":
				// Namespace import: import * as Foo from '...'
				for j := 0; j < int(child.NamedChildCount()); j++ {
					if child.NamedChild(j).Type() == "identifier" {
						specs = append(specs, ImportSpec{
							Path:      modulePath,
							Qualifier: string(content[child.NamedChild(j).StartByte():child.NamedChild(j).EndByte()]),
						})
						break
					}
				}
			case "named_imports":
				// Named imports: import { A, B as C } from '...'
				for j := 0; j < int(child.NamedChildCount()); j++ {
					spec := child.NamedChild(j)
					if spec.Type() != "import_specifier" {
						continue
					}
					// import_specifier named children: [name_identifier] or [name_identifier, alias_identifier]
					// Use the last identifier as the local name (alias wins over original name).
					count := int(spec.NamedChildCount())
					if count == 0 {
						continue
					}
					local := spec.NamedChild(count - 1)
					specs = append(specs, ImportSpec{
						Path:      modulePath,
						Qualifier: string(content[local.StartByte():local.EndByte()]),
					})
				}
			}
		}
		return true
	})
	return specs
}

// tsImportPath extracts the quoted module path from an import_statement node.
func tsImportPath(n *sitter.Node, content []byte) string {
	var result string
	walkNodes(n, func(inner *sitter.Node) bool {
		if inner.Type() == "string" {
			raw := string(content[inner.StartByte():inner.EndByte()])
			result = strings.Trim(raw, "\"'`")
			return false
		}
		return true
	})
	return result
}

// tsUnwrapExport returns the inner declaration node from a TypeScript
// export_statement. Returns nil when no recognized declaration is present
// (e.g. re-exports: export { A } from '...').
func tsUnwrapExport(node *sitter.Node, nodeTypes map[string]models.SnippetType) *sitter.Node {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "export_statement" {
			continue // don't recurse into nested export_statement sentinels
		}
		if _, ok := nodeTypes[child.Type()]; ok {
			return child
		}
	}
	return nil
}

// ── Snippet extraction ────────────────────────────────────────────────────────

// topLevelNodeTypes maps languages to the node types we treat as independent snippets.
var topLevelNodeTypes = map[models.Language]map[string]models.SnippetType{
	models.LangGo: {
		"function_declaration": models.SnippetFunction,
		"method_declaration":   models.SnippetMethod,
		"type_declaration":     models.SnippetStruct,
		"const_declaration":    models.SnippetConstant,
		"var_declaration":      models.SnippetVariable,
	},
	models.LangPython: {
		"function_definition": models.SnippetFunction,
		"class_definition":    models.SnippetClass,
	},
	models.LangTypeScript: {
		"function_declaration":       models.SnippetFunction,
		"class_declaration":          models.SnippetClass,
		"abstract_class_declaration": models.SnippetClass,
		"interface_declaration":      models.SnippetInterface,
		"type_alias_declaration":     models.SnippetStruct,
		"enum_declaration":           models.SnippetConstant,
		"lexical_declaration":        models.SnippetVariable,
		"export_statement":           models.SnippetUnknown, // sentinel — unwrapped in extractSnippets
	},
}

func extractSnippets(root *sitter.Node, content []byte, lang models.Language) []*ParsedSnippet {
	nodeTypes, ok := topLevelNodeTypes[lang]
	if !ok {
		return nil
	}

	var snippets []*ParsedSnippet
	// Walk only top-level children of the root (source_file node).
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		st, matched := nodeTypes[child.Type()]
		if !matched {
			continue
		}

		// TypeScript: export_statement wraps the actual declaration.
		// Unwrap to the inner node so name, type, and metadata are correct.
		if lang == models.LangTypeScript && child.Type() == "export_statement" {
			if inner := tsUnwrapExport(child, nodeTypes); inner != nil {
				child = inner
				st = nodeTypes[child.Type()]
			} else {
				continue // no recognized declaration inside the export
			}
		}

		raw := string(content[child.StartByte():child.EndByte()])
		name := extractName(child, content, lang)
		lineStart := int(child.StartPoint().Row) + 1
		lineEnd := int(child.EndPoint().Row) + 1
		wikilinks := extractWikilinks(child, content, lang)
		externalRefs := extractExternalRefs(child, content, lang)
		meta := extractMetadata(child, content, lang)

		// Refine type_declaration into struct vs interface.
		actualType := st
		if lang == models.LangGo && child.Type() == "type_declaration" {
			actualType = classifyGoTypeDecl(child, content)
		}

		snippets = append(snippets, &ParsedSnippet{
			Name:         name,
			SnippetType:  actualType,
			LineStart:    lineStart,
			LineEnd:      lineEnd,
			RawContent:   raw,
			Wikilinks:    wikilinks,
			ExternalRefs: externalRefs,
			Metadata:     meta,
		})
	}

	// Prepend a header snippet for any content before the first captured snippet
	// (package declaration, doc comments, import block). This ensures the code
	// assembly tab can reconstruct the full file.
	if len(snippets) > 0 && snippets[0].LineStart > 1 {
		headerEnd := snippets[0].LineStart - 1
		// Find the byte offset of the end of the header (start of first snippet).
		firstStart := uint32(0)
		for i := 0; i < int(root.NamedChildCount()); i++ {
			child := root.NamedChild(i)
			if int(child.StartPoint().Row)+1 == snippets[0].LineStart {
				firstStart = child.StartByte()
				break
			}
		}
		if firstStart > 0 {
			raw := strings.TrimRight(string(content[:firstStart]), "\n") + "\n"
			snippets = append([]*ParsedSnippet{{
				Name:        "",
				SnippetType: models.SnippetUnknown,
				LineStart:   1,
				LineEnd:     headerEnd,
				RawContent:  raw,
			}}, snippets...)
		}
	}

	return snippets
}

// extractName retrieves the identifier name from a named node.
func extractName(node *sitter.Node, content []byte, lang models.Language) string {
	// Try the "name" field first (works for Go, Python, JS).
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil {
		return string(content[nameNode.StartByte():nameNode.EndByte()])
	}

	// For Go type_declaration: look for type_spec child.
	if node.Type() == "type_declaration" {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == "type_spec" {
				spec := child.ChildByFieldName("name")
				if spec != nil {
					return string(content[spec.StartByte():spec.EndByte()])
				}
			}
		}
	}

	// TypeScript const/let: lexical_declaration → variable_declarator → name
	if node.Type() == "lexical_declaration" {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == "variable_declarator" {
				if nameNode := child.ChildByFieldName("name"); nameNode != nil {
					return string(content[nameNode.StartByte():nameNode.EndByte()])
				}
			}
		}
	}

	return ""
}

// extractWikilinks collects exported identifier names referenced within a node.
// Only symbols that could plausibly be defined in another file are kept:
//   - Go: uppercase identifiers only (Go's exported-symbol convention)
//   - Python: non-private, non-builtin names
//
// Local noise (ctx, err, i, ok, wg, etc.) is discarded at this stage.
// A second pruning pass in the pipeline further trims to only symbols that
// produced real edges (see pipeline Phase 2b).
func extractWikilinks(node *sitter.Node, content []byte, lang models.Language) []string {
	seen := make(map[string]bool)
	var refs []string

	walkNodes(node, func(n *sitter.Node) bool {
		if n.Type() == "identifier" || n.Type() == "type_identifier" || n.Type() == "field_identifier" {
			sym := string(content[n.StartByte():n.EndByte()])
			if sym == "" || seen[sym] || !isCrossFileCandidate(sym, lang) {
				return true
			}
			seen[sym] = true
			refs = append(refs, sym)
		}
		return true
	})
	return refs
}

// extractExternalRefs finds selector_expression nodes of the form pkg.Symbol
// where pkg is a lowercase identifier (likely a package alias) and Symbol is
// exported. These are used by the resolver to create edges to lib snippets.
// False positives (e.g. myVar.Field) are filtered later by cross-referencing
// against the file's actual import qualifiers.
func extractExternalRefs(node *sitter.Node, content []byte, lang models.Language) []ExternalRef {
	if lang != models.LangGo {
		// Only implemented for Go for now; Python uses dotted imports differently.
		return nil
	}
	seen := make(map[string]bool)
	var refs []ExternalRef

	walkNodes(node, func(n *sitter.Node) bool {
		if n.Type() != "selector_expression" {
			return true
		}
		operand := n.ChildByFieldName("operand")
		field := n.ChildByFieldName("field")
		if operand == nil || field == nil {
			return true
		}
		// Only capture simple-identifier operands (package aliases are single names).
		if operand.Type() != "identifier" {
			return true
		}
		qualifier := string(content[operand.StartByte():operand.EndByte()])
		symbol := string(content[field.StartByte():field.EndByte()])

		// qualifier should be lowercase (package aliases), symbol uppercase (exported).
		if len(qualifier) == 0 || !unicode.IsLower(rune(qualifier[0])) {
			return true
		}
		if len(symbol) == 0 || !unicode.IsUpper(rune(symbol[0])) {
			return true
		}
		key := qualifier + "." + symbol
		if !seen[key] {
			seen[key] = true
			refs = append(refs, ExternalRef{Qualifier: qualifier, Symbol: symbol})
		}
		return true
	})
	return refs
}

// isCrossFileCandidate returns true when a symbol is a plausible cross-file reference.
func isCrossFileCandidate(sym string, lang models.Language) bool {
	switch lang {
	case models.LangGo:
		// All cross-package Go symbols are exported (uppercase first letter).
		return len(sym) > 0 && unicode.IsUpper(rune(sym[0]))
	case models.LangPython:
		if strings.HasPrefix(sym, "_") {
			return false
		}
		return !pythonBuiltins[sym]
	case models.LangTypeScript:
		if strings.HasPrefix(sym, "_") {
			return false
		}
		return !typescriptBuiltins[sym]
	default:
		return true
	}
}

// pythonBuiltins lists built-in names excluded from Python wikilinks.
var pythonBuiltins = map[string]bool{
	"len": true, "range": true, "print": true, "type": true,
	"str": true, "int": true, "float": true, "bool": true,
	"list": true, "dict": true, "set": true, "tuple": true,
	"None": true, "True": true, "False": true, "self": true,
	"cls": true, "super": true, "object": true,
}

// typescriptBuiltins lists primitive types, global objects, and common keywords
// excluded from TypeScript wikilinks.
var typescriptBuiltins = map[string]bool{
	// Primitive types
	"any": true, "void": true, "never": true, "unknown": true,
	"boolean": true, "number": true, "string": true, "symbol": true,
	"bigint": true, "object": true, "null": true, "undefined": true,
	// Global objects
	"Array": true, "Object": true, "Map": true, "Set": true,
	"Promise": true, "Error": true, "Date": true, "RegExp": true,
	"JSON": true, "Math": true, "console": true, "process": true,
	"Buffer": true, "globalThis": true, "window": true, "document": true,
	// Common identifiers that are never cross-file references
	"this": true, "super": true, "constructor": true,
	"true": true, "false": true,
}

// ── Go type classification ───────────────────────────────────────────────────

// classifyGoTypeDecl determines whether a Go type_declaration is a struct,
// interface, or other type definition.
func classifyGoTypeDecl(node *sitter.Node, content []byte) models.SnippetType {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "type_spec" {
			typeNode := child.ChildByFieldName("type")
			if typeNode != nil {
				switch typeNode.Type() {
				case "interface_type":
					return models.SnippetInterface
				case "struct_type":
					return models.SnippetStruct
				}
			}
		}
	}
	return models.SnippetStruct // fallback for type aliases, etc.
}

// ExtractGoInterfaceMethods returns the method names declared in a Go interface
// type_declaration node.
func ExtractGoInterfaceMethods(node *sitter.Node, content []byte) []string {
	var methods []string
	walkNodes(node, func(n *sitter.Node) bool {
		if n.Type() == "method_spec" {
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil {
				methods = append(methods, string(content[nameNode.StartByte():nameNode.EndByte()]))
			}
		}
		return true
	})
	return methods
}

// ExtractGoMethodReceiver returns the receiver type name from a Go
// method_declaration node (stripping pointer indirection).
func ExtractGoMethodReceiver(node *sitter.Node, content []byte) string {
	params := node.ChildByFieldName("receiver")
	if params == nil {
		return ""
	}
	// The receiver is inside a parameter_list → parameter_declaration → type.
	var receiverType string
	walkNodes(params, func(n *sitter.Node) bool {
		if n.Type() == "type_identifier" {
			receiverType = string(content[n.StartByte():n.EndByte()])
			return false
		}
		return true
	})
	return receiverType
}

// ── Control-flow metadata extraction ─────────────────────────────────────────

// extractMetadata walks a snippet's AST subtree and collects control-flow
// markers: branching, error returns, goroutine spawns, channel ops, etc.
func extractMetadata(node *sitter.Node, content []byte, lang models.Language) models.SnippetMetadata {
	var m models.SnippetMetadata

	// Count return statements (for early-return detection).
	// The last return in a function body is not "early" — we subtract 1 after counting.
	returnCount := 0

	walkNodes(node, func(n *sitter.Node) bool {
		switch n.Type() {
		// Branching
		case "if_statement", "if_expression":
			m.BranchCount++
		case "expression_switch_statement", "type_switch_statement", "switch_statement":
			m.BranchCount++
		case "select_statement":
			m.BranchCount++
		case "try_statement": // TypeScript: try/catch is a branching construct
			m.BranchCount++
		case "throw_statement": // TypeScript: semantic equivalent of Go's panic
			m.HasPanic = true

		// Returns
		case "return_statement":
			returnCount++

		// Concurrency (Go-specific)
		case "go_statement":
			m.GoroutineSpawns++
		case "send_statement", "receive_expression":
			m.ChannelOps++
		case "defer_statement":
			m.HasDefer = true

		// Identifier-based detection
		case "identifier":
			sym := string(content[n.StartByte():n.EndByte()])
			switch sym {
			case "panic":
				m.HasPanic = true
			case "recover":
				m.HasRecover = true
			}

		// Selector expression: detect Mutex/RWMutex usage
		case "selector_expression":
			field := n.ChildByFieldName("field")
			if field != nil {
				fieldName := string(content[field.StartByte():field.EndByte()])
				if fieldName == "Lock" || fieldName == "Unlock" ||
					fieldName == "RLock" || fieldName == "RUnlock" {
					m.UsesMutex = true
				}
			}
		}
		return true
	})

	// Early returns: total returns minus the final one (if any).
	if returnCount > 1 {
		m.EarlyReturns = returnCount - 1
	}

	// Detect error return: check if function signature has "error" in result types.
	if lang == models.LangGo {
		m.ReturnsError = goReturnsError(node, content)

		// Method receiver type.
		if node.Type() == "method_declaration" {
			m.Receiver = ExtractGoMethodReceiver(node, content)
		}

		// Interface method set.
		if node.Type() == "type_declaration" && classifyGoTypeDecl(node, content) == models.SnippetInterface {
			m.InterfaceMethods = ExtractGoInterfaceMethods(node, content)
		}
	}

	return m
}

// goReturnsError checks if a Go function/method declaration returns an error type.
func goReturnsError(node *sitter.Node, content []byte) bool {
	result := node.ChildByFieldName("result")
	if result == nil {
		return false
	}
	// Walk result subtree looking for "error" type identifiers.
	found := false
	walkNodes(result, func(n *sitter.Node) bool {
		if n.Type() == "type_identifier" || n.Type() == "identifier" {
			if string(content[n.StartByte():n.EndByte()]) == "error" {
				found = true
				return false // stop
			}
		}
		return true
	})
	return found
}

// ── Utilities ─────────────────────────────────────────────────────────────────

// walkNodes performs a depth-first traversal, calling fn for each node.
// Returning false from fn stops descent into that node's children.
func walkNodes(node *sitter.Node, fn func(*sitter.Node) bool) {
	if node == nil {
		return
	}
	if !fn(node) {
		return
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		walkNodes(node.Child(i), fn)
	}
}
