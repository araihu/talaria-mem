#!/bin/sh
set -eu

# Opt-in compatibility check. Ordinary tests use the checked-in schema
# snapshot and never require Codex or a provider call.
command -v codex >/dev/null 2>&1 || {
  printf '%s\n' 'codex is unavailable; run this opt-in check on a Codex host.' >&2
  exit 2
}
command -v jq >/dev/null 2>&1 || {
  printf '%s\n' 'jq is required to compare generated Codex schemas.' >&2
  exit 2
}
schema_tmp=$(mktemp -d "${TMPDIR:-/tmp}/talaria-codex-schema.XXXXXX")
generated_tmp=$(mktemp -d "${TMPDIR:-/tmp}/talaria-codex-generated.XXXXXX")
cleanup() { rm -rf "$schema_tmp" "$generated_tmp"; }
trap cleanup 0 1 2 3 15

codex --version
codex app-server generate-json-schema --out "$schema_tmp"

snapshot_dir=./api/codex/schema/0.144.5
for schema_name in codex_app_server_protocol.schemas.json codex_app_server_protocol.v2.schemas.json; do
  if [ ! -f "$snapshot_dir/$schema_name" ] || [ ! -f "$schema_tmp/$schema_name" ]; then
    printf 'Codex schema file missing: %s\n' "$schema_name" >&2
    exit 1
  fi
  jq -S . "$snapshot_dir/$schema_name" > "$generated_tmp/committed-$schema_name"
  jq -S . "$schema_tmp/$schema_name" > "$generated_tmp/host-$schema_name"
  if ! cmp -s "$generated_tmp/committed-$schema_name" "$generated_tmp/host-$schema_name"; then
    printf 'Codex schema drift detected for %s.\n' "$schema_name" >&2
    exit 1
  fi
done
go run ./cmd/codexrpcgen \
  -schema "$schema_tmp" \
  -manifest ./api/codex/methods.json \
  -out "$generated_tmp"

printf '%s\n' 'Codex host schemas match the reviewed 0.144.5 snapshot.'
