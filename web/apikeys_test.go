package web

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// keysWeb is a web with the user pages and two users: alice, an administrator signed in, and bob,
// a player.
func keysWeb(t *testing.T) (*Web, *client) {
	t.Helper()
	web := usersWeb(t)
	web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN)
	web.auth.users.Add("bob", "bravo-secret-22", ROLE_VIEWER)
	web.auth.onLogin = web.recordLogin
	web.auth.onKey = web.recordKeyAttempt
	admin := &client{t: t, handler: web.auth.middleware(web.router)}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	if len(admin.cookies) != 1 {
		t.Fatal("alice signs in")
	}
	return web, admin
}

// createKey makes a key with the session or the password of c, and returns the answer.
func createKey(t *testing.T, c *client, name string, role string, headers ...string) APIKeyCreated {
	t.Helper()
	response := c.do("POST", "/api/keys", url.Values{"name": {name}, "role": {role}}, headers...)
	if response.Code != http.StatusCreated {
		t.Fatalf("create the key %q: %v %s", name, response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the answer that holds a key is not kept by caches")
	}
	var created APIKeyCreated
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created
}

func bearer(key string) string {
	return "Bearer " + key
}

func TestAPIKeyCreateUseAndRevoke(t *testing.T) {
	web, admin := keysWeb(t)
	created := createKey(t, admin, "  Home Assistant ", ROLE_ADMIN)
	if !strings.HasPrefix(created.Key, apiKeyPrefix) || len(created.Key) != len(apiKeyPrefix)+apiKeyLength || strings.Trim(created.Key[len(apiKeyPrefix):], apiKeyAlphabet) != "" {
		t.Fatalf("a key of letters and digits: %q", created.Key)
	}
	if created.Name != "Home Assistant" || created.Role != ROLE_ADMIN || created.User != "alice" || created.Hint != apiKeyPrefix+"…"+created.Key[len(created.Key)-apiKeyHintLength:] {
		t.Fatalf("the key made: %+v", created.KeyRow)
	}
	if other := createKey(t, admin, "Script", ROLE_VIEWER); other.Key == created.Key || other.ID == created.ID {
		t.Fatal("every key is another")
	}

	api := &client{t: t, handler: admin.handler, ip: "192.168.1.50"}
	if code := api.do("GET", "/index.html", nil, "Authorization", bearer(created.Key)).Code; code != http.StatusOK {
		t.Fatalf("a key signs in: %v", code)
	}
	if code := api.do("POST", "/sync", nil, "Authorization", bearer(created.Key)).Code; code != http.StatusAccepted {
		t.Fatalf("a key of an administrator does what its administrator does: %v", code)
	}
	if len(api.cookies) != 0 {
		t.Fatal("a key gets no session")
	}
	// the scheme is read whatever its case, and the space around the key is not part of it
	if code := api.do("GET", "/index.html", nil, "Authorization", "bearer  "+created.Key+" ").Code; code != http.StatusOK {
		t.Fatalf("the scheme in small letters: %v", code)
	}
	alice, _ := web.auth.users.Get("alice")
	if len(alice.APIKeys) != 2 || alice.APIKeys[0].LastUsed.IsZero() || time.Since(alice.LastLogin) > time.Minute {
		t.Fatalf("the use of the key is noted, and the sign-in of its user: %+v", alice.APIKeys)
	}

	var listed []KeyRow
	json.Unmarshal(admin.do("GET", "/api/keys", nil).Body.Bytes(), &listed)
	if len(listed) != 2 || listed[1].ID != created.ID || listed[1].LastUsed.IsZero() {
		t.Fatalf("the keys of the user, the newest first: %+v", listed)
	}

	revoked := admin.do("DELETE", "/api/keys/"+created.ID, nil)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke: %v %s", revoked.Code, revoked.Body.String())
	}
	response := api.do("GET", "/index.html", nil, "Authorization", bearer(created.Key))
	if response.Code != http.StatusUnauthorized || !strings.HasPrefix(response.Header().Get("WWW-Authenticate"), "Bearer ") {
		t.Fatalf("a revoked key no longer signs in: %v %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	if code := admin.do("DELETE", "/api/keys/"+created.ID, nil).Code; code != http.StatusNotFound {
		t.Fatalf("a key is revoked once: %v", code)
	}

	// kept in users.json: the key comes back after a start, the revoked one does not
	if err := web.auth.users.reload(); err != nil {
		t.Fatal(err)
	}
	if keys := web.auth.users.KeyRows("alice"); len(keys) != 1 || keys[0].Name != "Script" {
		t.Fatalf("the keys saved: %+v", keys)
	}
}

func TestReadOnlyKeysCannotDoWhatAdministratorsDo(t *testing.T) {
	web, admin := keysWeb(t)
	readOnly := createKey(t, admin, "Home Assistant", ROLE_VIEWER)
	if readOnly.Role != ROLE_VIEWER || readOnly.RoleName() != "Read only" {
		t.Fatalf("a key of read only: %+v", readOnly.KeyRow)
	}
	api := &client{t: t, handler: admin.handler}
	auth := []string{"Authorization", bearer(readOnly.Key)}
	if code := api.do("GET", "/index.html", nil, auth...).Code; code != http.StatusOK {
		t.Fatalf("a key of read only looks: %v", code)
	}
	for _, request := range [][2]string{{"POST", "/sync"}, {"GET", "/settings.html"}, {"GET", "/users.html"}, {"POST", "/users/delete"}} {
		if code := api.do(request[0], request[1], nil, auth...).Code; code != http.StatusForbidden {
			t.Fatalf("a key of read only of an administrator cannot %s %s: %v", request[0], request[1], code)
		}
	}
	// nor make a key that does more, or revoke the other keys of its user
	if code := api.do("POST", "/api/keys", url.Values{"name": {"More"}, "role": {ROLE_ADMIN}}, auth...).Code; code != http.StatusForbidden {
		t.Fatalf("a key cannot make keys: %v", code)
	}
	other := createKey(t, admin, "Other", ROLE_ADMIN)
	if code := api.do("DELETE", "/api/keys/"+other.ID, nil, auth...).Code; code != http.StatusForbidden {
		t.Fatalf("a key cannot revoke keys: %v", code)
	}
	if code := api.do("GET", "/api/keys", nil, auth...).Code; code != http.StatusOK {
		t.Fatalf("a key lists the keys of its user, which hold nothing secret: %v", code)
	}

	// a player makes keys of read only, never more
	player := &client{t: t, handler: admin.handler}
	player.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	if code := player.do("POST", "/api/keys", url.Values{"name": {"Mine"}, "role": {ROLE_ADMIN}}).Code; code != http.StatusBadRequest {
		t.Fatalf("a key cannot do more than its user: %v", code)
	}
	if code := player.do("POST", "/api/keys", url.Values{"name": {"Mine"}, "role": {ROLE_EDITOR}}).Code; code != http.StatusBadRequest {
		t.Fatalf("nor what an editor does: %v", code)
	}
	mine := createKey(t, player, "Mine", ROLE_VIEWER)
	// and revokes only their own: the keys of the others are not even found
	if code := player.do("DELETE", "/api/keys/"+other.ID, nil).Code; code != http.StatusNotFound {
		t.Fatalf("a player cannot revoke the keys of others: %v", code)
	}
	if keys := web.auth.users.KeyRows("bob"); len(keys) != 1 || keys[0].ID != mine.ID {
		t.Fatalf("the keys of bob: %+v", keys)
	}
	var listed []KeyRow
	json.Unmarshal(player.do("GET", "/api/keys?all=true", nil).Body.Bytes(), &listed)
	if len(listed) != 1 || listed[0].User != "bob" {
		t.Fatalf("only an administrator lists the keys of everybody: %+v", listed)
	}
	json.Unmarshal(admin.do("GET", "/api/keys?all=true", nil).Body.Bytes(), &listed)
	if len(listed) != 3 {
		t.Fatalf("the keys of everybody: %+v", listed)
	}
	// an administrator revokes the key of another user
	if code := admin.do("DELETE", "/api/keys/"+mine.ID, nil).Code; code != http.StatusOK {
		t.Fatalf("an administrator revokes any key: %v", code)
	}

	// names a key cannot have
	for _, name := range []string{"", "   ", strings.Repeat("x", maxAPIKeyName+1), "two\nlines"} {
		if code := admin.do("POST", "/api/keys", url.Values{"name": {name}, "role": {ROLE_VIEWER}}).Code; code != http.StatusBadRequest {
			t.Fatalf("the name %q: %v", name, code)
		}
	}
	if _, ok := cleanAPIKeyName(strings.Repeat("é", maxAPIKeyName)); !ok {
		t.Fatal("the length of a name is counted in letters")
	}
}

func TestKeysFollowTheRoleOfTheirUser(t *testing.T) {
	web, admin := keysWeb(t)
	web.auth.users.Add("carol", "charlie-secret-33", ROLE_ADMIN)
	carol := &client{t: t, handler: admin.handler}
	carol.do("POST", "/login.html", url.Values{"name": {"carol"}, "password": {"charlie-secret-33"}})
	key := createKey(t, carol, "Automation", ROLE_ADMIN)
	api := &client{t: t, handler: admin.handler}
	if code := api.do("POST", "/sync", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusAccepted {
		t.Fatalf("the key of an administrator: %v", code)
	}

	admin.do("POST", "/users/role", url.Values{"name": {"carol"}, "role": {ROLE_VIEWER}})
	if code := api.do("POST", "/sync", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusForbidden {
		t.Fatalf("a key does no more than its user does now: %v", code)
	}
	if code := api.do("GET", "/index.html", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusOK {
		t.Fatalf("and still what the user does: %v", code)
	}
	rows := web.auth.users.KeyRows("carol")
	if len(rows) != 1 || rows[0].Role != ROLE_VIEWER || rows[0].MadeAs != ROLE_ADMIN || rows[0].MadeAsName() != "Administrator" {
		t.Fatalf("the key shows the role it has now and the one it was made with: %+v", rows)
	}

	// a key of a guest made as read only stays a guest's: it does not download
	web.auth.users.Add("dave", "delta-secret-44", ROLE_GUEST)
	guest := &client{t: t, handler: admin.handler}
	guest.do("POST", "/login.html", url.Values{"name": {"dave"}, "password": {"delta-secret-44"}})
	if made := createKey(t, guest, "Look", ROLE_VIEWER); made.Role != ROLE_GUEST {
		t.Fatalf("the key of a guest: %+v", made.KeyRow)
	}
}

func TestDeletedUsersTakeTheirKeys(t *testing.T) {
	web, admin := keysWeb(t)
	player := &client{t: t, handler: admin.handler}
	player.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	key := createKey(t, player, "Tablet", ROLE_VIEWER)
	api := &client{t: t, handler: admin.handler}
	if code := api.do("GET", "/index.html", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusOK {
		t.Fatalf("the key of bob: %v", code)
	}
	if location := admin.do("POST", "/users/delete", url.Values{"name": {"bob"}}).Header().Get("Location"); !strings.Contains(location, "done=deleted") {
		t.Fatalf("delete bob: %q", location)
	}
	if code := api.do("GET", "/index.html", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusUnauthorized {
		t.Fatalf("the keys of a deleted user no longer sign in: %v", code)
	}
	// a new user of the same name does not get them back
	web.auth.users.Add("bob", "bravo-secret-55", ROLE_ADMIN)
	if code := api.do("GET", "/index.html", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusUnauthorized {
		t.Fatalf("nor once a user of the same name comes: %v", code)
	}
	if rows := web.auth.users.KeyRows(""); len(rows) != 0 {
		t.Fatalf("no key is left: %+v", rows)
	}
}

// A key that does not exist, or no longer does, is a failed sign-in: it is told to the activity
// and blocks the address as wrong passwords do.
func TestWrongKeysCountAsFailedSignIns(t *testing.T) {
	web, admin := keysWeb(t)
	good := createKey(t, admin, "Home Assistant", ROLE_VIEWER)
	revoked := createKey(t, admin, "Old", ROLE_VIEWER)
	admin.do("DELETE", "/api/keys/"+revoked.ID, nil)

	attacker := &client{t: t, handler: admin.handler, ip: "203.0.113.7"}
	fake := apiKeyPrefix + strings.Repeat("A", apiKeyLength)
	for i := 0; i < maxLoginFailures; i++ {
		key := fake
		if i%2 == 0 {
			key = revoked.Key
		}
		if code := attacker.do("GET", "/index.html", nil, "Authorization", bearer(key)).Code; code != http.StatusUnauthorized {
			t.Fatalf("a wrong key does not sign in: %v", code)
		}
	}
	if code := attacker.do("GET", "/index.html", nil, "Authorization", bearer(good.Key)).Code; code != http.StatusTooManyRequests {
		t.Fatalf("too many wrong keys block the address: %v", code)
	}
	if code := attacker.do("GET", "/index.html", nil, "Authorization", basic("alice", "alpha-secret-11")).Code; code != http.StatusTooManyRequests {
		t.Fatalf("for passwords too: %v", code)
	}
	// a key names no account, so the others still sign in, with the key too
	other := &client{t: t, handler: admin.handler, ip: "192.168.1.60"}
	if code := other.do("GET", "/index.html", nil, "Authorization", bearer(good.Key)).Code; code != http.StatusOK {
		t.Fatalf("the key works from another address: %v", code)
	}

	entries := web.activities().list()
	failed, blocked := 0, 0
	for _, entry := range entries {
		if entry.IP != "203.0.113.7" {
			continue
		}
		switch {
		case entry.Action == ACTION_LOGIN_FAIL && entry.Detail == apiKeyFailDetail && entry.User == "":
			failed++
		case entry.Action == ACTION_LOGIN_LOCK && entry.Detail == apiKeyBlockedDetail:
			blocked++
		}
	}
	if failed != 1 || blocked != 1 {
		t.Fatalf("a failed and a blocked key are told once an hour by address: %d failed, %d blocked in %+v", failed, blocked, entries)
	}
	if shown := translateActivity("es", entries); shown[0].User == "" {
		t.Fatalf("a key that does not exist is shown as one: %+v", shown[0])
	}

	// a bearer token of another kind, such as one a proxy adds for itself, is no attempt
	proxied := &client{t: t, handler: admin.handler, ip: "192.168.1.70"}
	for i := 0; i < maxLoginFailures+2; i++ {
		proxied.do("GET", "/index.html", nil, "Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.e30.x")
	}
	if code := proxied.do("GET", "/index.html", nil, "Authorization", bearer(good.Key)).Code; code != http.StatusOK {
		t.Fatalf("other bearer tokens do not block the address: %v", code)
	}
}

// The key is in the answer that makes it, and nowhere else: not in the log, the pages, the API,
// the activity, users.json or a backup.
func TestAPIKeysAreNeverShownAgain(t *testing.T) {
	web, admin := keysWeb(t)
	core, logs := observer.New(zap.DebugLevel)
	web.sugarLogger = zap.New(core).Sugar()
	web.HandleActivity()

	created := createKey(t, admin, "Home Assistant", ROLE_ADMIN)
	api := &client{t: t, handler: admin.handler, ip: "192.168.1.50"}
	api.do("GET", "/index.html", nil, "Authorization", bearer(created.Key))
	attacker := &client{t: t, handler: admin.handler, ip: "203.0.113.9"}
	attacker.do("GET", "/index.html", nil, "Authorization", bearer(created.Key+"x"))

	seen := map[string]string{}
	for _, path := range []string{"/account.html", "/users.html", "/activity.html", "/activity.csv", "/api/keys", "/api/keys?all=true"} {
		seen[path] = admin.do("GET", path, nil).Body.String()
	}
	var backup bytes.Buffer
	if err := web.writeBackup(&backup); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(backup.Bytes()), int64(backup.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		reader, _ := file.Open()
		content, _ := io.ReadAll(reader)
		reader.Close()
		seen["backup "+file.Name] = string(content)
	}
	stored, _ := os.ReadFile(filepath.Join(web.dataFolder, USERS_FILENAME))
	seen[USERS_FILENAME] = string(stored)
	for _, entry := range logs.All() {
		seen["log"] += entry.Message + "\n"
	}

	alice, _ := web.auth.users.Get("alice")
	hash := alice.APIKeys[0].Hash
	secret := created.Key[len(apiKeyPrefix):]
	for where, text := range seen {
		if strings.Contains(text, secret) {
			t.Fatalf("the key is in %s", where)
		}
		if where != USERS_FILENAME && where != "backup "+USERS_FILENAME && strings.Contains(text, hash) {
			t.Fatalf("the hash of the key is in %s", where)
		}
	}
	if !strings.Contains(seen[USERS_FILENAME], hash) || !strings.Contains(seen["backup "+USERS_FILENAME], hash) {
		t.Fatal("users.json and its backup keep the hash of the key")
	}
	if !strings.Contains(seen["log"], `API key "Home Assistant" of "alice" created`) {
		t.Fatalf("the log tells a key was made, by its name: %s", seen["log"])
	}
	if !strings.Contains(seen["/activity.csv"], ACTION_KEY_NEW) || !strings.Contains(seen["/activity.csv"], ACTION_KEY_USE) {
		t.Fatalf("the activity tells a key was made and used: %s", seen["/activity.csv"])
	}
}

func TestAPIKeysInTheActivity(t *testing.T) {
	web, admin := keysWeb(t)
	key := createKey(t, admin, "Home: Assistant", ROLE_VIEWER)
	api := &client{t: t, handler: admin.handler, ip: "192.168.1.50"}
	for range 3 {
		api.do("GET", "/index.html", nil, "Authorization", bearer(key.Key))
	}
	player := &client{t: t, handler: admin.handler}
	player.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	bobs := createKey(t, player, "Tablet", ROLE_VIEWER)
	admin.do("DELETE", "/api/keys/"+bobs.ID, nil)
	admin.do("DELETE", "/api/keys/"+key.ID, nil)

	count := map[string]int{}
	details := map[string][]string{}
	for _, entry := range translateActivity("es", web.activities().list()) {
		count[entry.Action]++
		details[entry.Action] = append(details[entry.Action], entry.User+" "+entry.Detail)
	}
	if count[ACTION_KEY_USE] != 1 {
		t.Fatalf("the use of a key is told once a day by key and address: %v", details[ACTION_KEY_USE])
	}
	if strings.Join(details[ACTION_KEY_NEW], "|") != `bob "Tablet": Solo lectura|alice "Home: Assistant": Solo lectura` {
		t.Fatalf("who made which key: %q", details[ACTION_KEY_NEW])
	}
	if strings.Join(details[ACTION_KEY_DEL], "|") != `alice "Home: Assistant"|alice "Tablet" (bob)` {
		t.Fatalf("who revoked which key, and whose: %q", details[ACTION_KEY_DEL])
	}
	if count[ACTION_LOGIN_OK] != 2 {
		t.Fatalf("the sign-ins of the login page are told as before: %v", count)
	}
}

// Without users the app is open on the local network until the first administrator is made with
// the setup code: no key opens that.
func TestAPIKeysDoNotSkipTheSetupCode(t *testing.T) {
	web := usersWeb(t)
	c := &client{t: t, handler: web.auth.middleware(web.router)}
	fake := apiKeyPrefix + strings.Repeat("B", apiKeyLength)
	created := c.do("POST", "/users/create", url.Values{"name": {"mallory"}, "password": {"mallory-secret-1"}, "role": {ROLE_ADMIN}}, "Authorization", bearer(fake))
	if !strings.Contains(created.Header().Get("Location"), "error=") || web.auth.Enabled() {
		t.Fatalf("the first administrator needs the setup code: %v %q", created.Code, created.Header().Get("Location"))
	}
	if code := c.do("POST", "/api/keys", url.Values{"name": {"Mine"}}).Code; code != http.StatusBadRequest {
		t.Fatalf("there are no keys while login is off: %v", code)
	}
}

func TestEnvironmentUsersHaveNoKeys(t *testing.T) {
	web := usersWeb(t)
	t.Setenv("SLM_AUTH_USERNAME", "root")
	t.Setenv("SLM_AUTH_PASSWORD", "root-secret-123")
	auth, err := newAuth(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	web.auth = auth
	web.auth.users.Add("bob", "bravo-secret-22", ROLE_VIEWER)
	c := &client{t: t, handler: web.auth.middleware(web.router)}
	if code := c.do("POST", "/api/keys", url.Values{"name": {"Mine"}}, "Authorization", basic("root", "root-secret-123")).Code; code != http.StatusBadRequest {
		t.Fatalf("the user of the environment keeps no keys: %v", code)
	}
	// with the password of a user, as a script would, a key is made
	key := createKey(t, c, "Script", "", "Authorization", basic("bob", "bravo-secret-22"))
	if key.Role != ROLE_VIEWER {
		t.Fatalf("a key is of read only unless told: %+v", key.KeyRow)
	}
	// and the administrator of the environment revokes it
	if code := c.do("DELETE", "/api/keys/"+key.ID, nil, "Authorization", basic("root", "root-secret-123")).Code; code != http.StatusOK {
		t.Fatalf("the administrator of the environment revokes keys: %v", code)
	}
}

func TestAPIKeysAreHashedAndCompared(t *testing.T) {
	store, err := loadUserStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	store.Add("alice", "alpha-secret-11", ROLE_ADMIN)
	secret, key, err := store.AddAPIKey("alice", "One", ROLE_ADMIN)
	if err != nil {
		t.Fatal(err)
	}
	salt, _ := hex.DecodeString(key.Salt)
	if len(salt) != apiKeySaltSize || key.Hash != hex.EncodeToString(apiKeyHash(secret, salt)) || strings.Contains(key.Hash, secret) {
		t.Fatalf("only the hash is kept, with its salt: %+v", key)
	}
	if key.Lookup != secret[len(apiKeyPrefix):len(apiKeyPrefix)+apiKeyLookupLength] {
		t.Fatalf("the start of the key finds it: %+v", key)
	}
	if user, found, ok := store.FindAPIKey(secret); !ok || user.Name != "alice" || found.ID != key.ID {
		t.Fatal("the key is found by its hash")
	}
	// the same start and length, another end: it is hashed and refused
	last := "a"
	if strings.HasSuffix(secret, last) {
		last = "b"
	}
	sameStart := secret[:len(secret)-1] + last
	for _, wrong := range []string{"", apiKeyPrefix, secret[:len(secret)-1], secret + "a", strings.ToUpper(secret), sameStart} {
		if _, _, ok := store.FindAPIKey(wrong); ok {
			t.Fatalf("%q is not the key", wrong)
		}
	}
	// two keys never share a salt
	otherSecret, other, err := store.AddAPIKey("alice", "Two", ROLE_VIEWER)
	if err != nil {
		t.Fatal(err)
	}
	if other.Salt == key.Salt || other.Hash == key.Hash {
		t.Fatal("each key has its own salt")
	}
	// a key that passed is remembered, and stops at once when it is revoked
	if _, _, ok := store.FindAPIKey(otherSecret); !ok {
		t.Fatal("the second key is found")
	}
	if _, ok := store.keysSeen.Load(cacheKey(otherSecret)); !ok {
		t.Fatal("a key that passed is remembered")
	}
	if _, _, err := store.DeleteAPIKey("", other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := store.FindAPIKey(otherSecret); ok {
		t.Fatal("a revoked key stops at once, even when remembered")
	}
	// a user copied out of the store shares no key with it
	user, _ := store.Get("alice")
	user.APIKeys[0].Role = ROLE_GUEST
	if again, _ := store.Get("alice"); again.APIKeys[0].Role != ROLE_ADMIN {
		t.Fatal("a copy of a user changes nothing in the store")
	}
	for range maxAPIKeys - 1 {
		if _, _, err := store.AddAPIKey("alice", "More", ROLE_VIEWER); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := store.AddAPIKey("alice", "Too many", ROLE_VIEWER); err != ErrAPIKeyCount {
		t.Fatalf("at most %d keys: %v", maxAPIKeys, err)
	}
	if _, _, err := store.AddAPIKey("nobody", "Key", ROLE_VIEWER); err != ErrUserNotFound {
		t.Fatalf("a key of nobody: %v", err)
	}
}

// Basic authentication works as before, beside the keys.
func TestBasicAuthBesideAPIKeys(t *testing.T) {
	_, admin := keysWeb(t)
	createKey(t, admin, "Home Assistant", ROLE_VIEWER)
	api := &client{t: t, handler: admin.handler}
	if code := api.do("GET", "/index.html", nil, "Authorization", basic("bob", "bravo-secret-22")).Code; code != http.StatusOK {
		t.Fatalf("basic authentication: %v", code)
	}
	if code := api.do("POST", "/sync", nil, "Authorization", basic("alice", "alpha-secret-11")).Code; code != http.StatusAccepted {
		t.Fatalf("basic authentication of an administrator: %v", code)
	}
	wrong := api.do("GET", "/index.html", nil, "Authorization", basic("bob", "wrong"))
	if wrong.Code != http.StatusUnauthorized || !strings.HasPrefix(wrong.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("a wrong password asks for basic authentication: %v %q", wrong.Code, wrong.Header().Get("WWW-Authenticate"))
	}
	if response := api.do("GET", "/index.html", nil); response.Code != http.StatusUnauthorized || !strings.HasPrefix(response.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("without credentials: %v", response.Code)
	}
}

// The routes of the keys are described in openapi.json, with the bearer scheme.
func TestOpenApiDocumentsTheAPIKeys(t *testing.T) {
	data, err := os.ReadFile("../resources/static/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			SecuritySchemes map[string]struct {
				Scheme string `json:"scheme"`
			} `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Components.SecuritySchemes["bearerAuth"].Scheme != "bearer" || spec.Components.SecuritySchemes["basicAuth"].Scheme != "basic" {
		t.Fatalf("the schemes: %+v", spec.Components.SecuritySchemes)
	}
	web := newTestWeb(t)
	web.handleAPIKeys()
	checked := 0
	web.router.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		path, _ := route.GetPathTemplate()
		methods, _ := route.GetMethods()
		for _, method := range methods {
			if _, ok := spec.Paths[path][strings.ToLower(method)]; !ok {
				t.Errorf("%s %s is not documented", method, path)
			}
			checked++
		}
		return nil
	})
	if checked != 3 {
		t.Fatalf("%d routes checked", checked)
	}
}
