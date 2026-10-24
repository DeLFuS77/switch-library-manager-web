package web

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// closedAddress is an address of this computer where nothing listens.
func closedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

// countingAddress is an address of this computer that counts the connections made to it.
func countingAddress(t *testing.T, connections *atomic.Int64) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			connection.Close()
		}
	}()
	return listener.Addr().String()
}

// With no connection the saved copy of the titles database is used, and nothing else is
// downloaded: the versions and the names in other languages wait for the next time.
func TestTitlesWithNoConnectionUseTheSavedCopy(t *testing.T) {
	web := newTestWeb(t)
	os.WriteFile(filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME), []byte(`{"0100000000010000": {"id": "0100000000010000", "name": "Game"}}`), 0644)
	os.WriteFile(filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME), []byte(`{"0100000000010000": {"65536": "2020-01-01"}}`), 0644)
	fallbacks := settings.FALLBACK_TITLES_JSON_URLS
	t.Cleanup(func() { settings.FALLBACK_TITLES_JSON_URLS = fallbacks })
	settings.FALLBACK_TITLES_JSON_URLS = []string{fmt.Sprintf("https://%s/titles.json", closedAddress(t))}
	var others atomic.Int64
	other := countingAddress(t, &others)
	versionFallbacks := settings.FALLBACK_VERSIONS_JSON_URLS
	t.Cleanup(func() { settings.FALLBACK_VERSIONS_JSON_URLS = versionFallbacks })
	settings.FALLBACK_VERSIONS_JSON_URLS = []string{fmt.Sprintf("https://%s/versions.json", other)}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.TitlesJsonUrl = fmt.Sprintf("https://%s/titles.json", closedAddress(t))
		s.VersionsJsonUrl = fmt.Sprintf("https://%s/versions.json", other)
		s.LocalizedTitlesJsonUrl = fmt.Sprintf("https://%s/titles.%%s.json", other)
		s.Language = "es"
	})

	// the first start: the database is made from the saved copy
	switchDB, err := web.buildSwitchDb(nil)
	if !errors.Is(err, db.ErrNoConnection) || switchDB == nil || switchDB.TitlesMap["0100000000010"] == nil {
		t.Fatalf("the saved copy with no connection: %v %+v", err, switchDB)
	}
	// with the titles the app has, they are kept as they are
	if kept, err := web.buildSwitchDb(switchDB); kept != nil || !errors.Is(err, db.ErrNoConnection) {
		t.Fatalf("the titles of the app are kept: %v %v", kept, err)
	}
	if others.Load() != 0 {
		t.Fatalf("nothing else is downloaded with no connection: %v connections", others.Load())
	}
}
