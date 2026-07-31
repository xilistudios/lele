/**
 * Approval / cancellation event handlers.
 *
 * Handles tool-approval requests from the server, approval results,
 * cancel acknowledgements, and subscribe acknowledgements that keep
 * the client processing-session state in sync.
 */
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

  ctx.setStreamingMessages((current) => current.map((m) => ({ ...m, streaming: false })))

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
}
