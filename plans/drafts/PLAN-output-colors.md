# Colored output for validate, attest, test, selfcheck

> Backlog: "Some nice colors in the outputs of `validate`, `attest`, `test` and `selfcheck`"

## Problem

All four operator-facing commands print plain text. Key tokens (error/warning,
allow/deny/ask, PASS/FAIL) disappear into a wall of monospace. A few ANSI
colors on the semantic tokens make output scannable at a glance.

## Constraints

- Single external dep policy (`mvdan.cc/sh/v3`). No color library.
- Auto-detect tty; respect `NO_COLOR` env var (no-color.org).
- Stderr error messages stay uncolored (usage/diagnostics, not structured output).

## Design

### New file: `src/color.go`

```go
type colorer struct{ enabled bool }

func newColorer(f *os.File, flag string) colorer
func (c colorer) red(s string) string
func (c colorer) green(s string) string
func (c colorer) yellow(s string) string
```

`newColorer` resolves color mode in priority order:

1. `NO_COLOR` env var set and non-empty -> disabled
2. `f.Stat()` reports `ModeCharDevice` -> enabled, otherwise disabled

Methods return the string unchanged when disabled; wrap with `\033[31m`/`32m`/
`33m` + `\033[0m` when enabled.

### New file: `src/color_test.go`

Table test: enabled vs disabled x each color. Verify ANSI wrapping and
passthrough.

### Tokens to color

| Command      | Token                | Color  |
|--------------|----------------------|--------|
| `validate`   | `error` level        | red    |
| `validate`   | `warning` level      | yellow |
| `validate`   | summary (errors > 0) | red    |
| `validate`   | summary (clean)      | green  |
| `test`       | `deny` decision      | red    |
| `test`       | `allow` decision     | green  |
| `test`       | `ask` decision       | yellow |
| `selfcheck`  | `FAIL`               | red    |
| `selfcheck`  | `PASS`               | green  |
| `attest`     | success line         | green  |

### Changes per file

**`validate.go`** -- `renderFindings` gains a `colorer` param. The `f.level`
token in the `%s:%d: %s: %s` / `%s: %s: %s` format strings gets colored. The
summary line gets colored based on error count.

**`test.go`** -- `renderTest` gains a `colorer` param. Color `outcome` in the
header line and `cl.Decision` in per-command lines (red/green/yellow by value).

**`selfcheck.go`** -- `renderSelfcheck` gains a `colorer` param. Color the
`status` word per check and the final `"selfcheck: PASS/FAIL"` line.

**`attest.go`** -- Color the `"attested %s tier ..."` success line green.

### Threading

Each `run*` function already receives `stdout io.Writer` (which is `os.Stdout`
in production). The `colorer` is created from `os.Stdout` in each `run*` function,
then passed to render helpers. For tests: pass `colorer{enabled: false}` so
existing assertions on plain output stay unchanged.

## Skipped

- Bold/dim/underline styling -- three flat colors covers every semantic token.
- Coloring stderr error messages -- unstructured diagnostics, not worth it.
