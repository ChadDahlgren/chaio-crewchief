#!/usr/bin/env bash
# Tests for the delegation hooks.
#
# The gate's decision is a pure function of (payload, filesystem, ledger), so it
# tests directly: build a throwaway home with a real requests table, pipe fixture
# JSON in, assert on stdout. No Claude Code involved.
#
#   ./plugin/hooks/test-hooks.sh
#
# Cases 4 and 6 are the only denials. Everything else exists to prove the gate
# stays out of the way, which matters more than the denials do — a gate that
# blocks legitimate work gets deleted.

set -u

HERE=$(cd "$(dirname "$0")" && pwd)
GATE="$HERE/write-gate.sh"
RAIL="$HERE/policy-rail.sh"

pass=0
fail=0

# Every test runs against a throwaway home under this root. Nothing is ever
# removed recursively; the OS reaps the temp dir.
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/crewchief-hooktest.XXXXXX") || exit 1
echo "test root: $ROOT"

# Three sandbox PATHs built from symlinks. Testing "the binary is absent" by
# prepending to PATH is impossible — prepending can only ADD. So each sandbox
# contains exactly the tools that case should see, and PATH is replaced outright.
TOOLS="bash jq sqlite3 awk grep cat date mkdir rm sed touch"
for dir in full nocc nosqlite; do
	mkdir -p "$ROOT/$dir"
	for t in $TOOLS; do
		[ "$dir" = nosqlite ] && [ "$t" = sqlite3 ] && continue
		src=$(command -v "$t" 2>/dev/null) || { echo "missing required tool: $t"; exit 1; }
		ln -sf "$src" "$ROOT/$dir/$t"
	done
	# A fake chaio-crewchief: condition 3 only asks whether the binary exists, and
	# depending on a real install would make the suite unrunnable in CI.
	if [ "$dir" != nocc ]; then
		printf '#!/bin/sh\nexit 0\n' >"$ROOT/$dir/chaio-crewchief"
		chmod +x "$ROOT/$dir/chaio-crewchief"
	fi
done
export PATH="$ROOT/full"

case_n=0

# new_home [minutes_ago]
# Fresh CHAIO_CREWCHIEF_HOME with a configured roster and a requests table.
# minutes_ago, when given, seeds one delivered row that many minutes in the past.
new_home() {
	case_n=$((case_n + 1))
	CC="$ROOT/home$case_n"
	mkdir -p "$CC"
	printf 'models:\n  - name: test-model\n    base_url: http://localhost:1\n' >"$CC/models.yaml"
	sqlite3 "$CC/chaio-crewchief.db" "
	  CREATE TABLE requests (
	    id TEXT PRIMARY KEY, task TEXT NOT NULL, model TEXT, mode TEXT,
	    tests TEXT, lang TEXT, async INTEGER NOT NULL DEFAULT 0,
	    status TEXT NOT NULL, artifact TEXT NOT NULL DEFAULT '',
	    escalation TEXT NOT NULL DEFAULT '', owner_pid INTEGER NOT NULL DEFAULT 0,
	    owner_host TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);"
	if [ "$#" -gt 0 ]; then
		sqlite3 "$CC/chaio-crewchief.db" "
		  INSERT INTO requests (id, task, status, created_at)
		  VALUES ('r$case_n', 'x', 'delivered',
		          strftime('%Y-%m-%dT%H:%M:%SZ','now','-$1 minutes'));"
	fi
	export CHAIO_CREWCHIEF_HOME="$CC"
}

# payload <path> <line_count>
payload() {
	local body
	body=$(awk -v n="$2" 'BEGIN{for(i=0;i<n;i++) print "x := 1"}')
	jq -nc --arg p "$1" --arg c "$body" \
		'{tool_name:"Write", tool_input:{file_path:$p, content:$c}}'
}

# check <name> <expected: allow|deny> <actual stdout>
check() {
	local name="$1" want="$2" out="$3" got
	if printf '%s' "$out" | grep -q '"permissionDecision"[[:space:]]*:[[:space:]]*"deny"'; then
		got=deny
	else
		got=allow
	fi
	if [ "$got" = "$want" ]; then
		pass=$((pass + 1))
		printf '  ok   %s (%s)\n' "$name" "$got"
	else
		fail=$((fail + 1))
		printf '  FAIL %s: wanted %s, got %s\n' "$name" "$want" "$got"
		printf '       stdout: %s\n' "$out"
	fi
}

echo
echo "write-gate"

# 1. Existing file, 300 lines of Go -> allow (an edit, not a new file)
new_home
touch "$CC/existing.go"
check "existing file" allow "$(payload "$CC/existing.go" 300 | "$GATE")"

# 2. New .go file, 40 lines -> allow (under the threshold)
new_home
check "under threshold" allow "$(payload "$CC/small.go" 40 | "$GATE")"

# 3. New .md file, 400 lines -> allow (prose is never gated)
new_home
check "markdown" allow "$(payload "$CC/doc.md" 400 | "$GATE")"

# 4. New .go file, 300 lines, empty ledger -> DENY
new_home
out=$(payload "$CC/big.go" 300 | "$GATE")
check "empty ledger" deny "$out"
printf '%s' "$out" | grep -q 'crewchief_delegate' ||
	{ fail=$((fail + 1)); echo "  FAIL denial reason lost the delegate instruction"; }

# 5. Delivered row 2 minutes old -> allow (the artifact touching down)
new_home 2
check "recent delegation" allow "$(payload "$CC/big.go" 300 | "$GATE")"

# 6. Delivered row 30 minutes old -> DENY (outside the 10-minute lookback)
new_home 30
check "stale delegation" deny "$(payload "$CC/big.go" 300 | "$GATE")"

# 7. Override token present -> allow, and the token is consumed
new_home
touch "$CHAIO_CREWCHIEF_HOME/override"
check "override" allow "$(payload "$CC/big.go" 300 | "$GATE")"
if [ -e "$CHAIO_CREWCHIEF_HOME/override" ]; then
	fail=$((fail + 1)); echo "  FAIL override token survived; it buys one write, not a disabled gate"
else
	pass=$((pass + 1)); echo "  ok   override consumed"
fi

# 7b. An override must NOT be spent on a write that was allowed anyway.
new_home 2
touch "$CHAIO_CREWCHIEF_HOME/override"
check "override not spent on allowed write" allow "$(payload "$CC/big.go" 300 | "$GATE")"
if [ -e "$CHAIO_CREWCHIEF_HOME/override" ]; then
	pass=$((pass + 1)); echo "  ok   override preserved"
else
	fail=$((fail + 1)); echo "  FAIL override wasted on a write the gate was going to allow"
fi

# 8. Kill switch -> allow
new_home
touch "$CHAIO_CREWCHIEF_HOME/gate-off"
check "kill switch" allow "$(payload "$CC/big.go" 300 | "$GATE")"

# 9. chaio-crewchief not on PATH -> allow
new_home
p=$(payload "$CC/big.go" 300)
check "binary absent" allow "$(printf '%s' "$p" | PATH="$ROOT/nocc" "$GATE")"

# 10. Malformed JSON -> allow, exit 0
new_home
out=$(printf 'not json at all' | "$GATE"); rc=$?
check "malformed payload" allow "$out"
if [ "$rc" -eq 0 ]; then
	pass=$((pass + 1)); echo "  ok   malformed payload exits 0"
else
	fail=$((fail + 1)); echo "  FAIL malformed payload exited $rc; hooks must fail open"
fi

# 11. sqlite3 unavailable -> allow (condition 7 becomes unevaluable, so it yields)
new_home
p=$(payload "$CC/big.go" 300)
check "no sqlite3" allow "$(printf '%s' "$p" | PATH="$ROOT/nosqlite" "$GATE")"

# 12. models.yaml absent -> allow (no fleet configured)
new_home
rm -f "$CHAIO_CREWCHIEF_HOME/models.yaml"
check "no roster" allow "$(payload "$CC/big.go" 300 | "$GATE")"

# 13. models.yaml present but every preset commented out -> allow
new_home
printf 'models: []\n#  - name: local\n' >"$CHAIO_CREWCHIEF_HOME/models.yaml"
check "all presets commented" allow "$(payload "$CC/big.go" 300 | "$GATE")"

echo
echo "policy-rail"

new_home
out=$("$RAIL")
if printf '%s' "$out" | grep -q 'crew chief'; then
	pass=$((pass + 1)); echo "  ok   emits policy when the binary is present"
else
	fail=$((fail + 1)); echo "  FAIL emitted nothing with chaio-crewchief on PATH"
fi

out=$(PATH="$ROOT/nocc" "$RAIL")
if [ -z "$out" ]; then
	pass=$((pass + 1)); echo "  ok   silent when the binary is absent"
else
	fail=$((fail + 1)); echo "  FAIL emitted policy with no chaio-crewchief on PATH: $out"
fi

echo
printf '%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
