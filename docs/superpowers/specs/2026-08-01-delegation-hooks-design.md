# Delegation hooks — making Crew Chief fire without being asked

**Date:** 2026-08-01
**Status:** implemented — see *Corrections found during implementation* at the end

## Problem

Crew Chief is installed, configured, and works when invoked. It is almost never
invoked on the model's own initiative. Observed failure modes, in the operator's
words:

- **Cold start** — it delegates when told to, never by choice.
- **Decay** — it delegates for two or three units, then silently reverts to
  writing everything inline.
- **Silent inline** — textbook delegable work (a self-contained file against a
  clear spec) gets written by hand with no consideration of the fleet.

Routing quality is *not* a reported problem. When Crew Chief is invoked, it picks
reasonable models and the work lands. This is an activation and persistence
problem, not a judgment problem.

## Root cause

All three failures are one failure. Every delegation surface Crew Chief ships is
**advisory**:

| Surface | Why it doesn't hold |
|---|---|
| `delegate` skill | Loaded on demand. The model must first decide the skill is relevant — the exact decision that is failing. |
| `fleet-worker` / `fleet-heavy` agents | Only reachable if something already decided to delegate. |
| `crewchief_*` MCP tools | Capability, not policy. Present whether or not they are used. |

None of them survive context pressure. A policy read once, early, competes with a
growing pile of tool output and is eventually summarized away. **The policy exists
only as long as the model remembers to care about it.**

Claude Code has exactly one surface that does not depend on model discretion:
hooks. They are currently unused (`hooks` is absent from the operator's
`settings.json`). This design uses them.

## Non-goals

- **Forcing delegation.** Not achievable short of removing the model's ability to
  write code, and not desirable. The goal is that the policy is always present and
  that skipping it is visible and slightly expensive.
- **Improving routing.** Model selection is out of scope; see *Deferred work*.
- **Gating `Edit`.** Ordinary code changes must stay frictionless or the operator
  will disable the whole thing within a week.
- **Gating agent spawns.** Real annoyance, different problem, folding it in now
  would confound whether the core mechanism worked.

## Architecture

The hooks ship **inside the plugin** (`plugin/hooks/`), not in the operator's
`~/.claude/settings.json`.

Activation is a product concern, not a local configuration accident. A gateway
that relays work to cheap models is worthless if the brain never calls it, and
every Crew Chief user hits this on day one for the same structural reason. Shipping
the hooks next to the `delegate` skill they enforce makes the plugin
self-activating: install it and delegation actually happens.

Declared in `plugin/hooks/hooks.json`, with `${CLAUDE_PLUGIN_ROOT}` resolving
script paths:

```json
{
  "description": "Keeps fleet delegation active and gates new self-contained code files",
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [{ "type": "command",
                    "command": "${CLAUDE_PLUGIN_ROOT}/hooks/policy-rail.sh",
                    "timeout": 5 }] }
    ],
    "PreToolUse": [
      { "matcher": "Write",
        "hooks": [{ "type": "command",
                    "command": "${CLAUDE_PLUGIN_ROOT}/hooks/write-gate.sh",
                    "timeout": 10 }] }
    ]
  }
}
```

Consequence: plugin hooks fire in every project where the plugin is enabled,
including repos with no fleet configured. Both scripts therefore self-disable when
`chaio-crewchief` is not on `PATH`, and both honor a kill switch. Quiet where
irrelevant.

## Component 1 — the policy rail (`UserPromptSubmit`)

Emits the delegation policy to stdout before **every** turn. For
`UserPromptSubmit`, stdout is added to the model's context.

```
[crew chief] Fleet delegation is active.
Delegable: ONE file, under ~250 lines, spec precise enough to write a test against.
Route it through crewchief_delegate or the fleet-worker agent instead of writing
it inline.
Not delegable — keep these yourself: architecture, ambiguous specs, cross-cutting
refactors, anything needing repo-wide context, engine-class or stateful files.
You verify every artifact. The gateway never judges quality.
```

Roughly 70 tokens per turn. Structurally incapable of decaying, because it does
not depend on the model electing to load anything. This alone addresses cold start
and decay.

The rail carries a **shape heuristic, not a model table** — deliberately. The Model
Lab matrix (12 builders × 3 languages, July 2026) found the useful routing
dimensionality collapses to about four buckets, while the variables that actually
predicted success were unit size (engine-class files: 0-for-24 across every model
and language, unrescued by larger token budgets), language choice, and spec
precision. The shape heuristic encodes the variables with evidence behind them, and
it stays true as the roster churns. A model table would rot silently.

Exits 0 with no output when `chaio-crewchief` is absent or the kill switch is set.

## Component 2 — the write gate (`PreToolUse` on `Write`)

Reads the hook payload on stdin and inspects `tool_input.file_path` and
`tool_input.content`. Denies only when **every** condition below holds; allows
otherwise.

| # | Condition to deny | Evaluated by |
|---|---|---|
| 1 | Kill switch absent (`$HOME/.chaio-crewchief/gate-off`) | file test |
| 2 | Override token absent (`$HOME/.chaio-crewchief/override`) | file test |
| 3 | `chaio-crewchief` on `PATH` | `command -v` |
| 4 | Target path does **not** already exist — a new file, not an edit | file test |
| 5 | Content exceeds **120 lines** | `wc -l` on content |
| 6 | Extension is a code extension (`.go .py .ts .tsx .js .rs .java .rb .c .cpp .cs .sh`) — never `.md`, `.yaml`, `.json`, lock files | suffix match |
| 7 | No `delivered` request in the ledger in the last 10 minutes | SQLite query |
| 8 | The registry has at least one usable preset | grep `models.yaml` |

Every condition is decidable by a shell script with no semantic judgment. That is
what makes the gate reliable, and it is why the trigger is *file shape* rather than
*delegability* — the latter is precisely the call the model is already failing to
make.

### The 120 and 250 line numbers are a band, not a contradiction

The rail says delegable work is under ~250 lines; the gate fires above 120. Both
are correct and they bound the same region from opposite sides:

- **Under 120** — small enough that delegation overhead may exceed the benefit.
  Not gated.
- **120–250** — the fleet's sweet spot. Gated, and delegation is the expected
  answer.
- **Over 250** — too large to delegate as a single unit (engine-class files went
  0-for-24 locally). Still gated, but the correct response is to **split it into
  smaller contracts and delegate those**, or to keep it frontier-side with an
  override. It is never "write 400 lines inline without thinking about it," which
  is the behavior being corrected.

The denial message covers both responses so the model is not pushed toward
delegating an oversized unit that will fail.

### Condition 8 exists to prevent a worse deadlock

Found empirically while wiring up a Grok preset: the operator's
`~/.chaio-crewchief/models.yaml` **did not exist**. `init` had never been run, so
the roster was empty and *every* delegation would have refused — the gateway
declines to relay when no preset resolves.

Without condition 8 the gate denies the write and instructs the model to
delegate, delegation refuses because there is no roster, and the model is stuck
with no legal path forward. That is a strictly worse failure than the one this
design exists to fix, and it lands on exactly the population most likely to hit
it: someone who just installed the plugin.

The check must be cheap enough for a `PreToolUse` hook, which rules out
`chaio-crewchief doctor` — it performs live health probes against every
configured endpoint (a real network round trip per preset). Instead, scan the
resolved `models.yaml` for at least one uncommented `- name:` entry. Missing
file, `models: []`, or an all-commented starter file all mean "no fleet," and the
gate stands down.

This also means the gate activates on its own the moment a roster is configured —
no separate enable step.

### Condition 7 exists to prevent a deadlock

The happy path is: brain delegates → fleet returns an artifact → **brain writes
that artifact to a new file, frequently over 120 lines** → gate denies it. Without a
lookback the gate deadlocks on the most common path in the system.

The ledger already records what is needed. Against
`${CHAIO_CREWCHIEF_HOME:-$HOME/.chaio-crewchief}/chaio-crewchief.db`:

```sql
SELECT COUNT(*) FROM requests
WHERE status = 'delivered'
  AND created_at > datetime('now', '-10 minutes');
```

Non-zero means a delegation just landed and this `Write` is almost certainly the
artifact touching down. Allow it.

This is deliberately loose — it permits any write in the ten minutes after a
delegation, including unrelated ones. Accepted for v1: the failure mode is a missed
gate, not a blocked artifact, and the log makes the looseness measurable. If it
proves too permissive, the tightening is to match written content against the
ledger's `artifact` column rather than to shorten the window.

### Denial message

Denial uses the structured form (exit 0, `permissionDecision: "deny"`) so the
reason reaches the model:

```
Crew Chief gate: new code file, <N> lines, no recent delegation.
This is fleet-shaped work. Either:
  - route it through crewchief_delegate (or spawn fleet-worker), then write the
    returned artifact; or
  - if it is over ~250 lines, split it into smaller single-file contracts and
    delegate those — oversized units fail locally with near-certainty.
If it genuinely must be inline (cross-cutting, needs repo context, or the fleet
already failed it), run:
  touch ~/.chaio-crewchief/override
and retry the write.
```

The override token is **consumed on use** — the gate deletes it after allowing a
write. It buys one write, not a disabled gate.

`permissionDecision: "ask"` was considered as an alternative to `"deny"`: it would
escalate each gated write to the operator instead. Rejected as the default — it
interrupts on every occurrence, and the operator's standing preference is minimal
permission prompts. Worth revisiting as a configurable mode if the log shows the
model overriding indiscriminately.

## Failure handling

**Both scripts fail open.** Any error — missing `jq`, missing `sqlite3`,
unreadable ledger, malformed payload, unset `HOME` — exits 0 and allows the write.
A broken hook must never block work; a gate that strands the operator gets deleted,
and then there is no gate at all.

Consequences to accept:

- A corrupt or absent ledger means condition 7 cannot be evaluated. Treat that as
  "recent delegation exists" (allow), not as a denial.
- The `PreToolUse` timeout is 10s; the SQLite query must stay well under it. It is
  an indexed count over one small table, so this is not expected to bind, but the
  script should not retry on timeout.

## Measurement

Every gate decision appends one tab-separated line to
`$HOME/.chaio-crewchief/gate.log`:

```
<iso8601>	<allow|deny|override>	<path>	<lines>	<reason>
```

This is the point of the exercise as much as the gate itself. It is the first time
the operator gets a number for *how often work should have been delegated and
wasn't*, instead of an impression. It also supplies the denominator for judging
whether the whole approach earned its place.

## Testing

The gate's decision logic is a pure function of (payload, filesystem, ledger), so
it tests directly by piping fixture JSON to the script and asserting the exit code
and stdout.

Cases, one per condition plus the interesting combinations:

1. Existing file, 300 lines of Go → allow (edit, not new)
2. New `.go` file, 40 lines → allow (under threshold)
3. New `.md` file, 400 lines → allow (not code)
4. New `.go` file, 300 lines, empty ledger → **deny**
5. New `.go` file, 300 lines, `delivered` row 2 minutes old → allow
6. New `.go` file, 300 lines, `delivered` row 30 minutes old → **deny**
7. New `.go` file, 300 lines, override token present → allow, **token removed**
8. Kill switch present → allow
9. `chaio-crewchief` not on `PATH` → allow
10. Malformed JSON on stdin → allow, exit 0
11. `sqlite3` unavailable → allow, exit 0
12. `models.yaml` absent → allow (no fleet configured)
13. `models.yaml` present but every preset commented out → allow

Cases 4 and 6 are the only denials; everything else proves the gate stays out of
the way. Fixtures build a throwaway SQLite file with the real `requests` schema.

The policy rail needs one test: emits the policy when `chaio-crewchief` is present,
emits nothing and exits 0 when it is not.

## Assumptions carried into this spec

The operator approved Option 2 (rail + gate) and the 120-line threshold. Two
recommendations were made but not explicitly ratified, and are adopted here:

1. **Plugin packaging** over personal dotfiles (see *Architecture*).
2. **Ledger lookback** as the artifact-write escape, rather than requiring an
   override on every delegated write.

Both are reversible and both are called out for the spec review gate.

## Deferred work

Out of scope here, tracked so it is not lost:

- **Bench refresh** — shortlist candidates from public leaderboards, run the
  existing 21-problem slate, add `grok-4.20-0309` (both reasoning and non-reasoning
  variants, which hold training constant and so isolate the reasoner rule for the
  first time), update `routing.yaml`. Independent of this work.
- **Ledger feedback loop** — the brain already judges every artifact and that
  verdict currently evaporates. Writing it back would turn the ledger into a routing
  scoreboard built on real work rather than synthetic benchmarks. Requires this
  design to be working first: no delegations means no verdicts to collect. The
  gateway would store a judgment the brain made, not make one, so the
  mechanism/policy split holds.
- **Agent-spawn nudge** — the "spun up Opus for a simple function" complaint.
  Revisit once the gate log shows whether it matters.

## Corrections found during implementation

Three places where the design above is wrong or underspecified. The shipped code
follows this section, not the text above it.

### The condition-7 SQL never fires the gate

The spec's query compares an RFC3339 column against sqlite's `datetime()` format:

```sql
created_at > datetime('now', '-10 minutes')   -- WRONG
```

`created_at` is stored as `2026-08-01T17:00:10Z`; `datetime('now')` yields
`2026-08-01 18:09:08`. SQLite compares them as strings, and at offset 10 `'T'`
(84) beats `' '` (32) — so **any row from the same date sorts as recent no matter
how old it is**. Verified directly: a 6-hour-old row satisfies the predicate.

The practical effect is that one delegation in the morning disables the gate for
the rest of the calendar day. Not a corner case — it is the ordinary path.

Shipped form matches the stored format:

```sql
created_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-10 minutes')
```

Test case 6 (a 30-minute-old delivered row must still deny) is what pins this;
under the spec's SQL it fails.

### The override is checked last, not second

The spec lists the override as condition 2, which would consume the token on the
first `Write` after it is created — very likely a `.md` file or a 20-line helper
that was never going to be gated. The token is then gone and the write it was
created for gets denied anyway.

Shipped order evaluates the override **after** every other condition, so it is
spent only on a write that would otherwise be denied. "Buys one write" means one
*gated* write. Test 7b covers this.

### Logging scope

The spec says every gate decision is logged. Shipped behaviour logs only the
**candidate set** — new code files over the threshold — and the decision reached
for each. Logging every `Write` in every project where the plugin is enabled
would bury the number the log exists to produce ("how often should this have been
delegated and wasn't") under markdown and small helpers.

Kill-switch and binary-absent exits log nothing at all: in those states there is
no gate, so there is no decision to record.
