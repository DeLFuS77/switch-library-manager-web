//go:build !windows

package diskfree

import (
	"errors"
	"syscall"
)

// Full tells an error of writing to a volume that has no room left.
func Full(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// fullError is the error of a full volume, for the tests.
var fullError error = syscall.ENOSPC
