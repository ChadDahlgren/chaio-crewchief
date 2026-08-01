#!/usr/bin/env bash
# UserPromptSubmit hook: re-inject the delegation policy every turn.
#
# Everything else Crew Chief ships is advisory — the delegate skill loads only if
# the model decides it's relevant (the exact decision that fails), and the agents
# and MCP tools are reachable only once something already chose to delegate. A
# policy read once, early, loses to context pressure. This one cannot decay,
# because it does not depend on the model electing to load anything.
#
# stdout from a UserPromptSubmit hook is added to the model's context. Silence is
# the correct output whenever the fleet isn't there to be used.

set -u

CC_HOME="${CHAIO_CREWCHIEF_HOME:-$HOME/.chaio-crewchief}"

# Plugin hooks fire in EVERY project where the plugin is enabled, including repos
# with no fleet at all. Stay quiet where irrelevant.
[ -e "$CC_HOME/gate-off" ] && exit 0
command -v chaio-crewchief >/dev/null 2>&1 || exit 0

# A shape heuristic, not a model table. The Model Lab matrix (12 builders x 3
# languages, July 2026) found routing collapses to ~4 useful buckets, while the
# variables that actually predicted success were unit size, language, and spec
# precision. Those are what's encoded here — and they stay true as the roster
# churns, where a model table would rot silently.
cat <<'EOF'
[crew chief] Fleet delegation is active.
Delegable: ONE file, under ~250 lines, spec precise enough to write a test against.
Route it through crewchief_delegate or the fleet-worker agent instead of writing
it inline.
Not delegable - keep these yourself: architecture, ambiguous specs, cross-cutting
refactors, anything needing repo-wide context, engine-class or stateful files.
You verify every artifact. The gateway never judges quality.
EOF

exit 0
