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

  // Last synthetic (not-yet-persisted) session handed out by the memo below.
  const syntheticSessionRef = useRef<ChatSession | null>(null)
  const currentSession = useMemo<ChatSession | null>(() => {
    if (!currentSessionKey) return null

    // Persisted sessions keep their own (stable) identity in `sessions`.
    const session = sessions.find((s) => s.key === currentSessionKey)
    if (session) return session

    // Not persisted yet (e.g. a brand-new session still streaming its first
    // reply): the name is derived from the session messages. `messages` gets a
    // fresh identity on every typewriter tick, so without the ref below this
    // object — and therefore the whole context value — would change ~31×/s and
    // re-render every consumer (ChatHeader, composer, …) for nothing.
    const sessionMessages = messages
      .filter((message) => message.sessionKey === currentSessionKey && message.role !== 'tool')
      .map((message) => message.content)

    const name = deriveSessionNameFromMessages(currentSessionKey, sessionMessages)
    const previous = syntheticSessionRef.current
    // The reuse is only sound while EVERY message-derived field of the object
    // is part of this comparison — add any new derived field here as well (the
    // ref write below depends on this guard, see there).
    if (previous && previous.key === currentSessionKey && previous.name === name) {
      return previous
    }

    // `key` or `name` changed (session switch, first user message, rename):
    // hand out a new object so the header does update.
    const synthesized: ChatSession = {
      key: currentSessionKey,
      name,
      created: new Date(0).toISOString(),
      updated: new Date(0).toISOString(),
    }
    // Writing this cache ref DURING render is deliberate and safe only because
    // of the guard above. The ref holds nothing but the last synthesized object,
    // and that guard compares every derived field, so a concurrent render React
    // discards can only have published an object that is field-for-field equal
    // to the one the next render derives — the write is not rolled back with a
    // discarded render, which is exactly why a NEW derived field must be added
    // to the guard: an unguarded field would then freeze at the discarded value
    // while the memo kept handing it out.
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
