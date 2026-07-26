#!/bin/bash
# Fleet-aware statusline: wraps the user's existing statusline (if any) and
# appends the Crew Chief ledger's savings figure, cached for 60s so the gateway
# isn't hammered on every render.
#
# Opt in (plugins can't ship statuslines) — in ~/.claude/settings.json:
#   "statusLine": { "type": "command",
#     "command": "bash ~/.claude/plugins/<...>/fleet/scripts/fleet-statusline.sh" }
# Set FLEET_INNER_STATUSLINE to your previous script to keep its output.
#
# Requires CHAIO_CREWCHIEF_URL. Without it, this script appends nothing and
# passes the inner statusline through untouched.
#
# The reason is that there is nothing for a shell script to query otherwise.
# Crew Chief's default is a gateway embedded in the MCP process on an ephemeral
# loopback port that only that process knows — no fixed address exists to curl.
# The alternative, shelling out to `chaio-crewchief usage --local`, would start
# a whole embedded instance (loopback listener, ownership lock, startup reaper)
# every 60 seconds from a prompt renderer, which is far too much machinery to
# run as a side effect of drawing a status line.
#
# So it stays quiet. A statusline that announced "gateway unreachable" on every
# prompt of a session where delegation is working fine would be worse than one
# that shows nothing at all.

input=$(cat)

inner=""
INNER_CMD="${FLEET_INNER_STATUSLINE:-$HOME/.claude/statusline-command.sh}"
if [ -f "$INNER_CMD" ]; then
  inner=$(echo "$input" | bash "$INNER_CMD")
fi

CACHE="${TMPDIR:-/tmp}/fleet-statusline-cache"
now=$(date +%s)
fresh=""
if [ -f "$CACHE" ]; then
  age=$(( now - $(stat -f %m "$CACHE" 2>/dev/null || stat -c %Y "$CACHE") ))
  [ "$age" -lt 60 ] && fresh=$(cat "$CACHE")
fi

if [ -z "$fresh" ] && [ -n "${CHAIO_CREWCHIEF_URL:-}" ]; then
  stats=$(curl -s -m 2 "${CHAIO_CREWCHIEF_URL%/}/stats" 2>/dev/null)
  if [ -n "$stats" ]; then
    fresh=$(echo "$stats" | jq -r '.totals | "fleet: $\(.cost_usd|.*100|round/100) vs $\(.counterfactual_usd|round) (\(.savings_pct*100|round)% saved)"' 2>/dev/null)
    [ -n "$fresh" ] && echo "$fresh" > "$CACHE"
  fi
fi

FLEET_COLOR="\033[2;32m"
RESET="\033[0m"
SEP="\033[2m|\033[0m"

if [ -n "$inner" ] && [ -n "$fresh" ]; then
  printf "%b %b %b%s%b" "$inner" "$SEP" "$FLEET_COLOR" "$fresh" "$RESET"
elif [ -n "$inner" ]; then
  printf "%b" "$inner"
elif [ -n "$fresh" ]; then
  printf "%b%s%b" "$FLEET_COLOR" "$fresh" "$RESET"
fi
# Nothing to say and no inner statusline: print nothing rather than an error.
