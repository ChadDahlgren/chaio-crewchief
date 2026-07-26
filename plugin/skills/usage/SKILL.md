---
name: usage
description: Render the Crew Chief efficiency report — per-model attempts/delivered/failed, real spend, frontier counterfactual, savings. Like /usage but for the model fleet. Use when the user asks what the fleet has done, spent, or saved.
---

Run `chaio-crewchief usage` (the binary must be on PATH) and show its output
verbatim in a code block — it is already formatted.

Which ledger it reads depends on the environment: `CHAIO_CREWCHIEF_URL` selects
a shared `serve` gateway, and without it the command reads the local ledger
under `~/.chaio-crewchief`. Force one with `--gateway` or `--local` when it
matters which you mean; the two hold different work. `--local` starts a real
embedded instance to answer, so it opens a loopback listener and takes the
ownership lock for the length of the command.

If the shell can't find `chaio-crewchief`, fall back to the `crewchief_stats`
MCP tool — it reaches whichever gateway this session is using, embedded or
shared — and render the same shape by hand: a per-model table (attempts,
delivered, failed, cost) followed by totals (tokens, spend, frontier
counterfactual, savings $ and %).

Note this is a `PATH` problem, not a missing install: the MCP server *is* the
binary (the plugin launches `chaio-crewchief mcp`), so if the `crewchief_*`
tools are available at all, the binary exists somewhere. It just isn't on the
`PATH` the Bash tool inherits — a Homebrew prefix or `~/go/bin` missing from
the interactive shell's profile is the usual cause. Worth mentioning to the
user, since every other CLI command has the same problem.

Note: "delivered" means a response came back, not that it was correct — Crew
Chief doesn't grade output.

Add one sentence of interpretation at the end — the single most notable number
(e.g. savings, or an unusual failure rate on one model).
