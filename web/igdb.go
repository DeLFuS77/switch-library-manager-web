package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// How long games take to beat comes from IGDB (igdb.com, by Twitch), free for non-commercial
// use. It is optional: each user creates a Twitch application and sets its Client ID and
// Secret, so no keys are shipped with the app. The times are looked up once per game in the
// background and kept in a file of the data folder, as IGDB asks.

const (
	TIMETOBEAT_FILENAME = "timetobeat.json"
	TASK_TIMETOBEAT     = "timetobeat"

	IGDB_CLIENT_ID_ENV     = "SLM_IGDB_CLIENT_ID"
	IGDB_CLIENT_SECRET_ENV = "SLM_IGDB_CLIENT_SECRET"

	// the Nintendo Switch in IGDB
	igdbSwitchPlatform = 130
	// IGDB allows 4 requests a second
	igdbRequestGap = 300 * time.Millisecond
	// games found are looked up again after a while (the times change as players report
	// them), and games not found sooner, in case they were added to IGDB
	timeToBeatFoundAge    = 90 * 24 * time.Hour
	timeToBeatNotFoundAge = 30 * 24 * time.Hour
)

var (
	igdbTokenUrl = "https://id.twitch.tv/oauth2/token"
	igdbApiUrl   = "https://api.igdb.com/v4/"
)

// TimeToBeat is how long a game takes to beat, in seconds, as reported by players on IGDB.
type TimeToBeat struct {
	Found      bool      `json:"found"`
	Checked    time.Time `json:"checked"`
	GameId     int       `json:"game,omitempty"`
	Slug       string    `json:"slug,omitempty"`
	Hastily    int       `json:"hastily,omitempty"`
	Normally   int       `json:"normally,omitempty"`
	Completely int       `json:"completely,omitempty"`
	Count      int       `json:"count,omitempty"`
}

// Known reports whether there is at least one time to show.
func (t TimeToBeat) Known() bool {
	return t.Found && (t.Hastily > 0 || t.Normally > 0 || t.Completely > 0)
}

// Main is the time of the story: the normal time, else the quick one.
func (t TimeToBeat) Main() int {
	if t.Normally > 0 {
		return t.Normally
	}
	return t.Hastily
}

// Url is the page of the game on IGDB, for the attribution.
func (t TimeToBeat) Url() string {
	if t.Slug == "" {
		return "https://www.igdb.com"
	}
	return "https://www.igdb.com/games/" + url.PathEscape(t.Slug)
}

// hoursText formats seconds as hours, rounded: "~12 h", "~1.5 h", "<1 h".
func hoursText(seconds int) string {
	hours := float64(seconds) / 3600
	switch {
	case seconds <= 0:
		return ""
	case hours < 1:
		return "<1 h"
	case hours < 10:
		return fmt.Sprintf("~%.1f h", hours)
	}
	return fmt.Sprintf("~%.0f h", hours)
}

type timeToBeatStore struct {
	mutex sync.Mutex
	path  string
	// by upper case title ID
	games map[string]TimeToBeat
}

func (web *Web) timesToBeat() *timeToBeatStore {
	web.timeToBeatOnce.Do(func() {
		store := &timeToBeatStore{path: filepath.Join(web.dataFolder, TIMETOBEAT_FILENAME), games: map[string]TimeToBeat{}}
		if data, err := os.ReadFile(store.path); err == nil {
			json.Unmarshal(data, &store.games)
		}
		web.timeToBeat = store
	})
	return web.timeToBeat
}

func (s *timeToBeatStore) get(id string) (TimeToBeat, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	value, ok := s.games[strings.ToUpper(id)]
	return value, ok
}

func (s *timeToBeatStore) set(id string, value TimeToBeat) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.games[strings.ToUpper(id)] = value
}

func (s *timeToBeatStore) snapshot() map[string]TimeToBeat {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	games := make(map[string]TimeToBeat, len(s.games))
	for id, value := range s.games {
		games[id] = value
	}
	return games
}

// hasKnown reports whether at least one game has a time to beat.
func (s *timeToBeatStore) hasKnown() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, value := range s.games {
		if value.Known() {
			return true
		}
	}
	return false
}

func (s *timeToBeatStore) save() error {
	s.mutex.Lock()
	data, err := json.Marshal(s.games)
	s.mutex.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}

// needsLookup reports whether a game is looked up: never looked up, or a while ago.
func (s *timeToBeatStore) needsLookup(id string, now time.Time) bool {
	value, ok := s.get(id)
	if !ok {
		return true
	}
	if value.Found {
		return now.Sub(value.Checked) > timeToBeatFoundAge
	}
	return now.Sub(value.Checked) > timeToBeatNotFoundAge
}

// igdbCredentials are the Client ID and Secret of the user's Twitch application: from the
// environment, else from the settings.
func igdbCredentials(dataFolder string) (string, string) {
	id, secret := strings.TrimSpace(os.Getenv(IGDB_CLIENT_ID_ENV)), strings.TrimSpace(os.Getenv(IGDB_CLIENT_SECRET_ENV))
	if id != "" && secret != "" {
		return id, secret
	}
	settingsObj := settings.ReadSettings(dataFolder)
	return strings.TrimSpace(settingsObj.IgdbClientId), strings.TrimSpace(settingsObj.IgdbClientSecret)
}

// igdbClient talks to IGDB, one request at a time and no faster than it allows.
type igdbClient struct {
	id, secret string
	http       *http.Client
	mutex      sync.Mutex
	token      string
	expires    time.Time
	last       time.Time
}

func newIgdbClient(id, secret string) *igdbClient {
	return &igdbClient{id: id, secret: secret, http: &http.Client{Timeout: 20 * time.Second}}
}

var errIgdbCredentials = errors.New("IGDB refused the Client ID or Secret")

func (c *igdbClient) authenticate(ctx context.Context) error {
	form := url.Values{"client_id": {c.id}, "client_secret": {c.secret}, "grant_type": {"client_credentials"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, igdbTokenUrl+"?"+form.Encode(), nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errIgdbCredentials
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("twitch answered %d", response.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&token); err != nil || token.AccessToken == "" {
		return errors.New("twitch sent no token")
	}
	c.token = token.AccessToken
	// a little before it ends
	c.expires = time.Now().Add(time.Duration(token.ExpiresIn)*time.Second - time.Minute)
	return nil
}

// query sends an Apicalypse query to an endpoint of IGDB and decodes the answer.
func (c *igdbClient) query(ctx context.Context, endpoint string, body string, result any) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if c.token == "" || time.Now().After(c.expires) {
			if err := c.authenticate(ctx); err != nil {
				return err
			}
		}
		if wait := igdbRequestGap - time.Since(c.last); wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		c.last = time.Now()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, igdbApiUrl+endpoint, bytes.NewBufferString(body))
		if err != nil {
			return err
		}
		request.Header.Set("Client-ID", c.id)
		request.Header.Set("Authorization", "Bearer "+c.token)
		request.Header.Set("Accept", "application/json")
		response, err := c.http.Do(request)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if err != nil {
			return err
		}
		switch response.StatusCode {
		case http.StatusOK:
			return json.Unmarshal(data, result)
		case http.StatusUnauthorized:
			// the token ended: once more with a new one
			c.token = ""
			continue
		case http.StatusTooManyRequests:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}
		return fmt.Errorf("IGDB answered %d", response.StatusCode)
	}
	return errors.New("IGDB did not answer")
}

// apicalypseText makes a name safe inside the quotes of a query.
func apicalypseText(text string) string {
	return strings.NewReplacer(`\`, " ", `"`, " ").Replace(text)
}

// lookup finds a game of the Switch on IGDB by its name, and how long it takes to beat.
func (c *igdbClient) lookup(ctx context.Context, name string, now time.Time) (TimeToBeat, error) {
	result := TimeToBeat{Checked: now}
	var games []struct {
		Id   int    `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	query := fmt.Sprintf(`search "%s"; fields id,name,slug; where platforms = (%d); limit 5;`, apicalypseText(name), igdbSwitchPlatform)
	if err := c.query(ctx, "games", query, &games); err != nil {
		return result, err
	}
	if len(games) == 0 {
		return result, nil
	}
	// the same name first, else the best match of IGDB
	chosen := games[0]
	wanted := searchText(name)
	for _, game := range games {
		if searchText(game.Name) == wanted {
			chosen = game
			break
		}
	}
	result.Found, result.GameId, result.Slug = true, chosen.Id, chosen.Slug

	var times []struct {
		Hastily    int `json:"hastily"`
		Normally   int `json:"normally"`
		Completely int `json:"completely"`
		Count      int `json:"count"`
	}
	if err := c.query(ctx, "game_time_to_beats", fmt.Sprintf(`fields hastily,normally,completely,count; where game_id = %d;`, chosen.Id), &times); err != nil {
		return result, err
	}
	if len(times) > 0 {
		result.Hastily, result.Normally, result.Completely, result.Count = times[0].Hastily, times[0].Normally, times[0].Completely, times[0].Count
	}
	return result, nil
}

// lookupTimesToBeat looks up the games of the library that were not looked up yet, in the
// background, as a task. Nothing happens without the IGDB credentials.
func (web *Web) lookupTimesToBeat(trigger string) bool {
	id, secret := igdbCredentials(web.dataFolder)
	if id == "" || secret == "" || isDemoMode() {
		return false
	}
	if !web.timeToBeatRunning.CompareAndSwap(false, true) {
		return false
	}
	store := web.timesToBeat()
	now := time.Now()
	type pending struct{ id, name string }
	games := []pending{}
	for _, item := range web.derived("library:"+DEFAULT_LANGUAGE, func() any { return web.buildLibrary(DEFAULT_LANGUAGE) }).([]TitleItem) {
		name := item.OriginalName
		if name == "" {
			name = item.Name
		}
		if name != "" && !item.Demo && store.needsLookup(item.Id, now) {
			games = append(games, pending{item.Id, name})
		}
	}
	if len(games) == 0 {
		web.timeToBeatRunning.Store(false)
		return false
	}

	taskId := web.taskLog().Start(TASK_TIMETOBEAT, trigger)
	web.backgroundWork.Add(1)
	go func() {
		defer web.backgroundWork.Add(-1)
		defer web.timeToBeatRunning.Store(false)
		client := newIgdbClient(id, secret)
		ctx := context.Background()
		var failure *TaskNote
		found := 0
		for i, game := range games {
			// a scan replaces the library: the rest waits for the next run
			if web.state.IsSynchronizing() {
				break
			}
			web.taskLog().Progress(taskId, i, len(games), game.name)
			value, err := client.lookup(ctx, game.name, time.Now())
			if errors.Is(err, errIgdbCredentials) {
				failure = &TaskNote{Text: NOTE_IGDB_CREDENTIALS}
				break
			}
			if err != nil {
				web.sugarLogger.Debugf("IGDB lookup of %q failed: %s", game.name, err)
				continue
			}
			store.set(game.id, value)
			if value.Known() {
				found++
			}
			if i%20 == 19 {
				store.save()
			}
		}
		web.taskLog().Progress(taskId, len(games), len(games), "")
		web.taskLog().SetResult(taskId, found, len(games))
		if err := store.save(); err != nil && failure == nil {
			failure = &TaskNote{Text: err.Error()}
		}
		web.invalidateDerived()
		web.taskLog().Finish(taskId, failure)
	}()
	return true
}

// NOTE_IGDB_CREDENTIALS is shown on the task when Twitch refuses the keys.
const NOTE_IGDB_CREDENTIALS = "Twitch refused the IGDB Client ID or Secret. Check them in Settings."
