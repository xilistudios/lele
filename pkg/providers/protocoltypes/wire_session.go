package protocoltypes

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const OptWireSessionID = "wire_session_id"

func WireSessionFromOptions(options map[string]interface{}) string {
	if options == nil {
		return ""
	}
	v, ok := options[OptWireSessionID].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

func hashSessionKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

var UnkeyedSessionBucket = hashSessionKey("")

func WireSessionID(sessionKey string) string {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return UnkeyedSessionBucket
	}
	return hashSessionKey(key)
}

func WireSessionForHeaders(options map[string]interface{}) string {
	if id := WireSessionFromOptions(options); id != "" {
		return id
	}
	return UnkeyedSessionBucket
}
