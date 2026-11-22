package providers

import (
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

const OptWireSessionID = protocoltypes.OptWireSessionID

var UnkeyedSessionBucket = protocoltypes.UnkeyedSessionBucket

func WireSessionID(sessionKey string) string { return protocoltypes.WireSessionID(sessionKey) }

func WithWireSessionID(opts map[string]interface{}, sessionKey string) map[string]interface{} {
	id := WireSessionID(sessionKey)
	out := make(map[string]interface{}, len(opts)+1)
	for k, v := range opts {
		out[k] = v
	}
	out[OptWireSessionID] = id
	return out
}
