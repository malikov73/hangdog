//go:build unix

package proc

import "syscall"

// Quit delivers SIGQUIT to pid, causing the Go runtime to print a full goroutine
// dump (with GOTRACEBACK=all) and exit.
func Quit(pid int) error {
	return syscall.Kill(pid, syscall.SIGQUIT)
}

// Kill force-terminates pid with SIGKILL, used to escalate when SIGQUIT is
// ignored so that hangdog itself never blocks waiting on a wedged binary.
func Kill(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
