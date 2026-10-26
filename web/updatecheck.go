package web

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	// how often GitHub is asked for the latest release
	updateCheckInterval = 24 * time.Hour
	releaseNotesLength  = 600
)

// latestReleaseUrl is the GitHub API of the latest release; a variable for tests.
var latestReleaseUrl = "https://api.github.com/repos/" + settings.REPOSITORY_OWNER + "/switch-library-manager-web/releases/latest"

// AppUpdate is a newer version of the app than the running one.
type AppUpdate struct {
	Version string
	Url     string
	Notes   string
	// the files of the release for this system, to update by itself (see selfupdate.go)
	files releaseFiles
}

// CanInstall tells whether the app can install this version by itself.
func (u *AppUpdate) CanInstall() bool {
	return u.files.archiveUrl != "" && selfUpdatable()
}

// updateChecker remembers the latest release found on GitHub.
type updateChecker struct {
	mutex   sync.Mutex
	latest  *AppUpdate
	checked time.Time
}

var appUpdateClient = &http.Client{Timeout: 15 * time.Second}

// newerVersion reports whether version (e.g. 1.12.0) is newer than current.
func newerVersion(version string, current string) bool {
	parse := func(text string) []int {
		parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(text), "v"), ".")
		numbers := make([]int, 3)
		for i := 0; i < len(parts) && i < 3; i++ {
			numbers[i], _ = strconv.Atoi(parts[i])
		}
		return numbers
	}
	a, b := parse(version), parse(current)
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// checkForUpdate asks GitHub for the latest release. Only the version of the app is
// sent, in the user agent, as GitHub requires one.
func (web *Web) checkForUpdate() {
	request, err := http.NewRequest(http.MethodGet, latestReleaseUrl, nil)
	if err != nil {
		return
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "switch-library-manager-web/"+settings.SLM_WEB_VERSION)
	response, err := appUpdateClient.Do(request)
	if err != nil {
		web.sugarLogger.Debugf("Checking for a new version failed: %v", err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return
	}
	release := struct {
		TagName string `json:"tag_name"`
		HtmlUrl string `json:"html_url"`
		Body    string `json:"body"`
		Draft   bool   `json:"draft"`
		Pre     bool   `json:"prerelease"`
		Assets  []struct {
			Name string `json:"name"`
			Url  string `json:"browser_download_url"`
		} `json:"assets"`
	}{}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil || release.Draft || release.Pre {
		return
	}
	// links must stay on GitHub
	if !strings.HasPrefix(release.HtmlUrl, "https://github.com/") {
		release.HtmlUrl = "https://github.com/" + settings.REPOSITORY_OWNER + "/switch-library-manager-web/releases"
	}

	web.updates.mutex.Lock()
	defer web.updates.mutex.Unlock()
	web.updates.checked = time.Now()
	web.updates.latest = nil
	if newerVersion(release.TagName, settings.SLM_WEB_VERSION) {
		notes := strings.TrimSpace(release.Body)
		// cut at a character, not in the middle of one written with several bytes
		if runes := []rune(notes); len(runes) > releaseNotesLength {
			notes = string(runes[:releaseNotesLength]) + "…"
		}
		assets := map[string]string{}
		for _, asset := range release.Assets {
			assets[asset.Name] = asset.Url
		}
		web.updates.latest = &AppUpdate{Version: strings.TrimPrefix(release.TagName, "v"), Url: release.HtmlUrl, Notes: notes}
		if files, ok := filesOfRelease(assets, systemArchive(runtime.GOOS, runtime.GOARCH)); ok {
			web.updates.latest.files = files
		}
		web.sugarLogger.Infof("Version %s is available", release.TagName)
	}
}

// availableUpdate returns the newer version found, or nil.
func (web *Web) availableUpdate() *AppUpdate {
	if !settings.ReadSettings(web.dataFolder).CheckForUpdates {
		return nil
	}
	web.updates.mutex.Lock()
	defer web.updates.mutex.Unlock()
	return web.updates.latest
}

// StartUpdateChecker checks for a new version shortly after the start and then daily.
func (web *Web) StartUpdateChecker() {
	go func() {
		time.Sleep(time.Minute)
		for {
			if settings.ReadSettings(web.dataFolder).CheckForUpdates {
				web.checkForUpdate()
			}
			time.Sleep(updateCheckInterval)
		}
	}()
}
