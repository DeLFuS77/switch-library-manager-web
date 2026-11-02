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
	oldSettings, oldKeys := settingsInstance.Load(), keysInstance.Load()
	settingsInstance.Store(nil)
	keysInstance.Store(nil)
	// never pick up a real ~/.switch/prod.keys from the machine running the tests
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		settingsInstance.Store(oldSettings)
		keysInstance.Store(oldKeys)
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

// The files of the data release are asked for compressed first, and then as they are; the files
// of other places have no compressed copy to ask for.
func TestDataReleaseFilesAreCompressedFirst(t *testing.T) {
	s := &AppSettings{TitlesJsonUrl: DEFAULT_TITLES_JSON_URL, VersionsJsonUrl: DEFAULT_VERSIONS_JSON_URL}

	want := append([]string{DEFAULT_TITLES_JSON_URL + ".zst", DEFAULT_TITLES_JSON_URL}, FALLBACK_TITLES_JSON_URLS...)
	if got := s.TitlesJsonUrls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TitlesJsonUrls() = %v, want %v", got, want)
	}
	// the versions of the data release, then the ones of blawar/titledb alone
	want = []string{DATA_RELEASE_URL + "versions.json.zst", DATA_RELEASE_URL + "versions.json", BLAWAR_VERSIONS_JSON_URL}
	if got := s.VersionsJsonUrls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("VersionsJsonUrls() = %v, want %v", got, want)
	}

	spanish := DATA_RELEASE_URL + "titles.es.json"
	if got := CompressedFirst(spanish); !reflect.DeepEqual(got, []string{spanish + ".zst", spanish}) {
		t.Fatalf("CompressedFirst(%q) = %v", spanish, got)
	}
	for _, url := range []string{"https://example.com/titles.json", BLAWAR_VERSIONS_JSON_URL, DATA_RELEASE_URL + "titles.json.zst"} {
		if got := CompressedFirst(url); !reflect.DeepEqual(got, []string{url}) {
			t.Fatalf("CompressedFirst(%q) = %v, want it alone", url, got)
		}
	}
}

// Settings saved when the versions of blawar/titledb were the default get the versions of the
// data release; a URL of one's own stays.
func TestOldDefaultVersionsUrlFollowsTheDataRelease(t *testing.T) {
	for saved, want := range map[string]string{
		BLAWAR_VERSIONS_JSON_URL:            DEFAULT_VERSIONS_JSON_URL,
		"https://example.com/versions.json": "https://example.com/versions.json",
	} {
		isolateSettings(t)
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), `{"versions_json_url": "`+saved+`", "port": 8080}`)
		if s := ReadSettings(dir); s.VersionsJsonUrl != want || s.Port != 8080 {
			t.Fatalf("saved %q: got %q, want %q", saved, s.VersionsJsonUrl, want)
		}
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

// A settings file that cannot be read (a power cut while it was written by an older version)
// gives way to the copy of the settings saved before it, not to the defaults: the folders, the
// tokens and every choice were lost.
func TestSettingsComeBackFromTheCopySavedBefore(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	if err := TrySaveSettings(&AppSettings{Folder: "/first", Port: 8080}, dir); err != nil {
		t.Fatal(err)
	}
	if err := TrySaveSettings(&AppSettings{Folder: "/second", Port: 8080}, dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), "")
	settingsInstance.Store(nil)
	if s := ReadSettings(dir); s.Folder != "/first" || s.Port != 8080 {
		t.Fatalf("the copy saved before: %+v", s)
	}
	if aside, _ := filepath.Glob(filepath.Join(dir, SETTINGS_FILENAME+".corrupt-*")); len(aside) != 1 {
		t.Fatalf("the file that cannot be read is kept aside: %v", aside)
	}
	// and saved again as the settings
	settingsInstance.Store(nil)
	if s := ReadSettings(dir); s.Folder != "/first" {
		t.Fatalf("saved: %+v", s)
	}
}

// Settings that cannot be saved are not changed, in memory either: the page said "Saved" and the
// change was gone at the next start.
func TestSettingsThatCannotBeSavedStayAsTheyWere(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	ReadSettings(dir)
	if _, err := TryUpdateSettings(dir, func(s *AppSettings) { s.Folder = "/before" }); err != nil {
		t.Fatal(err)
	}
	// a folder where the file is written aside: it cannot be
	blocked := filepath.Join(dir, SETTINGS_FILENAME+".tmp")
	if err := os.MkdirAll(filepath.Join(blocked, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	current, err := TryUpdateSettings(dir, func(s *AppSettings) { s.Folder = "/after" })
	if err == nil || current.Folder != "/before" || ReadSettings(dir).Folder != "/before" {
		t.Fatalf("not changed: %v %+v", err, ReadSettings(dir))
	}
	if UpdateSettings(dir, func(s *AppSettings) { s.Folder = "/after" }).Folder != "/before" {
		t.Fatal("not changed by UpdateSettings either")
	}
	saved := &AppSettings{}
	if err := decodeSettings(filepath.Join(dir, SETTINGS_FILENAME), saved); err != nil || saved.Folder != "/before" {
		t.Fatalf("the file as it was: %v %+v", err, saved)
	}
}

// Folders are left out by the start, the end or a part of their names, in any case; a word
// without * is a part too, and only the * at the ends count.
func TestExcludedFolder(t *testing.T) {
	patterns := []string{"_*", "*(Copy)", "*build*", "Cache", " .git/ ", "[old]*", "a*b", "*", ""}
	for name, want := range map[string]bool{
		"_Tests": true, "My_games": false, "Zelda (copy)": true, "(copy) Zelda": false, "Dragon Quest Builders": true,
		"CACHE2": true, ".github": true, "[OLD] games": true, "games [old]": false, "xa*bx": true, "ab": false, "Zelda": false, "": false,
	} {
		if got := ExcludedFolder(patterns, name); got != want {
			t.Errorf("%q: %v, want %v", name, got, want)
		}
	}
	if ExcludedFolder(nil, "cache") || ExcludedFolder([]string{"  ", "/", "**"}, "cache") {
		t.Fatal("nothing to look for leaves nothing out")
	}
}

// A text of a kind is kept as a line of the settings, and read back as the same kind and text.
func TestSkipPatterns(t *testing.T) {
	for _, c := range []struct{ kind, text, pattern string }{
		{SKIP_STARTING, " _ ", "_*"}, {SKIP_STARTING, "_*", "_*"}, {SKIP_ENDING, "_old", "*_old"}, {SKIP_ENDING, "*_old", "*_old"},
		{SKIP_CONTAINING, "build", "*build*"}, {SKIP_CONTAINING, "*", ""}, {SKIP_STARTING, " / ", ""},
	} {
		pattern := SkipPattern(c.kind, c.text)
		if pattern != c.pattern {
			t.Errorf("%s %q: %q, want %q", c.kind, c.text, pattern, c.pattern)
			continue
		}
		if kind, text := FolderSkip(pattern); pattern != "" && (kind != c.kind || text != SkipText(c.text)) {
			t.Errorf("%q read back as %s %q", pattern, kind, text)
		}
	}
	s := AppSettings{ExcludeFolders: []string{"_*", "*_old", "*build*", "cache", "*(copy)"}}
	if got := s.SkipFolders(SKIP_STARTING); !reflect.DeepEqual(got, []string{"_"}) {
		t.Fatalf("starting: %v", got)
	}
	if got := s.SkipFolders(SKIP_ENDING); !reflect.DeepEqual(got, []string{"_old", "(copy)"}) {
		t.Fatalf("ending: %v", got)
	}
	if got := s.SkipFolders(SKIP_CONTAINING); !reflect.DeepEqual(got, []string{"build", "cache"}) {
		t.Fatalf("containing: %v", got)
	}
}

func TestValidFolderPattern(t *testing.T) {
	for line, want := range map[string]bool{
		"_build": true, "cache/": true, `cache\`: true, "*_build*": true, "[a*": true,
		"/mnt/roms/cache": false, `D:\roms\cache`: false, "a/b": false, "/": false, "  ": false, "**": false,
	} {
		if got := ValidFolderPattern(line); got != want {
			t.Errorf("%q: %v, want %v", line, got, want)
		}
	}
}

// Settings written before the folders to leave out existed leave none out, and the list is
// saved and read back.
func TestExcludeFoldersAreMissingFromOlderSettings(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, SETTINGS_FILENAME), `{"folder": "/roms", "scan_folders": ["/more"], "port": 8080}`)
	if s := ReadSettings(dir); len(s.ExcludeFolders) != 0 || s.Folder != "/roms" {
		t.Fatalf("older settings: %#v", s.ExcludeFolders)
	}
	UpdateSettings(dir, func(s *AppSettings) { s.ExcludeFolders = []string{"_build", "*_old*"} })
	settingsInstance.Store(nil)
	if s := ReadSettings(dir); !reflect.DeepEqual(s.ExcludeFolders, []string{"_build", "*_old*"}) {
		t.Fatalf("read back: %#v", s.ExcludeFolders)
	}
	// never written for the options of Organize, which only get them while organizing
	data, _ := os.ReadFile(filepath.Join(dir, SETTINGS_FILENAME))
	if strings.Count(string(data), "_build") != 1 {
		t.Fatalf("saved once: %s", data)
	}
}
