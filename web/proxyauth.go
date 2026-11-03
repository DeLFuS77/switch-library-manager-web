package web

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A sign-in portal in front of the app (Authelia, Authentik or another forward-auth of nginx,
// Traefik or Caddy, or Cloudflare Access) already asks who each person is. Turned on, the app
// believes it, and the person opens the app as their user without its password:
//   - a header with the user (Remote-User, with Remote-Email and Remote-Groups when they come),
//     believed only from a proxy set in SLM_TRUSTED_PROXIES, and never from an address of
//     Cloudflare, through which anybody can send anything; from any other address it is ignored
//     and told in Activity, as anybody can write a header;
//   - the token Cloudflare Access adds, believed only with a valid signature of the team, for
//     the application (AUD) and not expired (see cfaccess.go).
// It is off by default and never works while there is no user: the first administrator is still
// made with the setup code. Passwords, basic authentication and API keys keep working as before.
// An environment variable wins over the same option of Settings, which then shows it locked.
// Signing in with OpenID Connect matches people with users the same way (see oidc.go).

const (
	PROXY_HEADER_ENV        = "SLM_AUTH_PROXY_HEADER"
	PROXY_EMAIL_HEADER_ENV  = "SLM_AUTH_PROXY_EMAIL_HEADER"
	PROXY_GROUPS_HEADER_ENV = "SLM_AUTH_PROXY_GROUPS_HEADER"
	PROXY_MATCH_ENV         = "SLM_AUTH_PROXY_MATCH"
	PROXY_NEW_USERS_ENV     = "SLM_AUTH_PROXY_NEW_USERS"
	PROXY_ADMIN_GROUP_ENV   = "SLM_AUTH_PROXY_ADMIN_GROUP"
	PROXY_LOGOUT_URL_ENV    = "SLM_AUTH_PROXY_LOGOUT_URL"
	CF_ACCESS_TEAM_ENV      = "SLM_CF_ACCESS_TEAM"
	CF_ACCESS_AUD_ENV       = "SLM_CF_ACCESS_AUD"

	defaultUserHeader   = "Remote-User"
	defaultEmailHeader  = "Remote-Email"
	defaultGroupsHeader = "Remote-Groups"

	// how a person signed in through a portal: by the header of a proxy, or by Cloudflare Access
	VIA_PROXY      = "proxy"
	VIA_CLOUDFLARE = "cloudflare"

	// how a person is matched with a user; "" is by name or by email
	MATCH_NAME  = "name"
	MATCH_EMAIL = "email"

	// Cloudflare serves this on the domain of every application of Access: it ends the session
	cfAccessLogoutPath = "/cdn-cgi/access/logout"

	// the longest name and groups read from a header
	maxPortalName   = 256
	maxPortalGroups = 4096
)

// errors shown to the user, translated by the interface
var (
	ErrEmail      = errors.New("Write an email like name@example.com.")
	ErrEmailTaken = errors.New("Another user has this email.")
)

// details of the activity, translated by the interface
const (
	portalUnknownDetail   = "no user with this name or email"
	portalUntrustedDetail = "header sent from an address that is not trusted"
	portalTokenDetail     = "Cloudflare Access token not valid"
)

var validEmail = regexp.MustCompile(`^[^\s@<>()",;:\\]+@[^\s@<>()",;:\\]+$`)

// headerNamePattern is the name of a header a portal sends the user in.
var headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// reservedHeaders are headers that carry something else: never taken for the one of the user.
var reservedHeaders = []string{"Authorization", "Cookie", "Host", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto",
	"X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip", CF_ACCESS_HEADER, "Content-Type", "Content-Length", "Origin", "Referer"}

// validHeaderName tells a name a portal may send the user, the email or the groups in.
func validHeaderName(name string) bool {
	if !headerNamePattern.MatchString(name) {
		return false
	}
	return !slices.ContainsFunc(reservedHeaders, func(reserved string) bool { return strings.EqualFold(reserved, name) })
}

// validLogoutUrl tells where Log out may send: a page of this site, or an http or https address.
func validLogoutUrl(address string) bool {
	if address == "" {
		return true
	}
	if strings.ContainsAny(address, "\\ \t\r\n") || len(address) > 2048 {
		return false
	}
	// a page of this site, never //host or /\host, which a browser takes for another site
	if strings.HasPrefix(address, "/") {
		return len(address) == 1 || (address[1] != '/' && address[1] != '\\')
	}
	parsed, err := url.Parse(address)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}

// portalNewUserRoles are the roles of the users a portal may make: they look, or also
// download, and never manage anything.
var portalNewUserRoles = []string{ROLE_GUEST, ROLE_VIEWER}

// readPortalEnv reads the options of the portal set in the environment, and which are set.
func readPortalEnv() (settings.PortalOptions, map[string]bool) {
	options := settings.PortalOptions{}
	set := map[string]bool{}
	read := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value != "" {
			set[name] = true
		}
		return value
	}
	if value := read(PROXY_HEADER_ENV); value != "" {
		options.Header, options.UserHeader = true, value
	}
	options.EmailHeader = read(PROXY_EMAIL_HEADER_ENV)
	options.GroupsHeader = read(PROXY_GROUPS_HEADER_ENV)
	if value := read(CF_ACCESS_TEAM_ENV); value != "" {
		options.Cloudflare, options.CloudflareTeam = true, value
	}
	options.CloudflareAud = read(CF_ACCESS_AUD_ENV)
	switch strings.ToLower(read(PROXY_MATCH_ENV)) {
	case MATCH_NAME:
		options.MatchBy = MATCH_NAME
	case MATCH_EMAIL:
		options.MatchBy = MATCH_EMAIL
	}
	switch strings.ToLower(read(PROXY_NEW_USERS_ENV)) {
	case ROLE_GUEST:
		options.NewUsers = ROLE_GUEST
	case ROLE_VIEWER, "player":
		options.NewUsers = ROLE_VIEWER
	}
	options.AdminGroup = read(PROXY_ADMIN_GROUP_ENV)
	options.LogoutUrl = read(PROXY_LOGOUT_URL_ENV)
	readOidcEnv(&options, read)
	return options, set
}

// portal returns the options of the portal in use: the ones of Settings, with the ones of the
// environment over them, and the default names of the headers.
func (a *Auth) portal() settings.PortalOptions {
	options := settings.PortalOptions{}
	if a.portalSettings != nil {
		options = a.portalSettings()
	}
	return a.withEnv(options)
}

// withEnv returns options of the portal with the ones of the environment over them, and the
// default names of the headers.
func (a *Auth) withEnv(options settings.PortalOptions) settings.PortalOptions {
	env, set := a.portalEnv, a.portalEnvSet
	oidcFromEnv(&options, env, set)
	if set[PROXY_HEADER_ENV] {
		options.Header, options.UserHeader = true, env.UserHeader
	}
	if set[PROXY_EMAIL_HEADER_ENV] {
		options.EmailHeader = env.EmailHeader
	}
	if set[PROXY_GROUPS_HEADER_ENV] {
		options.GroupsHeader = env.GroupsHeader
	}
	if set[CF_ACCESS_TEAM_ENV] {
		options.Cloudflare, options.CloudflareTeam = true, env.CloudflareTeam
	}
	if set[CF_ACCESS_AUD_ENV] {
		options.CloudflareAud = env.CloudflareAud
	}
	if set[PROXY_MATCH_ENV] {
		options.MatchBy = env.MatchBy
	}
	if set[PROXY_NEW_USERS_ENV] {
		options.NewUsers = env.NewUsers
	}
	if set[PROXY_ADMIN_GROUP_ENV] {
		options.AdminGroup = env.AdminGroup
	}
	if set[PROXY_LOGOUT_URL_ENV] {
		options.LogoutUrl = env.LogoutUrl
	}
	if options.UserHeader == "" {
		options.UserHeader = defaultUserHeader
	}
	if options.EmailHeader == "" {
		options.EmailHeader = defaultEmailHeader
	}
	if options.GroupsHeader == "" {
		options.GroupsHeader = defaultGroupsHeader
	}
	return options
}

// headerProxy tells an address whose header of the user is believed: a proxy set in
// SLM_TRUSTED_PROXIES. Never one of Cloudflare, even when it is set there: anybody can send a
// request through Cloudflare, with any header, to the address of the app.
func headerProxy(address string) bool {
	return configuredProxy(address) && !fromCloudflare(strings.Trim(strings.TrimSpace(address), "[]"))
}

// headerProxiesSet tells whether SLM_TRUSTED_PROXIES names a proxy believed for the header.
func headerProxiesSet() bool {
	for _, network := range trustedProxyNets {
		if !fromCloudflare(network.IP.String()) {
			return true
		}
	}
	return false
}

// portalIdentity is who a portal says sends a request.
type portalIdentity struct {
	Via    string
	Name   string
	Email  string
	Groups []string
	// the account of a provider of OpenID Connect, which no other person can take (see oidc.go)
	Subject string
}

// named is how the identity is shown: its name, else its email.
func (i *portalIdentity) named() string {
	if i.Name != "" {
		return i.Name
	}
	return i.Email
}

// inGroup tells a member of a group, in any case.
func (i *portalIdentity) inGroup(group string) bool {
	return slices.ContainsFunc(i.Groups, func(each string) bool { return strings.EqualFold(each, group) })
}

// portalEvent is a sign-in through a portal, or a header or token not believed, as it is told.
type portalEvent struct {
	// the user signed in, or who the portal named
	User string
	Via  string
	IP   string
	// "ok", "made" (a user was made), "unknown" (no user), "untrusted" (a header from an address
	// not trusted), "token" (a token of Cloudflare Access not valid) or "oidc" (a sign-in with
	// OpenID Connect that failed)
	Result string
	// the role a group gave, the role of a user made, or why a token was not valid
	Detail string
}

// headerValue reads a header a proxy sets once: empty when it is missing, sent twice or holds
// control characters.
func headerValue(r *http.Request, name string, limit int) string {
	values := r.Header.Values(name)
	if len(values) != 1 {
		return ""
	}
	value := strings.TrimSpace(values[0])
	if len(value) > limit || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
		return ""
	}
	return value
}

// portalIdentity returns who a portal turned on names in a request, or the problem of a header
// or token that is not believed. Nothing is told here.
func (a *Auth) portalIdentity(r *http.Request) (*portalIdentity, *portalEvent) {
	options := a.portal()
	if options.Header && validHeaderName(options.UserHeader) {
		name := headerValue(r, options.UserHeader, maxPortalName)
		email := ""
		if validHeaderName(options.EmailHeader) {
			email = headerValue(r, options.EmailHeader, maxPortalName)
			if !validEmail.MatchString(email) {
				email = ""
			}
		}
		if name != "" || email != "" {
			address := remoteHost(r)
			if !headerProxy(address) {
				identity := &portalIdentity{Name: name, Email: email}
				return nil, &portalEvent{User: identity.named(), Via: VIA_PROXY, IP: address, Result: "untrusted"}
			}
			identity := &portalIdentity{Via: VIA_PROXY, Name: name, Email: email}
			if validHeaderName(options.GroupsHeader) {
				for _, group := range strings.FieldsFunc(headerValue(r, options.GroupsHeader, maxPortalGroups), func(r rune) bool { return r == ',' || r == '|' }) {
					if group = strings.TrimSpace(group); group != "" {
						identity.Groups = append(identity.Groups, group)
					}
				}
			}
			return identity, nil
		}
	}
	if options.Cloudflare {
		token := r.Header.Get(CF_ACCESS_HEADER)
		team, teamOk := cfTeamName(options.CloudflareTeam)
		if token == "" || !teamOk || !cfAudPattern.MatchString(options.CloudflareAud) {
			return nil, nil
		}
		claims, err := a.cfKeys.verify(token, team, options.CloudflareAud, time.Now())
		if err != nil {
			return nil, &portalEvent{Via: VIA_CLOUDFLARE, IP: clientIp(r), Result: "token", Detail: err.Error()}
		}
		return &portalIdentity{Via: VIA_CLOUDFLARE, Email: claims.Email}, nil
	}
	return nil, nil
}

// fromPortal returns the user a portal says sends a request, making it when the options say so,
// and tells the sign-in. Never while login is off.
func (a *Auth) fromPortal(r *http.Request) *Principal {
	if !a.Enabled() {
		return nil
	}
	identity, problem := a.portalIdentity(r)
	if problem != nil {
		a.portalTell(*problem)
		return nil
	}
	if identity == nil {
		return nil
	}
	ip := clientIp(r)
	principal, made, err := a.portalPrincipal(identity)
	if err != nil {
		a.portalTell(portalEvent{User: identity.named(), Via: identity.Via, IP: ip, Result: "unknown"})
		return nil
	}
	if made {
		a.portalTell(portalEvent{User: principal.Name, Via: identity.Via, IP: ip, Result: "made", Detail: principal.stored})
	}
	detail := ""
	if principal.Role != principal.stored && principal.Role == ROLE_ADMIN {
		detail = ROLE_ADMIN
	}
	a.portalTell(portalEvent{User: principal.Name, Via: identity.Via, IP: ip, Result: "ok", Detail: detail})
	a.signedIn(principal.Name)
	return principal.Principal
}

// portalSignIn is a user a portal signed in, with the role stored for it.
type portalSignIn struct {
	*Principal
	stored string
}

// portalPrincipal returns the user of an identity: by name or by email as the options say (a
// token of Cloudflare names an email only), or a new one when the options make them. The
// members of the group of the administrators are administrators.
func (a *Auth) portalPrincipal(identity *portalIdentity) (portalSignIn, bool, error) {
	options := a.portal()
	match := options.MatchBy
	if identity.Via == VIA_CLOUDFLARE {
		match = MATCH_EMAIL
	}
	var principal portalSignIn
	made := false
	if match != MATCH_EMAIL && a.envUser != "" && identity.Name == a.envUser {
		principal = portalSignIn{&Principal{Name: a.envUser, Role: ROLE_ADMIN, Source: "env", Via: identity.Via}, ROLE_ADMIN}
	} else {
		role := ""
		if slices.Contains(portalNewUserRoles, options.NewUsers) {
			role = options.NewUsers
		}
		user, created, err := a.users.portalUser(identity.Name, identity.Email, match, role)
		if err != nil {
			return portalSignIn{}, false, err
		}
		principal, made = portalSignIn{&Principal{Name: user.Name, Role: user.Role, Via: identity.Via}, user.Role}, created
	}
	if options.AdminGroup != "" && identity.inGroup(options.AdminGroup) {
		principal.Role = ROLE_ADMIN
	}
	return principal, made, nil
}

// portalTell notes a sign-in through a portal for Settings, and tells the hook of it: a sign-in
// once a day by user and address, as the portal signs the person in with every request (with
// OpenID Connect, each one: it opens a session), a user made always, and a person without a
// user, a header from an address not trusted or a token not valid once an hour by address.
func (a *Auth) portalTell(event portalEvent) {
	now := time.Now()
	a.portalSeen.note(event, now)
	if a.onPortal == nil {
		return
	}
	switch event.Result {
	case "ok":
		if event.Via != VIA_OIDC && !a.shouldTell("portal:"+event.User, event.IP, event.Result, apiLoginTellInterval, now) {
			return
		}
	case "made":
	default:
		// whatever names it sends, an address is told once an hour
		if !a.shouldTell("", event.IP, "portal-"+event.Result, retryTellInterval, now) {
			return
		}
		event.User = a.attemptName(event.User)
	}
	a.onPortal(event)
}

// portalLogout is where Log out sends a person signed in by a portal: the address set, the
// page of Cloudflare Access that ends its session, or "" when the app cannot end it.
func (a *Auth) portalLogout(via string) string {
	options := a.portal()
	if options.LogoutUrl != "" && validLogoutUrl(options.LogoutUrl) {
		return options.LogoutUrl
	}
	if via == VIA_CLOUDFLARE {
		return cfAccessLogoutPath
	}
	return ""
}

// portalStatus is the last sign-in through a portal and the last problem, shown in Settings.
type portalStatus struct {
	mutex       sync.Mutex
	lastOk      portalEvent
	lastOkAt    time.Time
	lastProblem portalEvent
	problemAt   time.Time
}

func (s *portalStatus) note(event portalEvent, now time.Time) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if event.Result == "ok" {
		s.lastOk, s.lastOkAt = event, now
	} else if event.Result != "made" {
		s.lastProblem, s.problemAt = event, now
	}
}

// PortalView is the portal in Settings: the options in use, the ones set in the environment and
// how it went last.
type PortalView struct {
	Options settings.PortalOptions
	// the environment variables set, which lock their options
	Env map[string]bool
	// SLM_TRUSTED_PROXIES as it is set, and whether it names a proxy believed for the header
	TrustedProxies string
	HeaderProxies  bool
	// the header or Cloudflare Access is turned on and has what it needs
	HeaderReady     bool
	CloudflareReady bool
	// the last sign-in, and the last header or token not believed (zero times when none)
	LastOk        portalEvent
	LastOkAt      time.Time
	LastProblem   portalEvent
	LastProblemAt time.Time
	// the roles a new user may get
	NewUserRoles []string
	// OpenID Connect is on and has what it needs, the name on its button, and whether a client
	// secret is saved: the secret itself never goes to the page
	OidcReady       bool
	OidcButton      string
	OidcSecretSaved bool
}

func (a *Auth) portalView() PortalView {
	options := a.portal()
	_, teamOk := cfTeamName(options.CloudflareTeam)
	view := PortalView{Options: options, Env: a.portalEnvSet, TrustedProxies: os.Getenv(TRUSTED_PROXIES_ENV), HeaderProxies: headerProxiesSet(),
		HeaderReady:     options.Header && validHeaderName(options.UserHeader) && headerProxiesSet(),
		CloudflareReady: options.Cloudflare && teamOk && cfAudPattern.MatchString(options.CloudflareAud),
		NewUserRoles:    portalNewUserRoles,
		OidcReady:       oidcReady(options), OidcButton: oidcButton(options), OidcSecretSaved: options.OidcClientSecret != ""}
	view.Options.OidcClientSecret = ""
	a.portalSeen.mutex.Lock()
	view.LastOk, view.LastOkAt, view.LastProblem, view.LastProblemAt = a.portalSeen.lastOk, a.portalSeen.lastOkAt, a.portalSeen.lastProblem, a.portalSeen.problemAt
	a.portalSeen.mutex.Unlock()
	return view
}

// FindByEmail returns the user with an email, in any case.
func (s *UserStore) FindByEmail(email string) (User, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	if user := s.byEmailLocked(email); user != nil {
		return user.clone(), true
	}
	return User{}, false
}

// byEmailLocked returns the user with an email. The mutex must be held.
func (s *UserStore) byEmailLocked(email string) *User {
	if email == "" {
		return nil
	}
	for _, user := range s.users {
		if strings.EqualFold(user.Email, email) {
			return user
		}
	}
	return nil
}

// SetEmail sets the email a portal names a user with; "" removes it. No two users have one email.
func (s *UserStore) SetEmail(name string, email string) error {
	email = strings.TrimSpace(email)
	if email != "" && (len(email) > maxPortalName || !validEmail.MatchString(email)) {
		return ErrEmail
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	user, ok := s.users[strings.ToLower(name)]
	if !ok {
		return ErrUserNotFound
	}
	if other := s.byEmailLocked(email); other != nil && other != user {
		return ErrEmailTaken
	}
	// another email names another person: the account of OpenID Connect is linked again at the
	// next sign-in
	if !strings.EqualFold(user.Email, email) {
		user.OidcSubject = ""
	}
	user.Email = email
	return s.save()
}

// portalUser returns the user of a person a portal let in: the one of their name or their email,
// as match says, or else a new one with role (guest or viewer; "" makes none), without a
// password. A portal never makes the first user.
func (s *UserStore) portalUser(name string, email string, match string, role string) (User, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if match != MATCH_EMAIL && name != "" {
		if user, ok := s.users[strings.ToLower(name)]; ok {
			return user.clone(), false, nil
		}
	}
	if match != MATCH_NAME {
		if user := s.byEmailLocked(email); user != nil {
			return user.clone(), false, nil
		}
	}
	if !slices.Contains(portalNewUserRoles, role) || (len(s.users) == 0 && s.reservedName == "") {
		return User{}, false, ErrUserNotFound
	}
	user := s.newPortalUserLocked(name, email, role)
	// only a user the person is matched with the next time: else each request would make another
	if !(match != MATCH_EMAIL && user.Name == name) && !(match != MATCH_NAME && user.Email != "") {
		return User{}, false, ErrUserNotFound
	}
	if err := s.addLocked(user); err != nil {
		return User{}, false, err
	}
	return user.clone(), true, nil
}

// newPortalUserLocked is the user made for a person a portal or a provider let in, with role and
// without a password. The mutex must be held.
func (s *UserStore) newPortalUserLocked(name string, email string, role string) *User {
	user := &User{Name: s.freeNameLocked(portalUserName(name, email)), Role: role, Created: time.Now()}
	if email != "" && s.byEmailLocked(email) == nil {
		user.Email = email
	}
	return user
}

// addLocked stores a new user. The mutex must be held.
func (s *UserStore) addLocked(user *User) error {
	key := strings.ToLower(user.Name)
	s.users[key] = user
	if err := s.save(); err != nil {
		delete(s.users, key)
		return err
	}
	return nil
}

// portalUserName is the name of a user made for a person: the name the portal sent, else the
// part of the email before the @, with only the letters a name may have.
func portalUserName(name string, email string) string {
	if validUserName.MatchString(name) {
		return name
	}
	if local, _, ok := strings.Cut(email, "@"); ok && local != "" {
		name = local
	}
	var cleaned strings.Builder
	for _, r := range name {
		switch {
		case r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-'):
			cleaned.WriteRune(r)
		case unicode.IsSpace(r):
			cleaned.WriteRune('.')
		}
	}
	result := cleaned.String()
	if len(result) > 32 {
		result = result[:32]
	}
	if result = strings.Trim(result, ".-_"); result == "" {
		return "user"
	}
	return result
}

// freeNameLocked returns name, or name with a number when a user has it. The mutex must be held.
func (s *UserStore) freeNameLocked(name string) string {
	taken := func(candidate string) bool {
		_, exists := s.users[strings.ToLower(candidate)]
		return exists || (s.reservedName != "" && strings.EqualFold(candidate, s.reservedName))
	}
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		base := name
		if len(base)+len(suffix) > 32 {
			base = base[:32-len(suffix)]
		}
		if !taken(base + suffix) {
			return base + suffix
		}
	}
}

// portalViaName is how a portal is named in the activity and the pages.
func portalViaName(via string) string {
	switch via {
	case VIA_CLOUDFLARE:
		return "Cloudflare Access"
	case VIA_OIDC:
		return "OpenID Connect"
	}
	return "proxy"
}

// recordPortal notes a sign-in through a portal in the activity log, and a header or token not
// believed in the log of the app too.
func (web *Web) recordPortal(event portalEvent) {
	switch event.Result {
	case "untrusted":
		web.sugarLogger.Warnf("A header of the sign-in portal came from %s, which is not in %s: it was ignored", event.IP, TRUSTED_PROXIES_ENV)
	case "token":
		web.sugarLogger.Warnf("A token of Cloudflare Access was not valid: %s", event.Detail)
	case "oidc":
		web.sugarLogger.Warnf("A sign-in with OpenID Connect failed: %s", event.Detail)
	}
	if isDemoMode() {
		return
	}
	entry := activity.Entry{Time: time.Now(), User: event.User, IP: event.IP}
	switch event.Result {
	case "ok":
		entry.Action, entry.Detail = activity.ACTION_PORTAL_PROXY, event.Detail
		switch event.Via {
		case VIA_CLOUDFLARE:
			entry.Action = activity.ACTION_PORTAL_CF
		case VIA_OIDC:
			entry.Action = activity.ACTION_PORTAL_OIDC
		}
	case "made":
		entry.Action, entry.Detail = activity.ACTION_USER_NEW, event.User+": "+event.Detail
	case "unknown":
		entry.Action, entry.Detail = activity.ACTION_LOGIN_FAIL, portalUnknownDetail
	case "untrusted":
		entry.Action, entry.Detail = activity.ACTION_LOGIN_FAIL, portalUntrustedDetail
	case "token":
		entry.Action, entry.Detail = activity.ACTION_LOGIN_FAIL, portalTokenDetail
	case "oidc":
		entry.Action, entry.Detail = activity.ACTION_LOGIN_FAIL, oidcFailedDetail
	default:
		return
	}
	web.activities().Add(entry)
}
