import { type ReactNode, createContext, useContext, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type { ChatSession, ReasoningConfig } from '../lib/types'
import { useAppLogicContext, useAppStreamingContext } from './AppLogicContext'

export type SelectOption = { value: string; label: string; reasoning?: ReasoningConfig }

type GroupedModels = { label: string; options: SelectOption[] }[] | undefined

const deriveSessionNameFromMessages = (currentSessionKey: string, content: string[]) => {
  const firstLine = content
    .map((entry) => entry.trim())
    .find((entry) => entry.length > 0)
    ?.split('\n')[0]
    ?.trim()

  if (!firstLine) return currentSessionKey
  return firstLine.length > 80 ? `${firstLine.slice(0, 77)}...` : firstLine
}

type ChatPageContextValue = {
  canCancel: boolean
  hasConversation: boolean
  availableModels: SelectOption[]
  groupedModels: GroupedModels
  selectedModel: string
  thinkLevel: string
  currentSession: ChatSession | null
  parentSession: ChatSession | null
}

const ChatPageContext = createContext<ChatPageContextValue | null>(null)

export function ChatPageProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const {
    modelState,
    thinkLevel,
    currentAgent,
    sessions,
    currentSessionKey,
    parentSessionKey,
    isProcessing,
  } = useAppLogicContext()
  const { messages, toolStatus } = useAppStreamingContext()

  const hasConversation = (messages?.length ?? 0) > 0
  const canCancel = isProcessing || Boolean(toolStatus)

  const syntheticSessionRef = useRef<ChatSession | null>(null)
  const currentSession = useMemo<ChatSession | null>(() => {
    if (!currentSessionKey) return null

    const session = sessions.find((s) => s.key === currentSessionKey)
    if (session) return session

    const sessionMessages = messages
      .filter((message) => message.sessionKey === currentSessionKey && message.role !== 'tool')
      .map((message) => message.content)

    const name = deriveSessionNameFromMessages(currentSessionKey, sessionMessages)
    const previous = syntheticSessionRef.current
    if (previous && previous.key === currentSessionKey && previous.name === name) {
      return previous
    }

    const synthesized: ChatSession = {
      key: currentSessionKey,
      name,
      created: new Date(0).toISOString(),
      updated: new Date(0).toISOString(),
    }
    syntheticSessionRef.current = synthesized
    return synthesized
  }, [currentSessionKey, messages, sessions])

  const parentSession = useMemo<ChatSession | null>(() => {
    if (!parentSessionKey) return null
    return sessions.find((s) => s.key === parentSessionKey) ?? null
  }, [parentSessionKey, sessions])

  const availableModels = useMemo(() => {
    const available = modelState.available ?? []
    return available.length > 0
      ? available.map((model) => ({ value: model, label: model }))
      : currentAgent?.model
        ? [{ value: currentAgent.model, label: currentAgent.model }]
        : [{ value: t('chat.default'), label: t('chat.default') }]
  }, [modelState.available, currentAgent?.model, t])

  const groupedModels: GroupedModels = useMemo(() => {
    const groups = (modelState.groups ?? []).filter((group) => (group.models?.length ?? 0) > 0)
    if (groups.length === 0) return undefined

    return groups.map((group) => ({
      label: group.provider,
      options: group.models,
    }))
  }, [modelState.groups])

  const selectedModel = modelState.current || availableModels[0]?.value || t('chat.default')

  const value: ChatPageContextValue = useMemo(
    () => ({
      canCancel,
      hasConversation,
      availableModels,
      groupedModels,
      selectedModel,
      thinkLevel,
      currentSession,
      parentSession,
    }),
    [
      canCancel,
      hasConversation,
      availableModels,
      groupedModels,
      selectedModel,
      thinkLevel,
      currentSession,
      parentSession,
    ],
  )

  return <ChatPageContext.Provider value={value}>{children}</ChatPageContext.Provider>
}

export function useChatPageContext(): ChatPageContextValue {
  const context = useContext(ChatPageContext)
  if (!context) {
    throw new Error('useChatPageContext must be used within a ChatPageProvider')
  }
  return context
}
