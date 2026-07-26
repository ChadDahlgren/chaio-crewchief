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
- **Breaking:** `crewchief_delegate` now refuses `async: true` in embedded
  mode, which is the default mode, and returns an error naming the fix. An
  async job returns a request ID and finishes in the background; the embedded
  gateway dies with the MCP session, so the job would vanish rather than
  finish and `crewchief_request` would poll a request nobody is running.
  Before this release an unset `CHAIO_CREWCHIEF_URL` proxied to
  `http://localhost:8181`, where async worked — so a caller that relied on it
  will now get an error where it previously got a request ID. Set
  `CHAIO_CREWCHIEF_URL` to a `serve` gateway to keep async, or drop `async` to
  run the delegation synchronously. `POST /delegate` with `async: true` against
  a `serve` gateway is unchanged.
- **Breaking:** `CHAIO_CREWCHIEF_HOME` must now be an absolute path; a relative
  value is rejected at startup instead of being resolved against the process's
  working directory. This includes a literal unexpanded `~`, which is what a
  quoted value in an MCP server config JSON produces — it used to create a
  directory named `~` and report success. Claude Code launches the MCP server
  with an arbitrary working directory, so a relative home meant the CLI and the
  MCP session could silently use different ledgers and different lock
  directories.
- `~/.chaio-crewchief/` (or `CHAIO_CREWCHIEF_HOME`) is the default location for
  config and the ledger when paths are not given as flags. `serve`'s flags are
  unchanged.
- `serve` now creates a `locks/` directory alongside its `--db` and takes an
  ownership lock inside it, so a process can tell whether the owner of an
  in-flight ledger row is still alive. This is a new writable-directory
  requirement for existing deployments: `serve` must be able to create a
  subdirectory next to `--db`. It does not fail to start if it cannot — it logs
  a warning and disables orphan reaping, which leaves requests abandoned by an
  exited process stuck in `running` until cleaned up by hand.
- `fleet-statusline.sh` no longer falls back to `http://localhost:8181` when
  `CHAIO_CREWCHIEF_URL` is unset; it now passes the inner statusline through
  untouched. Embedded mode has no fixed address to curl — the gateway runs
  in-process on a kernel-assigned ephemeral loopback port — so embedded-mode
  users get no fleet statusline. This is a deliberate trade, not an
  improvement: silence beats a false "gateway unreachable" on every prompt of
  a session where delegation is working fine.
- The ledger opens with `journal_mode=WAL` and a `busy_timeout` instead of the
  default rollback journal, since embedded mode made it multi-writer by
  design. On an existing deployment, the mode is persisted into the database
  file the first time it's opened by the new binary, and two sibling files,
  `-wal` and `-shm`, appear next to it from then on — the containing
  directory needs to be writable, which the rollback journal already
  required, so this adds no new permission requirement. WAL needs shared
  memory and does not work on NFS, and there is no way to opt out: the journal
  mode is part of the connection string and is reapplied on every connection,
  so `PRAGMA journal_mode=DELETE` is undone by the next open. **A ledger on a
  network filesystem has to move to local disk** — point `CHAIO_CREWCHIEF_HOME`
  (or `serve --db`) somewhere local.

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
