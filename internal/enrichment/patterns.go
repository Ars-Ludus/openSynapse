package enrichment

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/Ars-Ludus/openSynapse/internal/db"
	"github.com/Ars-Ludus/openSynapse/internal/models"
)

// PatternCandidate is a group of snippets that share structural characteristics.
type PatternCandidate struct {
	Type       string            // "fan_out", "naming", "signature"
	Label      string            // human-readable group label
	SnippetIDs []uuid.UUID       // members
	Snippets   []*models.Snippet // full snippet data for LLM context
}

// MinGroupSize is the minimum number of snippets to form a pattern candidate.
const MinGroupSize = 3

// DetectCandidates analyzes the graph and returns structural groupings that
// may represent meaningful patterns. No LLM calls — pure graph/string analysis.
func DetectCandidates(ctx context.Context, database *db.DB) ([]PatternCandidate, error) {
	files, err := database.ListFiles(ctx)
	if err != nil {
		return nil, err
	}

	// Collect all non-header, non-external snippets.
	var allSnippets []*models.Snippet
	for _, f := range files {
		if strings.HasPrefix(f.Path, "lib:") {
			continue
		}
		snippets, err := database.GetSnippetsByFile(ctx, f.FileID)
		if err != nil {
			return nil, err
		}
		for _, s := range snippets {
			if s.SnippetType != models.SnippetUnknown && s.SnippetType != models.SnippetExternal && s.Name != "" {
				allSnippets = append(allSnippets, s)
			}
		}
	}

	var candidates []PatternCandidate

	// ── 1. Fan-out groups: snippets that call the same set of targets ────────
	candidates = append(candidates, detectFanOutGroups(ctx, database, allSnippets)...)

	// ── 2. Naming convention groups ──────────────────────────────────────────
	candidates = append(candidates, detectNamingGroups(allSnippets)...)

	return candidates, nil
}

// detectFanOutGroups finds snippets that share 2+ common outgoing edge targets.
func detectFanOutGroups(ctx context.Context, database *db.DB, snippets []*models.Snippet) []PatternCandidate {
	// Build snippet → target names map.
	snippetTargets := make(map[uuid.UUID][]string)
	for _, s := range snippets {
		targets, err := database.GetOutgoingEdgeTargetNames(ctx, s.SnippetID)
		if err != nil || len(targets) < 2 {
			continue
		}
		sort.Strings(targets)
		snippetTargets[s.SnippetID] = targets
	}

	// Group by shared target signature (sorted, joined).
	type targetKey struct {
		key     string
		targets []string
	}
	groups := make(map[string]*struct {
		targets    []string
		snippetIDs []uuid.UUID
		snippets   []*models.Snippet
	})

	snippetByID := make(map[uuid.UUID]*models.Snippet)
	for _, s := range snippets {
		snippetByID[s.SnippetID] = s
	}

	for sid, targets := range snippetTargets {
		key := strings.Join(targets, ",")
		g, ok := groups[key]
		if !ok {
			g = &struct {
				targets    []string
				snippetIDs []uuid.UUID
				snippets   []*models.Snippet
			}{targets: targets}
			groups[key] = g
		}
		g.snippetIDs = append(g.snippetIDs, sid)
		g.snippets = append(g.snippets, snippetByID[sid])
	}

	var candidates []PatternCandidate
	for _, g := range groups {
		if len(g.snippetIDs) < MinGroupSize {
			continue
		}
		candidates = append(candidates, PatternCandidate{
			Type:       "fan_out",
			Label:      "shared targets: " + strings.Join(g.targets, ", "),
			SnippetIDs: g.snippetIDs,
			Snippets:   g.snippets,
		})
	}
	return candidates
}

// detectNamingGroups finds snippets that share name prefixes or suffixes.
func detectNamingGroups(snippets []*models.Snippet) []PatternCandidate {
	// Only consider functions and methods.
	var named []*models.Snippet
	for _, s := range snippets {
		if s.SnippetType == models.SnippetFunction || s.SnippetType == models.SnippetMethod {
			named = append(named, s)
		}
	}

	// Group by common prefix (first word in camelCase/PascalCase).
	prefixGroups := make(map[string][]*models.Snippet)
	for _, s := range named {
		prefix := extractCamelPrefix(s.Name)
		if prefix != "" && len(prefix) >= 3 {
			prefixGroups[prefix] = append(prefixGroups[prefix], s)
		}
	}

	var candidates []PatternCandidate
	for prefix, group := range prefixGroups {
		if len(group) < MinGroupSize {
			continue
		}
		ids := make([]uuid.UUID, len(group))
		for i, s := range group {
			ids[i] = s.SnippetID
		}
		candidates = append(candidates, PatternCandidate{
			Type:       "naming",
			Label:      "prefix: " + prefix,
			SnippetIDs: ids,
			Snippets:   group,
		})
	}
	return candidates
}

// extractCamelPrefix returns the first word of a camelCase or PascalCase name.
// "HandleCreateUser" → "Handle", "getUserByID" → "get"
func extractCamelPrefix(name string) string {
	if name == "" {
		return ""
	}
	for i := 1; i < len(name); i++ {
		if name[i] >= 'A' && name[i] <= 'Z' {
			return name[:i]
		}
	}
	return "" // single word — not useful as a prefix
}
