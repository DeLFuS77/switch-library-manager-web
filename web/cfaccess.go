package web

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Cloudflare Access signs a token for every request it lets through to the application, and adds
// it in the Cf-Access-Jwt-Assertion header. Its signature is checked with the public keys of the
// team, which Cloudflare publishes at https://<team>.cloudflareaccess.com/cdn-cgi/access/certs,
// and then its audience (the AUD tag of the application), its issuer (the team) and its expiry.
// The standard library is enough: the tokens are RS256, a single algorithm checked by hand.

const (
	CF_ACCESS_HEADER = "Cf-Access-Jwt-Assertion"
	// the keys are fetched again after this time; Cloudflare keeps an old key valid for days
	// after it starts signing with a new one
	cfKeysMaxAge = 6 * time.Hour
	// a key that is not known yet makes the keys be fetched again, at most this often
	cfKeysRetry = time.Minute
	// the clocks of Cloudflare and of this computer may differ a little
	cfClockSkew = time.Minute
	// the longest token and list of keys read
	maxCfToken    = 16 << 10
	maxCfKeysSize = 1 << 20
)

var (
	errCfToken    = errors.New("the token is not a JSON web token")
	errCfAlg      = errors.New("the token is not signed with RS256")
	errCfKey      = errors.New("the token is signed with a key the team does not have")
	errCfSigned   = errors.New("the signature of the token is wrong")
	errCfAudience = errors.New("the token is for another application (AUD)")
	errCfIssuer   = errors.New("the token is of another team")
	errCfExpired  = errors.New("the token has expired")
	errCfEarly    = errors.New("the token is not valid yet")
	errCfEmail    = errors.New("the token names no person (a service token)")
)

// cfTeamPattern is the name of a team of Cloudflare Zero Trust, the first part of its
// cloudflareaccess.com domain.
var cfTeamPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// cfAudPattern is the AUD tag of an application of Cloudflare Access: 64 hexadecimal digits
// today, any letters and digits are taken.
var cfAudPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,256}$`)

// cfTeamName reads the team as it is written: the name, its domain or the address of the domain.
func cfTeamName(team string) (string, bool) {
	team = strings.ToLower(strings.TrimSpace(team))
	team = strings.TrimPrefix(team, "https://")
	team = strings.TrimSuffix(strings.TrimSuffix(team, "/"), ".cloudflareaccess.com")
	return team, cfTeamPattern.MatchString(team)
}

// cfAccessCertsURL is where the keys of a team are published; the tests point it at their own
// server.
var cfAccessCertsURL = func(team string) string {
	return "https://" + team + ".cloudflareaccess.com/cdn-cgi/access/certs"
}

// cfAccessIssuer is the issuer of the tokens of a team.
func cfAccessIssuer(team string) string {
	return "https://" + team + ".cloudflareaccess.com"
}

// cfAccessClient fetches the keys: from an https address only, without following redirects.
var cfAccessClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// cfAccessKeys keeps the public keys of a team.
type cfAccessKeys struct {
	mutex sync.Mutex
	team  string
	keys  map[string]*rsa.PublicKey
	// when the keys were fetched, and when a fetch was last tried
	fetched time.Time
	tried   time.Time
	// fetches one at a time; the others use the keys there are meanwhile
	fetching sync.Mutex
}

// key returns the key of an id, fetching the keys when they are old or the id is not known.
func (c *cfAccessKeys) key(team string, id string, now time.Time) (*rsa.PublicKey, error) {
	c.mutex.Lock()
	if c.team != team {
		c.team, c.keys, c.fetched, c.tried = team, nil, time.Time{}, time.Time{}
	}
	key, known := c.keys[id]
	stale := now.Sub(c.fetched) > cfKeysMaxAge
	retry := now.Sub(c.tried) >= cfKeysRetry
	c.mutex.Unlock()
	if (known && !stale) || !retry {
		if !known {
			return nil, errCfKey
		}
		return key, nil
	}

	c.fetching.Lock()
	defer c.fetching.Unlock()
	c.mutex.Lock()
	// another request fetched them meanwhile
	if c.team == team && now.Sub(c.tried) < cfKeysRetry {
		key, known = c.keys[id]
		c.mutex.Unlock()
		if !known {
			return nil, errCfKey
		}
		return key, nil
	}
	c.tried = now
	c.mutex.Unlock()

	keys, err := fetchCfAccessKeys(team)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if err != nil {
		// the keys there are stay in use until the fetch works again
		if known {
			return key, nil
		}
		return nil, fmt.Errorf("the keys of the team could not be fetched: %w", err)
	}
	if c.team == team {
		c.keys, c.fetched = keys, now
	}
	if key, known = keys[id]; !known {
		return nil, errCfKey
	}
	return key, nil
}

// fetchCfAccessKeys downloads the public keys of a team, by their id.
func fetchCfAccessKeys(team string) (map[string]*rsa.PublicKey, error) {
	address := cfAccessCertsURL(team)
	// the keys decide who signs in: never fetched over plain http
	if !strings.HasPrefix(address, "https://") {
		return nil, errNotHttps
	}
	response, err := cfAccessClient.Get(address)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %v", response.Status)
	}
	var document struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxCfKeysSize)).Decode(&document); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, jwk := range document.Keys {
		if jwk.Kty != "RSA" || (jwk.Alg != "" && jwk.Alg != "RS256") || jwk.Kid == "" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(jwk.N)
		e, errE := base64.RawURLEncoding.DecodeString(jwk.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent<<8 | int(b)
		}
		key := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
		// a short key, or a broken one, is not taken
		if key.N.BitLen() < 2048 || exponent < 3 || exponent%2 == 0 {
			continue
		}
		keys[jwk.Kid] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("the team publishes no RSA key")
	}
	return keys, nil
}

var errNotHttps = errors.New("not an https address")

// cfAudience is the aud of a token: one string or a list of them.
type cfAudience []string

func (a *cfAudience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = cfAudience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// cfClaims are what the app reads of a token.
type cfClaims struct {
	Audience  cfAudience `json:"aud"`
	Issuer    string     `json:"iss"`
	Expires   int64      `json:"exp"`
	NotBefore int64      `json:"nbf"`
	Email     string     `json:"email"`
}

// verify checks a token of Cloudflare Access and returns its claims: signed with a key of the
// team, for the application of aud, issued by the team and not expired.
func (c *cfAccessKeys) verify(token string, team string, aud string, now time.Time) (cfClaims, error) {
	if len(token) > maxCfToken {
		return cfClaims{}, errCfToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return cfClaims{}, errCfToken
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeJwtPart(parts[0], &header); err != nil {
		return cfClaims{}, errCfToken
	}
	// the algorithm is never the one the token asks for: only RS256, never none nor HS256
	if header.Alg != "RS256" {
		return cfClaims{}, errCfAlg
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return cfClaims{}, errCfToken
	}
	key, err := c.key(team, header.Kid, now)
	if err != nil {
		return cfClaims{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return cfClaims{}, errCfSigned
	}
	// signed by the team: what it says is read now
	var claims cfClaims
	if err := decodeJwtPart(parts[1], &claims); err != nil {
		return cfClaims{}, errCfToken
	}
	audience := false
	for _, each := range claims.Audience {
		if each == aud {
			audience = true
		}
	}
	switch {
	case !audience:
		return cfClaims{}, errCfAudience
	case claims.Issuer != cfAccessIssuer(team):
		return cfClaims{}, errCfIssuer
	case claims.Expires == 0 || !now.Before(time.Unix(claims.Expires, 0).Add(cfClockSkew)):
		return cfClaims{}, errCfExpired
	case claims.NotBefore != 0 && now.Add(cfClockSkew).Before(time.Unix(claims.NotBefore, 0)):
		return cfClaims{}, errCfEarly
	case strings.TrimSpace(claims.Email) == "":
		return cfClaims{}, errCfEmail
	}
	claims.Email = strings.TrimSpace(claims.Email)
	return claims, nil
}

// decodeJwtPart reads the header or the claims of a token.
func decodeJwtPart(part string, value any) error {
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
