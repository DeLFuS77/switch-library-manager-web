package db

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// published is a file of the data release: its content and its etag, or a status that is no
// file (a 404 when it is missing).
type published struct {
	body   []byte
	etag   string
	status int
}

// releaseServer serves files by name, as the data release does, and counts the requests to each.
type releaseServer struct {
	*httptest.Server
	mutex    sync.Mutex
	files    map[string]published
	requests map[string]int
	// the If-None-Match of the requests, by name
	etags map[string][]string
}

func newReleaseServer(t *testing.T, files map[string]published) *releaseServer {
	t.Helper()
	server := &releaseServer{files: files, requests: map[string]int{}, etags: map[string][]string{}}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		server.mutex.Lock()
		server.requests[name]++
		server.etags[name] = append(server.etags[name], r.Header.Get("If-None-Match"))
		file, ok := server.files[name]
		server.mutex.Unlock()
		switch {
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		case file.status != 0:
			w.WriteHeader(file.status)
		case file.etag != "" && r.Header.Get("If-None-Match") == file.etag:
			w.WriteHeader(http.StatusNotModified)
		default:
			if file.etag != "" {
				w.Header().Set("Etag", file.etag)
			}
			w.Write(file.body)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *releaseServer) count(name string) int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.requests[name]
}

func (s *releaseServer) sentEtags(name string) []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.etags[name]...)
}

// urls are the compressed file first and then the file, as settings.CompressedFirst gives them.
func (s *releaseServer) urls(name string) []string {
	return []string{s.URL + "/" + name + ".zst", s.URL + "/" + name}
}

func compressed(t *testing.T, content string) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll([]byte(content), nil)
}

// The compressed file is downloaded and decompressed: the saved copy is the file itself, and
// the file as it is is not asked for.
func TestLoadAndUpdateDecompressesTheCompressedFile(t *testing.T) {
	content := `{"0100000000010000": {"name": "Game"}}`
	server := newReleaseServer(t, map[string]published{
		"titles.json.zst": {body: compressed(t, content), etag: `"z1"`},
		"titles.json":     {body: []byte(content), etag: `"j1"`},
	})
	path := filepath.Join(t.TempDir(), "titles.json")

	result, err := LoadAndUpdate(server.urls("titles.json"), path, "")
	if err != nil || !result.Updated || result.Etag != `"z1"` {
		t.Fatalf("the compressed file: err=%v %+v", err, result)
	}
	if got := readAll(t, result.File); got != content {
		t.Fatalf("not decompressed: %q", got)
	}
	if server.count("titles.json") != 0 {
		t.Fatal("the file as it is was downloaded too")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

// Without a compressed file, as before the data release had one, the file as it is is used.
func TestLoadAndUpdateFallsBackToTheFileAsItIs(t *testing.T) {
	server := newReleaseServer(t, map[string]published{
		"titles.json": {body: []byte(`{"a": 1}`), etag: `"j1"`},
	})
	path := filepath.Join(t.TempDir(), "titles.json")

	result, err := LoadAndUpdate(server.urls("titles.json"), path, "")
	if err != nil || !result.Updated || result.Etag != `"j1"` || readAll(t, result.File) != `{"a": 1}` {
		t.Fatalf("the file as it is: err=%v %+v", err, result)
	}
	if server.count("titles.json.zst") != 1 {
		t.Fatalf("a missing compressed file is asked for once: %v requests", server.count("titles.json.zst"))
	}
}

// An unchanged file is not downloaded again: the etag of the compressed file gets "not modified"
// from it, and so does the etag of the file as it is when the compressed one is missing.
func TestLoadAndUpdateWithTheEtagOfEither(t *testing.T) {
	path := filepath.Join(t.TempDir(), "titles.json")
	if err := os.WriteFile(path, []byte(`{"saved": true}`), 0644); err != nil {
		t.Fatal(err)
	}
	server := newReleaseServer(t, map[string]published{
		"titles.json.zst": {body: compressed(t, `{"a": 1}`), etag: `"z1"`},
		"titles.json":     {body: []byte(`{"a": 1}`), etag: `"j1"`},
	})
	result, err := LoadAndUpdate(server.urls("titles.json"), path, `"z1"`)
	if err != nil || result.Updated || result.Failure != nil || result.Etag != `"z1"` || readAll(t, result.File) != `{"saved": true}` {
		t.Fatalf("the compressed file did not change: err=%v %+v", err, result)
	}
	if server.count("titles.json") != 0 {
		t.Fatal("the file as it is was asked for")
	}

	// the compressed file is missing for a while: the file as it is gets the etag too
	missing := newReleaseServer(t, map[string]published{
		"titles.json": {body: []byte(`{"a": 1}`), etag: `"j1"`},
	})
	result, err = LoadAndUpdate(missing.urls("titles.json"), path, `"j1"`)
	if err != nil || result.Updated || result.Failure != nil || readAll(t, result.File) != `{"saved": true}` {
		t.Fatalf("the file as it is did not change: err=%v %+v", err, result)
	}
	if got := missing.sentEtags("titles.json"); len(got) != 1 || got[0] != `"j1"` {
		t.Fatalf("the file as it is was asked for without the etag: %q", got)
	}
}

// The etag goes to the first file and to the same file as it is, but never to a mirror: a mirror
// answering "not modified" to the etag of another server would keep an old copy.
func TestEtagIsNotSentToAMirror(t *testing.T) {
	path := filepath.Join(t.TempDir(), "titles.json")
	server := newReleaseServer(t, map[string]published{
		"titles.json.zst": {status: http.StatusNotFound},
		"titles.json":     {status: http.StatusNotFound},
	})
	mirror := newReleaseServer(t, map[string]published{
		"titles.json": {body: []byte(`{"mirror": true}`), etag: `"m1"`},
	})
	result, err := LoadAndUpdate(append(server.urls("titles.json"), mirror.URL+"/titles.json"), path, `"j1"`)
	if err != nil || !result.Updated || readAll(t, result.File) != `{"mirror": true}` {
		t.Fatalf("the mirror: err=%v %+v", err, result)
	}
	if got := mirror.sentEtags("titles.json"); len(got) != 1 || got[0] != "" {
		t.Fatalf("the etag was sent to the mirror: %q", got)
	}
}

// A compressed file that is not one, or is cut short, is not used: the file as it is is.
func TestDamagedCompressedFileIsNotUsed(t *testing.T) {
	whole := compressed(t, `{"a": 1}`)
	cases := map[string]published{
		"a page":    {body: []byte("<html>challenge</html>")},
		"cut short": {body: whole[:len(whole)-6]},
	}
	for name, damaged := range cases {
		t.Run(name, func(t *testing.T) {
			server := newReleaseServer(t, map[string]published{
				"titles.json.zst": damaged,
				"titles.json":     {body: []byte(`{"plain": true}`)},
			})
			path := filepath.Join(t.TempDir(), "titles.json")
			result, err := LoadAndUpdate(server.urls("titles.json"), path, "")
			if err != nil || !result.Updated || readAll(t, result.File) != `{"plain": true}` {
				t.Fatalf("the file as it is: err=%v %+v", err, result)
			}
		})
	}

	// what is no compressed file at all is not asked for again
	server := newReleaseServer(t, map[string]published{
		"titles.json.zst": {body: []byte("<html>challenge</html>")},
		"titles.json":     {body: []byte(`{"plain": true}`)},
	})
	result, err := LoadAndUpdate(server.urls("titles.json"), filepath.Join(t.TempDir(), "titles.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	result.File.Close()
	if server.count("titles.json.zst") != 1 {
		t.Fatalf("a page instead of a compressed file was retried: %v requests", server.count("titles.json.zst"))
	}
}

// A small compressed file that grows larger than any download is refused, and the saved copy stays.
func TestCompressedFileTooLargeIsRefused(t *testing.T) {
	limit := maxDecompressedSize
	t.Cleanup(func() { maxDecompressedSize = limit })
	maxDecompressedSize = 1000

	large := `{"a": "` + strings.Repeat("x", 100000) + `"}`
	server := newReleaseServer(t, map[string]published{
		"titles.json.zst": {body: compressed(t, large)},
	})
	path := filepath.Join(t.TempDir(), "titles.json")
	if err := os.WriteFile(path, []byte(`{"saved": true}`), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := LoadAndUpdate(server.urls("titles.json")[:1], path, "")
	if err != nil || result.Updated || result.Failure == nil || readAll(t, result.File) != `{"saved": true}` {
		t.Fatalf("the saved copy: err=%v %+v", err, result)
	}
	if server.count("titles.json.zst") != 1 {
		t.Fatalf("a file too large was retried: %v requests", server.count("titles.json.zst"))
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

// A compressed file of several frames is read whole.
func TestDecompressDownloadOfSeveralFrames(t *testing.T) {
	var source bytes.Buffer
	source.Write(compressed(t, `{"a": `))
	source.Write(compressed(t, `1}`))
	var output bytes.Buffer
	if err := decompressDownload(&output, &source); err != nil || output.String() != `{"a": 1}` {
		t.Fatalf("two frames: err=%v %q", err, output.String())
	}
}
