package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/disintegration/gift"
	"github.com/gorilla/mux"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// An administrator can change by hand the name, the cover and the description of a game, on its
// page: what the titles database, its translations, the files of the game and IGDB tell is then
// left aside. The changes are kept apart, by title ID, and put on the titles each time they are
// made again (see withLibraryTitles), so neither a synchronization nor a scan loses them, and a
// game deleted finds them again when it comes back. The files of the game and its title ID are
// never touched; Organize names them with the name given when it is asked to.

const (
	// TITLE_OVERRIDES_FILENAME holds the changes, by upper case title ID of the game
	TITLE_OVERRIDES_FILENAME = "overrides.json"
	// OVERRIDE_COVERS_FOLDER holds the covers chosen, named with the title ID of their game
	OVERRIDE_COVERS_FOLDER = "covers"
	// the longest name and description kept, in characters
	maxOverrideName        = 200
	maxOverrideDescription = 10000
	// the largest cover accepted, in bytes and in pixels a side; it is kept at most
	// overrideCoverSize pixels a side, as large as the covers of the titles database
	maxOverrideCoverBytes = 5 << 20
	maxOverrideCoverSide  = 6000
	overrideCoverSize     = 1024
	overrideCoverQuality  = 90
)

// TitleOverride is what the user changed of a game by hand; what is empty stays as it was.
type TitleOverride struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// the file of the cover chosen, in OVERRIDE_COVERS_FOLDER
	Cover   string    `json:"cover,omitempty"`
	Changed time.Time `json:"changed"`
}

func (o TitleOverride) empty() bool {
	return o.Name == "" && o.Description == "" && o.Cover == ""
}

// overrideCoverName matches the files of the covers chosen: the title ID of the game and a part
// of the hash of the image, so a new cover is never taken for the one before by a browser
var overrideCoverName = regexp.MustCompile(`^[0-9A-F]{16}-[0-9a-f]{8}\.(jpg|png)$`)

// errors shown to the user, translated by the interface
var (
	errOverrideUnknown    = errorString("The titles database and the library do not know this game.")
	errOverrideNotImage   = errorString("The cover must be a JPEG, PNG or GIF image.")
	errOverrideCoverLarge = errorString("The cover is too large: at most 5 MB and 6000 pixels a side.")
	errOverrideCoverUrl   = errorString("The cover could not be downloaded from that address.")
	errOverrideSave       = errorString("The change could not be saved in the data folder.")
)

type titleOverrides struct {
	mutex  sync.Mutex
	path   string
	titles map[string]TitleOverride
}

func (web *Web) titleOverrides() *titleOverrides {
	web.overridesOnce.Do(func() {
		store := &titleOverrides{path: filepath.Join(web.dataFolder, TITLE_OVERRIDES_FILENAME)}
		store.reload()
		web.overrides = store
	})
	return web.overrides
}

// reload reads the file again (a backup was restored).
func (o *titleOverrides) reload() {
	titles := map[string]TitleOverride{}
	if data, ok := readStore(o.path); ok {
		parseStore(o.path, data, &titles)
	}
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.titles = map[string]TitleOverride{}
	for id, override := range titles {
		o.titles[strings.ToUpper(id)] = override
	}
}

// snapshot returns the changes of every game.
func (o *titleOverrides) snapshot() map[string]TitleOverride {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	result := make(map[string]TitleOverride, len(o.titles))
	for id, override := range o.titles {
		result[id] = override
	}
	return result
}

func (o *titleOverrides) get(id string) (TitleOverride, bool) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	override, ok := o.titles[strings.ToUpper(id)]
	return override, ok
}

// set keeps what was changed of a game, or forgets it when nothing is left; it returns what was
// kept before.
func (o *titleOverrides) set(id string, override TitleOverride) (TitleOverride, error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	id = strings.ToUpper(id)
	previous := o.titles[id]
	titles := make(map[string]TitleOverride, len(o.titles)+1)
	for other, kept := range o.titles {
		titles[other] = kept
	}
	if override.empty() {
		delete(titles, id)
	} else {
		titles[id] = override
	}
	data, err := json.Marshal(titles)
	if err != nil {
		return previous, err
	}
	if err := writeFileAtomic(o.path, data); err != nil {
		return previous, err
	}
	o.titles = titles
	return previous, nil
}

// withTitleOverrides is the titles database with what the user changed of the games by hand,
// which wins over the rest; the database itself is left as it is. A cover whose file is gone (a
// backup restored without it) is left out.
func (web *Web) withTitleOverrides(switchDB *db.SwitchTitlesDB) *db.SwitchTitlesDB {
	if switchDB == nil {
		return nil
	}
	overrides := web.titleOverrides().snapshot()
	if len(overrides) == 0 && len(switchDB.Edited) == 0 {
		return switchDB
	}
	edited := make(map[string]db.EditedTitle, len(overrides))
	for id, override := range overrides {
		title := db.EditedTitle{Name: override.Name, Description: override.Description, Cover: override.Cover}
		if title.Cover != "" {
			if _, err := os.Stat(web.overrideCoverPath(title.Cover)); err != nil {
				title.Cover = ""
			}
		}
		edited[id] = title
	}
	result := *switchDB
	result.Edited = edited
	return &result
}

// applyTitleOverrides shows the changes at once; during a synchronization they are shown when it
// ends, as it makes the titles again.
func (web *Web) applyTitleOverrides() {
	if web.state.update(web.withLibraryTitles) {
		web.invalidateDerived()
	}
}

// editedCover is the address of the cover the user chose for the game of a title ID (one of its
// updates or DLC too), or "".
func editedCover(switchDB *db.SwitchTitlesDB, titleId string) string {
	prefix, err := db.TitleIDPrefix(titleId)
	if err != nil {
		return ""
	}
	if edited, ok := switchDB.EditedTitle(strings.ToUpper(prefix) + "000"); ok && edited.Cover != "" {
		return "/i/" + edited.Cover
	}
	return ""
}

// gameIcon is the file of the cover of a game of the library: the one the user chose, else the
// one found for it; "" when it has none yet.
func gameIcon(switchDB *db.SwitchTitlesDB, prefix string, local *db.SwitchGameFiles) string {
	if edited, ok := switchDB.EditedTitle(strings.ToUpper(prefix) + "000"); ok && edited.Cover != "" {
		return edited.Cover
	}
	return local.Icon
}

func (web *Web) overrideCoverPath(name string) string {
	return filepath.Join(web.dataFolder, OVERRIDE_COVERS_FOLDER, name)
}

// cleanOverrideText is a text written by the user without the characters that are not text
// (controls, invisible ones), cut to at most limit characters; a name is kept on one line.
func cleanOverrideText(text string, limit int, oneLine bool) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var clean strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			if oneLine {
				r = ' '
			}
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar:
			continue
		}
		clean.WriteRune(r)
	}
	text = clean.String()
	if oneLine {
		text = strings.Join(strings.Fields(text), " ")
	} else {
		text = strings.TrimSpace(text)
	}
	if runes := []rune(text); len(runes) > limit {
		text = strings.TrimSpace(string(runes[:limit]))
	}
	return text
}

// normalizeCover checks that data is a real image (JPEG, PNG or a GIF, of which the first frame
// is kept) and makes it again: at most overrideCoverSize pixels a side, in JPEG, or in PNG when
// it has transparent parts. What else the file held (a script hidden in it, where and when a
// photo was taken) is left out. It returns the image and its extension.
func normalizeCover(data []byte) ([]byte, string, error) {
	if len(data) > maxOverrideCoverBytes {
		return nil, "", errOverrideCoverLarge
	}
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png", "image/gif":
	default:
		return nil, "", errOverrideNotImage
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return nil, "", errOverrideNotImage
	}
	if config.Width > maxOverrideCoverSide || config.Height > maxOverrideCoverSide {
		return nil, "", errOverrideCoverLarge
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errOverrideNotImage
	}
	result := source
	if bounds := source.Bounds(); bounds.Dx() > overrideCoverSize || bounds.Dy() > overrideCoverSize {
		filter := gift.New(gift.ResizeToFit(overrideCoverSize, overrideCoverSize, gift.LanczosResampling))
		resized := image.NewNRGBA(filter.Bounds(bounds))
		filter.Draw(resized, source)
		result = resized
	}
	var encoded bytes.Buffer
	if opaque, ok := result.(interface{ Opaque() bool }); ok && !opaque.Opaque() {
		if err := png.Encode(&encoded, result); err != nil {
			return nil, "", err
		}
		return encoded.Bytes(), "png", nil
	}
	if err := jpeg.Encode(&encoded, result, &jpeg.Options{Quality: overrideCoverQuality}); err != nil {
		return nil, "", err
	}
	return encoded.Bytes(), "jpg", nil
}

// saveOverrideCover keeps the cover chosen for the game of id, named with its ID, and makes its
// thumbnail; it returns the name of its file.
func (web *Web) saveOverrideCover(id string, data []byte) (string, error) {
	encoded, extension, err := normalizeCover(data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	name := strings.ToUpper(id) + "-" + hex.EncodeToString(sum[:4]) + "." + extension
	path := web.overrideCoverPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, encoded); err != nil {
		return "", err
	}
	if err := web.thumbnails().make(path, filepath.Join(web.dataFolder, "img", "thumbs", name)); err != nil {
		web.sugarLogger.Debugf("No thumbnail for the cover chosen %s: %v", name, err)
	}
	return name, nil
}

// removeOverrideCover deletes a cover chosen before, and its thumbnail.
func (web *Web) removeOverrideCover(name string) {
	if !overrideCoverName.MatchString(name) {
		return
	}
	os.Remove(web.overrideCoverPath(name))
	os.Remove(filepath.Join(web.dataFolder, "img", "thumbs", name))
}

// removeUnusedOverrideCovers deletes the covers no change uses any more (a backup restored).
func (web *Web) removeUnusedOverrideCovers() {
	used := map[string]bool{}
	for _, override := range web.titleOverrides().snapshot() {
		used[override.Cover] = true
	}
	entries, _ := os.ReadDir(filepath.Join(web.dataFolder, OVERRIDE_COVERS_FOLDER))
	for _, entry := range entries {
		if !entry.IsDir() && !used[entry.Name()] {
			web.removeOverrideCover(entry.Name())
		}
	}
}

// downloadOverrideCover gets once the cover at an address the user gave: from the internet only,
// as the covers of the titles database (see db.DownloadImage).
func downloadOverrideCover(address string) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, errOverrideCoverUrl
	}
	data, err := db.DownloadImage(parsed.String(), maxOverrideCoverBytes)
	if errors.Is(err, db.ErrImageTooLarge) {
		return nil, errOverrideCoverLarge
	}
	if err != nil {
		return nil, errOverrideCoverUrl
	}
	return data, nil
}

// ApiTitleOverride is what was changed of a game by hand, for the API.
type ApiTitleOverride struct {
	Id          string    `json:"id"`
	Edited      bool      `json:"edited"`
	Name        string    `json:"name,omitempty"`
	Description string    `json:"description,omitempty"`
	CoverUrl    string    `json:"coverUrl,omitempty"`
	Changed     time.Time `json:"changed,omitzero"`
}

func apiTitleOverride(id string, override TitleOverride, ok bool) ApiTitleOverride {
	result := ApiTitleOverride{Id: id, Edited: ok && !override.empty(), Name: override.Name, Description: override.Description, Changed: override.Changed}
	if override.Cover != "" {
		result.CoverUrl = "/i/" + override.Cover
	}
	return result
}

var errOverrideId = errorString("Invalid Title ID (16 hexadecimal characters)")

// overrideGameId is the title ID of the game a request is about (its update or DLC tell the same
// game), when the titles database or the library knows it.
func (web *Web) overrideGameId(r *http.Request) (string, error) {
	id := strings.ToUpper(strings.TrimSpace(mux.Vars(r)["titleId"]))
	if !titleIdPattern.MatchString(id) {
		return "", errOverrideId
	}
	prefix, err := db.TitleIDPrefix(id)
	if err != nil {
		return "", errOverrideId
	}
	switchDB, localDB := web.state.get()
	known := switchDB != nil && switchDB.TitlesMap[prefix] != nil
	if localDB != nil && localDB.TitlesMap[prefix] != nil {
		known = true
	}
	// a change kept of a game deleted can still be undone
	if _, kept := web.titleOverrides().get(strings.ToUpper(prefix) + "000"); !known && !kept {
		return "", errOverrideUnknown
	}
	return strings.ToUpper(prefix) + "000", nil
}

// writeOverrideIdError answers a request about a title ID that is not one, or of a game unknown.
func writeOverrideIdError(w http.ResponseWriter, lang string, err error) {
	status := http.StatusNotFound
	if errors.Is(err, errOverrideId) {
		status = http.StatusBadRequest
	}
	writeGlobalError(w, status, lang, err.Error())
}

func (web *Web) handleTitleOverrideGet(w http.ResponseWriter, r *http.Request) {
	id, err := web.overrideGameId(r)
	if err != nil {
		writeOverrideIdError(w, web.requestLanguage(r), err)
		return
	}
	override, ok := web.titleOverrides().get(id)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiTitleOverride(id, override, ok))
}

// handleTitleOverrideSet keeps the name and the description sent (empty: those of the game), and
// the cover sent as a file or as an address; without one the cover chosen before stays, unless
// removeCover asks for the one of the game again.
func (web *Web) handleTitleOverrideSet(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	id, err := web.overrideGameId(r)
	if err != nil {
		writeOverrideIdError(w, lang, err)
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(maxOverrideCoverBytes + 1<<20); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, errOverrideCoverLarge.Error())
			return
		}
	}
	previous, _ := web.titleOverrides().get(id)
	override := TitleOverride{
		Name:        cleanOverrideText(r.FormValue("name"), maxOverrideName, true),
		Description: cleanOverrideText(r.FormValue("description"), maxOverrideDescription, false),
		Cover:       previous.Cover,
		Changed:     time.Now().UTC(),
	}
	if r.FormValue("removeCover") == "true" {
		override.Cover = ""
	}
	var cover []byte
	if file, _, err := r.FormFile("cover"); err == nil {
		cover, err = io.ReadAll(io.LimitReader(file, maxOverrideCoverBytes+1))
		file.Close()
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, errOverrideNotImage.Error())
			return
		}
	} else if address := strings.TrimSpace(r.FormValue("coverUrl")); address != "" {
		if cover, err = downloadOverrideCover(address); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, err.Error())
			return
		}
	}
	if cover != nil {
		name, err := web.saveOverrideCover(id, cover)
		if err != nil {
			status, message := http.StatusBadRequest, err.Error()
			if !errors.Is(err, errOverrideNotImage) && !errors.Is(err, errOverrideCoverLarge) {
				web.sugarLogger.Errorf("The cover chosen for %s could not be saved: %v", id, err)
				status, message = http.StatusInternalServerError, errOverrideSave.Error()
			}
			writeGlobalError(w, status, lang, message)
			return
		}
		override.Cover = name
	}
	if _, err := web.titleOverrides().set(id, override); err != nil {
		web.sugarLogger.Errorf("The change of %s could not be saved: %v", id, err)
		if override.Cover != previous.Cover {
			web.removeOverrideCover(override.Cover)
		}
		writeGlobalError(w, http.StatusInternalServerError, lang, errOverrideSave.Error())
		return
	}
	if previous.Cover != "" && previous.Cover != override.Cover {
		web.removeOverrideCover(previous.Cover)
	}
	web.applyTitleOverrides()
	kept, ok := web.titleOverrides().get(id)
	writeJSON(w, http.StatusOK, apiTitleOverride(id, kept, ok))
}

// handleTitleOverrideDelete forgets what was changed of a game: it is shown as the titles
// database and its files tell again.
func (web *Web) handleTitleOverrideDelete(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	id, err := web.overrideGameId(r)
	if err != nil {
		writeOverrideIdError(w, lang, err)
		return
	}
	previous, err := web.titleOverrides().set(id, TitleOverride{})
	if err != nil {
		web.sugarLogger.Errorf("The change of %s could not be undone: %v", id, err)
		writeGlobalError(w, http.StatusInternalServerError, lang, errOverrideSave.Error())
		return
	}
	web.removeOverrideCover(previous.Cover)
	web.applyTitleOverrides()
	writeJSON(w, http.StatusOK, apiTitleOverride(id, TitleOverride{}, false))
}
