package enrichment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Ars-Ludus/providertron/capability"
	"github.com/Ars-Ludus/openSynapse/internal/models"
)

// Generator is a type alias for capability.Generator so callers don't need to
// import the providertron capability package directly.
type Generator = capability.Generator

// LLM drives code enrichment prompts via a capability.Generator.
// A nil generator disables enrichment (no-op).
type LLM struct {
	gen capability.Generator
}

// NewLLM creates an LLM enricher backed by gen.
// Pass nil to get a disabled (no-op) enricher.
func NewLLM(gen capability.Generator) *LLM {
	return &LLM{gen: gen}
}

func (l *LLM) enabled() bool { return l.gen != nil }

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

// SummariseCallChain generates a narrative summary of an execution path.
// chain is a list of snippets in call order, each with their descriptions.
func (l *LLM) SummariseCallChain(ctx context.Context, root *models.Snippet, chain []*models.Snippet) (string, error) {
	if !l.enabled() || len(chain) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("You are a senior software engineer. Respond with exactly one paragraph.\n\n")
	sb.WriteString(fmt.Sprintf("Trace the execution path starting from %s %s:\n\n", root.SnippetType, root.Name))

	for i, s := range chain {
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		sb.WriteString(fmt.Sprintf("%d. %s %s: %s\n", i+1, s.SnippetType, s.Name, desc))
	}

	sb.WriteString("\nDescribe what happens when this code executes, following the call chain. ")
	sb.WriteString("Focus on the data flow and side effects. Be specific and concise.")

	return l.complete(ctx, sb.String(), 512)
}

// SummarisePattern asks the LLM whether a group of structurally similar snippets
// represents a meaningful pattern. Returns name and description, or empty strings
// if the LLM judges the grouping to be coincidental.
func (l *LLM) SummarisePattern(ctx context.Context, candidate PatternSummaryInput) (name, description string, err error) {
	if !l.enabled() {
		return "", "", nil
	}

	var sb strings.Builder
	sb.WriteString("You are a senior software engineer analyzing code patterns.\n\n")
	sb.WriteString(fmt.Sprintf("These %d code units share structural similarities (%s):\n\n", len(candidate.Snippets), candidate.GroupLabel))
	for _, s := range candidate.Snippets {
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		sb.WriteString(fmt.Sprintf("- %s %s: %s\n", s.SnippetType, s.Name, desc))
	}
	sb.WriteString("\nIf there is a meaningful architectural pattern or convention here, respond with exactly two lines:\n")
	sb.WriteString("LINE 1: A short name for the pattern (e.g. \"HTTP handler convention\", \"Repository CRUD pattern\")\n")
	sb.WriteString("LINE 2: A 1-2 sentence description of the pattern and what it means for someone writing new code.\n\n")
	sb.WriteString("If the grouping is coincidental or not meaningful, respond with exactly: NONE")

	result, err := l.complete(ctx, sb.String(), 512)
	if err != nil {
		return "", "", err
	}

	result = strings.TrimSpace(result)
	if result == "NONE" || result == "" {
		return "", "", nil
	}

	lines := strings.SplitN(result, "\n", 2)
	if len(lines) < 2 {
		return "", "", nil
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
}

// PatternSummaryInput holds the context needed for LLM pattern synthesis.
type PatternSummaryInput struct {
	GroupLabel string
	Snippets   []*models.Snippet
}

func (l *LLM) complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	resp, err := l.gen.Generate(ctx, capability.GenerateRequest{
		MaxTokens: maxTokens,
		Messages:  []capability.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", fmt.Errorf("llm generate: %w", err)
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		slog.Warn("llm: empty content from provider", "model", resp.Model)
	}
	return content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n// ..."
}

// ── OpenAI-compatible generator ───────────────────────────────────────────────

// NewOpenAICompatGenerator returns a capability.Generator that talks to any
// OpenAI-compatible /v1/chat/completions endpoint (e.g. llama.cpp, Ollama).
// baseURL should include the /v1 path (e.g. "http://host:8080/v1").
// apiKey may be any non-empty string for local servers that don't check auth.
func NewOpenAICompatGenerator(baseURL, apiKey, model string) capability.Generator {
	return &openAICompatGen{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

type openAICompatGen struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

type oacChatRequest struct {
	Model     string       `json:"model"`
	Messages  []oacMessage `json:"messages"`
	MaxTokens int          `json:"max_tokens"`
}

type oacMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oacChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (g *openAICompatGen) Generate(ctx context.Context, req capability.GenerateRequest) (capability.GenerateResponse, error) {
	msgs := make([]oacMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = oacMessage{Role: m.Role, Content: m.Content}
	}

	model := req.Model
	if model == "" {
		model = g.model
	}

	payload := oacChatRequest{
		Model:     model,
		MaxTokens: req.MaxTokens,
		Messages:  msgs,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return capability.GenerateResponse{}, fmt.Errorf("openai-compat: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		g.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return capability.GenerateResponse{}, fmt.Errorf("openai-compat: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return capability.GenerateResponse{}, fmt.Errorf("openai-compat: request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return capability.GenerateResponse{}, fmt.Errorf("openai-compat: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return capability.GenerateResponse{}, fmt.Errorf("openai-compat: status %d: %s", resp.StatusCode, raw)
	}

	var cr oacChatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return capability.GenerateResponse{}, fmt.Errorf("openai-compat: decode: %w", err)
	}
	if cr.Error != nil {
		return capability.GenerateResponse{}, fmt.Errorf("openai-compat: api error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		slog.Warn("openai-compat: empty choices", "raw", string(raw[:min(500, len(raw))]))
		return capability.GenerateResponse{ID: cr.ID, Model: cr.Model}, nil
	}

	choice := cr.Choices[0]
	if choice.Message.Content == "" {
		slog.Warn("openai-compat: empty content",
			"finish_reason", choice.FinishReason,
			"reasoning_len", len(choice.Message.ReasoningContent),
		)
	}

	return capability.GenerateResponse{
		ID:      cr.ID,
		Content: choice.Message.Content,
		Model:   cr.Model,
		Usage: capability.UsageInfo{
			InputTokens:  cr.Usage.PromptTokens,
			OutputTokens: cr.Usage.CompletionTokens,
		},
	}, nil
}

