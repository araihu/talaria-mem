#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp "${TMPDIR:-/tmp}/talaria-sqlc-negative.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
cp "$root/internal/adapters/sqlite/sqlc.manifest" "$tmp"
sed '1s/^[0-9a-f]/0/' "$tmp" >"$tmp.rewritten"
mv "$tmp.rewritten" "$tmp"
set +e
TALARIA_SQLC_MANIFEST="$tmp" sh "$root/scripts/check-sqlc-generated.sh"
status=$?
set -e
if [ "$status" -ne 1 ]; then
	echo "expected tampered sqlc manifest to fail with exit 1, got $status" >&2
	exit 1
fi
printf 'task=%s scope=sqlc-manifest phase=RED expected_exit=1 actual_exit=%s\n' "${TALARIA_SQLC_NEGATIVE_TASK:-cp1-a-6}" "$status"
