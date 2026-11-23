import type { ChatMessage } from '../../lib/types'
import { restoreInProgressAssistant, stopAllStreaming } from '../streamingOps'
import { finalizeStreamingAssistantsForSession } from '../streamingOpsLocal'
import { restoreInProgressTool } from './tools'
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

  if (data.scope !== 'turn') {
    ctx.markActiveGroupsStopped()
  }
}

export function handleSubscribeAck(ctx: MessageEventContext, data: Record<string, unknown>) {
  const ackSessionKey = (data.session_key as string) ?? ''
  const ackProcessing = data.processing === true
  if (ackSessionKey) {
    ctx.syncProcessingSession(ackSessionKey, ackProcessing)
  }

  const inProgress = data.in_progress_messages as
    | Array<{ role: string; content?: string; reasoning_content?: string }>
    | undefined

  if (ackSessionKey && !ackProcessing) {
    ctx.setStreamingMessages((current) =>
      finalizeStreamingAssistantsForSession(current, ackSessionKey),
    )
  }

  if (ackProcessing && inProgress && inProgress.length > 0 && ackSessionKey) {
    ctx.setStreamingMessages((current) => {
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
            true,
          )
        },
        [...current],
      )
    })
  }

  if (ackProcessing && ackSessionKey) {
    restoreInProgressTool(ctx, ackSessionKey, data.in_progress_tool as Record<string, unknown>)
  }
}
