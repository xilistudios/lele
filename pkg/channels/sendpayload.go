package channels

// The rule "what counts as a sendable message" lives here, once, because it is
// asked in two places by each of the three send endpoints (WebSocket,
// /api/v1/chat/send, /api/v1/chat/send/stream):
//
//   - BEFORE attachments are processed, to reject a truly empty request with
//     the `content_missing` code;
//   - AFTER attachments are processed, to reject a request whose every
//     attachment was dropped (outside the upload dir, missing, too large)
//     with the `attachments_rejected` code — otherwise relaxing the first
//     guard would turn "attachment rejected" into a silent empty turn.
//
// Keeping a single predicate is what keeps the three endpoints in agreement;
// three hand-copied conditions drift (that is exactly how the frontend and the
// backend ended up disagreeing about attachments-only sends).
//
// Content is compared literally (== ""), never through strings.TrimSpace: the
// client trims before sending, and a whitespace-only message with text today is
// forwarded and rendered verbatim — normalizing here would silently change that.
func hasSendablePayload[T any](content string, attachments []T) bool {
	return content != "" || len(attachments) > 0
}
