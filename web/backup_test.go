package web

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	for name, content := range files {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		file.Write([]byte(content))
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestBackupLeavesOutKeysAndSessionKey(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		settings.SETTINGS_FILENAME: `{"folder":"/games"}`,
		USERS_FILENAME:             `[]`,
		"prod.keys":                "header_key = 00",
		"title.keys":               "00 = 00",
		"session.key":              "secret",
		"slm.db":                   "cache",
	} {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
	}
	web := &Web{dataFolder: dir}
	buffer := &bytes.Buffer{}
	if err := web.writeBackup(buffer); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, file := range archive.File {
		names[file.Name] = true
	}
	if !names[settings.SETTINGS_FILENAME] || !names[USERS_FILENAME] || len(names) != 2 {
		t.Fatalf("backup has %v", names)
	}
}

func TestReadBackupRejectsInvalidFiles(t *testing.T) {
	if _, err := readBackup([]byte("not a zip")); !errors.Is(err, ErrBackupInvalid) {
		t.Fatalf("not a zip: %v", err)
	}
	if _, err := readBackup(zipFiles(t, map[string]string{USERS_FILENAME: "[]"})); !errors.Is(err, ErrBackupInvalid) {
		t.Fatalf("no settings: %v", err)
	}
	if _, err := readBackup(zipFiles(t, map[string]string{settings.SETTINGS_FILENAME: "{", USERS_FILENAME: "[]"})); !errors.Is(err, ErrBackupFile) {
		t.Fatalf("damaged settings: %v", err)
	}
	files, err := readBackup(zipFiles(t, map[string]string{
		settings.SETTINGS_FILENAME: "{}",
		"prod.keys":                "header_key = 00",
		"../escape.json":           "{}",
	}))
	if err != nil || len(files) != 1 {
		t.Fatalf("other files must be ignored: %v %v", files, err)
	}
}

func TestRestoreBackupNeedsAnAdministrator(t *testing.T) {
	web := &Web{dataFolder: t.TempDir()}
	backup := zipFiles(t, map[string]string{
		settings.SETTINGS_FILENAME: "{}",
		USERS_FILENAME:             `[{"name":"ana","role":"viewer","password_hash":"x"}]`,
	})
	if err := web.restoreBackup(backup); !errors.Is(err, ErrBackupAdmin) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, USERS_FILENAME)); err == nil {
		t.Fatal("nothing may be written when the backup is refused")
	}
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	web := usersWeb(t)
	if err := web.auth.users.Add("ana", "password1", ROLE_ADMIN); err != nil {
		t.Fatal(err)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ConsoleFirmware = "18.1.0" })

	buffer := &bytes.Buffer{}
	if err := web.writeBackup(buffer); err != nil {
		t.Fatal(err)
	}

	web.auth.users.Add("luis", "password2", ROLE_VIEWER)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ConsoleFirmware = "19.0.0" })

	before := settings.Version()
	if err := web.restoreBackup(buffer.Bytes()); err != nil {
		t.Fatal(err)
	}
	if got := settings.ReadSettings(web.dataFolder).ConsoleFirmware; got != "18.1.0" {
		t.Fatalf("settings not restored: %q", got)
	}
	if settings.Version() == before {
		t.Fatal("cached pages must be rebuilt")
	}
	if _, ok := web.auth.users.Get("luis"); ok {
		t.Fatal("users not restored")
	}
	if _, ok := web.auth.users.Verify("ana", "password1"); !ok {
		t.Fatal("restored user cannot log in")
	}
}

func TestBackupMessagesAreTranslated(t *testing.T) {
	for _, err := range backupErrors {
		if _, ok := translations["es"][err.Error()]; !ok {
			t.Errorf("no Spanish translation for %q", err.Error())
		}
	}
}

// The Content Security Policy forbids inline scripts and event handler attributes.
func TestTemplatesHaveNoInlineScripts(t *testing.T) {
	inline := regexp.MustCompile(`<script(\s[^>]*)?>|\son[a-z]+="`)
	err := filepath.WalkDir("../resources", func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		// the exported web page opens from a file, without the app and its policy
		if filepath.Base(filepath.Dir(path)) == "export" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range inline.FindAllString(string(data), -1) {
			if !strings.Contains(match, "src=") && !strings.Contains(match, "application/json") {
				t.Errorf("%s: %s", path, match)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSecurityHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	withSecurityHeaders(http.NotFoundHandler()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if policy := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "script-src 'self';") {
		t.Fatalf("policy %q", policy)
	}
	if recorder.Header().Get("X-Frame-Options") != "DENY" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing headers")
	}
}
