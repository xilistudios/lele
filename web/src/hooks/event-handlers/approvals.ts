/**
 * Approval / cancellation event handlers.
 *
 * Handles tool-approval requests from the server, approval results,
 * cancel acknowledgements, and subscribe acknowledgements that keep
 * the client processing-session state in sync.
 */
import type { ChatMessage } from '../../lib/types'
import { restoreInProgressAssistant, stopAllStreaming } from '../streamingOps'
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
  const ackSessionKey = (data.session_key as string) ?? ''
  const ackProcessing = data.processing === true
  if (ackSessionKey) {
    ctx.syncProcessingSession(ackSessionKey, ackProcessing)
  }

  // Restore in-progress streaming content when switching back to a chat
  // that is still processing. Without this, the accumulated response is
  // lost until the stream completes (message.complete).
  const inProgress = data.in_progress_messages as
    | Array<{ role: string; content?: string; reasoning_content?: string }>
    | undefined

  if (inProgress && inProgress.length > 0 && ackSessionKey) {
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
