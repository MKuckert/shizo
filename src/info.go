package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The `info` subcommand prints the paths shizo resolves (§0), so install
// scripts and troubleshooting stop hardcoding ~/.shizo/... . Operator-only:
// never reads stdin, never logs, never emits a hook decision.

const infoUsage = `usage: shizo info [--cwd DIR] [--KEY]

Print the paths shizo resolves. With no flags, one "key<TAB>value" line per
entry. With --KEY, that value only, bare (e.g. shizo info --global-rules-path).

Flags:
  --cwd DIR   working directory for the project rules path (default: current dir)

Keys:
  install-root, global-rules-path, project-rules-path, attestation-dir,
  log-dir, engine-dir, hook-command, version`

func runInfo(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, infoUsage)
		return 0
	}
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, infoUsage) }
	cwd := fs.String("cwd", "", "working directory for the project rules path")
	keys := []string{
		"install-root", "global-rules-path", "project-rules-path",
		"attestation-dir", "log-dir", "engine-dir", "hook-command", "version",
	}
	want := make(map[string]*bool, len(keys))
	for _, k := range keys {
		want[k] = fs.Bool(k, false, "print "+k+" only")
	}
	if err := fs.Parse(args); err != nil {
		return 2 // flag already printed the error + usage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "info: unexpected argument %q\n\n%s\n", fs.Arg(0), infoUsage)
		return 2
	}

	if *cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			*cwd = wd
		}
	}
	root := installRoot()
	vals := map[string]string{
		"install-root":       root,
		"global-rules-path":  globalRulesPath(),
		"project-rules-path": projectRulesPathFor(*cwd),
		"attestation-dir":    attestDirIn(root),
		"log-dir":            logDir(),
		"engine-dir":         engineDirIn(root),
		"hook-command":       filepath.Join(engineDirIn(root), "shizo.sh"),
		"version":            version,
	}

	for _, k := range keys {
		if *want[k] {
			fmt.Fprintln(stdout, vals[k])
			return 0
		}
	}
	for _, k := range keys {
		fmt.Fprintf(stdout, "%s\t%s\n", k, vals[k])
	}
	return 0
}
