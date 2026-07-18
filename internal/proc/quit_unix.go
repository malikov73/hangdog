//go:build unix

package proc

import (
	"os/exec"
	"syscall"
)

// Supported reports whether hangdog's SIGQUIT-based mechanism works on this
// platform. It is a var, not a const, so callers branch on it at run time without
// the compiler eliminating the guard as dead code.
var Supported = true

// SetProcessGroup makes c start in its own process group, so its whole test
// process tree (go test plus every .test binary) can be terminated at once via
// KillGroup. Without it hangdog can signal individual binaries but has no
// last-resort way to guarantee the run ends.
func SetProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// Quit delivers SIGQUIT to pid, causing the Go runtime to print a full goroutine
// dump (with GOTRACEBACK=all) and exit.
func Quit(pid int) error {
	return syscall.Kill(pid, syscall.SIGQUIT)
}

// KillGroup SIGKILLs the entire process group led by pgid — the child's pid when
// it was started via SetProcessGroup. This is hangdog's guarantee that a wedged
// run always terminates, even when the specific stuck binary cannot be located or
// ignores SIGQUIT.
func KillGroup(pgid int) error {
	return syscall.Kill(-pgid, syscall.SIGKILL)
}
