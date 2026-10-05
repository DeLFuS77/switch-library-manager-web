package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
)

type LoginPageData struct {
	Lang  string
	Next  string
	Name  string
	Error string
}

type UsersPageData struct {
	GlobalPageData
	Users   []User
	EnvUser string
	Message string
	Error   string
}

type AccountPageData struct {
	GlobalPageData
	Message string
	Error   string
}

// messages shown after an action, by the key in the redirect
var userMessages = map[string]string{
	"created":  "The user was created.",
	"role":     "The role was changed.",
	"password": "The password was changed.",
	"deleted":  "The user was deleted.",
	"enabled":  "Login is now required. You are logged in as the new administrator.",
}

func (web *Web) HandleUsers() {
	login := web.mustParseTemplates(web.embedFS, "resources/login.html")
	users := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/users.html")
	account := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/account.html")

	web.router.HandleFunc("/login.html", func(w http.ResponseWriter, r *http.Request) {
		if !web.auth.Enabled() || web.auth.fromSession(r) != nil {
			http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
			return
		}
		web.renderLogin(w, r, login, LoginPageData{Next: r.URL.Query().Get("next")})
	}).Methods("GET")

	web.router.HandleFunc("/login.html", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		data := LoginPageData{Next: r.FormValue("next"), Name: strings.TrimSpace(r.FormValue("name"))}
		ip := clientIp(r)
		if !web.auth.limiter.allowed(ip) {
			minutes := int(math.Ceil(web.auth.limiter.retryAfter(ip).Minutes()))
			data.Error = translatef(lang, errTooManyLogins.Error(), minutes)
			w.WriteHeader(http.StatusTooManyRequests)
			web.renderLogin(w, r, login, data)
			return
		}
		principal, ok := web.auth.verify(data.Name, r.FormValue("password"))
		if !ok {
			web.auth.limiter.fail(ip)
			web.sugarLogger.Warnf("Failed login for %q from %s", data.Name, ip)
			data.Error = translate(lang, "Wrong user name or password.")
			w.WriteHeader(http.StatusUnauthorized)
			web.renderLogin(w, r, login, data)
			return
		}
		web.auth.limiter.succeed(ip)
		http.SetCookie(w, web.auth.sessionCookie(r, principal.Name))
		http.Redirect(w, r, safeNext(data.Next), http.StatusSeeOther)
	}).Methods("POST")

	web.router.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, clearSessionCookie(r))
		http.Redirect(w, r, "/login.html", http.StatusSeeOther)
	}).Methods("POST")

	web.router.HandleFunc("/users.html", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		data := UsersPageData{GlobalPageData: web.globalPageData("users"), Users: web.auth.users.List(), EnvUser: web.auth.envUser}
		if message, ok := userMessages[r.URL.Query().Get("done")]; ok {
			data.Message = translate(lang, message)
		}
		if err := userErrorFromQuery(r); err != nil {
			data.Error = translate(lang, err.Error())
		}
		web.render(w, r, users, data)
	}).Methods("GET")

	web.router.HandleFunc("/account.html", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		data := AccountPageData{GlobalPageData: web.globalPageData("account")}
		if message, ok := userMessages[r.URL.Query().Get("done")]; ok {
			data.Message = translate(lang, message)
		}
		if err := userErrorFromQuery(r); err != nil {
			data.Error = translate(lang, err.Error())
		}
		web.render(w, r, account, data)
	}).Methods("GET")

	web.router.HandleFunc("/users/create", func(w http.ResponseWriter, r *http.Request) {
		firstUser := !web.auth.Enabled()
		name := strings.TrimSpace(r.FormValue("name"))
		role := r.FormValue("role")
		if firstUser {
			// whoever enables the login must be able to manage it
			role = ROLE_ADMIN
		}
		if err := web.auth.users.Add(name, r.FormValue("password"), role); err != nil {
			redirectWithError(w, r, "/users.html", err)
			return
		}
		web.sugarLogger.Infof("User %q (%s) created", name, role)
		if firstUser {
			http.SetCookie(w, web.auth.sessionCookie(r, name))
			http.Redirect(w, r, "/users.html?done=enabled", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/users.html?done=created", http.StatusSeeOther)
	}).Methods("POST")

	web.router.HandleFunc("/users/role", func(w http.ResponseWriter, r *http.Request) {
		name := r.FormValue("name")
		if err := web.auth.users.SetRole(name, r.FormValue("role")); err != nil {
			redirectWithError(w, r, "/users.html", err)
			return
		}
		web.auth.forget()
		http.Redirect(w, r, "/users.html?done=role", http.StatusSeeOther)
	}).Methods("POST")

	web.router.HandleFunc("/users/password", func(w http.ResponseWriter, r *http.Request) {
		name := r.FormValue("name")
		if err := web.auth.users.SetPassword(name, r.FormValue("password")); err != nil {
			redirectWithError(w, r, "/users.html", err)
			return
		}
		web.auth.forget()
		if strings.EqualFold(name, principalFrom(r).Name) {
			// the new password ended the current session too
			http.SetCookie(w, web.auth.sessionCookie(r, principalFrom(r).Name))
		}
		http.Redirect(w, r, "/users.html?done=password", http.StatusSeeOther)
	}).Methods("POST")

	web.router.HandleFunc("/users/delete", func(w http.ResponseWriter, r *http.Request) {
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
	}).Methods("POST")

	web.router.HandleFunc("/account/password", func(w http.ResponseWriter, r *http.Request) {
		principal := principalFrom(r)
		if principal.Source == "env" || principal.Name == "" {
			redirectWithError(w, r, "/account.html", ErrEnvironmentUser)
			return
		}
		if _, ok := web.auth.users.Verify(principal.Name, r.FormValue("current")); !ok {
			redirectWithError(w, r, "/account.html", ErrWrongPassword)
			return
		}
		if err := web.auth.users.SetPassword(principal.Name, r.FormValue("password")); err != nil {
			redirectWithError(w, r, "/account.html", err)
			return
		}
		web.auth.forget()
		http.SetCookie(w, web.auth.sessionCookie(r, principal.Name))
		http.Redirect(w, r, "/account.html?done=password", http.StatusSeeOther)
	}).Methods("POST")
}

func (web *Web) renderLogin(w http.ResponseWriter, r *http.Request, templates templateSet, data LoginPageData) {
	data.Lang = web.requestLanguage(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.execute(w, data.Lang, data); err != nil {
		web.sugarLogger.Error(fmt.Errorf("executing template failed: %w", err))
	}
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
