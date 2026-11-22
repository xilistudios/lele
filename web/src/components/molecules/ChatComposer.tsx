import {
  type ChangeEvent,
  type FormEvent,
  type KeyboardEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { useSlashCommands } from '../../hooks/useSlashCommands'
import { getModeTheme } from '../../lib/modeTheme'
import type { SlashCommandInfo } from '../../lib/types'
import { endpoints } from '../../services/http/endpoints'
import { IconButton } from '../atoms/IconButton'
import { CloseIcon, FolderIcon, PlusIcon } from '../atoms/Icons'
import { StreamMetricsChip } from '../atoms/StreamMetricsChip'
import { FolderPickerModal } from '../organisms/FolderPickerModal'
import { AttachmentInput } from './AttachmentInput'
import { QueuedMessages } from './QueuedMessages'
import { SearchableSelect } from './SearchableSelect'
import { SlashCommandMenu } from './SlashCommandMenu'
import { completeDraft, filterCommands, isPaletteTrigger } from './commandPalette'

const IMAGE_EXTENSIONS = new Set(['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.svg'])

function isImageByExtension(name: string): boolean {
  const ext = name.toLowerCase().split('.').pop()
  return ext ? IMAGE_EXTENSIONS.has(`.${ext}`) : false
}

function buildFileUrl(apiUrl: string, path: string): string {
  const base = apiUrl.replace(/\/$/, '')
  return `${base}${endpoints.files.view}?path=${encodeURIComponent(path)}`
}

function FolderChip({
  folder,
  onClear,
  className,
}: {
  folder: string
  onClear: () => void
  className?: string
}) {
  const { t } = useTranslation()
  return (
    <span
      title={folder}
      data-testid="folder-chip"
      className={`flex max-w-[180px] items-center gap-1 rounded-full border border-accent-primary/30 bg-accent-primary/10 px-2 py-0.5 text-accent-primary ${className ?? ''}`}
    >
      <FolderIcon size={11} className="flex-shrink-0" />
      <span className="truncate text-2xs font-medium">
        {folder.split('/').filter(Boolean).pop() || folder}
      </span>
      <button
        type="button"
        onClick={onClear}
        title={t('chat.removeFolder')}
        aria-label={t('chat.removeFolder')}
        className="flex h-3.5 w-3.5 flex-shrink-0 items-center justify-center rounded-full hover:bg-accent-primary/25 transition-colors"
      >
        <CloseIcon size={8} />
      </button>
    </span>
  )
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
    sessionFolder,
    onSelectFolder,
    onClearFolder,
    chatMode,
    sendTyping,
    currentSessionKey,
  } = useAppLogicContext()
  const { apiUrl, api } = useAuthContext()

  const [draft, setDraft] = useState('')
  const [folderPickerOpen, setFolderPickerOpen] = useState(false)
  const [queueFullHint, setQueueFullHint] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const lastTypingSentRef = useRef(0)
  const inFlightRef = useRef(0)
  const lastSubmittedContentRef = useRef('')

  const { commands } = useSlashCommands(api, currentAgent?.id)
  const [paletteIdx, setPaletteIdx] = useState(0)
  const [paletteDismissed, setPaletteDismissed] = useState(false)

  const paletteTriggered = isPaletteTrigger(draft)
  const paletteItems = useMemo(() => filterCommands(commands, draft), [commands, draft])
  const paletteOpen = paletteTriggered && !paletteDismissed && paletteItems.length > 0
  const activePaletteIdx = Math.min(paletteIdx, Math.max(paletteItems.length - 1, 0))

  const selectCommand = (command: SlashCommandInfo) => {
    setDraft(completeDraft(draft, command))
    setPaletteIdx(0)
    setPaletteDismissed(false)
    textareaRef.current?.focus()
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: currentSessionKey is the trigger, not a read — the effect must run exactly when the session changes.
  useEffect(() => {
    setDraft('')
    setPaletteIdx(0)
    setPaletteDismissed(false)
    setQueueFullHint(false)
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }, [currentSessionKey])

  const submit = async (e?: FormEvent) => {
    e?.preventDefault()
    const content = draft.trim()
    if (!content && pendingAttachments.length === 0) return
    if (inFlightRef.current > 0 && content === lastSubmittedContentRef.current) return
    inFlightRef.current += 1
    lastSubmittedContentRef.current = content

    try {
      const accepted = await onSend(content, pendingAttachments)
      if (accepted === false) {
        setQueueFullHint(true)
        return
      }
      setQueueFullHint(false)
      setDraft('')
      if (textareaRef.current) {
        textareaRef.current.style.height = 'auto'
      }
    } finally {
      inFlightRef.current -= 1
    }
  }

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return

    if (paletteOpen) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setPaletteIdx((idx) => (idx + 1) % paletteItems.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setPaletteIdx((idx) => (idx - 1 + paletteItems.length) % paletteItems.length)
        return
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
        e.preventDefault()
        const command = paletteItems[activePaletteIdx]
        if (command) selectCommand(command)
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setPaletteDismissed(true)
        return
      }
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
  }

  const handleTextareaChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setDraft(e.target.value)
    setPaletteIdx(0)
    if (isPaletteTrigger(e.target.value)) setPaletteDismissed(false)
    if (queueFullHint && e.target.value.trim()) setQueueFullHint(false)
    e.target.style.height = 'auto'
    const cap = Number.parseFloat(getComputedStyle(e.target).maxHeight)
    e.target.style.height = `${Math.min(e.target.scrollHeight, Number.isFinite(cap) ? cap : 200)}px`

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

  const thinkingEnabled = useMemo(() => {
    if (!selectedModel) return false

    const normalizeModelName = (modelName: string): string => {
      const parts = modelName.split('/')
      return parts.length > 1 ? parts[parts.length - 1] : modelName
    }
    const normalizedSelectedModel = normalizeModelName(selectedModel)

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
      <QueuedMessages />
      {queueFullHint && (
        <p className="mb-2 px-1 text-xs text-state-error" role="alert">
          {t('chat.queueFull')}
        </p>
      )}
      {pendingAttachments.length > 0 && (
        <div className="mb-3 flex flex-wrap items-end gap-2 max-md:max-h-20 max-md:overflow-y-auto">
          {pendingAttachments.map((attachment) => {
            const isImg = isImageByExtension(attachment)
            if (isImg) {
              const url = buildFileUrl(apiUrl, attachment)
              return (
                <div
                  key={attachment}
                  className="relative group h-16 w-16 overflow-hidden rounded-2xl border border-border bg-background-secondary transition-all hover:border-border shadow-sm"
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
                    className="absolute right-1 top-1 flex h-4 w-4 items-center justify-center rounded-full bg-overlay text-text-on-accent hover:bg-overlay transition-colors"
                    title={t('chat.removeAttachment')}
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
                className="relative group flex items-center h-16 max-w-[240px] min-w-[160px] gap-2 rounded-2xl border border-border bg-background-secondary p-2 transition-all hover:border-border shadow-sm pr-8"
              >
                <div className="h-12 w-10 bg-accent-primary/10 text-accent-primary rounded-md flex flex-col items-center justify-center border border-accent-primary/15 flex-shrink-0 select-none">
                  <svg
                    className="h-4 w-4 text-accent-primary mb-0.5"
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
                  <span className="text-2xs font-bold tracking-wider leading-none">
                    {ext.slice(0, 4)}
                  </span>
                </div>
                <div className="flex flex-col min-w-0">
                  <span className="text-xs font-medium text-text-primary truncate">{filename}</span>
                  <span className="text-2xs text-text-tertiary leading-none mt-1">{ext} File</span>
                </div>
                <button
                  type="button"
                  onClick={() =>
                    onAttachmentsChange(pendingAttachments.filter((a) => a !== attachment))
                  }
                  className="absolute right-1.5 top-1/2 -translate-y-1/2 flex h-5 w-5 items-center justify-center rounded-full text-text-tertiary hover:bg-background-tertiary hover:text-text-primary transition-colors"
                  title={t('chat.removeAttachment')}
                >
                  <CloseIcon size={10} />
                </button>
              </div>
            )
          })}
        </div>
      )}
      {paletteOpen && (
        <div className="mb-2">
          <SlashCommandMenu
            items={paletteItems}
            activeIndex={activePaletteIdx}
            onSelect={selectCommand}
            onHover={setPaletteIdx}
          />
        </div>
      )}
      <div
        data-testid="composer-capsule"
        className={`flex items-center gap-1 rounded-full border bg-background-secondary p-1 shadow-card transition-all duration-fast focus-within:ring-1 focus-within:ring-accent-primary/40 ${composerTheme.border}`}
      >
        <IconButton
          variant="capsule"
          onClick={() => setFolderPickerOpen(true)}
          title={t('chat.addFolder')}
          ariaLabel={t('chat.addFolder')}
          className="h-9 w-9"
        >
          <PlusIcon size={16} />
        </IconButton>
        <div className="flex-shrink-0">
          <AttachmentInput
            variant="capsule"
            onUpload={onUploadAttachments}
            onAttach={(paths) => onAttachmentsChange((prev) => [...prev, ...paths])}
            className="h-9 w-9"
          />
        </div>
        <textarea
          ref={textareaRef}
          className="min-h-[36px] max-h-[200px] min-w-0 flex-1 resize-none bg-transparent px-2 py-[9px] text-sm text-text-primary outline-none placeholder:text-text-tertiary focus-visible:outline-none max-md:max-h-[min(200px,30dvh)]"
          placeholder={t('chat.messagePlaceholder')}
          value={draft}
          onChange={handleTextareaChange}
          onKeyDown={handleKeyDown}
          aria-autocomplete="list"
          aria-controls={paletteOpen ? 'slash-command-menu' : undefined}
          aria-activedescendant={paletteOpen ? `slash-command-${activePaletteIdx}` : undefined}
          disabled={false}
          rows={1}
        />
        <button
          type={canCancel ? 'button' : 'submit'}
          disabled={false}
          aria-label={canCancel ? t('chat.cancel') : t('chat.send')}
          className={`flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full transition-colors ${
            canCancel
              ? 'border border-state-error/30 bg-state-error/10 text-state-error hover:bg-state-error-fill hover:text-text-on-accent'
              : 'bg-accent-primary text-text-on-accent hover:bg-accent-hover'
          }`}
          onClick={canCancel ? onCancel : undefined}
        >
          {canCancel ? (
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <rect x="6" y="6" width="12" height="12" rx="2" />
            </svg>
          ) : (
            <svg
              width="15"
              height="15"
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
      <div data-testid="composer-chips" className="mt-2 flex flex-wrap items-center gap-1.5 px-1">
        {sessionFolder && (
          <FolderChip
            folder={sessionFolder}
            onClear={onClearFolder}
            className="min-w-0 max-w-[200px] max-md:basis-full"
          />
        )}
        <SearchableSelect
          className="min-w-0 flex-1 basis-[132px] max-w-[240px]"
          shape="pill"
          ariaLabel={t('chat.model')}
          buttonLabel={t('chat.model')}
          emptyLabel={t('chat.default')}
          groups={groupedModels}
          onChange={onSelectModel}
          options={groupedModels ? undefined : availableModels}
          placeholder={selectedModel}
          searchAriaLabel={`${t('chat.model')} ${t('chat.search')}`}
          searchPlaceholder={t('chat.model')}
          value={selectedModel}
        />
        {thinkingEnabled && (
          <SearchableSelect
            className="min-w-0 flex-none"
            shape="pill"
            ariaLabel={t('chat.thinking')}
            buttonLabel={t('chat.thinking')}
            direction="up"
            emptyLabel={t('chat.thinkingOff')}
            onChange={onSelectThinkLevel}
            options={thinkOptions}
            placeholder={
              thinkOptions.find((o) => o.value === thinkLevel)?.label ?? t('chat.thinkingOff')
            }
            searchAriaLabel={`${t('chat.thinking')} ${t('chat.search')}`}
            searchPlaceholder={t('chat.thinking')}
            value={thinkLevel}
          />
        )}
        {agentsOptions.length > 1 && (
          <SearchableSelect
            className="min-w-0 flex-none"
            shape="pill"
            ariaLabel={t('chat.agent')}
            buttonLabel={t('chat.agent')}
            disabled={hasConversation}
            emptyLabel={t('chat.agentLocked')}
            onChange={onSelectAgent}
            options={agentsOptions}
            placeholder={
              agentsOptions.find((a) => a.value === selectedAgentId)?.label ?? t('chat.agent')
            }
            searchAriaLabel={`${t('chat.agent')} ${t('chat.search')}`}
            searchPlaceholder={t('chat.agent')}
            value={selectedAgentId}
          />
        )}
        <StreamMetricsChip className="ml-auto pl-1" />
      </div>
      <FolderPickerModal
        open={folderPickerOpen}
        onClose={() => setFolderPickerOpen(false)}
        onSelect={(path) => {
          onSelectFolder(path)
          setFolderPickerOpen(false)
        }}
        currentFolder={sessionFolder || undefined}
      />
    </form>
  )
}
