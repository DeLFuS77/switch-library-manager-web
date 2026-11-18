//go:build !windows && !linux && !darwin

package edensync

// processNames cannot be read on this system.
func processNames() ([]string, error) {
	return nil, ErrCannotTell
}

// processAlive cannot tell on this system: a lock is stale only by its time.
func processAlive(pid int) bool {
	return true
}
