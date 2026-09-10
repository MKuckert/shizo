package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The `selfcheck` subcommand verifies, from inside the sandbox, that the
// deployed nono profile actually delivers the read/write shape the whole
// fail-closed model rests on (see docs/ARCHITECTURE.md "Install location" /
// "Self-protection"): engine/ and global/rules read-only, logs/ writable.
// nono enforces at the syscall layer, so OS mode bits don't reflect what's
// allowed — every check *attempts* the real operation, never inspects mode
// bits, and is non-destructive (no writes to global/rules; probe files are
// created and removed).

const selfcheckUsage = `bash-gate selfcheck — verify the sandbox enforces the read/write shape
bash-gate's fail-closed model depends on. Must be run inside the sandbox:
nono enforces at the syscall layer, so file mode bits don't reflect what's
actually allowed — each check attempts the real operation.

usage: bash-gate selfcheck [--root DIR] [--json]

Flags:
  --root DIR  install root to check (default: $CLAUDE_BASH_RULES_DIR or
              ~/.claude-bash-rules); logs/ is always checked at its real,
              fixed location ($HOME/.claude-bash-rules/logs, per gate.sh)
  --json      emit machine-readable JSON instead of a table

Exit 0 if every check passes, 1 if any fails, 2 on a usage problem.`

type checkResult struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

func runSelfcheck(args []string, stdout, stderr io.Writer) int {
	root := installRoot()
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, selfcheckUsage)
			return 0
		case a == "--json":
			asJSON = true
		case a == "--root":
			i++
			if i >= len(args) {
				fmt.Fprintf(stderr, "selfcheck: --root requires a value\n\n%s\n", selfcheckUsage)
				return 2
			}
			root = args[i]
		default:
			fmt.Fprintf(stderr, "selfcheck: unknown argument %q\n\n%s\n", a, selfcheckUsage)
			return 2
		}
	}

	results := selfcheckResults(root)

	if asJSON {
		b, _ := json.MarshalIndent(results, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		renderSelfcheck(stdout, results)
	}

	for _, r := range results {
		if !r.Pass {
			return 1
		}
	}
	return 0
}

// selfcheckResults runs every probe against the given install root and
// returns one result per check, in the order listed in the plan's table.
func selfcheckResults(root string) []checkResult {
	globalDir := filepath.Join(root, "global")
	globalRules := filepath.Join(globalDir, "rules")
	engineDir := filepath.Join(root, "engine")
	logs := logDir()

	var out []checkResult

	sandboxed := os.Getenv("NONO_CAP_FILE") != ""
	out = append(out, checkResult{"sandboxed (NONO_CAP_FILE set)", sandboxed, ""})

	if _, err := os.ReadFile(globalRules); err != nil {
		out = append(out, checkResult{"global/rules readable", false, err.Error()})
	} else {
		out = append(out, checkResult{"global/rules readable", true, ""})
	}

	notWritable, detail := probeFileNotWritable(globalRules)
	out = append(out, checkResult{"global/rules not writable", notWritable, detail})

	writable, detail := probeDirWrite(globalDir)
	out = append(out, checkResult{"global/ not writable", !writable, detail})

	writable, detail = probeDirWrite(engineDir)
	out = append(out, checkResult{"engine/ not writable", !writable, detail})

	writable, detail = probeDirWrite(logs)
	out = append(out, checkResult{"logs/ writable", writable, detail})

	errs := configLoadErrors(root)
	out = append(out, checkResult{"config loads clean", len(errs) == 0, strings.Join(errs, "; ")})

	return out
}

// probeFileNotWritable attempts to open path for writing (no write, no
// truncate) and closes it immediately. notWritable=true means the open
// itself failed, i.e. the sandbox blocked it.
func probeFileNotWritable(path string) (notWritable bool, detail string) {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return true, "open for write failed: " + err.Error()
	}
	f.Close()
	return false, "opened for write without error"
}

// probeDirWrite attempts to create and remove a `.probe-<pid>` file in dir.
// writable=true means the create succeeded (and was cleaned up).
func probeDirWrite(dir string) (writable bool, detail string) {
	path := filepath.Join(dir, fmt.Sprintf(".probe-%d", os.Getpid()))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false, "create failed: " + err.Error()
	}
	f.Close()
	os.Remove(path)
	return true, "created and removed " + path
}

// configLoadErrors validates the global tier at root the same way `validate`
// does, returning every error-level finding (warnings don't fail the check).
func configLoadErrors(root string) []string {
	globalRules := filepath.Join(root, "global", "rules")
	data, err := os.ReadFile(globalRules)
	if err != nil {
		return []string{"unreadable: " + err.Error()}
	}
	var msgs []string
	for _, f := range validateFile(globalRules, string(data), false) {
		if f.level == levelError {
			msgs = append(msgs, fmt.Sprintf("line %d: %s", f.line, f.msg))
		}
	}
	return msgs
}

func renderSelfcheck(w io.Writer, results []checkResult) {
	allPass := true
	for _, r := range results {
		status := "PASS"
		if !r.Pass {
			status = "FAIL"
			allPass = false
		}
		line := fmt.Sprintf("%-4s %s", status, r.Name)
		if r.Detail != "" {
			line += "  (" + r.Detail + ")"
		}
		fmt.Fprintln(w, line)
	}
	if allPass {
		fmt.Fprintln(w, "selfcheck: PASS")
	} else {
		fmt.Fprintln(w, "selfcheck: FAIL")
	}
}
