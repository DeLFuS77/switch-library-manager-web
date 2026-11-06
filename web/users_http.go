package web

import (
	"errors"
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"math"
	"net/http"
	"net/url"
	"strings"
)

type LoginPageData struct {
	Lang  string
	Next  string
	Name  string
	Error string
	// who a sign-in portal in front of the app named, when no user has that name or email, and
	// which portal it was
	Portal    string
	PortalVia string
	// signed out of the app while the portal keeps its session: told where to end it
	PortalOut bool
	// the name on the button of OpenID Connect, when it is on, and the notice of a sign-in with
	// it: OIDC_FAILED, OIDC_UNREACHABLE or OIDC_CANCELLED
	Oidc       string
	OidcName   string
	OidcNotice string
	// signed out, on the way to the page of another site that ends the session of the provider
	// or the portal, and its name
	Leaving     string
	LeavingName string
}

type UsersPageData struct {
	GlobalPageData
	// what users changed, newest first
	Activity []activity.Entry
	Users    []User
	EnvUser  string
	Message  string
	Error    string
	// a setup code missing, wrong or blocked: shown under its field, not over the page
	CodeError string
	// the API keys of every user
	Keys []KeyRow
	// the name of the provider of OpenID Connect, on the users linked with an account of it
	Oidc string
}

type AccountPageData struct {
	GlobalPageData
	Message string
	Error   string
	// the language of the user, and the languages to choose from
	Language  string
	Languages []string
	// the user is stored in users.json, so preferences can be kept
	Stored bool
	// the colours the avatar can take
	AvatarColors []string
	// the API keys of the user, and the roles a new one may have; a request signed in with a
	// key sees them but makes none
	Keys        []KeyRow
	KeyChoices  []RoleChoice
	CanMakeKeys bool
	// the user has a password: one made by a sign-in portal has none
	HasPassword bool
}

// messages shown after an action, by the key in the redirect
var userMessages = map[string]string{
	"created":  "The user was created.",
	"role":     "The role was changed.",
	"password": "The password was changed.",
	"deleted":  "The user was deleted.",
	"enabled":  "Login is now required. You are logged in as the new administrator.",
	"language": "Your language was saved.",
	"color":    "The colour of your icon was saved.",
	// the API keys, made and revoked by the scripts of the pages
	"keycreated": "The API key was made.",
	"keyrevoked": "The API key was revoked.",
	// the email a sign-in portal names a user with
	"email": "The email was saved.",
	// the link of a user with the account of a provider of OpenID Connect
	"unlinked": "The OpenID Connect account was unlinked.",
}

func (web *Web) HandleUsers() {
	login := web.mustParseTemplates(web.embedFS, "resources/login.html")
	users := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/users.html")
	account := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/account.html")

	web.router.HandleFunc("/login.html", web.loginPage(login)).Methods("GET")
	web.router.HandleFunc("/login.html", web.signIn(login)).Methods("POST")
	web.router.HandleFunc("/logout", web.signOut(login)).Methods("POST")
	web.router.HandleFunc("/users.html", web.usersPage(users)).Methods("GET")
	web.router.HandleFunc("/account.html", web.accountPage(account)).Methods("GET")
	web.router.HandleFunc("/users/create", web.createUser).Methods("POST")
	web.router.HandleFunc("/users/role", web.setUserRole).Methods("POST")
	web.router.HandleFunc("/users/password", web.setUserPassword).Methods("POST")
	web.router.HandleFunc("/users/delete", web.deleteUser).Methods("POST")
	web.router.HandleFunc("/account/language", web.setAccountLanguage).Methods("POST")
	web.router.HandleFunc("/account/color", web.setAccountColor).Methods("POST")
	web.router.HandleFunc("/account/password", web.setAccountPassword).Methods("POST")

	web.handleAPIKeys()
	web.handlePortal()
	web.handleOidc()
}

// loginPage shows the login page, or goes on to the app when the person is signed in already.
func (web *Web) loginPage(login templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := LoginPageData{Next: r.URL.Query().Get("next")}
		options := web.auth.portal()
		data.OidcName = oidcButton(options)
		if oidcReady(options) {
			data.Oidc = data.OidcName
		}
		via := principalFrom(r).Via
		// signed out of the app, while the portal in front keeps the person signed in
		if via != "" && r.URL.Query().Get("portal") == "out" {
			data.PortalOut, data.PortalVia = true, via
			web.renderLogin(w, r, login, data)
			return
		}
		if !web.auth.Enabled() || via != "" || web.auth.fromSession(r) != nil {
			redirectLocal(w, r, safeNext(data.Next))
			return
		}
		// the portal let the person in, but the app has no user for them
		if identity, _ := web.auth.portalIdentity(r); identity != nil {
			data.Portal, data.PortalVia = identity.named(), identity.Via
		}
		web.oidcLoginNotice(w, r, &data)
		web.renderLogin(w, r, login, data)
	}
}

// signIn checks the name and password sent by the login page, limited as the logins are.
func (web *Web) signIn(login templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		data := LoginPageData{Next: r.FormValue("next"), Name: strings.TrimSpace(r.FormValue("name"))}
		if options := web.auth.portal(); oidcReady(options) {
			data.Oidc, data.OidcName = oidcButton(options), oidcButton(options)
		}
		ip := clientIp(r)
		keys := loginKeys(ip, data.Name)
		retryAfter, delay := web.auth.limiter.check(keys...)
		if retryAfter > 0 {
			minutes := int(math.Ceil(retryAfter.Minutes()))
			if minutes < 1 {
				minutes = 1
			}
			data.Error = i18n.Translatef(lang, errTooManyLogins.Error(), minutes)
			web.auth.loginAttempt(data.Name, ip, "blocked", r.FormValue("password"), retryTellInterval)
			w.WriteHeader(http.StatusTooManyRequests)
			web.renderLogin(w, r, login, data)
			return
		}
		// a small delay after a few failures: barely felt by a person, costly for a bot
		if delay > 0 {
			sleepFor(delay)
		}
		principal, ok := web.auth.verify(data.Name, r.FormValue("password"))
		if !ok {
			web.auth.limiter.fail(keys...)
			// the address goes to Activity only: proxies send it in headers, which the log keeps out
			web.sugarLogger.Warnf("Failed login for %q", web.auth.attemptName(data.Name))
			web.auth.loginAttempt(data.Name, ip, "fail", r.FormValue("password"), 0)
			data.Error = i18n.Translate(lang, "Wrong user name or password.")
			w.WriteHeader(http.StatusUnauthorized)
			web.renderLogin(w, r, login, data)
			return
		}
		web.auth.limiter.succeed(keys...)
		web.auth.loginAttempt(principal.Name, ip, "ok", "", 0)
		web.auth.signedIn(principal.Name)
		http.SetCookie(w, web.auth.sessionCookie(r, principal.Name))
		redirectLocal(w, r, safeNext(data.Next))
	}
}

// signOut ends the session, and the one of the portal or the provider in front when there is one.
func (web *Web) signOut(login templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		web.auth.endSession(r)
		http.SetCookie(w, clearSessionCookie(r))
		target := "/login.html"
		via := principalFrom(r).Via
		switch via {
		case "":
		case VIA_OIDC:
			// out of the provider too, when it has a page for it
			if exit := web.auth.oidcLogout(r); exit != "" {
				target = exit
			}
			http.SetCookie(w, web.auth.oidcCookie(r, oidcHintCookie, "/logout", "", 0))
		default:
			// a person signed in by a portal signs out of it, or is told how to
			if target = web.auth.portalLogout(via); target == "" {
				target = "/login.html?portal=out"
			}
		}
		// a page of the app opens the one of another site: browsers do not follow a form to
		// another site (form-action 'self' of the Content-Security-Policy), not even redirected;
		// "//host" or "/\host" is another site too, though it starts with a slash
		if !isLocalUrl(target) {
			data := LoginPageData{Leaving: target, LeavingName: leavingName(target)}
			if via == VIA_OIDC {
				data.LeavingName = oidcButton(web.auth.portal())
			}
			web.renderLogin(w, r, login, data)
			return
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}

// usersPage lists the users, their API keys and the latest activity.
func (web *Web) usersPage(users templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		// the first administrator asks for the setup code: it is written again at the end of the log
		web.logSetupCode(false)
		data := UsersPageData{GlobalPageData: web.globalPageData("users"), Users: web.auth.users.List(), EnvUser: web.auth.envUser, Activity: translateActivity(web.requestLanguage(r), activity.Preview(web.activities().List(), 8)), Keys: web.auth.users.KeyRows(""), Oidc: oidcButton(web.auth.portal())}
		if message, ok := userMessages[r.URL.Query().Get("done")]; ok {
			data.Message = i18n.Translate(lang, message)
		}
		if err := userErrorFromQuery(r); err != nil {
			if errors.Is(err, ErrSetupCodeMissing) || errors.Is(err, ErrSetupCode) || errors.Is(err, ErrSetupCodeBlocked) {
				data.CodeError = i18n.Translate(lang, err.Error())
			} else {
				data.Error = i18n.Translate(lang, err.Error())
			}
		}
		web.render(w, r, users, data)
	}
}

// accountPage shows the account of the user: language, colour, password and API keys.
func (web *Web) accountPage(account templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		data := AccountPageData{GlobalPageData: web.globalPageData("account"), Languages: i18n.SupportedLanguages, AvatarColors: avatarColors}
		if principal := principalFrom(r); principal != nil && principal.Name != "" {
			if user, ok := web.auth.users.Get(principal.Name); ok {
				data.Stored, data.Language = true, user.Language
				data.Keys, data.KeyChoices, data.CanMakeKeys = web.auth.users.KeyRows(user.Name), apiKeyChoices(user.Role), principal.Source != "key"
				data.HasPassword = user.HasPassword()
			}
		}
		if message, ok := userMessages[r.URL.Query().Get("done")]; ok {
			data.Message = i18n.Translate(lang, message)
		}
		if err := userErrorFromQuery(r); err != nil {
			data.Error = i18n.Translate(lang, err.Error())
		}
		web.render(w, r, account, data)
	}
}

// createUser creates a user; the first one, an administrator, turns the login on with the setup
// code.
func (web *Web) createUser(w http.ResponseWriter, r *http.Request) {
	firstUser := !web.auth.Enabled()
	name := strings.TrimSpace(r.FormValue("name"))
	role := r.FormValue("role")
	if firstUser {
		// whoever enables the login must be able to manage it, and show the setup code of the
		// log: an address of the local network is not enough (see setupcode.go)
		role = ROLE_ADMIN
		if err := web.auth.checkSetupCode(r.FormValue("setup_code")); err != nil {
			web.setupCodeFailed(err)
			redirectWithError(w, r, "/users.html", err)
			return
		}
	}
	// the first user is only created if there is still none, so two requests at once
	// cannot both become administrators without login
	if err := web.auth.users.add(name, r.FormValue("password"), role, firstUser); err != nil {
		redirectWithError(w, r, "/users.html", err)
		return
	}
	web.sugarLogger.Infof("User %q (%s) created", name, role)
	if firstUser {
		web.auth.endSetupCode()
		http.SetCookie(w, web.auth.sessionCookie(r, name))
		http.Redirect(w, r, "/users.html?done=enabled", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/users.html?done=created", http.StatusSeeOther)
}

// setUserRole changes the role of a user.
func (web *Web) setUserRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := web.auth.users.SetRole(name, r.FormValue("role")); err != nil {
		redirectWithError(w, r, "/users.html", err)
		return
	}
	web.auth.forget()
	http.Redirect(w, r, "/users.html?done=role", http.StatusSeeOther)
}

// setUserPassword changes the password of a user, which ends their sessions.
func (web *Web) setUserPassword(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := web.auth.users.SetPassword(name, r.FormValue("password")); err != nil {
		redirectWithError(w, r, "/users.html", err)
		return
	}
	web.auth.forget()
	if strings.EqualFold(name, principalFrom(r).Name) {
		// the new password ended the current session too
		http.SetCookie(w, web.auth.renewedSessionCookie(r))
	}
	http.Redirect(w, r, "/users.html?done=password", http.StatusSeeOther)
}

// deleteUser deletes a user, never the one who asks.
func (web *Web) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if strings.EqualFold(name, principalFrom(r).Name) {
		redirectWithError(w, r, "/users.html", ErrDeleteSelf)
		return
	}
	if err := web.auth.users.Delete(name); err != nil {
		redirectWithError(w, r, "/users.html", err)
		return
	}
	web.auth.forget()
	web.sugarLogger.Infof("User %q deleted", name)
	http.Redirect(w, r, "/users.html?done=deleted", http.StatusSeeOther)
}

// setAccountLanguage saves the language of the interface of the user.
func (web *Web) setAccountLanguage(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r)
	lang := r.FormValue("language")
	if lang != "" && !i18n.IsSupportedLanguage(lang) {
		lang = ""
	}
	if principal == nil || principal.Name == "" {
		redirectWithError(w, r, "/account.html", ErrEnvironmentUser)
		return
	}
	if err := web.auth.users.SetLanguage(principal.Name, lang); err != nil {
		redirectWithError(w, r, "/account.html", err)
		return
	}
	http.Redirect(w, r, "/account.html?done=language", http.StatusSeeOther)
}

// setAccountColor saves the colour of the avatar of the user.
func (web *Web) setAccountColor(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r)
	if principal == nil || principal.Name == "" || principal.Source == "env" {
		redirectWithError(w, r, "/account.html", ErrEnvironmentUser)
		return
	}
	if err := web.auth.users.SetColor(principal.Name, r.FormValue("color")); err != nil {
		redirectWithError(w, r, "/account.html", err)
		return
	}
	http.Redirect(w, r, "/account.html?done=color", http.StatusSeeOther)
}

// setAccountPassword changes the password of the user, who gives the current one.
func (web *Web) setAccountPassword(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r)
	if principal.Source == "env" || principal.Name == "" {
		redirectWithError(w, r, "/account.html", ErrEnvironmentUser)
		return
	}
	// guessed from an open session, it is limited as the logins are
	keys := loginKeys(clientIp(r), principal.Name)
	if retryAfter, _ := web.auth.limiter.check(keys...); retryAfter > 0 {
		// a blocked attempt is not even checked
		redirectWithError(w, r, "/account.html", ErrWrongPassword)
		return
	}
	if _, ok := web.auth.users.Verify(principal.Name, r.FormValue("current")); !ok {
		web.auth.limiter.fail(keys...)
		redirectWithError(w, r, "/account.html", ErrWrongPassword)
		return
	}
	web.auth.limiter.succeed(keys...)
	if err := web.auth.users.SetPassword(principal.Name, r.FormValue("password")); err != nil {
		redirectWithError(w, r, "/account.html", err)
		return
	}
	web.auth.forget()
	http.SetCookie(w, web.auth.renewedSessionCookie(r))
	http.Redirect(w, r, "/account.html?done=password", http.StatusSeeOther)
}

func (web *Web) renderLogin(w http.ResponseWriter, r *http.Request, templates templateSet, data LoginPageData) {
	data.Lang = web.requestLanguage(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.execute(w, data.Lang, data); err != nil {
		web.sugarLogger.Error(fmt.Errorf("executing template failed: %w", err))
	}
}

// leavingName is how the page of another site that ends a session is named: its host.
func leavingName(target string) string {
	if parsed, err := url.Parse(target); err == nil && parsed.Host != "" {
		return parsed.Hostname()
	}
	return target
}

// redirectWithError shows a known error on the target page.
func redirectWithError(w http.ResponseWriter, r *http.Request, target string, err error) {
	for i, known := range userErrors {
		if errors.Is(err, known) {
			http.Redirect(w, r, fmt.Sprintf("%s?error=%d", target, i), http.StatusSeeOther)
			return
		}
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func userErrorFromQuery(r *http.Request) error {
	value := r.URL.Query().Get("error")
	for i, known := range userErrors {
		if value == fmt.Sprint(i) {
			return known
		}
	}
	return nil
}
