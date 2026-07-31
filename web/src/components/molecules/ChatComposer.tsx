import {
  type ChangeEvent,
  type FormEvent,
  type KeyboardEvent,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { getModeTheme } from '../../lib/modeTheme'
import { CloseIcon } from '../atoms/Icons'
import { AttachmentInput } from './AttachmentInput'
import { SearchableSelect } from './SearchableSelect'

const IMAGE_EXTENSIONS = new Set(['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.svg'])

function isImageByExtension(name: string): boolean {
  const ext = name.toLowerCase().split('.').pop()
  return ext ? IMAGE_EXTENSIONS.has(`.${ext}`) : false
}

function buildFileUrl(apiUrl: string, path: string): string {
  const base = apiUrl.replace(/\/$/, '')
  return `${base}/api/v1/files/view?path=${encodeURIComponent(path)}`
}

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
    chatMode,
    sendTyping,
    currentSessionKey,
  } = useAppLogicContext()
  const { apiUrl } = useAuthContext()

  const [draft, setDraft] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const lastTypingSentRef = useRef(0)

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

    const now = Date.now()
    if (now - lastTypingSentRef.current > 2000 && currentSessionKey) {
      lastTypingSentRef.current = now
      sendTyping(currentSessionKey)
    }
  }

  const agentsOptions = agents.map((agent) => ({
    value: agent.id,
    label: agent.name,
  }))
  const selectedAgentId = currentAgent?.id ?? ''

  const composerTheme = getModeTheme(chatMode)

  // Check if the currently selected model has reasoning enabled
  const thinkingEnabled = useMemo(() => {
    if (!selectedModel) return false

    // Normalize model names for comparison (with/without provider prefix)
    const normalizeModelName = (modelName: string): string => {
      const parts = modelName.split('/')
      return parts.length > 1 ? parts[parts.length - 1] : modelName
    }
    const normalizedSelectedModel = normalizeModelName(selectedModel)

    // Build a lookup set of models with reasoning enabled.
    // Only store models that actually have reasoning — flat availableModels
    // lack reasoning metadata, so they must not overwrite grouped model data.
    const modelsWithReasoning = new Set<string>()

    for (const group of groupedModels ?? []) {
      for (const model of group.options) {
        if (model.reasoning?.enable) {
          modelsWithReasoning.add(model.value)
          modelsWithReasoning.add(normalizeModelName(model.value))
        }
      }
    }
    for (const model of availableModels ?? []) {
      if (model.reasoning?.enable) {
        modelsWithReasoning.add(model.value)
        modelsWithReasoning.add(normalizeModelName(model.value))
      }
    }

    return (
      modelsWithReasoning.has(selectedModel) || modelsWithReasoning.has(normalizedSelectedModel)
    )
  }, [selectedModel, groupedModels, availableModels])
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
        <div className="mb-3 flex flex-wrap items-end gap-2">
          {pendingAttachments.map((attachment) => {
            const isImg = isImageByExtension(attachment)
            if (isImg) {
              const url = buildFileUrl(apiUrl, attachment)
              return (
                <div
                  key={attachment}
                  className="relative group h-16 w-16 overflow-hidden rounded-lg border border-border bg-background-secondary transition-all hover:border-border-light shadow-sm"
                >
                  <img
                    src={url}
                    alt={attachment.split('/').pop() ?? 'attachment'}
                    className="h-full w-full object-cover"
                  />
                  <div className="absolute inset-0 bg-black/20 opacity-0 group-hover:opacity-100 transition-opacity" />
                  <button
                    type="button"
                    onClick={() =>
                      onAttachmentsChange(pendingAttachments.filter((a) => a !== attachment))
                    }
                    className="absolute right-1 top-1 flex h-4 w-4 items-center justify-center rounded-full bg-black/60 text-white hover:bg-black/80 transition-colors"
                    title={t('chat.removeAttachment') || 'Remove attachment'}
                  >
                    <CloseIcon size={10} />
                  </button>
                </div>
              )
            }

            const filename = attachment.split('/').pop() ?? attachment
            const ext = filename.split('.').pop()?.toUpperCase() ?? 'FILE'
            return (
              <div
                key={attachment}
                className="relative group flex items-center h-16 max-w-[240px] min-w-[160px] gap-2 rounded-lg border border-border bg-background-secondary p-2 transition-all hover:border-border-light shadow-sm pr-8"
              >
                <div className="h-12 w-10 bg-cta-primary/10 text-cta-primary rounded-md flex flex-col items-center justify-center border border-cta-primary/15 flex-shrink-0 select-none">
                  <svg
                    className="h-4 w-4 text-cta-primary mb-0.5"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    strokeWidth="2.5"
                    aria-hidden="true"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z"
                    />
                  </svg>
                  <span className="text-[8px] font-bold tracking-wider leading-none">
                    {ext.slice(0, 4)}
                  </span>
                </div>
                <div className="flex flex-col min-w-0">
                  <span className="text-xs font-medium text-text-primary truncate">{filename}</span>
                  <span className="text-[10px] text-text-tertiary leading-none mt-1">
                    {ext} File
                  </span>
                </div>
                <button
                  type="button"
                  onClick={() =>
                    onAttachmentsChange(pendingAttachments.filter((a) => a !== attachment))
                  }
                  className="absolute right-1.5 top-1/2 -translate-y-1/2 flex h-5 w-5 items-center justify-center rounded-full text-text-tertiary hover:bg-background-tertiary hover:text-text-primary transition-colors"
                  title={t('chat.removeAttachment') || 'Remove attachment'}
                >
                  <CloseIcon size={10} />
                </button>
              </div>
            )
          })}
        </div>
      )}
      <div className="rounded-lg border border-border bg-background-secondary transition-colors focus-within:border-border-light">
        <div className={`h-0.5 w-full rounded-t-lg ${composerTheme.accentBar}`} />
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
        <div className="flex items-center justify-between px-3 pb-2 pt-1 gap-2">
          <div className="flex items-center gap-2 sm:gap-3 min-w-0 flex-1">
            <AttachmentInput
              onUpload={onUploadAttachments}
              onAttach={(paths) => onAttachmentsChange((prev) => [...prev, ...paths])}
            />
            <div className="flex flex-wrap items-center gap-1.5 sm:gap-2 text-[10px] text-text-tertiary min-w-0 flex-1">
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
              {agentsOptions.length > 1 && (
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
              )}
            </div>
          </div>
          <button
            type={canCancel ? 'button' : 'submit'}
            disabled={false}
            aria-label={canCancel ? t('chat.cancel') : t('chat.send')}
            className={`flex h-7 w-7 items-center justify-center rounded-md transition-colors ${
              canCancel
                ? 'bg-state-error-light text-state-error hover:bg-state-error hover:text-text-on-accent border border-state-error/30'
                : 'bg-cta-primary text-text-on-accent hover:bg-cta-hover'
            }`}
            onClick={canCancel ? onCancel : undefined}
          >
            {canCancel ? (
              <svg
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="currentColor"
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
