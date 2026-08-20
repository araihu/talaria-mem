#!/bin/sh
set -eu

# One owner-controlled executable serves all four Codex hook events. It never
# places the bearer token in arguments or diagnostics; the daemon receives the
# original bounded JSON body and retains only its typed safe fields.
endpoint=${TALARIA_ENDPOINT:-__TALARIA_ENDPOINT__}
config_dir=${TALARIA_CONFIG_DIR:-__TALARIA_CONFIG_DIR__}
token_file=${TALARIA_TOKEN_FILE:-__TALARIA_TOKEN_FILE__}

case "$endpoint" in
  http://127.0.0.1:*) port=${endpoint#http://127.0.0.1:} ;;
  *) port=invalid ;;
esac
case "$port" in
  ''|??????*|*[!0-9]*)
    printf '%s\n' 'Talaria-Mem requires literal loopback endpoint http://127.0.0.1:<port>.' >&2
    exit 1
    ;;
esac
if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
  printf '%s\n' 'Talaria-Mem requires a valid loopback port.' >&2
  exit 1
fi
if [ ! -r "$token_file" ]; then
  printf '%s\n' 'Talaria-Mem daemon token is unavailable; run setup first.' >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  printf '%s\n' 'Talaria-Mem requires curl for Codex hooks.' >&2
  exit 1
fi

body_file=$(mktemp "${TMPDIR:-/tmp}/talaria-mem-hook.XXXXXX") || {
  printf '%s\n' 'Talaria-Mem could not create a temporary hook buffer.' >&2
  exit 1
}
cleanup() { rm -f "$body_file"; }
trap cleanup 0 1 2 3 15
cat >"$body_file"

# Codex sends hook_event_name in the JSON object. Unknown fields are still
# passed through the bounded request body and discarded by the daemon parser.
event=$(sed -n 's/.*"hook_event_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <"$body_file" | head -n 1)
case "$event" in
  SessionStart) route=/control/v1/session-start ;;
  UserPromptSubmit) route=/control/v1/user-prompt-submit ;;
  PreCompact) route=/control/v1/pre-compact ;;
  SessionEnd) route=/control/v1/session-end ;;
  *)
    printf '%s\n' 'Talaria-Mem received an unsupported Codex hook event.' >&2
    exit 1
    ;;
esac

token=$(tr -d '\r\n' <"$token_file")
if [ -z "$token" ]; then
  printf '%s\n' 'Talaria-Mem daemon token is empty; run setup first.' >&2
  exit 1
fi

if ! response=$(curl --silent --show-error --noproxy '*' \
  --connect-timeout 1 --max-time 5 --request POST \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header "Authorization: Bearer $token" \
  --data-binary @"$body_file" --write-out '\n%{http_code}' "$endpoint$route"); then
  printf '%s\n' 'Talaria-Mem daemon unavailable; start talaria-mem daemon and retry the hook.' >&2
  exit 1
fi

status=$(printf '%s\n' "$response" | tail -n 1)
body=$(printf '%s\n' "$response" | sed '$d')
case "$status" in
  2??) printf '%s\n' "$body" ;;
  400) printf '%s\n' 'Talaria-Mem rejected the Codex hook payload.' >&2; exit 1 ;;
  401|403) printf '%s\n' 'Talaria-Mem hook authentication failed; rerun setup.' >&2; exit 1 ;;
  5??) printf '%s\n' 'Talaria-Mem daemon unavailable; inspect talaria-mem doctor.' >&2; exit 1 ;;
  *) printf '%s\n' 'Talaria-Mem hook request failed.' >&2; exit 1 ;;
esac
