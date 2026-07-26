# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the version is `0.x`, the HTTP API and configuration format may change
between minor versions. Breaking changes will always be called out here.

## [Unreleased]

### Added

- Embedded mode: `chaio-crewchief mcp` now runs the gateway in-process, so the
  plugin works after `brew install` with nothing else running.
- `chaio-crewchief init` writes a starter `models.yaml` to `~/.chaio-crewchief`.
- `--local` and `--gateway` on `usage` and `doctor` to pick a ledger explicitly.
- Requests left running by a process that exited are failed on startup instead
  of reporting a stale status forever.

### Changed

- **Breaking:** an unset `CHAIO_CREWCHIEF_URL` now selects embedded mode rather
  than defaulting to `http://localhost:8181`. Set the variable explicitly to
  keep proxying to a gateway.
- `~/.chaio-crewchief/` (or `CHAIO_CREWCHIEF_HOME`) is the default location for
  config and the ledger when paths are not given as flags. `serve`'s flags are
  unchanged.

### Security

- Embedded mode binds an unauthenticated ephemeral port on `127.0.0.1` for the
  life of the MCP session. Any local process can reach it while it is up. The
  threat model is in [SECURITY.md](SECURITY.md); it matters most on shared or
  multi-user machines.

## [0.4.0] — 2026-07-26

First public release. Earlier versions existed only in a private deployment
and are not published; this entry describes the state at first release rather
than a diff against something installable.

### Added

- **Homebrew and script installation.** `brew install ChadDahlgren/tap/chaio-crewchief`,
  or `scripts/install.sh`, which verifies the published SHA-256 before
  installing. Prebuilt binaries for linux and darwin on amd64 and arm64 are
  attached to each release with a `checksums.txt`.
- **`chaio-crewchief version`**, reporting the version injected at build time.
  Untagged local builds report `dev` rather than a misleading number.
- **Language routing.** A request may pass `lang` instead of `model` to
  resolve a preset through `routing.yaml` — a table the operator maintains,
  never inference the gateway performs.
- **Cost ledger.** Every attempt is priced through `rates.yaml` and tagged
  `local`, `cloud`, or `frontier`. `chaio-crewchief usage` and `GET /stats`
  report spend against a frontier counterfactual.
- **Async delegation.** `async: true` returns a request ID immediately;
  `GET /requests/{id}` polls for the result.
- **`chaio-crewchief doctor`**, reporting gateway reachability, per-preset
  health, missing API-key environment variables, and unknown provider classes.
- **MCP server** (`chaio-crewchief mcp`) exposing `crewchief_delegate`,
  `crewchief_request`, `crewchief_models`, `crewchief_health`,
  `crewchief_stats`, and `crewchief_history` over stdio.
- **Claude Code plugin** with fleet agents and status/delegate/usage skills.
- **Provider recipes** in `examples/` for llama.cpp, Cloudflare Workers AI,
  Amazon Bedrock, and Anthropic.

### Changed

- **`serve` now binds `127.0.0.1:8181` by default** rather than all
  interfaces. Crew Chief has no authentication and holds provider API keys, so
  a reachable port is a spending vector. Serving other hosts is an explicit
  `--addr` choice and logs a warning.
- **The gateway no longer judges output.** An earlier design ran hidden tests
  against model output and returned a `solved` verdict. That meant unsandboxed
  execution of generated code on the gateway box, and it put the definition of
  "correct" in the wrong place. `status` is now only `delivered` or `failed`,
  and retries cover mechanical failures alone. See the README for the full
  reasoning.
- **The offline model grader moved to
  [chaio-bench](https://github.com/ChadDahlgren/chaio-bench).** It drives model
  endpoints directly and never touched the gateway, and keeping its JavaScript
  dependency surface in a single-binary Go project served nobody.

### Fixed

- **Panic on any preset without `timeout_sec`.** A zero timeout left
  `context.CancelFunc` nil, and the unconditional `defer cancel()` dereferenced
  it — so every delegation to such a preset crashed. Hand-writing a minimal
  preset is a common first step, which made this easy to hit and hard to
  diagnose.
- **`usage` and `doctor` ignored a valid `CREWCHIEF_URL`.** The CLI carried its
  own copy of the gateway-URL resolver, which drifted from the MCP server's.
  Both now share one implementation, honoring `CHAIO_CREWCHIEF_URL`, then
  `CREWCHIEF_URL`, then `DISPATCH_URL`.

### Security

- Gateway binds loopback by default (see Changed).
- No authentication exists. The threat model is documented in
  [SECURITY.md](SECURITY.md) — read it before exposing the port.

[Unreleased]: https://github.com/ChadDahlgren/chaio-crewchief/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/ChadDahlgren/chaio-crewchief/releases/tag/v0.4.0
