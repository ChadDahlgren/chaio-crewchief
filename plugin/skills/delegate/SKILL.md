---
name: delegate
description: Delegate well-specified coding work to the cheap model fleet (local fleet + Cloudflare Workers AI) instead of doing it inline. Use when the user asks to "send this to the fleet", "delegate this", or when a task decomposes into self-contained, spec-able units that don't need frontier reasoning.
---

Route implementation work through the Crew Chief gateway instead of writing the
code yourself. Crew Chief relays the task to a cheaper model and returns
whatever that model produced — it does not judge quality. **You are the
verification step.**

What the gateway gives you is a measurement, not a guarantee. Every attempt is
recorded in the ledger with the model, wall time, tokens, what it actually cost,
and what the same tokens would have cost at frontier prices — so you can see
what delegation is worth *on your own work* rather than take a claim about it.
Whether the output is any good is a separate question, and answering it is your
job every time: read the artifact, and ideally run it.

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

Preset names are whatever the operator put in their own `models.yaml` — there
is no fixed set, and the starter file `init` writes has none enabled. **Call
`crewchief_models` to see the roster before naming one**; a preset that isn't
there won't resolve.

- omit both — resolves via `routing.yaml` if `lang` matches an entry, else
  the registry default
- name an exact preset from the roster to force a specific model — useful when
  the local GPU is busy, or as a second opinion after a different model's
  answer looked wrong
- if the roster has a frontier preset, naming it sends the same task down the
  same pipe for comparison — and bills it at frontier rates, so say so

(One deployment's roster reads `local-coder`, `cf-gpt-oss-120b`,
`bedrock-gpt-oss-120b`, `sonnet-5-ref`. Those are examples of the shape, not
names you can assume exist.)

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
