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
   - `crewchief_stats` — rows per model×outcome plus totals:
     `{attempts, prompt_tokens, output_tokens, cost_usd, counterfactual_usd,
     savings_pct, counterfactual_configured}`
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
     Only report a savings % when there is one to report: a false
     `counterfactual_configured` beside a zero `counterfactual_usd` means no
     frontier reference rate is configured, so say `savings: n/a` and why —
     `0.0%` there is a claim, not a reading. Same at zero attempts. A gateway
     predating this plugin omits the field, so a missing
     `counterfactual_configured` beside a non-zero `counterfactual_usd` is a
     real counterfactual, not an absent one.
   - Note the provider mix if interesting (local vs cloud vs frontier). Stats
     rows are per model×outcome and carry no `provider_class` — that field is
     on attempt rows, so get the mix from `crewchief_history` if you want it.
4. If `crewchief_health` reports the gateway is up but no models are
   configured, that is a config gap, not a fleet outage — say so. Crew Chief's
   own error names the right fix and you should pass it through: if
   `models.yaml` does not exist yet, `chaio-crewchief init` writes a starter
   file; if it exists, `init` already ran and every preset in it is still
   commented out, so the fix is to uncomment one and point it at a real
   endpoint. Either way the session has to restart afterward.
5. If a `CHAIO_CREWCHIEF_URL` gateway is genuinely unreachable, say which URL
   failed and suggest checking whatever host runs that gateway — and the model
   endpoints behind it. How it is supervised is the operator's choice; this
   repo ships no service units, so do not name one. (`systemctl status
   chaio-crewchief` is what it looks like on one deployment, not a general
   instruction.)

Keep it to a short table or a few lines — this is a glance, not a report.
