package web

import (
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/dtrunk90/switch-library-manager-web/switchfs/optimize"
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
	// the games the file holds, when it holds several (a collection on one cartridge)
	GamesInFile []string
	// the file of the game: its updates and DLC that are in it too (a cartridge or a NUP with them),
	// said on its row, translated
	AlsoHolds []string
	// an update or a DLC in the file of the game: it is not listed again as a file, with the whole
	// size of that file
	InGameFile bool
	// the parts of a NUP, the game first, with what each takes of the file: told instead of
	// AlsoHolds
	Parts []TitleFilePart
	// for a NUP: whether Eglantine and Lichii open it and can start its game
	Emulators *TitleEmulators
}

// TitleEmulators is what Eglantine and Lichii, the emulators that open NUP files, make of one,
// for its row in Files: whether they read it well, and what keeps them from it, translated.
type TitleEmulators struct {
	Ok       bool
	Warnings []string
	// its metadata is counted from 0: an administrator can fix it from the page (see nupfix.go)
	ZeroBased bool
	// it carries no icon: an administrator can give it the cover of its game (see nupicon.go)
	NoIcon bool
}

// what is said on the page of a NUP the emulators do not read well, before its reasons
const (
	emulatorsDoNotOpen   = "Eglantine and Lichii do not open it: %v."
	emulatorsCannotStart = "Eglantine and Lichii cannot start it: %v."
	emulatorsZeroBased   = "Eglantine and Lichii show its name, version and icon wrong: the types of its metadata are counted from 0, as a build of the packer of the format wrote them."
)

// nupEmulators is what the emulators make of the NUP a file is; nil for any other file, and for
// a NUP read by a version that did not tell it.
func nupEmulators(info db.SwitchFileInfo, lang string) *TitleEmulators {
	if !switchfs.IsNupFile(info.ExtendedInfo.FileName) || info.Metadata == nil || info.Metadata.Emulators == nil {
		return nil
	}
	emulators := info.Metadata.Emulators
	result := &TitleEmulators{Ok: emulators.Ok(), ZeroBased: emulators.ZeroBased, NoIcon: emulators.NoIcon}
	issue := func(issue switchfs.NupEmulatorIssue) string {
		return i18n.Translatef(lang, issue.Text, issue.Values()...)
	}
	switch {
	case len(emulators.Refused) > 0:
		reasons := make([]string, len(emulators.Refused))
		for i, refused := range emulators.Refused {
			reasons[i] = issue(refused)
		}
		result.Warnings = append(result.Warnings, i18n.Translatef(lang, emulatorsDoNotOpen, strings.Join(reasons, "; ")))
	case emulators.NoStart != nil:
		result.Warnings = append(result.Warnings, i18n.Translatef(lang, emulatorsCannotStart, issue(*emulators.NoStart)))
	}
	if emulators.ZeroBased {
		result.Warnings = append(result.Warnings, i18n.Translate(lang, emulatorsZeroBased))
	}
	if emulators.NoIcon {
		result.Warnings = append(result.Warnings, i18n.Translate(lang, NUP_NO_ICON))
	}
	return result
}

// TitleFilePart is a part of a NUP: the game, an update or a DLC, with the bytes of the file its
// data takes (the data an update shares with the game counted with the game).
type TitleFilePart struct {
	Kind string
	// "v458752" for the game and an update, the name of a DLC
	Detail string
	Size   int64
}

// the kinds of the parts of a NUP
const (
	PART_GAME   = "game"
	PART_UPDATE = "update"
	PART_DLC    = "dlc"
)

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
	// the cheats of the version the library has, or of others
	Cheats *CheatsInfo
	// the copies of the game from other regions the library has, with the one that stands for it
	Regions *RegionGroup
	// the game is on the wishlist
	Wished    bool
	Id        string
	Name      string
	Publisher string
	Region    string
	// from the titles database
	Genres        []string
	Players       int
	Languages     []string
	AgeRating     int
	RatingContent []string
	Description   string
	ReleaseDate   time.Time
	ImageUrl      string
	BannerUrl     string
	Screenshots   []string

	Owned bool
	// the game is marked as owned without a file (see owned.go): the user has it, a cartridge
	// for instance, even when the library has no file of it
	Marked  bool
	Base    *TitleFile
	Updates []TitleFile
	// every update released, the newest first
	Versions []TitleVersion
	// how long the game takes to beat, from IGDB
	TimeToBeat TimeToBeat
	// the backups of its save in the vault, the newest first
	Saves []SaveBackup

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

	// what the user changed by hand (see overrides.go), and the game as it is without it
	Edit                TitleOverride
	Edited              bool
	OriginalName        string
	OriginalDescription string
	OriginalImageUrl    string

	// All local files of the game, downloadable as one archive
	ArchiveFiles int
	ArchiveSize  int64
	// the files a complete pack of the game is made of (base, latest update and DLC)
	PackFiles int
	PackSize  int64
	// Optimize for install: whether the game can be, the size of the game and its newest update it
	// is made of, and the version it takes
	CanOptimize     bool
	OptimizeSize    int64
	OptimizeVersion int
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
	// how the game goes for the user, and the states to choose from
	Play       userdata.PlayEntry
	PlayStates []userdata.PlayState
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
		detail.Regions = web.regionGroupOf(baseId, lang)
		detail.Base = newTitleFile(local.File, strings.ToUpper(getType(local)), "/api/titles/"+baseId)
		detail.Base.Emulators = nupEmulators(local.File, lang)
		if local.Icon != "" {
			detail.ImageUrl = "/i/" + local.Icon
		}
		if local.Banner != "" {
			detail.BannerUrl = "/i/" + local.Banner
		}

		for _, version := range sortedVersions(local.Updates) {
			update := local.Updates[version]
			file := newTitleFile(update, "UPD", "/api/titles/"+baseId+"/updates/"+strconv.Itoa(version))
			file.Emulators = nupEmulators(update, lang)
			if update.ExtendedInfo == local.File.ExtendedInfo {
				file.InGameFile = true
				detail.Base.AlsoHolds = append(detail.Base.AlsoHolds, i18n.Translatef(lang, "update v%v", version))
			}
			detail.Updates = append(detail.Updates, *file)
		}
		// a game merged with its update (optimized for install, or by another tool) holds that update
		if local.File.Metadata != nil && local.File.Metadata.Version > 0 {
			detail.Base.AlsoHolds = append(detail.Base.AlsoHolds, i18n.Translatef(lang, "update v%v, merged into the game", local.File.Metadata.Version))
		}
		inGameFile := 0
		for _, dlc := range local.Dlc {
			if dlc.ExtendedInfo == local.File.ExtendedInfo {
				inGameFile++
			}
		}
		if inGameFile > 0 {
			detail.Base.AlsoHolds = append(detail.Base.AlsoHolds, i18n.Translatef(lang, "%v DLC", inGameFile))
		}
		detail.LocalUpdate = local.UpdateLevel()
		required := installedRequirement(local)
		detail.RequiredFirmware = firmwareVersion(required)
		detail.FirmwareTooNew = web.firmwareTooNew(required)

		entries := archiveEntries(local)
		detail.ArchiveFiles = len(entries)
		detail.ArchiveSize = archiveSize(entries)
		for _, file := range withCompressedCopies(packFiles(local), localDB.Skipped) {
			detail.PackFiles++
			detail.PackSize += file.Size
		}
		if optimize.Built {
			for _, file := range web.optimizeFiles(local) {
				detail.CanOptimize = true
				detail.OptimizeSize += file.Size
			}
			detail.OptimizeVersion = local.LatestUpdate
		}
	}
	if detail.Id == "" {
		// only updates or DLC of an unknown game are present
		detail.Id = strings.ToUpper(prefix + strings.Repeat("0", 16-len(prefix)))
	}
	detail.OriginalImageUrl = detail.ImageUrl
	if cover := editedCover(switchDB, detail.Id); cover != "" {
		detail.ImageUrl = cover
	}

	detail.Name = titleName(switchDB, lang, detail.Id, getLocalTitleName(title, local))
	detail.Wished = web.wishes().Has(detail.Id)
	detail.Marked = web.owned().Has(detail.Id)
	detail.TimeToBeat, _ = web.timesToBeat().get(detail.Id)
	detail.Saves = web.titleSaves(detail.Id, lang)
	// the name and description of the game without what the user changed by hand
	detail.OriginalName, detail.OriginalDescription = detail.Name, detail.Description
	if switchDB != nil {
		unedited := *switchDB
		unedited.Edited = nil
		detail.OriginalName = titleName(&unedited, lang, detail.Id, getLocalTitleName(title, local))
		if localized, ok := unedited.LocalizedTitle(lang, detail.Id); ok && localized.Description != "" {
			detail.OriginalDescription = localized.Description
		}
	}
	if localized, ok := switchDB.LocalizedTitle(lang, detail.Id); ok && localized.Description != "" {
		detail.Description = localized.Description
	}
	if detail.Name == "" {
		detail.Name = "Unknown title"
	}
	if detail.OriginalName == "" {
		detail.OriginalName = detail.Name
	}
	detail.Edit, detail.Edited = web.titleOverrides().get(detail.Id)
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
	if gamesInFiles := web.gamesInFiles(); len(gamesInFiles) > 0 {
		if detail.Base != nil {
			detail.Base.GamesInFile = gamesInFiles[detail.Base.Path]
		}
		for i := range detail.Updates {
			detail.Updates[i].GamesInFile = gamesInFiles[detail.Updates[i].Path]
		}
	}
	if detail.Owned {
		detail.Cheats = web.cheatsInfo(detail.Id, detail.LocalUpdate)
	}
	if title != nil {
		detail.Versions = titleVersions(detail.Name, title.Updates, local)
	}

	// All DLC known to the titles database or present in the library
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
				dlc.File.InGameFile = local.BaseExist && file.ExtendedInfo == local.File.ExtendedInfo
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
	if detail.Base != nil && local != nil {
		detail.Base.Parts = nupParts(local, detail.Name, detail.Dlc)
		if len(detail.Base.Parts) > 0 {
			detail.Base.AlsoHolds = nil
		}
	}

	return detail, true
}

// nupParts are the parts of the NUP a game is in, with what each takes of the file, the game
// first, then its updates and its DLC; none for any other file, or for a NUP read by a version
// that did not tell the sizes.
func nupParts(local *db.SwitchGameFiles, gameName string, dlc []TitleDlc) []TitleFilePart {
	file := local.File
	if !switchfs.IsNupFile(file.ExtendedInfo.FileName) || file.Metadata == nil || file.Metadata.StoredSize <= 0 {
		return nil
	}
	parts := []TitleFilePart{{Kind: PART_GAME, Detail: "v" + strconv.Itoa(file.Metadata.Version), Size: file.Metadata.StoredSize}}
	for _, version := range sortedVersions(local.Updates) {
		update := local.Updates[version]
		if update.ExtendedInfo == file.ExtendedInfo && update.Metadata != nil {
			parts = append(parts, TitleFilePart{Kind: PART_UPDATE, Detail: "v" + strconv.Itoa(version), Size: update.Metadata.StoredSize})
		}
	}
	// the DLC in the order of the page, by their names without the name of the game
	for _, entry := range dlc {
		held, ok := local.Dlc[strings.ToLower(entry.Id)]
		if !ok || held.ExtendedInfo != file.ExtendedInfo || held.Metadata == nil {
			continue
		}
		parts = append(parts, TitleFilePart{Kind: PART_DLC, Detail: dlcShortName(entry.Name, gameName), Size: held.Metadata.StoredSize})
	}
	return parts
}

// dlcShortName is the name of a DLC without the name of its game before it, as a list of the
// parts of the game has the game already: "Donkey Kong Bananza: DK Island" is "DK Island".
func dlcShortName(name string, gameName string) string {
	if gameName == "" || len(name) <= len(gameName) || !strings.EqualFold(name[:len(gameName)], gameName) {
		return name
	}
	after := name[len(gameName):]
	// the name of the game as a whole, not the start of a longer word
	if strings.IndexAny(after, " :-–—") != 0 {
		return name
	}
	if rest := strings.TrimLeft(after, " :-–—"); rest != "" {
		return rest
	}
	return name
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
			} else if local.BaseExist && local.File.Metadata != nil && local.File.Metadata.Version == version {
				// the game was rebuilt with this update in it (see UpdateLevel)
				entry.Owned = true
				if local.File.Metadata.Ncap != nil {
					entry.DisplayVersion = local.File.Metadata.Ncap.DisplayVersion
				}
			}
			entry.Installed = local.BaseExist && local.UpdateLevel() == version
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

func (web *Web) HandleTitle() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/title.html")
	web.handlePack()
	web.handleOptimize()
	web.handleNupFix()
	web.handleNupIcon()

	web.router.HandleFunc("/title/{titleId}.html", func(w http.ResponseWriter, r *http.Request) {
		detail, ok := web.getTitleDetail(mux.Vars(r)["titleId"], web.requestLanguage(r))
		if !ok {
			http.NotFound(w, r)
			return
		}
		web.render(w, r, templates, TitlePageData{GlobalPageData: web.globalPageData("title"), Title: detail,
			Collections: web.collections().Of(detail.Id), AllCollections: web.collections().Names(),
			Favorite: web.favorites().Has(detail.Id), Saga: web.sameSaga(detail.Id, detail.Name, web.requestLanguage(r)),
			Play: web.play().Of(playUser(r), detail.Id), PlayStates: userdata.PlayStates()})
	}).Methods("GET")
}

// NoFile reports whether the user has the game without a file of it in the library.
func (t TitleDetail) NoFile() bool {
	return t.Marked && !t.Owned
}

// HasGame reports whether the user has the game: its file is in the library, or it is marked.
func (t TitleDetail) HasGame() bool {
	return t.Owned || t.Marked
}

// IsSwitch2 reports whether the game is of the Nintendo Switch 2 alone.
func (t TitleDetail) IsSwitch2() bool {
	return db.Switch2(t.Id)
}

// DlcPercent is the share of the DLC of the game in the library.
func (t TitleDetail) DlcPercent() int {
	if t.DlcTotal == 0 {
		return 0
	}
	return t.DlcOwned * 100 / t.DlcTotal
}

// DescriptionHint is the beginning of the original description, shown in the empty field of the
// description when the game is edited.
func (t TitleDetail) DescriptionHint() string {
	runes := []rune(strings.Join(strings.Fields(t.OriginalDescription), " "))
	if len(runes) <= 200 {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:200])) + "…"
}

// OlderVersions is the number of versions shown only on request, after the newest five.
func (d TitleDetail) OlderVersions() int {
	return max(len(d.Versions)-5, 0)
}
