package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The same game is sold in several stores with a different title ID for each one: the
// European and the American copy are two games for the console. They are found by their
// names (see regionalName) and one copy is suggested to keep.

// RegionCopy is a copy of a game of the library sold in one store.
type RegionCopy struct {
	Id        string
	Name      string
	Region    string
	Languages []string
	// the language of the interface is one of the languages of the game
	HasLanguage bool
	// the update and the DLC of the library for this copy
	LocalUpdate int
	Dlc         int
	Size        int64
	// a file of the copy was found damaged by a check; all of its files are compressed
	Damaged    bool
	Compressed bool
	// the copy to keep, and why; the one suggested, and the one chosen by the user, which is
	// kept instead (see applyRegionChoice)
	Keep      bool
	Reasons   []string
	Suggested bool
	Chosen    bool
	// the game, its updates and DLC, deleted together
	files []db.ExtendedFileInfo
}

// RegionGroup is a game of the library with copies of several stores.
type RegionGroup struct {
	Name   string
	Copies []RegionCopy
	// the space of the copies that are not suggested to keep
	Savable int64
	// the user chose the copy kept
	Chosen bool
}

// reasons to keep a copy, translated by the interface
const (
	REGION_REASON_LANGUAGE  = "In your language"
	REGION_REASON_LANGUAGES = "More languages"
	REGION_REASON_UPDATE    = "Newer update"
	REGION_REASON_DLC       = "More DLC"
	REGION_REASON_SOUND     = "Not damaged"
	REGION_REASON_PREFERRED = "Your preferred region"
	REGION_REASON_SMALLER   = "Already compressed"
)

func (web *Web) regionalDuplicates(lang string) []RegionGroup {
	return web.derived("regionalDuplicates:"+lang, func() any { return web.buildRegionalDuplicates(lang) }).([]RegionGroup)
}

// regionCopies tells, for the games the library has from several regions, the copies left
// out when each game counts once (all but the one kept: chosen, else suggested) and, for the
// copy kept, the regions of the others. Nothing when the setting is off.
func (web *Web) regionCopies(lang string) (leftOut map[string]bool, others map[string][]string) {
	leftOut, others = map[string]bool{}, map[string][]string{}
	if !settings.ReadSettings(web.dataFolder).CountRegionsOnce {
		return leftOut, others
	}
	for _, group := range web.regionalDuplicates(lang) {
		kept, keptRegion := "", ""
		for _, copy := range group.Copies {
			if copy.Keep && kept == "" {
				kept, keptRegion = copy.Id, regionCode(copy.Region)
			}
		}
		if kept == "" && len(group.Copies) > 0 {
			kept, keptRegion = group.Copies[0].Id, regionCode(group.Copies[0].Region)
		}
		// a flag for each country: copies of one country in other languages ("US (Spanish)") add none
		regions := []string{}
		for _, copy := range group.Copies {
			if copy.Id != kept {
				leftOut[copy.Id] = true
				if region := regionCode(copy.Region); region != keptRegion && !hasValue(regions, region) {
					regions = append(regions, region)
				}
			}
		}
		others[kept] = regions
	}
	return leftOut, others
}

// regionsByName are the regions the titles database has each game in, by its name without
// the translation (regionalName): the flags of its card when each game counts once, the ones
// of the library lit and the others dimmed. A name of two games of one region (two games, not
// copies) has none.
func (web *Web) regionsByName() map[string][]string {
	return web.derived("regionsByName", func() any { return web.buildRegionsByName() }).(map[string][]string)
}

func (web *Web) buildRegionsByName() map[string][]string {
	result := map[string][]string{}
	switchDB, _ := web.state.get()
	if switchDB == nil {
		return result
	}
	ambiguous := map[string]bool{}
	for _, title := range switchDB.TitlesMap {
		name, region := title.Attributes.Name, regionCode(title.Attributes.Region)
		key := regionalName(name)
		if key == "" || region == "" || isDemo(title, name) {
			continue
		}
		if hasValue(result[key], region) {
			ambiguous[key] = true
			continue
		}
		result[key] = append(result[key], region)
	}
	for key := range ambiguous {
		delete(result, key)
	}
	for _, regions := range result {
		sort.Strings(regions)
	}
	return result
}

// missingRegions are the regions the titles database has a game in that the library has no
// copy of: the region of the card and the ones of its other copies are the library's.
func missingRegions(item TitleItem, byName map[string][]string) []string {
	all := byName[regionalName(item.OriginalName)]
	own := regionCode(item.Region)
	if own == "" || !hasValue(all, own) {
		return nil
	}
	missing := []string{}
	for _, region := range all {
		if region != own && !hasValue(item.OtherFlags, region) {
			missing = append(missing, region)
		}
	}
	return missing
}

// RegionZone is a group of the stores of Nintendo that sell a game with the same title ID: a game
// of Spain and one of the United Kingdom are one copy, a game of Japan another one. The titles
// database gives each copy the country of one of its stores.
type RegionZone struct {
	Code string
	Name string
}

// regionZones are the zones Settings offers to prefer, in this order; the name is translated.
var regionZones = []RegionZone{
	{"EU", "Europe"},
	{"US", "Americas"},
	{"AU", "Australia and New Zealand"},
	{"JP", "Japan"},
	{"KR", "Korea"},
	{"HK", "Hong Kong and Taiwan"},
	{"CN", "China"},
}

// preferredRegionsShown is how many zones Settings lets the user put in order
const preferredRegionsShown = 3

// regionZone is the zone of a country of the titles database, or of a zone; Europe for the
// countries of the other stores of Nintendo of Europe.
func regionZone(region string) string {
	switch region = regionCode(region); region {
	case "":
		return ""
	case "US", "CA", "MX", "BR", "AR", "CL", "CO", "PE":
		return "US"
	case "AU", "NZ":
		return "AU"
	case "JP", "KR", "CN":
		return region
	case "HK", "TW":
		return "HK"
	}
	return "EU"
}

// preferredZones are the zones of a preferred region setting, in order and each once: "EU,US,JP",
// or a country saved by an older version ("GB" is Europe).
func preferredZones(setting string) []string {
	zones := []string{}
	for _, region := range strings.Split(setting, ",") {
		if zone := regionZone(region); zone != "" && !hasValue(zones, zone) {
			zones = append(zones, zone)
		}
	}
	return zones
}

// zoneRank is the place of the zone of a region among the preferred ones: 0 the first, and
// len(zones) for a zone not preferred.
func zoneRank(region string, zones []string) int {
	zone := regionZone(region)
	for i, preferred := range zones {
		if zone == preferred {
			return i
		}
	}
	return len(zones)
}

// preferredChoices are the zones of the preferred region setting for the selects of Settings: one
// for each select, "" when it has none.
func preferredChoices(setting string) []string {
	choices := preferredZones(setting)
	for len(choices) < preferredRegionsShown {
		choices = append(choices, "")
	}
	return choices[:preferredRegionsShown]
}

// regionStatus is how up to date a game of several regions is when each game counts once: up to
// date when one of its copies is, and short of the DLC of the copy that has the most of them.
type regionStatus struct {
	update     bool
	missingDlc int
	dlcOwned   int
	dlcTotal   int
}

func (s regionStatus) apply(item *TitleItem) {
	item.UpdateAvailable, item.MissingDlcCount, item.DlcOwned, item.DlcTotal = s.update, s.missingDlc, s.dlcOwned, s.dlcTotal
}

// mergedStatus gives, for the copy kept of each game of several regions, its state with its other
// copies counted; nothing when each region counts apart.
func (web *Web) mergedStatus(lang string) map[string]regionStatus {
	if !settings.ReadSettings(web.dataFolder).CountRegionsOnce {
		return nil
	}
	return web.derived("mergedStatus:"+lang, func() any { return web.buildMergedStatus(lang) }).(map[string]regionStatus)
}

func (web *Web) buildMergedStatus(lang string) map[string]regionStatus {
	library := web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem)
	byId := make(map[string]TitleItem, len(library))
	for _, item := range library {
		byId[item.Id] = item
	}
	result := map[string]regionStatus{}
	for _, group := range web.regionalDuplicates(lang) {
		kept, ok := byId[group.Copies[0].Id]
		if !ok {
			continue
		}
		status := regionStatus{update: kept.UpdateAvailable, missingDlc: kept.MissingDlcCount, dlcOwned: kept.DlcOwned, dlcTotal: kept.DlcTotal}
		for _, copy := range group.Copies[1:] {
			other, ok := byId[copy.Id]
			if !ok {
				continue
			}
			status.update = status.update && other.UpdateAvailable
			if other.MissingDlcCount < status.missingDlc {
				status.missingDlc, status.dlcOwned, status.dlcTotal = other.MissingDlcCount, other.DlcOwned, other.DlcTotal
			}
		}
		result[kept.Id] = status
	}
	return result
}

// shownMissingUpdates are the updates the Updates page lists: when each game counts once, none of
// the copies of a game of several regions left out, nor of the copy kept when another one of its
// copies is up to date.
func (web *Web) shownMissingUpdates(lang string) map[string]process.IncompleteTitle {
	all := web.missingUpdates()
	merged := web.mergedStatus(lang)
	if merged == nil {
		return all
	}
	leftOut, _ := web.regionCopies(lang)
	shown := make(map[string]process.IncompleteTitle, len(all))
	for key, title := range all {
		id := strings.ToUpper(title.Attributes.Id)
		game := id
		if prefix, err := db.TitleIDPrefix(id); err == nil {
			game = strings.ToUpper(prefix) + "000"
		}
		if leftOut[game] {
			continue
		}
		if status, ok := merged[game]; ok && game == id && !status.update {
			continue
		}
		shown[key] = title
	}
	return shown
}

// regionCode is the country of a region of the titles database, without the language some
// games are sold in there under titles of their own: "US (Spanish)" is US.
func regionCode(region string) string {
	region = strings.ToUpper(strings.TrimSpace(region))
	if i := strings.IndexAny(region, " ("); i > 0 {
		region = region[:i]
	}
	return region
}

// coverFlags is the most flags a cover shows: the others are counted
const coverFlags = 4

func (web *Web) buildRegionalDuplicates(lang string) []RegionGroup {
	switchDB, localDB := web.state.get()
	if switchDB == nil || localDB == nil {
		return nil
	}
	damaged := web.verifications().damaged()
	byName := map[string][]RegionCopy{}
	for prefix, local := range localDB.TitlesMap {
		if !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		title := switchDB.TitlesMap[prefix]
		original := getLocalTitleName(title, local)
		key := regionalName(original)
		if key == "" {
			continue
		}
		// a demo is never a copy of the full game
		if isDemo(title, original) || localDemo(local.File.Metadata) {
			key += " |demo"
		}
		copy := RegionCopy{
			Id:          strings.ToUpper(local.File.Metadata.TitleId),
			Name:        titleName(switchDB, lang, local.File.Metadata.TitleId, original),
			LocalUpdate: local.LatestUpdate,
			Dlc:         len(local.Dlc),
			Size:        local.File.ExtendedInfo.Size,
		}
		copy.files = append(copy.files, local.File.ExtendedInfo)
		for _, update := range local.Updates {
			copy.Size += update.ExtendedInfo.Size
			copy.files = append(copy.files, update.ExtendedInfo)
		}
		for _, dlc := range local.Dlc {
			copy.Size += dlc.ExtendedInfo.Size
			copy.files = append(copy.files, dlc.ExtendedInfo)
		}
		copy.Compressed = true
		for _, file := range copy.files {
			path := filepath.Join(file.BaseFolder, file.FileName)
			if _, bad := damaged[path]; bad {
				copy.Damaged = true
			}
			switch strings.ToLower(filepath.Ext(file.FileName)) {
			case ".nsz", ".xcz":
			default:
				copy.Compressed = false
			}
		}
		if title != nil {
			copy.Region = title.Attributes.Region
			copy.Languages = title.Attributes.Languages
			copy.HasLanguage = hasValue(title.Attributes.Languages, lang)
		}
		byName[key] = append(byName[key], copy)
	}

	groups := []RegionGroup{}
	chosen := web.regionChoices().snapshot()
	preferred := preferredZones(settings.ReadSettings(web.dataFolder).PreferredRegion)
	for _, copies := range byName {
		if len(copies) < 2 || !distinctRegions(copies) {
			continue
		}
		group := suggestRegionCopy(copies, preferred)
		applyRegionChoice(&group, chosen)
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name) })
	return groups
}

// distinctRegions tells copies of one game from several stores: each of another region.
// Games of one region with a name alike ("DOOM" and "DOOM (1993)") are different games, and
// a copy without a known region cannot be told apart either.
func distinctRegions(copies []RegionCopy) bool {
	seen := map[string]bool{}
	for _, copy := range copies {
		if copy.Region == "" || seen[copy.Region] {
			return false
		}
		seen[copy.Region] = true
	}
	return true
}

// suggestRegionCopy marks the copy to keep: never a damaged one, then the one of the first
// preferred zone the game has a copy of (zones in order, none for no preference), the one in the
// language of the interface, with more languages, the newer update, more DLC and already
// compressed.
func suggestRegionCopy(copies []RegionCopy, preferred []string) RegionGroup {
	score := func(c RegionCopy) int {
		value := len(c.Languages)*10 + c.Dlc
		// the order chosen weighs more than the language and the rest; a damaged copy, the most
		if rank := zoneRank(c.Region, preferred); rank < len(preferred) {
			value += 10000 - rank*2000
		}
		if c.HasLanguage {
			value += 1000
		}
		if c.Damaged {
			value -= 100000
		}
		if c.LocalUpdate > 0 {
			value += 5
		}
		if c.Compressed {
			value += 2
		}
		return value
	}
	sort.SliceStable(copies, func(i, j int) bool {
		if score(copies[i]) != score(copies[j]) {
			return score(copies[i]) > score(copies[j])
		}
		return copies[i].Id < copies[j].Id
	})
	best := &copies[0]
	best.Keep, best.Suggested = true, true
	others := copies[1:]
	reason := func(text string, better func(other RegionCopy) bool) {
		for _, other := range others {
			if !better(other) {
				return
			}
		}
		best.Reasons = append(best.Reasons, text)
	}
	reason(REGION_REASON_SOUND, func(other RegionCopy) bool { return !best.Damaged && other.Damaged })
	reason(REGION_REASON_PREFERRED, func(other RegionCopy) bool {
		return zoneRank(best.Region, preferred) < zoneRank(other.Region, preferred)
	})
	reason(REGION_REASON_LANGUAGE, func(other RegionCopy) bool { return best.HasLanguage && !other.HasLanguage })
	reason(REGION_REASON_LANGUAGES, func(other RegionCopy) bool { return len(best.Languages) > len(other.Languages) })
	reason(REGION_REASON_UPDATE, func(other RegionCopy) bool { return best.LocalUpdate > other.LocalUpdate })
	reason(REGION_REASON_DLC, func(other RegionCopy) bool { return best.Dlc > other.Dlc })
	reason(REGION_REASON_SMALLER, func(other RegionCopy) bool { return best.Compressed && !other.Compressed })

	group := RegionGroup{Name: best.Name, Copies: copies}
	for _, other := range others {
		group.Savable += other.Size
	}
	return group
}

// removeRegionCopy deletes a copy of a game, with its updates and DLC, when another copy of the
// same game stays in the library. Only the copies listed on the Space page can be deleted.
func (web *Web) removeRegionCopy(id string, lang string) (int, int64, error) {
	var target *RegionCopy
	var others []RegionCopy
	for _, group := range web.buildRegionalDuplicates(lang) {
		for i := range group.Copies {
			if strings.EqualFold(group.Copies[i].Id, id) && len(group.Copies) > 1 {
				copy := group.Copies[i]
				target = &copy
				others = append(append([]RegionCopy{}, group.Copies[:i]...), group.Copies[i+1:]...)
			}
		}
	}
	if target == nil || len(target.files) == 0 {
		return 0, 0, errRegionCopy
	}
	if !web.soundCopyStays(*target, others) {
		return 0, 0, errRegionVerify
	}
	files := web.regionFilesToDelete(*target)
	if len(files) == 0 {
		return 0, 0, errRegionCopy
	}
	reasons := make([]string, len(files))
	for i := range reasons {
		reasons[i] = "other region"
	}
	deleted, freed, err := web.removeFilesAsTask(files, reasons)
	if err != nil {
		return 0, 0, err
	}
	web.sugarLogger.Infof("[Space] copy %s of another region deleted: %d files, %d bytes", target.Id, deleted, freed)
	return deleted, freed, nil
}

// soundCopyStays tells whether a copy of the game from another region stays, checked and
// sound: two games of the same name and region are different games, not copies, and a
// copy is never deleted while the one that stays could be damaged.
func (web *Web) soundCopyStays(target RegionCopy, others []RegionCopy) bool {
	store := web.verifications()
	for _, other := range others {
		if other.Damaged || len(other.files) == 0 || other.Region == "" || strings.EqualFold(other.Region, target.Region) {
			continue
		}
		base := other.files[0]
		path := filepath.Join(base.BaseFolder, base.FileName)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if record, ok := store.current(path, info); ok && record.sound() {
			return true
		}
	}
	return false
}

// regionFilesToDelete are the files of a copy, each once, without the ones that also hold
// another game (a collection on one card).
func (web *Web) regionFilesToDelete(target RegionCopy) []db.ExtendedFileInfo {
	_, localDB := web.state.get()
	others := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}}
	if localDB != nil {
		for prefix, title := range localDB.TitlesMap {
			if title.File.Metadata == nil || !strings.EqualFold(title.File.Metadata.TitleId, target.Id) {
				others.TitlesMap[prefix] = title
			}
		}
	}
	inUse := process.FilesInUse(others)
	files := []db.ExtendedFileInfo{}
	seen := map[string]bool{}
	for _, file := range target.files {
		path := filepath.Join(file.BaseFolder, file.FileName)
		if seen[path] || inUse[path] {
			continue
		}
		seen[path] = true
		files = append(files, file)
	}
	return files
}

var (
	errRegionVerify = errorString("A copy is only deleted while a copy of the game from another region stays, checked and sound: check the files first.")
	errRegionCopy   = errorString("This copy cannot be deleted: no other copy of the game is in the library.")
	errRegionBusy   = errorString("A synchronization is running, try again when it has finished.")
)

// errorString is an error whose text is shown translated.
type errorString string

func (e errorString) Error() string { return string(e) }

func (web *Web) handleRegionRemove() {
	web.router.HandleFunc("/space/regions/remove", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		deleted, freed, err := web.removeRegionCopy(id, lang)
		if err != nil {
			status := http.StatusBadRequest
			if err == errRegionBusy || err == errOrganizeBusy {
				status = http.StatusConflict
			}
			writeGlobalError(w, status, lang, err.Error())
			return
		}
		// the cached library no longer matches the files on disk
		web.Rescan(TRIGGER_ORGANIZE)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "freed": freed})
	}).Methods("POST")
}
