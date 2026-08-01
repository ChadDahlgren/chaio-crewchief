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
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			TotalTokens         int `json:"total_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
			// CostInUSDTicks is xAI's own billing figure for the call. Recorded
			// as a check against the modeled price, never used to price.
			CostInUSDTicks int64 `json:"cost_in_usd_ticks"`
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
		Content:            apiResp.Choices[0].Message.Content,
		Reasoning:          apiResp.Choices[0].Message.ReasoningContent,
		PromptTokens:       apiResp.Usage.PromptTokens,
		CachedPromptTokens: apiResp.Usage.PromptTokensDetails.CachedTokens,
		OutputTokens:       apiResp.Usage.CompletionTokens,
		ReasoningTokens: billableReasoningTokens(
			apiResp.Usage.PromptTokens,
			apiResp.Usage.CompletionTokens,
			apiResp.Usage.CompletionTokensDetails.ReasoningTokens,
			apiResp.Usage.TotalTokens,
		),
		ProviderCostUSD: ticksToUSD(apiResp.Usage.CostInUSDTicks),
		TokPerSec:       apiResp.Timings.PredictedPerSecond,
		Raw:             body,
	}

	return cr, nil
}

// usdPerTick converts xAI's cost_in_usd_ticks to dollars.
//
// The scale is derived empirically, not from vendor documentation: across three
// grok-build-0.1 calls on 2026-08-01 spanning a 17x cost range, ticks/1e10
// reproduced the modeled price to 0% error (e.g. 3686000 ticks against a
// $0.0003686 modeled call). It is used only to verify the modeled price, never
// to set one, so an incorrect scale surfaces as a drift warning rather than as
// a wrong number in the ledger.
const usdPerTick = 1e-10

func ticksToUSD(ticks int64) float64 {
	if ticks <= 0 {
		return 0
	}
	return float64(ticks) * usdPerTick
}

// billableReasoningTokens returns the reasoning tokens that must be added to
// completion_tokens to get true billable output.
//
// Providers disagree on whether completion_tokens already includes reasoning.
// OpenAI's convention is inclusive; xAI's is exclusive — an observed payload
// reported prompt 189, completion 2, reasoning 139, total 330, which only
// reconciles if reasoning sits outside completion. Adding reasoning
// unconditionally would double-bill every provider following the OpenAI
// convention.
//
// total_tokens disambiguates: whichever sum reconciles tells us the convention.
// When neither does — or total_tokens is absent — assume inclusive and return 0.
// That is the safe direction: it can undercount a non-conforming exclusive
// provider, whereas assuming exclusive would over-bill every standard one.
func billableReasoningTokens(prompt, completion, reasoning, total int) int {
	if reasoning <= 0 || total <= 0 {
		return 0
	}
	if prompt+completion+reasoning == total {
		return reasoning // exclusive: reasoning is billable output not yet counted
	}
	return 0 // inclusive, or unreconcilable — already inside completion_tokens
}
