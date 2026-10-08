package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleAsyncResult processes the small result/event messages: compaction
// feedback, skill scan/install/toggle/delete results, onboarding verify and
// the stream-render throttle tick. Cases that do not return early keep the
// original fall-through into update()'s post-switch block.
func (m *Model) handleAsyncResult(msg tea.Msg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case compactResultMsg:
		if msg.sessionKey == m.currentKey {
			m.lastDuration = time.Since(m.startTime)
			m.processing = false
			m.currentToolAction = ""
			// msg.result is backend-produced text rendered raw into the
			// overlay: sanitize so control/bidi chars cannot corrupt frame.
			m.compactFeedback = sanitizeDisplayText(msg.result)
			m.forceGotoBottom = true
			// Minimal invalidation (T14). Compaction rewrites the session's
			// EXCLUSION state, not its content: it flags the old messages as
			// ExcludeFromContext and evicts them from the resident slice
			// (SessionManager.CompactSession → ExcludeOldMessagesFromContext →
			// EvictExcludedMessages; the folded summary lives in the session
			// metadata, so it is not even injected as a message here). Every
			// surviving message therefore keeps the exact content its
			// messageFingerprint was built from, and its cached glamour output
			// is still what a re-render would produce — which is why the
			// per-message cache is NOT dropped here anymore: wiping it made
			// every compaction re-render the whole ~200-message window
			// through glamour, for messages nothing had touched.
			//
			// What compaction DOES move is already carried by the cache keys,
			// so no invalidation is needed for any of it:
			//   - the evicted prefix growing changes archivedTotal, and that is
			//     part of archivedCacheKey() — folded into both the
			//     rendered-base condition (renderedBaseArchiveKey) and the
			//     viewport fingerprint — so the base rebuilds and the refreshed
			//     archived rows are painted;
			//   - the resident slice shrinking changes its length and its
			//     user+assistant count, which the base tracks as
			//     renderedBaseHistoryLen / renderedBaseMsgCount;
			//   - the archived-prefix refresh itself is Update-path work:
			//     reloadSessions (the next statement) loads it through
			//     refreshArchivedHistory before the skip guard, never from
			//     View().
			// The one render the fingerprint could not vouch for — the last
			// message's tool-call rows, suppressed while a tool executes, which
			// are also the state /compact runs in (it sets processing +
			// currentToolAction before the backend call) — used to need a
			// manual cache drop here. It does not anymore: a suppressed render
			// is never written to the per-message cache in the first place
			// (buildRenderedHistoryLines, isExecutingMessage), so there is
			// nothing transient to invalidate and the rebuild below re-renders
			// the message in the state that is now on screen.
			m.reloadSessions()
		}

	case skillsScanResultMsg:
		return m, m.handleSkillScanResult(msg)

	case skillsInstallResultMsg:
		return m, m.handleSkillInstallResult(msg)

	case skillToggleResultMsg:
		return m, m.handleSkillToggleResult(msg)

	case skillDeleteResultMsg:
		return m, m.handleSkillDeleteResult(msg)

	case mcpToggleResultMsg:
		return m, m.handleMCPToggleResult(msg)

	case obVerifyResultMsg:
		m.obVerifying = false
		if !msg.success {
			m.obVerifyFailed = true
		}
		m.onboardingStep = obDone
		m.obFinalizeSetup()
		return m, nil

	case streamThrottleMsg:
		m.streamThrottleActive = false
		if m.streamPendingUpdate {
			m.updateViewport()
			m.streamPendingUpdate = false
			m.streamThrottleActive = true
			cmds = append(cmds, tea.Tick(m.streamThrottleInterval, func(t time.Time) tea.Msg {
				return streamThrottleMsg{}
			}))
		}

	case langCatalogMsg:
		return m, m.handleLangCatalogMsg(msg)

	case langInstallResultMsg:
		return m, m.handleLangInstallResult(msg)
	}

	return m.finishUpdate(msg, cmds)
}
