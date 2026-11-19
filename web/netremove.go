package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

// Removing a game from a console, from the web (the page Consoles).
//
//   - POST /api/install/consoles/{id}/remove with {"titleId": the ID of the application} asks a
//     console of the user to remove a game installed on it. It waits with what is sent to the
//     console (GET /install/push/wait) as an item with "action": "remove", the "titleId" and the
//     name of the game ("game"), and is noted in the activity. The same users who send to a
//     console ask it, of their own consoles only.
//   - NXDock asks on the console before removing it, copies its saves to the library first, and
//     tells the answer as for an install (POST /install/push/answer), with "result": "removed",
//     "failed" or "missing" (it was not installed). The answer is noted in the activity, and the
//     game leaves what is known to be installed on the console once it is not there.

var (
	errNetRemoveTitle        = errors.New("choose a game installed on the console")
	errNetRemoveNotInstalled = errors.New("the game is not installed on the console")
)

// netRemoveMissingPhrase ends the detail of the activity of a game a console did not find to remove
// (see activityPhrases)
const netRemoveMissingPhrase = ", it was not on the console"

// netGameLooks are the games of the library and of the titles database by the prefix of their
// title IDs, with their names and covers as the Library shows them.
func (web *Web) netGameLooks(lang string) map[string]TitleItem {
	return web.derived("consoleLooks:"+lang, func() any {
		items := web.derived("library+missing:"+lang, func() any { return web.buildLibraryWithMissing(lang) }).([]TitleItem)
		looks := make(map[string]TitleItem, len(items))
		for _, item := range items {
			prefix := netGamePrefix(item.Id)
			if prefix == "" {
				continue
			}
			// the copy the library has goes before the ones it does not have
			if known, ok := looks[prefix]; ok && (!known.Missing || item.Missing) {
				continue
			}
			looks[prefix] = item
		}
		return looks
	}).(map[string]TitleItem)
}

// netGameName is the name of a game by the prefix of its title ID, as the library has it: its title
// ID when nothing knows it.
func (web *Web) netGameName(prefix string) string {
	if look, ok := web.netGameLooks(i18n.DEFAULT_LANGUAGE)[prefix]; ok && look.Name != "" {
		return look.Name
	}
	return strings.ToUpper(prefix) + "000"
}

// handleNetConsoleRemove asks a console of the user to remove a game installed on it.
func (web *Web) handleNetConsoleRemove(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, errNetPushNotAllow.Error())
		return
	}
	if !netInstallEnabled(settings.ReadSettings(web.dataFolder)) {
		writeGlobalError(w, http.StatusConflict, lang, netPushOffMessage)
		return
	}
	console, ok := web.netUserConsole(principal.Name, mux.Vars(r)["id"])
	if !ok {
		writeGlobalError(w, http.StatusNotFound, lang, errNetPushConsole.Error())
		return
	}
	var body struct {
		TitleId string `json:"titleId"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	titleId := strings.ToUpper(strings.TrimSpace(body.TitleId))
	if kind, _ := db.ContentKind(titleId); err != nil || !netTitleIdPattern.MatchString(titleId) || kind != db.KIND_BASE {
		writeGlobalError(w, http.StatusBadRequest, lang, errNetRemoveTitle.Error())
		return
	}
	inventory, ok := web.readNetInventory(console.ID)
	if !ok {
		writeGlobalError(w, http.StatusConflict, lang, errNetConsoleUnknown.Error())
		return
	}
	prefix := netGamePrefix(titleId)
	if netGameInInventory(inventory, prefix) == nil {
		writeGlobalError(w, http.StatusConflict, lang, errNetRemoveNotInstalled.Error())
		return
	}
	name := web.netGameName(prefix)
	// asked already: once is enough
	if web.netPushes.removals(console.ID)[titleId] {
		writeJSON(w, http.StatusOK, map[string]any{"state": "asked", "console": console.Name, "game": name})
		return
	}
	if web.netPushes.waiting(console.ID)+web.netCopyWaits.count(console.ID) >= netPushMax {
		writeGlobalError(w, http.StatusTooManyRequests, lang, errNetPushFull.Error())
		return
	}
	random := make([]byte, 16)
	rand.Read(random)
	now := time.Now()
	push := &netPush{id: hex.EncodeToString(random), keyId: console.ID, keyName: console.Name, owner: principal.Name, game: name, made: now,
		item: netPushItem{Action: netPushRemove, TitleId: titleId, Game: name, Name: name}}
	if err := web.netPushes.add([]*netPush{push}); err != nil {
		writeGlobalError(w, http.StatusTooManyRequests, lang, err.Error())
		return
	}
	web.sugarLogger.Infof("Network install: %s asked the console %s to remove %s", principal.Name, console.Name, name)
	if !isDemoMode() {
		web.activities().Add(activity.Entry{Time: now, User: principal.Name, Action: activity.ACTION_NET_REMOVE,
			Detail: quotedKeyName(console.Name) + ": " + name, IP: clientIp(r)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "asked", "console": console.Name, "game": name})
}

// netRemoveAnswered notes what a console did with a game it was asked to remove: declined it,
// removed it, could not, or did not find it; a game no longer there leaves what is known to be
// installed on the console.
func (web *Web) netRemoveAnswered(r *http.Request, key *Principal, push *netPush, accepted bool, result string) {
	action, word, detail := activity.ACTION_NET_REMOVE_NO, "declined to remove", quotedKeyName(key.KeyName)+": "+push.item.Game
	if accepted {
		switch result {
		// an answer without a result accepted it all the same
		case netRemoveDone, "":
			action, word = activity.ACTION_NET_REMOVE_YES, "removed"
		case netRemoveMissing:
			action, word, detail = activity.ACTION_NET_REMOVE_FAIL, "did not find", detail+netRemoveMissingPhrase
		default:
			action, word = activity.ACTION_NET_REMOVE_FAIL, "could not remove"
		}
		if result == netRemoveDone || result == netRemoveMissing {
			if err := web.netInventoryDrop(key.KeyID, netGamePrefix(push.item.TitleId)); err != nil {
				web.sugarLogger.Warnf("Network install: what is installed on the console %s could not be kept without %s: %v", key.KeyName, push.item.Game, err)
			}
		}
	}
	web.sugarLogger.Infof("Network install: the console %s (%s) %s %s", key.KeyName, key.Name, word, push.item.Game)
	if !isDemoMode() {
		web.activities().Add(activity.Entry{Time: time.Now(), User: key.Name, Action: action, Detail: detail, IP: clientIp(r)})
	}
}

// netInventoryDrop leaves a game (by the prefix of its title ID) out of what is known to be
// installed on a console, with its update and DLC; the time it was played stays.
func (web *Web) netInventoryDrop(keyId string, prefix string) error {
	if prefix == "" {
		return nil
	}
	web.netInventories.Lock()
	defer web.netInventories.Unlock()
	inventory, ok := web.readNetInventory(keyId)
	if !ok {
		return nil
	}
	kept := inventory.Items[:0]
	for _, item := range inventory.Items {
		if netGamePrefix(item.TitleId) != prefix {
			kept = append(kept, item)
		}
	}
	if len(kept) == len(inventory.Items) {
		return nil
	}
	inventory.Items = kept
	data, err := json.Marshal(inventory)
	if err != nil {
		return err
	}
	if _, err := os.Stat(web.netInventoryPath(keyId)); err != nil {
		return err
	}
	return durable.WriteFile(web.netInventoryPath(keyId), data)
}
