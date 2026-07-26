# Crew Chief

**Crew Chief is a deliberately simple, fast, cheap phonebook between your frontier brain and a
fleet of models — local GPUs, Cloudflare Workers AI, Bedrock, any
OpenAI-compatible endpoint.**

You (or your frontier model — Claude, GPT, whatever's driving) decide what
needs doing and who should do it. Crew Chief relays the work order, retries
*only* mechanical failures (no response, a timeout, a transport error —
never "the answer was wrong"), and returns whatever the model produced. It
never grades the output. Judging quality is the brain's job — it's the one
with the intelligence to do it; Crew Chief just gives it hands.

Every attempt lands in a **cost ledger** priced against what the same
tokens would have cost at frontier rates, so "what did the fleet save me"
is a queryable number (`chaio-crewchief usage`), not a vibe.

## Why not have Crew Chief verify the work too?

We built that first — hidden tests, retry-with-feedback, a `solved`
verdict. It worked, but it was wrong on reflection: running model-generated
code as a "test" meant unsandboxed code execution on the gateway box (a
real, demonstrated security hole), and worse, *someone* still has to write
the test — which means the gateway was quietly making design decisions
(what counts as "correct") that belong to whoever actually understands the
task. A frontier model deciding "is this right, and if not, what do I do
about it" is exactly the judgment a cheap fleet model shouldn't be trusted
with, and Crew Chief shouldn't fake having either. So that whole path is
gone. Crew Chief relays; you decide.

## Install

```bash
# macOS
brew install ChadDahlgren/tap/chaio-crewchief

# or tap once, then short names work for this and anything else I publish
brew tap ChadDahlgren/tap
brew install chaio-crewchief

# Linux (or macOS without Homebrew) — verifies the published checksum
curl -fsSL https://raw.githubusercontent.com/ChadDahlgren/chaio-crewchief/main/scripts/install.sh | sh

# from source
go install github.com/ChadDahlgren/chaio-crewchief/gateway/cmd/chaio-crewchief@latest
```

Prebuilt binaries for linux and darwin on amd64 and arm64 are attached to
every [release](https://github.com/ChadDahlgren/chaio-crewchief/releases),
with a `checksums.txt` alongside them.

## One binary

```bash
cd gateway && go build -o chaio-crewchief ./cmd/chaio-crewchief

chaio-crewchief serve --models models.yaml --db chaio-crewchief.db   # the gateway
chaio-crewchief mcp      # stdio MCP server for Claude Code / any MCP client
chaio-crewchief doctor   # is my fleet configured right? (keys, health, provider classes)
chaio-crewchief usage    # efficiency report: spend vs frontier counterfactual
chaio-crewchief version
```

**`serve` binds `127.0.0.1:8181` by default.** Crew Chief has no
authentication and its environment holds your provider API keys, so anyone
who can reach the port can spend them. Serving other machines on a trusted
network (a fleet box your laptop talks to) is `--addr :8181`, and it logs a
warning to keep that choice deliberate. Don't put it on the open internet.

Everything else in the repo is optional equipment: `plugin/` (Claude Code
plugin: fleet agents + skills) and `examples/` (per-provider recipes).

Model grading lives in its own repository:
[chaio-bench](https://github.com/ChadDahlgren/chaio-bench) scores candidate models
against a fixed Python/React/Angular suite so you can decide what belongs in
`routing.yaml`. It's separate because it's Python and Node in service of a
single Go binary, and because Crew Chief never grades anything at runtime.

## Delegate

```bash
curl -s localhost:8181/delegate -H 'Content-Type: application/json' -d '{
  "task": "Write a Python function add(a,b) returning a+b."
}'
# → {"status":"delivered","artifact":"def add(a, b): ...","attempts":[...]}

# long jobs: async:true → {"request_id":"...","status":"running"}
curl -s localhost:8181/requests/<id>      # poll for the result
curl -s localhost:8181/stats              # the ledger
```

`status` is only ever `delivered` (a response came back — unjudged) or
`failed` (every mechanical retry was exhausted). There is no `verified`,
no `solved`, no partial credit — Crew Chief doesn't know enough to offer one.

**Routing:** pass `model` to name an exact preset, or `lang` to resolve via
`routing.yaml` (a plain data lookup — see [`routing.yaml`](gateway/routing.yaml)),
or omit both for the registry default. Crew Chief never infers which model
is "best"; that table is something you maintain, informed by
[chaio-bench](https://github.com/ChadDahlgren/chaio-bench).

**Retries:** `retries` (default 2) bounds *mechanical* retries only. Set
`0` for single-shot. There is no other retry mode — Crew Chief doesn't
attempt to fix a bad answer, because it can't tell a bad answer from a
good one.

## Claude Code integration

```bash
claude plugin marketplace add <this repo>
claude plugin install chaio-crewchief
```

You get the `crewchief_*` MCP tools (via `chaio-crewchief mcp` — the binary must
be on PATH), the `fleet-worker`/`fleet-heavy` agents, and the
`/chaio-crewchief:status`, `/chaio-crewchief:delegate` and
`/chaio-crewchief:usage` skills. Set `CHAIO_CREWCHIEF_URL` if the gateway isn't
on localhost (`CREWCHIEF_URL` and `DISPATCH_URL` are honored as fallbacks for
configs written under earlier names).

## Providers are presets, not integrations

Any OpenAI-compatible endpoint is one YAML block in `models.yaml`: base_url
(+ optional `model_id`, `api_key_env`, `health_path`, `omit_temperature` for
Claude 4.6+). Working recipes in [`examples/`](examples/): llama.cpp,
Cloudflare Workers AI, Amazon Bedrock (`bedrock-mantle` + API key),
Anthropic. Keys live in the gateway's environment, never in config.

## Design notes

- **Crew Chief never judges output.** No sandboxed execution, no hidden
  tests, no "verified" status — that entire surface (and the security hole
  it implied) is gone by design, not by omission.
- **Crew Chief never chooses a model.** `routing.yaml` is data you
  maintain, not inference the gateway performs. It's a phonebook, not a
  scheduler — deciding who works is the calling brain's job.
- **Crew Chief only reacts to mechanics.** Retry exists solely for "the
  call itself failed" (no response, timeout, transport/API error). "The
  answer was wrong" is never something it detects or acts on.
- **The ledger is the point.** `provider_class` + per-model rates + a
  frontier counterfactual make fleet economics a queryable number.

## Status

Extracted from a working single-user deployment (a DGX Spark + Cloudflare +
Anthropic fleet that classifies ~3,000 emails/day). APIs are small and
stable; expect sharp edges in packaging.

Single maintainer, `0.x`, and honest about it: the HTTP API and config format
may change between minor versions, and breaking changes are called out in the
[changelog](CHANGELOG.md).

## Contributing and reporting

- [CONTRIBUTING.md](CONTRIBUTING.md) — how to build and test, and the one
  design boundary worth reading before writing code
- [SECURITY.md](SECURITY.md) — the threat model, and how to report a
  vulnerability privately. **Read this before exposing the port**: there is no
  authentication and the process holds your provider API keys.
- [CHANGELOG.md](CHANGELOG.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)

License: [Apache-2.0](LICENSE).
