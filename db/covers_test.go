package db

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadCovers(t *testing.T) {
	dataFolder := t.TempDir()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("image " + r.URL.Path))
	}))
	defer server.Close()

	cached := &SwitchGameFiles{}
	os.MkdirAll(filepath.Join(dataFolder, "img"), 0755)
	os.WriteFile(filepath.Join(dataFolder, "img", "cached.jpg"), []byte("x"), 0644)
	fresh := &SwitchGameFiles{}

	downloadCovers(dataFolder, []coverDownload{
		{title: cached, url: server.URL + "/cached.jpg", icon: true},
		{title: fresh, url: server.URL + "/fresh.jpg", icon: true},
		{title: fresh, url: server.URL + "/banner.jpg"},
	}, nil)

	if cached.Icon != "cached.jpg" || fresh.Icon != "fresh.jpg" || fresh.Banner != "banner.jpg" {
		t.Fatalf("covers not assigned: %+v %+v", cached, fresh)
	}
	if requests.Load() != 2 {
		t.Fatalf("a cached cover must not be downloaded again: %v requests", requests.Load())
	}
}

func TestDownloadCoversStopsWhenTheServerIsUnreachable(t *testing.T) {
	dataFolder := t.TempDir()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	covers := []coverDownload{}
	for i := 0; i < 200; i++ {
		covers = append(covers, coverDownload{title: &SwitchGameFiles{}, url: fmt.Sprintf("%s/%d.jpg", server.URL, i), icon: true})
	}
	downloadCovers(dataFolder, covers, nil)
	first := requests.Load()
	if first >= 50 {
		t.Fatalf("the downloads must stop after a few failures, got %v requests", first)
	}

	// failed covers are not tried again soon
	downloadCovers(dataFolder, covers[:3], nil)
	if requests.Load() != first {
		t.Fatalf("recent failures must not be retried: %v -> %v", first, requests.Load())
	}
}

func TestCoversThatFailedRecentlyWait(t *testing.T) {
	dataFolder := t.TempDir()
	os.MkdirAll(filepath.Join(dataFolder, "img"), 0755)
	os.WriteFile(filepath.Join(dataFolder, "img", coverFailuresFilename), []byte(fmt.Sprintf(`{"https://x/failed.jpg": %d, "https://x/old.jpg": 1}`, time.Now().Unix())), 0644)
	got := CoversToTry(dataFolder, []string{"https://x/failed.jpg", "https://x/old.jpg", "https://x/new.jpg"})
	if len(got) != 2 || got[0] != "https://x/old.jpg" || got[1] != "https://x/new.jpg" {
		t.Fatalf("got %v", got)
	}
}
