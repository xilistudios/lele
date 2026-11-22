import { type QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  AgentCommandDetail,
  AgentCommandUpdateRequest,
  AgentCommandWriteRequest,
} from '../lib/types'
import type { ApiClient } from '../services/http/client'

export const agentCommandsQueryKey = (agentId: string) => ['agentCommands', agentId] as const

export const agentCommandDetailQueryKey = (agentId: string, name: string | null) =>
  ['agentCommand', agentId, name ?? ''] as const

export function invalidateAgentCommands(queryClient: QueryClient, agentId: string) {
  queryClient.invalidateQueries({ queryKey: agentCommandsQueryKey(agentId) })
}

type Options = {
  agentId: string
}

export function useAgentCommands(api: ApiClient, { agentId }: Options) {
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: agentCommandsQueryKey(agentId),
    queryFn: () => api.agentCommands(agentId),
    staleTime: 10_000,
    retry: 1,
  })

  const create = useMutation({
    mutationFn: (body: AgentCommandWriteRequest) => api.agentCommandCreate(agentId, body),
    onSettled: () => invalidateAgentCommands(queryClient, agentId),
  })

  const update = useMutation({
    mutationFn: ({ name, content }: { name: string } & AgentCommandUpdateRequest) =>
      api.agentCommandUpdate(agentId, name, { content }),
    onSettled: () => invalidateAgentCommands(queryClient, agentId),
  })

  const remove = useMutation({
    mutationFn: (name: string) => api.agentCommandRemove(agentId, name),
    onSettled: () => invalidateAgentCommands(queryClient, agentId),
  })

  return {
    ...query,
    commands: query.data?.commands ?? [],
    builtin: query.data?.builtin ?? [],
    create,
    update,
    remove,
  }
}

export function useAgentCommandDetail(
  api: ApiClient,
  agentId: string,
  name: string | null,
): { data: AgentCommandDetail | undefined; isLoading: boolean; isError: boolean } {
  const query = useQuery({
    queryKey: agentCommandDetailQueryKey(agentId, name),
    queryFn: () => api.agentCommand(agentId, name as string),
    enabled: name !== null,
    staleTime: 10_000,
    retry: 1,
  })
  return { data: query.data, isLoading: query.isLoading, isError: query.isError }
}
