package launch

import (
	"errors"
	"testing"
)

// SLM_OPEN_BROWSER decides whether the browser opens, whatever the system.
func TestShouldOpenBrowserFollowsTheSetting(t *testing.T) {
	t.Setenv("SLM_OPEN_BROWSER", "false")
	if ShouldOpenBrowser() {
		t.Fatal("false keeps the browser closed")
	}
	t.Setenv("SLM_OPEN_BROWSER", "TRUE")
	if !ShouldOpenBrowser() {
		t.Fatal("true opens it, in any case")
	}
}

// The port is taken as Linux and as Windows say it.
func TestPortInUse(t *testing.T) {
	for message, want := range map[string]bool{
		"listen tcp :3000: bind: address already in use":                                                                       true,
		"listen tcp :3000: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.": true,
		"listen tcp :3000: bind: permission denied":                                                                            false,
	} {
		if got := PortInUse(errors.New(message)); got != want {
			t.Errorf("%q: %v", message, got)
		}
	}
}
