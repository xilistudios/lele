import type { ClientEvent } from '../../lib/types'

export type ClientCommand =
  | { event: 'subscribe'; data: { session_key: string } }
  | { event: 'unsubscribe'; data: { session_key?: string } }
  | { event: 'approve'; data: { request_id: string; approved: boolean } }
  | { event: 'cancel'; data: Record<string, never> }
  | { event: 'ping'; data: Record<string, never> }
  | { event: 'typing'; data: Record<string, never> }
  | { event: string; data: unknown }

export type { ClientEvent }
