package web

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The library can be exported as a web page that opens without the app: a ZIP with an
// index.html and the thumbnails of the covers, to show the collection to someone without
// giving access to the server. Only names, versions and covers are included.

type exportSiteGame struct {
	Id            string
	Name          string
	Year          string
	Version       string
	Size          int64
	UpdateMissing bool
	DlcMissing    int
	DlcOwned      int
	// "update", "dlc" or "ok"; both first ones may be set, separated by a space
	Status string
	Search string
	Cover  string
}

type exportSiteData struct {
	Title      string
	Created    time.Time
	Games      []exportSiteGame
	Updates    int
	MissingDlc int
	Size       int64
}

// writeExportSite writes the ZIP of the exported web page.
func (web *Web) writeExportSite(w io.Writer, templates templateSet, lang string) error {
	_, localDB := web.state.get()
	data := exportSiteData{Title: translate(lang, "My Switch library"), Created: time.Now()}
	covers := map[string]string{}
	for _, title := range web.getExport() {
		game := exportSiteGame{Id: title.Id, Name: title.Name, Version: title.Version, Size: title.Size,
			UpdateMissing: title.UpdateMissing, DlcMissing: title.DlcMissing, DlcOwned: title.DlcOwned}
		if len(title.ReleaseDate) >= 4 {
			game.Year = title.ReleaseDate[:4]
		}
		statuses := []string{}
		if title.UpdateMissing {
			statuses = append(statuses, "update")
			data.Updates++
		}
		if title.DlcMissing > 0 {
			statuses = append(statuses, "dlc")
		}
		if len(statuses) == 0 {
			statuses = append(statuses, "ok")
		}
		game.Status = strings.Join(statuses, " ")
		game.Search = strings.ToLower(title.Name + " " + title.Id)
		data.MissingDlc += title.DlcMissing
		data.Size += title.Size
		if localDB != nil {
			if local, ok := localDB.TitlesMap[strings.ToLower(title.Id[:13])]; ok && local.Icon != "" {
				thumbnail := filepath.Join(web.dataFolder, "img", "thumbs", local.Icon)
				if _, err := os.Stat(thumbnail); err == nil {
					game.Cover = strconv.Itoa(len(covers)+1) + ".jpg"
					covers[game.Cover] = thumbnail
				}
			}
		}
		data.Games = append(data.Games, game)
	}

	archive := zip.NewWriter(w)
	page := &bytes.Buffer{}
	if err := templates.execute(page, lang, data); err != nil {
		return err
	}
	file, err := archive.Create("index.html")
	if err != nil {
		return err
	}
	if _, err := file.Write(page.Bytes()); err != nil {
		return err
	}
	for name, path := range covers {
		image, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// thumbnails are JPEG already: stored, not compressed again
		file, err := archive.CreateHeader(&zip.FileHeader{Name: "covers/" + name, Method: zip.Store, Modified: time.Now()})
		if err != nil {
			return err
		}
		if _, err := file.Write(image); err != nil {
			return err
		}
	}
	return archive.Close()
}
