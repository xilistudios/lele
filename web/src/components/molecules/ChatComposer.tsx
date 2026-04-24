import { type ChangeEvent, type FormEvent, type KeyboardEvent, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { AttachmentInput } from './AttachmentInput'
import { SearchableSelect } from './SearchableSelect'

export function ChatComposer() {
  const { t } = useTranslation()
  const { canCancel, hasConversation, availableModels, groupedModels, selectedModel, thinkLevel } =
    useChatPageContext()
  const {
    currentAgent,
    agents,
    pendingAttachments,
    onSend,
    onCancel,
    onUploadAttachments,
    onAttachmentsChange,
    onSelectAgent,
    onSelectModel,
    onSelectThinkLevel,
  } = useAppLogicContext()

  const [draft, setDraft] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    const content = draft.trim()
    if (!content && pendingAttachments.length === 0) return

    onSend(content, pendingAttachments)
    setDraft('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
  }

  const handleTextareaChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setDraft(e.target.value)
    e.target.style.height = 'auto'
    e.target.style.height = `${Math.min(e.target.scrollHeight, 200)}px`
  }

  const agentsOptions = agents.map((agent) => ({
    value: agent.id,
    label: agent.name,
  }))
  const selectedAgentId = currentAgent?.id ?? ''

  // Check if the currently selected model has reasoning enabled
  // Normalize model names for comparison (with/without provider prefix)
  const normalizeModelName = (modelName: string): string => {
    const parts = modelName.split('/')
    return parts.length > 1 ? parts[parts.length - 1] : modelName
  }
  const normalizedSelectedModel = normalizeModelName(selectedModel)

  const findModelReasoning = (): boolean => {
    // First check grouped models (they have the full provider/model format)
    for (const group of groupedModels ?? []) {
      for (const model of group.options) {
        const normalizedValue = normalizeModelName(model.value)
        if (
          (model.value === selectedModel || normalizedValue === normalizedSelectedModel) &&
          model.reasoning?.enable
        ) {
          return true
        }
      }
    }
    // Then check flat available models
    for (const model of availableModels ?? []) {
      const normalizedValue = normalizeModelName(model.value)
      if (
        (model.value === selectedModel || normalizedValue === normalizedSelectedModel) &&
        model.reasoning?.enable
      ) {
        return true
      }
    }
    return false
  }
  const thinkingEnabled = findModelReasoning()
  const thinkOptions = [
    { value: 'default', label: t('chat.thinkingDefault') },
    { value: 'off', label: t('chat.thinkingOff') },
    { value: 'low', label: t('chat.thinkingLow') },
    { value: 'medium', label: t('chat.thinkingMedium') },
    { value: 'high', label: t('chat.thinkingHigh') },
  ]
  return (
    <form onSubmit={submit}>
      {pendingAttachments.length > 0 && (
        <div className="mb-3 flex flex-wrap gap-2">
          {pendingAttachments.map((attachment) => (
            <span
              key={attachment}
              className="rounded-full border border-border bg-background-secondary px-3 py-1 text-xs text-text-secondary"
            >
              {attachment.split('/').pop() ?? attachment}
            </span>
          ))}
        </div>
      )}
      <div className="rounded-lg border border-border bg-background-secondary transition-colors focus-within:border-border-light">
        <textarea
          ref={textareaRef}
          className="min-h-[44px] max-h-[200px] w-full resize-none bg-transparent px-4 pb-2 pt-3 text-sm text-text-primary outline-none placeholder:text-text-tertiary"
          placeholder={t('chat.messagePlaceholder')}
          value={draft}
          onChange={handleTextareaChange}
          onKeyDown={handleKeyDown}
          disabled={false}
          rows={1}
        />
        <div className="flex items-center justify-between px-3 pb-2 pt-1">
          <div className="flex items-center gap-3">
            <AttachmentInput onUpload={onUploadAttachments} onAttach={onAttachmentsChange} />
            <div className="flex items-center gap-2 text-[10px] text-text-tertiary">
              <SearchableSelect
                ariaLabel={t('chat.model')}
                buttonLabel={t('chat.model')}
                emptyLabel={t('chat.default')}
                groups={groupedModels}
                onChange={onSelectModel}
                options={groupedModels ? undefined : availableModels}
                placeholder={selectedModel}
                searchAriaLabel={`${t('chat.model')} buscar`}
                searchPlaceholder={t('chat.model')}
                value={selectedModel}
              />
              {thinkingEnabled && (
                <SearchableSelect
                  ariaLabel={t('chat.thinking')}
                  buttonLabel={t('chat.thinking')}
                  direction="up"
                  emptyLabel={t('chat.thinkingOff')}
                  onChange={onSelectThinkLevel}
                  options={thinkOptions}
                  placeholder={
                    thinkOptions.find((o) => o.value === thinkLevel)?.label ?? t('chat.thinkingOff')
                  }
                  searchAriaLabel={`${t('chat.thinking')} buscar`}
                  searchPlaceholder={t('chat.thinking')}
                  value={thinkLevel}
                />
              )}
              <SearchableSelect
                ariaLabel={t('chat.agent')}
                buttonLabel={t('chat.agent')}
                disabled={hasConversation}
                emptyLabel={t('chat.agentLocked')}
                onChange={onSelectAgent}
                options={agentsOptions}
                placeholder={
                  agentsOptions.find((a) => a.value === selectedAgentId)?.label ?? t('chat.agent')
                }
                searchAriaLabel={`${t('chat.agent')} buscar`}
                searchPlaceholder={t('chat.agent')}
                value={selectedAgentId}
              />
            </div>
          </div>
          <button
            type={canCancel ? 'button' : 'submit'}
            disabled={false}
            aria-label={canCancel ? t('chat.cancel') : t('chat.send')}
            className="flex h-7 w-7 items-center justify-center rounded-md transition-colors disabled:opacity-20"
            onClick={canCancel ? onCancel : undefined}
            style={
              canCancel
                ? {
                    backgroundColor: 'rgba(255, 69, 58, 0.15)',
                    border: '1px solid var(--border)',
                  }
                : {}
            }
            onMouseEnter={(e) => {
              if (canCancel) e.currentTarget.style.backgroundColor = 'rgba(255, 69, 58, 0.25)'
            }}
            onMouseLeave={(e) => {
              if (canCancel) e.currentTarget.style.backgroundColor = 'rgba(255, 69, 58, 0.15)'
            }}
          >
            {canCancel ? (
              <svg
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="currentColor"
                className="text-state-error"
                aria-hidden="true"
              >
                <rect x="6" y="6" width="12" height="12" rx="2" />
              </svg>
            ) : (
              <svg
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.5"
                aria-hidden="true"
                className="text-black hover:text-black"
              >
                <line x1="12" y1="19" x2="12" y2="5" />
                <polyline points="5 12 12 5 19 12" />
              </svg>
            )}
          </button>
        </div>
      </div>
    </form>
  )
}
