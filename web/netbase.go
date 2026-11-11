package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// The bytes of a base game for NXDock: a game NXDock merged with its update keeps only what that
// update read of the base, and a newer update may read a little more (usually a few MB). NXDock
// asks for those places here:
//
//	/install/base/{title ID}/{offset}/{length}
//
// and gets the RomFS section of the program of the base game in the library, decrypted, as the
// update reads it (switchfs.OpenBaseRomfs). NXDock checks every block with the hashes of the new
// update. The same gate as the rest of the network install: on, a user, a key of a console.

const (
	netInstallBase = NET_INSTALL_PREFIX + "base/"
	// the most asked at once
	netBaseMaxRead = 64 << 20
	// a base opened stays open this long after its last read (an NCZ is decompressed once)
	netBaseKeep = 10 * time.Minute
)

type netBaseOpen struct {
	base *switchfs.BaseRomfs
	path string
	last time.Time
}

// netBases are the bases opened lately, by title ID.
type netBases struct {
	mutex sync.Mutex
	open  map[uint64]*netBaseOpen
	timer *time.Timer
}

// read reads [offset, offset + length) of the base of titleId at path, opening it when needed.
func (b *netBases) read(path string, titleId uint64, offset int64, p []byte) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.open == nil {
		b.open = map[uint64]*netBaseOpen{}
	}
	entry := b.open[titleId]
	if entry != nil && entry.path != path {
		entry.base.Close()
		delete(b.open, titleId)
		entry = nil
	}
	if entry == nil {
		base, err := switchfs.OpenBaseRomfs(context.Background(), path, titleId)
		if err != nil {
			return err
		}
		entry = &netBaseOpen{base: base, path: path}
		b.open[titleId] = entry
	}
	entry.last = time.Now()
	b.closeLater()
	_, err := entry.base.ReadAt(p, offset)
	return err
}

// closeLater closes the bases not read for netBaseKeep, once that has passed.
func (b *netBases) closeLater() {
	if b.timer != nil {
		return
	}
	b.timer = time.AfterFunc(netBaseKeep, func() {
		b.mutex.Lock()
		defer b.mutex.Unlock()
		b.timer = nil
		for id, entry := range b.open {
			if time.Since(entry.last) >= netBaseKeep {
				entry.base.Close()
				delete(b.open, id)
			}
		}
		if len(b.open) > 0 {
			b.closeLater()
		}
	})
}

func (web *Web) handleNetBase(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, netInstallBase), "/")
	if len(parts) != 3 || len(parts[0]) != 16 {
		http.NotFound(w, r)
		return
	}
	titleId, err1 := strconv.ParseUint(parts[0], 16, 64)
	offset, err2 := strconv.ParseInt(parts[1], 10, 64)
	length, err3 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || offset < 0 || length <= 0 || length > netBaseMaxRead {
		http.Error(w, "the place asked is not valid", http.StatusBadRequest)
		return
	}
	files, _ := web.netFilesKept(options)
	path := ""
	for _, file := range files {
		if file.kind == netFolderGames && strings.EqualFold(file.titleId, parts[0]) {
			path = file.path
			break
		}
	}
	if path == "" {
		http.Error(w, "the base game is not in the library", http.StatusNotFound)
		return
	}
	p := make([]byte, length)
	if err := web.netBases.read(path, titleId, offset, p); err != nil {
		web.sugarLogger.Warnf("Network install: the base of %016X for NXDock: %v", titleId, err)
		http.Error(w, "the base game could not be read", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Write(p)
}
