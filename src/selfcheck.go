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
  --root DIR  install root to check (default: $BASH_GATE_DIR or
              ~/.bash-gate); logs/ is always checked at its real,
              fixed location ($HOME/.bash-gate/logs, per gate.sh)
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

	attestDir := filepath.Join(root, "attestations")
	storeOK, detail := probeStoreReadOnly(attestDir)
	out = append(out, checkResult{"attestations/ not writable", storeOK, detail})

	writable, detail = probeDirWrite(logs)
	out = append(out, checkResult{"logs/ writable", writable, detail})

	errs := configLoadErrors(root)
	out = append(out, checkResult{"config loads clean", len(errs) == 0, strings.Join(errs, "; ")})

	return out
}

// probeStoreReadOnly verifies the attestation store cannot be tampered with:
// (1) existing records can't be overwritten, (2) new files can't be created,
// (3) the parent can't be renamed away.
func probeStoreReadOnly(attestDir string) (ok bool, detail string) {
	entries, err := os.ReadDir(attestDir)
	if err != nil {
		return false, "cannot read attestations/: " + err.Error()
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".attest" {
			continue
		}
		f, err := os.OpenFile(filepath.Join(attestDir, e.Name()), os.O_WRONLY, 0)
		if err == nil {
			f.Close()
			return false, "attestations/ is writable (overwrite succeeded)"
		}
		break
	}
	tmp := filepath.Join(attestDir, fmt.Sprintf(".probe-create-%d", os.Getpid()))
	f, err := os.Create(tmp)
	if err == nil {
		f.Close()
		os.Remove(tmp)
		return false, "attestations/ is writable (create succeeded)"
	}
	parent := filepath.Dir(attestDir)
	ptmp := filepath.Join(parent, fmt.Sprintf(".probe-parent-%d", os.Getpid()))
	pf, err := os.Create(ptmp)
	if err == nil {
		pf.Close()
		os.Remove(ptmp)
		return false, "attestations/ parent is writable (rename attack possible)"
	}
	return true, "store is read-only"
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
	col := newColorer(w)
	allPass := true
	for _, r := range results {
		var status string
		if r.Pass {
			status = col.success("PASS")
		} else {
			status = col.error("FAIL")
			allPass = false
		}
		line := fmt.Sprintf("%-4s %s", status, r.Name)
		if r.Detail != "" {
			line += "  (" + r.Detail + ")"
		}
		fmt.Fprintln(w, line)
	}
	if allPass {
		fmt.Fprintln(w, "selfcheck: "+col.success("PASS"))
	} else {
		fmt.Fprintln(w, "selfcheck: "+col.error("FAIL"))
	}
}
