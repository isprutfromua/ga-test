package tokenhash

import (
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns the lowercase hex-encoded SHA-256 digest of the token.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}