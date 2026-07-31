/**
 * Group chat (Mixture-of-Agents) event handlers.
 *
 * Handles the lifecycle of collaborative group sessions:
 * status changes, incoming turns, tool calls within turns,
 * and final synthesis on group completion.
 */
import type { GroupInfo, GroupToolCall, GroupTurn } from '../../lib/types'
import { getSessionKey, isSessionMismatch } from './helpers'
import type { MessageEventContext } from './types'

export function handleGroupStatus(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'group.status')) return

  const groupId = data.group_id as string
  const status = data.status as GroupInfo['status']
  const participants = (data.participants as string) ?? ''

  if (eventSessionKey) {
    if (status === 'started') {
      ctx.addProcessingSession(eventSessionKey)
    } else if (status === 'done' || status === 'error' || status === 'stopped') {
      ctx.removeProcessingSession(eventSessionKey)
    }
  }

  ctx.upsertGroup(groupId, (existing) => ({
    groupID: groupId,
    status,
    strategy: existing?.strategy ?? '',
    participants,
    layers: existing?.layers ?? 0,
    totalTokens: existing?.totalTokens ?? 0,
    createdAt: existing?.createdAt ?? new Date().toISOString(),
    turns: existing?.turns ?? [],
    synthesis: existing?.synthesis,
  }))
}

export function handleGroupTurn(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'group.turn')) return

  const groupId = data.group_id as string
  const incomingTurn: GroupTurn = {
    groupID: groupId,
    speaker: data.speaker as string,
    label: data.label as string,
    role: data.role as GroupTurn['role'],
    layer: data.layer as number,
    turnIndex: data.turn_index as number,
    content: data.content as string,
  }

  ctx.upsertGroup(groupId, (existing) => {
    const turns = existing?.turns ? [...existing.turns] : []
    // Deduplicate by turn_index — replace if same index exists, preserving existing toolCalls
    const existingIdx = turns.findIndex((t) => t.turnIndex === incomingTurn.turnIndex)
    if (existingIdx >= 0) {
      turns[existingIdx] = {
        ...incomingTurn,
        toolCalls: turns[existingIdx].toolCalls ?? incomingTurn.toolCalls,
      }
    } else {
      turns.push(incomingTurn)
    }
    return {
      groupID: groupId,
      status: existing?.status ?? 'started',
      strategy: existing?.strategy ?? '',
      participants: existing?.participants ?? '',
      layers: Math.max(existing?.layers ?? 0, incomingTurn.layer + 1),
      totalTokens: existing?.totalTokens ?? 0,
      createdAt: existing?.createdAt ?? new Date().toISOString(),
      turns,
      synthesis: existing?.synthesis,
    }
  })
}

export function handleGroupTool(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'group.tool')) return

  const groupId = data.group_id as string
  const turnIndex = data.turn_index as number
  const toolCallId = data.tool_call_id as string
  const toolName = data.tool as string
  const status = data.status as GroupToolCall['status']
  const args = data.arguments as string | undefined
  const result = data.result as string | undefined

  ctx.upsertGroup(groupId, (existing) => {
    const turns = existing?.turns ? [...existing.turns] : []
    let targetIdx = turns.findIndex((t) => t.turnIndex === turnIndex)

    // If turn not found, push a placeholder
    if (targetIdx < 0) {
      turns.push({
        groupID: groupId,
        speaker: data.speaker as string,
        label: (data.label as string) || (data.speaker as string),
        role: 'proposer',
        layer: data.layer as number,
        turnIndex,
        content: '',
        toolCalls: [],
      })
      targetIdx = turns.length - 1
    }

    const turn = turns[targetIdx]
    const toolCalls = turn.toolCalls ? [...turn.toolCalls] : []
    const existingTcIdx = toolCalls.findIndex((tc) => tc.tool_call_id === toolCallId)

    const updatedTc: GroupToolCall = {
      tool_call_id: toolCallId,
      tool: toolName,
      status,
      // Preserve prior arguments if incoming event has none
      arguments: args ?? (existingTcIdx >= 0 ? toolCalls[existingTcIdx].arguments : undefined),
      result,
    }

    if (existingTcIdx >= 0) {
      toolCalls[existingTcIdx] = updatedTc
    } else {
      toolCalls.push(updatedTc)
    }

    turns[targetIdx] = { ...turn, toolCalls }

    return {
      groupID: groupId,
      status: existing?.status ?? 'started',
      strategy: existing?.strategy ?? '',
      participants: existing?.participants ?? '',
      layers: existing?.layers ?? 0,
      totalTokens: existing?.totalTokens ?? 0,
      createdAt: existing?.createdAt ?? new Date().toISOString(),
      turns,
      synthesis: existing?.synthesis,
    }
  })
}

export function handleGroupComplete(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'group.complete')) return

  if (eventSessionKey) {
    ctx.removeProcessingSession(eventSessionKey)
  }

  const groupId = data.group_id as string
  const content = data.content as string

  ctx.upsertGroup(groupId, (existing) => ({
    groupID: groupId,
    status: 'done',
    strategy: (data.strategy as string) ?? existing?.strategy ?? '',
    participants: existing?.participants ?? '',
    layers: (data.layers as number) ?? existing?.layers ?? 0,
    totalTokens: (data.total_tokens as number) ?? existing?.totalTokens ?? 0,
    createdAt: existing?.createdAt ?? new Date().toISOString(),
    turns: existing?.turns ?? [],
    synthesis: content,
  }))
}
