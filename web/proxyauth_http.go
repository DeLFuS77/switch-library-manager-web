package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// errors of the options of the portal, shown in Settings and translated by the interface
var (
	ErrPortalHeader = errors.New("Write the name of a header, like Remote-User.")
	ErrPortalTeam   = errors.New("Write the team name of Cloudflare Zero Trust, like myteam.")
	ErrPortalAud    = errors.New("Paste the Application Audience (AUD) tag of the application in Cloudflare Access.")
	ErrPortalLogout = errors.New("Write an address that starts with https:// or /.")
	ErrPortalGroup  = errors.New("Write the name of one group, without commas.")
	ErrPortalOption = errors.New("Unknown option")
)

// portalOptionsFromForm reads the options of the portal sent from Settings over the ones there
// are: an option set in the environment is locked, and its field is not sent. A wrong one is told
// with the field it is in.
func portalOptionsFromForm(r *http.Request, current settings.PortalOptions, env map[string]bool) (settings.PortalOptions, string, error) {
	options := current
	set := func(variable string, field *string, value string) {
		if !env[variable] {
			*field = strings.TrimSpace(value)
		}
	}
	if !env[PROXY_HEADER_ENV] {
		options.Header = r.FormValue("portal_header") == "true"
	}
	set(PROXY_HEADER_ENV, &options.UserHeader, r.FormValue("portal_user_header"))
	set(PROXY_EMAIL_HEADER_ENV, &options.EmailHeader, r.FormValue("portal_email_header"))
	set(PROXY_GROUPS_HEADER_ENV, &options.GroupsHeader, r.FormValue("portal_groups_header"))
	if !env[CF_ACCESS_TEAM_ENV] {
		options.Cloudflare = r.FormValue("portal_cloudflare") == "true"
	}
	set(CF_ACCESS_TEAM_ENV, &options.CloudflareTeam, r.FormValue("portal_cf_team"))
	set(CF_ACCESS_AUD_ENV, &options.CloudflareAud, r.FormValue("portal_cf_aud"))
	set(PROXY_MATCH_ENV, &options.MatchBy, r.FormValue("portal_match"))
	set(PROXY_NEW_USERS_ENV, &options.NewUsers, r.FormValue("portal_new_users"))
	set(PROXY_ADMIN_GROUP_ENV, &options.AdminGroup, r.FormValue("portal_admin_group"))
	set(PROXY_LOGOUT_URL_ENV, &options.LogoutUrl, r.FormValue("portal_logout_url"))

	for _, header := range []struct{ field, name string }{{"portal_user_header", options.UserHeader}, {"portal_email_header", options.EmailHeader}, {"portal_groups_header", options.GroupsHeader}} {
		if header.name != "" && !validHeaderName(header.name) {
			return options, header.field, ErrPortalHeader
		}
	}
	if options.CloudflareTeam != "" || options.Cloudflare {
		team, ok := cfTeamName(options.CloudflareTeam)
		if !ok {
			return options, "portal_cf_team", ErrPortalTeam
		}
		options.CloudflareTeam = team
	}
	if (options.CloudflareAud != "" || options.Cloudflare) && !cfAudPattern.MatchString(options.CloudflareAud) {
		return options, "portal_cf_aud", ErrPortalAud
	}
	if options.MatchBy != "" && options.MatchBy != MATCH_NAME && options.MatchBy != MATCH_EMAIL {
		return options, "portal_match", ErrPortalOption
	}
	if options.NewUsers != "" && !slices.Contains(portalNewUserRoles, options.NewUsers) {
		return options, "portal_new_users", ErrPortalOption
	}
	if len(options.AdminGroup) > maxPortalName || strings.ContainsFunc(options.AdminGroup, unicode.IsControl) || strings.ContainsAny(options.AdminGroup, ",|") {
		return options, "portal_admin_group", ErrPortalGroup
	}
	if !validLogoutUrl(options.LogoutUrl) {
		return options, "portal_logout_url", ErrPortalLogout
	}
	return options, "", nil
}

// handlePortal serves the options of the portal in Settings and the emails of the users, both for
// administrators only.
func (web *Web) handlePortal() {
	// sent by the scripts of Settings, as its other forms: a wrong option is shown under its field
	web.router.HandleFunc("/settings/portal", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		current := settings.ReadSettings(web.dataFolder).Portal
		options, field, err := portalOptionsFromForm(r, current, web.auth.portalEnvSet)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			encoder.Encode(ErrorResponse{FieldErrors: []FieldError{{Field: field, Message: translate(lang, err.Error())}}})
			return
		}
		if _, err := settings.TryUpdateSettings(web.dataFolder, func(appSettings *settings.AppSettings) {
			appSettings.Portal = options
		}); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			encoder.Encode(notSaved(lang, err).failure)
			return
		}
		encoder.Encode(SuccessResponse{StrongMessage: translate(lang, "Success!"), Message: translate(lang, "The options of the sign-in portal were saved.")})
	}).Methods("POST")

	web.router.HandleFunc("/users/email", func(w http.ResponseWriter, r *http.Request) {
		if err := web.auth.users.SetEmail(r.FormValue("name"), r.FormValue("email")); err != nil {
			redirectWithError(w, r, "/users.html", err)
			return
		}
		http.Redirect(w, r, "/users.html?done=email", http.StatusSeeOther)
	}).Methods("POST")
}

// logPortalProblems tells in the log, at the start, an option of the portal that keeps it from
// working, so a container set up with variables shows why.
func (web *Web) logPortalProblems() {
	if web.auth == nil {
		return
	}
	view := web.auth.portalView()
	options := view.Options
	if options.Header {
		switch {
		case !validHeaderName(options.UserHeader):
			web.sugarLogger.Warnf("The header of the sign-in portal %q is not a valid header name: it is ignored", options.UserHeader)
		case !view.HeaderProxies:
			web.sugarLogger.Warnf("Signing in with the header %s is on, but %s names no proxy: the header is ignored until it names the address of the proxy", options.UserHeader, TRUSTED_PROXIES_ENV)
		}
	}
	if options.Cloudflare && !view.CloudflareReady {
		web.sugarLogger.Warnf("Signing in with Cloudflare Access is on, but it needs the team (%s) and the AUD tag of the application (%s)", CF_ACCESS_TEAM_ENV, CF_ACCESS_AUD_ENV)
	}
}
