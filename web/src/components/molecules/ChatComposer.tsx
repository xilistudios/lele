import { type ChangeEvent, type FormEvent, type KeyboardEvent, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { AttachmentInput } from './AttachmentInput'
import { SearchableSelect } from './SearchableSelect'

export function ChatComposer() {
  const { t } = useTranslation()
  const { canCancel, hasConversation, availableModels, groupedModels, selectedModel } =
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

  const agentsOptions = agents.map((agent) => ({ value: agent.id, label: agent.name }))
  const selectedAgentId = currentAgent?.id ?? ''

  return (
    <form onSubmit={submit}>
      {pendingAttachments.length > 0 && (
        <div className="mb-3 flex flex-wrap gap-2">
          {pendingAttachments.map((attachment) => (
            <span
              key={attachment}
              className="rounded-full border border-[#3a3a3a] bg-[#222] px-3 py-1 text-xs text-[#bbb]"
            >
              {attachment.split('/').pop() ?? attachment}
            </span>
          ))}
        </div>
      )}
      <div className="rounded-lg border border-[#3a3a3a] bg-[#222] transition-colors focus-within:border-[#555]">
        <textarea
          ref={textareaRef}
          className="min-h-[44px] max-h-[200px] w-full resize-none bg-transparent px-4 pb-2 pt-3 text-sm text-white outline-none placeholder:text-[#444]"
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
            <div className="flex items-center gap-2 text-[10px] text-[#555]">
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
            style={canCancel ? { backgroundColor: '#351717', border: '1px solid #5a2b2b' } : {}}
            onMouseEnter={(e) => {
              if (canCancel) e.currentTarget.style.backgroundColor = '#4a2020'
            }}
            onMouseLeave={(e) => {
              if (canCancel) e.currentTarget.style.backgroundColor = '#351717'
            }}
          >
            {canCancel ? (
              <svg width="12" height="12" viewBox="0 0 24 24" fill="#f0b4b4" aria-hidden="true">
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
