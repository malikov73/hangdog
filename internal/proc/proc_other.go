//go:build !unix

package proc

// TestBinaries is unsupported without SIGQUIT (e.g. Windows).
func TestBinaries(parent int) ([]TestBinary, error) {
	return nil, ErrUnsupported
}
