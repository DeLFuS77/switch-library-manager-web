package web

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A game of the library that titles.json does not list (removed from the eShop, sold in one
// store only, a homebrew) still gets a title of its own: the versions Nintendo published (to
// tell its updates), its name and publisher from its own files, and from IGDB, when its keys
// are set, its genres, release date, description and cover. The rest of the app uses it like
// any title; Missing does not list it, as it is not one of the database.

// IGDB_TITLES_FILENAME keeps what IGDB told of these games, as IGDB asks to keep its data.
const IGDB_TITLES_FILENAME = "igdbtitles.json"

// a game IGDB did not find is asked again after a month, in case it was added
const igdbTitleRetry = 30 * 24 * time.Hour

// igdbTitleVersion is what a game is asked again under, found or not: 2, the release date of the
// console searched, where before it was the first one on any platform (a game of the Switch 2 that
// came out on the PC years before had that date); 3, the name searched without the marks and the
// words of the eShop, then by shorter forms (see igdbSearchNames); 4, the game Wikidata links a
// game of the Switch 2 to first, and a game of the Switch 2 IGDB lists on the Switch only (see
// igdbFind).
const igdbTitleVersion = 4

// IgdbTitle is what IGDB tells of a game the titles database does not list.
type IgdbTitle struct {
	Checked     time.Time `json:"checked"`
	Found       bool      `json:"found"`
	Version     int       `json:"version,omitempty"`
	Genres      []string  `json:"genres,omitempty"`
	ReleaseDate int       `json:"release_date,omitempty"`
	Publisher   string    `json:"publisher,omitempty"`
	Developer   string    `json:"developer,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Cover       string    `json:"cover,omitempty"`
}

type igdbTitleStore struct {
	mutex sync.Mutex
	path  string
	games map[string]IgdbTitle
	// one save at a time: an older one never ends after a newer one
	saving sync.Mutex
}

func (web *Web) igdbTitles() *igdbTitleStore {
	web.igdbTitlesOnce.Do(func() {
		store := &igdbTitleStore{path: filepath.Join(web.dataFolder, IGDB_TITLES_FILENAME), games: map[string]IgdbTitle{}}
		if data, ok := durable.ReadStore(store.path); ok {
			durable.ParseStore(store.path, data, &store.games)
		}
		web.igdbTitlesStore = store
	})
	return web.igdbTitlesStore
}

func (s *igdbTitleStore) get(prefix string) (IgdbTitle, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	game, ok := s.games[prefix]
	return game, ok
}

func (s *igdbTitleStore) set(prefix string, game IgdbTitle) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.games[prefix] = game
}

func (s *igdbTitleStore) save() error {
	s.saving.Lock()
	defer s.saving.Unlock()
	s.mutex.Lock()
	data, err := json.Marshal(s.games)
	s.mutex.Unlock()
	if err != nil {
		return err
	}
	return durable.WriteFile(s.path, data)
}

// needsLookup tells a game never asked, not found a while ago, or asked by an older version of
// the app (see igdbTitleVersion).
func (s *igdbTitleStore) needsLookup(prefix string, now time.Time) bool {
	game, ok := s.get(prefix)
	return !ok || !game.Found && now.Sub(game.Checked) > igdbTitleRetry || game.Version < igdbTitleVersion
}

// igdbGenres are the genres and themes of IGDB in the genres of the app; the others (Indie...)
// are left out.
var igdbGenres = map[string]string{
	"Point-and-click": "Adventure", "Fighting": "Fighting", "Shooter": "Shooter", "Music": "Music", "Platform": "Platformer",
	"Puzzle": "Puzzle", "Racing": "Racing", "Real Time Strategy (RTS)": "Strategy", "Role-playing (RPG)": "RPG", "Simulator": "Simulation",
	"Sport": "Sports", "Strategy": "Strategy", "Turn-based strategy (TBS)": "Strategy", "Tactical": "Strategy",
	"Hack and slash/Beat 'em up": "Action", "Quiz/Trivia": "Party", "Pinball": "Arcade", "Adventure": "Adventure", "Arcade": "Arcade",
	"Visual Novel": "Adventure", "Card & Board Game": "Board Game", "MOBA": "Strategy", "Action": "Action", "Party": "Party",
}

// lookupTitle finds a game of the Switch on IGDB by its name, and what it tells of it.
func (c *igdbClient) lookupTitle(ctx context.Context, name string, now time.Time) (IgdbTitle, error) {
	return c.lookupTitleOn(ctx, name, igdbSwitchPlatform, now)
}

type igdbNamedField struct {
	Name string `json:"name"`
}

// igdbTitleGame is a game of IGDB with what the title of a game is made of.
type igdbTitleGame struct {
	Name             string `json:"name"`
	FirstReleaseDate int64  `json:"first_release_date"`
	ReleaseDates     []struct {
		Date     int64 `json:"date"`
		Platform int   `json:"platform"`
	} `json:"release_dates"`
	Summary string           `json:"summary"`
	Genres  []igdbNamedField `json:"genres"`
	Themes  []igdbNamedField `json:"themes"`
	Cover   struct {
		ImageId string `json:"image_id"`
	} `json:"cover"`
	Companies []struct {
		Company   igdbNamedField `json:"company"`
		Publisher bool           `json:"publisher"`
		Developer bool           `json:"developer"`
	} `json:"involved_companies"`
}

func (game igdbTitleGame) igdbName() string { return game.Name }

const igdbTitleFields = "name,first_release_date,release_dates.date,release_dates.platform,summary,genres.name,themes.name,cover.image_id," +
	"involved_companies.company.name,involved_companies.publisher,involved_companies.developer"

// lookupTitleOn is lookupTitle for the games of a platform of IGDB: the Switch, or the Switch 2.
func (c *igdbClient) lookupTitleOn(ctx context.Context, name string, platform int, now time.Time) (IgdbTitle, error) {
	return c.lookupTitleFor(ctx, name, "", platform, now)
}

// lookupTitleFor is lookupTitleOn for a game Wikidata may link to its game on IGDB (slug; see
// igdbFind).
func (c *igdbClient) lookupTitleFor(ctx context.Context, name string, slug string, platform int, now time.Time) (IgdbTitle, error) {
	result := IgdbTitle{Checked: now, Version: igdbTitleVersion}
	chosen, found, err := igdbFind[igdbTitleGame](ctx, c, igdbTitleFields, name, slug, platform)
	if err != nil || !found {
		return result, err
	}
	result.Found = true
	result.Summary = strings.TrimSpace(chosen.Summary)
	// the first day it came out on the console searched; the first one on any platform only when
	// IGDB gives no date of another platform, else a game of the Switch 2 that came out on the PC
	// years before would show that date (a game without a date of its console shows none)
	var first int64
	otherPlatform := false
	for _, release := range chosen.ReleaseDates {
		if release.Platform != platform {
			otherPlatform = otherPlatform || release.Date > 0
			continue
		}
		if release.Date > 0 && (first == 0 || release.Date < first) {
			first = release.Date
		}
	}
	if first == 0 && !otherPlatform {
		first = chosen.FirstReleaseDate
	}
	if first > 0 {
		date := time.Unix(first, 0).UTC()
		result.ReleaseDate = date.Year()*10000 + int(date.Month())*100 + date.Day()
	}
	seen := map[string]bool{}
	for _, genre := range append(chosen.Genres, chosen.Themes...) {
		if mapped, ok := igdbGenres[genre.Name]; ok && !seen[mapped] {
			seen[mapped] = true
			result.Genres = append(result.Genres, mapped)
		}
	}
	for _, company := range chosen.Companies {
		if company.Publisher && result.Publisher == "" {
			result.Publisher = company.Company.Name
		}
		if company.Developer && result.Developer == "" {
			result.Developer = company.Company.Name
		}
	}
	if chosen.Cover.ImageId != "" {
		result.Cover = "https://images.igdb.com/igdb/image/upload/t_cover_big/" + chosen.Cover.ImageId + ".jpg"
	}
	return result, nil
}

// nacpPublisher is the publisher a game gives in its files, in English first.
func nacpPublisher(local *db.SwitchGameFiles) string {
	if local == nil || local.File.Metadata == nil || local.File.Metadata.Ncap == nil {
		return ""
	}
	titles := local.File.Metadata.Ncap.TitleName
	for _, language := range []switchfs.Language{switchfs.AmericanEnglish, switchfs.BritishEnglish, switchfs.French, switchfs.German,
		switchfs.Spanish, switchfs.LatinAmericanSpanish, switchfs.Italian, switchfs.Dutch, switchfs.Portuguese, switchfs.Japanese} {
		if publisher := strings.TrimSpace(titles[language.String()].Publisher); publisher != "" {
			return publisher
		}
	}
	for _, title := range titles {
		if publisher := strings.TrimSpace(title.Publisher); publisher != "" {
			return publisher
		}
	}
	return ""
}

// withLibraryTitles is the titles database with what IGDB told of the games of the Switch 2 of
// the list of switchbrew (see withSwitch2Details), a title for each game of the library it does
// not list, made again on every scan (so a game removed takes its title away), and, last, what
// the user changed of the games by hand, which wins over all of it (see withTitleOverrides); the
// database itself is left as it is, as it is shared.
func (web *Web) withLibraryTitles(switchDB *db.SwitchTitlesDB, localDB *db.LocalSwitchFilesDB) *db.SwitchTitlesDB {
	return web.withTitleOverrides(web.addLibraryTitles(web.withSwitch2Details(switchDB), localDB))
}

// addLibraryTitles is the titles database with a title for each game of the library it does not
// list.
func (web *Web) addLibraryTitles(switchDB *db.SwitchTitlesDB, localDB *db.LocalSwitchFilesDB) *db.SwitchTitlesDB {
	if switchDB == nil || localDB == nil {
		return switchDB
	}
	added := map[string]*db.SwitchTitle{}
	for prefix, local := range localDB.TitlesMap {
		if !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		known, ok := switchDB.TitlesMap[prefix]
		if ok && !known.FromLibrary && usableName(known.Attributes.Name) {
			continue
		}
		added[prefix] = web.libraryTitle(prefix, local, known, switchDB.Versions[prefix])
	}
	before := false
	for _, title := range switchDB.TitlesMap {
		if title.FromLibrary {
			before = true
			break
		}
	}
	if len(added) == 0 && !before {
		return switchDB
	}
	titles := make(map[string]*db.SwitchTitle, len(switchDB.TitlesMap)+len(added))
	for prefix, title := range switchDB.TitlesMap {
		if !title.FromLibrary {
			titles[prefix] = title
		}
	}
	for prefix, title := range added {
		titles[prefix] = title
	}
	enriched := *switchDB
	enriched.TitlesMap = titles
	return &enriched
}

// libraryTitle is the title of a game of the library from what is known of it: what the
// database has (its DLC, maybe), the versions published, its own files and IGDB.
func (web *Web) libraryTitle(prefix string, local *db.SwitchGameFiles, known *db.SwitchTitle, versions map[int]string) *db.SwitchTitle {
	title := &db.SwitchTitle{Dlc: map[string]db.TitleAttributes{}, FromLibrary: true}
	if known != nil {
		// a title made before is made again from the database alone
		if !known.FromLibrary {
			title.Attributes, title.Updates, title.Dlc = known.Attributes, known.Updates, known.Dlc
		} else {
			title.Dlc = known.Dlc
		}
	}
	if title.Updates == nil {
		title.Updates = versions
	}
	attributes := &title.Attributes
	if attributes.Id == "" {
		attributes.Id = strings.ToUpper(local.File.Metadata.TitleId)
	}
	if !usableName(attributes.Name) {
		attributes.Name = getLocalTitleName(nil, local)
	}
	if attributes.Publisher == "" {
		attributes.Publisher = nacpPublisher(local)
	}
	if found, ok := web.igdbTitles().get(prefix); ok && found.Found {
		if len(attributes.Genres) == 0 {
			attributes.Genres = found.Genres
		}
		if attributes.ReleaseDate == 0 {
			attributes.ReleaseDate = found.ReleaseDate
		}
		if attributes.Publisher == "" {
			attributes.Publisher = found.Publisher
		}
		if attributes.Description == "" {
			attributes.Description = found.Summary
		}
		if attributes.IconUrl == "" {
			attributes.IconUrl = found.Cover
		}
	}
	return title
}

// libraryTitlesToLookUp are the games of the library made a title of, never asked to IGDB
// (or not found a while ago), by prefix with their name.
func (web *Web) libraryTitlesToLookUp(now time.Time) map[string]string {
	switchDB, _ := web.state.get()
	pending := map[string]string{}
	if switchDB == nil {
		return pending
	}
	store := web.igdbTitles()
	for prefix, title := range switchDB.TitlesMap {
		if title.FromLibrary && usableName(title.Attributes.Name) && store.needsLookup(prefix, now) {
			pending[prefix] = title.Attributes.Name
		}
	}
	return pending
}

// lookupLibraryTitles asks IGDB about the games of the library the titles database does not
// list, and about the games of the Switch 2 of the list of switchbrew, and puts what it tells in
// their titles.
func (web *Web) lookupLibraryTitles(ctx context.Context, client *igdbClient) error {
	pending := web.libraryTitlesToLookUp(time.Now())
	for prefix, name := range web.switch2TitlesToLookUp(time.Now()) {
		pending[prefix] = name
	}
	if len(pending) == 0 {
		return nil
	}
	store := web.igdbTitles()
	// the games of IGDB Wikidata links the games of the Switch 2 to
	slugs := web.switch2Slugs()
	// a game of the library IGDB gives a cover of, and that has none yet
	newCover := false
	_, localDB := web.state.get()
	for prefix, name := range pending {
		if web.state.IsSynchronizing() {
			break
		}
		// a game of the Switch 2 is searched among the games of the Switch 2
		platform, err := client.platformOf(ctx, prefix)
		found := IgdbTitle{}
		if err == nil {
			found, err = client.lookupTitleFor(ctx, name, slugs[prefix], platform, time.Now())
		}
		if err != nil {
			if errors.Is(err, errIgdbCredentials) {
				return err
			}
			web.sugarLogger.Debugf("IGDB lookup of the title %q failed: %s", name, err)
			continue
		}
		store.set(prefix, found)
		if localDB != nil && found.Found && found.Cover != "" {
			if local, ok := localDB.TitlesMap[prefix]; ok && local.BaseExist && local.Icon == "" {
				newCover = true
			}
		}
	}
	if err := store.save(); err != nil {
		return err
	}
	// the titles are made again with what IGDB told, of the databases as they are then: a
	// library replaced meanwhile (covers, a scan) is not written over with the one of before
	if web.state.update(web.withLibraryTitles) {
		web.invalidateDerived()
		// the new covers of the games of the library show now, not after the next scan
		if newCover {
			web.startCoverDownloads()
		}
	}
	return nil
}

// update replaces the titles database with what change makes of the current databases, under
// the lock, so nothing replaced meanwhile is lost. Nothing changes while a synchronization runs:
// it makes them again itself.
func (s *WebState) update(change func(*db.SwitchTitlesDB, *db.LocalSwitchFilesDB) *db.SwitchTitlesDB) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.isSynchronizing {
		return false
	}
	s.switchDB = change(s.switchDB, s.localDB)
	s.version++
	return true
}
