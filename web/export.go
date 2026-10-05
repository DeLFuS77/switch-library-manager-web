package web

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ExportFile struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	Size           int64  `json:"size"`
	Version        int    `json:"version"`
	DisplayVersion string `json:"displayVersion,omitempty"`
}

type ExportDlc struct {
	Id              string      `json:"id"`
	Name            string      `json:"name"`
	Status          string      `json:"status"`
	UpdateAvailable bool        `json:"updateAvailable,omitempty"`
	File            *ExportFile `json:"file,omitempty"`
}

type ExportTitle struct {
	Id             string       `json:"id"`
	Name           string       `json:"name"`
	Publisher      string       `json:"publisher,omitempty"`
	Region         string       `json:"region,omitempty"`
	ReleaseDate    string       `json:"releaseDate,omitempty"`
	Version        string       `json:"version,omitempty"`
	LocalUpdate    int          `json:"localUpdate"`
	LatestUpdate   int          `json:"latestUpdate"`
	UpdateMissing  bool         `json:"updateMissing"`
	UpdatesIgnored bool         `json:"updatesIgnored,omitempty"`
	DlcOwned       int          `json:"dlcOwned"`
	DlcMissing     int          `json:"dlcMissing"`
	Size           int64        `json:"size"`
	Files          []ExportFile `json:"files"`
	Dlc            []ExportDlc  `json:"dlc"`
}

func exportFile(file *TitleFile) *ExportFile {
	if file == nil {
		return nil
	}
	return &ExportFile{Type: file.Type, Path: file.Path, Size: file.Size, Version: file.Version, DisplayVersion: file.DisplayVersion}
}

// getExport returns every game of the library, sorted by name.
func (web *Web) getExport() []ExportTitle {
	_, localDB := web.state.get()
	titles := []ExportTitle{}
	if localDB == nil {
		return titles
	}

	for _, local := range localDB.TitlesMap {
		if !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		detail, ok := web.getTitleDetail(local.File.Metadata.TitleId)
		if !ok {
			continue
		}

		title := ExportTitle{
			Id:             detail.Id,
			Name:           detail.Name,
			Publisher:      detail.Publisher,
			Region:         detail.Region,
			LocalUpdate:    detail.LocalUpdate,
			LatestUpdate:   detail.LatestUpdate,
			UpdateMissing:  detail.UpdateMissing && !detail.UpdatesIgnored,
			UpdatesIgnored: detail.UpdatesIgnored,
			DlcMissing:     detail.MissingDlc,
			Files:          []ExportFile{},
			Dlc:            []ExportDlc{},
		}
		if !detail.ReleaseDate.IsZero() {
			title.ReleaseDate = detail.ReleaseDate.Format("2006-01-02")
		}

		if detail.Base != nil {
			title.Version = detail.Base.DisplayVersion
			title.Files = append(title.Files, *exportFile(detail.Base))
		}
		for i := range detail.Updates {
			if i == 0 && detail.Updates[0].DisplayVersion != "" {
				title.Version = detail.Updates[0].DisplayVersion
			}
			title.Files = append(title.Files, *exportFile(&detail.Updates[i]))
		}

		for _, dlc := range detail.Dlc {
			status := "missing"
			switch {
			case dlc.Owned:
				status = "owned"
				title.DlcOwned++
			case dlc.Ignored:
				status = "ignored"
			}
			title.Dlc = append(title.Dlc, ExportDlc{Id: dlc.Id, Name: dlc.Name, Status: status, UpdateAvailable: dlc.UpdateAvailable, File: exportFile(dlc.File)})
		}

		for _, file := range title.Files {
			title.Size += file.Size
		}
		for _, dlc := range title.Dlc {
			if dlc.File != nil {
				title.Size += dlc.File.Size
			}
		}

		titles = append(titles, title)
	}

	sort.Slice(titles, func(i, j int) bool {
		a, b := strings.ToLower(titles[i].Name), strings.ToLower(titles[j].Name)
		if a == b {
			return titles[i].Id < titles[j].Id
		}
		return a < b
	})
	return titles
}

func exportFileName(extension string) string {
	return "switch-library-" + time.Now().Format("2006-01-02") + "." + extension
}

func (web *Web) HandleExport() {
	web.router.HandleFunc("/export/library.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(exportFileName("json")))
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		encoder.Encode(web.getExport())
	}).Methods("GET")

	web.router.HandleFunc("/export/library.csv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(exportFileName("csv")))
		// the byte order mark makes spreadsheet applications read the file as UTF-8
		w.Write([]byte{0xEF, 0xBB, 0xBF})

		writer := csv.NewWriter(w)
		writer.Write([]string{"Title ID", "Name", "Publisher", "Region", "Release date", "Version", "Local update",
			"Latest update", "Update missing", "DLC owned", "DLC missing", "Size (bytes)", "Base file"})
		for _, title := range web.getExport() {
			baseFile := ""
			if len(title.Files) > 0 {
				baseFile = title.Files[0].Path
			}
			writer.Write([]string{
				title.Id, csvSafe(title.Name), csvSafe(title.Publisher), title.Region, title.ReleaseDate, csvSafe(title.Version),
				strconv.Itoa(title.LocalUpdate), strconv.Itoa(title.LatestUpdate), strconv.FormatBool(title.UpdateMissing),
				strconv.Itoa(title.DlcOwned), strconv.Itoa(title.DlcMissing), strconv.FormatInt(title.Size, 10), csvSafe(baseFile),
			})
		}
		writer.Flush()
	}).Methods("GET")
}

// csvSafe prevents spreadsheet applications from evaluating a cell as a formula
// (CSV injection), e.g. a game name starting with "=".
func csvSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}
