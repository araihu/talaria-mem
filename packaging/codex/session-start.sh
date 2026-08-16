#!/bin/sh
set -eu

# Talaria-Mem owns no Codex binary lifecycle. This hook sends the event to the
# already-running authenticated loopback daemon and prints its bounded JSON
# response. Unknown event fields stay inside the daemon decoder.
endpoint=${TALARIA_ENDPOINT:-http://127.0.0.1:7437}
config_dir=${TALARIA_CONFIG_DIR:-${HOME:?}/.config/talaria-mem}
token_file=${TALARIA_TOKEN_FILE:-$config_dir/token}

case "$endpoint" in
  http://127.0.0.1:*) port=${endpoint#http://127.0.0.1:} ;;
  *) port=invalid ;;
esac
case "$port" in
  ''|??????*|*[!0-9]*)
    printf '%s\n' 'Talaria-Mem SessionStart requires literal loopback endpoint http://127.0.0.1:<port>.' >&2
    exit 1
    ;;
esac
if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
  printf '%s\n' 'Talaria-Mem SessionStart requires a valid loopback port.' >&2
  exit 1
fi

if [ ! -r "$token_file" ]; then
  printf '%s\n' 'Talaria-Mem daemon token is unavailable; start talaria-mem setup or daemon first.' >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  printf '%s\n' 'Talaria-Mem SessionStart requires curl; install it or run the daemon client directly.' >&2
  exit 1
fi

token=$(tr -d '\r\n' <"$token_file")
if [ -z "$token" ]; then
  printf '%s\n' 'Talaria-Mem daemon token is empty; run token setup before SessionStart.' >&2
  exit 1
fi

if ! curl --silent --show-error --fail --noproxy '*' \
  --connect-timeout 1 --max-time 5 --request POST \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header "Authorization: Bearer $token" \
  --data-binary @- "$endpoint/control/v1/session-start"; then
  printf '%s\n' 'Talaria-Mem daemon unavailable; start talaria-mem daemon and retry SessionStart.' >&2
  exit 1
fi
