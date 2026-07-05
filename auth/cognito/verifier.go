package cognito

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"oss.nandlabs.io/golly/auth"
)

// Cognito token_use claim values. Access tokens carry a client_id claim
// but no aud; ID tokens carry aud but no client_id.
const (
	TokenUseID     = "id"
	TokenUseAccess = "access"
)

// DefaultKeyTTL is the fallback cache lifetime for JWKS entries when
// WithKeyTTL is not supplied. Cognito rotates signing keys infrequently
// so an hour is a conservative default.
const DefaultKeyTTL = time.Hour

// DefaultLeeway is the clock-skew allowance used when validating exp and
// iat during VerifyToken.
const DefaultLeeway = 60 * time.Second

// IssuerPrefix is prepended to "<region>.amazonaws.com/<userPoolID>" to
// form the expected iss claim on Cognito-minted tokens.
const IssuerPrefix = "https://cognito-idp."

// jwksPath is appended to the issuer URL to obtain the JWKS endpoint.
const jwksPath = "/.well-known/jwks.json"

// Verifier is a Cognito User Pool JWT verifier. It caches JWKS entries
// per-kid and refreshes them on demand when a token references an
// unknown kid. Verifier is safe for concurrent use.
type Verifier struct {
	region      string
	userPoolID  string
	issuer      string
	jwksURL     string
	appClientID string
	tokenUses   map[string]struct{}
	ttl         time.Duration
	leeway      time.Duration
	httpc       *http.Client
	now         func() time.Time

	mu   sync.RWMutex
	keys map[string]*keyEntry
}

type keyEntry struct {
	pub     *rsa.PublicKey
	expires time.Time
}

// Option customizes a Verifier at construction time.
type Option func(*Verifier)

// WithJWKSURL overrides the JWKS endpoint. Primarily used by tests.
func WithJWKSURL(url string) Option { return func(v *Verifier) { v.jwksURL = url } }

// WithHTTPClient overrides the HTTP client used to fetch JWKS.
func WithHTTPClient(c *http.Client) Option {
	return func(v *Verifier) {
		if c != nil {
			v.httpc = c
		}
	}
}

// WithKeyTTL sets the cache lifetime for fetched JWKS entries.
func WithKeyTTL(d time.Duration) Option {
	return func(v *Verifier) {
		if d > 0 {
			v.ttl = d
		}
	}
}

// WithLeeway sets the clock-skew allowance used by VerifyToken.
func WithLeeway(d time.Duration) Option {
	return func(v *Verifier) {
		if d >= 0 {
			v.leeway = d
		}
	}
}

// WithNow overrides the time source used by VerifyToken. Primarily used
// by tests to exercise expired/not-yet-valid paths.
func WithNow(fn func() time.Time) Option {
	return func(v *Verifier) {
		if fn != nil {
			v.now = fn
		}
	}
}

// WithAppClientID pins the expected Cognito app client id. When set,
// VerifyToken requires:
//   - id tokens:     aud == clientID
//   - access tokens: client_id (claim) == clientID
//
// When unset (the default) the client-id check is skipped.
func WithAppClientID(clientID string) Option {
	return func(v *Verifier) { v.appClientID = clientID }
}

// WithTokenUse restricts the accepted token_use values. Pass one or
// both of TokenUseID / TokenUseAccess. When omitted, the verifier
// accepts either token flavour, matching Cognito's own guidance that
// most APIs consume access tokens while apps consume id tokens.
func WithTokenUse(uses ...string) Option {
	return func(v *Verifier) {
		if len(uses) == 0 {
			return
		}
		set := make(map[string]struct{}, len(uses))
		for _, u := range uses {
			set[u] = struct{}{}
		}
		v.tokenUses = set
	}
}

// NewVerifier constructs a Cognito JWT verifier for the given region
// and User Pool id. The returned *Verifier implements auth.Verifier and
// can be plugged into auth.VerifyOptions.VerifierFallback; VerifyToken
// is the recommended entry point for typical usage.
func NewVerifier(region, userPoolID string, opts ...Option) (*Verifier, error) {
	if region == "" {
		return nil, errors.New("auth/cognito: region is required")
	}
	if userPoolID == "" {
		return nil, errors.New("auth/cognito: userPoolID is required")
	}
	issuer := IssuerPrefix + region + ".amazonaws.com/" + userPoolID
	v := &Verifier{
		region:     region,
		userPoolID: userPoolID,
		issuer:     issuer,
		jwksURL:    issuer + jwksPath,
		tokenUses: map[string]struct{}{
			TokenUseID:     {},
			TokenUseAccess: {},
		},
		ttl:    DefaultKeyTTL,
		leeway: DefaultLeeway,
		httpc:  http.DefaultClient,
		now:    time.Now,
		keys:   make(map[string]*keyEntry),
	}
	for _, o := range opts {
		o(v)
	}
	return v, nil
}

// Alg reports the algorithm this verifier validates. Cognito always
// signs with RS256.
func (v *Verifier) Alg() string { return auth.AlgRS256 }

// Verify implements auth.Verifier by extracting the kid from the JWS
// header carried in signingInput, resolving the RSA public key (with a
// JWKS refresh on unknown kid), and validating the RS256 signature.
// It does not perform claim validation — for that use VerifyToken.
func (v *Verifier) Verify(signingInput, sig []byte) error {
	dot := indexByte(signingInput, '.')
	if dot < 0 {
		return auth.ErrInvalidToken
	}
	hdrB, err := base64.RawURLEncoding.DecodeString(string(signingInput[:dot]))
	if err != nil {
		return auth.ErrInvalidToken
	}
	var hdr auth.Header
	if err := json.Unmarshal(hdrB, &hdr); err != nil {
		return auth.ErrInvalidToken
	}
	if hdr.Alg != auth.AlgRS256 {
		return auth.ErrAlgNotAllowed
	}
	if hdr.Kid == "" {
		return fmt.Errorf("%w: token missing kid", auth.ErrKeyNotFound)
	}
	pub, err := v.keyForKid(context.Background(), hdr.Kid)
	if err != nil {
		return fmt.Errorf("%w: %v", auth.ErrKeyNotFound, err)
	}
	sum := sha256.Sum256(signingInput)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return auth.ErrInvalidSig
	}
	return nil
}

// Keyset returns an auth.Verifier bound to a specific kid. Suitable for
// wiring into auth.VerifyOptions.Keyset when the caller prefers
// explicit per-kid dispatch over Verifier's own header parse.
func (v *Verifier) Keyset(kid string) (auth.Verifier, error) {
	pub, err := v.keyForKid(context.Background(), kid)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrKeyNotFound, err)
	}
	return auth.NewRSVerifier(auth.AlgRS256, pub)
}

// VerifyToken parses, signature-checks and claim-validates a compact
// Cognito-issued JWT. It enforces:
//   - alg = RS256
//   - iss = https://cognito-idp.<region>.amazonaws.com/<userPoolID>
//   - token_use is one of the configured values (default: id or access)
//   - if WithAppClientID is set:
//     id-tokens: aud contains the client id
//     access-tokens: client_id claim equals the client id
//   - exp is in the future (with configured leeway)
//   - iat is in the past  (with configured leeway)
//   - sub is non-empty
//
// The returned Claims carry the RFC 7519 registered fields; Cognito-
// specific claims (cognito:groups, cognito:username, token_use,
// client_id) are surfaced via Claims.Extra.
func (v *Verifier) VerifyToken(ctx context.Context, token string) (*auth.Claims, error) {
	// Prime the JWKS cache using the caller-supplied context so that
	// the network fetch respects any request-scoped deadline.
	if kid := peekKid(token); kid != "" {
		if _, err := v.keyForKid(ctx, kid); err != nil {
			return nil, fmt.Errorf("%w: %v", auth.ErrKeyNotFound, err)
		}
	}
	claims, err := auth.Verify(token, auth.VerifyOptions{
		Algs:             []string{auth.AlgRS256},
		VerifierFallback: v,
		Issuer:           v.issuer,
		// Audience is checked manually because Cognito access tokens
		// carry client_id instead of aud; auth.VerifyOptions.Audience
		// would reject them.
		Leeway: v.leeway,
		Now:    v.now,
	})
	if err != nil {
		return nil, err
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: sub claim is empty", auth.ErrInvalidToken)
	}
	if claims.IssuedAt != nil {
		if claims.IssuedAt.After(v.now().Add(v.leeway)) {
			return nil, auth.ErrNotYetValid
		}
	}
	tokenUse, _ := claims.Extra["token_use"].(string)
	if tokenUse == "" {
		return nil, fmt.Errorf("%w: token_use claim is missing", auth.ErrInvalidToken)
	}
	if _, ok := v.tokenUses[tokenUse]; !ok {
		return nil, fmt.Errorf("%w: token_use %q not accepted", auth.ErrInvalidToken, tokenUse)
	}
	if v.appClientID != "" {
		switch tokenUse {
		case TokenUseID:
			ok := false
			for _, a := range claims.Audience {
				if a == v.appClientID {
					ok = true
					break
				}
			}
			if !ok {
				return nil, auth.ErrAudMismatch
			}
		case TokenUseAccess:
			cid, _ := claims.Extra["client_id"].(string)
			if cid != v.appClientID {
				return nil, auth.ErrAudMismatch
			}
		}
	}
	return claims, nil
}

// keyForKid returns the cached public key for kid, refreshing the JWKS
// on a cache miss or when the cached entry has expired.
func (v *Verifier) keyForKid(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	now := v.now()
	v.mu.RLock()
	entry, ok := v.keys[kid]
	v.mu.RUnlock()
	if ok && now.Before(entry.expires) {
		return entry.pub, nil
	}
	if err := v.refresh(ctx); err != nil {
		return nil, err
	}
	v.mu.RLock()
	entry, ok = v.keys[kid]
	v.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("kid %q not present in JWKS", kid)
	}
	return entry.pub, nil
}

// jwks is the shape returned by Cognito's .well-known/jwks.json.
type jwks struct {
	Keys []jwk `json:"keys"`
}

// jwk carries the fields we care about from a JSON Web Key. Cognito
// always publishes RSA public keys, so kty is expected to be "RSA".
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// refresh fetches the JWKS and replaces the in-memory cache. It uses a
// write lock across the network call so concurrent misses collapse into
// a single fetch.
func (v *Verifier) refresh(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.httpc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("auth/cognito: JWKS fetch returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var set jwks
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("auth/cognito: decode JWKS: %w", err)
	}
	if len(set.Keys) == 0 {
		return errors.New("auth/cognito: JWKS response contains no keys")
	}
	next := make(map[string]*keyEntry, len(set.Keys))
	exp := v.now().Add(v.ttl)
	for _, k := range set.Keys {
		if k.Kty != "" && k.Kty != "RSA" {
			// Skip non-RSA keys silently — Cognito only publishes RSA
			// but be tolerant of a future JWKS shape.
			continue
		}
		if k.Alg != "" && k.Alg != auth.AlgRS256 {
			continue
		}
		pub, err := parseRSAPublicKeyFromJWK(k)
		if err != nil {
			return fmt.Errorf("auth/cognito: kid %q: %w", k.Kid, err)
		}
		next[k.Kid] = &keyEntry{pub: pub, expires: exp}
	}
	if len(next) == 0 {
		return errors.New("auth/cognito: JWKS contained no usable RS256 keys")
	}
	v.keys = next
	return nil
}

// parseRSAPublicKeyFromJWK reconstructs an *rsa.PublicKey from the
// base64url-encoded modulus (n) and exponent (e) fields of a JWK.
func parseRSAPublicKeyFromJWK(k jwk) (*rsa.PublicKey, error) {
	if k.N == "" || k.E == "" {
		return nil, errors.New("JWK missing n or e")
	}
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}
	// The exponent is a big-endian byte string; left-pad to 4 bytes so
	// binary.BigEndian.Uint32 can decode it directly.
	if len(eb) > 4 {
		return nil, fmt.Errorf("exponent too large (%d bytes)", len(eb))
	}
	padded := make([]byte, 4)
	copy(padded[4-len(eb):], eb)
	e := int(binary.BigEndian.Uint32(padded))
	if e <= 0 {
		return nil, errors.New("non-positive exponent")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nb),
		E: e,
	}, nil
}

// peekKid extracts the kid header from a compact JWT without any
// signature check. It returns "" on any parse error — the subsequent
// Verify pass will surface the specific problem.
func peekKid(token string) string {
	dot := strings.IndexByte(token, '.')
	if dot < 0 {
		return ""
	}
	hb, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return ""
	}
	var h auth.Header
	if err := json.Unmarshal(hb, &h); err != nil {
		return ""
	}
	return h.Kid
}

// indexByte is a byte-slice equivalent of strings.IndexByte; inlined
// here to avoid a string conversion on the hot signature-verify path.
func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
