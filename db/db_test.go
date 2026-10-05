package db

import (
	"errors"
	"fmt"

	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTitleIDPrefixValidationAndGrouping(t *testing.T) {
	tests := []struct {
		id      string
		prefix  string
		wantErr bool
	}{
		{id: "0100000000010000", prefix: "0100000000010"},
		{id: "0100000000010800", prefix: "0100000000010"},
		{id: "0100000000011001", prefix: "0100000000010"},
		{id: "0100000000012101", prefix: "0100000000011"},
		{id: "01006F8002326000", prefix: "01006f8002326"},
		{id: "01006F8002327001", prefix: "01006f8002326"},
		{id: "01006F8002328000", prefix: "01006f8002328"},
		{id: "short", wantErr: true},
		{id: "010000000001000g", wantErr: true},
		{id: "0100000000010001", wantErr: true},
	}
	for _, tt := range tests {
		got, err := titleIDPrefix(tt.id)
		if tt.wantErr {
			if err == nil {
				t.Errorf("titleIDPrefix(%q) accepted invalid ID", tt.id)
			}
		} else if err != nil || got != tt.prefix {
			t.Errorf("titleIDPrefix(%q) = %q, %v; want %q", tt.id, got, err, tt.prefix)
		}
	}
}

func TestCreateSwitchTitleDBKeepsNeighbouringBaseGamesApart(t *testing.T) {
	titles := `{
		"01006F8002326000": {"id": "01006F8002326000", "name": "Game A"},
		"01006F8002327001": {"id": "01006F8002327001", "name": "Game A DLC"},
		"01006F8002328000": {"id": "01006F8002328000", "name": "Game B"},
		"01006F8002326800": {"id": "01006F8002326800"},
		"not-a-title-id":   {"id": "not-a-title-id"}
	}`
	versions := `{"01006f8002326000": {"65536": "2020-01-01"}}`

	result, err := CreateSwitchTitleDB(strings.NewReader(titles), strings.NewReader(versions))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TitlesMap) != 2 {
		t.Fatalf("expected 2 title groups, got %d: %v", len(result.TitlesMap), result.TitlesMap)
	}
	gameA, gameB := result.TitlesMap["01006f8002326"], result.TitlesMap["01006f8002328"]
	if gameA == nil || gameA.Attributes.Name != "Game A" || gameB == nil || gameB.Attributes.Name != "Game B" {
		t.Fatalf("base games grouped incorrectly: A=%v B=%v", gameA, gameB)
	}
	if _, ok := gameA.Dlc["01006f8002327001"]; !ok || len(gameB.Dlc) != 0 {
		t.Fatalf("DLC assigned to the wrong game: A=%v B=%v", gameA.Dlc, gameB.Dlc)
	}
	if gameA.Updates[65536] != "2020-01-01" {
		t.Fatalf("updates not attached: %v", gameA.Updates)
	}
}

func jsonServer(t *testing.T, status int, body string, etag string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if etag != "" && r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if etag != "" {
			w.Header().Set("Etag", etag)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func readAll(t *testing.T, file *os.File) string {
	t.Helper()
	defer file.Close()
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLoadAndUpdateFileFallsBackToMirror(t *testing.T) {
	broken := jsonServer(t, http.StatusServiceUnavailable, "", "")
	invalid := jsonServer(t, http.StatusOK, "<html>challenge</html>", "")
	mirror := jsonServer(t, http.StatusOK, `{"a": 1}`, `"v2"`)
	path := filepath.Join(t.TempDir(), "titles.json")

	file, etag, err := LoadAndUpdateFile([]string{broken.URL, invalid.URL, mirror.URL}, path, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, file); got != `{"a": 1}` || etag != `"v2"` {
		t.Fatalf("unexpected result: %q etag=%q", got, etag)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

func TestLoadAndUpdateFileUsesLocalCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "titles.json")
	if err := os.WriteFile(path, []byte(`{"local": true}`), 0644); err != nil {
		t.Fatal(err)
	}

	notModified := jsonServer(t, http.StatusOK, `{"remote": true}`, `"v1"`)
	file, etag, err := LoadAndUpdateFile([]string{notModified.URL}, path, `"v1"`)
	if err != nil || readAll(t, file) != `{"local": true}` || etag != `"v1"` {
		t.Fatalf("not modified response should keep the local file: err=%v etag=%q", err, etag)
	}

	invalid := jsonServer(t, http.StatusOK, "not json", "")
	file, _, err = LoadAndUpdateFile([]string{invalid.URL}, path, "")
	if err != nil || readAll(t, file) != `{"local": true}` {
		t.Fatalf("invalid download should keep the local file: err=%v", err)
	}
}

func TestLoadAndUpdateFileFailsWithoutAnyCopy(t *testing.T) {
	broken := jsonServer(t, http.StatusServiceUnavailable, "", "")
	path := filepath.Join(t.TempDir(), "titles.json")

	if _, _, err := LoadAndUpdateFile([]string{broken.URL}, path, ""); err == nil {
		t.Fatal("expected an error without remote or local file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("an empty file must not be created")
	}
}

func TestDownloadFileCachesAndNeverLeavesEmptyFiles(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte("image"))
	}))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "icon.jpg")

	for i := 0; i < 2; i++ {
		if err := DownloadFile(server.URL, path); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("existing image downloaded again (%d requests)", requests)
	}

	broken := jsonServer(t, http.StatusNotFound, "", "")
	missing := filepath.Join(dir, "missing.jpg")
	if err := DownloadFile(broken.URL, missing); err == nil {
		t.Fatal("expected download error")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("failed download left a file behind")
	}
}

func TestScanWithFilenameFallback(t *testing.T) {
	romDir := t.TempDir()
	files := []string{
		"Game [0100000000010000][v0].nsp",
		"Game Update [0100000000010800][v65536].nsp",
		"Game DLC [0100000000011001][v0].nsp",
		"Other [01006F8002328000][v0].xci",
		"readme.txt",
		"a",
		"._Game [0100000000010000][v0].nsp",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(romDir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	// no titles database and no keys: classification is based on file names only
	localDB, err := manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), []string{romDir, filepath.Join(romDir, "missing")}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}

	game := localDB.TitlesMap["0100000000010"]
	if game == nil || !game.BaseExist || game.LatestUpdate != 65536 || len(game.Dlc) != 1 {
		t.Fatalf("game not classified correctly: %#v", game)
	}
	if other := localDB.TitlesMap["01006f8002328"]; other == nil || !other.BaseExist {
		t.Fatalf("second game missing: %v", localDB.TitlesMap)
	}
	if len(localDB.Skipped) != 2 {
		t.Fatalf("expected readme.txt and \"a\" to be skipped, got %v", localDB.Skipped)
	}
}

func TestLocalDBManagerRebuildsOldSchemaCache(t *testing.T) {
	dataDir := t.TempDir()
	manager, err := NewLocalSwitchDBManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	stale := map[string]*SwitchGameFiles{"010000000001": {BaseExist: true}}
	if err := manager.db.AddEntry(DB_TABLE_LOCAL_LIBRARY, "titles", stale); err != nil {
		t.Fatal(err)
	}
	// simulate a cache written by a version without schema tracking
	if err := manager.db.SetInternalValue(DB_KEY_LIBRARY_SCHEMA, ""); err != nil {
		t.Fatal(err)
	}
	manager.Close()

	manager, err = NewLocalSwitchDBManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	titles := map[string]*SwitchGameFiles{}
	if err := manager.db.GetEntry(DB_TABLE_LOCAL_LIBRARY, "titles", &titles); err != nil {
		t.Fatal(err)
	}
	if len(titles) != 0 {
		t.Fatalf("stale cache was not cleared: %v", titles)
	}
	if got := manager.db.GetInternalValue(DB_KEY_LIBRARY_SCHEMA); got != LIBRARY_SCHEMA_VERSION {
		t.Fatalf("schema version = %q", got)
	}
}

func TestReadErrorTextExplainsMissingKeys(t *testing.T) {
	missing := readErrorText("NSP", fmt.Errorf("reading: %w", &switchfs.MissingKeyError{KeyName: "key_area_key_application_15"}))
	if !strings.Contains(missing, "key_area_key_application_15") || !strings.Contains(missing, "newer firmware") {
		t.Fatalf("missing key hint not shown: %q", missing)
	}
	if other := readErrorText("XCI", errors.New("broken")); other != "failed to read XCI [reason: broken]" {
		t.Fatalf("unexpected text: %q", other)
	}
}

// A cache with files but no recognized titles used to scan every file twice.
func TestScanDoesNotDuplicateCachedFiles(t *testing.T) {
	romDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(romDir, "unknown.nsp"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	for i := 0; i < 3; i++ {
		localDB, err := manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), []string{romDir}, nil, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if localDB.NumFiles != 1 || len(localDB.Skipped) != 1 {
			t.Fatalf("run %d: %d files, %d skipped", i, localDB.NumFiles, len(localDB.Skipped))
		}
	}
}

func TestKeysChanged(t *testing.T) {
	dataDir := t.TempDir()
	manager, err := NewLocalSwitchDBManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	// no keys before and now: nothing to rescan
	changedWithoutKeys := manager.KeysChanged("")
	// keys were added (e.g. prod.keys copied into the data folder)
	changedWithKeys := manager.KeysChanged("abc")
	manager.Close()
	if changedWithoutKeys || !changedWithKeys {
		t.Fatalf("without keys changed=%v, with keys changed=%v", changedWithoutKeys, changedWithKeys)
	}

	// the fingerprint survives a restart
	manager, err = NewLocalSwitchDBManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if manager.KeysChanged("abc") {
		t.Fatal("fingerprint was not persisted")
	}
}
