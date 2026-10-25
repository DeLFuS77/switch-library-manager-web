package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func compressWeb(t *testing.T) (*Web, string) {
	web, path, _ := compressWebWithLibrary(t)
	return web, path
}

// compressWebWithLibrary also returns a function that puts the test library back, as
// the rescan after a compression replaces it.
func compressWebWithLibrary(t *testing.T) (*Web, string, func()) {
	t.Helper()
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	if err := testnsp.WriteKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { settings.InitSwitchKeys(t.TempDir()) })

	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	web.localDbManager = manager

	folder := t.TempDir()
	path := filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	nsp, err := testnsp.WriteNsp(path)
	if err != nil {
		t.Fatal(err)
	}
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist: true,
				File: db.SwitchFileInfo{
					ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(path), BaseFolder: folder, Size: int64(len(nsp))},
					Metadata:     &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"},
				},
				Updates: map[int]db.SwitchFileInfo{},
				Dlc:     map[string]db.SwitchFileInfo{},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{},
	}
	web.state.set(nil, library)
	web.HandleCompress()
	// the rescan that follows a compression writes in the temporary folders: it ends before
	// they are removed
	t.Cleanup(func() { waitForBackgroundWork(web) })
	return web, path, func() { web.state.set(nil, library) }
}

func postForm(web *Web, path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, request)
	return recorder
}

func waitForCompression(t *testing.T, web *Web) Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for web.compressionRunning() {
		if time.Now().After(deadline) {
			t.Fatal("the compression did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == TASK_COMPRESS {
			return task
		}
	}
	t.Fatal("no compression task")
	return Task{}
}

func TestCompressPageAndRun(t *testing.T) {
	web, path := compressWeb(t)

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), filepath.Base(path)) {
		t.Fatalf("the NSP must be listed: %v", page.Code)
	}

	if code := postForm(web, "/compress/start", url.Values{"path": {"/etc/passwd"}}).Code; code != http.StatusBadRequest {
		t.Fatalf("files outside the library must be refused: %v", code)
	}

	started := postForm(web, "/compress/start", url.Values{"path": {path}, "level": {"fast"}, "delete_originals": {"true"}})
	if started.Code != http.StatusAccepted {
		t.Fatalf("start: %v %s", started.Code, started.Body.String())
	}
	task := waitForCompression(t, web)
	if task.Status != TASK_SUCCESS || task.Files != 1 || task.Saved <= 0 {
		t.Fatalf("task: %+v", task)
	}
	// each file is listed with its size before and after, and the share saved
	nsz, _ := os.Stat(switchfs.NszPath(path))
	if len(task.Results) != 1 || task.Results[0].Name != filepath.Base(path) || nsz == nil || task.Results[0].After != nsz.Size() ||
		task.Original != task.Results[0].Before || task.Saved != task.Results[0].Before-task.Results[0].After || task.SavedPercent() == "" {
		t.Fatalf("the result of the file: %+v", task)
	}
	web.HandleTasks()
	history := httptest.NewRecorder()
	web.router.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/tasks.html", nil))
	if !strings.Contains(history.Body.String(), "task-results") || !strings.Contains(history.Body.String(), task.SavedPercent()) || !strings.Contains(history.Body.String(), "saving-saved") {
		t.Fatal("the tasks page shows the files compressed and the share saved")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the original must be deleted after the verification")
	}
	if _, err := os.Stat(switchfs.NszPath(path)); err != nil {
		t.Fatal("the NSZ must exist")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("no temporary file may be left")
		}
	}
}

func TestCompressKeepsOriginalsWhenAsked(t *testing.T) {
	web, path, restore := compressWebWithLibrary(t)
	if code := postForm(web, "/compress/start", url.Values{"path": {path}}).Code; code != http.StatusAccepted {
		t.Fatalf("start: %v", code)
	}
	if task := waitForCompression(t, web); task.Status != TASK_SUCCESS {
		t.Fatalf("task: %+v", task)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the original must be kept")
	}
	// wait for the rescan that follows the compression
	for deadline := time.Now().Add(10 * time.Second); web.state.IsSynchronizing() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	restore()
	// as the rescan found it: the compressed copy is a duplicate of the original
	_, library := web.state.get()
	compressed := switchfs.CompressedPath(path)
	info, err := os.Stat(compressed)
	if err != nil {
		t.Fatal(err)
	}
	library.Skipped[db.ExtendedFileInfo{FileName: filepath.Base(compressed), BaseFolder: filepath.Dir(compressed), Size: info.Size()}] = db.SkippedFile{ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + path + ")"}
	web.invalidateDerived()
	// a file with a compressed copy is not offered again
	if code := postForm(web, "/compress/start", url.Values{"path": {path}}).Code; code != http.StatusBadRequest {
		t.Fatalf("a file that already has an NSZ must not be compressed again: %v", code)
	}
	if len(web.uncompressedCandidates()) != 0 || len(web.compressCandidates()) != 1 {
		t.Fatal("the compressed original is only listed on the Space page")
	}
}

func TestDamagedFilesAreNotCompressed(t *testing.T) {
	web, path := compressWeb(t)
	data, _ := os.ReadFile(path)
	data[len(data)-100] ^= 0xFF
	os.WriteFile(path, data, 0o644)

	postForm(web, "/compress/start", url.Values{"path": {path}, "delete_originals": {"true"}})
	task := waitForCompression(t, web)
	if task.Status != TASK_FAILED || len(task.Warnings) != 1 || task.Warnings[0].Text != NOTE_COMPRESS_DAMAGED {
		t.Fatalf("a damaged file must be reported: %+v", task)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a damaged original must be kept")
	}
	// it is remembered as damaged, like a check finds it: told in Issues and not offered to
	// compress again while it does not change (it failed again on every run)
	if web.verifications().damaged()[path] == "" {
		t.Fatal("the damage is remembered")
	}
	if len(web.compressCandidates()) != 0 {
		t.Fatal("a damaged file is not offered to compress again")
	}
	web.HandleCompress()
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
	if !strings.Contains(page.Body.String(), "/issues.html?kind=damaged") {
		t.Fatal("the Compress page tells the damaged files are not listed")
	}
	// a new copy of the file is offered again
	data[len(data)-100] ^= 0xFF
	os.WriteFile(path, data, 0o644)
	os.Chtimes(path, time.Now(), time.Now().Add(time.Minute))
	web.invalidateDerived()
	if len(web.compressCandidates()) != 1 {
		t.Fatal("a file replaced since is offered again")
	}
}

// A file cut short is damaged too: its compression failed with "read … bytes: EOF" and it was
// offered again every time.
func TestCompressDamageIncludesFilesCutShort(t *testing.T) {
	if !compressDamage(fmt.Errorf("x.nca: read 103872 of 114688 bytes: %w", io.EOF)) || !compressDamage(errors.New("x.nca does not match its content ID: the file is damaged or was modified")) {
		t.Fatal("damage")
	}
	if compressDamage(errCompressSpace) || compressDamage(context.Canceled) {
		t.Fatal("not damage")
	}
	if compressNote(TASK_COMPRESS, fmt.Errorf("read 1 of 2 bytes: %w", io.EOF)) != NOTE_COMPRESS_DAMAGED {
		t.Fatal("the note of a file cut short")
	}
}

func TestCompressNotesAreTranslated(t *testing.T) {
	for _, text := range compressNoteTexts {
		if _, ok := translations["es"][text]; !ok {
			t.Errorf("no Spanish translation for %q", text)
		}
	}
}

func TestDecompressFromThePage(t *testing.T) {
	web, path := compressWeb(t)
	original, _ := os.ReadFile(path)
	nsz := switchfs.CompressedPath(path)
	if _, err := switchfs.CompressGame(context.Background(), path, nsz, switchfs.CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	info, _ := os.Stat(nsz)
	web.state.set(nil, &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist: true,
				File: db.SwitchFileInfo{
					ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(nsz), BaseFolder: filepath.Dir(nsz), Size: info.Size()},
					Metadata:     &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"},
				},
				Updates: map[int]db.SwitchFileInfo{},
				Dlc:     map[string]db.SwitchFileInfo{},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{},
	})

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
	if !strings.Contains(page.Body.String(), "decompressForm") || !strings.Contains(page.Body.String(), `data-file-list="/compress/nsz-list"`) {
		t.Fatal("the Decompress section must be shown")
	}
	// the NSZ files are loaded when the section is opened, filtered on the server
	list := httptest.NewRecorder()
	web.router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/compress/nsz-list?q=game", nil))
	if !strings.Contains(list.Body.String(), filepath.Base(nsz)) {
		t.Fatalf("the NSZ must be offered for decompression: %s", list.Body.String())
	}
	none := httptest.NewRecorder()
	web.router.ServeHTTP(none, httptest.NewRequest(http.MethodGet, "/compress/nsz-list?q=nothing-like-this", nil))
	if strings.Contains(none.Body.String(), filepath.Base(nsz)) {
		t.Fatal("the filter is applied on the server")
	}

	if code := postForm(web, "/decompress/start", url.Values{"path": {nsz}, "delete_compressed": {"true"}}).Code; code != http.StatusAccepted {
		t.Fatalf("start: %v", code)
	}
	deadline := time.Now().Add(20 * time.Second)
	for web.compressionRunning() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	var task Task
	for _, candidate := range web.taskLog().Snapshot() {
		if candidate.Kind == TASK_DECOMPRESS {
			task = candidate
			break
		}
	}
	if task.Status != TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("task: %+v", task)
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, original) {
		t.Fatal("the NSP must be restored byte for byte")
	}
	if _, err := os.Stat(nsz); !os.IsNotExist(err) {
		t.Fatal("the NSZ must be deleted after the check")
	}
}

func TestConvertGameCardToNsp(t *testing.T) {
	web, path, _ := compressWebWithLibrary(t)
	folder := filepath.Dir(path)
	xci := filepath.Join(folder, "Card [0100000000020000][v0].xci")
	data, err := testnsp.WriteXci(xci)
	if err != nil {
		t.Fatal(err)
	}
	_, library := web.state.get()
	library.TitlesMap["0100000000020"] = &db.SwitchGameFiles{
		BaseExist: true,
		File:      db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(xci), BaseFolder: folder, Size: int64(len(data))}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000020000"}},
		Updates:   map[int]db.SwitchFileInfo{},
		Dlc:       map[string]db.SwitchFileInfo{},
	}
	web.invalidateDerived()

	if response := postForm(web, "/convert/start", url.Values{"path": {path}}); response.Code != http.StatusBadRequest {
		t.Fatalf("only game cards are converted: %d", response.Code)
	}
	if response := postForm(web, "/convert/start", url.Values{"path": {xci}, "delete_originals": {"true"}}); response.Code != http.StatusAccepted {
		t.Fatalf("convert: %d %s", response.Code, response.Body.String())
	}
	task := waitForTask(t, web, TASK_CONVERT)
	if task.Status != TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("one card converted: %+v", task)
	}
	if _, err := os.Stat(strings.TrimSuffix(xci, ".xci") + ".nsp"); err != nil {
		t.Fatal("the NSP must exist")
	}
	if _, err := os.Stat(xci); !os.IsNotExist(err) {
		t.Fatal("the checked card is deleted when asked")
	}
}

// Each file of a compression tells what is done to it: its compression (with its hashes taken
// meanwhile), then the check of the copy.
func TestCompressionLanesTellTheirPhase(t *testing.T) {
	web, path := compressWeb(t)
	info, _ := os.Stat(path)
	phases := []string{}
	web.runFileTask(TASK_COMPRESS, []string{path}, 2, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		for step := int64(0); step < 2; step++ {
			report(step*info.Size())(1, info.Size())
			for _, task := range web.taskLog().Snapshot() {
				if task.Running() && len(task.Lanes) == 1 {
					lane := task.Lanes[0]
					phases = append(phases, fmt.Sprintf("%s %d/%d", lane.Phase, lane.Step, lane.Steps))
					// the file reached is told apart from its name, which stays as it is
					if task.FileNumber != 1 || task.FileCount != 1 || task.Message != filepath.Base(path) {
						t.Errorf("the counter of the files: %d of %d, %q", task.FileNumber, task.FileCount, task.Message)
					}
				}
			}
		}
		return 0, nil
	})
	waitForCompression(t, web)
	if strings.Join(phases, ", ") != PHASE_COMPRESSING+" 1/2, "+PHASE_VERIFYING+" 2/2" {
		t.Fatalf("phases: %v", phases)
	}
}

// With several files at once, each one has its line and the results add up.
func TestSeveralFilesAtOnce(t *testing.T) {
	web, _ := compressWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CompressFilesAtOnce = 2 })
	folder := t.TempDir()
	paths := []string{}
	for _, name := range []string{"a.nsp", "b.nsp", "c.nsp"} {
		path := filepath.Join(folder, name)
		os.WriteFile(path, make([]byte, 1000), 0o644)
		paths = append(paths, path)
	}
	var mutex sync.Mutex
	running, most, lanes := 0, 0, 0
	both := make(chan struct{})
	var once sync.Once
	started := web.runFileTask(TASK_COMPRESS, paths, 3, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		mutex.Lock()
		running++
		most = max(most, running)
		if running == 2 {
			once.Do(func() { close(both) })
		}
		mutex.Unlock()
		select {
		case <-both:
		case <-time.After(5 * time.Second):
		}
		report(1000)(500, 1000)
		for _, task := range web.taskLog().Snapshot() {
			if task.Running() {
				mutex.Lock()
				lanes = max(lanes, len(task.Lanes))
				mutex.Unlock()
			}
		}
		mutex.Lock()
		running--
		mutex.Unlock()
		return 100, nil
	})
	if !started {
		t.Fatal("the task must start")
	}
	task := waitForCompression(t, web)
	if most != 2 || lanes != 2 {
		t.Fatalf("two files at once, each with its line: %d at once, %d lines", most, lanes)
	}
	if task.Status != TASK_SUCCESS || task.Files != 3 || task.Saved != 300 || len(task.Results) != 3 || task.Workers != 2 {
		t.Fatalf("the results of the three files: %+v", task)
	}
}

// Files written at the same time each need their space: the second one is refused when
// the space left is taken by the first.
func TestSpaceIsReservedForFilesWrittenAtOnce(t *testing.T) {
	web := newTestWeb(t)
	free, err := diskFree(web.dataFolder)
	if err != nil {
		t.Skip("no free space on this system")
	}
	half := int64(free/2) + 1
	release, err := web.reserveSpace(web.dataFolder, half)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := web.reserveSpace(web.dataFolder, half); !errors.Is(err, errCompressSpace) {
		t.Fatalf("the second file does not fit: %v", err)
	}
	release()
	second, err := web.reserveSpace(web.dataFolder, half)
	if err != nil {
		t.Fatal("given back, the space is free again")
	}
	second()
}

// A compression reads its original whole: the original and the NSZ count as checked, and the
// next file check does not read them again.
func TestCompressionCountsAsACheck(t *testing.T) {
	web, path := compressWeb(t)
	if !web.startCompression([]string{path}, "fast", false) {
		t.Fatal("the compression must start")
	}
	if task := waitForCompression(t, web); task.Status != TASK_SUCCESS {
		t.Fatalf("task: %+v", task)
	}
	for _, file := range []string{path, switchfs.NszPath(path)} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if record, ok := web.verifications().current(file, info); !ok || !record.sound() {
			t.Fatalf("%s counts as checked", filepath.Base(file))
		}
	}
}

// A pause holds a compression in the middle of a file until it is resumed, and a cancel still
// stops it while paused.
func TestCompressionPause(t *testing.T) {
	web, path := compressWeb(t)
	reached := make(chan int64, 10)
	// the job waits at the gate after its first report, so the pause comes before the next one
	gate := make(chan struct{})
	started := web.runFileTask(TASK_COMPRESS, []string{path}, 1, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		progress := report(0)
		for done := int64(1); done <= 3; done++ {
			progress(done, 3)
			reached <- done
			if done == 1 {
				<-gate
			}
		}
		return 0, ctx.Err()
	})
	if !started {
		t.Fatal("the compression must start")
	}
	// the first report goes through before the pause
	<-reached
	if !web.pauseCompression(true) {
		t.Fatal("a running compression can be paused")
	}
	if web.pauseCompression(true) {
		t.Fatal("a paused compression is not paused twice")
	}
	close(gate)
	paused := false
	for _, task := range web.taskLog().Snapshot() {
		if task.Running() && task.Paused {
			paused = true
		}
	}
	if !paused {
		t.Fatal("the task says it is paused")
	}
	select {
	case done := <-reached:
		t.Fatalf("the work went on while paused: %d", done)
	case <-time.After(300 * time.Millisecond):
	}
	web.pauseCompression(false)
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the work goes on once resumed")
	}
	<-reached
	waitForCompression(t, web)

	// cancelled while paused: the work ends
	web.runFileTask(TASK_COMPRESS, []string{path}, 1, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		progress := report(0)
		reached <- 0
		progress(1, 2)
		return 0, ctx.Err()
	})
	<-reached
	web.pauseCompression(true)
	web.cancelCompression()
	waitForCompression(t, web)
	task := waitForTask(t, web, TASK_COMPRESS)
	if task.Paused {
		t.Fatal("a task that ended is no longer paused")
	}
}

// The files of a compression go by name, not the biggest first, and their sizes follow them.
func TestSortedByName(t *testing.T) {
	paths := []string{"/b/Zelda.nsp", "/a/mario.nsp", "/c/Animal.nsp", "/a/Mario.nsp"}
	sizes := []int64{30, 10, 20, 11}
	gotPaths, gotSizes := sortedByName(paths, sizes)
	want := []string{"/c/Animal.nsp", "/a/Mario.nsp", "/a/mario.nsp", "/b/Zelda.nsp"}
	wantSizes := []int64{20, 11, 10, 30}
	for i := range want {
		if gotPaths[i] != want[i] || gotSizes[i] != wantSizes[i] {
			t.Fatalf("by name: %v %v", gotPaths, gotSizes)
		}
	}
	if paths[0] != "/b/Zelda.nsp" {
		t.Fatal("the list given is left as it was")
	}
	if got, sizes := sortedByName([]string{"b.nsp", "a.nsp"}, nil); got[0] != "a.nsp" || sizes != nil {
		t.Fatalf("without sizes: %v %v", got, sizes)
	}
}

// The bar of a file reaches its end: each step fills its share, the size of the file, though
// the check of the NSZ counts the files inside it, fewer bytes than the original.
func TestCompressionBarReachesItsEnd(t *testing.T) {
	web, path := compressWeb(t)
	info, _ := os.Stat(path)
	var mutex sync.Mutex
	reached := map[int64]int64{}
	web.runFileTask(TASK_COMPRESS, []string{path}, 2, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return web.compressFile(ctx, path, switchfs.LevelFast, false, func(step int64) func(int64, int64) {
			progress := report(step)
			return func(done int64, total int64) {
				mutex.Lock()
				reached[step] = max(reached[step], done)
				mutex.Unlock()
				progress(done, total)
			}
		})
	})
	waitForCompression(t, web)
	mutex.Lock()
	defer mutex.Unlock()
	for _, step := range []int64{0, info.Size()} {
		if reached[step] != info.Size() {
			t.Fatalf("the step from %d reached %d of %d", step, reached[step], info.Size())
		}
	}
}

// The Compress page starts at the level chosen in Settings, which Measure this server measures,
// and at Balanced when none is chosen.
func TestCompressPageStartsAtTheLevelOfSettings(t *testing.T) {
	web, _ := compressWeb(t)
	checked := func() string {
		page := httptest.NewRecorder()
		web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
		found := regexp.MustCompile(`name="level" id="level\w+" value="(\w+)" checked`).FindAllStringSubmatch(page.Body.String(), -1)
		if len(found) != 1 {
			t.Fatalf("one level is chosen: %v", found)
		}
		return found[0][1]
	}
	if level := checked(); level != "balanced" {
		t.Fatalf("none chosen in Settings: %q", level)
	}
	for _, level := range []string{"max", "fast", "balanced"} {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.AutoCompressLevel = level })
		if got := checked(); got != level {
			t.Fatalf("%q in Settings: %q", level, got)
		}
	}
}
