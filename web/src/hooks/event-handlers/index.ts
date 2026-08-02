/**
 * Event-handlers barrel & dispatcher.
 *
 * Re-exports every domain handler, builds the `HANDLERS` routing table,
 * and provides the single `dispatchMessageEvent` entry-point that the
 * rest of the application calls.
 */

// ── Re-exports ──────────────────────────────────────────────────────────────

export type { ClientEvent, MessageEventContext } from './types'
export {
  snapshotToGroupInfo,
  getSessionKey,
  isSessionMismatch,
  findToolMessageIndex,
} from './helpers'

export {
  handleWelcome,
  handleMessageStream,
  handleMessageThinking,
  handleMessageAck,
  handleMessageComplete,
  handleHistoryUpdated,
  handleMessagesCatchup,
  handleAttachments,
  handleMessageError,
  handleStreamError,
  handleTypingIndicator,
} from './streaming'

export {
  handleToolExecuting,
  handleToolResult,
  handleSubagentResult,
} from './tools'

export {
  handleApprovalRequest,
  handleApproveResult,
  handleCancelAck,
  handleSubscribeAck,
} from './approvals'

export {
  handleGroupStatus,
  handleGroupTurn,
  handleGroupTool,
  handleGroupComplete,
} from './groups'

// ── Imports for dispatcher ──────────────────────────────────────────────────

import {
  handleApprovalRequest,
  handleApproveResult,
  handleCancelAck,
  handleSubscribeAck,
} from './approvals'
import { handleGroupComplete, handleGroupStatus, handleGroupTool, handleGroupTurn } from './groups'
import {
  handleAttachments,
  handleHistoryUpdated,
  handleMessageAck,
  handleMessageComplete,
  handleMessageError,
  handleMessageStream,
  handleMessageThinking,
  handleMessagesCatchup,
  handleStreamError,
  handleTypingIndicator,
  handleWelcome,
} from './streaming'
import { handleSubagentResult, handleToolExecuting, handleToolResult } from './tools'
import type { ClientEvent, MessageEventContext } from './types'

// ── Dispatcher ──────────────────────────────────────────────────────────────

const HANDLERS: Record<string, (ctx: MessageEventContext, event: ClientEvent) => void> = {
  welcome: (ctx, e) => handleWelcome(ctx, e.data as Record<string, unknown>),
  reconnected: (ctx, e) => handleWelcome(ctx, e.data as Record<string, unknown>),
  'message.stream': (ctx, e) => handleMessageStream(ctx, e.data as Record<string, unknown>),
  'message.thinking': (ctx, e) => handleMessageThinking(ctx, e.data as Record<string, unknown>),
  'message.ack': (ctx, e) => handleMessageAck(ctx, e.data as Record<string, unknown>),
  'message.complete': (ctx, e) => handleMessageComplete(ctx, e.data as Record<string, unknown>),
  'history.updated': (ctx, e) => handleHistoryUpdated(ctx, e.data as Record<string, unknown>),
  'messages.catchup': (ctx, e) => handleMessagesCatchup(ctx, e.data as Record<string, unknown>),
  attachments: (ctx, e) => handleAttachments(ctx, e),
  'tool.executing': (ctx, e) => handleToolExecuting(ctx, e.data as Record<string, unknown>),
  'tool.result': (ctx, e) => handleToolResult(ctx, e.data as Record<string, unknown>),
  'subagent.result': (ctx, e) => handleSubagentResult(ctx, e.data as Record<string, unknown>),
  'approval.request': (ctx, e) => handleApprovalRequest(ctx, e),
  'approve.result': (ctx, e) => handleApproveResult(ctx, e),
  'cancel.ack': (ctx, e) => handleCancelAck(ctx, e.data as Record<string, unknown>),
  'subscribe.ack': (ctx, e) => handleSubscribeAck(ctx, e.data as Record<string, unknown>),
  'message.error': (ctx, e) => handleMessageError(ctx, e.data as Record<string, unknown>),
  'stream.error': (ctx, e) => handleStreamError(ctx, e.data as Record<string, unknown>),
  'group.status': (ctx, e) => handleGroupStatus(ctx, e.data as Record<string, unknown>),
  'group.turn': (ctx, e) => handleGroupTurn(ctx, e.data as Record<string, unknown>),
  'group.tool': (ctx, e) => handleGroupTool(ctx, e.data as Record<string, unknown>),
  'group.complete': (ctx, e) => handleGroupComplete(ctx, e.data as Record<string, unknown>),
  'typing.indicator': (ctx, e) => handleTypingIndicator(ctx, e.data as Record<string, unknown>),
}

/**
 * Dispatches a WebSocket client event to the appropriate handler.
 * Each event type maps to a focused, single-responsibility function.
 */
export function dispatchMessageEvent(ctx: MessageEventContext, event: ClientEvent): void {
  const handler = HANDLERS[event.event]
  handler?.(ctx, event)
}
