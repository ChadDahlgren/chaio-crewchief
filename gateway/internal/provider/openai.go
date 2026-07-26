package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

type OpenAI struct {
	client *http.Client
}

func New() *OpenAI {
	return &OpenAI{
		client: &http.Client{},
	}
}

func (o *OpenAI) Complete(ctx context.Context, base types.Preset, req types.CompletionRequest) (types.CompletionResponse, error) {
	// A preset with no timeout_sec yields Timeout == 0. Bind cancel to a no-op
	// in that case: an unconditional `defer cancel()` on a nil CancelFunc
	// panics, which would make every delegation to such a preset crash.
	reqCtx, cancel := ctx, context.CancelFunc(func() {})
	if req.Timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	// Normalize URL
	baseURL := strings.TrimSuffix(base.BaseURL, "/v1")
	url := fmt.Sprintf("%s/v1/chat/completions", baseURL)

	payload := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.User},
		},
		"max_tokens": req.MaxTokens,
		"stream":     false,
	}
	if !base.OmitTemperature {
		payload["temperature"] = req.Temperature
	}
	if base.ModelID != "" {
		payload["model"] = base.ModelID
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return types.CompletionResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		return types.CompletionResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if base.APIKeyEnv != "" {
		key := os.Getenv(base.APIKeyEnv)
		if key == "" {
			return types.CompletionResponse{}, fmt.Errorf("preset %q requires api key in env %s, which is unset", base.Name, base.APIKeyEnv)
		}
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return types.CompletionResponse{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.CompletionResponse{}, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Truncate body for error message to avoid overly long messages
		var bodySnippet string
		if len(body) > 500 {
			bodySnippet = string(body[len(body)-500:])
		} else {
			bodySnippet = string(body)
		}
		return types.CompletionResponse{}, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, bodySnippet)
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Timings struct {
			PredictedPerSecond float64 `json:"predicted_per_second"`
		} `json:"timings"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return types.CompletionResponse{}, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return types.CompletionResponse{}, fmt.Errorf("no choices in response")
	}

	cr := types.CompletionResponse{
		Content:      apiResp.Choices[0].Message.Content,
		Reasoning:    apiResp.Choices[0].Message.ReasoningContent,
		PromptTokens: apiResp.Usage.PromptTokens,
		OutputTokens: apiResp.Usage.CompletionTokens,
		TokPerSec:    apiResp.Timings.PredictedPerSecond,
		Raw:          body,
	}

	return cr, nil
}
