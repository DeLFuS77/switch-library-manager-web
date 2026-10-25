package web

import (
	"github.com/dtrunk90/switch-library-manager-web/db"
	"os"
	"testing"
	"time"
)

// the tests download from servers on this computer
func TestMain(m *testing.M) {
	db.AllowLocalDownloads = true
	// with no connection the titles database is retried; the tests do not wait for it
	db.RetryDelay = time.Millisecond
	// the login delay would make the tests slow; its value is checked on its own
	sleepFor = func(time.Duration) {}
	// the requests of the tests are sent to example.com
	allowedHosts = append(allowedHosts, "example.com")
	code := m.Run()
	removeSharedDemo()
	os.Exit(code)
}
