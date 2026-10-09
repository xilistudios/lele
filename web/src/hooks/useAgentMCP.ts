import { type QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useAuthContext } from '../contexts/AuthContext'
import type { MCPLayerName, MCPQueryLayer } from '../lib/mcpTypes'
import { ApiError } from '../services/http/errors'

/**
 * MCP management hooks (T5 UI): merged inventory, per-layer raw file,
 * surgical toggle, raw save and validation.
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
