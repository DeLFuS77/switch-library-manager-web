package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The games of the Nintendo Switch 2 alone have title IDs from 04. There are no public keys for
// them, so their files cannot be read; the library lists them for whoever has the console ("I
// have a Switch 2" in Settings), and the games one has on a cartridge are marked by hand (see
// owned.go). titles.json knows few of them: titles.switch2.json, made with the titles data from
// the title list of switchbrew, has the names and regions of the others, and IGDB, when its keys
// are set, their cover, release date and description.

const SWITCH2_JSON_FILENAME = "titles.switch2.json"

// a game of the Switch 2 IGDB gives no release date, or one still to come, is asked again after
// a week: its date is set as it comes closer
const switch2DateRetry = 7 * 24 * time.Hour

// switch2Title is a game of titles.switch2.json.
type switch2Title struct {
	Name string `json:"name"`
	// the regions it is sold in: US, EU, JP, KR, CN
	Regions []string `json:"regions,omitempty"`
	// how it is sold: digital, cartridge, key card
	Sold []string `json:"sold,omitempty"`
	// its game on IGDB (the slug), which Wikidata links it to: asked before searching it by name
	Igdb string `json:"igdb,omitempty"`
	// the day it came out on the Switch 2, from Wikidata: 2025-06-05
	Date string `json:"date,omitempty"`
}

// switch2Date is a date of the list as the titles database writes it: 2025-06-05 is 20250605;
// 0 when there is none, or one that cannot be read.
func switch2Date(date string) int {
	day, err := time.Parse("2006-01-02", strings.TrimSpace(date))
	if err != nil {
		return 0
	}
	return dateNumber(day)
}

// switch2Slugs are the games of IGDB Wikidata links the games of the list to, by title ID prefix.
func (web *Web) switch2Slugs() map[string]string {
	slugs := map[string]string{}
	for id, entry := range web.loadSwitch2Titles() {
		slug := strings.TrimSpace(entry.Igdb)
		if slug == "" {
			continue
		}
		if prefix, err := db.TitleIDPrefix(strings.ToUpper(strings.TrimSpace(id))); err == nil {
			slugs[prefix] = slug
		}
	}
	return slugs
}

// switch2List is titles.switch2.json: the games by upper case title ID.
type switch2List struct {
	Titles map[string]switch2Title `json:"titles"`
}

// downloadSwitch2Titles fetches a newer titles.switch2.json; failures keep the saved copy.
func (web *Web) downloadSwitch2Titles() {
	settingsObj := settings.ReadSettings(web.dataFolder)
	path := filepath.Join(web.dataFolder, SWITCH2_JSON_FILENAME)
	etag := settingsObj.LocalizedTitlesEtags["switch2"]
	if _, err := os.Stat(path); err != nil {
		etag = ""
	}
	result, err := db.LoadAndUpdateAllowingEmpty(settings.CompressedFirst(settings.DEFAULT_SWITCH2_JSON_URL), path, etag)
	if err != nil {
		web.sugarLogger.Debugf("The list of the games of the Switch 2 is not available: %v", err)
		return
	}
	result.File.Close()
	if result.Etag != etag {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			if s.LocalizedTitlesEtags == nil {
				s.LocalizedTitlesEtags = map[string]string{}
			}
			s.LocalizedTitlesEtags["switch2"] = result.Etag
		})
	}
}

// loadSwitch2Titles reads the saved titles.switch2.json.
func (web *Web) loadSwitch2Titles() map[string]switch2Title {
	list := switch2List{}
	if data, err := os.ReadFile(filepath.Join(web.dataFolder, SWITCH2_JSON_FILENAME)); err == nil {
		if err := json.Unmarshal(data, &list); err != nil {
			web.sugarLogger.Warnf("The list of the games of the Switch 2 cannot be read: %v", err)
		}
	}
	return list.Titles
}

// applySwitch2Titles adds to the titles database the games of titles.switch2.json it does not
// list: the games of the Switch 2 titles.json has win. A title made from the list before is made
// again, so a game taken out of the list goes away.
func (web *Web) applySwitch2Titles(switchDB *db.SwitchTitlesDB) {
	if switchDB == nil {
		return
	}
	for prefix, title := range switchDB.TitlesMap {
		if !title.FromSwitch2List {
			continue
		}
		if len(title.Dlc) == 0 && len(title.Updates) == 0 {
			delete(switchDB.TitlesMap, prefix)
			continue
		}
		// what titles.json had of it: its DLC and updates without the game
		switchDB.TitlesMap[prefix] = &db.SwitchTitle{Dlc: title.Dlc, Updates: title.Updates}
	}
	preferred := preferredZones(settings.ReadSettings(web.dataFolder).PreferredRegion)
	for id, entry := range web.loadSwitch2Titles() {
		id = strings.ToUpper(strings.TrimSpace(id))
		name := strings.TrimSpace(entry.Name)
		if !titleIdPattern.MatchString(id) || !db.Switch2(id) || !usableName(name) {
			continue
		}
		if kind, err := db.ContentKind(id); err != nil || kind != db.KIND_BASE {
			continue
		}
		prefix, err := db.TitleIDPrefix(id)
		if err != nil {
			continue
		}
		title := &db.SwitchTitle{Dlc: map[string]db.TitleAttributes{}, FromSwitch2List: true}
		if known, ok := switchDB.TitlesMap[prefix]; ok {
			if known.Attributes.Id != "" && usableName(known.Attributes.Name) {
				continue
			}
			if known.Dlc != nil {
				title.Dlc = known.Dlc
			}
			title.Updates = known.Updates
		}
		if versions, ok := switchDB.Versions[prefix]; ok && title.Updates == nil {
			title.Updates = versions
		}
		// the date of Wikidata, until IGDB tells its own (see withSwitch2Details)
		title.Attributes = db.TitleAttributes{Id: id, Name: name, Region: switch2Region(entry.Regions, preferred), ReleaseDate: switch2Date(entry.Date)}
		switchDB.TitlesMap[prefix] = title
	}
}

// switch2Region is the region a game of the list shows: the first of the preferred ones it is
// sold in, else the one of the titles database (US) and then the others.
func switch2Region(regions []string, preferred []string) string {
	for _, zone := range append(preferred, "US", "EU", "JP", "KR", "CN") {
		for _, region := range regions {
			if strings.EqualFold(region, zone) {
				return strings.ToUpper(region)
			}
		}
	}
	if len(regions) > 0 {
		return strings.ToUpper(regions[0])
	}
	return ""
}

// withSwitch2Details puts what IGDB told of the games of the list in their titles: their cover,
// release date, description, genres and publisher. The database itself is left as it is, as it
// is shared.
func (web *Web) withSwitch2Details(switchDB *db.SwitchTitlesDB) *db.SwitchTitlesDB {
	if switchDB == nil {
		return nil
	}
	store := web.igdbTitles()
	var titles map[string]*db.SwitchTitle
	for prefix, title := range switchDB.TitlesMap {
		if !title.FromSwitch2List {
			continue
		}
		found, ok := store.get(prefix)
		if !ok || !found.Found {
			continue
		}
		// the list gives only the name and the regions: the rest is what IGDB tells, the newest
		attributes := title.Attributes
		if found.Cover != "" {
			attributes.IconUrl = found.Cover
		}
		if found.ReleaseDate != 0 {
			attributes.ReleaseDate = found.ReleaseDate
		}
		if found.Summary != "" {
			attributes.Description = found.Summary
		}
		if len(found.Genres) > 0 {
			attributes.Genres = found.Genres
		}
		if found.Publisher != "" {
			attributes.Publisher = found.Publisher
		}
		if attributes.IconUrl == title.Attributes.IconUrl && attributes.ReleaseDate == title.Attributes.ReleaseDate && attributes.Description == title.Attributes.Description &&
			slices.Equal(attributes.Genres, title.Attributes.Genres) && attributes.Publisher == title.Attributes.Publisher {
			continue
		}
		if titles == nil {
			titles = make(map[string]*db.SwitchTitle, len(switchDB.TitlesMap))
			for key, value := range switchDB.TitlesMap {
				titles[key] = value
			}
		}
		copied := *title
		copied.Attributes = attributes
		titles[prefix] = &copied
	}
	if titles == nil {
		return switchDB
	}
	enriched := *switchDB
	enriched.TitlesMap = titles
	return &enriched
}

// switch2TitlesToLookUp are the games of the Switch 2 to ask IGDB about, by prefix with their
// name: the ones of the list never asked (or not found a while ago), and the ones without a
// release date or with one still to come, asked again after a while. Only the ones that can be
// shown: all of them with "I have a Switch 2", else the ones marked as owned and the ones the
// folders have a file of (a NUP), which the Library shows always.
func (web *Web) switch2TitlesToLookUp(now time.Time) map[string]string {
	pending := map[string]string{}
	switchDB, localDB := web.state.get()
	if switchDB == nil {
		return pending
	}
	all := settings.ReadSettings(web.dataFolder).HasSwitch2
	marked := web.markedPrefixes()
	store := web.igdbTitles()
	today := dateNumber(now)
	for prefix, title := range switchDB.TitlesMap {
		if !title.FromSwitch2List || !usableName(title.Attributes.Name) {
			continue
		}
		if !all && !hasGame(localDB, marked, prefix) {
			continue
		}
		found, asked := store.get(prefix)
		switch {
		case !asked, !found.Found && now.Sub(found.Checked) > igdbTitleRetry, found.Version < igdbTitleVersion:
		case found.Found && (found.ReleaseDate == 0 || found.ReleaseDate > today) && now.Sub(found.Checked) > switch2DateRetry:
		default:
			continue
		}
		pending[prefix] = title.Attributes.Name
	}
	return pending
}

// dateNumber is a day as the titles database writes it: 20250605.
func dateNumber(day time.Time) int {
	return day.Year()*10000 + int(day.Month())*100 + day.Day()
}

// igdbSwitch2Platform is the Nintendo Switch 2 in IGDB; the first search of a game of the Switch
// 2 asks IGDB for the platform of the slug "switch-2", in case it changed (see platformOf).
const igdbSwitch2Platform = 508

// platformOf is the platform of IGDB the game of a title ID is searched in: the Switch 2 for the
// games of the Switch 2 alone, else the Switch.
func (c *igdbClient) platformOf(ctx context.Context, titleId string) (int, error) {
	if !db.Switch2(strings.ToUpper(titleId)) {
		return igdbSwitchPlatform, nil
	}
	c.platformMutex.Lock()
	defer c.platformMutex.Unlock()
	if c.switch2 != 0 {
		return c.switch2, nil
	}
	var platforms []struct {
		Id   int    `json:"id"`
		Slug string `json:"slug"`
	}
	if err := c.query(ctx, "platforms", `fields id,slug; where slug = "switch-2";`, &platforms); err != nil {
		if errors.Is(err, errIgdbCredentials) {
			return 0, err
		}
		// asked again by the next search
		return igdbSwitch2Platform, nil
	}
	c.switch2 = igdbSwitch2Platform
	if len(platforms) == 1 && platforms[0].Id > 0 && platforms[0].Slug == "switch-2" {
		c.switch2 = platforms[0].Id
	}
	return c.switch2, nil
}
