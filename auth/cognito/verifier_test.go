package cognito

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"oss.nandlabs.io/golly/auth"
)

// testSigner bundles an RSA key + kid to keep test tokens compact.
type testSigner struct {
	kid string
	key *rsa.PrivateKey
}

func newTestSigner(t *testing.T, kid string) *testSigner {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return &testSigner{kid: kid, key: k}
}

// jwk returns the JWK dict entry (kid, kty=RSA, n, e, alg=RS256) that
// mirrors what Cognito publishes at .well-known/jwks.json.
func (s *testSigner) jwk() map[string]string {
	pub := s.key.PublicKey
	// Big-endian encoding of the exponent, trimmed of leading zeros.
	eBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(eBuf, uint32(pub.E))
	i := 0
	for i < len(eBuf)-1 && eBuf[i] == 0 {
		i++
	}
	return map[string]string{
		"kid": s.kid,
		"kty": "RSA",
		"alg": auth.AlgRS256,
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(eBuf[i:]),
	}
}

// mint builds a signed compact JWT using RS256 with the signer's kid
// baked into the header.
func (s *testSigner) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	hdr := map[string]any{"alg": auth.AlgRS256, "typ": "JWT", "kid": s.kid}
	hb, _ := json.Marshal(hdr)
	pb, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// jwksServer serves a Cognito-shaped JWKS response ({"keys":[...]}).
// Each request bumps the atomic hit counter so tests can assert on
// refresh behavior.
type jwksServer struct {
	*httptest.Server
	hits atomic.Int64
	body atomic.Value // string
}

func newJWKSServer(t *testing.T, signers ...*testSigner) *jwksServer {
	t.Helper()
	js := &jwksServer{}
	js.setSigners(signers...)
	js.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(js.body.Load().(string)))
	}))
	t.Cleanup(js.Close)
	return js
}

func (js *jwksServer) setSigners(signers ...*testSigner) {
	keys := make([]map[string]string, 0, len(signers))
	for _, s := range signers {
		keys = append(keys, s.jwk())
	}
	b, _ := json.Marshal(map[string]any{"keys": keys})
	js.body.Store(string(b))
}

// idClaims returns a valid id-token claim set for the given signer and
// user pool identifier. Individual tests mutate the map before minting.
func idClaims(region, userPoolID, clientID, sub string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":       IssuerPrefix + region + ".amazonaws.com/" + userPoolID,
		"aud":       clientID,
		"sub":       sub,
		"token_use": TokenUseID,
		"iat":       now.Add(-time.Minute).Unix(),
		"exp":       now.Add(time.Hour).Unix(),
	}
}

// accessClaims returns a valid access-token claim set. Cognito access
// tokens carry client_id (not aud) and token_use=access.
func accessClaims(region, userPoolID, clientID, sub string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":       IssuerPrefix + region + ".amazonaws.com/" + userPoolID,
		"client_id": clientID,
		"sub":       sub,
		"token_use": TokenUseAccess,
		"iat":       now.Add(-time.Minute).Unix(),
		"exp":       now.Add(time.Hour).Unix(),
	}
}

func newVerifier(t *testing.T, region, userPoolID string, jwks *jwksServer, opts ...Option) *Verifier {
	t.Helper()
	full := append([]Option{WithJWKSURL(jwks.URL)}, opts...)
	v, err := NewVerifier(region, userPoolID, full...)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

const (
	testRegion = "us-east-1"
	testPool   = "us-east-1_ABCdef123"
	testClient = "abcdefghijklmnop"
)

func TestVerify_ValidIDToken_Success(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	jwks := newJWKSServer(t, signer)
	v := newVerifier(t, testRegion, testPool, jwks, WithAppClientID(testClient))

	claims := idClaims(testRegion, testPool, testClient, "user-42")
	claims["cognito:username"] = "alice"
	claims["cognito:groups"] = []string{"admins"}
	claims["email"] = "alice@example.com"
	token := signer.mint(t, claims)

	got, err := v.VerifyToken(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if got.Subject != "user-42" {
		t.Errorf("subject: got %q want %q", got.Subject, "user-42")
	}
	if len(got.Audience) != 1 || got.Audience[0] != testClient {
		t.Errorf("audience: got %v want [%s]", got.Audience, testClient)
	}
	if got.Issuer != IssuerPrefix+testRegion+".amazonaws.com/"+testPool {
		t.Errorf("issuer: got %q", got.Issuer)
	}
	if v, ok := got.Extra["token_use"].(string); !ok || v != TokenUseID {
		t.Errorf("token_use not surfaced: %v", got.Extra["token_use"])
	}
	if v, ok := got.Extra["cognito:username"].(string); !ok || v != "alice" {
		t.Errorf("cognito:username not surfaced: %v", got.Extra["cognito:username"])
	}
	if _, ok := got.Extra["cognito:groups"]; !ok {
		t.Errorf("cognito:groups not surfaced: %v", got.Extra)
	}
}

func TestVerify_ValidAccessToken_Success(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	jwks := newJWKSServer(t, signer)
	v := newVerifier(t, testRegion, testPool, jwks, WithAppClientID(testClient))

	claims := accessClaims(testRegion, testPool, testClient, "user-99")
	claims["scope"] = "aws.cognito.signin.user.admin"
	token := signer.mint(t, claims)

	got, err := v.VerifyToken(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if got.Subject != "user-99" {
		t.Errorf("subject: got %q want %q", got.Subject, "user-99")
	}
	if v, ok := got.Extra["client_id"].(string); !ok || v != testClient {
		t.Errorf("client_id not surfaced: %v", got.Extra["client_id"])
	}
	if v, ok := got.Extra["token_use"].(string); !ok || v != TokenUseAccess {
		t.Errorf("token_use: got %v", got.Extra["token_use"])
	}
}

func TestVerify_BadSignature_Fails(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	jwks := newJWKSServer(t, signer)
	v := newVerifier(t, testRegion, testPool, jwks)

	token := signer.mint(t, idClaims(testRegion, testPool, testClient, "user-42"))
	// Replace the last four base64url chars of the signature so the
	// decode succeeds but the resulting bytes don't verify.
	tampered := token[:len(token)-4] + "AAAA"

	if _, err := v.VerifyToken(context.Background(), tampered); !errors.Is(err, auth.ErrInvalidSig) {
		t.Fatalf("VerifyToken: got %v, want ErrInvalidSig", err)
	}
}

func TestVerify_WrongIssuer_Fails(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	jwks := newJWKSServer(t, signer)
	v := newVerifier(t, testRegion, testPool, jwks)

	claims := idClaims(testRegion, testPool, testClient, "user-42")
	claims["iss"] = IssuerPrefix + testRegion + ".amazonaws.com/us-east-1_OtherPool"
	token := signer.mint(t, claims)

	if _, err := v.VerifyToken(context.Background(), token); !errors.Is(err, auth.ErrIssuerMismatch) {
		t.Fatalf("VerifyToken: got %v, want ErrIssuerMismatch", err)
	}
}

func TestVerify_WrongAudience_Fails(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	jwks := newJWKSServer(t, signer)
	v := newVerifier(t, testRegion, testPool, jwks, WithAppClientID(testClient))

	// ID token whose aud does not match the pinned app client id.
	claims := idClaims(testRegion, testPool, "some-other-client", "user-42")
	token := signer.mint(t, claims)

	if _, err := v.VerifyToken(context.Background(), token); !errors.Is(err, auth.ErrAudMismatch) {
		t.Fatalf("id-token wrong aud: got %v, want ErrAudMismatch", err)
	}

	// Access token whose client_id does not match either.
	claims2 := accessClaims(testRegion, testPool, "some-other-client", "user-42")
	token2 := signer.mint(t, claims2)
	if _, err := v.VerifyToken(context.Background(), token2); !errors.Is(err, auth.ErrAudMismatch) {
		t.Fatalf("access-token wrong client_id: got %v, want ErrAudMismatch", err)
	}
}

func TestVerify_ExpiredToken_Fails(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	jwks := newJWKSServer(t, signer)
	v := newVerifier(t, testRegion, testPool, jwks, WithLeeway(0))

	claims := idClaims(testRegion, testPool, testClient, "user-42")
	claims["iat"] = time.Now().Add(-2 * time.Hour).Unix()
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	token := signer.mint(t, claims)

	if _, err := v.VerifyToken(context.Background(), token); !errors.Is(err, auth.ErrExpired) {
		t.Fatalf("VerifyToken: got %v, want ErrExpired", err)
	}
}

func TestVerify_UnknownKid_RefreshesJWKS(t *testing.T) {
	signerOld := newTestSigner(t, "kid-old")
	signerNew := newTestSigner(t, "kid-new")
	// Start with only the old kid published.
	jwks := newJWKSServer(t, signerOld)
	v := newVerifier(t, testRegion, testPool, jwks)

	// Prime the cache with a valid old-kid token.
	if _, err := v.VerifyToken(context.Background(), signerOld.mint(t, idClaims(testRegion, testPool, testClient, "user-old"))); err != nil {
		t.Fatalf("prime: %v", err)
	}
	primeHits := jwks.hits.Load()
	if primeHits == 0 {
		t.Fatalf("expected at least one JWKS fetch during priming")
	}

	// Rotate the JWKS to publish the new kid alongside the old one.
	jwks.setSigners(signerOld, signerNew)

	claims := idClaims(testRegion, testPool, testClient, "user-new")
	token := signerNew.mint(t, claims)
	got, err := v.VerifyToken(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyToken (rotate): %v", err)
	}
	if got.Subject != "user-new" {
		t.Errorf("subject: got %q", got.Subject)
	}
	if jwks.hits.Load() <= primeHits {
		t.Errorf("expected another JWKS fetch on unknown kid; hits stayed at %d", jwks.hits.Load())
	}
}

func TestVerify_WrongTokenUse_Fails(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	jwks := newJWKSServer(t, signer)
	// Only accept id tokens; access tokens must be rejected.
	v := newVerifier(t, testRegion, testPool, jwks, WithTokenUse(TokenUseID))

	claims := accessClaims(testRegion, testPool, testClient, "user-42")
	token := signer.mint(t, claims)

	_, err := v.VerifyToken(context.Background(), token)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("VerifyToken: got %v, want ErrInvalidToken", err)
	}
}

func TestVerify_EmptySubject_Fails(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	jwks := newJWKSServer(t, signer)
	v := newVerifier(t, testRegion, testPool, jwks)

	claims := idClaims(testRegion, testPool, testClient, "")
	token := signer.mint(t, claims)

	_, err := v.VerifyToken(context.Background(), token)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("VerifyToken: got %v, want ErrInvalidToken", err)
	}
}

func TestNewVerifier_RequiresRegionAndPool(t *testing.T) {
	if _, err := NewVerifier("", "pool"); err == nil {
		t.Fatal("expected error for empty region")
	}
	if _, err := NewVerifier("us-east-1", ""); err == nil {
		t.Fatal("expected error for empty userPoolID")
	}
}

func TestVerifier_Alg(t *testing.T) {
	v, err := NewVerifier(testRegion, testPool)
	if err != nil {
		t.Fatal(err)
	}
	if v.Alg() != auth.AlgRS256 {
		t.Errorf("Alg: got %q", v.Alg())
	}
}

// Compile-time assertion: *Verifier satisfies auth.Verifier.
var _ auth.Verifier = (*Verifier)(nil)
