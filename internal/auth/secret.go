package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
)

// secretBytes is the size of every secret garret issues: 256 bits.
const secretBytes = 32

// NewSecret reads 256 bits from rand and returns them as 43 characters of
// unpadded base64url. garret uses it for invitation, session, CSRF, access
// and setup tokens. Production code passes crypto/rand.Reader.
func NewSecret(rand io.Reader) (string, error) {
	b := make([]byte, secretBytes)
	if _, err := io.ReadFull(rand, b); err != nil {
		return "", fmt.Errorf("auth: read secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Digest returns the SHA-256 of secret. Only digests of secrets are stored.
func Digest(secret string) [sha256.Size]byte {
	return sha256.Sum256([]byte(secret))
}

// DigestEqual reports whether a and b are equal, in time that depends only
// on their lengths.
func DigestEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
