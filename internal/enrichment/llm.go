package enrichment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"opensynapse/internal/models"
)

// LLM calls an OpenAI-compatible /v1/chat/completions endpoint (e.g. llama.cpp).
type LLM struct {
	baseURL string // e.g. "http://192.168.254.8:8080/v1"
	model   string // passed in the request body; llama.cpp ignores it
	client  *http.Client
}

// NewLLM creates an LLM enricher that talks to a local OpenAI-compatible server.
// Pass an empty baseURL to get a no-op enricher.
func NewLLM(baseURL, model string) *LLM {
	if baseURL == "" {
		return &LLM{}
	}
	return &LLM{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (l *LLM) enabled() bool { return l.client != nil }

// SummariseFile generates a high-level description of a source file.
func (l *LLM) SummariseFile(ctx context.Context, f *models.CodeFile, snippetNames []string) (string, error) {
	if !l.enabled() {
		return "", nil
	}
	imports := strings.Join(f.Dependencies, ", ")
	if imports == "" {
		imports = "(none)"
	}
	symbols := strings.Join(snippetNames, ", ")
	if symbols == "" {
		symbols = "(none)"
	}
	prompt := fmt.Sprintf(
		"You are a senior software engineer. Respond with exactly one short paragraph.\n\n"+
			"File: %s\nLanguage: %s\nImports: %s\nTop-level symbols: %s\n\n"+
			"Describe what this file does and its role in the codebase in 2-3 sentences. "+
			"Be specific and technical.",
		f.Path, f.Language, imports, symbols,
	)
	return l.complete(ctx, prompt, 1024)
}

// SummariseSnippet generates a description for a single code snippet.
func (l *LLM) SummariseSnippet(ctx context.Context, s *models.Snippet, filePath string) (string, error) {
	if !l.enabled() {
		return "", nil
	}
	raw := s.RawContent
	if len(raw) > 3000 {
		raw = raw[:3000] + "\n// ... (truncated)"
	}
	name := s.Name
	if name == "" {
		name = "(unnamed)"
	}
	prompt := fmt.Sprintf(
		"You are a senior software engineer. Respond with exactly one sentence.\n\n"+
			"File: %s\n"+
			"Symbol: %s %s (lines %d-%d)\n\n"+
			"%s\n\n"+
			"Describe what this code does in one concise sentence. "+
			"Focus on purpose and behaviour, not implementation details.",
		filePath, s.SnippetType, name, s.LineStart, s.LineEnd, raw,
	)
	return l.complete(ctx, prompt, 1024)
}

// SummariseEdge produces a merged-context description for a reference edge.
func (l *LLM) SummariseEdge(ctx context.Context, src, dst *models.Snippet, edgeType models.EdgeType) (string, error) {
	if !l.enabled() {
		return "", nil
	}
	prompt := fmt.Sprintf(
		"You are a senior software engineer.\n\n"+
			"Two code units are connected via a '%s' relationship:\n\n"+
			"Source (%s '%s'):\n```\n%s\n```\n\n"+
			"Target (%s '%s'):\n```\n%s\n```\n\n"+
			"Write one sentence describing the data/control flow between these two units.",
		edgeType,
		src.SnippetType, src.Name, truncate(src.RawContent, 800),
		dst.SnippetType, dst.Name, truncate(dst.RawContent, 800),
	)
	return l.complete(ctx, prompt, 1024)
}

// ── OpenAI-compatible chat completion ─────────────────────────────────────────

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (l *LLM) complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	payload := chatRequest{
		Model:     l.model,
		MaxTokens: maxTokens,
		Messages:  []chatMessage{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		l.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm status %d: %s", resp.StatusCode, raw)
	}

	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("llm decode: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llm error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		log.Printf("llm: empty choices — raw body: %.500s", raw)
		return "", nil
	}

	choice := cr.Choices[0]
	content := strings.TrimSpace(choice.Message.Content)
	if content == "" {
		log.Printf("llm: empty content (finish_reason=%q, reasoning_tokens=%d) — raw body: %.300s",
			choice.FinishReason, len(choice.Message.ReasoningContent), raw)
	}
	return content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n// ..."
}
