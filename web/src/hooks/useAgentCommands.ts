import { type QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  AgentCommandDetail,
  AgentCommandUpdateRequest,
  AgentCommandWriteRequest,
} from '../lib/types'
import type { ApiClient } from '../services/http/client'

/**
 * Commands of ONE agent (per-agent Commands tab, brief §6 / plan F9).
 *
 * Same shape the Skills tab settled on: reads go through react-query against
 * the per-agent endpoint (one source of truth — the server flattens the four
 * harness discovery levels and decides `deletable`/`shadowed_by`), and every
 * mutation invalidates the list query instead of patching it optimistically.
 * A command is a markdown FILE on disk; the server may merge or shadow it in
 * ways the client cannot recompute, so cached rows must never be edited here.
 *
 * `staleTime`/`retry` mirror AgentSkillsSection's catalog query: fresh enough
 * for tab switches, one retry for a transient network failure.
 */

/** Query key of the per-agent command list. Exported for tests/invalidation. */
export const agentCommandsQueryKey = (agentId: string) => ['agentCommands', agentId] as const

/** Query key of one command's raw markdown (editor load). */
export const agentCommandDetailQueryKey = (agentId: string, name: string | null) =>
  ['agentCommand', agentId, name ?? ''] as const

/** Invalidate the command list of one agent after a mutation. */
export function invalidateAgentCommands(queryClient: QueryClient, agentId: string) {
  queryClient.invalidateQueries({ queryKey: agentCommandsQueryKey(agentId) })
}

type Options = {
  /** Agent whose commands are read and mutated. */
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

  /** Create writes the FULL markdown body; `scope` defaults server-side to workspace. */
  const create = useMutation({
    mutationFn: (body: AgentCommandWriteRequest) => api.agentCommandCreate(agentId, body),
    onSettled: () => invalidateAgentCommands(queryClient, agentId),
  })

  /** Update replaces the file content of the command that wins precedence. */
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
    /** Always an array: the backend guarantees [] over null, but a failed query has no data. */
    commands: query.data?.commands ?? [],
    builtin: query.data?.builtin ?? [],
    create,
    update,
    remove,
  }
}

/**
 * Raw markdown of one command for the editor. Pass `name = null` while the
 * dialog is closed: the query stays disabled and no request leaves the page.
 * The server answers 403 `not_editable` for config/directory-level commands.
 */
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
