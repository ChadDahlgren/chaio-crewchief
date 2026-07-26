#!/bin/bash
# Fleet-aware statusline: wraps the user's existing statusline (if any) and
# appends the Dispatch ledger's savings figure, cached for 60s so the gateway
# isn't hammered on every render.
#
# Opt in (plugins can't ship statuslines) — in ~/.claude/settings.json:
#   "statusLine": { "type": "command",
#     "command": "bash ~/.claude/plugins/<...>/fleet/scripts/fleet-statusline.sh" }
# Set FLEET_INNER_STATUSLINE to your previous script to keep its output.

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

if [ -z "$fresh" ]; then
  stats=$(curl -s -m 2 ${DISPATCH_URL:-http://localhost:8181}/stats 2>/dev/null)
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
else
  printf "%b%s%b" "$FLEET_COLOR" "${fresh:-fleet: gateway unreachable}" "$RESET"
fi
