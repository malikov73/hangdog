//go:build !unix

package proc

// Quit is unsupported without SIGQUIT (e.g. Windows).
func Quit(pid int) error {
	return ErrUnsupported
}

// Kill is unsupported on platforms without the unix signal model.
func Kill(pid int) error {
	return ErrUnsupported
}
