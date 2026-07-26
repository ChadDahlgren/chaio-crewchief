// Package mcpserve exposes the Crew Chief gateway as a stdio MCP server
// (`chaio-crewchief mcp`). It is a thin proxy: every tool call maps to one HTTP
// request against a running gateway (CHAIO_CREWCHIEF_URL, default localhost:8181),
// so the MCP process can run on a laptop while the gateway runs on the box
// with the GPUs. No logic lives here.
package mcpserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/gwurl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	delegateTimeout = 700 * time.Second
	defaultTimeout  = 15 * time.Second
)

// GatewayURL resolves the gateway base URL from the environment. The rules
// live in internal/gwurl so the MCP server and the CLI subcommands can never
// disagree about where the gateway is.
func GatewayURL() string { return gwurl.URLFromEnv() }

type client struct {
	base string
	hc   *http.Client
}

func (c *client) get(ctx context.Context, path string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return "", err
	}
	return c.do(req)
}

func (c *client) post(ctx context.Context, path string, body any, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+path, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *client) do(req *http.Request) (string, error) {
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gateway returned %d: %s", resp.StatusCode, truncate(string(body), 500))
	}
	return string(body), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// HistoryQuery builds the /history query string from tool inputs.
func HistoryQuery(model, outcome string, limit int) string {
	q := url.Values{}
	if model != "" {
		q.Set("model", model)
	}
	if outcome != "" {
		q.Set("outcome", outcome)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if enc := q.Encode(); enc != "" {
		return "/history?" + enc
	}
	return "/history"
}

type delegateIn struct {
	Task  string `json:"task" jsonschema:"the work order text"`
	Model string `json:"model,omitempty" jsonschema:"force a specific model preset from /models; omit to route via lang or the registry default"`
	Lang  string `json:"lang,omitempty" jsonschema:"language hint for routing.yaml lookup, e.g. typescript (ignored if model is set)"`
	// omitempty means an explicit 0 is indistinguishable from "unset" over
	// MCP and falls back to the gateway's default (2) rather than disabling
	// retries. Use the HTTP API directly with retries:0 if single-shot with
	// no retry matters for a specific call.
	Retries int  `json:"retries,omitempty" jsonschema:"mechanical-failure retries (no response/timeout/error only); omit for default (2)"`
	Async   bool `json:"async,omitempty" jsonschema:"return a request_id immediately; poll crewchief_request"`
}

type requestIn struct {
	RequestID string `json:"request_id" jsonschema:"id returned by an async crewchief_delegate"`
}

type historyIn struct {
	Model   string `json:"model,omitempty"`
	Outcome string `json:"outcome,omitempty" jsonschema:"delivered|failed"`
	Limit   int    `json:"limit,omitempty"`
}

type emptyIn struct{}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// Run serves the MCP tools over stdio until the client disconnects.
func Run(ctx context.Context, version string) error {
	c := &client{base: GatewayURL(), hc: &http.Client{}}
	s := mcp.NewServer(&mcp.Implementation{Name: "chaio-crewchief", Version: version}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "crewchief_delegate",
		Description: "Relay a work order to a fleet model and return whatever it produced. Crew Chief does not judge the output — that's the caller's job. Status is delivered (a response came back) or failed (every mechanical retry — no response/timeout/error — was exhausted). Set async:true for long jobs and poll crewchief_request."},
		func(ctx context.Context, req *mcp.CallToolRequest, in delegateIn) (*mcp.CallToolResult, any, error) {
			out, err := c.post(ctx, "/delegate", in, delegateTimeout)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "crewchief_request",
		Description: "Poll the status/result of an async delegation by request_id."},
		func(ctx context.Context, req *mcp.CallToolRequest, in requestIn) (*mcp.CallToolResult, any, error) {
			out, err := c.get(ctx, "/requests/"+url.PathEscape(in.RequestID), defaultTimeout)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "crewchief_models",
		Description: "List the model roster (presets) the gateway can delegate to."},
		func(ctx context.Context, req *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
			out, err := c.get(ctx, "/models", defaultTimeout)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "crewchief_health",
		Description: "Check gateway and per-model health. Call once at session start."},
		func(ctx context.Context, req *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
			out, err := c.get(ctx, "/health", defaultTimeout)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "crewchief_stats",
		Description: "Aggregate delegation telemetry: tokens, verdicts, real cost vs frontier counterfactual."},
		func(ctx context.Context, req *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
			out, err := c.get(ctx, "/stats", defaultTimeout)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "crewchief_history",
		Description: "Query past delegation attempts, filtered by model and/or outcome (delivered|failed)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in historyIn) (*mcp.CallToolResult, any, error) {
			out, err := c.get(ctx, HistoryQuery(in.Model, in.Outcome, in.Limit), defaultTimeout)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	return s.Run(ctx, &mcp.StdioTransport{})
}
