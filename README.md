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

Pricing is opt-in: the ledger records every attempt either way, but until a
`rates.yaml` sits next to your config, every attempt prices at $0.00 against a
$0.00 counterfactual and `usage` reports `savings: $0.00 (0.0%)`. `init` does
not write one. Copy [`gateway/rates.yaml`](gateway/rates.yaml) to
`~/.chaio-crewchief/rates.yaml` and edit it to your presets and current
provider prices — step 3 below.

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

## Get started (Claude Code)

Four steps, no server to run.

**1. Install the binary.**

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

**2. Add the plugin.**

```bash
claude plugin marketplace add ChadDahlgren/chaio-crewchief
claude plugin install chaio-crewchief
```

The plugin launches `chaio-crewchief mcp`, so the binary has to be on `PATH`.

**3. Write a starter config and point it at a model.**

```bash
chaio-crewchief init          # writes ~/.chaio-crewchief/models.yaml
$EDITOR ~/.chaio-crewchief/models.yaml
```

The file `init` writes has every preset commented out, so it parses as an
empty roster until you enable one deliberately. Uncomment a block, point
`base_url` at something real — a local llama.cpp or Ollama, a Cloudflare
Workers AI endpoint, anything OpenAI-compatible — and name the environment
variable holding the token in `api_key_env`. Keys live in the environment,
never in the file. Working recipes are in [`examples/`](examples/).

Then, if you want the cost ledger to report anything but zero, add a rates
file — `init` does not write one, and a missing one prices every attempt at
$0.00 against a $0.00 counterfactual:

```bash
curl -fsSL https://raw.githubusercontent.com/ChadDahlgren/chaio-crewchief/main/gateway/rates.yaml \
  -o ~/.chaio-crewchief/rates.yaml
$EDITOR ~/.chaio-crewchief/rates.yaml
```

The keys under `models:` are preset names from your `models.yaml`, priced in
$/1M tokens; a preset missing from the file is priced at $0, which is what you
want for a local GPU and not what you want for a cloud endpoint. The
`counterfactual:` block is the frontier rate `usage` compares against. Edit
both to your own presets and to current provider prices — the shipped file is
one deployment's roster, not a live price list.

`CHAIO_CREWCHIEF_HOME` overrides `~/.chaio-crewchief` if you want the config
and ledger somewhere else. It must be an **absolute path** — a relative value,
or a literal `~` that no shell expanded (what a quoted value in an MCP config
JSON produces), is rejected rather than resolved against whatever working
directory the process happened to start in.

**4. Restart the session and delegate.**

You get the `crewchief_*` MCP tools, the `fleet-worker`/`fleet-heavy` agents,
and the `/chaio-crewchief:status`, `/chaio-crewchief:delegate` and
`/chaio-crewchief:usage` skills. `chaio-crewchief doctor` reports whether the
roster is reachable, and `chaio-crewchief usage` prints the ledger.

## Two modes

**Embedded (the default).** `chaio-crewchief mcp` runs the gateway inside the
MCP server process. Nothing to start, nothing to keep running — it comes up
with your Claude Code session and dies with it, reading config and writing its
ledger under `~/.chaio-crewchief`.

It is a real HTTP server, not an in-memory shortcut: it binds an **ephemeral,
unauthenticated port on `127.0.0.1`** for the life of the session, and any
process on the machine can reach it while it is up. Same exposure as the
gateway below, in a smaller window. See [SECURITY.md](SECURITY.md).

**Gateway.** Set `CHAIO_CREWCHIEF_URL` and `mcp` proxies a `serve` process
instead of running its own. That's the next section.

## One binary

```bash
cd gateway && go build -o chaio-crewchief ./cmd/chaio-crewchief

chaio-crewchief init     # write a starter ~/.chaio-crewchief/models.yaml
chaio-crewchief mcp      # stdio MCP server for Claude Code / any MCP client
chaio-crewchief serve --models models.yaml --db chaio-crewchief.db   # shared gateway
chaio-crewchief doctor   # is my fleet configured right? (keys, health, provider classes)
chaio-crewchief usage    # efficiency report: spend vs frontier counterfactual
chaio-crewchief version
```

`doctor` and `usage` read whichever ledger the environment selects. `--local`
forces the embedded one under `~/.chaio-crewchief`, `--gateway` forces the one
behind `CHAIO_CREWCHIEF_URL`. Both ledgers are real and they hold different
work, so name the one you mean when it matters.

`--local` is not as read-only as it looks: answering from the local ledger means
starting a real embedded instance, so `usage --local` and `doctor --local` open
a loopback listener, take the ownership lock, and run the startup reaper — which
writes to the ledger, failing any request left running by a process that has
since exited.

Everything else in the repo is optional equipment: `plugin/` (Claude Code
plugin: fleet agents + skills) and `examples/` (per-provider recipes).

## Sharing a fleet across machines

Embedded mode is per-session and per-machine. Run `serve` when you want one
gateway several machines talk to — a fleet box your laptop delegates to — or
when you need async delegation, which requires a process that outlives the
request.

```bash
# on the fleet box
chaio-crewchief serve --models models.yaml --db chaio-crewchief.db --addr :8181

# on every machine that should use it, including for `mcp`
export CHAIO_CREWCHIEF_URL=http://fleet-box:8181
```

**Setting `CHAIO_CREWCHIEF_URL` switches every command to that gateway** —
`mcp`, `doctor`, and `usage` all stop using the embedded instance and its local
ledger. Unset it to go back. (`CREWCHIEF_URL` and `DISPATCH_URL` are honored as
fallbacks for configs written under earlier names.)

**`serve` binds `127.0.0.1:8181` by default.** Crew Chief has no
authentication and its environment holds your provider API keys, so anyone
who can reach the port can spend them. Serving other machines on a trusted
network is `--addr :8181`, and it logs a warning to keep that choice
deliberate. Don't put it on the open internet.

## The HTTP API

Both modes serve the same handlers; the examples below use a `serve` on 8181
because that's the address you can predict. In embedded mode the port is
kernel-assigned and you talk to it through the MCP tools instead.

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

Model grading lives in that separate repository: it scores candidate models
against a fixed Python/React/Angular suite so you can decide what belongs in
`routing.yaml`. It's separate because it's Python and Node in service of a
single Go binary, and because Crew Chief never grades anything at runtime.

**Retries:** `retries` (default 2) bounds *mechanical* retries only. Set
`0` for single-shot. There is no other retry mode — Crew Chief doesn't
attempt to fix a bad answer, because it can't tell a bad answer from a
good one.

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
