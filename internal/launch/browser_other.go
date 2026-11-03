//go:build !windows && !darwin

package launch

import "errors"

// OpenUrl does nothing on servers: the address is shown in the log.
func OpenUrl(url string) error {
	return errors.New("no desktop browser")
}
