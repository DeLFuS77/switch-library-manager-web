package web

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

type TitleFile struct {
	Type           string
	FileName       string
	Path           string
	Size           int64
	Version        int
	DisplayVersion string
	DownloadUrl    string
	// the firmware the file needs, e.g. 12.1.0
	RequiredFirmware string
}

// TitleVersion is an update released for a game, for the version history of the game page.
type TitleVersion struct {
	Version int
	// the number of the update: 1 for the first one
	Number int
	// the version shown by the game, when the file is in the library (e.g. 1.2.0)
	DisplayVersion string
	Date           time.Time
	// the update is in the library, or it is the one installed
	Owned     bool
	Installed bool
	// a web search for what the update brings
	NotesUrl string
}

type TitleDlc struct {
	Id              string
	Name            string
	Owned           bool
	Ignored         bool
	File            *TitleFile
	LatestVersion   int
	UpdateAvailable bool
}

type TitleDetail struct {
	// the game is on the wishlist
	Wished      bool
	Id          string
	Name        string
	Publisher   string
	Region      string
	// from the titles database
	Genres        []string
	Players       int
	Languages     []string
	AgeRating     int
	RatingContent []string
	Description string
	ReleaseDate time.Time
	ImageUrl    string
	BannerUrl   string
	Screenshots []string

	Owned   bool
	Base    *TitleFile
	Updates []TitleFile
	// every update released, the newest first
	Versions []TitleVersion

	LocalUpdate      int
	LatestUpdate     int
	LatestUpdateDate time.Time
	UpdateMissing    bool
	UpdatesIgnored   bool

	Dlc        []TitleDlc
	MissingDlc int
	// DLC in the library, of the DLC not ignored
	DlcOwned int
	DlcTotal int

	// the firmware the installed game needs, and whether the console is too old for it
	RequiredFirmware string
	FirmwareTooNew   bool

	// all local files of the game, downloadable as one archive
	ArchiveFiles int
	ArchiveSize  int64
}

type TitlePageData struct {
	GlobalPageData
	Title TitleDetail
	// the collections of the game, and every collection to choose from
	Collections    []string
	AllCollections []string
	// the game is a favorite, and the other games of its series
	Favorite bool
	Saga     SagaProgress
}

func newTitleFile(info db.SwitchFileInfo, fileType string, downloadUrl string) *TitleFile {
	file := &TitleFile{
		Type:        fileType,
		FileName:    info.ExtendedInfo.FileName,
		Path:        filepath.Join(info.ExtendedInfo.BaseFolder, info.ExtendedInfo.FileName),
		Size:        info.ExtendedInfo.Size,
		DownloadUrl: downloadUrl,
	}
	if info.Metadata != nil {
		file.Version = info.Metadata.Version
		if info.Metadata.Ncap != nil {
			file.DisplayVersion = info.Metadata.Ncap.DisplayVersion
		}
	}
	file.RequiredFirmware = firmwareVersion(requiredSystemVersion(info))
	return file
}

// getTitleDetail collects everything known about the game a title ID (base, update or
// DLC) belongs to. It returns false if neither the library nor the titles database knows it.
func (web *Web) getTitleDetail(titleId string, lang string) (TitleDetail, bool) {
	detail := TitleDetail{}

	prefix, err := db.TitleIDPrefix(titleId)
	if err != nil {
		return detail, false
	}

	switchDB, localDB := web.state.get()
	var title *db.SwitchTitle
	if switchDB != nil {
		title = switchDB.TitlesMap[prefix]
	}
	var local *db.SwitchGameFiles
	if localDB != nil {
		local = localDB.TitlesMap[prefix]
	}
	if (title == nil || title.Attributes.Id == "") && local == nil {
		return detail, false
	}

	settingsObj := settings.ReadSettings(web.dataFolder)
	ignoredDlc := toLowerSet(settingsObj.IgnoreDLCTitleIds)
	ignoredUpdates := toLowerSet(settingsObj.IgnoreUpdateTitleIds)

	if title != nil {
		detail.Id = strings.ToUpper(title.Attributes.Id)
		detail.Publisher = title.Attributes.Publisher
		detail.Genres = title.Attributes.Genres
		detail.Players = title.Attributes.Players
		detail.Languages = title.Attributes.Languages
		detail.AgeRating = title.Attributes.AgeRating
		detail.RatingContent = title.Attributes.RatingContent
		detail.Region = title.Attributes.Region
		details := web.titleDetails(title, lang)
		detail.Description = strings.TrimSpace(details.Description)
		detail.ImageUrl = title.Attributes.IconUrl
		detail.BannerUrl = title.Attributes.BannerUrl
		detail.Screenshots = details.Screenshots
		if release, err := intToTime(title.Attributes.ReleaseDate); err == nil {
			detail.ReleaseDate = release
		}
	}

	if local != nil && local.BaseExist && local.File.Metadata != nil {
		detail.Owned = true
		baseId := strings.ToUpper(local.File.Metadata.TitleId)
		if detail.Id == "" {
			detail.Id = baseId
		}
		detail.Base = newTitleFile(local.File, strings.ToUpper(getType(local)), "/api/titles/"+baseId)
		if local.Icon != "" {
			detail.ImageUrl = "/i/" + local.Icon
		}
		if local.Banner != "" {
			detail.BannerUrl = "/i/" + local.Banner
		}

		for _, version := range sortedVersions(local.Updates) {
			update := local.Updates[version]
			detail.Updates = append(detail.Updates, *newTitleFile(update, "UPD", "/api/titles/"+baseId+"/updates/"+strconv.Itoa(version)))
		}
		detail.LocalUpdate = local.LatestUpdate
		required := installedRequirement(local)
		detail.RequiredFirmware = firmwareVersion(required)
		detail.FirmwareTooNew = web.firmwareTooNew(required)

		entries := archiveEntries(local)
		detail.ArchiveFiles = len(entries)
		detail.ArchiveSize = archiveSize(entries)
	}
	if detail.Id == "" {
		// only updates or DLC of an unknown game are present
		detail.Id = strings.ToUpper(prefix + strings.Repeat("0", 16-len(prefix)))
	}

	detail.Name = titleName(switchDB, lang, detail.Id, getLocalTitleName(title, local))
	detail.Wished = web.wishes().has(detail.Id)
	if localized, ok := switchDB.LocalizedTitle(lang, detail.Id); ok && localized.Description != "" {
		detail.Description = localized.Description
	}
	if detail.Name == "" {
		detail.Name = "Unknown title"
	}
	_, detail.UpdatesIgnored = ignoredUpdates[strings.ToLower(detail.Id)]

	if title != nil {
		for version, date := range title.Updates {
			if version > detail.LatestUpdate {
				detail.LatestUpdate = version
				detail.LatestUpdateDate, _ = strToTime("2006-01-02", date)
			}
		}
	}
	detail.UpdateMissing = detail.Owned && detail.LocalUpdate < detail.LatestUpdate
	if title != nil {
		detail.Versions = titleVersions(detail.Name, title.Updates, local)
	}

	// all DLC known to the titles database or present in the library
	dlcIds := map[string]struct{}{}
	if title != nil {
		for id := range title.Dlc {
			dlcIds[id] = struct{}{}
		}
	}
	if local != nil {
		for id := range local.Dlc {
			dlcIds[id] = struct{}{}
		}
	}
	for id := range dlcIds {
		dlc := TitleDlc{Id: strings.ToUpper(id)}
		_, dlc.Ignored = ignoredDlc[id]

		if title != nil {
			if attributes, ok := title.Dlc[id]; ok {
				dlc.Name = attributes.Name
				if latest, err := attributes.Version.Int64(); err == nil {
					dlc.LatestVersion = int(latest)
				}
			}
		}

		if local != nil {
			if file, ok := local.Dlc[id]; ok {
				dlc.Owned = true
				dlc.File = newTitleFile(file, "DLC", "/api/titles/"+detail.Id+"/dlc/"+dlc.Id)
				if dlc.Name == "" {
					dlc.Name = strings.TrimSpace(db.ParseTitleNameFromFileName(file.ExtendedInfo.FileName))
				}
				_, updateIgnored := ignoredUpdates[id]
				// ignoring the updates of a game also ignores the updates of its DLC (as in Updates)
				dlc.UpdateAvailable = !updateIgnored && !detail.UpdatesIgnored && !settingsObj.IgnoreDLCUpdates && dlc.File.Version < dlc.LatestVersion
			}
		}
		if dlc.Name == "" {
			dlc.Name = dlc.Id
		}
		dlc.Name = titleName(switchDB, lang, dlc.Id, dlc.Name)
		// DLC of a game that is not in the library are not "missing"
		if detail.Owned && !dlc.Owned && !dlc.Ignored {
			detail.MissingDlc++
		}
		if dlc.Owned {
			detail.DlcOwned++
			detail.DlcTotal++
		} else if !dlc.Ignored {
			detail.DlcTotal++
		}
		detail.Dlc = append(detail.Dlc, dlc)
	}
	sort.Slice(detail.Dlc, func(i, j int) bool {
		if detail.Dlc[i].Name == detail.Dlc[j].Name {
			return detail.Dlc[i].Id < detail.Dlc[j].Id
		}
		return detail.Dlc[i].Name < detail.Dlc[j].Name
	})

	return detail, true
}

// titleVersions lists the updates of a game known to the titles database, the newest first,
// with the ones in the library marked.
func titleVersions(name string, released map[int]string, local *db.SwitchGameFiles) []TitleVersion {
	versions := make([]TitleVersion, 0, len(released))
	for version, date := range released {
		if version <= 0 {
			continue
		}
		entry := TitleVersion{Version: version, Number: version >> 16}
		entry.Date, _ = strToTime("2006-01-02", date)
		if local != nil {
			if update, ok := local.Updates[version]; ok {
				entry.Owned = true
				if update.Metadata != nil && update.Metadata.Ncap != nil {
					entry.DisplayVersion = update.Metadata.Ncap.DisplayVersion
				}
			}
			entry.Installed = local.BaseExist && local.LatestUpdate == version
		}
		query := name + " Nintendo Switch update"
		if entry.DisplayVersion != "" {
			query += " " + entry.DisplayVersion
		} else if !entry.Date.IsZero() {
			query += " " + entry.Date.Format("January 2006")
		}
		query += " patch notes"
		entry.NotesUrl = "https://duckduckgo.com/?q=" + url.QueryEscape(query)
		versions = append(versions, entry)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version > versions[j].Version })
	return versions
}

func sortedVersions(updates map[int]db.SwitchFileInfo) []int {
	versions := make([]int, 0, len(updates))
	for version := range updates {
		versions = append(versions, version)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(versions)))
	return versions
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func (web *Web) HandleTitle() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/title.html")

	web.router.HandleFunc("/title/{titleId}.html", func(w http.ResponseWriter, r *http.Request) {
		detail, ok := web.getTitleDetail(mux.Vars(r)["titleId"], web.requestLanguage(r))
		if !ok {
			http.NotFound(w, r)
			return
		}
		web.render(w, r, templates, TitlePageData{GlobalPageData: web.globalPageData("title"), Title: detail,
			Collections: web.collections().of(detail.Id), AllCollections: web.collections().names(),
			Favorite: web.favorites().has(detail.Id), Saga: web.sameSaga(detail.Id, detail.Name, web.requestLanguage(r))})
	}).Methods("GET")
}

// DlcPercent is the share of the DLC of the game in the library.
func (t TitleDetail) DlcPercent() int {
	if t.DlcTotal == 0 {
		return 0
	}
	return t.DlcOwned * 100 / t.DlcTotal
}

// OlderVersions is the number of versions shown only on request, after the newest five.
func (d TitleDetail) OlderVersions() int {
	return max(len(d.Versions)-5, 0)
}
