import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { ChatSession } from '../lib/types'
import { useAppLogicContext } from './AppLogicContext'

export type SelectOption = { value: string; label: string }

type GroupedModels = { label: string; options: SelectOption[] }[] | undefined

type ChatPageContextValue = {
  // Computed state
  canCancel: boolean
  hasConversation: boolean
  availableModels: SelectOption[]
  groupedModels: GroupedModels
  selectedModel: string
  currentSession: ChatSession | null
}

const ChatPageContext = createContext<ChatPageContextValue | null>(null)

export function ChatPageProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const {
    messages,
    isStreaming,
    toolStatus,
    modelState,
    currentAgent,
    sessions,
    currentSessionKey,
  } = useAppLogicContext()

  const hasConversation = messages.length > 0
  const canCancel = isStreaming || Boolean(toolStatus)
  const currentSession = sessions.find((s) => s.key === currentSessionKey) ?? sessions[0] ?? null

  const availableModels = useMemo(() => {
    return modelState.available.length > 0
      ? modelState.available.map((model) => ({ value: model, label: model }))
      : currentAgent?.model
        ? [{ value: currentAgent.model, label: currentAgent.model }]
        : [{ value: t('chat.default'), label: t('chat.default') }]
  }, [modelState.available, currentAgent?.model, t])

  const groupedModels: GroupedModels = useMemo(() => {
    const groups = modelState.groups.filter((group) => group.models.length > 0)
    if (groups.length === 0) return undefined

    return groups.map((group) => ({
      label: group.provider,
      options: group.models,
    }))
  }, [modelState.groups])

  const selectedModel = modelState.current || availableModels[0]?.value || t('chat.default')

  const value: ChatPageContextValue = {
    canCancel,
    hasConversation,
    availableModels,
    groupedModels,
    selectedModel,
    currentSession,
  }

  return <ChatPageContext.Provider value={value}>{children}</ChatPageContext.Provider>
}

export function useChatPageContext(): ChatPageContextValue {
  const context = useContext(ChatPageContext)
  if (!context) {
    throw new Error('useChatPageContext must be used within a ChatPageProvider')
  }
  return context
}
