# Embedded mode design

Date: 2026-07-26
Status: approved; corrected 2026-07-26 against the code while writing the plan

## Problem

`chaio-crewchief mcp` is a pure HTTP proxy. `internal/mcpserve` holds no logic:
each of its six tools maps to one request against a gateway resolved from
`CHAIO_CREWCHIEF_URL`, defaulting to `http://localhost:8181`.

So the plugin only works if the user is already running `chaio-crewchief serve`
somewhere. A person who runs `brew install`, adds the plugin, and opens Claude
Code gets an MCP server that registers successfully and then fails every tool
call against a port nothing is listening on. Nothing in the install path tells
them a second process is required.

This is an artifact of the project's origin — it began as a service on a GPU
box, where HTTP *was* the product — not a design requirement. Every component
the MCP server needs already exists and is already wired by `serve()`.

## Goals

- `chaio-crewchief mcp` works with no server, no port to configure, and no
  environment variable.
- Sharing one fleet across machines stays possible, opt-in, and unchanged.
- The two modes expose identical tool behavior.
- Nothing about the existing GX10 deployment changes.

## Non-goals

Archive retention, bearer-token auth, and request size limits remain on the
backlog and are out of scope here.

## Mode selection

One resolver, used by `mcp`, `usage`, and `doctor`:

| Condition | Mode |
|---|---|
| `CHAIO_CREWCHIEF_URL` (or legacy `CREWCHIEF_URL` / `DISPATCH_URL`) set | **gateway** — proxy to it |
| unset | **embedded** — start in-process |

`usage` and `doctor` additionally accept `--gateway` / `--local` to force a mode
regardless of the environment. This exists because a user with the variable
exported would otherwise have no way to inspect a local ledger, and vice versa.

`doctor` reports which mode it resolved and why.

### Breaking change

`gwurl.URL()` no longer defaults to `http://localhost:8181`. An unset URL now
means "embedded", not "proxy to localhost". Anyone relying on the implicit
default must set the variable explicitly. Goes in CHANGELOG under *Changed*.

## Architecture

A new `internal/embed` package owns the component wiring:

```go
type Config struct { Home string }   // resolved home directory

type Instance struct {
    BaseURL string        // http://127.0.0.1:<kernel-assigned port>
    Close   func() error  // shut the listener, close the store
}

func Start(ctx context.Context, cfg Config) (*Instance, error)
```

`Start` performs the same sequence `serve()` does today — registry, rates,
store, archive, routing, provider, `engine.NewWithRouter`, `server.New` — then
listens on `127.0.0.1:0` and serves. `serve()` is refactored to call the same
wiring with its flag-supplied paths, so the codebase has one wiring path rather
than two.

`mcp` in embedded mode calls `Start` and points the existing `mcpserve` client
at the returned `BaseURL`. `mcpserve` itself changes only in how its base URL is
chosen.

### Why a loopback listener rather than direct engine calls

Reusing `server.New` makes tool behavior identical across modes *by
construction*: there is no second implementation that can drift. The
alternative — an interface implemented twice, once over HTTP and once against
the engine — would require lifting the aggregation and shaping logic in
`/stats` and `/history` out of `internal/server` to be shared. That refactor is
the bulk of the work and is the part most likely to introduce a divergence, and
a divergence there means `usage` and `crewchief_stats` quietly disagree about
money.

The honest cost is a real listener. It binds `127.0.0.1` only, uses a
kernel-assigned port, is unauthenticated, and lives for the session. Any local
process can reach it while it is up. This is documented rather than hidden.

Auto-spawning a background `serve` daemon was rejected: it re-creates the
problem being fixed — a process the user did not ask for and cannot see — and
adds orphan, stale-PID, and port-conflict handling.

### Watchers

Registry and rates hot-reload watchers are **not** started in embedded mode.
They exist so a long-lived daemon picks up config edits; a per-session process
does not need them, and they would cost two goroutines and two fsnotify watchers
per Claude Code session.

## Paths

```
$CHAIO_CREWCHIEF_HOME  →  else  $HOME/.chaio-crewchief
├── models.yaml          registry (required for delegation)
├── rates.yaml           optional; absent → everything prices at $0
├── routing.yaml         optional; absent → registry default model
├── chaio-crewchief.db   ledger
└── archive/             prompt and response blobs
```

Resolution lives in one function in `internal/chome`.

`embed.Start` creates the home directory and `archive/` when missing — those are
its own working files. It never creates a `models.yaml`.

`serve`'s existing path flags are untouched.

XDG's config/data split was considered and rejected. It is correct about
filesystem taxonomy and wrong about what users actually do, which is go look at
their files in one place. `CHAIO_CREWCHIEF_HOME` covers anyone who disagrees.

## First run

With no `models.yaml`, `embed.Start` **succeeds** with an empty registry rather
than failing. Failing here surfaces to Claude Code as an opaque "MCP server
failed to start".

- `crewchief_health` — reports healthy but unconfigured, naming the exact path
  and directing the user to `chaio-crewchief init`.
- `crewchief_models` — empty roster, same guidance.
- `crewchief_delegate` — returns that guidance as an error rather than
  dereferencing a nil model.

A **malformed** `models.yaml` must still fail loudly. Silently treating a YAML
typo as "no models configured" would be worse than failing.

This requires `LoadRegistry` to distinguish absent from malformed. It currently
does not — it wraps every `os.ReadFile` failure as one opaque `failed to read
file` error — so adding that distinction is part of this work.

### `chaio-crewchief init`

Writes a commented starter `models.yaml` into the home directory containing an
Ollama example and a generic OpenAI-compatible example, and prints the path.

Refuses to overwrite an existing file, exiting non-zero. `--force` overwrites.

This is the only code path in the binary that writes configuration, and it runs
only when invoked by name. Auto-writing config on first run, and auto-detecting
a local Ollama, were both rejected: the first creates files in a home directory
as a side effect of an unrelated action, and the second hard-codes one vendor
into a deliberately vendor-neutral project and fails confusingly when Ollama is
running with no models pulled.

## Async

`crewchief_delegate` with `async:true` returns an error in embedded mode,
explaining that async requires a gateway outliving the session and how to run
one. An embedded process dies with the Claude Code session, so an accepted async
job would vanish.

`crewchief_request` continues to work in embedded mode — it is a read against
the ledger, and looking up a request from an earlier session is legitimate.

Supporting embedded async remains possible later; nothing here forecloses it.

## Startup reaping

Any request left in a non-terminal state by a process that no longer exists is
marked `failed`, with a reason recording that its owner is gone. This runs
immediately after the store is opened, before anything else touches the
database, and applies to **both** modes — a `serve` killed with SIGKILL strands
rows the same way.

It targets the **`requests`** table. The `attempts` table has no status column —
it records a `verdict` after an attempt completes — while `StatusRunning`, the
only non-terminal state, lives on `requests`.

### Ownership

Reaping must not fail a row owned by a live sibling. Two Claude Code sessions
running embedded mode share one SQLite file, and a naive reap would mark the
first session's in-flight request as failed.

Two columns on the request row, `owner_pid` and `owner_host`, plus a lock file
per running process at `$HOME/locks/<pid>.lock` held under `flock`.

A row is reaped only when:

1. `owner_host` matches this host — rows written by another machine are never
   touched. The GX10 ledger is written by a `serve` on that box, and nothing on
   a laptop should declare its rows dead.
2. `owner_pid` is non-zero — a zero means the row predates ownership and says
   nothing.
3. The owner's lock can be acquired, meaning nobody holds it.

Anything ambiguous is left alone. A stale row is a smaller harm than a wrongly
failed one.

**Why a lock file rather than comparing process start times.** A stored PID
alone cannot settle liveness, because PIDs are reused: a dead owner's number can
belong to an unrelated live process, and the orphan would then never be cleaned
up. Comparing the process start time against a recorded one fixes that, but
obtaining a start time portably means reading `/proc/<pid>/stat` on Linux and a
`KERN_PROC` sysctl on macOS — per-OS code or a new dependency, to answer a
question the kernel already answers. An `flock` is released when its holder
dies, however it dies. If the lock can be taken, the owner is gone.
`syscall.Flock` covers every target platform; there is no Windows target.

### Writing the owner

The store is told its owner once, when it is opened, and stamps every request it
records. The engine records requests knowing nothing about processes, so without
this every row would carry `owner_pid = 0`, be skipped by guard 2 above, and
reaping would silently never fire.

## Error handling

Embedded startup is the new risk surface, because failures there reach the user
as an opaque MCP startup error. `embed.Start` therefore fails on exactly two
conditions:

- an unreadable or corrupt store
- a malformed (not missing) configuration file

Every other condition degrades to a running server that explains itself through
tool responses. Startup errors are also written to stderr in full, since that is
what lands in the MCP logs.

## Testing

- `internal/embed`: `Start` against a temporary home — `BaseURL` answers
  `/health`; `Close` releases both the port and the database.
- Missing `models.yaml` → `Start` succeeds, `/models` is empty, `/delegate`
  returns the guidance error. Malformed `models.yaml` → `Start` fails. These are
  the two regression tests that matter most.
- Mode selection: table test over environment variable × flag, including the
  legacy `CREWCHIEF_URL` and `DISPATCH_URL` names.
- Reaping: seed rows for (dead PID, this host), (live PID, this host), (any PID,
  other host), and a row with no recorded owner; assert only the first is
  failed, and that a second reap is a no-op. Separately, assert that a recorded
  request carries its owner automatically — without that, reaping never fires in
  production no matter how correct the rest of it is.
- `init`: writes when absent, refuses when present, `--force` overwrites, and
  the emitted file is valid input to `LoadRegistry`.
- CI smoke: a second MCP handshake with `CHAIO_CREWCHIEF_URL` unset and an empty
  temporary home, asserting the server starts and `tools/list` returns. This is
  the exact scenario broken today, so CI should guard it.

## Documentation

- README leads with the Claude Code path: `brew install` → add plugin → `init` →
  delegate. Gateway mode moves to a "sharing a fleet across machines" section.
- README and SECURITY.md both name the loopback listener explicitly: embedded
  mode binds an ephemeral unauthenticated `127.0.0.1` port for the life of the
  session, reachable by any local process. Same threat model as the gateway,
  smaller window.
- CHANGELOG records the unset-URL behavior change under *Changed*.

## Related fix

The plugin should fail with a readable message when the binary is not on
`PATH`. Today this is a cryptic MCP startup error.
