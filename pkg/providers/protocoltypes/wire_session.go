// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package protocoltypes

import "strings"

// OptWireSessionID carries the conversation-scoped id a transport may need to
// stamp on the wire (value: string).
//
// Transport-neutral on purpose: the transport knows the HEADER NAME (static per
// provider instance), the caller knows the VALUE (conversation-scoped, so it
// must travel per call — providers are constructed per call, see
// pkg/providers/factory.go createProviderFromSelection).
//
// Deliberately NOT "session_id": that key is already read by the Gemini Code
// Assist transport as a BODY field (geminiSessionIDFromOptions), so reusing it
// would silently change another provider's wire format.
const OptWireSessionID = "wire_session_id"

// WireSessionFromOptions returns the caller-supplied wire session id, or "".
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
