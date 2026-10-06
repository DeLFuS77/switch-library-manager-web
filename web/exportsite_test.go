package web

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportWebPage(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	switchDB, localDB := testDatabases(t)
	localDB.TitlesMap["0100000000010"].Icon = "cover.jpg"
	os.MkdirAll(filepath.Join(web.dataFolder, "img", "thumbs"), 0755)
	os.WriteFile(filepath.Join(web.dataFolder, "img", "thumbs", "cover.jpg"), []byte("jpeg"), 0644)
	web.state.set(switchDB, localDB)
	web.HandleExport()

	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/export/library-site.zip", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("got %d", recorder.Code)
	}
	archive, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range archive.File {
		reader, _ := file.Open()
		data, _ := io.ReadAll(reader)
		reader.Close()
		files[file.Name] = string(data)
	}
	page := files["index.html"]
	if !strings.Contains(page, "Known Game") || !strings.Contains(page, `src="covers/1.jpg"`) || files["covers/1.jpg"] != "jpeg" {
		t.Fatalf("exported page: %d files", len(files))
	}
	for name := range files {
		if strings.Contains(name, "keys") || strings.HasSuffix(name, ".nsp") {
			t.Fatalf("only the page and covers are exported: %s", name)
		}
	}
}
