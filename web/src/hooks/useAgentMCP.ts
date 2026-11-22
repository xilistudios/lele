import { type QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { MCPLayerName, MCPQueryLayer } from '../lib/mcpTypes'
import { ApiError } from '../services/http/errors'
import { usePageVisible } from './usePageVisible'

export const agentMCPInventoryQueryKey = (agentId: string) =>
  ['agentMCPInventory', agentId] as const

export const mcpRawFileQueryKey = (agentId: string, layer: MCPQueryLayer | null) =>
  ['agentMCPRawFile', agentId, layer ?? ''] as const

export const mcpAuthStatusQueryKey = (agentId: string, flowId: string | null) =>
  ['agentMCPAuthStatus', agentId, flowId ?? ''] as const

export const MCP_AUTH_POLL_INTERVAL_MS = 1500

export function invalidateAgentMCPInventory(queryClient: QueryClient, agentId: string) {
  queryClient.invalidateQueries({ queryKey: agentMCPInventoryQueryKey(agentId) })
}

export function mcpErrorCode(err: unknown): string | undefined {
  return err instanceof ApiError ? err.code : undefined
}

export type MCPDriveToggleVars = {
  layer: MCPQueryLayer
  name: string
  enabled: boolean
  force?: boolean
}

export type MCPValidateVars = {
  layer: MCPLayerName
  content: string
}

export function useAgentMCPInventory(agentId: string) {
  const { api } = useAuthContext()
  return useQuery({
    queryKey: agentMCPInventoryQueryKey(agentId),
    queryFn: () => api.mcpInventory(agentId),
    staleTime: 10_000,
    retry: 1,
  })
}

export function useMCPRawFile(agentId: string, layer: MCPLayerName | null) {
  const { api } = useAuthContext()
  return useQuery({
    queryKey: mcpRawFileQueryKey(agentId, layer),
    queryFn: () => api.mcpRaw(agentId, layer as MCPLayerName),
    enabled: layer !== null,
    staleTime: 10_000,
    retry: 1,
  })
}

export function useMCPDriveToggle(agentId: string) {
  const queryClient = useQueryClient()
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: ({ layer, name, enabled, force }: MCPDriveToggleVars) =>
      api.mcpToggle(agentId, layer, name, enabled, force),
    onSuccess: (data, variables) => {
      invalidateAgentMCPInventory(queryClient, agentId)
      const written = (data.layer === 'auto' ? variables.layer : data.layer) as MCPQueryLayer
      queryClient.invalidateQueries({ queryKey: mcpRawFileQueryKey(agentId, written) })
    },
  })
}

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

export function useMCPValidate(agentId: string) {
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: ({ layer, content }: MCPValidateVars) =>
      api.mcpValidate(agentId, { layer, content }),
  })
}

export function useMCPAuthStart(agentId: string) {
  const { api } = useAuthContext()
  return useMutation({
    mutationFn: (name: string) => api.mcpAuthStart(agentId, name),
  })
}

export function useMCPAuthFlow(agentId: string, flowId: string | null) {
  const { api } = useAuthContext()
  const queryClient = useQueryClient()
  const pageVisible = usePageVisible()
  const invalidatedFlowRef = useRef<string | null>(null)

  const query = useQuery({
    queryKey: mcpAuthStatusQueryKey(agentId, flowId),
    queryFn: () => api.mcpAuthStatus(agentId, flowId as string),
    enabled: flowId !== null,
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
