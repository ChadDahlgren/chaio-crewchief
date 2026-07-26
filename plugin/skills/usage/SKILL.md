---
name: usage
description: Render the Crew Chief efficiency report — per-model attempts/delivered/failed, real spend, frontier counterfactual, savings. Like /usage but for the model fleet. Use when the user asks what the fleet has done, spent, or saved.
---

Run `chaio-crewchief usage` (the binary must be on PATH; set `CHAIO_CREWCHIEF_URL` if the
gateway isn't local) and show its output verbatim in a code block — it is
already formatted. If the binary isn't installed, GET `${CHAIO_CREWCHIEF_URL:-http://localhost:8181}/stats`
and render the same shape: a per-model table (attempts, delivered, failed,
cost) followed by totals (tokens, spend, frontier counterfactual, savings $
and %). Note: "delivered" means a response came back, not that it was
correct — Crew Chief doesn't grade output.

Add one sentence of interpretation at the end — the single most notable
number (e.g. savings, or an unusual failure rate on one model).
