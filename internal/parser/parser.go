// Package parser uses Tree-sitter to extract atomic code snippets from source files.
// CGO is required: the tree-sitter C library is linked at compile time.
package parser

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tree_golang "github.com/smacker/go-tree-sitter/golang"
	tree_python "github.com/smacker/go-tree-sitter/python"

	"opensynapse/internal/models"
)

// ParsedSnippet is the raw output of the parser before DB insertion.
type ParsedSnippet struct {
	Name        string
	SnippetType models.SnippetType
	LineStart   int // 1-based
	LineEnd     int // 1-based
	RawContent  string
	Wikilinks   []string // symbol names referenced within the snippet
}

// ParsedFile is the full parse result for one source file.
type ParsedFile struct {
	Imports  []string        // raw import paths / module names
	Snippets []*ParsedSnippet
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
	result.Imports = extractImports(root, content, lang)
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
	default:
		return nil
	}
}

// ── Import extraction ─────────────────────────────────────────────────────────

func extractImports(root *sitter.Node, content []byte, lang models.Language) []string {
	var imports []string
	switch lang {
	case models.LangGo:
		imports = goImports(root, content)
	case models.LangPython:
		imports = pythonImports(root, content)
	}
	return imports
}

func goImports(root *sitter.Node, content []byte) []string {
	var imports []string
	walkNodes(root, func(n *sitter.Node) bool {
		if n.Type() == "import_spec" {
			pathNode := n.ChildByFieldName("path")
			if pathNode != nil {
				raw := string(content[pathNode.StartByte():pathNode.EndByte()])
				// strip surrounding quotes
				raw = strings.Trim(raw, `"`)
				if raw != "" {
					imports = append(imports, raw)
				}
			}
		}
		return true
	})
	return imports
}

func pythonImports(root *sitter.Node, content []byte) []string {
	var imports []string
	walkNodes(root, func(n *sitter.Node) bool {
		switch n.Type() {
		case "import_statement":
			for i := 0; i < int(n.NamedChildCount()); i++ {
				child := n.NamedChild(i)
				if child.Type() == "dotted_name" || child.Type() == "aliased_import" {
					imports = append(imports, string(content[child.StartByte():child.EndByte()]))
				}
			}
		case "import_from_statement":
			mod := n.ChildByFieldName("module_name")
			if mod != nil {
				imports = append(imports, string(content[mod.StartByte():mod.EndByte()]))
			}
		}
		return true
	})
	return imports
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

		raw := string(content[child.StartByte():child.EndByte()])
		name := extractName(child, content, lang)
		lineStart := int(child.StartPoint().Row) + 1
		lineEnd := int(child.EndPoint().Row) + 1
		wikilinks := extractWikilinks(child, content, lang)

		snippets = append(snippets, &ParsedSnippet{
			Name:        name,
			SnippetType: st,
			LineStart:   lineStart,
			LineEnd:     lineEnd,
			RawContent:  raw,
			Wikilinks:   wikilinks,
		})
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

	return ""
}

// extractWikilinks collects identifier names referenced within a node.
func extractWikilinks(node *sitter.Node, content []byte, lang models.Language) []string {
	seen := make(map[string]bool)
	var refs []string

	walkNodes(node, func(n *sitter.Node) bool {
		if n.Type() == "identifier" || n.Type() == "type_identifier" || n.Type() == "field_identifier" {
			sym := string(content[n.StartByte():n.EndByte()])
			if sym != "" && !seen[sym] {
				seen[sym] = true
				refs = append(refs, sym)
			}
		}
		return true
	})
	return refs
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
