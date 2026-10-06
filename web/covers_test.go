package web

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

func TestCoversAreDownloadedAfterTheScan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("image " + r.URL.Path))
	}))
	defer server.Close()

	web := newTestWeb(t)
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	web.localDbManager = manager

	// one cover is cached already, the others are downloaded
	os.MkdirAll(filepath.Join(web.dataFolder, "img"), 0755)
	os.WriteFile(filepath.Join(web.dataFolder, "img", "cached.jpg"), []byte("x"), 0644)
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": {Attributes: db.TitleAttributes{Id: "0100000000010000", IconUrl: server.URL + "/cached.jpg", BannerUrl: server.URL + "/banner1.jpg"}},
		"0100000000020": {Attributes: db.TitleAttributes{Id: "0100000000020000", IconUrl: server.URL + "/icon2.jpg"}},
	}}
	first := &db.SwitchGameFiles{BaseExist: true}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": first,
		"0100000000020": {BaseExist: true},
	}}
	web.state.set(switchDB, localDB)
	before := web.state.version

	web.downloadMissingCovers()

	_, updated := web.state.get()
	if updated == localDB || web.state.version == before {
		t.Fatal("the library is replaced by a copy with the covers")
	}
	if first.Icon != "" {
		t.Fatal("the library pages were reading must not change")
	}
	game1, game2 := updated.TitlesMap["0100000000010"], updated.TitlesMap["0100000000020"]
	if game1.Icon != "cached.jpg" || game1.Banner != "banner1.jpg" || game2.Icon != "icon2.jpg" {
		t.Fatalf("covers: %+v %+v", game1, game2)
	}

	// the downloads are shown in Tasks
	var coverTask *Task
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == TASK_COVERS {
			coverTask = &task
		}
	}
	if coverTask == nil || coverTask.Status != TASK_SUCCESS || coverTask.Files != 2 || coverTask.Trigger != TRIGGER_SCAN {
		t.Fatalf("cover task: %+v", coverTask)
	}

	// the covers are saved for the next start
	saved, err := manager.CreateLocalSwitchFilesDB(switchDB, web.dataFolder, []string{}, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.TitlesMap["0100000000020"] == nil || saved.TitlesMap["0100000000020"].Icon != "icon2.jpg" {
		t.Fatalf("saved library: %+v", saved.TitlesMap)
	}
}

func TestCoverDownloadsStopForAScan(t *testing.T) {
	web := newTestWeb(t)
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": {Attributes: db.TitleAttributes{Id: "0100000000010000", IconUrl: "http://127.0.0.1:1/icon.jpg"}},
	}}
	web.state.set(switchDB, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{"0100000000010": {BaseExist: true}}})
	web.state.startSync()
	web.downloadMissingCovers()
	if !web.covers.again {
		t.Fatal("covers are downloaded again after the scan")
	}
}

func TestRemoteCoversAreThumbnailedAndCached(t *testing.T) {
	cover := &bytes.Buffer{}
	jpeg.Encode(cover, image.NewRGBA(image.Rect(0, 0, 800, 800)), nil)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write(cover.Bytes())
	}))
	defer server.Close()
	saved := remoteCoverBase
	remoteCoverBase = server.URL + "/i/"
	defer func() { remoteCoverBase = saved }()

	name := strings.Repeat("ab", 32) + ".jpg"
	if got := thumbUrl(remoteCoverBase + name); got != "/t/"+name {
		t.Fatalf("thumbUrl = %q", got)
	}
	if got := thumbUrl("https://example.com/i/" + name); got != "https://example.com/i/"+name {
		t.Fatal("other sites are left alone")
	}

	web := newTestWeb(t)
	mux := http.NewServeMux()
	saveMux := http.DefaultServeMux
	http.DefaultServeMux = mux
	defer func() { http.DefaultServeMux = saveMux }()
	web.HandleImages()
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/t/"+name, nil))
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("got %d %s", recorder.Code, recorder.Header().Get("Content-Type"))
		}
	}
	if requests != 1 {
		t.Fatalf("the cover is fetched once and cached: %d requests", requests)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/t/not-a-cover.jpg", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("only names of the cover server are fetched: %d", recorder.Code)
	}
}
