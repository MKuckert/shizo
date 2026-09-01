#!/bin/sh
# PreToolUse wrapper for bash-gate. Fail-closed: any unrecognised outcome
# (crash exit, empty body, non-JSON body) is turned into a blocking deny (exit 2).
# The one honoured non-JSON stdout is the literal `defer` sentinel (returntype
# automode), mapped to empty output. See PLAN §2.2.
dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
log="$HOME/.claude-bash-rules/logs"
mkdir -p "$log" 2>/dev/null

# No decision may run unlogged: if the log dir is not writable (it is read-only
# from inside the sandbox until a logs write grant is added), fail closed with a
# specific, actionable deny instead of the cryptic crash-deny below. The probe
# runs in a subshell so a redirection failure on the special built-in `:` cannot
# terminate the wrapper.
if ! ( : >>"$log/gate.log" ) 2>/dev/null; then
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"bash-gate: log directory %s is not writable, so decisions cannot be logged. Add a nono write grant for it (or fix its permissions). Refusing to run unlogged."}}' "$log"
  exit 2
fi

input=$(cat)                                   # drain the hook JSON once
[ -n "$GATE_DEBUG" ] && printf '%s\n' "$input" >>"$log/stdin.log" 2>/dev/null
out=$(printf '%s' "$input" | "$dir/bash-gate" 2>>"$log/gate.log")
rc=$?

case "$rc:$out" in
  0:defer)                                     # automode non-deny → emit nothing
    ;;
  0:\{*)                                       # allow / ask → JSON, exit 0
    printf '%s' "$out"
    ;;
  2:\{*)                                       # deny → JSON + blocking exit 2
    printf '%s' "$out"
    exit 2
    ;;
  *)                                           # crash / empty / garbage → deny
    printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"bash-gate failed (exit %s)"}}' "$rc"
    exit 2
    ;;
esac
exit 0
