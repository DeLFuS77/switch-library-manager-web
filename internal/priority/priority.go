// Package priority runs the app below the normal priority of the processor, so the other
// programs of the server stay responsive while it compresses, checks or converts. Each system
// does it its own way, as NXPress does.
package priority

// SetLow runs the app below the normal priority of the processor; false gives the normal one
// back.
func SetLow(low bool) error {
	return setLow(low)
}
