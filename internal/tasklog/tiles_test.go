package tasklog

import (
	"testing"
)

// A long task shows how far it is before its first whole percent.
func TestTaskPercentExact(t *testing.T) {
	cases := map[[2]int]string{{8, 1000}: "0.80", {0, 1000}: "0.00", {1000, 1000}: "100", {5, 0}: "0", {3, 2}: "100"}
	for c, want := range cases {
		if got := (Task{Current: c[0], Total: c[1]}).PercentExact(); got != want {
			t.Errorf("%d of %d: %s, want %s", c[0], c[1], got, want)
		}
	}
}
