package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a compact random identifier for request correlation.
func New() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Extremely rare fallback path.
		return "000000000000000000000000"
	}
	return hex.EncodeToString(b)
}
