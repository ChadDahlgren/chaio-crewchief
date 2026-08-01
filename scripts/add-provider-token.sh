#!/usr/bin/env bash
# Add a provider API token to the Crew Chief fleet.
#
# Prompts for the token (input hidden), verifies it against the provider when
# it knows how, and writes it to two places:
#
#   1. ~/.claude/settings.json  -> the "env" block. This is the one that matters:
#      the Crew Chief MCP server is launched by Claude Code and inherits its
#      environment, NOT a login shell's. A token exported only in ~/.zshrc is
#      invisible to the MCP server when Claude Code was started from the desktop
#      app or Spotlight.
#   2. $CHAIO_CREWCHIEF_HOME/secrets.env (mode 0600) -> for `source`-ing into a
#      terminal when you want to curl the provider by hand.
#
# SECURITY: both files store the token in PLAINTEXT, readable by your user
# account. This is the same posture as ~/.aws/credentials and gh. It is not
# encryption at rest. Do not commit or share ~/.claude/settings.json after
# running this.
#
# Usage:
#   ./add-provider-token.sh                # defaults to XAI_API_KEY
#   ./add-provider-token.sh CLOUDFLARE_API_TOKEN
#
# The token is never passed as a command-line argument (that would put it in
# your shell history and in world-readable process listings).

set -euo pipefail

VAR="${1:-XAI_API_KEY}"
SETTINGS="$HOME/.claude/settings.json"
CC_HOME="${CHAIO_CREWCHIEF_HOME:-$HOME/.chaio-crewchief}"
ENV_FILE="$CC_HOME/secrets.env"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

case "$VAR" in
  *[!A-Za-z0-9_]*|'') die "'$VAR' is not a valid environment variable name" ;;
esac

command -v jq >/dev/null 2>&1 || die "jq is required (brew install jq)"

# ---------------------------------------------------------------- read token
printf 'Paste %s (input hidden), then press Enter:\n> ' "$VAR"
IFS= read -rs TOKEN || die "could not read input"
printf '\n'
[ -n "$TOKEN" ] || die "empty token, nothing written"
export TOKEN   # passed to jq via env, never argv: /proc/PID/cmdline is
               # world-readable on Linux, /proc/PID/environ is not.

# ------------------------------------------------------------------- verify
# Only xAI is verifiable without more config (Cloudflare needs an account id).
if [ "$VAR" = "XAI_API_KEY" ] && command -v curl >/dev/null 2>&1; then
  printf 'Verifying against https://api.x.ai/v1/models ... '
  if body=$(curl -sf -m 20 https://api.x.ai/v1/models \
              -H "Authorization: Bearer $TOKEN" 2>/dev/null); then
    printf 'ok\n\nModel IDs available to this token:\n'
    printf '%s\n' "$body" | jq -r '.data[].id' 2>/dev/null | sed 's/^/  /' \
      || printf '  (could not parse response)\n'
    printf '\nUse one of these as model_id in your models.yaml preset.\n\n'
  else
    printf 'FAILED\n'
    printf 'The token was rejected or the endpoint was unreachable.\n' >&2
    printf 'Nothing has been written. Check the token and try again.\n' >&2
    exit 1
  fi
fi

# --------------------------------------------------- write secrets.env (0600)
umask 077
mkdir -p "$CC_HOME"
touch "$ENV_FILE"
chmod 600 "$ENV_FILE"

tmp_env=$(mktemp "$CC_HOME/.secrets.env.XXXXXX")
trap 'rm -f "$tmp_env" "${tmp_settings:-}"' EXIT
# drop any previous line for this var, keep everything else
grep -v "^export ${VAR}=" "$ENV_FILE" > "$tmp_env" 2>/dev/null || true
printf 'export %s=%s\n' "$VAR" "$TOKEN" >> "$tmp_env"
chmod 600 "$tmp_env"
mv "$tmp_env" "$ENV_FILE"
printf 'wrote %s (mode 0600)\n' "$ENV_FILE"

# ------------------------------------------------ write ~/.claude/settings.json
mkdir -p "$(dirname "$SETTINGS")"
[ -f "$SETTINGS" ] || printf '{}\n' > "$SETTINGS"

jq -e . "$SETTINGS" >/dev/null 2>&1 \
  || die "$SETTINGS is not valid JSON; refusing to touch it"

backup="$SETTINGS.bak.$(date +%Y%m%d-%H%M%S)"
cp "$SETTINGS" "$backup"

tmp_settings=$(mktemp "${SETTINGS}.XXXXXX")
jq --arg k "$VAR" '.env = ((.env // {}) + {($k): env.TOKEN})' \
   "$SETTINGS" > "$tmp_settings"

jq -e . "$tmp_settings" >/dev/null 2>&1 \
  || die "generated settings.json was invalid; original left untouched at $SETTINGS"

chmod --reference="$SETTINGS" "$tmp_settings" 2>/dev/null || chmod 600 "$tmp_settings"
mv "$tmp_settings" "$SETTINGS"
tmp_settings=""

printf 'wrote %s (backup: %s)\n' "$SETTINGS" "$backup"

# ------------------------------------------------------------------- summary
printf '\nStored %s (ends in ...%s)\n' "$VAR" "${TOKEN: -4}"
cat <<'EOF'

Next steps:
  1. Restart Claude Code so the MCP server picks up the new environment.
     (The env block is read at startup; a running session will not see it.)
  2. Add the preset to ~/.chaio-crewchief/models.yaml, using the Grok Build 0.1
     id from the list above (fast coding model, $1.00/$2.00 per M, 256K ctx):

       - name: grok-build
         base_url: https://api.x.ai/v1
         model_id: "PASTE_THE_GROK_BUILD_ID_FROM_ABOVE"
         api_key_env: XAI_API_KEY
         health_path: /models
         provider_class: cloud
         system_prompt: "You are a code generator. Output ONLY a raw code block. No prose. Write the SIMPLEST correct implementation - prefer built-ins."
         temperature: 0.3
         max_tokens: 6000
         timeout_sec: 300

  3. Confirm the fleet sees it:
       chaio-crewchief doctor
     (There is no `chaio-crewchief models` subcommand — the roster is listed by
     the crewchief_models MCP tool. An unrecognized subcommand silently falls
     through to `serve`.)

For a terminal session (not needed for Claude Code):
  source ~/.chaio-crewchief/secrets.env
EOF
