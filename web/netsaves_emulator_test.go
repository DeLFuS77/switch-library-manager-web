package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The names the files of a save of an emulator take: the folder dropped goes, a folder of the
// game (its title ID) the save was zipped in goes, the folder 0 of Ryujinx is the save and 1 is
// left, the files the system leaves go, and a folder of the save itself stays.
func TestEmulatorSaveNames(t *testing.T) {
	cases := []struct {
		names  []string
		folder bool
		want   []string
		err    bool
	}{
		{names: []string{"Saves/save.dat", "Saves/sub/x.bin"}, folder: true, want: []string{"save.dat", "sub/x.bin"}},
		{names: []string{"0100000000010000/save.dat", "0100000000010000/system.dat"}, want: []string{"save.dat", "system.dat"}},
		{names: []string{"0000000000000001/0/a.bin", "0000000000000001/1/a.bin", "0000000000000001/0/b/c.bin"}, folder: true, want: []string{"a.bin", "b/c.bin"}},
		{names: []string{"0/a.bin", "1/a.bin"}, want: []string{"a.bin"}},
		{names: []string{"data/a.bin", "data/b.bin"}, want: []string{"data/a.bin", "data/b.bin"}},
		{names: []string{`win\save.dat`, ".DS_Store", "Thumbs.db"}, want: []string{"win/save.dat"}},
		{names: []string{"../escape.bin"}, err: true},
		{names: []string{"C:/save.dat"}, err: true},
		{names: []string{".DS_Store"}, err: true},
		{names: []string{"a.bin", "A.BIN"}, err: true},
	}
	for _, c := range cases {
		names, keep, err := emulatorSaveNames(c.names, c.folder)
		if c.err {
			if err == nil {
				t.Errorf("%v: no error", c.names)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: %v", c.names, err)
			continue
		}
		got := []string{}
		for i, name := range names {
			if keep[i] {
				got = append(got, name)
			}
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: %v, want %v", c.names, got, c.want)
		}
	}
	if _, _, err := emulatorSaveNames([]string{"Thumbs.db"}, false); !errors.Is(err, errNetSaveEmpty) {
		t.Errorf("only files of the system: %v", err)
	}
}

// emulatorUpload is a request of the page with the files of a save, each with its path.
func emulatorUpload(t *testing.T, c *client, query string, files map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		form.WriteField("path", name)
		part, _ := form.CreateFormFile("file", name[strings.LastIndex(name, "/")+1:])
		part.Write([]byte(files[name]))
	}
	form.Close()
	return sendBody(c, "POST", "/api/saves/0100000000010000"+query, body.Bytes(), "Content-Type", form.FormDataContentType())
}

// savedZipFiles reads the files of a ZIP.
func savedZipFiles(t *testing.T, data []byte) map[string]string {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, entry := range archive.File {
		reader, _ := entry.Open()
		content, _ := io.ReadAll(reader)
		reader.Close()
		files[entry.Name] = string(content)
	}
	return files
}

// A save of an emulator uploaded from the page is a copy of the game marked as one, named after
// the emulator, which comes back as a ZIP of its files; a ZIP sent is taken too, and the console
// lists it as any other.
func TestNetSaveFromAnEmulator(t *testing.T) {
	_, _, bob, console, auth, _, _ := pushWeb(t)
	got := emulatorUpload(t, bob, "?emulator=eden&note=before+the+boss&folder=1", map[string]string{
		"0100000000010000/save.dat": "game", "0100000000010000/sub/x.bin": "more", "0100000000010000/desktop.ini": "junk"})
	if got.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", got.Code, got.Body.String())
	}
	var answer struct{ Id string }
	json.Unmarshal(got.Body.Bytes(), &answer)
	saves := listSaves(t, bob, "/api/saves")
	if len(saves) != 1 || saves[0].Console != "Eden" || !saves[0].Emulator || saves[0].Note != "before the boss" || saves[0].Version != 0 {
		t.Fatalf("listed: %+v", saves)
	}
	download := bob.do("GET", "/api/saves/0100000000010000/"+answer.Id, nil)
	if files := savedZipFiles(t, download.Body.Bytes()); !reflect.DeepEqual(files, map[string]string{"save.dat": "game", "sub/x.bin": "more"}) {
		t.Fatalf("the ZIP: %v", files)
	}
	// the console lists it, and takes it back as the save of the game
	if listed := listSaves(t, console, "/install/saves?title=0100000000010000", "Authorization", auth); len(listed) != 1 || !listed[0].Emulator {
		t.Fatalf("the console: %+v", listed)
	}

	// one ZIP sent, with the folder of the game in it, of another emulator
	var zipped bytes.Buffer
	writer := zip.NewWriter(&zipped)
	entry, _ := writer.Create("0100000000010000/save.dat")
	entry.Write([]byte("from ryujinx"))
	writer.Close()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "save.zip")
	part.Write(zipped.Bytes())
	form.Close()
	got = sendBody(bob, "POST", "/api/saves/0100000000010000?emulator=ryujinx", body.Bytes(), "Content-Type", form.FormDataContentType())
	if got.Code != http.StatusOK {
		t.Fatalf("a ZIP: %d %s", got.Code, got.Body.String())
	}
	json.Unmarshal(got.Body.Bytes(), &answer)
	if files := savedZipFiles(t, bob.do("GET", "/api/saves/0100000000010000/"+answer.Id, nil).Body.Bytes()); !reflect.DeepEqual(files, map[string]string{"save.dat": "from ryujinx"}) {
		t.Fatalf("the ZIP sent: %v", files)
	}
	if saves := listSaves(t, bob, "/api/saves"); len(saves) != 2 || saves[0].Console != "Ryujinx" {
		t.Fatalf("listed: %+v", saves)
	}

	// what is not a save is refused, and a console cannot send one this way
	if got := emulatorUpload(t, bob, "", map[string]string{"../x": "no"}); got.Code != http.StatusBadRequest {
		t.Errorf("a path out of the save: %d", got.Code)
	}
	var none bytes.Buffer
	empty := multipart.NewWriter(&none)
	empty.Close()
	if got := sendBody(bob, "POST", "/api/saves/0100000000010000", none.Bytes(), "Content-Type", empty.FormDataContentType()); got.Code != http.StatusBadRequest {
		t.Errorf("no files: %d", got.Code)
	}
	if got := emulatorUploadAs(t, console, auth); got.Code == http.StatusOK {
		t.Errorf("a console sent a save of an emulator: %d", got.Code)
	}
}

// emulatorUploadAs sends a save of an emulator with the key of a console.
func emulatorUploadAs(t *testing.T, c *client, auth string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "save.dat")
	part.Write([]byte("x"))
	form.Close()
	return sendBody(c, "POST", "/api/saves/0100000000010000", body.Bytes(), "Content-Type", form.FormDataContentType(), "Authorization", auth)
}

// The page shows a save of an emulator marked as one, offers each save for an emulator, and offers
// to bring one only to a user who has saves.
func TestNetSavesPageWithAnEmulator(t *testing.T) {
	web, _, bob, console, auth, _, _ := pushWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleNetSavesPage()
	sendBody(console, "POST", "/install/saves/0100000000010000?note=At+the+castle&version=65536&account=Bob", saveZip(t, "a"), "Authorization", auth)
	emulatorUpload(t, bob, "?emulator=ryujinx&note=From+the+PC", map[string]string{"save.dat": "x"})
	body := bob.do("GET", "/saves", nil).Body.String()
	for _, want := range []string{"net-save-emulator", "Ryujinx", "From the PC", "v65536", "&mdash;", "data-net-save-emulator", `data-net-save-version="65536"`,
		`data-net-save-upload="0100000000010000"`, `id="netSaveUpload"`, `id="netSaveEmulator"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page has no %s", want)
		}
	}
	if dump := os.Getenv("SLM_SAVES_PAGE"); dump != "" {
		os.WriteFile(dump, []byte(body), 0o644)
	}
	// with no user, no saves and nothing to bring
	anyone := &client{t: t, handler: bob.handler}
	if other := anyone.do("GET", "/saves", nil).Body.String(); strings.Contains(other, `id="netSaveUpload"`) {
		t.Error("one with no saves is offered to bring one")
	}
}
