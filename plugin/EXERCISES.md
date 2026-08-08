# Fleet Plugin — Hands-On Exercises

Five-minute tour of the fleet. Run these in any Claude Code session with the
`chaio-crewchief` plugin installed.

## 1. The glance (30s)
> `/chaio-crewchief:status`

Health per preset + the ledger (real spend vs frontier counterfactual).
Baseline the `attempts` and `cost_usd` numbers — exercises 2 and 4 will move them.

## 2. Spawn a fleet worker (2–3 min)
> Use the fleet-worker agent with this work order: a Python function
> `parse_duration(s)` converting "1h30m15s"-style strings to seconds; units
> h/m/s at most once, in order; invalid input raises ValueError.

What to watch for in its report:
- Crew Chief relays the task and returns `status: delivered` — a response came
  back, unjudged. There is no "verified" from Crew Chief's side.
- **The agent itself reads the artifact against the spec** before reporting
  success — that judgment now lives entirely with the calling agent, not
  the gateway.
- If the agent judges the result wrong, watch what it does next: fix small
  gaps itself, re-delegate with a tightened spec, or try a different model —
  never a blind retry of the same prompt.
- `status: failed` (if you see it) is mechanical only — no response, a
  timeout, a transport error. It never means "the code was wrong."

## 3. The audit trail (30s)
> Use crewchief_history filtered to the model from exercise 2 and show me the
> attempts for that request.

Every attempt is a row: outcome (delivered/failed), tokens, wall, tok/sec,
provider_class, cost, and content-addressed refs for the full
prompt/response/artifact (nothing the fleet does is unauditable). Re-run
`/chaio-crewchief:status` and diff the baseline: that's what the exercise cost.
A local preset is priced at $0.00 — as is everything else, if you have not
written a `~/.chaio-crewchief/rates.yaml`, which `init` does not create.

## 4. Same job, cloud burst (1 min)

Needs a cloud preset in your roster — run `crewchief_models` and use one of
those names below. (`cf-gpt-oss-120b` is one deployment's Cloudflare Workers AI
preset, not a name that exists by default.)

> Delegate with crewchief_delegate: task "Write a Python function slugify(s)
> that lowercases, replaces runs of non-alphanumerics with single hyphens,
> and strips leading/trailing hyphens", model "<your cloud preset>".

Same pipeline, same telemetry — but provider_class `cloud` and typically faster
wall time than the local GPU when it's busy. Swapping `model` is the entire
difference between local and cloud-burst. The cost shows up as a real (tiny)
`cost_usd` only if you priced that preset in `~/.chaio-crewchief/rates.yaml`;
without a rates file, every attempt reads $0.00 no matter who ran it. Read the
artifact yourself, same as exercise 2 — Crew Chief still isn't judging.

## 5. Score a new model (optional, ~10 min)
> Separate tool, separate repository:
> [chaio-bench](https://github.com/chaio-dev/chaio-bench)
>
> ```bash
> git clone https://github.com/chaio-dev/chaio-bench && cd chaio-bench
> python3 runner.py --models <preset> --smoke
> ```

This is a *separate, offline* exercise — it doesn't touch the live gateway and
isn't part of this plugin. It's how you'd decide whether a new model earns an
entry in `routing.yaml`, informed by real graded results rather than a guess.
