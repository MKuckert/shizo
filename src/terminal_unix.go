//go:build linux || darwin

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// ioctlIsTerminal asks the kernel whether f is a terminal via TIOCGWINSZ.
// A character-device stat alone is not enough: /dev/null passes it but has
// no tty, and pagers launched for it would block or misbehave.
func ioctlIsTerminal(f *os.File) bool {
	var ws struct{ Row, Col, Xpixel, Ypixel uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	return errno == 0
}
