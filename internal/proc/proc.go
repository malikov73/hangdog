// Package proc discovers the child `<pkg>.test` binaries that `go test` spawns,
// so hangdog can deliver SIGQUIT to the exact process that is stuck rather than
// to `go test` itself (which would kill the run before it flushes the dump).
//
// Discovery is Unix-only: the whole mechanism depends on SIGQUIT, which does not
// exist on Windows. On unsupported platforms every call returns ErrUnsupported.
package proc

import "errors"

// ErrUnsupported is returned by TestBinaries on platforms without SIGQUIT.
var ErrUnsupported = errors.New("hangdog: process discovery is only supported on unix (SIGQUIT-based)")

// TestBinary is a running `go test` child process.
type TestBinary struct {
	PID  int
	Name string // basename, e.g. "pkga.test"
	Cwd  string // working directory == the package's source dir
}
