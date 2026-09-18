#!/usr/bin/env bash
# VERSION 3
set -euo pipefail

WORKSPACE=$(git rev-parse --show-toplevel 2>/dev/null || echo "$PWD")

SANDBOX_COMMAND=opencode
SANDBOX_COMMAND_DEFAULTS=()

if [[ $# -eq 0 || "${1:-}" == -* ]]; then
    set -- ${SANDBOX_COMMAND_DEFAULTS[@]+"${SANDBOX_COMMAND_DEFAULTS[@]}"} "$@"
fi

if [[ "${1:-}" == "$SANDBOX_COMMAND" ]]; then
    shift
fi

"$WORKSPACE/.sandbox/start.sh" "$SANDBOX_COMMAND" "$@"
