package group

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func NewGroupID(label string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("group:%s-%x", label, time.Now().UnixNano())
	}
	return fmt.Sprintf("group:%s-%s", label, hex.EncodeToString(b[:]))
}
