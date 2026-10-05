package web

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/gorilla/mux"
)

var unsafeFileNameChars = regexp.MustCompile(`[/\\?%*:|"<>\x00-\x1f]`)

type archiveEntry struct {
	file   db.ExtendedFileInfo
	folder string
}

// archiveEntries lists the files of a game: base, updates and DLC. Multi content files
// that contain several of them are included once.
func archiveEntries(local *db.SwitchGameFiles) []archiveEntry {
	entries := []archiveEntry{}
	seen := map[string]bool{}
	add := func(file db.ExtendedFileInfo, folder string) {
		path := filepath.Join(file.BaseFolder, file.FileName)
		if seen[path] {
			return
		}
		seen[path] = true
		entries = append(entries, archiveEntry{file: file, folder: folder})
	}

	if local.BaseExist {
		add(local.File.ExtendedInfo, "")
	}
	versions := make([]int, 0, len(local.Updates))
	for version := range local.Updates {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	for _, version := range versions {
		add(local.Updates[version].ExtendedInfo, "Updates/")
	}
	dlcIds := make([]string, 0, len(local.Dlc))
	for id := range local.Dlc {
		dlcIds = append(dlcIds, id)
	}
	sort.Strings(dlcIds)
	for _, id := range dlcIds {
		add(local.Dlc[id].ExtendedInfo, "DLC/")
	}
	return entries
}

func archiveSize(entries []archiveEntry) int64 {
	var size int64
	for _, entry := range entries {
		size += entry.file.Size
	}
	return size
}

// safeFileName removes characters that are not allowed in file names.
func safeFileName(name string) string {
	name = strings.TrimSpace(unsafeFileNameChars.ReplaceAllString(name, ""))
	if name == "" {
		return "game"
	}
	return name
}

// writeArchive streams the files as a ZIP archive. Switch files are already compressed,
// so they are stored as they are: fast, and no temporary file or memory is needed.
func writeArchive(w io.Writer, root string, entries []archiveEntry) error {
	archive := zip.NewWriter(w)
	for _, entry := range entries {
		path := filepath.Join(entry.file.BaseFolder, entry.file.FileName)
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return err
		}
		header := &zip.FileHeader{
			Name:     root + entry.folder + entry.file.FileName,
			Method:   zip.Store,
			Modified: info.ModTime(),
		}
		writer, err := archive.CreateHeader(header)
		if err == nil {
			_, err = io.Copy(writer, file)
		}
		file.Close()
		if err != nil {
			return err
		}
	}
	return archive.Close()
}

func (web *Web) HandleArchive() {
	web.router.HandleFunc("/api/titles/{titleId}/archive.zip", func(w http.ResponseWriter, r *http.Request) {
		switchDB, localDB := web.state.get()
		local := findLocalTitle(localDB, mux.Vars(r)["titleId"])
		if local == nil {
			http.NotFound(w, r)
			return
		}

		id := strings.ToUpper(local.File.Metadata.TitleId)
		var title *db.SwitchTitle
		if switchDB != nil {
			if prefix, err := db.TitleIDPrefix(id); err == nil {
				title = switchDB.TitlesMap[prefix]
			}
		}
		name := safeFileName(getLocalTitleName(title, local)) + " [" + id + "]"

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name+".zip"))
		if err := writeArchive(w, name+"/", archiveEntries(local)); err != nil {
			// the response has started, the client receives a truncated archive
			web.sugarLogger.Warnf("Archive of %v aborted: %v", id, err)
		}
	}).Methods("GET")
}
