---
name: status
description: Show the hybrid model fleet's health and cost ledger — Crew Chief gateway health per preset, attempt/success stats, real spend vs frontier counterfactual savings. Use when the user asks how the fleet is doing, what it has saved, or whether models are up.
---

Report the fleet's current state.

There is no fixed gateway address to reach for. Crew Chief runs its gateway
inside the MCP process by default, on an ephemeral loopback port that only that
process knows; a shared `serve` gateway exists only when `CHAIO_CREWCHIEF_URL`
names one. So ask the tools, not a URL.

1. **Preferred — the MCP tools**, which reach whichever gateway this session is
   actually using:
   - `crewchief_health` — per-preset healthy flags
   - `crewchief_stats` — rows per model×verdict plus totals:
     `{attempts, prompt_tokens, output_tokens, cost_usd, counterfactual_usd, savings_pct}`
   - `crewchief_models` — the configured roster
2. **If the MCP tools aren't available** (the plugin isn't loaded, or you're
   outside a session that has it), shell out to the CLI instead:
   - `chaio-crewchief doctor --local` — config, keys, per-preset health
   - `chaio-crewchief usage --local` — the ledger, already formatted
   Drop `--local` to read the shared gateway named by `CHAIO_CREWCHIEF_URL`.
   Note that `--local` starts a real embedded instance to answer: it opens a
   loopback listener and takes the ownership lock for the length of the command.
3. Present a compact summary:
   - One line per unhealthy preset (skip healthy ones unless all are healthy —
     then say so in one line).
   - Ledger: attempts, total tokens, real spend, counterfactual, savings %.
   - Note the provider mix if interesting (local vs cloud vs frontier
     `provider_class` in stats rows).
4. If `crewchief_health` reports the gateway is up but no models are configured,
   the fix is `chaio-crewchief init` and a session restart — say that rather
   than reporting a fleet outage. If a `CHAIO_CREWCHIEF_URL` gateway is genuinely
   unreachable, suggest checking it on the host that runs it
   (`systemctl status chaio-crewchief llama-server`).

Keep it to a short table or a few lines — this is a glance, not a report.
