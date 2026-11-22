import { useQuery } from '@tanstack/react-query'
import type { ApiClient } from '../services/http/client'
import { agentCatalogQueryKey } from './useAgentSkills'

export function useAgentCatalog(api: ApiClient, agentId: string | null) {
  return useQuery({
    queryKey: agentCatalogQueryKey(agentId ?? ''),
    queryFn: () => api.getAgentCatalog(agentId as string),
    enabled: !!agentId,
    staleTime: 10_000,
    retry: 1,
  })
}
