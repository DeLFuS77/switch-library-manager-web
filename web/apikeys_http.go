package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

// APIKeyCreated is the answer to a new API key: the only one that holds the key.
type APIKeyCreated struct {
	KeyRow
	Key string `json:"key"`
}

// apiKeyStatus is the status of the answer to an error of the API keys.
func apiKeyStatus(err error) int {
	switch {
	case errors.Is(err, ErrAPIKeyNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrAPIKeyFromAPIKey):
		return http.StatusForbidden
	case errors.Is(err, ErrAPIKeyName), errors.Is(err, ErrAPIKeyRole), errors.Is(err, ErrAPIKeyCount), errors.Is(err, ErrAPIKeyAccount):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// keyOwner is the user whose keys a request makes or revokes: one of users.json, signed in with
// a session or a password. A key cannot make keys, or a stolen key of read only would make one of
// its administrator.
func (web *Web) keyOwner(r *http.Request) (*Principal, error) {
	principal := principalFrom(r)
	if principal.Source == "key" {
		return nil, ErrAPIKeyFromAPIKey
	}
	if principal.Name == "" || principal.Source == "env" {
		return nil, ErrAPIKeyAccount
	}
	if _, ok := web.auth.users.Get(principal.Name); !ok {
		return nil, ErrAPIKeyAccount
	}
	return principal, nil
}

func (web *Web) handleAPIKeys() {
	// the keys of the user; with all=true an administrator gets the keys of everybody
	web.router.HandleFunc("/api/keys", func(w http.ResponseWriter, r *http.Request) {
		principal := principalFrom(r)
		owner := principal.Name
		if r.URL.Query().Get("all") == "true" && principal.IsAdmin() {
			owner = ""
		} else if owner == "" {
			writeJSON(w, http.StatusOK, []KeyRow{})
			return
		}
		writeJSON(w, http.StatusOK, web.auth.users.KeyRows(owner))
	}).Methods("GET")

	web.router.HandleFunc("/api/keys", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		principal, err := web.keyOwner(r)
		if err != nil {
			writeGlobalError(w, apiKeyStatus(err), lang, err.Error())
			return
		}
		role := r.FormValue("role")
		if role == "" {
			role = ROLE_VIEWER
		}
		secret, key, err := web.auth.users.AddAPIKey(principal.Name, r.FormValue("name"), role)
		if err != nil {
			writeGlobalError(w, apiKeyStatus(err), lang, err.Error())
			return
		}
		web.sugarLogger.Infof("API key %q of %q created", key.Name, principal.Name)
		web.activities().add(Activity{Time: time.Now(), User: principal.Name, Action: ACTION_KEY_NEW, Detail: quotedKeyName(key.Name) + ": " + apiKeyRoleDetail(key.Role), IP: clientIp(r)})
		user, _ := web.auth.users.Get(principal.Name)
		// the key is in this answer only: nothing on the way may keep it
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, APIKeyCreated{KeyRow: keyRow(user, key), Key: secret})
	}).Methods("POST")

	// a key of the user, or of anybody for an administrator
	web.router.HandleFunc("/api/keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		actor := principalFrom(r)
		if actor.Source == "key" {
			writeGlobalError(w, http.StatusForbidden, lang, ErrAPIKeyFromAPIKey.Error())
			return
		}
		// an administrator, also the one set with environment variables, revokes any key
		owner := actor.Name
		if actor.IsAdmin() {
			owner = ""
		} else if owner == "" {
			writeGlobalError(w, http.StatusNotFound, lang, ErrAPIKeyNotFound.Error())
			return
		}
		user, key, err := web.auth.users.DeleteAPIKey(owner, mux.Vars(r)["id"])
		if err != nil {
			writeGlobalError(w, apiKeyStatus(err), lang, err.Error())
			return
		}
		web.sugarLogger.Infof("API key %q of %q revoked", key.Name, user)
		detail := quotedKeyName(key.Name)
		if user != actor.Name {
			detail += " (" + user + ")"
		}
		web.activities().add(Activity{Time: time.Now(), User: actor.Name, Action: ACTION_KEY_DEL, Detail: detail, IP: clientIp(r)})
		row := KeyRow{User: user, ID: key.ID, Name: key.Name, Role: key.Role, Hint: apiKeyPrefix + "…" + key.Hint, Created: key.Created, LastUsed: key.LastUsed}
		writeJSON(w, http.StatusOK, row)
	}).Methods("DELETE")
}
