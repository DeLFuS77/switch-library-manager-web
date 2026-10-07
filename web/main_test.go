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
	// the login delay would make the tests slow; its value is checked on its own
	sleepFor = func(time.Duration) {}
	os.Exit(m.Run())
}
