package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// The `edit` subcommand is operator-only, like attest/validate/test/selfcheck:
// it never touches the hook pipeline. It opens a rule tier in $EDITOR and, on
// a clean editor exit, attests the result through attestPath — the same code
// path as `attest`. See docs/RULES.md "Attestation".

const editUsage = `shizo edit — open a rule tier in $EDITOR and attest it afterwards.

usage: shizo edit [--global]

Without --global, opens the project tier <cwd>/.shizo; with --global, the
resolved global tier ($SHIZO_DIR/global/rules). The editor is $EDITOR, falling
back to $VISUAL; if neither is set this exits with an error — shizo never
guesses an editor. A non-zero editor exit (the user cancelled) skips
attestation and exits 0, so the tier stays unattested. A clean editor exit
attests the file directly, unchanged or not.

Run this outside the sandbox: the attestation record lands in
$SHIZO_DIR/attestations/, outside the sandbox writable set. Never touches the
hook pipeline.`

func runEdit(args []string, stdout, stderr io.Writer) int {
	global := false
	for _, a := range args {
		switch {
		case a == "--global":
			global = true
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, editUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "edit: unknown flag %q\n\n%s\n", a, editUsage)
			return 2
		default:
			fmt.Fprintf(stderr, "edit: too many arguments\n\n%s\n", editUsage)
			return 2
		}
	}

	// The attestation record lands in $SHIZO_DIR/attestations/, outside the
	// sandbox's writable set — bail before opening the editor.
	if os.Getenv("NONO_CAP_FILE") != "" {
		fmt.Fprintln(stderr, "edit: this command must be run outside the sandbox")
		return 1
	}

	path := globalRulesPath()
	if !global {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "edit: %s\n", err)
			return 1
		}
		path = projectRulesPathFor(cwd)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}

	// $EDITOR may carry its own flags (e.g. "vi -e"); split on whitespace
	// and exec directly — no shell, so the path needs no quoting. The
	// editor spec itself is not shell-parsed: a quoted argument in $EDITOR
	// is split on its spaces. Fields also collapses an unset or
	// whitespace-only $EDITOR to nothing.
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		fmt.Fprintln(stderr, "edit: neither $EDITOR nor $VISUAL is set")
		return 1
	}
	cmd := exec.Command(fields[0], append(fields[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The user cancelled: skipping attestation is the safe
			// direction — an unattested tier keeps denying everything.
			fmt.Fprintln(stdout, "editor exited non-zero — attestation skipped")
			return 0
		}
		fmt.Fprintf(stderr, "edit: %s\n", err)
		return 1
	}

	tier, recordPath, err := attestPath(path, global)
	if err != nil {
		fmt.Fprintf(stderr, "edit: %s\n", err)
		return 1
	}
	printAttested(stdout, tier, path, recordPath)
	return 0
}
