import { useQuery } from '@tanstack/react-query'
import type { ApiClient } from '../services/http/client'
import { agentCatalogQueryKey } from './useAgentSkills'

/**
 * Per-agent skill (and tool) catalog for the `/skills` page's "By agent" view.
 *
 * The key is the SAME one the agent config page uses
 * (`useAgentSkills.ts` exports `agentCatalogQueryKey`), so the two surfaces
 * share one cache entry: install from `/agents/coder/skills`, navigate to
 * `/skills?view=agent&agent=coder`, and the list is already fresh — and vice
 * versa. `QueryClientProvider` is app-wide, so no provider work is needed; a
 * caller that only needs the data (no mutations) does not have to reach for
 * `useAgentSkills`.
 *
 * `agentId` may be `null` while the agent list is still loading or when the
 * user has no agents: the query is then disabled and the key falls back to
 * `''`, which no mounted query ever shares (the enabled guard means it never
 * fetches).
 */
export function useAgentCatalog(api: ApiClient, agentId: string | null) {
  return useQuery({
    queryKey: agentCatalogQueryKey(agentId ?? ''),
    queryFn: () => api.getAgentCatalog(agentId as string),
    enabled: !!agentId,
    staleTime: 10_000,
    retry: 1,
  })
}
