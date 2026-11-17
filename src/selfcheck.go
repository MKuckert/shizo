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
// "Self-protection"): engine/ and global/rules read-only, logs/ writable,
// and the workspace's execution-controlling paths (.git/config, .git/hooks/,
// .claude/settings.json, the project .shizo tier) not writable. nono
// enforces at the syscall layer, so OS mode bits don't reflect what's
// allowed — every check *attempts* the real operation, never inspects mode
// bits, and is non-destructive (no writes to global/rules; probe files are
// created and removed). Workspace probes warn instead of fail because
// layouts vary (no .claude, worktrees, ...); --strict promotes them.

const selfcheckUsage = `shizo selfcheck — verify the sandbox enforces the read/write shape
shizo's fail-closed model depends on. Must be run inside the sandbox:
nono enforces at the syscall layer, so file mode bits don't reflect what's
actually allowed — each check attempts the real operation.

usage: shizo selfcheck [--root DIR] [--workspace DIR] [--strict] [--json]

Flags:
  --root DIR       install root to check (default: $SHIZO_DIR or
                   ~/.shizo); logs/ is always checked at its real,
                   fixed location ($HOME/.shizo/logs, per shizo.sh)
  --workspace DIR  workspace whose execution-controlling paths are
                   probed (default: cwd)
  --strict         include workspace warnings in the exit code
  --json           emit machine-readable JSON instead of a table

The workspace probes (.git/config, .git/hooks/, .claude/settings.json,
.shizo) warn instead of fail because layouts vary; --strict promotes them.

Exit 0 if every check passes, 1 if any fails (or a warning does under
--strict), 2 on a usage problem.`

type checkResult struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
	// Warn marks a check whose failure is advisory (workspace probes):
	// it affects the exit code only under --strict.
	Warn bool `json:"warn,omitempty"`
	// NA marks a check whose target does not exist; there is nothing to
	// probe, so the check passes by absence.
	NA bool `json:"na,omitempty"`
}

func runSelfcheck(args []string, stdout, stderr io.Writer) int {
	root := installRoot()
	workspace, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "selfcheck: cannot determine cwd: %v\n", err)
		return 2
	}
	asJSON, strict := false, false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, selfcheckUsage)
			return 0
		case a == "--json":
			asJSON = true
		case a == "--strict":
			strict = true
		case a == "--root", a == "--workspace":
			i++
			if i >= len(args) {
				fmt.Fprintf(stderr, "selfcheck: %s requires a value\n\n%s\n", a, selfcheckUsage)
				return 2
			}
			if a == "--root" {
				root = args[i]
			} else {
				workspace = args[i]
			}
		default:
			fmt.Fprintf(stderr, "selfcheck: unknown argument %q\n\n%s\n", a, selfcheckUsage)
			return 2
		}
	}

	results := append(selfcheckResults(root), workspaceCheckResults(workspace)...)

	if asJSON {
		b, _ := json.MarshalIndent(results, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		renderSelfcheck(stdout, results, strict)
	}

	return selfcheckExitCode(results, strict)
}

// selfcheckExitCode is the selfcheck exit rule: any failed non-warn check
// fails; a failed warn check fails only under --strict.
func selfcheckExitCode(results []checkResult, strict bool) int {
	for _, r := range results {
		if !r.Pass && (!r.Warn || strict) {
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
	engineDir := engineDirIn(root)
	logs := logDir()

	var out []checkResult

	notRoot := euidFn() != 0
	detail := ""
	if !notRoot {
		detail = "euid=0"
	}
	out = append(out, checkResult{Name: "not running as root", Pass: notRoot, Detail: detail})

	sandboxed := os.Getenv("NONO_CAP_FILE") != ""
	out = append(out, checkResult{Name: "sandboxed (NONO_CAP_FILE set)", Pass: sandboxed})

	if _, err := os.ReadFile(globalRules); err != nil {
		out = append(out, checkResult{Name: "global/rules readable", Pass: false, Detail: err.Error()})
	} else {
		out = append(out, checkResult{Name: "global/rules readable", Pass: true})
	}

	notWritable, detail := probeFileNotWritable(globalRules)
	out = append(out, checkResult{Name: "global/rules not writable", Pass: notWritable, Detail: detail})

	writable, detail := probeDirWrite(globalDir)
	out = append(out, checkResult{Name: "global/ not writable", Pass: !writable, Detail: detail})

	writable, detail = probeDirWrite(engineDir)
	out = append(out, checkResult{Name: "engine/ not writable", Pass: !writable, Detail: detail})

	attestDir := attestDirIn(root)
	storeOK, detail := probeStoreReadOnly(attestDir)
	out = append(out, checkResult{Name: "attestations/ not writable", Pass: storeOK, Detail: detail})

	writable, detail = probeDirWrite(logs)
	out = append(out, checkResult{Name: "logs/ writable", Pass: writable, Detail: detail})

	errs := configLoadErrors(root)
	out = append(out, checkResult{Name: "config loads clean", Pass: len(errs) == 0, Detail: strings.Join(errs, "; ")})

	return out
}

// workspaceProbes are the execution-controlling paths of the workspace
// (see docs/ARCHITECTURE.md "Self-protection"): each one decides what later
// executes, so the sandbox profile must keep them read-only.
type workspaceProbe struct {
	name string // check name, e.g. ".git/config not writable"
	path string // relative to the workspace root
	dir  bool   // probe as a directory (create-and-remove) rather than open-for-write
	note string // advisory detail appended when the path is writable
}

var workspaceProbes = []workspaceProbe{
	{name: ".git/config not writable", path: filepath.Join(".git", "config")},
	{name: ".git/hooks/ not writable", path: filepath.Join(".git", "hooks"), dir: true},
	{name: ".claude/settings.json not writable", path: filepath.Join(".claude", "settings.json")},
	{name: ".shizo not writable", path: ".shizo",
		note: "informational: a tampered project tier flips its attestation hash, which denies all commands"},
}

// workspaceCheckResults probes each execution-controlling path that exists in
// workspace. Results are warn-level: a writable path is reported but does not
// fail the run (layouts vary), and a missing path is N/A, not FAIL. The
// probes reuse the same non-destructive operations as the install checks.
func workspaceCheckResults(workspace string) []checkResult {
	var out []checkResult
	for _, p := range workspaceProbes {
		full := filepath.Join(workspace, p.path)
		if _, err := os.Stat(full); err != nil {
			out = append(out, checkResult{Name: p.name, Pass: true, Warn: true, NA: true, Detail: "path does not exist"})
			continue
		}
		var ok bool
		var detail string
		if p.dir {
			writable, d := probeDirWrite(full)
			ok, detail = !writable, d
		} else {
			notWritable, d := probeFileNotWritable(full)
			ok, detail = notWritable, d
		}
		if !ok && p.note != "" {
			detail += " (" + p.note + ")"
		}
		out = append(out, checkResult{Name: p.name, Pass: ok, Warn: true, Detail: detail})
	}
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

func renderSelfcheck(w io.Writer, results []checkResult, strict bool) {
	col := newColorer(w)
	warnings := 0
	for _, r := range results {
		var status string
		switch {
		case r.NA:
			status = "N/A"
		case r.Pass:
			status = col.success("PASS")
		case r.Warn && !strict:
			warnings++
			status = col.warning("WARN")
		default:
			status = col.error("FAIL")
		}
		line := fmt.Sprintf("%-4s %s", status, r.Name)
		if r.Detail != "" {
			line += "  (" + r.Detail + ")"
		}
		fmt.Fprintln(w, line)
	}
	if selfcheckExitCode(results, strict) == 0 {
		summary := "selfcheck: " + col.success("PASS")
		if warnings > 0 {
			summary += fmt.Sprintf(" (%d warning%s)", warnings, plural(warnings))
		}
		fmt.Fprintln(w, summary)
	} else {
		fmt.Fprintln(w, "selfcheck: "+col.error("FAIL"))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
