/**
 * Approval / cancellation event handlers.
 *
 * Handles tool-approval requests from the server, approval results,
 * cancel acknowledgements, and subscribe acknowledgements that keep
 * the client processing-session state in sync.
 */
import type { ChatMessage } from '../../lib/types'
import { restoreInProgressAssistant, stopAllStreaming } from '../streamingOps'
import { finalizeStreamingAssistantsForSession } from '../streamingOpsLocal'
import type { ClientEvent, MessageEventContext } from './types'

export function handleApprovalRequest(ctx: MessageEventContext, event: ClientEvent) {
  ctx.setApprovalRequest(event.data)
}

export function handleApproveResult(ctx: MessageEventContext, event: ClientEvent) {
  const resultData = event.data as { request_id: string; approved: boolean; command: string }
  ctx.showApprovalResult(resultData.request_id, resultData.approved, resultData.command)
}

export function handleCancelAck(ctx: MessageEventContext, data: Record<string, unknown>) {
  ctx.setToolStatus(null)
  ctx.processingSessionKeyRef.current = null
  ctx.clearAllQueues()

  // NOTE: cancel.ack always carries the CLIENT's base key — websocket.go emits
  // it from client.SessionKey without ResolveSessionKey (unlike message.ack).
  // If the backend ever resolves aliases here, re-tag with effectiveSessionKey
  // (see streaming.ts) so the clear below targets the key the UI uses.
  const cancelledSessionKey = (data.session_key as string) ?? ctx.currentSessionKeyRef.current
  if (cancelledSessionKey) {
    ctx.removeProcessingSession(cancelledSessionKey)
  }

  ctx.setStreamingMessages(stopAllStreaming)

  // Mark any active groups as stopped — without this, groups with status 'started'
  // would remain 'started' forever and the processing indicator would never clear.
  ctx.markActiveGroupsStopped()
}

export function handleSubscribeAck(ctx: MessageEventContext, data: Record<string, unknown>) {
  // NOTE: subscribe.ack always carries the CLIENT's base key — websocket.go
  // echoes the payload's session_key directly, without ResolveSessionKey
  // (unlike message.ack). If the backend ever resolves aliases here, re-tag
  // with effectiveSessionKey (see streaming.ts) so processing-sync, stale
  // cleanup and restore all target the key the UI state is keyed by.
  const ackSessionKey = (data.session_key as string) ?? ''
  const ackProcessing = data.processing === true
  if (ackSessionKey) {
    ctx.syncProcessingSession(ackSessionKey, ackProcessing)
  }

  // Stale cleanup: the backend says this session is NOT processing, so any
  // assistant still flagged streaming:true for it (restored by a previous
  // welcome, an ack placeholder whose message.complete was lost, ...) is
  // stale and would keep the session-scoped loading indicator lit forever.
  const inProgress = data.in_progress_messages as
    | Array<{ role: string; content?: string; reasoning_content?: string }>
    | undefined

  if (ackSessionKey && !ackProcessing) {
    ctx.setStreamingMessages((current) =>
      finalizeStreamingAssistantsForSession(current, ackSessionKey),
    )
  }

  // Restore in-progress streaming content when switching back to a chat
  // that is still processing. Without this, the accumulated response is
  // lost until the stream completes (message.complete).
  //
  // Only when ackProcessing is true: if the backend already finished
  // (processing:false) but still reports in_progress leftovers, restoring
  // them as streaming would re-create the stuck-loading bug this handler is
  // meant to clear.
  if (ackProcessing && inProgress && inProgress.length > 0 && ackSessionKey) {
    ctx.setStreamingMessages((current) => {
      // Don't create duplicates if we already have streaming content for this session
      const hasExisting = current.some(
        (m) => m.sessionKey === ackSessionKey && m.role === 'assistant' && m.streaming,
      )
      if (hasExisting) return current

      return inProgress.reduce<ChatMessage[]>(
        (acc, msg) => {
          if (msg.role !== 'assistant') return acc
          return restoreInProgressAssistant(
            acc,
            ackSessionKey,
            msg.content ?? '',
            msg.reasoning_content ?? '',
            true, // subscribe.ack: insert after the session's last user message
          )
        },
        [...current],
      )
    })
  }
}
