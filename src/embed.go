package main

import _ "embed"

// The `install` subcommand materializes the wrapper and the pi extension
// from these embedded sources, so a bare release binary self-installs
// without any source file on disk (docs/CLAUDE_CODE_HOOKS.md, docs/PI.md).

//go:embed shizo.sh
var embeddedWrapper []byte

//go:embed pi-shizo.ts
var embeddedPiExtension []byte
