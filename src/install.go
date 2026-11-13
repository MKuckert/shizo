package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The `install claude-hook` subcommand registers the shizo.sh wrapper as a
// PreToolUse/Bash hook in a Claude Code settings file (see
// docs/CLAUDE_CODE_HOOKS.md). Operator-only: it never touches the hook
// pipeline. It merges into whatever is already in the file and refuses to
// write over JSON it cannot parse.

const installUsage = `shizo install — wire shizo into a coding agent. Operator-only;
never touches the hook pipeline.

usage: shizo install claude-hook [--project | --local | --global]
       shizo install pi-extension [--project | --global]

claude-hook registers the shizo.sh wrapper as a PreToolUse/Bash hook in a
Claude Code settings file, merging into whatever is already there; it writes
the wrapper next to the binary from the embedded source if missing.
pi-extension copies the embedded pi extension into pi's extension directory
(docs/PI.md); it never overwrites a different file.

Flags:
  --project   claude-hook: .claude/settings.json in the cwd (default)
              pi-extension: .pi/extensions/shizo.ts in the cwd (default)
  --local     claude-hook only: .claude/settings.local.json in the cwd
  --global    claude-hook: $HOME/.claude/settings.json
              pi-extension: $HOME/.pi/agent/extensions/shizo.ts

Exit 0 on success (or if already installed), 1 on an I/O or parse
problem, 2 on a usage problem.`

// installExe resolves this executable; var so tests can override.
var installExe = os.Executable

func runInstall(args []string, stdout, stderr io.Writer) int {
	usageErr := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "install: "+format+"\n\n%s\n", append(a, installUsage)...)
		return 2
	}

	target, targetFlag := "", ""
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-h", "--help":
			fmt.Fprintln(stdout, installUsage)
			return 0
		case "--project", "--local", "--global":
			if targetFlag != "" && targetFlag != a {
				return usageErr("%s and %s are mutually exclusive", targetFlag, a)
			}
			targetFlag = a
		default:
			if strings.HasPrefix(a, "-") {
				return usageErr("unknown argument %q", a)
			}
			positional = append(positional, a)
		}
	}
	if len(positional) != 1 || (positional[0] != "claude-hook" && positional[0] != "pi-extension") {
		return usageErr("expected exactly one target: claude-hook or pi-extension")
	}
	if positional[0] == "pi-extension" {
		if targetFlag == "--local" {
			return usageErr("pi-extension has no --local scope")
		}
		return installPiExtension(targetFlag == "--global", stdout, stderr)
	}

	switch targetFlag {
	case "--global":
		target = filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
	case "--local":
		target = filepath.Join(".claude", "settings.local.json")
	default:
		target = filepath.Join(".claude", "settings.json")
	}

	cmd, err := ensureWrapper()
	if err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}

	settings, err := readSettings(target)
	if err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}

	changed, err := addBashHook(settings, cmd)
	if err != nil {
		fmt.Fprintf(stderr, "install: %s %v — fix or move it first\n", target, err)
		return 1
	}
	if !changed {
		fmt.Fprintf(stdout, "already installed -> %s\n", target)
		return 0
	}

	if err := writeSettings(target, settings); err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed hook -> %s\n", target)
	return 0
}

// ensureWrapper resolves the shizo.sh next to the running binary, writing it
// from the embedded source if missing. We register the wrapper, never the
// binary (docs/CLAUDE_CODE_HOOKS.md). A present file that differs from the
// embedded source is refused, never overwritten: an operator may have patched
// it.
func ensureWrapper() (string, error) {
	exe, err := installExe()
	if err != nil {
		return "", fmt.Errorf("cannot locate the shizo binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	p := filepath.Join(filepath.Dir(exe), "shizo.sh")
	cur, err := os.ReadFile(p)
	switch {
	case os.IsNotExist(err):
		if err := os.WriteFile(p, embeddedWrapper, 0o644); err != nil {
			return "", fmt.Errorf("cannot write wrapper %s: %w", p, err)
		}
		// WriteFile's mode is umask-masked; the hook needs the exec bit.
		if err := os.Chmod(p, 0o755); err != nil {
			return "", fmt.Errorf("cannot make wrapper %s executable: %w", p, err)
		}
	case err != nil:
		return "", fmt.Errorf("cannot read wrapper %s: %w", p, err)
	default:
		if !bytes.Equal(cur, embeddedWrapper) {
			return "", fmt.Errorf("%s differs from the wrapper built into this binary — review and remove it first", p)
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o100 == 0 {
			// Our own content, exec bit lost: restore it rather than register
			// a wrapper the hook cannot spawn.
			if err := os.Chmod(p, 0o755); err != nil {
				return "", fmt.Errorf("cannot make wrapper %s executable: %w", p, err)
			}
		}
	}
	return p, nil
}

// installPiExtension copies the embedded pi extension into pi's extension
// directory (docs/PI.md). Idempotent: an identical regular file is left
// alone; a different existing file is refused, never overwritten. Stale
// symlinks from older installs are replaced by the regular copy so they
// cannot dangle once engine/shizo-pi.ts is gone.
func installPiExtension(global bool, stdout, stderr io.Writer) int {
	target := filepath.Join(".pi", "extensions", "shizo.ts")
	if global {
		target = filepath.Join(os.Getenv("HOME"), ".pi", "agent", "extensions", "shizo.ts")
	}
	cur, err := os.ReadFile(target)
	if !os.IsNotExist(err) {
		if err != nil {
			fmt.Fprintf(stderr, "install: %s already exists — fix or move it first\n", target)
			return 1
		}
		if !bytes.Equal(cur, embeddedPiExtension) {
			fmt.Fprintf(stderr, "install: %s already exists with different content — fix or move it first\n", target)
			return 1
		}
		fi, lerr := os.Lstat(target)
		if lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
			fmt.Fprintf(stdout, "already installed -> %s\n", target)
			return 0
		}
		// Identical content behind a symlink: migrate to a regular file below.
		if err := os.Remove(target); err != nil {
			fmt.Fprintf(stderr, "install: %v\n", err)
			return 1
		}
	} else if _, lerr := os.Lstat(target); lerr == nil {
		// A broken symlink: ReadFile saw IsNotExist, but WriteFile would not
		// replace it — remove it first.
		if err := os.Remove(target); err != nil {
			fmt.Fprintf(stderr, "install: %v\n", err)
			return 1
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}
	if err := os.WriteFile(target, embeddedPiExtension, 0o644); err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed pi extension -> %s\n", target)
	return 0
}

func readSettings(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v) — fix or move it first", path, err)
	}
	return m, nil
}

// addBashHook merges the command into hooks.PreToolUse's "Bash" matcher,
// creating any missing level. Returns false if the wrapper is already
// registered there (idempotent re-run). A key that exists with an unexpected
// shape is an error, never an overwrite: we refuse rather than silently drop
// a hand-edited value.
func addBashHook(settings map[string]any, cmd string) (bool, error) {
	hooks, err := objectAt(settings, "hooks")
	if err != nil {
		return false, err
	}
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	pre, err := arrayAt(hooks, "PreToolUse")
	if err != nil {
		return false, err
	}

	var entry map[string]any
	for _, e := range pre {
		m, ok := e.(map[string]any)
		if !ok {
			return false, fmt.Errorf("has a non-object entry in hooks.PreToolUse")
		}
		if m["matcher"] == "Bash" {
			entry = m
			break
		}
	}
	if entry == nil {
		entry = map[string]any{"matcher": "Bash"}
		pre = append(pre, entry)
	}

	list, err := arrayAt(entry, "hooks")
	if err != nil {
		return false, err
	}
	// A wrapper entry already present is updated in place rather than
	// duplicated: a stale path from an earlier install root would otherwise
	// leave a dangling hook behind a false "already installed".
	for _, h := range list {
		m, ok := h.(map[string]any)
		if !ok {
			return false, fmt.Errorf("has a non-object entry in the Bash matcher's hooks")
		}
		s, _ := m["command"].(string)
		if !strings.HasSuffix(s, "shizo.sh") {
			continue
		}
		if s == cmd {
			return false, nil
		}
		m["command"] = cmd
		m["type"] = "command"
		hooks["PreToolUse"] = pre
		return true, nil
	}
	entry["hooks"] = append(list, map[string]any{"type": "command", "command": cmd})
	hooks["PreToolUse"] = pre
	return true, nil
}

// objectAt / arrayAt read a key that must be absent, null, or the expected
// JSON shape. Anything else is reported so the caller can refuse the write.
func objectAt(m map[string]any, key string) (map[string]any, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, nil
	}
	o, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("has a non-object %q key", key)
	}
	return o, nil
}

func arrayAt(m map[string]any, key string) ([]any, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, nil
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("has a non-array %q key", key)
	}
	return a, nil
}

func writeSettings(path string, settings map[string]any) error {
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file 0600; widen to the usual settings mode before
	// the rename, which lands in the same directory and preserves it.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
