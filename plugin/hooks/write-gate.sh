#!/usr/bin/env bash
# PreToolUse hook on Write: deny brand-new, self-contained code files that are
# big enough to be fleet-shaped when no delegation just happened.
#
# The trigger is FILE SHAPE, never "is this delegable" — the latter is precisely
# the judgment call the model is already failing to make. Every condition below is
# decidable by a shell script with no semantics, which is what makes it reliable.
#
# FAIL OPEN, ALWAYS. Missing jq, missing sqlite3, unreadable ledger, malformed
# payload, unset HOME — all exit 0 and allow. A gate that strands the operator
# gets deleted, and then there is no gate at all.

set -u

CC_HOME="${CHAIO_CREWCHIEF_HOME:-$HOME/.chaio-crewchief}"
DB="$CC_HOME/chaio-crewchief.db"
MODELS="$CC_HOME/models.yaml"
OVERRIDE="$CC_HOME/override"
LOG="$CC_HOME/gate.log"

# Lines at or below this are small enough that delegation overhead may exceed the
# benefit. See the band in the design doc: <120 not worth it, 120-250 the fleet's
# sweet spot, >250 must be SPLIT rather than delegated whole (engine-class files
# went 0-for-24 locally).
THRESHOLD=120
LOOKBACK_MIN=10

allow() { exit 0; }

# Logs only files the gate actually considered — new code files over the
# threshold. That candidate set is the denominator the operator wants ("how often
# should this have been delegated"); logging every .md and every 12-line helper
# would bury it.
log_decision() {
	local decision="$1" path="$2" lines="$3" reason="$4" stamp
	stamp=$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null) || return 0
	mkdir -p "$CC_HOME" 2>/dev/null || return 0
	printf '%s\t%s\t%s\t%s\t%s\n' \
		"$stamp" "$decision" "$path" "$lines" "$reason" >>"$LOG" 2>/dev/null || true
}

deny() {
	local path="$1" lines="$2"
	log_decision deny "$path" "$lines" no-recent-delegation
	local reason
	reason=$(
		cat <<EOF
Crew Chief gate: new code file, $lines lines, no recent delegation.
This is fleet-shaped work. Either:
  - route it through crewchief_delegate (or spawn fleet-worker), then write the
    returned artifact; or
  - if it is over ~250 lines, split it into smaller single-file contracts and
    delegate those - oversized units fail locally with near-certainty.
If it genuinely must be inline (cross-cutting, needs repo context, or the fleet
already failed it), run:
  touch $OVERRIDE
and retry the write.
EOF
	)
	jq -nc --arg r "$reason" '{
	  hookSpecificOutput: {
	    hookEventName: "PreToolUse",
	    permissionDecision: "deny",
	    permissionDecisionReason: $r
	  }
	}' 2>/dev/null || allow
	exit 0
}

# ---- conditions 1 and 3: kill switch, and is there a Crew Chief here at all ----
[ -e "$CC_HOME/gate-off" ] && allow
command -v chaio-crewchief >/dev/null 2>&1 || allow
command -v jq >/dev/null 2>&1 || allow

payload=$(cat) || allow
FILE_PATH=$(printf '%s' "$payload" | jq -r '.tool_input.file_path // empty' 2>/dev/null) || allow
[ -n "$FILE_PATH" ] || allow

# ---- condition 4: a NEW file, not an edit ----
# Edits must stay frictionless or the whole thing gets disabled within a week.
[ -e "$FILE_PATH" ] && allow

# ---- condition 6: code, never prose or config ----
case "$FILE_PATH" in
*.go | *.py | *.ts | *.tsx | *.js | *.rs | *.java | *.rb | *.c | *.cpp | *.cs | *.sh) ;;
*) allow ;;
esac

# ---- condition 5: over the threshold ----
# awk over wc -l: wc undercounts by one when the content has no trailing newline,
# which is exactly how model-generated content often arrives.
content=$(printf '%s' "$payload" | jq -r '.tool_input.content // empty' 2>/dev/null) || allow
[ -n "$content" ] || allow
lines=$(printf '%s' "$content" | awk 'END{print NR}' 2>/dev/null) || allow
[ "$lines" -gt "$THRESHOLD" ] 2>/dev/null || allow

# ---- condition 8: is there a fleet to delegate TO ----
# Without this the gate deadlocks the newest users: deny the write, tell the model
# to delegate, delegation refuses for lack of a roster, no legal path forward.
# Cheap by necessity — `doctor` does a live network probe per preset, far too slow
# for a PreToolUse hook. An uncommented "- name:" is the whole test.
grep -qE '^[[:space:]]*-[[:space:]]*name:' "$MODELS" 2>/dev/null || {
	log_decision allow "$FILE_PATH" "$lines" no-fleet
	allow
}

# ---- condition 7: did a delegation just land ----
# The happy path is delegate -> fleet returns artifact -> brain writes it to a new
# file, frequently over 120 lines. Without a lookback the gate deadlocks on the
# most common path in the system.
#
# created_at is RFC3339 ("2026-08-01T17:00:10Z"), NOT sqlite's datetime() format
# ("2026-08-01 18:09:08"). Comparing the two is a string compare, and at offset 10
# 'T' (84) beats ' ' (32) — so any row from the SAME DAY sorts as recent no matter
# how old it is. Verified: a 6-hour-old row satisfies datetime('now','-10 minutes').
# One delegation in the morning would disable the gate until midnight. Matching the
# stored format with strftime is load-bearing, not stylistic.
command -v sqlite3 >/dev/null 2>&1 || {
	log_decision allow "$FILE_PATH" "$lines" no-sqlite3
	allow
}
recent=$(sqlite3 -readonly "$DB" \
	"SELECT COUNT(*) FROM requests
	  WHERE status = 'delivered'
	    AND created_at > strftime('%Y-%m-%dT%H:%M:%SZ','now','-$LOOKBACK_MIN minutes');" \
	2>/dev/null) || recent=""

# An absent or corrupt ledger means condition 7 is unevaluable. Treat that as
# "a delegation exists" (allow), never as a denial.
case "$recent" in
'' | *[!0-9]*)
	log_decision allow "$FILE_PATH" "$lines" ledger-unreadable
	allow
	;;
esac
[ "$recent" -gt 0 ] && {
	log_decision allow "$FILE_PATH" "$lines" recent-delegation
	allow
}

# ---- condition 2: the override token ----
# Checked last, and consumed only here. An override buys one GATED write; spending
# it on a write that was going to be allowed anyway would make it useless.
if [ -e "$OVERRIDE" ]; then
	rm -f "$OVERRIDE" 2>/dev/null || true
	log_decision override "$FILE_PATH" "$lines" token-consumed
	allow
fi

deny "$FILE_PATH" "$lines"
