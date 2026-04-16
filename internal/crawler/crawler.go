package crawler

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"opensynapse/internal/models"
)

// FileInfo is a lightweight record of a discovered source file.
type FileInfo struct {
	Path     string
	Language models.Language
	Size     int64
}

// skipDirs contains directory names to skip during traversal.
var skipDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	".build":       true,
	"target":       true, // Rust/Maven
	".idea":        true,
	".vscode":      true,
}

// extToLang maps file extensions to their language.
var extToLang = map[string]models.Language{
	".go":  models.LangGo,
	".py":  models.LangPython,
	".js":  models.LangJavaScript,
	".mjs": models.LangJavaScript,
	".cjs": models.LangJavaScript,
	".ts":  models.LangTypeScript,
	".tsx": models.LangTypeScript,
	".rs":  models.LangRust,
}

// Walk recursively traverses root and returns all indexable source files.
func Walk(root string) ([]*FileInfo, error) {
	var files []*FileInfo

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}

		if d.IsDir() {
			// Never skip the root itself; only skip hidden/vendor dirs inside it.
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}

		lang, ok := extToLang[strings.ToLower(filepath.Ext(path))]
		if !ok {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		files = append(files, &FileInfo{
			Path:     path,
			Language: lang,
			Size:     info.Size(),
		})
		return nil
	})

	return files, err
}

// ReadFile returns the byte contents of a file, capped at 2 MB.
func ReadFile(path string) ([]byte, error) {
	const maxSize = 2 << 20 // 2 MB
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSize {
		return nil, nil // skip very large files
	}
	return os.ReadFile(path)
}

// DetectLanguage infers the language from a file's extension.
func DetectLanguage(path string) models.Language {
	lang, ok := extToLang[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return models.LangUnknown
	}
	return lang
}
