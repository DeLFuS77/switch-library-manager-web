//go:build !windows && !darwin

package web

import "errors"

// openUrl does nothing on servers: the address is shown in the log.
func openUrl(url string) error {
	return errors.New("no desktop browser")
}
