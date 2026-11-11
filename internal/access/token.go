package access

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"strings"
	"time"
)

const (
	// TokenPrefix marks a service token, so it is told apart from a JWT without parsing either.
	TokenPrefix = "mbs_"

	tokenBytes      = 32
	defaultTokenTTL = 365 * 24 * time.Hour
)

func IsServiceToken(s string) bool { return strings.HasPrefix(s, TokenPrefix) }

func newToken(r io.Reader) (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is a plain SHA-256: a token is 256 random bits, so there is nothing to brute-force
// and a slow password hash would only delay every request.
func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
