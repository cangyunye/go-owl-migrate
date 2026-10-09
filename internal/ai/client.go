// Package ai implements the optional vendor-API layer behind serve's
// /api/v1/ai/* endpoints: an OpenAI-compatible chat-completions client
// (deepseek first; custom vendors via base_url) plus the embedded router
// system prompt shared verbatim with evals/ai-router.
//
// The API key never enters config files, logs, or session objects: callers
// pass it from an environment variable named by config ai.api_key_env.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is one chat message. Role: system | user | assistant.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Options tunes a single Chat call.
type Options struct {
	JSONMode  bool          // response_format json_object (vendor must support it)
	Effort    string        // thinking intensity: low | high | max (reasoning models only)
	MaxTokens int           // thinking tokens count toward this budget on reasoning models
	Timeout   time.Duration // per-attempt HTTP timeout
}

// Usage echoes the vendor token accounting for the reply.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Reply is the assistant content plus usage.
type Reply struct {
	Content string
	Usage   Usage
}

// Client is a minimal OpenAI-compatible chat-completions client.
type Client struct {
	baseURL   string
	apiKey    string
	model     string
	hc        *http.Client
	maxRetry  int
	clientKey string // identifies the vendor in errors, e.g. "deepseek"
}

// NewClient builds a client. baseURL is the API root without /chat/completions.
func NewClient(baseURL, apiKey, model string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		model:     model,
		hc:        &http.Client{Timeout: timeout},
		maxRetry:  3,
		clientKey: "vendor",
	}
}

type chatRequest struct {
	Model          string      `json:"model"`
	Messages       []Message   `json:"messages"`
	MaxTokens      int         `json:"max_tokens,omitempty"`
	Effort         string      `json:"effort,omitempty"`
	ResponseFormat *respFormat `json:"response_format,omitempty"`
}

type respFormat struct {
	Type string `json:"type"`
}

type chatEnvelope struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage  `json:"usage"`
	Error *exErr `json:"error"`
}

type exErr struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// Chat sends one chat-completions request. Transient failures (network,
// 429, 5xx) are retried with linear backoff; vendor error envelopes are
// surfaced verbatim in the returned error.
func (c *Client) Chat(ctx context.Context, system string, msgs []Message, opt Options) (Reply, error) {
	all := make([]Message, 0, len(msgs)+1)
	if system != "" {
		all = append(all, Message{Role: "system", Content: system})
	}
	all = append(all, msgs...)

	body := chatRequest{
		Model:     c.model,
		Messages:  all,
		MaxTokens: opt.MaxTokens,
		Effort:    opt.Effort,
	}
	if opt.JSONMode {
		body.ResponseFormat = &respFormat{Type: "json_object"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Reply{}, err
	}

	var lastErr error
	for attempt := 0; attempt < c.maxRetry; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Reply{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		reply, retryable, err := c.once(ctx, payload)
		if err == nil {
			return reply, nil
		}
		lastErr = err
		if !retryable {
			return Reply{}, err
		}
	}
	return Reply{}, fmt.Errorf("%s: %w（已重试 %d 次）", c.clientKey, lastErr, c.maxRetry)
}

func (c *Client) once(ctx context.Context, payload []byte) (Reply, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Reply{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.hc.Do(req)
	if err != nil {
		return Reply{}, true, err // transport errors are retryable
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Reply{}, true, err
	}

	var env chatEnvelope
	_ = json.Unmarshal(data, &env) // 信封缺失时按原始状态码处理
	if env.Error != nil {
		// 429/5xx 的错误信封同样值得重试（网关抖动常带信封）。
		retryable := resp.StatusCode == 429 || resp.StatusCode >= 500
		return Reply{}, retryable, fmt.Errorf("http %d: %s", resp.StatusCode, env.Error.Message)
	}
	if resp.StatusCode >= 400 {
		// Rate limits and server errors are worth another attempt.
		retryable := resp.StatusCode == 429 || resp.StatusCode >= 500
		return Reply{}, retryable, fmt.Errorf("http %d: %.200s", resp.StatusCode, data)
	}
	if len(env.Choices) == 0 {
		return Reply{}, false, fmt.Errorf("http %d: 响应无 choices", resp.StatusCode)
	}
	return Reply{Content: env.Choices[0].Message.Content, Usage: env.Usage}, false, nil
}

// ListModels probes the vendor's OpenAI-compatible /models listing. Used by
// the provider-config UI to offer a dropdown after base_url + key are filled;
// endpoints without the listing surface the error verbatim (the UI falls
// back to free-text model input).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %.200s", resp.StatusCode, data)
	}
	var env struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("响应不是 OpenAI /models 结构: %.120s", data)
	}
	out := make([]string, 0, len(env.Data))
	seen := map[string]bool{}
	for _, m := range env.Data {
		if m.ID != "" && !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, m.ID)
		}
	}
	return out, nil
}

// ExtractJSON pulls the first balanced JSON object out of a model reply,
// tolerating markdown fences and surrounding prose.
func ExtractJSON(text string) ([]byte, error) {
	start := strings.Index(text, "{")
	if start < 0 {
		return nil, fmt.Errorf("回复中没有 JSON 对象: %.200s", text)
	}
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inStr {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return []byte(text[start : i+1]), nil
			}
		}
	}
	return nil, fmt.Errorf("JSON 未闭合: %.200s", text[start:])
}
