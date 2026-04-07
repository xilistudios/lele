import { type ChangeEvent, type FormEvent, type KeyboardEvent, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AttachmentInput } from './AttachmentInput'
import { SearchableSelect } from './SearchableSelect'

type SelectOption = { value: string; label: string }

type Props = {
  placeholder?: string
  disabled?: boolean
  canCancel: boolean
  attachments: string[]
  selectedModel: string
  availableModels: SelectOption[]
  groupedModels?: { label: string; options: SelectOption[] }[]
  selectedAgent: string
  agents: SelectOption[]
  hasConversation: boolean
  onSend: (content: string, attachments: string[]) => void
  onCancel: () => void
  onUploadAttachments: (files: File[]) => Promise<string[]>
  onAttachmentsChange: (attachments: string[]) => void
  onSelectModel: (model: string) => void
  onSelectAgent: (agentId: string) => void
}

export function ChatComposer({
  placeholder,
  disabled = false,
  canCancel,
  attachments,
  selectedModel,
  availableModels,
  groupedModels,
  selectedAgent,
  agents,
  hasConversation,
  onSend,
  onCancel,
  onUploadAttachments,
  onAttachmentsChange,
  onSelectModel,
  onSelectAgent,
}: Props) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    const content = draft.trim()
    if (!content && attachments.length === 0) return

    onSend(content, attachments)
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

  return (
    <form onSubmit={submit}>
      {attachments.length > 0 && (
        <div className="mb-3 flex flex-wrap gap-2">
          {attachments.map((attachment) => (
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
          placeholder={placeholder ?? t('chat.messagePlaceholder')}
          value={draft}
          onChange={handleTextareaChange}
          onKeyDown={handleKeyDown}
          disabled={disabled}
          rows={1}
        />
        <div className="flex items-center justify-between px-3 pb-2 pt-1">
          <div className="flex items-center gap-3">
            <AttachmentInput onUpload={onUploadAttachments} onAttach={onAttachmentsChange} />
            {canCancel && (
              <button
                type="button"
                className="rounded-md border border-[#5a2b2b] px-3 py-1 text-xs text-[#f0b4b4] transition-colors hover:bg-[#351717]"
                onClick={onCancel}
              >
                {t('chat.cancel')}
              </button>
            )}
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
                options={agents}
                placeholder={
                  agents.find((a) => a.value === selectedAgent)?.label ?? t('chat.agent')
                }
                searchAriaLabel={`${t('chat.agent')} buscar`}
                searchPlaceholder={t('chat.agent')}
                value={selectedAgent}
              />
            </div>
          </div>
          <button
            type="submit"
            disabled={disabled}
            aria-label={t('chat.send')}
            className="flex h-7 w-7 items-center justify-center rounded-md bg-white text-black transition-colors hover:bg-[#ddd] disabled:opacity-20"
          >
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
          </button>
        </div>
      </div>
    </form>
  )
}
