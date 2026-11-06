package web

import (
	"crypto/hmac"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// handleOidc serves the sign-in with OpenID Connect: the button of the login page starts it, and
// the provider sends the person back to the callback. Both work without a session.
func (web *Web) handleOidc() {
	web.router.HandleFunc(oidcLoginPath, func(w http.ResponseWriter, r *http.Request) {
		next := r.URL.Query().Get("next")
		w.Header().Set("Cache-Control", "no-store")
		// nothing while there is no user: the first administrator needs the setup code
		if !web.auth.Enabled() || !oidcReady(web.auth.portal()) {
			http.Redirect(w, r, "/login.html", http.StatusSeeOther)
			return
		}
		cookie, target, err := web.auth.oidcStart(r, next)
		if err != nil {
			web.auth.portalTell(portalEvent{Via: VIA_OIDC, IP: clientIp(r), Result: "oidc", Detail: err.Error()})
			http.Redirect(w, r, oidcNotice(OIDC_UNREACHABLE, next), http.StatusSeeOther)
			return
		}
		http.SetCookie(w, cookie)
		http.Redirect(w, r, target, http.StatusSeeOther)
	}).Methods("GET")

	web.router.HandleFunc(oidcCallbackPath, web.oidcCallback).Methods("GET")
}

// oidcNotice is the login page with the notice of a sign-in with OpenID Connect.
func oidcNotice(notice string, next string) string {
	target := "/login.html?oidc=" + notice
	if next != "" && safeNext(next) == next {
		target += "&next=" + url.QueryEscape(next)
	}
	return target
}

// oidcCallback is where the provider sends the person back: the sign-in this browser started,
// with the code to change for the ID token. A person it names gets the session of the app.
func (web *Web) oidcCallback(w http.ResponseWriter, r *http.Request) {
	auth := web.auth
	now := time.Now()
	flow, started := auth.readOidcFlow(r, now)
	// a sign-in comes back once
	http.SetCookie(w, auth.oidcCookie(r, oidcFlowCookie, oidcLoginPath, "", 0))
	w.Header().Set("Cache-Control", "no-store")
	if !auth.Enabled() || !oidcReady(auth.portal()) {
		http.Redirect(w, r, "/login.html", http.StatusSeeOther)
		return
	}
	ip := clientIp(r)
	query := r.URL.Query()
	failed := func(notice string, reason string) {
		auth.portalTell(portalEvent{Via: VIA_OIDC, IP: ip, Result: "oidc", Detail: reason})
		http.Redirect(w, r, oidcNotice(notice, flow.Next), http.StatusSeeOther)
	}
	if answer := query.Get("error"); answer != "" {
		// the person said no at the provider
		if answer == "access_denied" && started {
			http.Redirect(w, r, oidcNotice(OIDC_CANCELLED, flow.Next), http.StatusSeeOther)
			return
		}
		failed(OIDC_FAILED, "the provider answered "+oidcText(answer))
		return
	}
	// only the failed sign-ins of this address: a person who signs in with a password is not
	// kept out by them
	keys := []string{"ip:" + ip}
	retryAfter, delay := auth.oidcLimiter.check(keys...)
	if retryAfter > 0 {
		failed(OIDC_FAILED, "too many failed sign-ins from this address")
		return
	}
	if delay > 0 {
		sleepFor(delay)
	}
	if !started || !hmac.Equal([]byte(query.Get("state")), []byte(flow.State)) {
		auth.oidcLimiter.fail(keys...)
		failed(OIDC_FAILED, "the sign-in expired or was started in another browser (state)")
		return
	}
	identity, idToken, err := auth.oidcFinish(r.Context(), flow, query.Get("code"), now)
	if err != nil {
		if errors.Is(err, errOidcUnreachable) {
			failed(OIDC_UNREACHABLE, err.Error())
			return
		}
		auth.oidcLimiter.fail(keys...)
		failed(OIDC_FAILED, err.Error())
		return
	}
	principal, made, err := auth.oidcPrincipal(identity)
	if err != nil {
		// the login page tells the person who the provider says they are
		auth.portalTell(portalEvent{User: identity.named(), Via: VIA_OIDC, IP: ip, Result: "unknown"})
		http.SetCookie(w, auth.oidcCookie(r, oidcWhoCookie, "/login.html", auth.seal("who", []byte(identity.named())), oidcWhoTime))
		http.Redirect(w, r, oidcNotice(OIDC_UNKNOWN, flow.Next), http.StatusSeeOther)
		return
	}
	auth.oidcLimiter.succeed(keys...)
	if made {
		auth.portalTell(portalEvent{User: principal.Name, Via: VIA_OIDC, IP: ip, Result: "made", Detail: principal.stored})
	}
	mark, detail := sessionOidc, ""
	if principal.Role == ROLE_ADMIN && principal.stored != ROLE_ADMIN {
		mark, detail = oidcAdminMark(auth.portal().AdminGroup), ROLE_ADMIN
	}
	auth.portalTell(portalEvent{User: principal.Name, Via: VIA_OIDC, IP: ip, Result: "ok", Detail: detail})
	auth.signedIn(principal.Name)
	http.SetCookie(w, auth.markedSessionCookie(r, principal.Name, mark))
	// the ID token goes back to the provider at Log out, so it knows whose session to end
	if len(idToken) <= maxOidcHint {
		http.SetCookie(w, auth.oidcCookie(r, oidcHintCookie, "/logout", idToken, sessionLifetime))
	} else {
		http.SetCookie(w, auth.oidcCookie(r, oidcHintCookie, "/logout", "", 0))
	}
	redirectLocal(w, r, safeNext(flow.Next))
}

// oidcLoginNotice reads the notice a sign-in with OpenID Connect left for the login page: the
// person the provider named without a user of the app, or how it went.
func (web *Web) oidcLoginNotice(w http.ResponseWriter, r *http.Request, data *LoginPageData) {
	switch notice := r.URL.Query().Get("oidc"); notice {
	case OIDC_UNKNOWN:
		cookie, err := r.Cookie(oidcWhoCookie)
		if err != nil {
			return
		}
		http.SetCookie(w, web.auth.oidcCookie(r, oidcWhoCookie, "/login.html", "", 0))
		if who, ok := web.auth.unseal("who", cookie.Value); ok && len(who) > 0 {
			data.Portal, data.PortalVia = string(who), VIA_OIDC
		}
	case OIDC_FAILED, OIDC_UNREACHABLE, OIDC_CANCELLED:
		data.OidcNotice = notice
	}
}
