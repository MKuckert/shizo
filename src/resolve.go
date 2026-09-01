package main

import (
	"os"
	"path/filepath"
	"strings"
)

// §3 — the nine safe builtins. These short-circuit to allow without path
// resolution or a rule. Everything else (export, set, shopt, alias, kill,
// history, ...) is denied.
var safeBuiltins = map[string]bool{
	"cd": true, "echo": true, "printf": true, "pwd": true, "true": true,
	"false": true, "test": true, "[": true, ":": true,
}

// §2 step 9 — execution-hiding names denied regardless of any allow rule.
var uncondDeny = map[string]bool{
	"eval": true, "exec": true, "source": true, ".": true, "command": true,
	"builtin": true, "enable": true, "trap": true, "xargs": true, "env": true,
	"sudo": true, "doas": true, "su": true, "nohup": true, "setsid": true,
}

type resolveResult struct {
	builtin bool
	path    string // resolved executable path (empty when builtin or on error)
	err     string // non-empty -> deny with this reason
}

// resolveCommand implements §2 step 10: safe-builtin short-circuit, then
// inherited-PATH lookup (slash names relative to cwd), then a trusted-dir check.
// No realpath, no symlink following.
func resolveCommand(name string, trusted []string, cwd, pathEnv string) resolveResult {
	if safeBuiltins[name] {
		return resolveResult{builtin: true}
	}
	if name == "" {
		return resolveResult{err: "empty command name"}
	}

	if strings.ContainsRune(name, '/') {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		if !isExecutable(path) {
			return resolveResult{err: "command not found: " + name}
		}
		if !dirTrusted(filepath.Dir(path), trusted) {
			return resolveResult{err: "command resolves outside trusted directories: " + path}
		}
		return resolveResult{path: path}
	}

	// Bare name: search the inherited PATH, first match wins (as bash does).
	for _, dir := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		if dir == "" {
			continue // skip empty entries; do not treat as cwd
		}
		cand := filepath.Join(dir, name)
		if isExecutable(cand) {
			if !dirTrusted(filepath.Dir(cand), trusted) {
				return resolveResult{err: "command resolves outside trusted directories: " + cand}
			}
			return resolveResult{path: cand}
		}
	}
	return resolveResult{err: "command not found in PATH: " + name}
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func dirTrusted(dir string, trusted []string) bool {
	dir = filepath.Clean(dir)
	for _, t := range trusted {
		if filepath.Clean(t) == dir {
			return true
		}
	}
	return false
}
