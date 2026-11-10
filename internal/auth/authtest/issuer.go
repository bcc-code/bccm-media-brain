// Package authtest is a stand-in for the auth proxy in tests: it signs JWTs and publishes
// the matching key set over HTTP.
package authtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const Audience = "mediabrain-test"

type Issuer struct {
	URL     string // the "iss" claim
	JWKSURL string

	srv    *httptest.Server
	signer jose.Signer
}

func NewIssuer() *Issuer {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return newIssuer(key, "test-key")
}

func newIssuer(key *ecdsa.PrivateKey, kid string) *Issuer {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: jose.JSONWebKey{Key: key, KeyID: kid}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		panic(err)
	}
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: kid, Algorithm: "ES256", Use: "sig"}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(set)
	}))
	return &Issuer{URL: srv.URL, JWKSURL: srv.URL + "/jwks", srv: srv, signer: signer}
}

func (i *Issuer) Close() { i.srv.Close() }

// Sign signs exactly the given claims.
func (i *Issuer) Sign(claims map[string]any) string {
	token, err := jwt.Signed(i.signer).Claims(claims).Serialize()
	if err != nil {
		panic(err)
	}
	return token
}

// Token returns a valid one-hour token for the subject. Every call gives a different token.
func (i *Issuer) Token(subject string, extra map[string]any) string {
	jti := make([]byte, 8)
	_, _ = rand.Read(jti)
	claims := map[string]any{
		"iss": i.URL, "aud": Audience, "sub": subject, "jti": hex.EncodeToString(jti),
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	return i.Sign(claims)
}
