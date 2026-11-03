package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"golang.org/x/oauth2"
)

// Signing in with OpenID Connect (PocketID, Authentik, Keycloak or another provider): the login
// page shows a button that sends the person to the provider, which sends them back with a code.
// The app changes the code for an ID token (with PKCE and the client secret), checks it (the
// signature with the keys of the provider, the issuer, the audience, the expiry and the nonce of
// this sign-in) and gives the person the usual session of the app, as the user the portal options
// match them with (see proxyauth.go: by name or by email, users made only as guests or players,
// the administrators group). Unlike the header of a proxy, the provider is asked only at sign-in.
// The provider is read over https only. It is off by default and never works while there is no
// user: the first administrator is still made with the setup code.

const (
	OIDC_ISSUER_ENV        = "SLM_OIDC_ISSUER"
	OIDC_CLIENT_ID_ENV     = "SLM_OIDC_CLIENT_ID"
	OIDC_CLIENT_SECRET_ENV = "SLM_OIDC_CLIENT_SECRET"
	OIDC_BUTTON_ENV        = "SLM_OIDC_BUTTON"
	OIDC_SCOPES_ENV        = "SLM_OIDC_SCOPES"

	// signed in with OpenID Connect
	VIA_OIDC = "oidc"

	oidcLoginPath    = "/login/oidc"
	oidcCallbackPath = "/login/oidc/callback"

	// the sign-in on its way to the provider and back, the name of a person without a user for
	// the login page, and the ID token Log out hands the provider back
	oidcFlowCookie = "slm_oidc_flow"
	oidcWhoCookie  = "slm_oidc_who"
	oidcHintCookie = "slm_oidc_hint"
	oidcFlowTime   = 10 * time.Minute
	oidcWhoTime    = 2 * time.Minute
	// a longer ID token is not kept: the cookie would be too large for the browsers
	maxOidcHint = 3000

	defaultOidcScopes = "openid profile email groups"
	defaultOidcButton = "SSO"
	maxOidcButton     = 40
	maxOidcScopes     = 512
	maxOidcField      = 2048

	// what the app read of the provider is kept this long, and a provider that could not be
	// read is asked again after a while
	oidcProviderTime = 24 * time.Hour
	oidcRetryAfter   = time.Minute
	// the longest wait for the provider, and the largest answer read from it
	oidcTimeout     = 15 * time.Second
	maxOidcResponse = 1 << 20

	// the notices of the login page, by the answer of the provider
	OIDC_UNKNOWN     = "unknown"
	OIDC_FAILED      = "failed"
	OIDC_UNREACHABLE = "unreachable"
	OIDC_CANCELLED   = "cancelled"
)

// details of the activity, translated by the interface
const oidcFailedDetail = "OpenID Connect sign-in not valid"

var (
	errOidcHttps       = errors.New("the provider must be reached over https")
	errOidcUnreachable = errors.New("the provider could not be reached")
)

// shown in Users, translated by the interface
var ErrOidcNotLinked = errors.New("The user is not linked with an account of the provider.")

// readOidcEnv reads the options of OpenID Connect set in the environment: an issuer turns it on.
func readOidcEnv(options *settings.PortalOptions, read func(name string) string) {
	if value := read(OIDC_ISSUER_ENV); value != "" {
		options.Oidc, options.OidcIssuer = true, normalIssuer(value)
	}
	options.OidcClientId = read(OIDC_CLIENT_ID_ENV)
	options.OidcClientSecret = read(OIDC_CLIENT_SECRET_ENV)
	options.OidcButton = read(OIDC_BUTTON_ENV)
	options.OidcScopes = read(OIDC_SCOPES_ENV)
}

// oidcFromEnv puts the options of OpenID Connect set in the environment over the ones of
// Settings.
func oidcFromEnv(options *settings.PortalOptions, env settings.PortalOptions, set map[string]bool) {
	if set[OIDC_ISSUER_ENV] {
		options.Oidc, options.OidcIssuer = true, env.OidcIssuer
	}
	if set[OIDC_CLIENT_ID_ENV] {
		options.OidcClientId = env.OidcClientId
	}
	if set[OIDC_CLIENT_SECRET_ENV] {
		options.OidcClientSecret = env.OidcClientSecret
	}
	if set[OIDC_BUTTON_ENV] {
		options.OidcButton = env.OidcButton
	}
	if set[OIDC_SCOPES_ENV] {
		options.OidcScopes = env.OidcScopes
	}
}

// normalIssuer is the address of a provider as it is kept: without spaces, and without the end
// of the address of its configuration, which some providers show instead.
func normalIssuer(issuer string) string {
	return strings.TrimSuffix(strings.TrimSpace(issuer), "/.well-known/openid-configuration")
}

// validIssuer tells the address of a provider: https, with a host, no user, query or fragment.
func validIssuer(issuer string) bool {
	if issuer == "" || len(issuer) > maxOidcField || strings.ContainsFunc(issuer, unicode.IsSpace) {
		return false
	}
	parsed, err := url.Parse(issuer)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && !parsed.ForceQuery
}

// validOidcScopes tells scopes separated by spaces, of the characters RFC 6749 allows.
func validOidcScopes(scopes string) bool {
	if len(scopes) > maxOidcScopes {
		return false
	}
	for _, r := range scopes {
		if r != ' ' && (r < 0x21 || r > 0x7e || r == '"' || r == '\\') {
			return false
		}
	}
	return true
}

// oidcScopes are the scopes asked for: the ones set, or the default ones, always with openid.
func oidcScopes(options settings.PortalOptions) []string {
	scopes := strings.Fields(options.OidcScopes)
	if len(scopes) == 0 {
		scopes = strings.Fields(defaultOidcScopes)
	}
	for _, scope := range scopes {
		if scope == oidc.ScopeOpenID {
			return scopes
		}
	}
	return append([]string{oidc.ScopeOpenID}, scopes...)
}

// oidcButton is the name on the button of the login page.
func oidcButton(options settings.PortalOptions) string {
	if name := strings.TrimSpace(options.OidcButton); name != "" {
		return name
	}
	return defaultOidcButton
}

// oidcReady tells whether signing in with OpenID Connect is on and has what it needs.
func oidcReady(options settings.PortalOptions) bool {
	return options.Oidc && validIssuer(options.OidcIssuer) && options.OidcClientId != ""
}

// httpsOnly refuses a request to the provider that is not over https, also when the provider
// redirects to one, and reads at most maxOidcResponse of each answer.
type httpsOnly struct{ next http.RoundTripper }

func (t httpsOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		return nil, errOidcHttps
	}
	response, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	response.Body = limitedBody{io.LimitReader(response.Body, maxOidcResponse), response.Body}
	return response, nil
}

type limitedBody struct {
	io.Reader
	io.Closer
}

// newOidcClient is the client of the requests to providers, over https only.
func newOidcClient(transport http.RoundTripper) *http.Client {
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	return &http.Client{Timeout: oidcTimeout, Transport: httpsOnly{transport}}
}

// oidcProvider is what the app read of a provider: its addresses and keys, and the page that
// ends its session ("" when it has none).
type oidcProvider struct {
	provider   *oidc.Provider
	endSession string
}

// oidcProviders keeps the provider read last, so a sign-in does not read it again.
type oidcProviders struct {
	mutex    sync.Mutex
	issuer   string
	current  *oidcProvider
	readAt   time.Time
	err      error
	failedAt time.Time
	// the requests to the provider, made with the Auth (the tests set their own, still over
	// https only)
	client *http.Client
}

func (p *oidcProviders) httpClient() *http.Client {
	if p.client == nil {
		return newOidcClient(nil)
	}
	return p.client
}

// get returns the provider of an issuer, read again once a day; while it cannot be read, the
// one read before. A provider that could not be read is not asked again for a minute.
func (p *oidcProviders) get(issuer string) (*oidcProvider, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	now := time.Now()
	if p.issuer != issuer {
		p.issuer, p.current, p.err = issuer, nil, nil
	}
	if p.current != nil && now.Sub(p.readAt) < oidcProviderTime {
		return p.current, nil
	}
	if p.err != nil && now.Sub(p.failedAt) < oidcRetryAfter {
		if p.current != nil {
			return p.current, nil
		}
		return nil, p.err
	}
	provider, err := discoverOidc(p.httpClient(), issuer)
	if err != nil {
		p.err, p.failedAt = err, now
		if p.current != nil {
			return p.current, nil
		}
		return nil, err
	}
	p.current, p.readAt, p.err = provider, now, nil
	return provider, nil
}

// check reads a provider now, as Settings saves it, and keeps it when it can be read.
func (p *oidcProviders) check(issuer string) error {
	provider, err := discoverOidc(p.httpClient(), issuer)
	if err != nil {
		return err
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.issuer, p.current, p.readAt, p.err = issuer, provider, time.Now(), nil
	return nil
}

// discoverOidc reads the configuration of a provider: the issuer it names must be the one
// given, and every address of it must be https.
func discoverOidc(client *http.Client, issuer string) (*oidcProvider, error) {
	ctx, cancel := context.WithTimeout(oidc.ClientContext(context.Background(), client), oidcTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	var metadata struct {
		JwksUri    string `json:"jwks_uri"`
		EndSession string `json:"end_session_endpoint"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, err
	}
	endpoint := provider.Endpoint()
	for _, address := range []string{endpoint.AuthURL, endpoint.TokenURL, metadata.JwksUri} {
		if !httpsAddress(address) {
			return nil, fmt.Errorf("%w: %s", errOidcHttps, address)
		}
	}
	result := &oidcProvider{provider: provider}
	// a page to end the session that is not https is not used
	if httpsAddress(metadata.EndSession) {
		result.endSession = metadata.EndSession
	}
	return result, nil
}

func httpsAddress(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

// oidcConfig is the client of the app at the provider, for a redirect address.
func oidcConfig(options settings.PortalOptions, provider *oidcProvider, redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: options.OidcClientId, ClientSecret: options.OidcClientSecret, Endpoint: provider.provider.Endpoint(),
		RedirectURL: redirect, Scopes: oidcScopes(options)}
}

// oidcRedirect is the address the provider sends people back to: this site as the browser opened
// the login page, which the button sends as the Referer (a proxy may have changed the host and
// the scheme of the request), else as the request opened it. It is the one Settings shows. Taken
// from a page of another site, it is not one the provider knows: it refuses it, and the code of a
// provider that would not is of no use without the secret of PKCE of this browser.
func oidcRedirect(r *http.Request) string {
	if page, err := url.Parse(r.Referer()); err == nil && (page.Scheme == "https" || page.Scheme == "http") && page.Host != "" && page.User == nil && page.Path == "/login.html" {
		return page.Scheme + "://" + page.Host + oidcCallbackPath
	}
	scheme := "http"
	if isHttps(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + oidcCallbackPath
}

// oidcFlow is a sign-in on its way to the provider, kept in a signed cookie of the browser until
// it comes back: what the answer must carry (state), what the ID token must carry (nonce), the
// secret of PKCE, the redirect address given and the page to open after.
type oidcFlow struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Redirect string `json:"r"`
	Next     string `json:"x"`
	Expiry   int64  `json:"e"`
}

// seal signs a value for a cookie, for one purpose only.
func (a *Auth) seal(purpose string, value []byte) string {
	encoded := base64.RawURLEncoding.EncodeToString(value)
	return encoded + "." + a.sealMac(purpose, encoded)
}

func (a *Auth) sealMac(purpose string, encoded string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte("oidc\x00" + purpose + "\x00" + encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// unseal returns a value signed for a purpose, or false.
func (a *Auth) unseal(purpose string, sealed string) ([]byte, bool) {
	encoded, mac, ok := strings.Cut(sealed, ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(a.sealMac(purpose, encoded))) {
		return nil, false
	}
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	return value, err == nil
}

func (a *Auth) oidcCookie(r *http.Request, name string, path string, value string, lifetime time.Duration) *http.Cookie {
	cookie := &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHttps(r)}
	if lifetime > 0 {
		cookie.Expires, cookie.MaxAge = time.Now().Add(lifetime), int(lifetime.Seconds())
	} else {
		cookie.MaxAge = -1
	}
	return cookie
}

// readOidcFlow returns the sign-in this browser started, while it has not expired.
func (a *Auth) readOidcFlow(r *http.Request, now time.Time) (oidcFlow, bool) {
	cookie, err := r.Cookie(oidcFlowCookie)
	if err != nil {
		return oidcFlow{}, false
	}
	value, ok := a.unseal("flow", cookie.Value)
	if !ok {
		return oidcFlow{}, false
	}
	var flow oidcFlow
	if json.Unmarshal(value, &flow) != nil || flow.State == "" || now.Unix() > flow.Expiry {
		return oidcFlow{}, false
	}
	return flow, true
}

// oidcStart starts a sign-in: the cookie that keeps it, and the address of the provider to send
// the person to.
func (a *Auth) oidcStart(r *http.Request, next string) (*http.Cookie, string, error) {
	options := a.portal()
	provider, err := a.oidc.get(options.OidcIssuer)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", errOidcUnreachable, err)
	}
	flow := oidcFlow{State: rand.Text(), Nonce: rand.Text(), Verifier: oauth2.GenerateVerifier(), Redirect: oidcRedirect(r), Next: safeNext(next),
		Expiry: time.Now().Add(oidcFlowTime).Unix()}
	value, err := json.Marshal(flow)
	if err != nil {
		return nil, "", err
	}
	target := oidcConfig(options, provider, flow.Redirect).AuthCodeURL(flow.State, oidc.Nonce(flow.Nonce), oauth2.S256ChallengeOption(flow.Verifier))
	return a.oidcCookie(r, oidcFlowCookie, oidcLoginPath, a.seal("flow", value), oidcFlowTime), target, nil
}

// oidcClaims are what the app reads of the ID token: the account, the name and the email,
// whether the provider checked the email, and the groups.
type oidcClaims struct {
	Subject           string          `json:"sub"`
	PreferredUsername string          `json:"preferred_username"`
	Email             string          `json:"email"`
	EmailVerified     json.RawMessage `json:"email_verified"`
	Groups            json.RawMessage `json:"groups"`
}

// oidcText is a name or an email of a token as it is used: empty when it is too long or holds
// control characters.
func oidcText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxPortalName || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
		return ""
	}
	return value
}

// identity is who the token of a provider names. An email the provider does not say it checked
// is not used: anybody could have given it to the provider.
func (c oidcClaims) identity(issuer string) *portalIdentity {
	identity := &portalIdentity{Via: VIA_OIDC, Name: oidcText(c.PreferredUsername), Subject: oidcSubject(issuer, c.Subject)}
	if email := oidcText(c.Email); validEmail.MatchString(email) {
		var verified any
		if json.Unmarshal(c.EmailVerified, &verified) == nil && (verified == true || verified == "true") {
			identity.Email = email
		}
	}
	// a list of groups, or one group alone
	var groups []string
	if json.Unmarshal(c.Groups, &groups) != nil {
		var group string
		if json.Unmarshal(c.Groups, &group) == nil {
			groups = []string{group}
		}
	}
	for _, group := range groups {
		if group = oidcText(group); group != "" {
			identity.Groups = append(identity.Groups, group)
		}
	}
	return identity
}

// oidcFinish changes the code the provider sent back for an ID token, checks it and returns who
// it names, and the token itself. Errors of errOidcUnreachable mean the provider did not answer.
func (a *Auth) oidcFinish(ctx context.Context, flow oidcFlow, code string, now time.Time) (*portalIdentity, string, error) {
	options := a.portal()
	provider, err := a.oidc.get(options.OidcIssuer)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", errOidcUnreachable, err)
	}
	if code == "" {
		return nil, "", errors.New("the provider sent no code")
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, a.oidc.httpClient()), oidcTimeout)
	defer cancel()
	token, err := oidcConfig(options, provider, flow.Redirect).Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		var refused *oauth2.RetrieveError
		if errors.As(err, &refused) {
			return nil, "", fmt.Errorf("the provider refused the code: %w", err)
		}
		return nil, "", fmt.Errorf("%w: %w", errOidcUnreachable, err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return nil, "", errors.New("the provider sent no ID token")
	}
	verifier := provider.provider.Verifier(&oidc.Config{ClientID: options.OidcClientId, Now: func() time.Time { return now }})
	idToken, err := verifier.Verify(ctx, raw)
	if err != nil {
		return nil, "", err
	}
	if !hmac.Equal([]byte(idToken.Nonce), []byte(flow.Nonce)) {
		return nil, "", errors.New("the ID token is not of this sign-in (nonce)")
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, "", err
	}
	if claims.Subject == "" {
		return nil, "", errors.New("the ID token names no account (sub)")
	}
	identity := claims.identity(idToken.Issuer)
	if identity.Name == "" && identity.Email == "" {
		return nil, "", errors.New("the ID token names nobody (no preferred_username nor verified email)")
	}
	return identity, raw, nil
}

// oidcSubject is how the account of a provider is kept on the user it is linked with: a hash
// of the issuer and the subject, which the provider never gives to another account.
func oidcSubject(issuer string, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return hex.EncodeToString(sum[:])
}

// oidcPrincipal returns the user of a person a provider names: the one linked with their
// account, else, at their first sign-in, the one of their name or their email as the options say
// (or a new one when they make them), which is linked with it from then on. A user linked with
// another account is never taken, so a person who renames themselves at the provider cannot come
// in as somebody else. The user set in the environment signs in with its password only: it has
// nowhere to keep the link. The members of the group of the administrators are administrators.
func (a *Auth) oidcPrincipal(identity *portalIdentity) (portalSignIn, bool, error) {
	options := a.portal()
	role := ""
	if slices.Contains(portalNewUserRoles, options.NewUsers) {
		role = options.NewUsers
	}
	user, made, err := a.users.oidcUser(identity.Subject, identity.Name, identity.Email, options.MatchBy, role)
	if err != nil {
		return portalSignIn{}, false, err
	}
	principal := portalSignIn{&Principal{Name: user.Name, Role: user.Role, Via: VIA_OIDC}, user.Role}
	if options.AdminGroup != "" && identity.inGroup(options.AdminGroup) {
		principal.Role = ROLE_ADMIN
	}
	return principal, made, nil
}

// oidcUser returns the user linked with an account of a provider, or links one: see
// oidcPrincipal.
func (s *UserStore) oidcUser(subject string, name string, email string, match string, role string) (User, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, user := range s.users {
		if user.OidcSubject == subject {
			return user.clone(), false, nil
		}
	}
	var found *User
	if user, ok := s.users[strings.ToLower(name)]; ok && match != MATCH_EMAIL && name != "" && user.OidcSubject == "" {
		found = user
	} else if user := s.byEmailLocked(email); user != nil && match != MATCH_NAME && user.OidcSubject == "" {
		found = user
	}
	made := false
	if found == nil {
		// a provider never makes the first user
		if !slices.Contains(portalNewUserRoles, role) || (len(s.users) == 0 && s.reservedName == "") {
			return User{}, false, ErrUserNotFound
		}
		found, made = s.newPortalUserLocked(name, email, role), true
	}
	found.OidcSubject = subject
	if made {
		if err := s.addLocked(found); err != nil {
			return User{}, false, err
		}
	} else if err := s.save(); err != nil {
		found.OidcSubject = ""
		return User{}, false, err
	}
	return found.clone(), made, nil
}

// UnlinkOidc removes the link of a user with the account of a provider, which ends the sessions
// opened with it (see markTag). The next sign-in with OpenID Connect links the user again, as
// the first one did.
func (s *UserStore) UnlinkOidc(name string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	user, ok := s.users[strings.ToLower(name)]
	if !ok {
		return ErrUserNotFound
	}
	subject := user.OidcSubject
	if subject == "" {
		return ErrOidcNotLinked
	}
	user.OidcSubject = ""
	if err := s.save(); err != nil {
		user.OidcSubject = subject
		return err
	}
	return nil
}

// oidcLogout is where Log out sends a person signed in with OpenID Connect: the page of the
// provider that ends its session, or "" when it has none.
func (a *Auth) oidcLogout(r *http.Request) string {
	options := a.portal()
	if !oidcReady(options) {
		return ""
	}
	provider, err := a.oidc.get(options.OidcIssuer)
	if err != nil || provider.endSession == "" {
		return ""
	}
	target, err := url.Parse(provider.endSession)
	if err != nil {
		return ""
	}
	query := target.Query()
	query.Set("client_id", options.OidcClientId)
	if hint, err := r.Cookie(oidcHintCookie); err == nil && hint.Value != "" {
		query.Set("id_token_hint", hint.Value)
	}
	target.RawQuery = query.Encode()
	return target.String()
}

// the length of the hash of the group in the mark of a session of an administrator by the group
const oidcAdminMarkLength = 16

// oidcAdminMark is the mark of a session a group made an administrator: it names the group, so
// another group set later does not make the person an administrator.
func oidcAdminMark(group string) string {
	sum := sha256.Sum256([]byte("admin group\x00" + strings.ToLower(group)))
	return sessionOidcAdmin + hex.EncodeToString(sum[:])[:oidcAdminMarkLength]
}

// oidcAdminGroupOn tells whether a session a group made an administrator still is one: while
// OpenID Connect is on and the group is still the one of the administrators.
func (a *Auth) oidcAdminGroupOn(mark string) bool {
	options := a.portal()
	return oidcReady(options) && options.AdminGroup != "" && mark == oidcAdminMark(options.AdminGroup)
}

// oidcIssuerProblem is the error of an issuer in Settings: the address the provider names
// itself with, when it is another one.
func oidcIssuerProblem(err error) (string, bool) {
	var mismatch *oidc.IssuerMismatchError
	if errors.As(err, &mismatch) {
		return mismatch.Discovered, true
	}
	return "", false
}
