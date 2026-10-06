package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Key discovery tests adapted from https://github.com/trembon/switch-library-manager

func isolateSettings(t *testing.T) {
	t.Helper()
	oldSettings, oldKeys := settingsInstance.Load(), keysInstance
	settingsInstance.Store(nil)
	keysInstance = nil
	// never pick up a real ~/.switch/prod.keys from the machine running the tests
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		settingsInstance.Store(oldSettings)
		keysInstance = oldKeys
	})
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultSettings(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()

	s := ReadSettings(dir)
	if s.TitlesJsonUrl != DEFAULT_TITLES_JSON_URL || s.VersionsJsonUrl != DEFAULT_VERSIONS_JSON_URL {
		t.Fatalf("unexpected default URLs: %q %q", s.TitlesJsonUrl, s.VersionsJsonUrl)
	}
	if s.Prodkeys != "" || s.Port != 3000 {
		t.Fatalf("unexpected defaults: %#v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, SETTINGS_FILENAME)); err != nil {
		t.Fatalf("default settings were not saved: %v", err)
	}
}

func TestOldSettingsFileGetsDefaultURLsAndEtagReset(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	// settings.json written by a version without configurable URLs, and no cached JSON files
	writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), `{"titles_etag": "old", "versions_etag": "old", "folder": "/roms", "port": 8080}`)

	s := ReadSettings(dir)
	if s.TitlesJsonUrl != DEFAULT_TITLES_JSON_URL || s.VersionsJsonUrl != DEFAULT_VERSIONS_JSON_URL {
		t.Fatalf("missing URLs were not filled in: %q %q", s.TitlesJsonUrl, s.VersionsJsonUrl)
	}
	if s.TitlesEtag != DEFAULT_TITLES_ETAG || s.VersionsEtag != DEFAULT_VERSIONS_ETAG {
		t.Fatalf("etags were not reset without local files: %q %q", s.TitlesEtag, s.VersionsEtag)
	}
	if s.Folder != "/roms" || s.Port != 8080 {
		t.Fatalf("existing values were lost: %#v", s)
	}
}

func TestEtagKeptWhenLocalFileExists(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, TITLE_JSON_FILENAME), "{}")
	writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), `{"titles_etag": "current"}`)

	if s := ReadSettings(dir); s.TitlesEtag != "current" {
		t.Fatalf("etag was reset although titles.json exists: %q", s.TitlesEtag)
	}
}

func TestMalformedSettingsFallsBackToDefaults(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), "{not json")

	s := ReadSettings(dir)
	if s.Port != 3000 || s.TitlesJsonUrl != DEFAULT_TITLES_JSON_URL {
		t.Fatalf("malformed settings did not fall back to defaults: %#v", s)
	}
}

func TestJsonUrlsIncludeFallbacks(t *testing.T) {
	s := &AppSettings{TitlesJsonUrl: "https://example.com/titles.json", VersionsJsonUrl: FALLBACK_VERSIONS_JSON_URLS[0]}

	want := append([]string{"https://example.com/titles.json"}, FALLBACK_TITLES_JSON_URLS...)
	if got := s.TitlesJsonUrls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TitlesJsonUrls() = %v, want %v", got, want)
	}
	// a configured URL equal to a fallback is not tried twice
	if got := s.VersionsJsonUrls(); len(got) != len(FALLBACK_VERSIONS_JSON_URLS) {
		t.Fatalf("VersionsJsonUrls() = %v, expected no duplicates", got)
	}
}

func TestKeyDiscoveryOrderAndMissingKeys(t *testing.T) {
	isolateSettings(t)
	base := t.TempDir()
	configuredDir := filepath.Join(base, "configured")
	if err := os.Mkdir(configuredDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(configuredDir, "prod.keys"), "header_key = configured\nfoo = bar\n")
	writeFile(t, filepath.Join(base, "prod.keys"), "header_key = current\n")
	SaveSettings(&AppSettings{Prodkeys: configuredDir}, base)
	keys, err := InitSwitchKeys(base)
	if err != nil || keys.GetKey("header_key") != "configured" || keys.GetKey("foo") != "bar" {
		t.Fatalf("configured key discovery: keys=%v err=%v", keys, err)
	}

	isolateSettings(t)
	SaveSettings(&AppSettings{Prodkeys: filepath.Join(base, "missing.keys")}, base)
	keys, err = InitSwitchKeys(base)
	if err != nil || keys.GetKey("header_key") != "current" {
		t.Fatalf("data folder fallback: keys=%v err=%v", keys, err)
	}

	isolateSettings(t)
	emptyBase := t.TempDir()
	SaveSettings(&AppSettings{}, emptyBase)
	keys, err = InitSwitchKeys(emptyBase)
	if err == nil || keys != nil || !strings.Contains(err.Error(), "prod.keys") {
		t.Fatalf("missing key result: keys=%v err=%v", keys, err)
	}
	if IsKeysFileAvailable() {
		t.Fatal("keys reported as available after failed discovery")
	}
}

func TestKeyDiscoveryHomeFallback(t *testing.T) {
	isolateSettings(t)
	base := t.TempDir()
	home := os.Getenv("HOME")
	if err := os.Mkdir(filepath.Join(home, ".switch"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".switch", "prod.keys"), "header_key = home\n")
	SaveSettings(&AppSettings{}, base)
	keys, err := InitSwitchKeys(base)
	if err != nil || keys.GetKey("header_key") != "home" || !IsKeysFileAvailable() {
		t.Fatalf("home key discovery: keys=%v err=%v", keys, err)
	}
}

func TestGetSwitchKeysAcceptsFolderAndFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prod.keys"), "header_key = folder\n")
	keyFile := filepath.Join(dir, "custom.KEYS")
	writeFile(t, keyFile, "header_key = file\n")

	if keys, err := GetSwitchKeys(dir); err != nil || keys["header_key"] != "folder" {
		t.Fatalf("folder path: keys=%v err=%v", keys, err)
	}
	if keys, err := GetSwitchKeys(keyFile); err != nil || keys["header_key"] != "file" {
		t.Fatalf("file path: keys=%v err=%v", keys, err)
	}
	if _, err := GetSwitchKeys(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error for missing keys")
	}
}

func TestMissingOrganizeTemplatesGetDefaults(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), `{"organize_options": {"rename_files": true}}`)

	s := ReadSettings(dir)
	if s.OrganizeOptions.FolderNameTemplate != DEFAULT_FOLDER_NAME_TEMPLATE || s.OrganizeOptions.FileNameTemplate != DEFAULT_FILE_NAME_TEMPLATE || !s.OrganizeOptions.RenameFiles {
		t.Fatalf("unexpected organize options: %+v", s.OrganizeOptions)
	}
}

func TestNotificationDefaults(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	// settings.json written by a version without notifications
	writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), `{"port": 3000}`)
	if s := ReadSettings(dir); !s.Notifications.NotifyUpdates || !s.Notifications.NotifyDlc {
		t.Fatalf("notification kinds should default to enabled: %+v", s.Notifications)
	}

	isolateSettings(t)
	writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), `{"notifications": {"notify_updates": false, "notify_dlc": true}}`)
	if s := ReadSettings(dir); s.Notifications.NotifyUpdates || !s.Notifications.NotifyDlc {
		t.Fatalf("saved choices must be kept: %+v", s.Notifications)
	}
}

func TestKeysFingerprint(t *testing.T) {
	isolateSettings(t)
	if KeysFingerprint() != "" {
		t.Fatal("no keys loaded: empty fingerprint expected")
	}
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "prod.keys"), "header_key = aa\nkey_area_key_application_00 = bb\n")
	SaveSettings(&AppSettings{}, base)
	if _, err := InitSwitchKeys(base); err != nil {
		t.Fatal(err)
	}
	first := KeysFingerprint()
	if len(first) != 16 || strings.Contains(first, "aa") {
		t.Fatalf("unexpected fingerprint %q", first)
	}

	// a key is added (newer firmware)
	writeFile(t, filepath.Join(base, "prod.keys"), "header_key = aa\nkey_area_key_application_00 = bb\nkey_area_key_application_15 = cc\n")
	InitSwitchKeys(base)
	if second := KeysFingerprint(); second == first || second == "" {
		t.Fatalf("fingerprint did not change: %q %q", first, second)
	}
}

func TestMovedRepositoryUrl(t *testing.T) {
	original := previousOwnerHash
	defer func() { previousOwnerHash = original }()
	hash := sha256.Sum256([]byte("oldowner"))
	previousOwnerHash = hex.EncodeToString(hash[:])

	for url, want := range map[string]string{
		"https://github.com/OldOwner/switch-library-manager-web/releases/download/data/titles.json": "https://github.com/" + REPOSITORY_OWNER + "/switch-library-manager-web/releases/download/data/titles.json",
		"https://github.com/someone/switch-library-manager-web/releases/download/data/titles.json":  "https://github.com/someone/switch-library-manager-web/releases/download/data/titles.json",
		"https://github.com/OldOwner/other-project/titles.json":                                     "https://github.com/OldOwner/other-project/titles.json",
		"https://raw.githubusercontent.com/blawar/titledb/master/versions.json":                     "https://raw.githubusercontent.com/blawar/titledb/master/versions.json",
		"": "",
	} {
		if got := movedRepositoryUrl(url); got != want {
			t.Errorf("movedRepositoryUrl(%q) = %q, want %q", url, got, want)
		}
	}
}
