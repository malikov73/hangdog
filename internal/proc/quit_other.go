//go:build !unix

package proc

// Quit is unsupported without SIGQUIT (e.g. Windows).
func Quit(pid int) error {
	return ErrUnsupported
}
