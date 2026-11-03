//go:build windows

package priority

import (
	"testing"

	"golang.org/x/sys/windows"
)

// Low priority puts the app below the normal class, and back.
func TestSetLow(t *testing.T) {
	class := func() uint32 {
		value, _ := windows.GetPriorityClass(windows.CurrentProcess())
		return value
	}
	defer SetLow(false)
	if err := SetLow(true); err != nil || class() != windows.BELOW_NORMAL_PRIORITY_CLASS {
		t.Fatalf("low: %#x %v", class(), err)
	}
	if err := SetLow(false); err != nil || class() != windows.NORMAL_PRIORITY_CLASS {
		t.Fatalf("back: %#x %v", class(), err)
	}
}
