package db

import (
	"os"
	"testing"
)

// the tests download from servers on this computer
func TestMain(m *testing.M) {
	AllowLocalDownloads = true
	os.Exit(m.Run())
}
