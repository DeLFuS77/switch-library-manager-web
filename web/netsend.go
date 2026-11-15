package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/diskfree"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Send to console, Compressed (experimental): the files go in the experimental format of NXPress
// (NCZSECT2, NCZSECT3), which compresses more than a plain NSZ, so less goes over the network;
// NXDock reads it and decompresses it while it installs. Send to console is only for NXDock.
//
//   - A file of the library already in that format goes as it is, at once (as the catalog gives
//     it with ?ncz=native).
//   - An NSP is compressed first, as a task (Compress to send), into a folder of the data folder:
//     never next to the library, and nothing of the library is deleted. Once the copy is checked,
//     what was sent with it goes to the console, which downloads it from /install/c/.
//   - An NSZ without experimental content, an XCI or an XCZ goes as Normal: it is compressed
//     already, or a game card.
//
// The pushes wait in memory, as the others do: after a restart nothing waits for a copy, so the
// copies are not made again then. A copy is deleted when the console declines it, and once
// nothing has used it for 24 hours (the life of a push).

const (
	NET_PREPARE_ACTION        = "Compress to send"
	NOTE_NET_PREPARE_FAILED   = "The game could not be compressed to send: send it as Normal."
	NOTE_NET_PREPARE_UNWANTED = "No console waits for it any more: send it again."
	NOTE_NET_SEND_SPACE       = "Not enough free space in the data folder to compress it first: send it as Normal."

	// the folder of the data folder with the copies made to send
	netSendFolder        = "net-send"
	netInstallCompressed = NET_INSTALL_PREFIX + "c/"
)

var (
	netFileIdPattern   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	errNetSendUnwanted = errors.New("no console waits for it any more")
	errNetSendSpace    = errors.New(NOTE_NET_SEND_SPACE)
)

// how a file goes with Compressed
const (
	netSendNative  = "native"  // in the experimental format already: as it is
	netSendPrepare = "prepare" // an NSP, compressed first
	netSendAsIs    = "as-is"   // compressed already, or a game card: as Normal
)

// netSendModeOf is how a file of the library goes with Compressed.
func netSendModeOf(file netFile) string {
	if file.stream != nil {
		return netSendNative
	}
	if strings.EqualFold(filepath.Ext(file.path), ".nsp") {
		return netSendPrepare
	}
	return netSendAsIs
}

// netNativeOf is a file in the experimental format as it is on the disk: its size and the format
// of its extension. ok is false for any other file, or one no longer there.
func netNativeOf(file netFile) (size int64, format string, ok bool) {
	if file.stream == nil {
		return 0, "", false
	}
	stat, err := os.Stat(file.path)
	if err != nil {
		return 0, "", false
	}
	return stat.Size(), strings.TrimPrefix(strings.ToLower(filepath.Ext(file.path)), "."), true
}

func (web *Web) netSendFolder() string {
	return filepath.Join(web.dataFolder, netSendFolder)
}

// netSendCopy is where the copy of a file of the library made to send goes.
func (web *Web) netSendCopy(id string) string {
	return filepath.Join(web.netSendFolder(), id+".nsz")
}

// netSendFresh is the copy of a file made to send, when there is one written after the file.
func (web *Web) netSendFresh(file netFile) (string, os.FileInfo, bool) {
	source, err := os.Stat(file.path)
	if err != nil {
		return "", nil, false
	}
	target := web.netSendCopy(file.id)
	copied, err := os.Stat(target)
	if err != nil || copied.Size() == 0 || !copied.ModTime().After(source.ModTime()) {
		return "", nil, false
	}
	return target, copied, true
}

// netCopyWaits are the pushes that wait for the copy of their file, by the id of the file.
type netCopyWaits struct {
	mutex   sync.Mutex
	waiting map[string][]*netPush
	// the file each copy is made of, and the files a task is to compress (queued or running)
	sources   map[string]string
	preparing map[string]bool
}

// wait keeps pushes until the copy of their file is made, and gives the files no task is to
// compress yet.
func (s *netCopyWaits) wait(pushes []*netPush, sources map[string]string) []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.waiting == nil {
		s.waiting, s.sources, s.preparing = map[string][]*netPush{}, map[string]string{}, map[string]bool{}
	}
	s.forgetOld(time.Now())
	paths := []string{}
	for _, push := range pushes {
		s.waiting[push.fileId] = append(s.waiting[push.fileId], push)
		if !s.preparing[push.fileId] {
			s.preparing[push.fileId] = true
			s.sources[push.fileId] = sources[push.fileId]
			paths = append(paths, sources[push.fileId])
		}
	}
	return paths
}

// forgetOld drops what waited too long, as the pushes do. With the mutex held.
func (s *netCopyWaits) forgetOld(now time.Time) {
	for id, list := range s.waiting {
		kept := list[:0]
		for _, push := range list {
			if now.Sub(push.made) < netPushLife {
				kept = append(kept, push)
			}
		}
		if len(kept) == 0 {
			delete(s.waiting, id)
		} else {
			s.waiting[id] = kept
		}
	}
}

// wanted tells whether something sent waits for the copy of a file.
func (s *netCopyWaits) wanted(id string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.forgetOld(time.Now())
	return len(s.waiting[id]) > 0
}

// take gives what waits for the copy of a file, which no longer waits.
func (s *netCopyWaits) take(id string) []*netPush {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.forgetOld(time.Now())
	list := s.waiting[id]
	delete(s.waiting, id)
	delete(s.preparing, id)
	delete(s.sources, id)
	return list
}

// dropSources forgets what waits for the copies of the files at paths, where they are now.
func (s *netCopyWaits) dropSources(paths []string, resolve func(string) string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for id, source := range s.sources {
		for _, path := range paths {
			if samePath(resolve(source), resolve(path)) {
				delete(s.waiting, id)
				delete(s.preparing, id)
				delete(s.sources, id)
				break
			}
		}
	}
}

// count is what waits for a copy to go to a console.
func (s *netCopyWaits) count(key string) int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	count := 0
	for _, list := range s.waiting {
		for _, push := range list {
			if push.keyId == key {
				count++
			}
		}
	}
	return count
}

// inUse are the files whose copies something waits for or a task makes.
func (s *netCopyWaits) inUse(used map[string]bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for id := range s.waiting {
		used[id] = true
	}
	for id := range s.preparing {
		used[id] = true
	}
}

// copies are the copies the pushes waiting or given use, by the id of their file.
func (q *netPushQueue) copies(used map[string]bool) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	for _, list := range q.pending {
		for _, push := range list {
			if push.copy != "" {
				used[push.fileId] = true
			}
		}
	}
	for _, push := range q.delivered {
		if push.copy != "" {
			used[push.fileId] = true
		}
	}
}

// netPrepareEnded forgets what still waits for the copies of the files at paths: their task ended
// without them (cancelled, or taken out of the queue).
func (web *Web) netPrepareEnded(paths []string) {
	web.netCopyWaits.dropSources(paths, web.moves.resolve)
}

// netSendPrepare keeps the pushes of files to compress first until their copies are made, and
// starts the task that makes them, or queues it.
func (web *Web) netSendPrepare(pushes []*netPush, sources map[string]string) {
	paths := web.netCopyWaits.wait(pushes, sources)
	if len(paths) == 0 {
		return
	}
	names := map[string]string{}
	for id, path := range sources {
		names[path] = id
	}
	task := QueuedTask{Kind: tasklog.TASK_NET_PREPARE, Paths: paths, Names: map[string]string{}}
	for _, path := range paths {
		task.Names[path] = names[path]
	}
	web.enqueue(task)
}

// netSendRoom tells whether the copies of files of that size fit in the data folder, with the room
// the app keeps free.
func (web *Web) netSendRoom(size int64) error {
	if size == 0 {
		return nil
	}
	folder := web.netSendFolder()
	if err := os.MkdirAll(folder, 0755); err != nil {
		return err
	}
	if free, err := diskfree.Free(folder); err == nil && int64(free) < size+(64<<20) {
		return errNetSendSpace
	}
	return nil
}

// startNetPrepare compresses the files at paths to send them, one after the other; names gives
// the id of each file as it was sent.
func (web *Web) startNetPrepare(paths []string, names map[string]string, trigger string) bool {
	names = web.moves.resolveKeys(names)
	// every file is read twice, as in a compression: compressed (and hashed meanwhile), then
	// verified. Nothing goes on after a restart: what was sent waits in memory only
	return web.runFileTaskFull(tasklog.TASK_NET_PREPARE, trigger, paths, nil, 2, nil, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		id := names[path]
		if id == "" {
			id = netFileId(path)
		}
		if !web.netCopyWaits.wanted(id) {
			web.netCopyWaits.take(id)
			return 0, errNetSendUnwanted
		}
		target, err := web.makeNetCopy(ctx, path, id, report)
		if errors.Is(err, switchfs.ErrNotCompressible) {
			// nothing in it compresses: it goes as Normal
			target, err = "", nil
		}
		pushes := web.netCopyWaits.take(id)
		if err != nil {
			return 0, err
		}
		for _, push := range pushes {
			push.copy = target
		}
		if err := web.netPushes.add(pushes); err != nil {
			web.sugarLogger.Warnf("Network install: %s was compressed to send, but the console has too many installs waiting", path)
		}
		return 0, nil
	})
}

// makeNetCopy writes the copy of an NSP to send in the experimental format of NXPress, into the
// data folder: under a hidden name, checked whole, and only then given its name. A copy written
// after the NSP is used as it is.
func (web *Web) makeNetCopy(ctx context.Context, path string, id string, report func(step int64) func(int64, int64)) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	folder := web.netSendFolder()
	if err := os.MkdirAll(folder, 0755); err != nil {
		return "", err
	}
	target := web.netSendCopy(id)
	if copied, err := os.Stat(target); err == nil && copied.Size() > 0 && copied.ModTime().After(info.ModTime()) {
		return target, nil
	}
	release, err := web.reserveSpace(folder, info.Size())
	if err != nil {
		return "", err
	}
	defer release()

	appSettings := settings.ReadSettings(web.dataFolder)
	level := settingsLevel(appSettings)
	temporary := filepath.Join(folder, "."+filepath.Base(target)+".tmp")
	defer os.Remove(temporary)
	// the NCA in sparse storage compressed too, and the LZ4 of the compressed storage made again
	// when it is installed: what NXDock reads and the installers do not
	result, err := switchfs.CompressGame(ctx, path, temporary, switchfs.CompressOptions{
		Level: level, Workers: switchfs.LimitThreads(level, compressThreads(appSettings.CompressThreads, 1), 1),
		Progress: filling(info.Size(), report(0)), Sparse: true, Rebuild: true,
	})
	if err != nil {
		return "", readFailure(path, info, err)
	}
	originalCheck, err := switchfs.CheckHashes(ctx, path, result.Hashes)
	if err != nil {
		return "", readFailure(path, info, err)
	}
	web.verifications().remember(path, originalCheck)
	if err := switchfs.VerifyCompressed(ctx, temporary, result.Hashes, filling(info.Size(), report(info.Size()))); err != nil {
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", &notContentError{err}
	}
	if err := durable.CommitFile(temporary, target); err != nil {
		return "", err
	}
	web.sugarLogger.Infof("Compressed %s to send: %d -> %d bytes", path, result.InputSize, result.OutputSize)
	return target, nil
}

// netPrepareNote is the note of a file that could not be compressed to send.
func netPrepareNote(err error) string {
	switch {
	case errors.Is(err, errCompressSpace):
		return NOTE_NET_SEND_SPACE
	case errors.Is(err, errNetSendUnwanted):
		return NOTE_NET_PREPARE_UNWANTED
	case errors.Is(err, switchfs.ErrNoTitleKey):
		return NOTE_COMPRESS_NO_KEY
	}
	return NOTE_NET_PREPARE_FAILED
}

// netSendDiscard deletes the copy of a file made to send, unless something still uses it.
func (web *Web) netSendDiscard(id string) {
	used := map[string]bool{}
	web.netPushes.copies(used)
	web.netCopyWaits.inUse(used)
	if !used[id] {
		os.Remove(web.netSendCopy(id))
	}
}

// netCopySweep deletes the copies nothing has used for 24 hours; at the start, also what a task
// cut by a stop left half written, and the tasks of the queue that were to compress for pushes,
// which no longer wait.
func (web *Web) netCopySweep(starting bool) {
	if starting {
		ids := map[int64]bool{}
		for _, item := range web.queue().list() {
			if item.Kind == tasklog.TASK_NET_PREPARE {
				ids[item.Id] = true
			}
		}
		if len(ids) > 0 {
			web.queue().remove(ids)
		}
	}
	entries, err := os.ReadDir(web.netSendFolder())
	if err != nil {
		return
	}
	used := map[string]bool{}
	web.netPushes.copies(used)
	web.netCopyWaits.inUse(used)
	now := time.Now()
	for _, entry := range entries {
		name := entry.Name()
		full := filepath.Join(web.netSendFolder(), name)
		if strings.HasPrefix(name, ".") {
			if starting {
				os.Remove(full)
			}
			continue
		}
		info, err := entry.Info()
		if err != nil || entry.IsDir() || used[strings.TrimSuffix(name, ".nsz")] {
			continue
		}
		if now.Sub(info.ModTime()) >= netPushLife {
			os.Remove(full)
		}
	}
}

// netPushItemFor is a file sent as the console gets it: with Compressed, in the experimental
// format as it is, or the copy made of it.
func (web *Web) netPushItemFor(push *netPush, file netFile) netPushItem {
	item := netPushItemOf(file)
	switch {
	case push.native:
		if size, format, ok := netNativeOf(file); ok {
			item.Size, item.Format, item.Url = size, format, item.Url+"?raw=1"
		}
	case push.copy != "":
		stat, err := os.Stat(push.copy)
		if err != nil {
			// deleted since: it goes as Normal
			web.sugarLogger.Warnf("Network install: the copy of %s made to send is gone, it is sent as Normal", file.name)
			break
		}
		name := strings.TrimSuffix(file.name, filepath.Ext(file.name)) + ".nsz"
		item.Url = netInstallCompressed + file.id + "/" + url.PathEscape(name)
		item.Size, item.Format = stat.Size(), "nsz"
		if item.Name == file.name {
			item.Name = name
		}
	}
	return item
}

// handleNetCompressed gives the copy of a file of the library made to send, with range requests;
// only of a file still in the library, behind the same gate as the rest of /install/.
func (web *Web) handleNetCompressed(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, netInstallCompressed), "/")
	if !netFileIdPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	_, byId := web.netFilesKept(options)
	file, found := byId[id]
	if !found {
		http.NotFound(w, r)
		return
	}
	source, err := os.Open(web.netSendCopy(id))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// a download over the Wi-Fi of the console takes long
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Method == http.MethodGet && (r.Header.Get("Range") == "" || strings.HasPrefix(r.Header.Get("Range"), "bytes=0-")) {
		web.noteNetInstall(r, file)
	}
	// what is sent is counted, for the Tasks page, by the size of the copy
	counted := file
	counted.size = info.Size()
	w = web.netSendCounted(w, r, counted)
	http.ServeContent(w, r, "", info.ModTime(), source)
}
