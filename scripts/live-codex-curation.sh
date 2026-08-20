#!/bin/sh
set -eu

if [ "${TALARIA_LIVE_CODEX:-}" != "1" ]; then
  printf '%s\n' 'Set TALARIA_LIVE_CODEX=1 to opt into live Codex curation.' >&2
  exit 2
fi
command -v codex >/dev/null 2>&1 || {
  printf '%s\n' 'codex is unavailable.' >&2
  exit 2
}
command -v talaria-mem >/dev/null 2>&1 || {
  printf '%s\n' 'talaria-mem is unavailable on PATH.' >&2
  exit 2
}

# This script intentionally records protocol/version metadata only. It never
# prints prompts, model responses, locators, credentials, or memory content.
codex --version
talaria-mem status --json
printf '%s\n' 'Live curation preflight passed; invoke one explicitly approved test session.'
