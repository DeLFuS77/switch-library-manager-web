package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/gift"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/ttx"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/gorilla/mux"
)

// Send to nx2-flow: a NUP file of the library, with the cover of its game, goes to a Switch 2
// running nx2-flow, whose Transfer screen receives files over the local network (the TTX1
// protocol, see internal/ttx). The user types the address of the console, which is remembered
// for them; the app connects to it, sends the file into the folder of the games of nx2-flow and
// the cover into the folder of its covers, named by the title ID, and reads the file back to
// check it. Each send is a task of Tasks, which can be cancelled.
//
// Only users who may send a game to a console (see netPairer), never in the demo, and only to an
// address of the local network: the app is not made to connect anywhere else.

const (
	// the paths of the web
	nx2flowGamePath   = "/api/nx2flow/game/"
	nx2flowSendPath   = "/api/nx2flow/send"
	nx2flowCancelPath = "/api/nx2flow/cancel/"

	// the folders of the card nx2-flow reads, and its settings
	nx2flowGamesFolder  = "/nx2-flow/games"
	nx2flowCoversFolder = "/nx2-flow/covers"
	nx2flowSettingsFile = "/nx2-flow/menu.cfg"

	// the kind of the task
	TASK_NX2FLOW = tasklog.TASK_NX2FLOW
	// what the activity notes
	NX2FLOW_ACTION = "Send to nx2-flow"

	// the file of the data folder with the address each user sends to
	NX2FLOW_FILENAME = "nx2flow.json"

	// the front cover nx2-flow shows, in pixels
	nx2flowCoverWidth  = 500
	nx2flowCoverHeight = 813
	// the largest image read to make it from
	nx2flowCoverSource = 16 << 20

	// how long the console has to answer when the send starts
	nx2flowDialTimeout = 5 * time.Second
	// sends at the same time at most, of all users
	nx2flowMaxRunning = 4
	// room left on the card besides the file: the cover and what the card needs
	nx2flowSpareRoom = 4 << 20
)

// the messages a send is refused with
const (
	errNx2flowNotAllowed = errorString("Only a user who may download sends a game to a console.")
	errNx2flowNoFile     = errorString("This game has no NUP file in the library to send.")
	errNx2flowGone       = errorString("The NUP file is no longer in the library.")
	errNx2flowAddress    = errorString("Type the address of the console, as the Transfer screen of nx2-flow shows it (for example 192.168.1.50).")
	errNx2flowNotLocal   = errorString("Only an address of the local network: the one the Transfer screen of nx2-flow shows.")
	errNx2flowReach      = errorString("The console does not answer at that address. Open the Transfer screen of nx2-flow on it (ZL) and check the address.")
	errNx2flowBusy       = errorString("Something is already being sent to this console: wait for it to end, or cancel it in Tasks.")
	errNx2flowTooMany    = errorString("Too many sends at the same time: wait for one to end.")
	errNx2flowNoRoom     = errorString("The card of the console does not have room for this game.")
	errNx2flowNotFound   = errorString("That send is not going on.")
)

// the notes of the task
const (
	NOTE_NX2FLOW_FAILED   = "The game could not be sent to nx2-flow."
	NOTE_NX2FLOW_CHECK    = "The file on the card is not the one sent: send it again."
	NOTE_NX2FLOW_NO_COVER = "The game was sent without a cover: there is no cover of it to send."
	NOTE_NX2FLOW_COVER    = "The game was sent, its cover could not be."
)

// the progress messages of the task
const (
	nx2flowSending  = "Sending %v"
	nx2flowChecking = "Checking %v on the console"
	nx2flowCover    = "Sending the cover"
)

// nx2flowFile is a NUP file of a game that can be sent.
type nx2flowFile struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	path string
	// the title ID the cover is named by, lower case
	titleId string
}

// nx2flowRun is a send going on.
type nx2flowRun struct {
	task    int64
	user    string
	address string
	game    string
	file    string
	started time.Time
	cancel  context.CancelFunc
}

// nx2flowSends are the sends going on, and the addresses the users send to.
type nx2flowSends struct {
	mutex   sync.Mutex
	running map[int64]*nx2flowRun
	// by user, loaded the first time
	addresses map[string]string
	loaded    bool
}

// nx2flowRequest is a send asked for: the game (its title ID), which of its NUP files (its id,
// or the first one), the address typed and whether its cover goes too.
type nx2flowRequest struct {
	User    string
	Ip      string
	TitleId string
	FileId  string
	Address string
	Cover   bool
}

// nx2flowRefusal is a send that did not start: the status of the answer and why.
type nx2flowRefusal struct {
	status int
	err    error
}

func (r *nx2flowRefusal) Error() string { return r.err.Error() }
func (r *nx2flowRefusal) Unwrap() error { return r.err }

func refuseNx2flow(status int, err error) error {
	return &nx2flowRefusal{status: status, err: err}
}

// nx2flowAddress reads the address a user typed: an IP address or a name of the local network,
// with or without a port (28302 when none). It gives the address to connect to, with the IP it
// stands for, and the address to remember, as typed but cleaned.
func nx2flowAddress(ctx context.Context, typed string) (string, string, error) {
	typed = strings.TrimSpace(typed)
	typed = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(typed, "http://"), "tcp://"), "/")
	if typed == "" || len(typed) > 255 || strings.ContainsAny(typed, " /\\@?#") {
		return "", "", errNx2flowAddress
	}
	host, port := typed, ttx.DefaultPort
	if h, p, err := net.SplitHostPort(typed); err == nil {
		number, err := strconv.Atoi(p)
		if err != nil || number < 1 || number > 65535 {
			return "", "", errNx2flowAddress
		}
		host, port = h, number
	} else if strings.Count(typed, ":") == 1 {
		// a port that is not a number
		return "", "", errNx2flowAddress
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return "", "", errNx2flowAddress
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		found, err := net.DefaultResolver.LookupIPAddr(lookup, host)
		if err != nil || len(found) == 0 {
			return "", "", errNx2flowReach
		}
		for _, address := range found {
			ips = append(ips, address.IP)
		}
	}
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			remembered := host
			if strings.Contains(host, ":") {
				remembered = "[" + host + "]"
			}
			if port != ttx.DefaultPort {
				remembered += ":" + strconv.Itoa(port)
			}
			return net.JoinHostPort(ip.String(), strconv.Itoa(port)), remembered, nil
		}
	}
	return "", "", errNx2flowNotLocal
}

// nx2flowCardName is the name of a file on the card: what the cards and nx2-flow take.
func nx2flowCardName(name string) string {
	extension := strings.ToLower(filepath.Ext(name))
	base := netSafeName(strings.TrimSuffix(name, filepath.Ext(name)))
	if base == "" {
		base = "game"
	}
	return base + extension
}

// nx2flowFiles are the NUP files of the game of a title ID: the game itself, its updates and DLC
// when they are NUP files, and the copies in NUP of a game kept in another format.
func (web *Web) nx2flowFiles(titleId string) []nx2flowFile {
	_, localDB := web.state.get()
	prefix, err := db.TitleIDPrefix(strings.ToUpper(titleId))
	if localDB == nil || err != nil {
		return nil
	}
	game, ok := localDB.TitlesMap[prefix]
	if !ok {
		return nil
	}
	files := []nx2flowFile{}
	seen := map[string]bool{}
	application := prefix + "000"
	if game.File.Metadata != nil && game.File.Metadata.TitleId != "" {
		application = strings.ToLower(game.File.Metadata.TitleId)
	}
	add := func(info db.ExtendedFileInfo) {
		info = compressedSinceScan(info)
		full := web.moves.resolve(filepath.Join(info.BaseFolder, info.FileName))
		if !switchfs.IsNupFile(full) || seen[full] || web.linksIntoData(full) {
			return
		}
		stat, err := os.Stat(full)
		if err != nil || stat.IsDir() {
			return
		}
		seen[full] = true
		files = append(files, nx2flowFile{Id: netFileId(full), Name: filepath.Base(full), Size: stat.Size(), path: full, titleId: application})
	}
	kept := map[string]bool{}
	if game.BaseExist {
		add(game.File.ExtendedInfo)
		kept[filepath.Join(game.File.ExtendedInfo.BaseFolder, game.File.ExtendedInfo.FileName)] = true
	}
	updates := make([]int, 0, len(game.Updates))
	for version := range game.Updates {
		updates = append(updates, version)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(updates)))
	for _, version := range updates {
		add(game.Updates[version].ExtendedInfo)
	}
	dlcs := make([]string, 0, len(game.Dlc))
	for id := range game.Dlc {
		dlcs = append(dlcs, id)
	}
	sort.Strings(dlcs)
	for _, id := range dlcs {
		add(game.Dlc[id].ExtendedInfo)
	}
	// the NUP of a game the library keeps as an NSP or XCI
	others := []db.ExtendedFileInfo{}
	for file, reason := range localDB.Skipped {
		if switchfs.IsNupFile(file.FileName) && db.KeptInOtherFormat(file, reason) && kept[db.KeptBy(reason.ReasonText)] {
			others = append(others, file)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i].FileName < others[j].FileName })
	for _, file := range others {
		add(file)
	}
	return files
}

// nx2flowGameName is the name of the game of a title ID, as the pages show it.
func (web *Web) nx2flowGameName(titleId string, lang string) string {
	switchDB, localDB := web.state.get()
	prefix, err := db.TitleIDPrefix(strings.ToUpper(titleId))
	if err != nil || localDB == nil {
		return titleId
	}
	local := localDB.TitlesMap[prefix]
	var known *db.SwitchTitle
	if switchDB != nil {
		known = switchDB.TitlesMap[prefix]
	}
	name := titleName(switchDB, lang, strings.ToUpper(prefix)+"000", getLocalTitleName(known, local))
	if name == "" {
		return titleId
	}
	return name
}

// nx2flowCoverImage is the image the cover is made from: the one chosen in Edit, the cover IGDB
// has of a game of the Switch 2, else the icon of the game; nil when there is none.
func (web *Web) nx2flowCoverImage(titleId string) []byte {
	prefix, err := db.TitleIDPrefix(strings.ToUpper(titleId))
	if err != nil {
		return nil
	}
	switchDB, _ := web.state.get()
	if edited, ok := switchDB.EditedTitle(strings.ToUpper(prefix) + "000"); ok && edited.Cover != "" {
		if path, ok := web.imagePath(edited.Cover); ok {
			if data, err := os.ReadFile(path); err == nil {
				return data
			}
		}
	}
	if enriched := web.withSwitch2Details(switchDB); enriched != nil {
		if title := enriched.TitlesMap[prefix]; title != nil && title.FromSwitch2List && title.Attributes.IconUrl != "" {
			if data, err := db.DownloadImage(title.Attributes.IconUrl, nx2flowCoverSource); err == nil {
				return data
			}
		}
	}
	return web.databaseCover(prefix, nx2flowCoverSource)
}

// nx2flowCoverJpeg makes the front cover nx2-flow shows, 500 by 813, from an image: a portrait
// cover fills it, cut in the middle; a square icon or a wide picture goes whole across it, on a
// blurred and darkened copy of itself.
func nx2flowCoverJpeg(data []byte) ([]byte, error) {
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 {
		return nil, errors.New("an empty image")
	}
	frame := image.Rect(0, 0, nx2flowCoverWidth, nx2flowCoverHeight)
	result := image.NewNRGBA(frame)
	fill := gift.New(gift.ResizeToFill(nx2flowCoverWidth, nx2flowCoverHeight, gift.LanczosResampling, gift.CenterAnchor))
	if float64(bounds.Dx())/float64(bounds.Dy()) <= 0.8 {
		fill.Draw(result, source)
	} else {
		background := gift.New(gift.ResizeToFill(nx2flowCoverWidth/5, nx2flowCoverHeight/5, gift.LinearResampling, gift.CenterAnchor),
			gift.GaussianBlur(4), gift.ResizeToFill(nx2flowCoverWidth, nx2flowCoverHeight, gift.LinearResampling, gift.CenterAnchor), gift.Brightness(-35))
		background.Draw(result, source)
		front := gift.New(gift.ResizeToFit(nx2flowCoverWidth, nx2flowCoverHeight, gift.LanczosResampling))
		size := front.Bounds(bounds)
		at := image.Pt((nx2flowCoverWidth-size.Dx())/2, (nx2flowCoverHeight-size.Dy())/2)
		front.DrawAt(result, source, at, gift.OverOperator)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, result, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// nx2flowAddresses loads the addresses the users send to. With the mutex held.
func (web *Web) nx2flowLoadAddresses() {
	s := &web.nx2flow
	if s.loaded {
		return
	}
	s.loaded = true
	s.addresses = map[string]string{}
	file := filepath.Join(web.dataFolder, NX2FLOW_FILENAME)
	if data, ok := durable.ReadStore(file); ok {
		var stored struct {
			Addresses map[string]string `json:"addresses"`
		}
		if durable.ParseStore(file, data, &stored) == nil && stored.Addresses != nil {
			s.addresses = stored.Addresses
		}
	}
}

// nx2flowRemembered is the address a user sent to last; "" when none.
func (web *Web) nx2flowRemembered(user string) string {
	s := &web.nx2flow
	s.mutex.Lock()
	defer s.mutex.Unlock()
	web.nx2flowLoadAddresses()
	return s.addresses[user]
}

// nx2flowRemember keeps the address a user sent to.
func (web *Web) nx2flowRemember(user string, address string) {
	s := &web.nx2flow
	s.mutex.Lock()
	defer s.mutex.Unlock()
	web.nx2flowLoadAddresses()
	if s.addresses[user] == address {
		return
	}
	s.addresses[user] = address
	data, err := json.Marshal(map[string]any{"addresses": s.addresses})
	if err == nil {
		err = durable.WriteFile(filepath.Join(web.dataFolder, NX2FLOW_FILENAME), data)
	}
	if err != nil {
		web.sugarLogger.Warnf("Failed to save the address of nx2-flow: %v", err)
	}
}

// nx2flowGamesFolderOf is the folder of the games a menu.cfg names, the default one when it names
// none the receiver takes.
func nx2flowGamesFolderOf(settings []byte) string {
	for _, line := range strings.Split(string(settings), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(key) != "games" {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(value, "sdmc:")
		value = strings.TrimRight(value, "/")
		if value != "" && ttx.CheckPath(value) == nil {
			return value
		}
	}
	return nx2flowGamesFolder
}

// sendToNx2flow starts sending a NUP file of the library to nx2-flow: the internal function
// behind the button and the dialog. It connects to the console first, so an address that does
// not answer is told at once; the send then goes on as a task. It gives the task.
func (web *Web) sendToNx2flow(request nx2flowRequest) (int64, error) {
	if isDemoMode() {
		return 0, refuseNx2flow(http.StatusForbidden, errorString(DEMO_DISABLED))
	}
	files := web.nx2flowFiles(request.TitleId)
	if len(files) == 0 {
		return 0, refuseNx2flow(http.StatusNotFound, errNx2flowNoFile)
	}
	file := files[0]
	if request.FileId != "" {
		found := false
		for _, candidate := range files {
			if candidate.Id == request.FileId {
				file, found = candidate, true
			}
		}
		if !found {
			return 0, refuseNx2flow(http.StatusNotFound, errNx2flowGone)
		}
	}
	dial, remembered, err := nx2flowAddress(context.Background(), request.Address)
	if err != nil {
		return 0, refuseNx2flow(http.StatusBadRequest, err)
	}

	s := &web.nx2flow
	s.mutex.Lock()
	if s.running == nil {
		s.running = map[int64]*nx2flowRun{}
	}
	if len(s.running) >= nx2flowMaxRunning {
		s.mutex.Unlock()
		return 0, refuseNx2flow(http.StatusTooManyRequests, errNx2flowTooMany)
	}
	for _, run := range s.running {
		if run.address == dial {
			s.mutex.Unlock()
			return 0, refuseNx2flow(http.StatusConflict, errNx2flowBusy)
		}
	}
	// held for the address while it connects
	placeholder := &nx2flowRun{address: dial, user: request.User}
	s.running[-time.Now().UnixNano()] = placeholder
	s.mutex.Unlock()
	release := func() {
		s.mutex.Lock()
		for id, run := range s.running {
			if run == placeholder {
				delete(s.running, id)
			}
		}
		s.mutex.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	connect, stop := context.WithTimeout(ctx, nx2flowDialTimeout)
	client, err := ttx.Dial(connect, dial, nx2flowDialTimeout)
	var info ttx.Info
	if err == nil {
		info, err = client.Ping(connect)
	}
	stop()
	if err != nil {
		if client != nil {
			client.Close()
		}
		cancel()
		release()
		web.sugarLogger.Infof("Send to nx2-flow: %s does not answer: %v", dial, err)
		return 0, refuseNx2flow(http.StatusBadGateway, errNx2flowReach)
	}
	if info.Total > 0 && info.Free < uint64(file.Size)+nx2flowSpareRoom {
		client.Close()
		cancel()
		release()
		return 0, refuseNx2flow(http.StatusInsufficientStorage, errNx2flowNoRoom)
	}
	web.nx2flowRemember(request.User, remembered)

	game := web.nx2flowGameName(request.TitleId, "en")
	taskId := web.taskLog().Start(TASK_NX2FLOW, tasklog.TRIGGER_MANUAL)
	web.taskLog().SetSubject(taskId, game)
	web.taskLog().ProgressArg(taskId, 0, int(max(file.Size>>20, 1)), nx2flowSending, file.Name)
	run := &nx2flowRun{task: taskId, user: request.User, address: dial, game: game, file: file.Name, started: time.Now(), cancel: cancel}
	s.mutex.Lock()
	for id, held := range s.running {
		if held == placeholder {
			delete(s.running, id)
		}
	}
	s.running[taskId] = run
	s.mutex.Unlock()
	web.sugarLogger.Infof("Send to nx2-flow: %s sends %s to %s (%s)", request.User, file.Name, dial, info.Version)
	if !isDemoMode() {
		web.activities().Add(activity.Entry{Time: run.started, User: request.User, Action: activity.ACTION_NX2FLOW,
			Detail: remembered + ": " + game, IP: request.Ip})
	}

	go func() {
		defer func() {
			client.Close()
			cancel()
			s.mutex.Lock()
			delete(s.running, taskId)
			s.mutex.Unlock()
		}()
		failure := web.runNx2flowSend(ctx, client, taskId, file, request.Cover)
		web.taskLog().Finish(taskId, failure)
	}()
	return taskId, nil
}

// runNx2flowSend sends the file and its cover on a connection to the receiver, as the task taskId,
// and gives the failure of the task, nil when it went well.
func (web *Web) runNx2flowSend(ctx context.Context, client *ttx.Client, taskId int64, file nx2flowFile, withCover bool) *tasklog.Note {
	failed := func(err error) *tasklog.Note {
		if ctx.Err() != nil {
			return &tasklog.Note{Text: tasklog.NOTE_COMPRESS_CANCELED}
		}
		var remote *ttx.RemoteError
		detail := err.Error()
		if errors.As(err, &remote) {
			detail = "nx2-flow: " + remote.Error()
		}
		web.sugarLogger.Warnf("Send to nx2-flow: %s failed: %v", file.Name, err)
		return &tasklog.Note{Text: NOTE_NX2FLOW_FAILED, Detail: file.Name + ": " + detail}
	}

	// the folder of the games nx2-flow was set to, when its settings name one
	folder := nx2flowGamesFolder
	var settings bytes.Buffer
	if _, err := client.Get(ctx, nx2flowSettingsFile, &settings, 64<<10); err == nil {
		folder = nx2flowGamesFolderOf(settings.Bytes())
	} else if ctx.Err() != nil {
		return failed(err)
	} else {
		var remote *ttx.RemoteError
		if !errors.As(err, &remote) {
			return failed(err)
		}
	}
	target := path.Join(folder, nx2flowCardName(file.Name))

	source, err := os.Open(file.path)
	if err != nil {
		return failed(err)
	}
	defer source.Close()
	stat, err := source.Stat()
	if err != nil {
		return failed(err)
	}
	total := int(max(stat.Size()>>20, 1))
	shown := time.Time{}
	sum, err := client.Put(ctx, target, source, stat.Size(), func(sent int64) {
		if now := time.Now(); now.Sub(shown) >= 500*time.Millisecond || sent == stat.Size() {
			shown = now
			web.taskLog().ProgressArg(taskId, int(sent>>20), total, nx2flowSending, file.Name)
		}
	})
	if err != nil {
		if errors.Is(err, ttx.ErrMismatch) {
			return &tasklog.Note{Text: NOTE_NX2FLOW_CHECK, Detail: file.Name}
		}
		return failed(err)
	}
	// read back from the card, which may take a while for a large file
	web.taskLog().ProgressArg(taskId, total, total, nx2flowChecking, file.Name)
	size, hash, err := client.Hash(ctx, target)
	var remote *ttx.RemoteError
	if err != nil && errors.As(err, &remote) && ctx.Err() == nil {
		// nx2-flow keeps a file the card cannot hold whole (4 GB or more in FAT32, or bigger than
		// split= of its menu.cfg) in parts "<name>.001", "<name>.002"…: each one is checked
		if parts, found, perr := nx2flowCheckParts(ctx, client, target, source, stat.Size()); found {
			if perr != nil {
				if errors.Is(perr, ttx.ErrMismatch) {
					web.sugarLogger.Warnf("Send to nx2-flow: %s on the card in parts: %v", file.Name, perr)
					return &tasklog.Note{Text: NOTE_NX2FLOW_CHECK, Detail: file.Name}
				}
				return failed(perr)
			}
			web.sugarLogger.Infof("Send to nx2-flow: %s is on the card in %d parts", file.Name, parts)
			size, hash, err = uint64(stat.Size()), sum, nil
		}
	}
	if err != nil {
		return failed(err)
	}
	if size != uint64(stat.Size()) || hash != sum {
		web.sugarLogger.Warnf("Send to nx2-flow: %s on the card is %d bytes %08x, sent %d bytes %08x", file.Name, size, hash, stat.Size(), sum)
		return &tasklog.Note{Text: NOTE_NX2FLOW_CHECK, Detail: file.Name}
	}
	web.taskLog().SetResult(taskId, 0, 1)

	if withCover {
		web.taskLog().Progress(taskId, total, total, nx2flowCover)
		picture := web.nx2flowCoverImage(file.titleId)
		if picture == nil {
			web.taskLog().Warn(taskId, NOTE_NX2FLOW_NO_COVER, "")
			return nil
		}
		cover, err := nx2flowCoverJpeg(picture)
		if err != nil {
			// an image the app cannot read (WebP, or a file cut short)
			web.sugarLogger.Warnf("Send to nx2-flow: the cover of %s cannot be read: %v", file.Name, err)
			web.taskLog().Warn(taskId, NOTE_NX2FLOW_COVER, "")
			return nil
		}
		_, err = client.Put(ctx, path.Join(nx2flowCoversFolder, file.titleId+".jpg"), bytes.NewReader(cover), int64(len(cover)), nil)
		if err != nil {
			if ctx.Err() != nil {
				// the game is on the card: only the cover was cut
				web.taskLog().Warn(taskId, NOTE_NX2FLOW_COVER, "")
				return nil
			}
			web.sugarLogger.Warnf("Send to nx2-flow: the cover of %s failed: %v", file.Name, err)
			web.taskLog().Warn(taskId, NOTE_NX2FLOW_COVER, err.Error())
		}
	}
	return nil
}

// cancelNx2flowSend cancels the send of task id, of the user or, for an administrator, of anyone.
func (web *Web) cancelNx2flowSend(id int64, user string, admin bool) bool {
	s := &web.nx2flow
	s.mutex.Lock()
	defer s.mutex.Unlock()
	run, ok := s.running[id]
	if !ok || run.cancel == nil || (!admin && run.user != user) {
		return false
	}
	run.cancel()
	return true
}

// nx2flowRunning are the tasks of the sends of a user going on.
func (web *Web) nx2flowRunning(user string) []int64 {
	s := &web.nx2flow
	s.mutex.Lock()
	defer s.mutex.Unlock()
	tasks := []int64{}
	for id, run := range s.running {
		if run.user == user && run.task != 0 {
			tasks = append(tasks, id)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i] < tasks[j] })
	return tasks
}

// handleNx2flowGame gives what the dialog needs for a game: its NUP files, the address the user
// sent to last, and the sends of the user going on.
func (web *Web) handleNx2flowGame(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, errNx2flowNotAllowed.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	files := web.nx2flowFiles(mux.Vars(r)["titleId"])
	if files == nil {
		files = []nx2flowFile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "address": web.nx2flowRemembered(principal.Name),
		"port": ttx.DefaultPort, "running": web.nx2flowRunning(principal.Name), "demo": isDemoMode()})
}

// handleNx2flowSend starts a send: {titleId, fileId, address, cover}.
func (web *Web) handleNx2flowSend(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, errNx2flowNotAllowed.Error())
		return
	}
	if isDemoMode() {
		writeGlobalError(w, http.StatusForbidden, lang, DEMO_DISABLED)
		return
	}
	body := struct {
		TitleId string `json:"titleId"`
		FileId  string `json:"fileId"`
		Address string `json:"address"`
		Cover   *bool  `json:"cover"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil || !titleIdPattern.MatchString(strings.ToUpper(body.TitleId)) {
		writeGlobalError(w, http.StatusBadRequest, lang, errNx2flowNoFile.Error())
		return
	}
	taskId, err := web.sendToNx2flow(nx2flowRequest{User: principal.Name, Ip: clientIp(r), TitleId: body.TitleId, FileId: body.FileId,
		Address: body.Address, Cover: body.Cover == nil || *body.Cover})
	if err != nil {
		status := http.StatusInternalServerError
		var refusal *nx2flowRefusal
		if errors.As(err, &refusal) {
			status = refusal.status
		}
		writeGlobalError(w, status, lang, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task": taskId})
}

// handleNx2flowCancel cancels a send going on.
func (web *Web) handleNx2flowCancel(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, errNx2flowNotAllowed.Error())
		return
	}
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || !web.cancelNx2flowSend(id, principal.Name, principal.IsAdmin()) {
		writeGlobalError(w, http.StatusNotFound, lang, errNx2flowNotFound.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": id})
}

// HandleNx2flow adds the paths of Send to nx2-flow.
func (web *Web) HandleNx2flow() {
	web.router.HandleFunc(nx2flowGamePath+"{titleId:[0-9A-Fa-f]{16}}", web.handleNx2flowGame).Methods(http.MethodGet)
	web.router.HandleFunc(nx2flowSendPath, web.handleNx2flowSend).Methods(http.MethodPost)
	web.router.HandleFunc(nx2flowCancelPath+"{id:[0-9]+}", web.handleNx2flowCancel).Methods(http.MethodPost)
}

// nx2flowAllowed tells the paths of Send to nx2-flow a user who may look sends to: the handlers
// check that they may download.
func nx2flowAllowed(urlPath string) bool {
	return urlPath == nx2flowSendPath || strings.HasPrefix(urlPath, nx2flowCancelPath)
}

// nx2flowCheckParts checks a file nx2-flow keeps in parts "<name>.001", "<name>.002"… beside
// target: their sizes add up to the size of the file sent, and each one has the sum of its piece
// of source. found tells whether parts are there at all; it gives how many.
func nx2flowCheckParts(ctx context.Context, client *ttx.Client, target string, source *os.File, size int64) (parts int, found bool, err error) {
	entries, err := client.List(ctx, path.Dir(target))
	if err != nil {
		return 0, false, err
	}
	sizes := map[string]uint64{}
	for _, entry := range entries {
		if !entry.Dir {
			sizes[entry.Name] = entry.Size
		}
	}
	base := path.Base(target)
	var offset int64
	for number := 1; ; number++ {
		name := fmt.Sprintf("%s.%03d", base, number)
		partSize, ok := sizes[name]
		if !ok {
			break
		}
		found = true
		got, hash, err := client.Hash(ctx, path.Join(path.Dir(target), name))
		if err != nil {
			return number - 1, true, err
		}
		if got != partSize || offset+int64(got) > size {
			return number - 1, true, fmt.Errorf("%w: %s is %d bytes", ttx.ErrMismatch, name, got)
		}
		local := crc32.NewIEEE()
		if _, err := io.Copy(local, io.NewSectionReader(source, offset, int64(got))); err != nil {
			return number - 1, true, err
		}
		if local.Sum32() != hash {
			return number - 1, true, fmt.Errorf("%w: %s", ttx.ErrMismatch, name)
		}
		offset += int64(got)
		parts = number
	}
	if found && offset != size {
		return parts, true, fmt.Errorf("%w: the parts hold %d of %d bytes", ttx.ErrMismatch, offset, size)
	}
	return parts, found, nil
}
