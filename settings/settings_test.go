package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Key discovery tests adapted from https://github.com/trembon/switch-library-manager

func isolateSettings(t *testing.T) {
	t.Helper()
	oldSettings, oldKeys := settingsInstance, keysInstance
	settingsInstance = nil
	keysInstance = nil
	// never pick up a real ~/.switch/prod.keys from the machine running the tests
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		settingsInstance, keysInstance = oldSettings, oldKeys
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
