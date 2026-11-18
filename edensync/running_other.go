//go:build !windows && !linux && !darwin

package edensync

// processNames cannot be read on this system.
func processNames() ([]string, error) {
	return nil, ErrCannotTell
}
