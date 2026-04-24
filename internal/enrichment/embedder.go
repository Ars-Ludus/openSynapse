package enrichment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Ars-Ludus/providertron/capability"
)

// Embedder produces a fixed-dimension vector representation for a text string.
type Embedder interface {
	// Embed returns a vector for a single text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch returns vectors for multiple texts (order preserved).
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// Dimension returns the embedding output dimension.
	Dimension() int
}

// ── NullEmbedder ──────────────────────────────────────────────────────────────

// NullEmbedder returns zero vectors. Useful when no embedding provider is configured.
type NullEmbedder struct{ dim int }

func NewNullEmbedder(dim int) *NullEmbedder { return &NullEmbedder{dim: dim} }

func (n *NullEmbedder) Dimension() int { return n.dim }

func (n *NullEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, n.dim), nil
}

func (n *NullEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, n.dim)
	}
	return out, nil
}

// ── VoyageEmbedder ────────────────────────────────────────────────────────────

// VoyageEmbedder calls the Voyage AI embeddings API.
// Docs: https://docs.voyageai.com/reference/embeddings-api
type VoyageEmbedder struct {
	apiKey string
	model  string
	dim    int
	client *http.Client
}

func NewVoyageEmbedder(apiKey string, dim int) *VoyageEmbedder {
	model := "voyage-code-2" // 1024-dim, optimised for code
	if dim == 768 {
		model = "voyage-code-2" // still 1024 from Voyage; ONNX handles 768
	}
	return &VoyageEmbedder{
		apiKey: apiKey,
		model:  model,
		dim:    dim,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (v *VoyageEmbedder) Dimension() int { return v.dim }

func (v *VoyageEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := v.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (v *VoyageEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"input": texts,
		"model": v.model,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.voyageai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.apiKey)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("voyage API status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("voyage decode: %w", err)
	}

	out := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// ── LocalEmbedder ─────────────────────────────────────────────────────────────

// LocalEmbedder calls the embedder.py HTTP sidecar running on localhost.
// Start the sidecar with:
//
//	cd internal/vect-embed && python embedder.py --serve 8765
//
// Protocol: POST <baseURL>/embed
//
//	Request:  {"texts": ["..."], "is_query": false}
//	Response: {"embeddings": [[0.1, ...]]}
type LocalEmbedder struct {
	baseURL string
	dim     int
	client  *http.Client
}

func NewLocalEmbedder(baseURL string, dim int) *LocalEmbedder {
	return &LocalEmbedder{
		baseURL: baseURL,
		dim:     dim,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (l *LocalEmbedder) Dimension() int { return l.dim }

func (l *LocalEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := l.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (l *LocalEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"texts":    texts,
		"is_query": false,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		l.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("local embedder request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local embedder status %d", resp.StatusCode)
	}

	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
		Error      string      `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("local embedder decode: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("local embedder: %s", result.Error)
	}
	return result.Embeddings, nil
}

// EmbedQuery adds the "Query: " prefix that CodeRankEmbed expects for search
// queries (as opposed to document/snippet embeddings).
func (l *LocalEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"texts":    []string{text},
		"is_query": true,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		l.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("local embedder request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
		Error      string      `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("local embedder decode: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("local embedder: %s", result.Error)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("local embedder: empty response")
	}
	return result.Embeddings[0], nil
}

// ── ProviderEmbedder ──────────────────────────────────────────────────────────

// ProviderEmbedder adapts a capability.Embedder (from providertron) to the
// internal Embedder interface. Vectors are converted from []float64 to []float32.
// EmbedBatch loops over single-item calls since capability.Embedder has no
// batch method.
type ProviderEmbedder struct {
	emb capability.Embedder
	dim int
}

// NewProviderEmbedder wraps a providertron capability.Embedder.
func NewProviderEmbedder(emb capability.Embedder, dim int) *ProviderEmbedder {
	return &ProviderEmbedder{emb: emb, dim: dim}
}

func (p *ProviderEmbedder) Dimension() int { return p.dim }

func (p *ProviderEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := p.emb.Embed(ctx, capability.EmbedRequest{Input: text})
	if err != nil {
		return nil, fmt.Errorf("provider embedder: %w", err)
	}
	out := make([]float32, len(resp.Vector))
	for i, v := range resp.Vector {
		out[i] = float32(v)
	}
	return out, nil
}

func (p *ProviderEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := p.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// ── Factory ───────────────────────────────────────────────────────────────────

// NewEmbedder selects an implementation based on provider name.
func NewEmbedder(provider, voyageAPIKey, localEmbedURL string, dim int) Embedder {
	switch provider {
	case "voyage":
		return NewVoyageEmbedder(voyageAPIKey, dim)
	case "local":
		return NewLocalEmbedder(localEmbedURL, dim)
	default:
		return NewNullEmbedder(dim)
	}
}
