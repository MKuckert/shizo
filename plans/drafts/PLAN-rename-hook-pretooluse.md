## Plan: Rename `shizo claude-hook` to `shizo hook pretooluse`

1. In `src/main.go` `dispatch`, replace the `case "claude-hook"` with `case "hook"` that dispatches `args[1] == "pretooluse"` to `runClaudeHook`, and errors with `hookUsage` + exit 2 for a missing or unknown event (never reading stdin).
2. Rename `claudeHookUsage` → `hookUsage` and `runClaudeHook` → `runHookPreToolUse`, rewriting the usage text to `shizo hook pretooluse` and listing `pretooluse` as the only supported event.
3. In `runHelp`, accept `help hook` (and `help hook pretooluse`) → `hookUsage`, dropping the `claude-hook` case.
4. Update `rootUsage`: the command line becomes `hook <event>  hook mode: read hook JSON on stdin, emit an allow/ask/deny decision` and the prose sentence names `"hook pretooluse"` as the entrypoint.
5. Change `src/shizo.sh:22` to invoke `"$dir/shizo" hook pretooluse`.
6. Update `docs/CLAUDE_CODE_HOOKS.md` wrapper section to say it runs `shizo hook pretooluse`, integrated into the surrounding prose.
7. In `src/main_test.go`, update the help table case to `{[]string{"hook"}, "usage: shizo hook pretooluse"}`, retarget the dispatch test to `[]string{"hook", "pretooluse"}`, and add cases asserting bare `hook` and `hook bogus` exit 2 without consuming stdin.
8. Remove the backlog line in `BACKLOG.md` and run `make test`.

Skipped: `claude-hook` back-compat alias, add when a binary-only upgrade path exists that doesn't also replace `shizo.sh`.
