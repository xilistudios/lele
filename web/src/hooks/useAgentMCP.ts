import { type QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { MCPLayerName, MCPQueryLayer } from '../lib/mcpTypes'
import { ApiError } from '../services/http/errors'
import { usePageVisible } from './usePageVisible'

/**
 * MCP management hooks (T5 UI): merged inventory, per-layer raw file,
 * surgical toggle, raw save, validation, and the OAuth flow of one server
 * (start → poll → manual complete / cancel / logout).
 *
 * Same shape the Commands/Skills tabs settled on (useAgentSkills /
 * useAgentCommands): reads go through react-query with an exported query key,
 * and every mutation invalidates the affected keys instead of patching cached
 * rows optimistically — an mcp.json write is resolved server-side (winner,
 * shadowing, aliases) in ways the client cannot recompute.
 *
 * Transport note: every request goes through the house `ApiClient`
 * (`api.mcpInventory`, `api.mcpRaw`, …), i.e. the same `request()` every other
 * agent-scoped call uses — the 401 → single-flight refresh → replay machinery
 * in services/http/client.ts applies unchanged. Failures arrive as the house
 * `ApiError` (status + parsed `code`), so `mcpErrorCode` keeps working.
 */

/** Query key of one agent's merged MCP inventory. Exported for tests. */
export const agentMCPInventoryQueryKey = (agentId: string) =>
  ['agentMCPInventory', agentId] as const

/**
 * Query key of ONE layer's raw mcp.json. `layer` null (editor closed) maps to
 * '' so the disabled query still has a stable key. Invalidating a concrete
 * layer key only touches that layer's query — never its siblings.
 */
export const mcpRawFileQueryKey = (agentId: string, layer: MCPQueryLayer | null) =>
  ['agentMCPRawFile', agentId, layer ?? ''] as const

/**
 * Query key of ONE pending/terminal OAuth flow. `flowId` null (no flow
 * started, or the modal is closed) maps to '' so the disabled query still has
 * a stable key.
 */
export const mcpAuthStatusQueryKey = (agentId: string, flowId: string | null) =>
  ['agentMCPAuthStatus', agentId, flowId ?? ''] as const

/**
 * How often a PENDING flow is polled. Only three outcomes exist
 * (`pending`/`done`/`error`) and the backend starts a 5 min deadline, so a
 * 1.5 s cadence is responsive without hammering the gateway.
 */
export const MCP_AUTH_POLL_INTERVAL_MS = 1500

/** Invalidate the inventory of one agent after a mutation. */
export function invalidateAgentMCPInventory(queryClient: QueryClient, agentId: string) {
  queryClient.invalidateQueries({ queryKey: agentMCPInventoryQueryKey(agentId) })
}

/**
 * Machine-readable `code` of a failed MCP request (e.g. `mcp_entry_shadowed`),
 * when the server sent one. The Skills-style call sites only surface
 * `err.message`; conflict handling needs the code too — exposed here as a
 * helper instead of changing shared error code. The ApiClient throws the
 * `ApiError` that `parseApiError` produced, so the code is read off it.
 */
export function mcpErrorCode(err: unknown): string | undefined {
  return err instanceof ApiError ? err.code : undefined
}

/** Toggle input: `layer` is the URL segment (stored layer or `auto`). */
export type MCPDriveToggleVars = {
  layer: MCPQueryLayer
  name: string
  enabled: boolean
  /** Overwrite a losing copy (server answers 409 `mcp_entry_shadowed` without it). */
  force?: boolean
}

/** Validate input: the literal bytes of ONE stored layer's file. */
export type MCPValidateVars = {
  layer: MCPLayerName
  content: string
}

/** Merged inventory (one row per NAME) of one agent's mcp.json stack. */
export function useAgentMCPInventory(agentId: string) {
  const { api } = useAuthContext()
  return useQuery({
    queryKey: agentMCPInventoryQueryKey(agentId),
    queryFn: () => api.mcpInventory(agentId),
    staleTime: 10_000,
    retry: 1,
  })
}

/**
 * Literal bytes of ONE layer's mcp.json (raw editor). Pass `layer = null`
 * while no layer is selected: the query stays disabled and no request leaves
 * the page. The server refuses `auto` here (invalid_layer).
 */
export function useMCPRawFile(agentId: string, layer: MCPLayerName | null) {
  const { api } = useAuthContext()
  return useQuery({
    queryKey: mcpRawFileQueryKey(agentId, layer),
    // `enabled` gates the request; queryFn never runs with a null layer.
    queryFn: () => api.mcpRaw(agentId, layer as MCPLayerName),
    enabled: layer !== null,
    staleTime: 10_000,
    retry: 1,
  })
}

/**
 * Enable/disable one server in ONE layer (PUT toggle). On success the
 * inventory and the RAW file of the layer actually written are invalidated —
 * and only that layer's raw key, so sibling layers' editors keep their
 * cached bytes.
 */
export function useMCPDriveToggle(agentId: string) {
  const queryClient = useQueryClient()
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: ({ layer, name, enabled, force }: MCPDriveToggleVars) =>
      api.mcpToggle(agentId, layer, name, enabled, force),
    onSuccess: (data, variables) => {
      invalidateAgentMCPInventory(queryClient, agentId)
      // `data.layer` echoes the layer the server wrote (auto already
      // resolved); fall back to the requested one if it ever echoes auto.
      const written = (data.layer === 'auto' ? variables.layer : data.layer) as MCPQueryLayer
      queryClient.invalidateQueries({ queryKey: mcpRawFileQueryKey(agentId, written) })
    },
  })
}

/**
 * Write the full literal bytes of ONE layer's mcp.json (raw editor save).
 * Invalidates the inventory plus that layer's raw key — the write may change
 * winners/shadowing the client cannot recompute locally.
 */
export function useSaveMCPRaw(agentId: string, layer: MCPLayerName) {
  const queryClient = useQueryClient()
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: (content: string) => api.mcpPutRaw(agentId, layer, { content }),
    onSuccess: () => {
      invalidateAgentMCPInventory(queryClient, agentId)
      queryClient.invalidateQueries({ queryKey: mcpRawFileQueryKey(agentId, layer) })
    },
  })
}

/**
 * Validate one layer's literal bytes WITHOUT writing (POST validate).
 * Deliberately invalidates NOTHING: a validation is a pure read of what the
 * caller typed — nothing on disk changed.
 */
export function useMCPValidate(agentId: string) {
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: ({ layer, content }: MCPValidateVars) =>
      api.mcpValidate(agentId, { layer, content }),
  })
}

/**
 * Start an OAuth flow for ONE server (POST, no body). Returns the flow id plus
 * the `auth_url` the user must open.
 *
 * Invalidates NOTHING on success: starting a flow only registers a loopback
 * listener server-side — the inventory row (and its `auth` report) is
 * unchanged until a poll observes `done`, which is where the inventory gets
 * invalidated.
 */
export function useMCPAuthStart(agentId: string) {
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: (name: string) => api.mcpAuthStart(agentId, name),
  })
}

/**
 * Status of ONE flow, polled until it reaches a terminal state.
 *
 * Poll-until-terminal: `refetchInterval` returns the cadence while the flow is
 * `pending` and `false` on `done`/`error` — React Query then CLEARS the timer
 * instead of leaving it armed. While the tab is hidden the callback returns
 * `false` too (same `pageVisible` gate as `useChatHistory`), so a backgrounded
 * modal runs no timer at all.
 *
 * An absent `data` is deliberately NOT treated as pending: the first fetch of
 * an enabled query is unconditional, and a poll that keeps failing (expired
 * flow swept by the backend, gateway down) must not busy-loop every 1.5 s.
 *
 * The `done` observation invalidates the MCP inventory EXACTLY ONCE per flow
 * id. Rationale: the backend reloads the MCP manager when the flow completes
 * (the credential only becomes visible to it at fingerprint time), so the
 * cached inventory still reports the pre-auth state and the list has to be
 * re-read to show it. The `useRef` latch is keyed by `flowId` because the
 * effect can legitimately re-run for the same flow (a re-observation of
 * `done`, e.g. React re-invoking effects) and each invalidation refetches the
 * whole inventory; a STRICT-mode double effect must not cost a second read.
 * Keying on the flow id also keeps the door open for a later flow: a new id
 * invalidates again, which is what we want.
 */
export function useMCPAuthFlow(agentId: string, flowId: string | null) {
  const { api } = useAuthContext()
  const queryClient = useQueryClient()
  const pageVisible = usePageVisible()
  // Last flow id whose `done` already invalidated the inventory.
  const invalidatedFlowRef = useRef<string | null>(null)

  const query = useQuery({
    queryKey: mcpAuthStatusQueryKey(agentId, flowId),
    // `enabled` gates the request; queryFn never runs with a null flow id.
    queryFn: () => api.mcpAuthStatus(agentId, flowId as string),
    enabled: flowId !== null,
    // No retry: a 404 (flow swept/expired) or 501 (no OAuth support) is the
    // answer, not a transient glitch.
    retry: false,
    refetchInterval: (query) => {
      if (!pageVisible) return false
      return query.state.data?.state === 'pending' ? MCP_AUTH_POLL_INTERVAL_MS : false
    },
  })

  useEffect(() => {
    if (flowId === null || query.data?.state !== 'done') return
    if (invalidatedFlowRef.current === flowId) return
    invalidatedFlowRef.current = flowId
    invalidateAgentMCPInventory(queryClient, agentId)
  }, [query.data?.state, flowId, agentId, queryClient])

  return query
}

/**
 * Manual completion of ONE flow (POST `{code, state?}`): the user pasted the
 * redirect URL instead of letting the gateway's loopback callback receive it.
 * Invalidates the flow status key so the hook re-reads the authoritative state
 * (the server owns the flow and may already have resolved it).
 */
export function useMCPAuthComplete(agentId: string, flowId: string) {
  const queryClient = useQueryClient()
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: (body: { code: string; state?: string }) =>
      api.mcpAuthComplete(agentId, flowId, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: mcpAuthStatusQueryKey(agentId, flowId) })
    },
  })
}

/**
 * Cancel ONE flow (DELETE → 204). Also invalidates the flow status key: the
 * authoritative state changes to a terminal one server-side (or the flow is
 * gone), and the poller must be told to stop from fresh data rather than from
 * a local guess.
 */
export function useMCPAuthCancel(agentId: string, flowId: string) {
  const queryClient = useQueryClient()
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: () => api.mcpAuthCancel(agentId, flowId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: mcpAuthStatusQueryKey(agentId, flowId) })
    },
  })
}

/**
 * Forget the stored credential of ONE server (DELETE → 204). Invalidates the
 * inventory: the row's `auth` report flips back to "required", and the server
 * reloads the MCP manager for the row to actually lose its session.
 */
export function useMCPLogout(agentId: string) {
  const queryClient = useQueryClient()
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: (name: string) => api.mcpLogout(agentId, name),
    onSuccess: () => {
      invalidateAgentMCPInventory(queryClient, agentId)
    },
  })
}
