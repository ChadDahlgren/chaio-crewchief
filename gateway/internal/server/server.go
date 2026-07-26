package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/rates"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

type Engine interface {
	Run(ctx context.Context, req types.DelegateRequest) (types.DelegateResult, error)
	RunWithID(ctx context.Context, reqID string, req types.DelegateRequest) (types.DelegateResult, error)
	NewRequestID() string
}

// StatsResponse is the /stats body: the per-bucket rows plus a whole-store
// rollup with the frontier counterfactual — the 20%-thesis dashboard number.
type StatsResponse struct {
	Rows   []types.StatRow `json:"rows"`
	Totals StatsTotalsView `json:"totals"`
}

type StatsTotalsView struct {
	Attempts          int     `json:"attempts"`
	PromptTokens      int64   `json:"prompt_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CostUSD           float64 `json:"cost_usd"`
	CounterfactualUSD float64 `json:"counterfactual_usd"`
	SavingsPct        float64 `json:"savings_pct"`
}

// New wires the HTTP handler. rt may be nil, in which case counterfactual_usd
// and savings_pct are always 0.
func New(eng Engine, store types.Store, reg types.Registry, arch types.Archiver, rt rates.Table) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /delegate", func(w http.ResponseWriter, r *http.Request) {
		var req types.DelegateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}

		if !req.Async {
			res, err := eng.Run(r.Context(), req)
			if err != nil {
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(res)
		} else {
			reqID := eng.NewRequestID()
			go func() {
				defer func() {
					if r := recover(); r != nil {
						_ = store.UpdateRequestResult(context.Background(), reqID, types.StatusFailed, "", "panic during delegation")
					}
				}()
				_, _ = eng.RunWithID(context.Background(), reqID, req)
			}()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"request_id": reqID, "status": "running"})
		}
	})

	mux.HandleFunc("GET /requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec, found, err := store.GetRequest(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if !found {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(rec)
	})

	mux.HandleFunc("GET /attempts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		attempt, found, err := store.GetAttempt(r.Context(), id)
		if err != nil {
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if !found {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(attempt)
	})

	mux.HandleFunc("GET /history", func(w http.ResponseWriter, r *http.Request) {
		filter := types.AttemptFilter{
			Limit: 50,
		}
		if model := r.URL.Query().Get("model"); model != "" {
			filter.Model = model
		}
		if outcome := r.URL.Query().Get("outcome"); outcome != "" {
			filter.Outcome = types.AttemptOutcome(outcome)
		}
		if search := r.URL.Query().Get("search"); search != "" {
			filter.Search = search
		}
		if since := r.URL.Query().Get("since"); since != "" {
			if t, err := time.Parse(time.RFC3339, since); err == nil {
				filter.Since = t
			}
		}
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
				filter.Limit = limit
			}
		}

		attempts, err := store.QueryAttempts(r.Context(), filter)
		if err != nil {
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(attempts)
	})

	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.Stats(r.Context())
		if err != nil {
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		totals, err := store.StatsTotals(r.Context())
		if err != nil {
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}

		view := StatsTotalsView{
			Attempts:     totals.Attempts,
			PromptTokens: totals.PromptTokens,
			OutputTokens: totals.OutputTokens,
			CostUSD:      totals.CostUSD,
		}
		if rt != nil {
			view.CounterfactualUSD = rt.Counterfactual(int(totals.PromptTokens), int(totals.OutputTokens))
		}
		if view.CounterfactualUSD > 0 {
			view.SavingsPct = 1 - view.CostUSD/view.CounterfactualUSD
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(StatsResponse{Rows: rows, Totals: view})
	})

	mux.HandleFunc("GET /models", func(w http.ResponseWriter, r *http.Request) {
		models := reg.List()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(models)
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		models := reg.List()
		results := make([]map[string]interface{}, len(models))
		var wg sync.WaitGroup
		wg.Add(len(models))

		for i, preset := range models {
			go func(idx int, p types.Preset) {
				defer wg.Done()
				healthy := false
				if p.BaseURL != "" {
					healthPath := p.HealthPath
					if healthPath == "" {
						healthPath = "/health"
					}
					client := http.Client{Timeout: 5 * time.Second}
					req, err := http.NewRequest("GET", p.BaseURL+healthPath, nil)
					if err == nil {
						if p.APIKeyEnv != "" {
							if key := os.Getenv(p.APIKeyEnv); key != "" {
								req.Header.Set("Authorization", "Bearer "+key)
							}
						}
						resp, err := client.Do(req)
						if err == nil {
							_ = resp.Body.Close()
							healthy = resp.StatusCode == http.StatusOK
						}
					}
				}
				results[idx] = map[string]interface{}{
					"name":    p.Name,
					"healthy": healthy,
				}
			}(i, preset)
		}

		wg.Wait()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"models": results,
		})
	})

	return mux
}
