---
name: delegate
description: Delegate well-specified coding work to the cheap model fleet (local fleet + Cloudflare Workers AI) instead of doing it inline. Use when the user asks to "send this to the fleet", "delegate this", or when a task decomposes into self-contained, spec-able units that don't need frontier reasoning.
---

Route implementation work through the Crew Chief gateway instead of writing the
code yourself. Crew Chief relays the task and returns whatever the model
produced — it does not judge quality. **You are the verification step.**
The economics still favor this: fleet models solve well-specified
single-file tasks at frontier quality for ~1/10th to $0 cost when the answer
is right — but "right" is something you have to check yourself every time,
by reading the artifact and, ideally, running it.

## How to delegate

- **One self-contained unit** (single file, clear spec): spawn the
  `fleet-worker` agent with the work order. It calls `crewchief_delegate`,
  reads the result, and judges it before reporting back.
- **Small multi-unit order (2-4 files with clean seams)**: spawn `fleet-heavy`.
  It freezes contracts itself, drives units sequentially, and owns
  integration verification (Crew Chief never sees cross-unit behavior).
- **Never delegate**: architecture, ambiguous specs, cross-cutting refactors,
  anything needing repo-wide context. That work stays with you (frontier).

## Model choice (the `model` or `lang` param on crewchief_delegate)

- omit both — resolves via `routing.yaml` if `lang` matches an entry, else
  the registry default
- name an exact preset (e.g. `cf-gpt-oss-120b`) to force a specific model —
  useful when the local GPU is busy, or as a second opinion after a
  different model's answer looked wrong
- `sonnet-5-ref` — frontier via the same pipe, for comparison

## Discipline (non-negotiable)

- **Read every artifact before accepting it.** `status: delivered` means a
  response came back, nothing more. There is no verified/unverified
  distinction from Crew Chief — that judgment is entirely yours now.
- `status: failed` is mechanical only (no response/timeout/error) — it is
  never a signal that the code is wrong. Don't treat it as a quality result.
- If the artifact is wrong: fix it yourself if the gap is small, re-delegate
  with a tightened spec, or try a different model. Never blind-retry the
  same model hoping for a different answer.
- Report what the ledger charged regardless of outcome.
