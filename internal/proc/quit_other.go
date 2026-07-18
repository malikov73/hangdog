//go:build !unix

package proc

import "os/exec"

// Supported is false on platforms without SIGQUIT (e.g. Windows): hangdog cannot
// operate there and bails out early rather than stripping go test's own timeout.
var Supported = false

// SetProcessGroup is a no-op where POSIX process groups are unavailable.
func SetProcessGroup(*exec.Cmd) {}

// Quit is unsupported without SIGQUIT.
func Quit(int) error { return ErrUnsupported }

// KillGroup is unsupported without POSIX process groups.
func KillGroup(int) error { return ErrUnsupported }
