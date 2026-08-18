#!/bin/sh
set -eu

# Talaria-Mem owns no Codex binary lifecycle. This hook sends the event to the
# already-running authenticated loopback daemon and prints its bounded JSON
# response. Unknown event fields stay inside the daemon decoder.
endpoint=${TALARIA_ENDPOINT:-__TALARIA_ENDPOINT__}
config_dir=${TALARIA_CONFIG_DIR:-${HOME:?}/.talaria-mem/config}
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

response_file=$(mktemp "${TMPDIR:-/tmp}/talaria-mem-session-start.XXXXXX") || {
  printf '%s\n' 'Talaria-Mem could not create a temporary hook response file.' >&2
  exit 1
}
cleanup() {
  rm -f "$response_file"
}
trap cleanup 0 1 2 3 15

if ! status=$(curl --silent --show-error --noproxy '*' \
  --connect-timeout 1 --max-time 5 --request POST \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header "Authorization: Bearer $token" \
  --data-binary @- --output "$response_file" \
  --write-out '%{http_code}' "$endpoint/control/v1/session-start"); then
  printf '%s\n' 'Talaria-Mem daemon unavailable; start talaria-mem daemon and retry SessionStart.' >&2
  exit 1
fi

case "$status" in
  2??)
    cat "$response_file"
    ;;
  400)
    printf '%s\n' 'Talaria-Mem rejected the Codex SessionStart payload; check the hook and daemon versions.' >&2
    exit 1
    ;;
  401|403)
    printf '%s\n' 'Talaria-Mem SessionStart authentication failed; rerun setup and review the token file permissions.' >&2
    exit 1
    ;;
  404)
    printf '%s\n' 'Talaria-Mem has no workspace binding for the Codex cwd; bind that path before SessionStart.' >&2
    exit 1
    ;;
  5??)
    printf 'Talaria-Mem daemon returned HTTP %s; inspect talaria-mem doctor.\n' "$status" >&2
    exit 1
    ;;
  *)
    printf 'Talaria-Mem daemon returned unexpected HTTP %s.\n' "$status" >&2
    exit 1
    ;;
esac
