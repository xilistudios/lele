// Package group implements multi-agent collaboration ("Mixture of Agents").
// id.go provides collision-resistant group ID generation.
package group

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NewGroupID returns a collision-resistant group ID with the form
// "group:<label>-<16 hex chars>". The crypto/rand suffix makes parallel
// or rapid group starts safe (no "already exists" collisions).
func NewGroupID(label string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail; fall back to a timestamp-based suffix.
		return fmt.Sprintf("group:%s-%x", label, time.Now().UnixNano())
	}
	return fmt.Sprintf("group:%s-%s", label, hex.EncodeToString(b[:]))
}
