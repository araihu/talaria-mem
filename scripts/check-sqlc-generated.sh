#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
generated="$root/internal/adapters/sqlite/sqlc"
manifest="${TALARIA_SQLC_MANIFEST:-$root/internal/adapters/sqlite/sqlc.manifest}"
tmp=$(mktemp "${TMPDIR:-/tmp}/talaria-sqlc-check.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM

if [ ! -f "$manifest" ]; then
	echo "missing sqlc generated manifest: $manifest" >&2
	exit 1
fi

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

if ! cmp -s "$manifest" "$tmp"; then
	echo "sqlc generated manifest drifted; run make sqlc-generate" >&2
	diff -u "$manifest" "$tmp" >&2 || true
	exit 1
fi

if git -C "$root" ls-files --others --exclude-standard -- "$generated" | grep -q .; then
	echo "untracked files found in sqlc generated directory" >&2
	exit 1
fi
