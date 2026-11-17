//go:build !linux && !darwin

package main

import "os"

// ioctlIsTerminal is a no-op on platforms without the TIOCGWINSZ syscall
// (shizo is Unix-only in practice); docs output stays raw there.
func ioctlIsTerminal(f *os.File) bool { return false }
