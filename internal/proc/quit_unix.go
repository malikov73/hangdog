//go:build unix

package proc

import "syscall"

// Quit delivers SIGQUIT to pid, causing the Go runtime to print a full goroutine
// dump (with GOTRACEBACK=all) and exit.
func Quit(pid int) error {
	return syscall.Kill(pid, syscall.SIGQUIT)
}
