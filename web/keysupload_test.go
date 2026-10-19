package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// made-up keys: the shape of a prod.keys, none of its values
const fakeProdKeys = "header_key = 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff\n" +
	"key_area_key_application_00 = 0123456789abcdef0123456789abcdef\n" +
	"; a comment\n" +
	"master_key_00 = fedcba9876543210fedcba9876543210\n"

const fakeTitleKeys = "0100000000010000000000000000000a = 0123456789abcdef0123456789abcdef\n"

func TestCheckKeys(t *testing.T) {
	if err := checkProdKeys([]byte(fakeProdKeys)); err != nil {
		t.Fatalf("a prod.keys: %v", err)
	}
	for _, bad := range []string{"", "hello", "header_key = 00\n", "key_area_key_application_00 = 00\n", "header_key = zz\nkey_area_key_application_00 = 00\n"} {
		if checkProdKeys([]byte(bad)) == nil {
			t.Errorf("not a prod.keys: %q", bad)
		}
	}
	if err := checkTitleKeys([]byte(fakeTitleKeys)); err != nil {
		t.Fatalf("a title.keys: %v", err)
	}
	if checkTitleKeys([]byte(fakeProdKeys)) == nil || checkTitleKeys(nil) == nil {
		t.Error("a prod.keys or nothing is not a title.keys")
	}
}

func uploadKeys(t *testing.T, web *Web, files map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	for field, content := range files {
		part, _ := form.CreateFormFile(field, field+".keys")
		part.Write([]byte(content))
	}
	form.Close()
	request := httptest.NewRequest(http.MethodPost, "/settings/keys", body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, request)
	return recorder
}

// The keys sent from Settings are checked, kept in the data folder for the owner only, and the
// settings point to them; anything else is refused and nothing is written.
func TestKeysUpload(t *testing.T) {
	web := newTestWeb(t)
	// saving the keys scans the library again
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	web.localDbManager = manager
	web.HandleKeysUpload()

	if response := uploadKeys(t, web, map[string]string{"prod_keys": "not keys"}); response.Code != http.StatusBadRequest {
		t.Fatalf("a file that is not keys: %d", response.Code)
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, "prod.keys")); !os.IsNotExist(err) {
		t.Fatal("nothing is written for a refused file")
	}
	if response := uploadKeys(t, web, map[string]string{"prod_keys": fakeProdKeys, "title_keys": "nope"}); response.Code != http.StatusBadRequest {
		t.Fatalf("a wrong title.keys: %d", response.Code)
	}

	response := uploadKeys(t, web, map[string]string{"prod_keys": fakeProdKeys, "title_keys": fakeTitleKeys})
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "0123456789abcdef") {
		t.Fatalf("the keys are saved, and never sent back: %d %s", response.Code, response.Body.String())
	}
	for name, want := range map[string]string{"prod.keys": fakeProdKeys, "title.keys": fakeTitleKeys} {
		path := filepath.Join(web.dataFolder, name)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s saved: %v", name, err)
		}
		if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf("%s readable by the owner only: %v", name, info.Mode().Perm())
		}
	}
	if got := settings.ReadSettings(web.dataFolder).Prodkeys; got != web.dataFolder {
		t.Fatalf("the settings point to the keys of the data folder: %q", got)
	}
}
