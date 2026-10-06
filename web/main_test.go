package web

import (
	"os"
	"testing"
	"github.com/dtrunk90/switch-library-manager-web/db"
)

// the tests download from servers on this computer
func TestMain(m *testing.M) {
	db.AllowLocalDownloads = true
	os.Exit(m.Run())
}
