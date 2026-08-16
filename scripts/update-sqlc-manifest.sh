#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
generated="$root/internal/adapters/sqlite/sqlc"
manifest="$root/internal/adapters/sqlite/sqlc.manifest"
tmp=$(mktemp "${TMPDIR:-/tmp}/talaria-sqlc-manifest.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM

hash_file() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
		return
	fi
	sha256sum "$1" | awk '{print $1}'
}

find "$generated" -type f -print | sort | while IFS= read -r file; do
	rel=${file#"$root/"}
	printf '%s  %s\n' "$(hash_file "$file")" "$rel"
done >"$tmp"
mv "$tmp" "$manifest"
trap - EXIT HUP INT TERM
