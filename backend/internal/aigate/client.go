// Package aigate provides pocketd's stateless AI gateway: it forwards client
// text fragments to embedding/LLM providers and returns results WITHOUT
// persisting anything. This is the "触须" (tentacle) of the lobster — the
// phone sends only the minimal text fragment needed for AI computation;
// pocketd never stores user data.
//
// Privacy contract (see docs/2026-07-02-lobster-server-stateless-design.md):
//   - /api/embed: receives {text}, returns {embedding, model}. Logs at most
//     the request size, never the content.
//   - /api/llm/chat: receives {messages}, returns {content}. Same no-store rule.
//
// Provider selection is configured via env: POCKET_EMBED_PROVIDER (openai/groq),
// POCKET_EMBED_MODEL, POCKET_LLM_PROVIDER, POCKET_LLM_MODEL.
package aigate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Embedder computes a vector for a text fragment. Stateless: no storage.
type Embedder interface {
	Embed(ctx context.Context, text string) (embedding []float32, model string, err error)
}

// LLMClient does a single chat completion. Stateless: no conversation history kept.
type LLMClient interface {
	Chat(ctx context.Context, model string, messages []ChatMessage) (content string, err error)
}

type ChatMessage struct {
	Role    string `json:"role"`    // system / user / assistant
	Content string `json:"content"`
}

// ---- OpenAI-compatible implementations ----

// OpenAIEmbedder works with OpenAI text-embedding-3-small (1536-dim) and any
// OpenAI-compatible endpoint (e.g. Groq, local).
type OpenAIEmbedder struct {
	BaseURL string // https://api.openai.com/v1 (default) or Groq/local
	APIKey  string
	Model   string // text-embedding-3-small
	Client  *http.Client
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, string, error) {
	body, _ := json.Marshal(map[string]string{"model": e.Model, "input": text})
	req, err := http.NewRequestWithContext(ctx, "POST", e.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.APIKey)

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("embed API %d: %s", resp.StatusCode, string(r))
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", fmt.Errorf("decode embed response: %w", err)
	}
	if len(out.Data) == 0 {
		return nil, "", fmt.Errorf("empty embedding response")
	}
	return out.Data[0].Embedding, out.Model, nil
}

// OpenAILLM is a minimal OpenAI-compatible chat client.
type OpenAILLM struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func (l *OpenAILLM) Chat(ctx context.Context, model string, messages []ChatMessage) (string, error) {
	payload := map[string]any{"model": model, "messages": messages}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", l.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.APIKey)

	resp, err := l.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("llm API %d: %s", resp.StatusCode, string(r))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode llm response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty llm response")
	}
	return out.Choices[0].Message.Content, nil
}

// ---- Constructors ----

// NewEmbedder picks a provider by config. Defaults to OpenAI text-embedding-3-small.
func NewEmbedder(baseURL, apiKey, model string) Embedder {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &OpenAIEmbedder{BaseURL: baseURL, APIKey: apiKey, Model: model, Client: &http.Client{Timeout: 30 * time.Second}}
}

// NewLLM picks a provider by config. Groq is a good default for fast, cheap inference.
func NewLLM(baseURL, apiKey string) LLMClient {
	if baseURL == "" {
		baseURL = "https://api.groq.com/openai/v1"
	}
	return &OpenAILLM{BaseURL: baseURL, APIKey: apiKey, Client: &http.Client{Timeout: 60 * time.Second}}
}
