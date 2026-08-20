#!/bin/sh
set -eu

# Opt-in compatibility check. Ordinary tests use the checked-in schema
# snapshot and never require Codex or a provider call.
command -v codex >/dev/null 2>&1 || {
  printf '%s\n' 'codex is unavailable; run this opt-in check on a Codex host.' >&2
  exit 2
}
schema_tmp=$(mktemp -d "${TMPDIR:-/tmp}/talaria-codex-schema.XXXXXX")
generated_tmp=$(mktemp -d "${TMPDIR:-/tmp}/talaria-codex-generated.XXXXXX")
cleanup() { rm -rf "$schema_tmp" "$generated_tmp"; }
trap cleanup 0 1 2 3 15

codex --version
codex app-server generate-json-schema --out "$schema_tmp"
go run ./cmd/codexrpcgen \
  -schema "$schema_tmp" \
  -manifest ./api/codex/methods.json \
  -out "$generated_tmp"

printf '%s\n' 'Codex schema generation completed; compare reviewed protocol drift manually.'
