---
name: status
description: Show the hybrid model fleet's health and cost ledger — Crew Chief gateway health per preset, attempt/success stats, real spend vs frontier counterfactual savings. Use when the user asks how the fleet is doing, what it has saved, or whether models are up.
---

Report the fleet's current state. The Crew Chief gateway serves HTTP on the fleet box
(`${CHAIO_CREWCHIEF_URL:-http://localhost:8181}`, fallbacks `http://localhost:8181`,
`http://localhost:8181`).

1. Prefer the MCP tools if available: `crewchief_health`, `crewchief_stats`,
   `crewchief_models`. Otherwise curl the gateway directly:
   - `GET /health` — per-preset healthy flags
   - `GET /stats` — rows per model×stage×verdict plus totals:
     `{attempts, prompt_tokens, output_tokens, cost_usd, counterfactual_usd, savings_pct}`
2. Present a compact summary:
   - One line per unhealthy preset (skip healthy ones unless all are healthy — then say so in one line).
   - Ledger: attempts, total tokens, real spend, counterfactual, savings %.
   - Note the provider mix if interesting (local vs cloud vs frontier `provider_class` in stats rows).
3. If the gateway is unreachable on all URLs, say so and suggest checking
   `ssh your-gateway-host systemctl status dispatch llama-server`.

Keep it to a short table or a few lines — this is a glance, not a report.
