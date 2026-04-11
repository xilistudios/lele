import type { ClientEvent } from '../../lib/types'

export type ClientCommand =
  | { event: 'subscribe'; data: { session_key: string; agent_id?: string } }
  | { event: 'unsubscribe'; data: { session_key?: string } }
  | { event: 'approve'; data: { request_id: string; approved: boolean } }
  | { event: 'cancel'; data: Record<string, never> }
  | { event: 'ping'; data: Record<string, never> }
  | { event: 'typing'; data: Record<string, never> }

export type { ClientEvent }

export function serializeCommand(command: ClientCommand): string {
  return JSON.stringify(command)
}

export function parseEvent(raw: string): ClientEvent {
  return JSON.parse(raw) as ClientEvent
}
